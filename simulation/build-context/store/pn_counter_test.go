package store

import (
	"fmt"
	"sync"
	"testing"
)

// TestNewPNCounter 验证新建 PNCounter 的初始状态
func TestNewPNCounter(t *testing.T) {
	counter := NewPNCounter("test_key")

	if counter.Key != "test_key" {
		t.Errorf("Expected key 'test_key', got '%s'", counter.Key)
	}
	if counter.Value() != 0.0 {
		t.Errorf("Expected initial value 0, got %f", counter.Value())
	}
	if len(counter.Incr) != 0 || len(counter.Decr) != 0 {
		t.Error("Expected empty Incr/Decr maps")
	}
	if counter.LocalIncr != 0.0 || counter.LocalDecr != 0.0 {
		t.Error("Expected zero local deltas")
	}
}

// TestIncrement 验证单节点增加操作
func TestIncrement(t *testing.T) {
	counter := NewPNCounter("test")

	// 单次增加
	counter.Increment("node-1", 10.0)
	if counter.Value() != 10.0 {
		t.Errorf("Expected value 10.0, got %f", counter.Value())
	}

	// 多次增加同一节点
	counter.Increment("node-1", 5.0)
	if counter.Value() != 15.0 {
		t.Errorf("Expected value 15.0, got %f", counter.Value())
	}

	// 不同节点增加
	counter.Increment("node-2", 3.0)
	if counter.Value() != 18.0 {
		t.Errorf("Expected value 18.0, got %f", counter.Value())
	}

	// 验证本地增量
	incr, decr := counter.GetLocalDelta()
	if incr != 18.0 || decr != 0.0 {
		t.Errorf("Expected local delta (18.0, 0.0), got (%f, %f)", incr, decr)
	}
}

// TestDecrement 验证单节点减少操作
func TestDecrement(t *testing.T) {
	counter := NewPNCounter("test")

	// 先增加再减少
	counter.Increment("node-1", 100.0)
	counter.Decrement("node-1", 30.0)

	if counter.Value() != 70.0 {
		t.Errorf("Expected value 70.0, got %f", counter.Value())
	}

	// 验证本地增量
	incr, decr := counter.GetLocalDelta()
	if incr != 100.0 || decr != 30.0 {
		t.Errorf("Expected local delta (100.0, 30.0), got (%f, %f)", incr, decr)
	}
}

// TestValueCalculation 验证 sum(Incr) - sum(Decr) 计算
func TestValueCalculation(t *testing.T) {
	counter := NewPNCounter("test")

	// 多节点混合操作
	counter.Increment("node-1", 50.0)
	counter.Increment("node-2", 30.0)
	counter.Decrement("node-1", 10.0)
	counter.Decrement("node-3", 5.0)

	expected := 50.0 + 30.0 - 10.0 - 5.0 // 65.0
	actual := counter.Value()
	if actual != expected {
		t.Errorf("Expected value %f, got %f", expected, actual)
	}
}

// TestMerge_BasicMerge 验证基础合并操作
func TestMerge_BasicMerge(t *testing.T) {
	local := NewPNCounter("test")
	remoteIncr := map[string]float64{"node-a": 100.0, "node-b": 50.0}
	remoteDecr := map[string]float64{"node-a": 20.0}

	local.Merge(remoteIncr, remoteDecr, "remote-node")

	// 验证本地状态被更新
	if local.Incr["node-a"] != 100.0 {
		t.Errorf("Expected Incr[node-a] = 100.0, got %f", local.Incr["node-a"])
	}
	if local.Decr["node-a"] != 20.0 {
		t.Errorf("Expected Decr[node-a] = 20.0, got %f", local.Decr["node-a"])
	}
	if local.Incr["node-b"] != 50.0 {
		t.Errorf("Expected Incr[node-b] = 50.0, got %f", local.Incr["node-b"])
	}
}

// TestMerge_MaxValueRule 验证 CRDT Max 规则
func TestMerge_MaxValueRule(t *testing.T) {
	counter := NewPNCounter("test")

	// 本地操作
	counter.Increment("node-1", 50.0)

	// 远程合并（更大的值）
	remoteIncr := map[string]float64{"node-1": 80.0, "node-2": 20.0}
	remoteDecr := map[string]float64{}
	counter.Merge(remoteIncr, remoteDecr, "node-2")

	// 应该取 node-1 的最大值 80.0
	expected := 80.0 + 20.0 // 100.0
	actual := counter.Value()
	if actual != expected {
		t.Errorf("Expected value %f after merge, got %f", expected, actual)
	}

	// 验证取了最大值而不是累加
	if counter.Incr["node-1"] != 80.0 {
		t.Errorf("Expected Incr[node-1] = 80.0 (max), got %f", counter.Incr["node-1"])
	}
}

// TestMerge_Concurrent 并发合并测试
func TestMerge_Concurrent(t *testing.T) {
	counter := NewPNCounter("test")
	var wg sync.WaitGroup

	// 启动多个 goroutine 并发合并
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(nodeID string, value float64) {
			defer wg.Done()
			remoteIncr := map[string]float64{nodeID: value}
			counter.Merge(remoteIncr, nil, nodeID)
		}(fmt.Sprintf("node-%d", i), float64(i))
	}

	wg.Wait()

	// 验证无 panic 且值大于 0
	if counter.Value() <= 0 {
		t.Error("Expected positive value after concurrent merges")
	}

	// 验证所有节点都被记录
	if len(counter.Incr) != 100 {
		t.Errorf("Expected 100 nodes in Incr map, got %d", len(counter.Incr))
	}
}

