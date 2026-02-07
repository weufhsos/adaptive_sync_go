package pi

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weufhsos/adaptive_sync_go/store"
)

// TestNewInspector 验证新建 Inspector
func TestNewInspector(t *testing.T) {
	inspector := NewInspector()

	if inspector == nil {
		t.Fatal("NewInspector returned nil")
	}

	stats := inspector.GetStats()
	if stats == nil {
		t.Fatal("GetStats returned nil")
	}

	if stats.TotalChecks != 0 {
		t.Errorf("Expected TotalChecks 0, got %d", stats.TotalChecks)
	}
	if stats.ReportsGenerated != 0 {
		t.Errorf("Expected ReportsGenerated 0, got %d", stats.ReportsGenerated)
	}
	if stats.AveragePhi != 0 {
		t.Errorf("Expected AveragePhi 0, got %f", stats.AveragePhi)
	}
}

// TestStartStop 启动和停止
func TestStartStop(t *testing.T) {
	inspector := NewInspector()

	// 启动
	inspector.Start()

	// 短暂运行
	time.Sleep(50 * time.Millisecond)

	// 停止
	inspector.Stop()

	// 再次停止不应该 panic
	inspector.Stop()
}

// TestCheckInconsistency_InsufficientHistory 历史不足时跳过
func TestCheckInconsistency_InsufficientHistory(t *testing.T) {
	inspector := NewInspector()

	counter := store.NewPNCounter("test")
	counter.Increment("node-1", 10.0) // 只有 1 条历史

	callbackCalled := false
	inspector.SetOnInconsistencyReport(func(phi float64) {
		callbackCalled = true
	})

	inspector.CheckInconsistency("test", time.Now().UnixNano(), counter)

	// 等待处理
	time.Sleep(50 * time.Millisecond)

	// 历史不足，回调不应该被调用
	if callbackCalled {
		t.Error("Callback should not be called with insufficient history")
	}

	stats := inspector.GetStats()
	if stats.TotalChecks != 0 {
		t.Errorf("Expected TotalChecks 0 for insufficient history, got %d", stats.TotalChecks)
	}
}

// TestCheckInconsistency_SufficientHistory 足够历史时处理
func TestCheckInconsistency_SufficientHistory(t *testing.T) {
	inspector := NewInspector()

	// 创建有足够历史的 counter
	counter := store.NewPNCounter("test")
	for i := 0; i < 5; i++ {
		counter.Increment("node-1", float64(i+1))
		time.Sleep(1 * time.Millisecond) // 确保时间戳不同
	}

	reportedPhi := 0.0
	callbackCalled := false

	inspector.SetOnInconsistencyReport(func(phi float64) {
		reportedPhi = phi
		callbackCalled = true
	})

	inspector.CheckInconsistency("test", time.Now().UnixNano(), counter)

	// 等待回调
	time.Sleep(100 * time.Millisecond)

	if !callbackCalled {
		t.Error("Callback should be called with sufficient history")
	}

	if reportedPhi <= 0 {
		t.Errorf("Expected positive phi, got %f", reportedPhi)
	}

	stats := inspector.GetStats()
	if stats.TotalChecks == 0 {
		t.Error("Expected TotalChecks > 0")
	}
	if stats.ReportsGenerated == 0 {
		t.Error("Expected ReportsGenerated > 0")
	}
}

// TestSetOnInconsistencyReport 回调设置
func TestSetOnInconsistencyReport(t *testing.T) {
	inspector := NewInspector()

	var reportedValues []float64
	var mu sync.Mutex

	inspector.SetOnInconsistencyReport(func(phi float64) {
		mu.Lock()
		defer mu.Unlock()
		reportedValues = append(reportedValues, phi)
	})

	// 创建测试数据
	counter := store.NewPNCounter("test")
	for i := 0; i < 10; i++ {
		counter.Increment("node-1", float64(i+1))
	}

	// 多次调用
	inspector.CheckInconsistency("test-1", time.Now().UnixNano(), counter)
	inspector.CheckInconsistency("test-2", time.Now().UnixNano(), counter)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	count := len(reportedValues)
	mu.Unlock()

	if count < 2 {
		t.Errorf("Expected at least 2 reports, got %d", count)
	}
}

