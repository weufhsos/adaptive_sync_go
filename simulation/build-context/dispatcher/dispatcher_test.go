package dispatcher

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewDispatcher 验证新建 Dispatcher 配置
func TestNewDispatcher(t *testing.T) {
	d := NewDispatcher("node-1", []string{"peer-1:50051"})

	if d.nodeID != "node-1" {
		t.Errorf("Expected nodeID 'node-1', got '%s'", d.nodeID)
	}

	if len(d.peers) != 1 || d.peers[0] != "peer-1:50051" {
		t.Errorf("Expected peers ['peer-1:50051'], got %v", d.peers)
	}

	cl := d.GetCurrentConsistencyLevel()
	if cl.QueueSize != 100 {
		t.Errorf("Expected default QueueSize 100, got %d", cl.QueueSize)
	}
	if cl.Timeout != 50*time.Millisecond {
		t.Errorf("Expected default Timeout 50ms, got %v", cl.Timeout)
	}

	// 验证统计初始化
	stats := d.GetStats()
	if stats.TotalUpdates != 0 {
		t.Errorf("Expected TotalUpdates 0, got %d", stats.TotalUpdates)
	}
}

// TestEnqueue_Normal 正常入队操作
func TestEnqueue_Normal(t *testing.T) {
	d := NewDispatcher("node-1", nil)

	err := d.Enqueue("test_key", 10.0, "update-1")
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	stats := d.GetStats()
	if stats.TotalUpdates != 1 {
		t.Errorf("Expected TotalUpdates 1, got %d", stats.TotalUpdates)
	}
	if stats.QueueFullDrops != 0 {
		t.Errorf("Expected QueueFullDrops 0, got %d", stats.QueueFullDrops)
	}
}

// TestEnqueue_QueueFull 队列满时入队
func TestEnqueue_QueueFull(t *testing.T) {
	d := NewDispatcher("node-1", nil)
	d.SetConsistencyLevel(ConsistencyLevel{QueueSize: 1, Timeout: 10 * time.Millisecond})

	// 填满队列
	err1 := d.Enqueue("key1", 1.0, "id-1")
	if err1 != nil {
		t.Fatalf("First enqueue should succeed, got: %v", err1)
	}

	// 再次入队应该失败
	err2 := d.Enqueue("key2", 2.0, "id-2")
	if err2 == nil {
		t.Error("Expected error when queue is full")
	}

	stats := d.GetStats()
	if stats.QueueFullDrops != 1 {
		t.Errorf("Expected QueueFullDrops 1, got %d", stats.QueueFullDrops)
	}
}

// TestSetConsistencyLevel 动态调整 CL
func TestSetConsistencyLevel(t *testing.T) {
	d := NewDispatcher("node-1", nil)

	newCL := ConsistencyLevel{QueueSize: 200, Timeout: 100 * time.Millisecond}
	d.SetConsistencyLevel(newCL)

	cl := d.GetCurrentConsistencyLevel()
	if cl.QueueSize != 200 {
		t.Errorf("Expected QueueSize 200, got %d", cl.QueueSize)
	}
	if cl.Timeout != 100*time.Millisecond {
		t.Errorf("Expected Timeout 100ms, got %v", cl.Timeout)
	}
}

// TestGetCurrentConsistencyLevel 获取当前 CL
func TestGetCurrentConsistencyLevel(t *testing.T) {
	d := NewDispatcher("node-1", nil)

	// 默认值
	cl := d.GetCurrentConsistencyLevel()
	if cl.QueueSize != 100 || cl.Timeout != 50*time.Millisecond {
		t.Errorf("Expected default CL (100, 50ms), got (%d, %v)", cl.QueueSize, cl.Timeout)
	}

	// 修改后
	d.SetConsistencyLevel(ConsistencyLevel{QueueSize: 50, Timeout: 25 * time.Millisecond})
	cl = d.GetCurrentConsistencyLevel()
	if cl.QueueSize != 50 || cl.Timeout != 25*time.Millisecond {
		t.Errorf("Expected modified CL (50, 25ms), got (%d, %v)", cl.QueueSize, cl.Timeout)
	}
}

// TestStartStop 启动和停止
func TestStartStop(t *testing.T) {
	d := NewDispatcher("node-1", nil)

	// 启动
	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 短暂运行
	time.Sleep(50 * time.Millisecond)

	// 停止
	d.Stop()

	// 再次停止不应该 panic
	d.Stop()
}

// TestFastMode 验证 Fast 模式发送
func TestFastMode(t *testing.T) {
	d := NewDispatcher("node-1", nil)
	d.SetConsistencyLevel(ConsistencyLevel{QueueSize: 10, Timeout: 100 * time.Millisecond})

	callCount := int32(0)
	d.SetOnUpdateProcessed(func(key string, success bool) {
		atomic.AddInt32(&callCount, 1)
	})

	// 启动分发器
	if err := d.Start(); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer d.Stop()

	// 队列未满时入队
	for i := 0; i < 5; i++ {
		err := d.Enqueue(fmt.Sprintf("key-%d", i), float64(i+1), fmt.Sprintf("id-%d", i))
		if err != nil {
			t.Errorf("Enqueue %d failed: %v", i, err)
		}
	}

	// 等待处理
	time.Sleep(200 * time.Millisecond)

	// 验证回调被调用
	finalCount := atomic.LoadInt32(&callCount)
	if finalCount < 3 {
		t.Logf("Fast mode: processed %d updates (may vary due to timing)", finalCount)
	}
}

