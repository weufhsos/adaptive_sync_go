package ac

import (
	"sync"
	"testing"
	"time"
)

// TestNew_DefaultConfig 默认配置创建
func TestNew_DefaultConfig(t *testing.T) {
	m := New(
		WithNodeID("test-node"),
	)

	if m == nil {
		t.Fatal("Manager should not be nil")
	}

	if m.config == nil {
		t.Fatal("Config should not be nil")
	}

	if m.config.NodeID != "test-node" {
		t.Errorf("Expected NodeID 'test-node', got '%s'", m.config.NodeID)
	}

	// 验证默认配置
	if m.config.GRPCPort != 50051 {
		t.Errorf("Expected default GRPCPort 50051, got %d", m.config.GRPCPort)
	}
	if m.config.TargetPhi != 1.05 {
		t.Errorf("Expected default TargetPhi 1.05, got %f", m.config.TargetPhi)
	}
	if m.config.InitialCL.QueueSize != 100 {
		t.Errorf("Expected default QueueSize 100, got %d", m.config.InitialCL.QueueSize)
	}
	if m.config.InitialCL.Timeout != 50*time.Millisecond {
		t.Errorf("Expected default Timeout 50ms, got %v", m.config.InitialCL.Timeout)
	}
}

// TestNew_WithOptions 带选项创建
func TestNew_WithOptions(t *testing.T) {
	m := New(
		WithNodeID("node-1"),
		WithPeers([]string{"peer-1:50051", "peer-2:50051"}),
		WithGRPCPort(50088),
		WithTargetPhi(1.10),
		WithInitialCL(200, 100*time.Millisecond),
	)

	if m.config.GRPCPort != 50088 {
		t.Errorf("Expected GRPCPort 50088, got %d", m.config.GRPCPort)
	}
	if m.config.TargetPhi != 1.10 {
		t.Errorf("Expected TargetPhi 1.10, got %f", m.config.TargetPhi)
	}
	if m.config.InitialCL.QueueSize != 200 {
		t.Errorf("Expected QueueSize 200, got %d", m.config.InitialCL.QueueSize)
	}
	if m.config.InitialCL.Timeout != 100*time.Millisecond {
		t.Errorf("Expected Timeout 100ms, got %v", m.config.InitialCL.Timeout)
	}

	// 验证对等节点
	if len(m.config.PeerAddresses) != 2 {
		t.Errorf("Expected 2 peers, got %d", len(m.config.PeerAddresses))
	}
}

// TestNew_MissingNodeID 缺少 NodeID（会 panic）
func TestNew_MissingNodeID(t *testing.T) {
	// 这个测试会 panic，我们在 recover 中捕获
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Expected panic occurred: %v", r)
		} else {
			t.Error("Expected panic for missing NodeID")
		}
	}()

	// 这行代码应该触发 panic
	New() // 没有 WithNodeID
}

// TestUpdate_Increment 正数增量更新
func TestUpdate_Increment(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 不启动（避免端口冲突），直接测试 Update/Get
	err := m.Update("link_1_bw", 100.0)
	// 可能因为分发失败而返回错误，但本地状态应该已更新
	if err != nil {
		t.Logf("Update returned error (expected without peers): %v", err)
	}

	value := m.Get("link_1_bw")
	if value != 100.0 {
		t.Errorf("Expected value 100.0, got %f", value)
	}
}

// TestUpdate_Decrement 负数增量更新
func TestUpdate_Decrement(t *testing.T) {
	m := New(WithNodeID("test-node"))

	m.Update("link_1_bw", 100.0)
	err := m.Update("link_1_bw", -30.0)

	if err != nil {
		t.Logf("Update returned error: %v", err)
	}

	value := m.Get("link_1_bw")
	if value != 70.0 {
		t.Errorf("Expected value 70.0, got %f", value)
	}
}