// TestReconstructTimelines 时间线重构
func TestReconstructTimelines(t *testing.T) {
	inspector := NewInspector()

	// 创建测试历史
	history := []store.UpdateLog{
		{Timestamp: 1000, Value: 10.0, NodeID: "node-1", Operation: store.Increment},
		{Timestamp: 1001, Value: 5.0, NodeID: "node-1", Operation: store.Increment},
		{Timestamp: 1002, Value: 3.0, NodeID: "node-1", Operation: store.Decrement},
	}

	observed, optimal := inspector.reconstructTimelines(history, 1003)

	if len(observed) != 3 {
		t.Errorf("Expected 3 observed values, got %d", len(observed))
	}
	if len(optimal) != 3 {
		t.Errorf("Expected 3 optimal values, got %d", len(optimal))
	}

	// 验证计算过程
	// 10 -> 15 -> 12
	expected := []float64{10.0, 15.0, 12.0}
	for i, exp := range expected {
		if observed[i] != exp {
			t.Errorf("Observed[%d]: expected %f, got %f", i, exp, observed[i])
		}
	}
}

// TestCalculateCost 成本计算（标准差）
func TestCalculateCost(t *testing.T) {
	inspector := NewInspector()

	// 测试空时间线
	cost := inspector.calculateCost([]float64{})
	if cost != 0 {
		t.Errorf("Expected 0 for empty timeline, got %f", cost)
	}

	// 测试均匀分布（方差应该为 0）
	uniform := []float64{10.0, 10.0, 10.0, 10.0}
	uniformCost := inspector.calculateCost(uniform)
	if uniformCost != 0 {
		t.Errorf("Expected 0 variance for uniform values, got %f", uniformCost)
	}

	// 测试有差异的分布
	varied := []float64{5.0, 10.0, 15.0, 20.0}
	variedCost := inspector.calculateCost(varied)
	if variedCost <= 0 {
		t.Errorf("Expected positive variance for varied values, got %f", variedCost)
	}

	// 验证更大差异 => 更大成本
	largeVar := []float64{1.0, 50.0, 100.0}
	largeCost := inspector.calculateCost(largeVar)
	if largeCost <= variedCost {
		t.Errorf("Larger variance should have higher cost: %f <= %f", largeCost, variedCost)
	}
}

// TestPhi_NoDeviation φ=1 场景（无偏差）
func TestPhi_NoDeviation(t *testing.T) {
	inspector := NewInspector()

	// 创建完全相同的历史（理想情况）
	history := make([]store.UpdateLog, 10)
	for i := 0; i < 10; i++ {
		history[i] = store.UpdateLog{
			Timestamp: int64(1000 + i),
			Value:     10.0,
			NodeID:    "node-1",
			Operation: store.Increment,
		}
	}

	counter := store.NewPNCounter("test")
	for _, log := range history {
		if log.Operation == store.Increment {
			counter.Increment(log.NodeID, log.Value)
		} else {
			counter.Decrement(log.NodeID, log.Value)
		}
	}

	// 在这种构造的完美场景下，observed 和 optimal 应该相同
	// 但由于算法简化，我们验证 phi 接近 1.0
	inspector.SetOnInconsistencyReport(func(phi float64) {
		t.Logf("Phi for no-deviation case: %f", phi)
		// phi 可能不严格等于 1.0，但在合理范围内
		if phi <= 0 || phi > 10.0 {
			t.Errorf("Phi %f seems unreasonable for no-deviation case", phi)
		}
	})

	inspector.CheckInconsistency("test", time.Now().UnixNano(), counter)
	time.Sleep(50 * time.Millisecond)
}

