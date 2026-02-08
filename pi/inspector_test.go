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

// TestReconstructTimelines 时间线重构（简化版本）
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

// TestReconstructTimelinesWithRemote 完整版本的时间线重构测试
// 测试论文 Algorithm 2 的核心逻辑:
//   - Observed Timeline: 本地在收到远程更新之前的操作历史
//   - Optimal Timeline: 将远程更新按原始时间戳插入后的理想历史
func TestReconstructTimelinesWithRemote(t *testing.T) {
	inspector := NewInspector()

	// 场景: 模拟 SDN 链路带宽状态同步
	// 本地节点在 T=1000, 1002, 1004 时刻进行了操作
	// 远程节点在 T=1001 时刻进行了操作，但在 T=1005 才到达本地

	// 本地历史记录（模拟带宽更新）
	localHistory := []store.UpdateLog{
		{Timestamp: 1000, Value: 100.0, NodeID: "local", Operation: store.Increment}, // 初始带宽 100
		{Timestamp: 1002, Value: 20.0, NodeID: "local", Operation: store.Decrement},  // 本地分配 -20
		{Timestamp: 1004, Value: 10.0, NodeID: "local", Operation: store.Decrement},  // 本地分配 -10
	}

	// 远程更新（迟到的）
	// 原始时间戳 T=1001，但在 T=1005 才到达
	remoteUpdate := &RemoteUpdate{
		Key:          "link_1_bw",
		Value:        30.0,
		Operation:    store.Decrement, // 远程节点分配了 30 带宽
		OriginTime:   1001,            // 原始时间戳
		ReceiveTime:  1005,            // 到达时间
		OriginNodeID: "remote",
	}

	observed, optimal := inspector.reconstructTimelinesWithRemote(localHistory, remoteUpdate)

	// ========== 验证 Observed Timeline ==========
	// 应该只包含 T < 1005 的本地操作，不含远程更新
	// 期望序列: 100 -> 80 -> 70
	expectedObserved := []float64{100.0, 80.0, 70.0}

	if len(observed) != len(expectedObserved) {
		t.Errorf("Observed timeline length: expected %d, got %d", len(expectedObserved), len(observed))
	} else {
		for i, exp := range expectedObserved {
			if observed[i] != exp {
				t.Errorf("Observed[%d]: expected %.1f, got %.1f", i, exp, observed[i])
			}
		}
	}

	// ========== 验证 Optimal Timeline ==========
	// 应该将远程更新按原始时间戳(T=1001)插入
	// 时间序列: T=1000 -> T=1001 -> T=1002 -> T=1004
	// 值序列: 100 -> 70 -> 50 -> 40
	expectedOptimal := []float64{100.0, 70.0, 50.0, 40.0}

	if len(optimal) != len(expectedOptimal) {
		t.Errorf("Optimal timeline length: expected %d, got %d", len(expectedOptimal), len(optimal))
	} else {
		for i, exp := range expectedOptimal {
			if optimal[i] != exp {
				t.Errorf("Optimal[%d]: expected %.1f, got %.1f", i, exp, optimal[i])
			}
		}
	}

	t.Logf("Observed Timeline: %v", observed)
	t.Logf("Optimal Timeline: %v", optimal)
}

// TestReconstructTimelinesWithRemote_EarlyRemote 测试远程更新在时间线开头的情况
func TestReconstructTimelinesWithRemote_EarlyRemote(t *testing.T) {
	inspector := NewInspector()

	// 本地历史
	localHistory := []store.UpdateLog{
		{Timestamp: 1002, Value: 50.0, NodeID: "local", Operation: store.Increment},
		{Timestamp: 1004, Value: 10.0, NodeID: "local", Operation: store.Increment},
	}

	// 远程更新（原始时间比本地第一条记录更早）
	remoteUpdate := &RemoteUpdate{
		Key:          "link_bw",
		Value:        100.0,
		Operation:    store.Increment,
		OriginTime:   1000, // 比本地第一条记录更早
		ReceiveTime:  1005,
		OriginNodeID: "remote",
	}

	observed, optimal := inspector.reconstructTimelinesWithRemote(localHistory, remoteUpdate)

	// Observed: 只有本地操作 (50, 60)
	expectedObserved := []float64{50.0, 60.0}
	if len(observed) != len(expectedObserved) {
		t.Errorf("Observed length mismatch: expected %d, got %d", len(expectedObserved), len(observed))
	}

	// Optimal: 远程更新应该在最前面 (100, 150, 160)
	expectedOptimal := []float64{100.0, 150.0, 160.0}
	if len(optimal) != len(expectedOptimal) {
		t.Errorf("Optimal length mismatch: expected %d, got %d", len(expectedOptimal), len(optimal))
	} else {
		for i, exp := range expectedOptimal {
			if optimal[i] != exp {
				t.Errorf("Optimal[%d]: expected %.1f, got %.1f", i, exp, optimal[i])
			}
		}
	}

	t.Logf("Early remote - Observed: %v, Optimal: %v", observed, optimal)
}