// TestGet 获取状态值
func TestGet(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 不存在的键应该返回 0
	value := m.Get("non-existent")
	if value != 0.0 {
		t.Errorf("Expected 0.0 for non-existent key, got %f", value)
	}

	// 设置后再获取
	m.Update("test-key", 42.5)
	value = m.Get("test-key")
	if value != 42.5 {
		t.Errorf("Expected 42.5, got %f", value)
	}
}

// TestSnapshot 获取快照
func TestSnapshot(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 空快照
	snapshot := m.Snapshot()
	if len(snapshot) != 0 {
		t.Errorf("Expected empty snapshot, got %d entries", len(snapshot))
	}

	// 添加一些数据
	m.Update("key1", 10.0)
	m.Update("key2", 20.0)
	m.Update("key3", 30.0)

	// 获取快照
	snapshot = m.Snapshot()

	if len(snapshot) != 3 {
		t.Errorf("Expected 3 keys in snapshot, got %d", len(snapshot))
	}

	// 验证值正确
	if snapshot["key1"] != 10.0 || snapshot["key2"] != 20.0 || snapshot["key3"] != 30.0 {
		t.Errorf("Snapshot values incorrect: %v", snapshot)
	}

	// 修改原始数据不应该影响快照
	m.Update("key1", 5.0)
	if snapshot["key1"] != 10.0 {
		t.Error("Snapshot should be immutable copy")
	}
}

// TestConcurrentUpdates 并发更新
func TestConcurrentUpdates(t *testing.T) {
	m := New(WithNodeID("test-node"))

	const numGoroutines = 50
	const incrementsPerGoroutine = 10

	var wg sync.WaitGroup

	// 启动多个 goroutine 并发更新
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				key := "concurrent_key"
				m.Update(key, 1.0)
			}
		}(i)
	}

	wg.Wait()

	// 验证最终值
	expected := float64(numGoroutines * incrementsPerGoroutine)
	value := m.Get("concurrent_key")
	if value != expected {
		t.Errorf("Expected value %f after %d increments, got %f",
			expected, numGoroutines*incrementsPerGoroutine, value)
	}
}

// TestGetStats 获取统计信息
func TestGetStats(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 初始状态
	stats := m.GetStats()
	if stats == nil {
		t.Fatal("GetStats returned nil")
	}

	if stats.TotalUpdates != 0 {
		t.Errorf("Expected 0 total updates, got %d", stats.TotalUpdates)
	}
	if stats.SuccessfulOps != 0 {
		t.Errorf("Expected 0 successful ops, got %d", stats.SuccessfulOps)
	}
	if stats.FailedOps != 0 {
		t.Errorf("Expected 0 failed ops, got %d", stats.FailedOps)
	}
	if stats.ActiveStates != 0 {
		t.Errorf("Expected 0 active states, got %d", stats.ActiveStates)
	}

	// 执行一些操作后检查
	m.Update("key1", 10.0)
	m.Update("key2", 20.0)

	stats = m.GetStats()
	if stats.ActiveStates != 2 {
		t.Errorf("Expected 2 active states, got %d", stats.ActiveStates)
	}
}

// TestMultipleKeys 多键操作
func TestMultipleKeys(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 操作多个不同的键
	keys := []string{"link_1_bw", "link_2_bw", "link_3_bw"}
	values := []float64{100.0, 200.0, 300.0}

	for i, key := range keys {
		m.Update(key, values[i])
	}

	// 验证每个键的值
	for i, key := range keys {
		value := m.Get(key)
		if value != values[i] {
			t.Errorf("Key %s: expected %f, got %f", key, values[i], value)
		}
	}

	// 验证快照包含所有键
	snapshot := m.Snapshot()
	if len(snapshot) != 3 {
		t.Errorf("Expected 3 keys in snapshot, got %d", len(snapshot))
	}
}