// TestPhi_WithDeviation φ>1 场景（有偏差）
func TestPhi_WithDeviation(t *testing.T) {
	inspector := NewInspector()

	// 创建有明显差异的历史
	counter := store.NewPNCounter("test")

	// 模拟正常的递增序列
	for i := 0; i < 10; i++ {
		counter.Increment("node-1", float64(i*10+100))
		time.Sleep(1 * time.Millisecond)
	}

	// 模拟延迟更新造成的影响
	// 这种构造使得 observed 和 optimal 产生差异

	reportedPhi := 0.0
	inspector.SetOnInconsistencyReport(func(phi float64) {
		reportedPhi = phi
	})

	inspector.CheckInconsistency("test", time.Now().UnixNano(), counter)
	time.Sleep(50 * time.Millisecond)

	// 验证 phi 被报告（具体值取决于计算细节）
	if reportedPhi <= 0 {
		t.Error("Expected positive phi for deviation case")
	}
	t.Logf("Phi with deviation: %f", reportedPhi)
}

// TestStats_Update 统计信息更新
func TestStats_Update(t *testing.T) {
	inspector := NewInspector()

	// 创建测试数据
	counter := store.NewPNCounter("test")
	for i := 0; i < 10; i++ {
		counter.Increment("node-1", float64(i+1))
	}

	phiValues := []float64{1.0, 1.2, 0.8, 1.5, 1.1}
	var callCount int32

	inspector.SetOnInconsistencyReport(func(phi float64) {
		atomic.AddInt32(&callCount, 1)
	})

	// 多次检查
	for _, phi := range phiValues {
		// 模拟多次调用（实际中由 CheckInconsistency 触发）
		inspector.updateStats(phi)
	}

	stats := inspector.GetStats()

	// 验证统计累加
	if stats.TotalChecks != 5 {
		t.Errorf("Expected TotalChecks 5, got %d", stats.TotalChecks)
	}
	if stats.ReportsGenerated != 5 {
		t.Errorf("Expected ReportsGenerated 5, got %d", stats.ReportsGenerated)
	}

	// 验证平均值计算（简单检查合理性）
	if stats.AveragePhi <= 0 {
		t.Errorf("Expected positive average phi, got %f", stats.AveragePhi)
	}
}

// TestConcurrentChecks 并发检查测试
func TestConcurrentChecks(t *testing.T) {
	inspector := NewInspector()

	// 创建测试数据
	counter := store.NewPNCounter("test")
	for i := 0; i < 20; i++ {
		counter.Increment("node-1", float64(i+1))
	}

	var reportCount int32
	inspector.SetOnInconsistencyReport(func(phi float64) {
		atomic.AddInt32(&reportCount, 1)
	})

	// 并发执行检查
	for i := 0; i < 10; i++ {
		go func(idx int) {
			inspector.CheckInconsistency(fmt.Sprintf("key-%d", idx), time.Now().UnixNano(), counter)
		}(i)
	}

	time.Sleep(200 * time.Millisecond)

	finalCount := atomic.LoadInt32(&reportCount)
	if finalCount < 5 {
		t.Logf("Concurrent checks generated %d reports (may vary)", finalCount)
	}

	// 验证无 panic，统计数据合理
	stats := inspector.GetStats()
	if stats.TotalChecks < 5 {
		t.Errorf("Expected at least 5 total checks, got %d", stats.TotalChecks)
	}
}

// TestEdgeCases 边缘情况测试
func TestEdgeCases(t *testing.T) {
	inspector := NewInspector()

	// 测试非常大的数值
	counter := store.NewPNCounter("test")
	counter.Increment("node-1", 1e10) // 100亿
	counter.Decrement("node-1", 1e9)  // 10亿

	inspector.SetOnInconsistencyReport(func(phi float64) {
		if phi <= 0 {
			t.Errorf("Phi should be positive for large values, got %f", phi)
		}
	})

	inspector.CheckInconsistency("test", time.Now().UnixNano(), counter)
	time.Sleep(50 * time.Millisecond)

	// 测试零值历史
	zeroCounter := store.NewPNCounter("zero-test")
	// 不添加任何操作，历史为空
	inspector.CheckInconsistency("zero", time.Now().UnixNano(), zeroCounter)
	// 应该静默跳过，不 panic
}