// TestGetLocalDelta 验证本地增量获取
func TestGetLocalDelta(t *testing.T) {
	counter := NewPNCounter("test")

	// 增加操作
	counter.Increment("node-1", 100.0)
	incr, decr := counter.GetLocalDelta()
	if incr != 100.0 || decr != 0.0 {
		t.Errorf("Expected (100.0, 0.0), got (%f, %f)", incr, decr)
	}

	// 减少操作
	counter.Decrement("node-1", 30.0)
	incr, decr = counter.GetLocalDelta()
	if incr != 100.0 || decr != 30.0 {
		t.Errorf("Expected (100.0, 30.0), got (%f, %f)", incr, decr)
	}
}

// TestClearLocalDelta 验证清除本地增量
func TestClearLocalDelta(t *testing.T) {
	counter := NewPNCounter("test")

	counter.Increment("node-1", 100.0)
	counter.Decrement("node-1", 30.0)

	// 清除前检查
	incr, decr := counter.GetLocalDelta()
	if incr == 0.0 && decr == 0.0 {
		t.Error("Local deltas should not be zero before clear")
	}

	// 清除
	counter.ClearLocalDelta()

	// 清除后检查
	incr, decr = counter.GetLocalDelta()
	if incr != 0.0 || decr != 0.0 {
		t.Errorf("Expected zero deltas after clear, got (%f, %f)", incr, decr)
	}

	// 验证总值不受影响
	if counter.Value() != 70.0 {
		t.Errorf("Value should remain 70.0 after clear, got %f", counter.Value())
	}
}

// TestGetState 验证状态深拷贝
func TestGetState(t *testing.T) {
	counter := NewPNCounter("test")
	counter.Increment("node-1", 100.0)
	counter.Decrement("node-2", 50.0)

	// 获取状态副本
	incr, decr := counter.GetState()

	// 修改副本不应该影响原始数据
	incr["node-1"] = 999.0
	decr["node-2"] = 999.0

	// 验证原始数据未变
	if counter.Incr["node-1"] != 100.0 {
		t.Errorf("Original Incr should not be modified, got %f", counter.Incr["node-1"])
	}
	if counter.Decr["node-2"] != 50.0 {
		t.Errorf("Original Decr should not be modified, got %f", counter.Decr["node-2"])
	}
}

// TestHistory_Record 验证历史记录功能
func TestHistory_Record(t *testing.T) {
	counter := NewPNCounter("test")

	initialHistoryLen := len(counter.GetHistory())

	// 执行一些操作
	counter.Increment("node-1", 10.0)
	counter.Decrement("node-1", 5.0)
	counter.Increment("node-2", 20.0)

	history := counter.GetHistory()
	expectedLen := initialHistoryLen + 3
	if len(history) != expectedLen {
		t.Errorf("Expected history length %d, got %d", expectedLen, len(history))
	}

	// 验证最后一项是最近的操作
	lastEntry := history[len(history)-1]
	if lastEntry.Value != 20.0 || lastEntry.NodeID != "node-2" || lastEntry.Operation != Increment {
		t.Errorf("Last history entry incorrect: %+v", lastEntry)
	}
}

// TestHistory_MaxSize 验证历史记录限制
func TestHistory_MaxSize(t *testing.T) {
	counter := NewPNCounter("test")
	counter.SetMaxHistorySize(5)

	// 添加超过限制的记录
	for i := 0; i < 10; i++ {
		counter.Increment("node-1", float64(i+1))
	}

	history := counter.GetHistory()
	if len(history) != 5 {
		t.Errorf("Expected history size 5, got %d", len(history))
	}

	// 验证保留的是最新的记录
	firstValue := history[0].Value
	lastValue := history[4].Value
	if firstValue != 6.0 || lastValue != 10.0 {
		t.Errorf("Expected values [6,7,8,9,10], got [%f,...,%f]", firstValue, lastValue)
	}
}

// TestSetMaxHistorySize 验证动态调整历史大小
func TestSetMaxHistorySize(t *testing.T) {
	counter := NewPNCounter("test")

	// 先添加很多记录
	for i := 0; i < 20; i++ {
		counter.Increment("node-1", 1.0)
	}

	if len(counter.GetHistory()) != 20 {
		t.Error("Should have 20 history entries initially")
	}

	// 设置更小的限制
	counter.SetMaxHistorySize(10)

	if len(counter.GetHistory()) != 10 {
		t.Errorf("Expected 10 entries after resize, got %d", len(counter.GetHistory()))
	}
}

// TestConcurrentOperations 并发操作测试
func TestConcurrentOperations(t *testing.T) {
	counter := NewPNCounter("test")
	var wg sync.WaitGroup

	// 并发执行多种操作
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			nodeID := fmt.Sprintf("node-%d", idx%5)

			if idx%2 == 0 {
				counter.Increment(nodeID, float64(idx))
			} else {
				counter.Decrement(nodeID, float64(idx))
			}
		}(i)
	}

	wg.Wait()

	// 验证最终状态合理（不会panic，值在合理范围内）
	value := counter.Value()
	t.Logf("Final concurrent value: %f", value)
	if value < -10000 || value > 10000 {
		t.Errorf("Value %f seems unreasonable after concurrent operations", value)
	}
}

// TestEdgeCases 边缘情况测试
func TestEdgeCases(t *testing.T) {
	counter := NewPNCounter("test")

	// 零值操作
	counter.Increment("node-1", 0.0)
	counter.Decrement("node-1", 0.0)
	if counter.Value() != 0.0 {
		t.Errorf("Zero operations should not change value, got %f", counter.Value())
	}

	// 负数增加（应该当作正值处理）
	counter.Increment("node-1", -10.0)
	// 注意：这里的行为取决于实现，如果允许负数增加，则值为-10

	// 负数减少（应该当作正值处理）
	counter.Decrement("node-1", -5.0)
	// 如果负数减少被当作正值增加，则值为-15
}