// TestZeroAndNegativeValues 零值和负值测试
func TestZeroAndNegativeValues(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 零值更新
	m.Update("zero-key", 0.0)
	if m.Get("zero-key") != 0.0 {
		t.Errorf("Zero update should result in zero value")
	}

	// 负值更新
	m.Update("negative-key", -50.0)
	if m.Get("negative-key") != -50.0 {
		t.Errorf("Expected -50.0, got %f", m.Get("negative-key"))
	}

	// 从负值增加
	m.Update("negative-key", 75.0)
	if m.Get("negative-key") != 25.0 {
		t.Errorf("Expected 25.0, got %f", m.Get("negative-key"))
	}
}

// TestLargeNumbers 大数值测试
func TestLargeNumbers(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 大正值
	m.Update("large-pos", 1e12) // 1万亿
	if m.Get("large-pos") != 1e12 {
		t.Errorf("Large positive number not handled correctly")
	}

	// 大负值
	m.Update("large-neg", -1e9) // 负10亿
	if m.Get("large-neg") != -1e9 {
		t.Errorf("Large negative number not handled correctly")
	}
}

// TestComponentInitialization 组件初始化
func TestComponentInitialization(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 验证所有组件都被正确初始化
	if m.stores == nil {
		t.Error("Stores map should be initialized")
	}

	if m.dispatcher == nil {
		t.Error("Dispatcher should be initialized")
	}

	if m.piInspector == nil {
		t.Error("PI Inspector should be initialized")
	}

	if m.ocaCtrl == nil {
		t.Error("OCA Controller should be initialized")
	}

	if m.grpcServer == nil {
		t.Error("gRPC Server should be initialized")
	}

	if m.stopChan == nil {
		t.Error("Stop channel should be initialized")
	}

	if m.stats == nil {
		t.Error("Stats should be initialized")
	}
}

// TestStoreCreation 存储创建
func TestStoreCreation(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 验证 getOrCreateStore 创建新的存储
	store1 := m.getOrCreateStore("key1")
	if store1 == nil {
		t.Fatal("getOrCreateStore returned nil")
	}

	// 验证同一键返回相同存储
	store2 := m.getOrCreateStore("key1")
	if store1 != store2 {
		t.Error("Same key should return same store instance")
	}

	// 验证不同键返回不同存储
	store3 := m.getOrCreateStore("key2")
	if store1 == store3 {
		t.Error("Different keys should return different store instances")
	}
}

// TestStatsConcurrency 统计并发安全性
func TestStatsConcurrency(t *testing.T) {
	m := New(WithNodeID("test-node"))

	var ops int32 = 1000

	// 并发执行更新操作
	var wg sync.WaitGroup
	for i := 0; i < int(ops); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Update("concurrent-stats", 1.0)
		}()
	}

	wg.Wait()

	// 获取统计信息
	stats := m.GetStats()

	// 验证统计计数合理（可能因分发失败而小于 ops）
	if stats.TotalUpdates > int64(ops) {
		t.Errorf("TotalUpdates %d exceeds expected %d", stats.TotalUpdates, ops)
	}

	t.Logf("Concurrent stats: Total=%d, Success=%d, Failed=%d, ActiveStates=%d",
		stats.TotalUpdates, stats.SuccessfulOps, stats.FailedOps, stats.ActiveStates)
}

// TestEdgeCases 边缘情况测试
func TestEdgeCases(t *testing.T) {
	m := New(WithNodeID("test-node"))

	// 空字符串键
	m.Update("", 42.0)
	if m.Get("") != 42.0 {
		t.Errorf("Empty string key not handled correctly")
	}

	// 特殊字符键
	specialKey := "key/with.special-chars_123"
	m.Update(specialKey, 99.9)
	if m.Get(specialKey) != 99.9 {
		t.Errorf("Special character key not handled correctly")
	}

	// 非常长的键
	longKey := ""
	for i := 0; i < 1000; i++ {
		longKey += "a"
	}
	m.Update(longKey, 123.456)
	if m.Get(longKey) != 123.456 {
		t.Errorf("Long key not handled correctly")
	}
}