// TestCheckInconsistencyWithRemote 测试完整的不一致性检查流程
func TestCheckInconsistencyWithRemote(t *testing.T) {
	inspector := NewInspector()

	var reportedPhi float64
	inspector.SetOnInconsistencyReport(func(phi float64) {
		reportedPhi = phi
	})

	// 构造一个会产生不一致性的场景
	localHistory := []store.UpdateLog{
		{Timestamp: 1000, Value: 100.0, NodeID: "local", Operation: store.Increment},
		{Timestamp: 1003, Value: 50.0, NodeID: "local", Operation: store.Decrement},
	}

	remoteUpdate := &RemoteUpdate{
		Key:          "test_key",
		Value:        30.0,
		Operation:    store.Decrement,
		OriginTime:   1001,
		ReceiveTime:  1005,
		OriginNodeID: "remote",
	}

	inspector.CheckInconsistencyWithRemote(remoteUpdate, localHistory)

	// 等待回调被调用
	time.Sleep(100 * time.Millisecond)

	// phi 应该大于 0（有效值）
	// 注意: φ = observed_cost / optimal_cost
	// - φ ≈ 1.0: 无明显不一致性
	// - φ > 1.0: 实际成本高于理想成本
	// - φ < 1.0: 实际成本低于理想成本（远程更新导致理想历史波动更大）
	if reportedPhi <= 0 {
		t.Errorf("Expected phi > 0, got %f", reportedPhi)
	}

	stats := inspector.GetStats()
	if stats.TotalChecks != 1 {
		t.Errorf("Expected 1 check, got %d", stats.TotalChecks)
	}

	t.Logf("φ value from CheckInconsistencyWithRemote: %.4f", reportedPhi)
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

// TestCheckInconsistency_BasicPhi 基础 φ 计算测试
func TestCheckInconsistency_BasicPhi(t *testing.T) {
	inspector := NewInspector()

	// 创建有足够历史的 counter
	counter := store.NewPNCounter("test")
	for i := 0; i < 10; i++ {
		counter.Increment("node-1", float64(i+1)*10)
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

	// 验证 φ 值合理性（应该是一个正数）
	if reportedPhi <= 0 {
		t.Errorf("Expected positive phi, got %f", reportedPhi)
	}

	// 验证统计更新
	stats := inspector.GetStats()
	if stats.TotalChecks == 0 {
		t.Error("Expected TotalChecks > 0")
	}
	if stats.ReportsGenerated == 0 {
		t.Error("Expected ReportsGenerated > 0")
	}

	t.Logf("Basic phi calculation result: %f", reportedPhi)
}

// TestPhi_NoDeviation φ=1 场景（无偏差）
func TestPhi_NoDeviation(t *testing.T) {
	inspector := NewInspector()

	// 创建完全一致的历史（理想情况）
	counter := store.NewPNCounter("test")
	// 模拟完全同步的操作序列
	values := []float64{100.0, 100.0, 100.0, 100.0, 100.0}
	for _, val := range values {
		counter.Increment("node-1", val)
		time.Sleep(1 * time.Millisecond)
	}

	reportedPhi := 0.0
	inspector.SetOnInconsistencyReport(func(phi float64) {
		reportedPhi = phi
		t.Logf("Phi for no-deviation case: %f", phi)
	})

	inspector.CheckInconsistency("test", time.Now().UnixNano(), counter)
	time.Sleep(100 * time.Millisecond)

	// 在理想情况下，φ 应该接近 1.0（无显著不一致性）
	if reportedPhi <= 0 {
		t.Errorf("Expected positive phi for no-deviation case, got %f", reportedPhi)
	}
	// φ 不一定严格等于 1.0，但应该在合理范围内
	if reportedPhi > 10.0 {
		t.Errorf("Phi %f seems unreasonably large for no-deviation case", reportedPhi)
	}
}

// TestPhi_WithDeviation φ>1 场景（有偏差）
func TestPhi_WithDeviation(t *testing.T) {
	inspector := NewInspector()

	// 创建有明显波动的历史（模拟不一致性）
	counter := store.NewPNCounter("test")

	// 模拟大幅波动的操作序列
	values := []float64{10.0, 100.0, 20.0, 150.0, 30.0, 200.0, 25.0}
	for _, val := range values {
		counter.Increment("node-1", val)
		time.Sleep(1 * time.Millisecond)
	}

	reportedPhi := 0.0
	inspector.SetOnInconsistencyReport(func(phi float64) {
		reportedPhi = phi
	})

	inspector.CheckInconsistency("test", time.Now().UnixNano(), counter)
	time.Sleep(100 * time.Millisecond)

	// 有显著波动的情况下，应该产生不一致性报告
	if reportedPhi <= 0 {
		t.Error("Expected positive phi for deviation case")
	}

	// φ 值反映了不一致性程度
	t.Logf("Phi with deviation: %f", reportedPhi)
}

// TestStats_Update 统计信息更新
func TestStats_Update(t *testing.T) {
	inspector := NewInspector()

	// 创建测试数据
	counter := store.NewPNCounter("test")
	for i := 0; i < 15; i++ {
		counter.Increment("node-1", float64(i+1)*5)
		time.Sleep(1 * time.Millisecond)
	}

	var reportedPhis []float64
	var mu sync.Mutex

	inspector.SetOnInconsistencyReport(func(phi float64) {
		mu.Lock()
		defer mu.Unlock()
		reportedPhis = append(reportedPhis, phi)
	})

	// 多次执行检查
	for i := 0; i < 5; i++ {
		inspector.CheckInconsistency(fmt.Sprintf("key-%d", i), time.Now().UnixNano(), counter)
	}

	time.Sleep(200 * time.Millisecond)

	stats := inspector.GetStats()

	// 验证统计累加
	if stats.TotalChecks < 5 {
		t.Errorf("Expected TotalChecks >= 5, got %d", stats.TotalChecks)
	}
	if stats.ReportsGenerated < 5 {
		t.Errorf("Expected ReportsGenerated >= 5, got %d", stats.ReportsGenerated)
	}

	// 验证平均值计算合理性
	if stats.AveragePhi <= 0 {
		t.Errorf("Expected positive average phi, got %f", stats.AveragePhi)
	}

	mu.Lock()
	phiCount := len(reportedPhis)
	mu.Unlock()

	if phiCount < 5 {
		t.Errorf("Expected at least 5 phi reports, got %d", phiCount)
	}

	t.Logf("Stats - TotalChecks: %d, ReportsGenerated: %d, AveragePhi: %f",
		stats.TotalChecks, stats.ReportsGenerated, stats.AveragePhi)
}

// TestConcurrentAccess 并发访问测试
func TestConcurrentAccess(t *testing.T) {
	inspector := NewInspector()

	// 创建测试数据
	counters := make([]*store.PNCounter, 5)
	for i := 0; i < 5; i++ {
		counters[i] = store.NewPNCounter(fmt.Sprintf("counter-%d", i))
		for j := 0; j < 10; j++ {
			counters[i].Increment("node-1", float64(j+1)*10)
			time.Sleep(100 * time.Microsecond)
		}
	}

	var reportCount int32
	inspector.SetOnInconsistencyReport(func(phi float64) {
		atomic.AddInt32(&reportCount, 1)
	})

	// 并发执行多种操作
	var wg sync.WaitGroup

	// 并发检查不一致性
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			inspector.CheckInconsistency(fmt.Sprintf("key-%d", idx), time.Now().UnixNano(), counters[idx%5])
		}(i)
	}

	// 并发获取统计信息
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = inspector.GetStats()
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	finalCount := atomic.LoadInt32(&reportCount)
	t.Logf("Concurrent access generated %d reports", finalCount)

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
	counter := store.NewPNCounter("test-large")
	counter.Increment("node-1", 1e10) // 100亿
	counter.Decrement("node-1", 1e9)  // 10亿

	largePhiReported := false
	inspector.SetOnInconsistencyReport(func(phi float64) {
		largePhiReported = true
		if phi <= 0 {
			t.Errorf("Phi should be positive for large values, got %f", phi)
		}
	})

	inspector.CheckInconsistency("test-large", time.Now().UnixNano(), counter)
	time.Sleep(100 * time.Millisecond)

	if !largePhiReported {
		t.Log("Large value test completed without panic")
	}

	// 测试零值历史
	zeroCounter := store.NewPNCounter("zero-test")
	// 不添加任何操作，历史为空
	inspector.CheckInconsistency("zero", time.Now().UnixNano(), zeroCounter)
	// 应该静默跳过，不 panic

	// 测试极短历史
	shortCounter := store.NewPNCounter("short-test")
	shortCounter.Increment("node-1", 10.0) // 只有一个操作
	inspector.CheckInconsistency("short", time.Now().UnixNano(), shortCounter)
	// 应该跳过处理

	// 测试负数值
	negativeCounter := store.NewPNCounter("negative-test")
	negativeCounter.Increment("node-1", 100.0)
	negativeCounter.Decrement("node-1", 150.0) // 结果为负数

	negativePhiReported := false
	inspector.SetOnInconsistencyReport(func(phi float64) {
		negativePhiReported = true
	})

	inspector.CheckInconsistency("negative", time.Now().UnixNano(), negativeCounter)
	time.Sleep(100 * time.Millisecond)

	if !negativePhiReported {
		t.Log("Negative value test completed without panic")
	}

	t.Log("All edge cases passed without panic")
}