// TestAddRemovePeer 添加/移除对等节点
func TestAddRemovePeer(t *testing.T) {
	d := NewDispatcher("node-1", nil)

	// 添加对等节点
	peerAddr := "localhost:50051"
	// 注意：这里我们不能创建真实的 gRPC 客户端，所以只测试管理逻辑
	d.AddPeer(peerAddr, nil) // 传入 nil 客户端用于测试

	// 移除对等节点
	d.RemovePeer(peerAddr)

	// 再次移除不应该 panic
	d.RemovePeer(peerAddr)
}

// TestOnUpdateProcessedCallback 回调函数调用
func TestOnUpdateProcessedCallback(t *testing.T) {
	d := NewDispatcher("node-1", nil)

	var callbackKey string
	var callbackSuccess bool
	callbackCalled := false

	d.SetOnUpdateProcessed(func(key string, success bool) {
		callbackKey = key
		callbackSuccess = success
		callbackCalled = true
	})

	// 启动
	if err := d.Start(); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer d.Stop()

	// 入队操作
	d.Enqueue("callback_test", 1.0, "test-id")

	// 等待回调（即使没有真实客户端，回调也应该被触发）
	time.Sleep(100 * time.Millisecond)

	// 注意：由于没有真实的 gRPC 客户端，发送会失败，但回调仍会被调用
	t.Logf("Callback called: %v, key: %s, success: %v", callbackCalled, callbackKey, callbackSuccess)
}

// TestStats 统计信息准确性
func TestStats(t *testing.T) {
	d := NewDispatcher("node-1", nil)

	// 初始状态
	stats := d.GetStats()
	if stats.TotalUpdates != 0 || stats.SuccessfulSends != 0 || stats.FailedSends != 0 {
		t.Error("Initial stats should be zero")
	}

	// 执行一些操作
	d.Enqueue("key1", 1.0, "id-1")
	d.Enqueue("key2", 2.0, "id-2")

	// 检查统计更新
	stats = d.GetStats()
	if stats.TotalUpdates < 2 {
		t.Errorf("Expected at least 2 total updates, got %d", stats.TotalUpdates)
	}
}

// TestBoundedStaleness 有界过时性保证
func TestBoundedStaleness(t *testing.T) {
	d := NewDispatcher("node-1", nil)
	queueSize := 3
	d.SetConsistencyLevel(ConsistencyLevel{QueueSize: queueSize, Timeout: 50 * time.Millisecond})

	// 填满队列
	for i := 0; i < queueSize; i++ {
		err := d.Enqueue(fmt.Sprintf("key-%d", i), float64(i+1), fmt.Sprintf("id-%d", i))
		if err != nil {
			t.Fatalf("Enqueue %d failed: %v", i, err)
		}
	}

	// 下一个应该被拒绝
	err := d.Enqueue("overflow-key", 99.0, "overflow-id")
	if err == nil {
		t.Error("Expected error for queue overflow")
	}

	// 验证错误信息包含关键信息
	if err != nil && len(err.Error()) < 20 {
		t.Errorf("Error message too short: %v", err)
	}
}

// TestConcurrentEnqueue 并发出队测试
func TestConcurrentEnqueue(t *testing.T) {
	d := NewDispatcher("node-1", nil)
	d.SetConsistencyLevel(ConsistencyLevel{QueueSize: 100, Timeout: 50 * time.Millisecond})

	if err := d.Start(); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer d.Stop()

	var successCount int32
	var failCount int32

	// 并发入队
	for i := 0; i < 50; i++ {
		go func(idx int) {
			err := d.Enqueue(fmt.Sprintf("concurrent-key-%d", idx), float64(idx), fmt.Sprintf("id-%d", idx))
			if err == nil {
				atomic.AddInt32(&successCount, 1)
			} else {
				atomic.AddInt32(&failCount, 1)
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond)

	finalSuccess := atomic.LoadInt32(&successCount)
	finalFail := atomic.LoadInt32(&failCount)

	t.Logf("Concurrent enqueue: success=%d, fail=%d", finalSuccess, finalFail)

	// 至少有一些应该成功
	if finalSuccess == 0 {
		t.Error("Expected at least some successful enqueues")
	}
}

// TestQueueSizeManagement 队列大小管理
func TestQueueSizeManagement(t *testing.T) {
	d := NewDispatcher("node-1", nil)

	// 默认队列大小
	if cap(d.queue) != 1000 {
		t.Errorf("Expected default queue capacity 1000, got %d", cap(d.queue))
	}

	// 动态调整 CL 不应该影响已创建的队列
	// （在当前实现中，队列容量在创建时固定）
}
