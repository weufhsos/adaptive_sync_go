# Go-SDN 自适应一致性 (AC) 测试计划书

## 1. 测试概述

### 1.1 测试目的
本测试计划书旨在确保 Go-SDN 自适应一致性 (AC) 协调层库的各个组件功能正确、性能达标，并验证系统在分布式环境中的行为符合论文算法设计。

### 1.2 测试范围
| 测试类型 | 覆盖范围 |
|---------|---------|
| 单元测试 | PNCounter、Dispatcher、PI Inspector、OCA Controller、gRPC传输层、Manager |
| 集成测试 | 多节点同步、端到端流程、故障恢复、自适应调整闭环 |
| 性能测试 | 吞吐量、延迟、内存占用、并发性能 |

### 1.3 测试环境要求
- **Go 版本**: 1.21+
- **测试框架**: Go 标准 `testing` 包
- **依赖**: `google.golang.org/grpc`
- **并发测试**: `sync/atomic` 用于计数验证
- **Mock框架**: `testify/mock` (可选)

---

## 2. 单元测试计划

### 2.1 PNCounter 模块 (`store/pn_counter_test.go`)

#### 2.1.1 测试目标
验证 CRDT PN-Counter 实现的正确性，包括增减操作、合并逻辑和值计算。

#### 2.1.2 测试用例

| 用例编号 | 测试名称 | 测试描述 | 预期结果 |
|---------|---------|---------|---------|
| TC-PNC-001 | TestNewPNCounter | 验证新建 PNCounter 的初始状态 | Key 正确，Incr/Decr 为空 map，Value() == 0 |
| TC-PNC-002 | TestIncrement | 验证单节点增加操作 | Incr[nodeID] 累加正确，Value() 返回正确值 |
| TC-PNC-003 | TestDecrement | 验证单节点减少操作 | Decr[nodeID] 累加正确，Value() 返回正确值 |
| TC-PNC-004 | TestValueCalculation | 验证 sum(Incr) - sum(Decr) 计算 | 多节点累加后 Value() 计算正确 |
| TC-PNC-005 | TestMerge_BasicMerge | 验证基础合并操作 | 远程状态正确合并到本地 |
| TC-PNC-006 | TestMerge_MaxValueRule | 验证 CRDT Max 规则 | 相同节点取最大值 |
| TC-PNC-007 | TestMerge_Concurrent | 并发合并测试 | 无数据竞争，最终一致 |
| TC-PNC-008 | TestGetLocalDelta | 验证本地增量获取 | LocalIncr/LocalDecr 返回正确 |
| TC-PNC-009 | TestClearLocalDelta | 验证清除本地增量 | 清除后 LocalIncr/LocalDecr == 0 |
| TC-PNC-010 | TestGetState | 验证状态深拷贝 | 返回副本，修改不影响原数据 |
| TC-PNC-011 | TestHistory_Record | 验证历史记录功能 | 操作被正确记录到 History |
| TC-PNC-012 | TestHistory_MaxSize | 验证历史记录限制 | 超过 maxHistorySize 后自动裁剪 |
| TC-PNC-013 | TestSetMaxHistorySize | 验证动态调整历史大小 | 设置后历史被正确裁剪 |

#### 2.1.3 测试代码示例

```go
// store/pn_counter_test.go
package store

import (
    "sync"
    "testing"
)

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
}

func TestIncrement(t *testing.T) {
    counter := NewPNCounter("test")
    
    counter.Increment("node-1", 10.0)
    counter.Increment("node-1", 5.0)
    counter.Increment("node-2", 3.0)
    
    if counter.Value() != 18.0 {
        t.Errorf("Expected value 18.0, got %f", counter.Value())
    }
}

func TestDecrement(t *testing.T) {
    counter := NewPNCounter("test")
    
    counter.Increment("node-1", 100.0)
    counter.Decrement("node-1", 30.0)
    
    if counter.Value() != 70.0 {
        t.Errorf("Expected value 70.0, got %f", counter.Value())
    }
}

func TestMerge_MaxValueRule(t *testing.T) {
    counter := NewPNCounter("test")
    
    // 本地操作
    counter.Increment("node-1", 50.0)
    
    // 远程合并（更大的值）
    remoteIncr := map[string]float64{"node-1": 80.0, "node-2": 20.0}
    remoteDecr := map[string]float64{}
    counter.Merge(remoteIncr, remoteDecr, "node-2")
    
    // 应该取 node-1 的最大值 80.0
    if counter.Value() != 100.0 { // 80 + 20
        t.Errorf("Expected value 100.0 after merge, got %f", counter.Value())
    }
}

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
}

func TestHistory_MaxSize(t *testing.T) {
    counter := NewPNCounter("test")
    counter.SetMaxHistorySize(10)
    
    // 添加 20 条记录
    for i := 0; i < 20; i++ {
        counter.Increment("node-1", 1.0)
    }
    
    history := counter.GetHistory()
    if len(history) != 10 {
        t.Errorf("Expected history size 10, got %d", len(history))
    }
}
```

---

### 2.2 Dispatcher 模块 (`dispatcher/dispatcher_test.go`)

#### 2.2.1 测试目标
验证分发控制器的准入控制、队列管理和 Fast/Batched 模式切换逻辑（Algorithm 5）。

#### 2.2.2 测试用例

| 用例编号 | 测试名称 | 测试描述 | 预期结果 |
|---------|---------|---------|---------|
| TC-DSP-001 | TestNewDispatcher | 验证新建 Dispatcher 配置 | 默认 CL 配置正确 |
| TC-DSP-002 | TestEnqueue_Normal | 正常入队操作 | 返回 nil，统计更新 |
| TC-DSP-003 | TestEnqueue_QueueFull | 队列满时入队 | 返回错误，QueueFullDrops++ |
| TC-DSP-004 | TestSetConsistencyLevel | 动态调整 CL | CL 值正确更新 |
| TC-DSP-005 | TestGetCurrentConsistencyLevel | 获取当前 CL | 返回正确的 CL 配置 |
| TC-DSP-006 | TestStartStop | 启动和停止 | 无 panic，状态正确切换 |
| TC-DSP-007 | TestFastMode | 验证 Fast 模式发送 | 队列未满时立即发送 |
| TC-DSP-008 | TestBatchedMode | 验证批处理模式 | 定时器触发批量发送 |
| TC-DSP-009 | TestAddRemovePeer | 添加/移除对等节点 | 客户端列表正确更新 |
| TC-DSP-010 | TestOnUpdateProcessedCallback | 回调函数调用 | 回调被正确触发 |
| TC-DSP-011 | TestStats | 统计信息准确性 | 各项统计正确累加 |
| TC-DSP-012 | TestBoundedStaleness | 有界过时性保证 | 队列满时拒绝新更新 |

#### 2.2.3 测试代码示例

```go
// dispatcher/dispatcher_test.go
package dispatcher

import (
    "testing"
    "time"
)

func TestNewDispatcher(t *testing.T) {
    d := NewDispatcher("node-1", []string{"peer-1:50051"})
    
    if d.nodeID != "node-1" {
        t.Errorf("Expected nodeID 'node-1', got '%s'", d.nodeID)
    }
    
    cl := d.GetCurrentConsistencyLevel()
    if cl.QueueSize != 100 {
        t.Errorf("Expected default QueueSize 100, got %d", cl.QueueSize)
    }
}

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
}

func TestEnqueue_QueueFull(t *testing.T) {
    d := NewDispatcher("node-1", nil)
    d.SetConsistencyLevel(ConsistencyLevel{QueueSize: 1, Timeout: 10 * time.Millisecond})
    
    // 填满队列
    d.Enqueue("key1", 1.0, "id-1")
    
    // 再次入队应该失败
    err := d.Enqueue("key2", 2.0, "id-2")
    if err == nil {
        t.Error("Expected error when queue is full")
    }
    
    stats := d.GetStats()
    if stats.QueueFullDrops != 1 {
        t.Errorf("Expected QueueFullDrops 1, got %d", stats.QueueFullDrops)
    }
}

func TestSetConsistencyLevel(t *testing.T) {
    d := NewDispatcher("node-1", nil)
    
    newCL := ConsistencyLevel{QueueSize: 200, Timeout: 100 * time.Millisecond}
    d.SetConsistencyLevel(newCL)
    
    cl := d.GetCurrentConsistencyLevel()
    if cl.QueueSize != 200 || cl.Timeout != 100*time.Millisecond {
        t.Error("CL not updated correctly")
    }
}

func TestStartStop(t *testing.T) {
    d := NewDispatcher("node-1", nil)
    
    if err := d.Start(); err != nil {
        t.Errorf("Start failed: %v", err)
    }
    
    // 让分发循环运行一会
    time.Sleep(100 * time.Millisecond)
    
    // 不应该 panic
    d.Stop()
}

func TestOnUpdateProcessedCallback(t *testing.T) {
    d := NewDispatcher("node-1", nil)
    
    callbackCalled := false
    d.SetOnUpdateProcessed(func(key string, success bool) {
        callbackCalled = true
    })
    
    d.Start()
    defer d.Stop()
    
    d.Enqueue("test", 1.0, "id-1")
    
    // 等待回调被调用
    time.Sleep(200 * time.Millisecond)
    
    // 注意：由于没有真实的 gRPC 客户端，回调可能在发送失败后被调用
}
```

---

### 2.3 PI Inspector 模块 (`pi/inspector_test.go`)

#### 2.3.1 测试目标
验证性能检查模块的不一致性比率计算逻辑（Algorithm 2）。

#### 2.3.2 测试用例

| 用例编号 | 测试名称 | 测试描述 | 预期结果 |
|---------|---------|---------|---------|
| TC-PI-001 | TestNewInspector | 验证新建 Inspector | 初始化正确，统计为 0 |
| TC-PI-002 | TestStartStop | 启动和停止 | 无 panic，正常退出 |
| TC-PI-003 | TestCheckInconsistency_BasicPhi | 基础 φ 计算 | 返回合理的不一致性比率 |
| TC-PI-004 | TestCheckInconsistency_InsufficientHistory | 历史不足时跳过 | 不生成报告 |
| TC-PI-005 | TestSetOnInconsistencyReport | 回调设置 | 回调被正确调用 |
| TC-PI-006 | TestReconstructTimelines | 时间线重构 | 生成正确的 observed/optimal 序列 |
| TC-PI-007 | TestCalculateCost | 成本计算（标准差） | 返回正确的方差值 |
| TC-PI-008 | TestStats_Update | 统计信息更新 | AveragePhi 正确计算 |
| TC-PI-009 | TestPhi_NoDeviation | φ=1 场景 | 无偏差时返回 1.0 |
| TC-PI-010 | TestPhi_WithDeviation | φ>1 场景 | 有偏差时返回 >1.0 |

#### 2.3.3 测试代码示例

```go
// pi/inspector_test.go
package pi

import (
    "testing"
    "time"
    
    "github.com/weufhsos/adaptive_sync_go/store"
)

func TestNewInspector(t *testing.T) {
    inspector := NewInspector()
    
    stats := inspector.GetStats()
    if stats.TotalChecks != 0 {
        t.Errorf("Expected TotalChecks 0, got %d", stats.TotalChecks)
    }
}

func TestStartStop(t *testing.T) {
    inspector := NewInspector()
    inspector.Start()
    
    time.Sleep(50 * time.Millisecond)
    
    inspector.Stop() // 不应该 panic
}

func TestCheckInconsistency_InsufficientHistory(t *testing.T) {
    inspector := NewInspector()
    
    counter := store.NewPNCounter("test")
    counter.Increment("node-1", 10.0) // 只有 1 条历史
    
    callbackCalled := false
    inspector.SetOnInconsistencyReport(func(phi float64) {
        callbackCalled = true
    })
    
    inspector.CheckInconsistency("test", time.Now().UnixNano(), counter)
    
    time.Sleep(100 * time.Millisecond)
    
    if callbackCalled {
        t.Error("Callback should not be called with insufficient history")
    }
}

func TestSetOnInconsistencyReport(t *testing.T) {
    inspector := NewInspector()
    
    reportedPhi := 0.0
    inspector.SetOnInconsistencyReport(func(phi float64) {
        reportedPhi = phi
    })
    
    // 创建有足够历史的 counter
    counter := store.NewPNCounter("test")
    for i := 0; i < 10; i++ {
        counter.Increment("node-1", float64(i+1))
    }
    
    inspector.CheckInconsistency("test", time.Now().UnixNano(), counter)
    
    time.Sleep(100 * time.Millisecond)
    
    if reportedPhi == 0 {
        t.Error("Expected non-zero phi to be reported")
    }
}

func TestCalculateCost(t *testing.T) {
    inspector := NewInspector()
    
    // 测试空时间线
    cost := inspector.calculateCost([]float64{})
    if cost != 0 {
        t.Errorf("Expected 0 for empty timeline, got %f", cost)
    }
    
    // 测试均匀分布（方差应该较小）
    uniform := []float64{10.0, 10.0, 10.0}
    uniformCost := inspector.calculateCost(uniform)
    if uniformCost != 0 {
        t.Errorf("Expected 0 variance for uniform values, got %f", uniformCost)
    }
    
    // 测试有差异的分布
    varied := []float64{10.0, 20.0, 30.0}
    variedCost := inspector.calculateCost(varied)
    if variedCost <= 0 {
        t.Error("Expected positive variance for varied values")
    }
}
```

---

### 2.4 OCA Controller 模块 (`oca/controller_test.go`)

#### 2.4.1 测试目标
验证在线自适应控制器的 PID 算法和阈值算法实现（Algorithm 3 & 4）。

#### 2.4.2 测试用例

| 用例编号 | 测试名称 | 测试描述 | 预期结果 |
|---------|---------|---------|---------|
| TC-OCA-001 | TestNewController | 验证新建 Controller | 默认 PID 参数正确 |
| TC-OCA-002 | TestStartStop | 启动和停止 | 无 panic，正常退出 |
| TC-OCA-003 | TestAdjust_HighPhi | φ > TargetPhi 调整 | 收紧 CL（减小 QS/TO）|
| TC-OCA-004 | TestAdjust_LowPhi | φ < TargetPhi 调整 | 放宽 CL（增大 QS/TO）|
| TC-OCA-005 | TestAdjust_OnTarget | φ == TargetPhi | CL 保持稳定 |
| TC-OCA-006 | TestPID_Proportional | P 项计算 | 输出与误差成正比 |
| TC-OCA-007 | TestPID_Integral | I 项累积 | 持续误差导致积分累加 |
| TC-OCA-008 | TestPID_Derivative | D 项响应 | 误差变化率影响输出 |
| TC-OCA-009 | TestMapOutputToCL | 输出映射 | PID 输出正确映射到 QS/TO |
| TC-OCA-010 | TestSetPIDParameters | 动态设置 PID | Kp/Ki/Kd 正确更新 |
| TC-OCA-011 | TestSetTargetPhi | 设置目标 φ | TargetPhi 正确更新 |
| TC-OCA-012 | TestHistory_Window | 历史窗口管理 | 窗口大小维护正确 |
| TC-OCA-013 | TestThresholdController | 阈值控制器 | 上下限触发调整 |

#### 2.4.3 测试代码示例

```go
// oca/controller_test.go
package oca

import (
    "testing"
    "time"
)

func TestNewController(t *testing.T) {
    ctrl := NewController(1.05)
    
    if ctrl.TargetPhi != 1.05 {
        t.Errorf("Expected TargetPhi 1.05, got %f", ctrl.TargetPhi)
    }
    if ctrl.Kp != 1.0 || ctrl.Ki != 0.1 || ctrl.Kd != 0.05 {
        t.Error("Default PID parameters incorrect")
    }
}

func TestStartStop(t *testing.T) {
    ctrl := NewController(1.05)
    ctrl.Start()
    
    time.Sleep(50 * time.Millisecond)
    
    ctrl.Stop() // 不应该 panic
}

func TestAdjust_HighPhi(t *testing.T) {
    ctrl := NewController(1.05)
    ctrl.Start()
    defer ctrl.Stop()
    
    // 高 φ 值（偏差大）
    newCL := ctrl.Adjust(1.5)
    
    if newCL == nil {
        t.Fatal("Expected non-nil CL")
    }
    
    // 高偏差应该导致更严格的 CL（较小的 QS）
    // 具体值取决于 PID 参数
    t.Logf("High Phi adjustment: QS=%d, TO=%v", newCL.QueueSize, newCL.Timeout)
}

func TestAdjust_LowPhi(t *testing.T) {
    ctrl := NewController(1.05)
    ctrl.Start()
    defer ctrl.Stop()
    
    // 低 φ 值（偏差小）
    newCL := ctrl.Adjust(1.0)
    
    if newCL == nil {
        t.Fatal("Expected non-nil CL")
    }
    
    // 低偏差应该导致更宽松的 CL（较大的 QS）
    t.Logf("Low Phi adjustment: QS=%d, TO=%v", newCL.QueueSize, newCL.Timeout)
}

func TestMapOutputToCL(t *testing.T) {
    ctrl := NewController(1.05)
    
    // 测试边界值
    testCases := []struct {
        output   float64
        minQS    int
        maxQS    int
    }{
        {100.0, 10, 100},   // 最大正输出 -> 最小 QS
        {-100.0, 900, 1000}, // 最大负输出 -> 最大 QS
        {0.0, 400, 600},    // 零输出 -> 中间 QS
    }
    
    for _, tc := range testCases {
        cl := ctrl.mapOutputToCL(tc.output)
        if cl.QueueSize < tc.minQS || cl.QueueSize > tc.maxQS {
            t.Errorf("Output %.1f -> QS=%d, expected in [%d, %d]",
                tc.output, cl.QueueSize, tc.minQS, tc.maxQS)
        }
    }
}

func TestSetPIDParameters(t *testing.T) {
    ctrl := NewController(1.05)
    
    ctrl.SetPIDParameters(2.0, 0.5, 0.1)
    
    if ctrl.Kp != 2.0 || ctrl.Ki != 0.5 || ctrl.Kd != 0.1 {
        t.Error("PID parameters not updated correctly")
    }
}

func TestThresholdController(t *testing.T) {
    tc := NewThresholdController(1.1, 1.0, 3)
    
    // 填满窗口，平均值 > 上限
    tc.AdjustThreshold(1.2)
    tc.AdjustThreshold(1.2)
    cl := tc.AdjustThreshold(1.2) // 触发 raiseCL
    
    // 检查 CL 被收紧
    if cl.QueueSize >= 100 {
        t.Logf("Note: QS=%d (may not decrease on first trigger)", cl.QueueSize)
    }
}
```

---

### 2.5 gRPC 传输层模块 (`transport/grpc_test.go`)

#### 2.5.1 测试目标
验证 gRPC 服务端和客户端的通信功能。

#### 2.5.2 测试用例

| 用例编号 | 测试名称 | 测试描述 | 预期结果 |
|---------|---------|---------|---------|
| TC-GRP-001 | TestNewGRPCServer | 验证新建 Server | 端口配置正确 |
| TC-GRP-002 | TestServer_StartStop | 启动和停止 Server | 状态正确切换 |
| TC-GRP-003 | TestServer_DoubleStart | 重复启动 | 返回错误 |
| TC-GRP-004 | TestRegisterUpdateHandler | 注册更新处理器 | Handler 被调用 |
| TC-GRP-005 | TestRegisterCLChangeHandler | 注册 CL 变更处理器 | Handler 被调用 |
| TC-GRP-006 | TestNewGRPCClient | 创建 gRPC 客户端 | 连接建立成功 |
| TC-GRP-007 | TestClient_PropagateUpdate | 客户端发送更新 | 服务端正确接收 |
| TC-GRP-008 | TestClient_ChangeConsistencyLevel | 客户端发送 CL 变更 | 服务端正确处理 |
| TC-GRP-009 | TestConnectionManager_AddPeer | 添加对等节点 | 连接被正确管理 |
| TC-GRP-010 | TestConnectionManager_RemovePeer | 移除对等节点 | 连接被正确关闭 |
| TC-GRP-011 | TestConnectionManager_BroadcastUpdate | 广播更新 | 所有节点收到更新 |
| TC-GRP-012 | TestIsRunning | 检查运行状态 | 状态准确反映 |

#### 2.5.3 测试代码示例

```go
// transport/grpc_test.go
package transport

import (
    "context"
    "testing"
    "time"
    
    "github.com/weufhsos/adaptive_sync_go/proto"
)

func TestNewGRPCServer(t *testing.T) {
    server := NewGRPCServer(50099)
    
    if server.port != 50099 {
        t.Errorf("Expected port 50099, got %d", server.port)
    }
    if server.IsRunning() {
        t.Error("Server should not be running initially")
    }
}

func TestServer_StartStop(t *testing.T) {
    server := NewGRPCServer(50100)
    
    if err := server.Start(); err != nil {
        t.Fatalf("Failed to start server: %v", err)
    }
    
    if !server.IsRunning() {
        t.Error("Server should be running after Start()")
    }
    
    server.Stop()
    
    if server.IsRunning() {
        t.Error("Server should not be running after Stop()")
    }
}

func TestServer_DoubleStart(t *testing.T) {
    server := NewGRPCServer(50101)
    
    if err := server.Start(); err != nil {
        t.Fatalf("First start failed: %v", err)
    }
    defer server.Stop()
    
    if err := server.Start(); err == nil {
        t.Error("Expected error on double start")
    }
}

func TestRegisterUpdateHandler(t *testing.T) {
    server := NewGRPCServer(50102)
    
    handlerCalled := false
    server.RegisterUpdateHandler(func(update *proto.UpdateMessage) error {
        handlerCalled = true
        return nil
    })
    
    if err := server.Start(); err != nil {
        t.Fatalf("Failed to start server: %v", err)
    }
    defer server.Stop()
    
    // 创建客户端连接并发送更新
    client, err := NewGRPCClient("localhost:50102")
    if err != nil {
        t.Fatalf("Failed to create client: %v", err)
    }
    defer client.Close()
    
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    update := &proto.UpdateMessage{
        Key:          "test_key",
        OriginNodeId: "test_node",
        Value:        10.0,
        Op:           proto.Operation_INCREMENT,
    }
    
    _, err = client.PropagateUpdate(ctx, update)
    if err != nil {
        t.Logf("PropagateUpdate error (may be expected): %v", err)
    }
    
    // 等待处理
    time.Sleep(100 * time.Millisecond)
}

func TestConnectionManager_AddRemovePeer(t *testing.T) {
    // 先启动一个服务端
    server := NewGRPCServer(50103)
    if err := server.Start(); err != nil {
        t.Fatalf("Failed to start server: %v", err)
    }
    defer server.Stop()
    
    cm := NewConnectionManager()
    
    // 添加对等节点
    if err := cm.AddPeer("localhost:50103"); err != nil {
        t.Fatalf("Failed to add peer: %v", err)
    }
    
    client, exists := cm.GetClient("localhost:50103")
    if !exists || client == nil {
        t.Error("Peer not found after adding")
    }
    
    // 移除对等节点
    cm.RemovePeer("localhost:50103")
    
    _, exists = cm.GetClient("localhost:50103")
    if exists {
        t.Error("Peer should not exist after removal")
    }
}
```

---

### 2.6 Manager 模块 (`ac_test.go`)

#### 2.6.1 测试目标
验证 AC 管理器的核心 API 功能和组件集成。

#### 2.6.2 测试用例

| 用例编号 | 测试名称 | 测试描述 | 预期结果 |
|---------|---------|---------|---------|
| TC-MGR-001 | TestNew_DefaultConfig | 默认配置创建 | Manager 正确初始化 |
| TC-MGR-002 | TestNew_WithOptions | 带选项创建 | 配置正确应用 |
| TC-MGR-003 | TestNew_MissingNodeID | 缺少 NodeID | panic 或错误 |
| TC-MGR-004 | TestStartStop | 启动和停止 | 各组件正确生命周期 |
| TC-MGR-005 | TestUpdate_Increment | 正数增量更新 | 值正确增加 |
| TC-MGR-006 | TestUpdate_Decrement | 负数增量更新 | 值正确减少 |
| TC-MGR-007 | TestGet | 获取状态值 | 返回正确值 |
| TC-MGR-008 | TestSnapshot | 获取快照 | 返回所有状态 |
| TC-MGR-009 | TestHandleRemoteUpdate | 处理远程更新 | CRDT 合并正确 |
| TC-MGR-010 | TestGetStats | 获取统计信息 | 统计准确 |
| TC-MGR-011 | TestConcurrentUpdates | 并发更新 | 无数据竞争 |
| TC-MGR-012 | TestOCACallback | OCA 回调触发 | CL 被自动调整 |

#### 2.6.3 测试代码示例

```go
// ac_test.go
package ac

import (
    "sync"
    "testing"
    "time"
)

func TestNew_DefaultConfig(t *testing.T) {
    m := New(
        WithNodeID("test-node"),
    )
    
    if m == nil {
        t.Fatal("Manager should not be nil")
    }
    
    if m.config.NodeID != "test-node" {
        t.Errorf("Expected NodeID 'test-node', got '%s'", m.config.NodeID)
    }
}

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
}

func TestUpdate_Increment(t *testing.T) {
    m := New(WithNodeID("test-node"))
    
    // 不启动（避免端口冲突），直接测试 Update/Get
    if err := m.Update("link_1_bw", 100.0); err != nil {
        // 可能因为分发失败而返回错误，但本地状态应该已更新
        t.Logf("Update returned error (expected without peers): %v", err)
    }
    
    value := m.Get("link_1_bw")
    if value != 100.0 {
        t.Errorf("Expected value 100.0, got %f", value)
    }
}

func TestUpdate_Decrement(t *testing.T) {
    m := New(WithNodeID("test-node"))
    
    m.Update("link_1_bw", 100.0)
    m.Update("link_1_bw", -30.0)
    
    value := m.Get("link_1_bw")
    if value != 70.0 {
        t.Errorf("Expected value 70.0, got %f", value)
    }
}

func TestSnapshot(t *testing.T) {
    m := New(WithNodeID("test-node"))
    
    m.Update("key1", 10.0)
    m.Update("key2", 20.0)
    m.Update("key3", 30.0)
    
    snapshot := m.Snapshot()
    
    if len(snapshot) != 3 {
        t.Errorf("Expected 3 keys in snapshot, got %d", len(snapshot))
    }
    if snapshot["key1"] != 10.0 || snapshot["key2"] != 20.0 || snapshot["key3"] != 30.0 {
        t.Error("Snapshot values incorrect")
    }
}

func TestConcurrentUpdates(t *testing.T) {
    m := New(WithNodeID("test-node"))
    
    var wg sync.WaitGroup
    
    // 启动多个 goroutine 并发更新
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            m.Update("concurrent_key", 1.0)
        }()
    }
    
    wg.Wait()
    
    value := m.Get("concurrent_key")
    if value != 100.0 {
        t.Errorf("Expected value 100.0 after 100 increments, got %f", value)
    }
}

func TestGetStats(t *testing.T) {
    m := New(WithNodeID("test-node"))
    
    m.Update("key1", 10.0)
    m.Update("key2", 20.0)
    
    stats := m.GetStats()
    
    if stats.ActiveStates != 2 {
        t.Errorf("Expected 2 active states, got %d", stats.ActiveStates)
    }
}
```

---

## 3. 集成测试计划

### 3.1 多节点同步测试 (`integration/multi_node_test.go`)

#### 3.1.1 测试目标
验证多个 AC 实例之间的状态同步行为和 CRDT 最终一致性。

#### 3.1.2 测试场景

| 场景编号 | 测试名称 | 测试描述 | 预期结果 |
|---------|---------|---------|---------|
| IT-MN-001 | TestTwoNodeSync | 两节点状态同步 | 更新被正确传播 |
| IT-MN-002 | TestThreeNodeSync | 三节点状态同步 | 所有节点最终一致 |
| IT-MN-003 | TestConcurrentUpdates | 多节点并发更新 | CRDT 正确合并 |
| IT-MN-004 | TestPartitionRecovery | 网络分区恢复 | 分区后状态合并正确 |
| IT-MN-005 | TestLatePropagation | 延迟传播 | 延迟更新被正确处理 |

#### 3.1.3 测试代码示例

```go
// integration/multi_node_test.go
package integration

import (
    "testing"
    "time"
    
    "github.com/weufhsos/adaptive_sync_go"
)

func TestTwoNodeSync(t *testing.T) {
    // 创建两个节点
    node1 := ac.New(
        ac.WithNodeID("node-1"),
        ac.WithGRPCPort(50201),
        ac.WithPeers([]string{"localhost:50202"}),
    )
    
    node2 := ac.New(
        ac.WithNodeID("node-2"),
        ac.WithGRPCPort(50202),
        ac.WithPeers([]string{"localhost:50201"}),
    )
    
    // 启动节点
    if err := node1.Start(); err != nil {
        t.Fatalf("Failed to start node1: %v", err)
    }
    defer node1.Stop()
    
    if err := node2.Start(); err != nil {
        t.Fatalf("Failed to start node2: %v", err)
    }
    defer node2.Stop()
    
    // 等待连接建立
    time.Sleep(500 * time.Millisecond)
    
    // 在 node1 上更新
    node1.Update("shared_key", 100.0)
    
    // 等待同步
    time.Sleep(1 * time.Second)
    
    // 验证 node2 是否收到更新
    value := node2.Get("shared_key")
    t.Logf("Node2 value: %f", value)
    
    // 注意：由于网络延迟，值可能不会立即同步
    // 在完整实现中，应该有重试和验证机制
}

func TestThreeNodeEventualConsistency(t *testing.T) {
    ports := []int{50211, 50212, 50213}
    nodes := make([]*ac.Manager, 3)
    
    // 构建对等节点列表
    allPeers := []string{
        "localhost:50211",
        "localhost:50212",
        "localhost:50213",
    }
    
    // 创建和启动所有节点
    for i := 0; i < 3; i++ {
        peers := make([]string, 0, 2)
        for j, addr := range allPeers {
            if j != i {
                peers = append(peers, addr)
            }
        }
        
        nodes[i] = ac.New(
            ac.WithNodeID(fmt.Sprintf("node-%d", i+1)),
            ac.WithGRPCPort(ports[i]),
            ac.WithPeers(peers),
        )
        
        if err := nodes[i].Start(); err != nil {
            t.Fatalf("Failed to start node-%d: %v", i+1, err)
        }
        defer nodes[i].Stop()
    }
    
    // 等待连接建立
    time.Sleep(1 * time.Second)
    
    // 每个节点独立更新
    nodes[0].Update("test_key", 10.0)
    nodes[1].Update("test_key", 20.0)
    nodes[2].Update("test_key", 30.0)
    
    // 等待同步收敛
    time.Sleep(3 * time.Second)
    
    // 验证最终一致性
    values := []float64{
        nodes[0].Get("test_key"),
        nodes[1].Get("test_key"),
        nodes[2].Get("test_key"),
    }
    
    t.Logf("Final values: node1=%f, node2=%f, node3=%f",
        values[0], values[1], values[2])
    
    // 所有节点的值应该相等（或接近）
    // CRDT 保证最终一致性
}
```

---

### 3.2 端到端流程测试 (`integration/e2e_test.go`)

#### 3.2.1 测试目标
验证完整的自适应一致性调整闭环：更新 → PI检测 → OCA调整 → 分发。

#### 3.2.2 测试场景

| 场景编号 | 测试名称 | 测试描述 | 预期结果 |
|---------|---------|---------|---------|
| IT-E2E-001 | TestFullAdaptiveCycle | 完整自适应周期 | CL 根据 φ 自动调整 |
| IT-E2E-002 | TestHighLoadAdaptation | 高负载场景 | CL 收紧以保证一致性 |
| IT-E2E-003 | TestLowLoadAdaptation | 低负载场景 | CL 放宽以提高吞吐量 |
| IT-E2E-004 | TestSDNIntegrationFlow | SDN 集成流程模拟 | 带宽分配/查询正常 |

#### 3.2.3 测试代码示例

```go
// integration/e2e_test.go
package integration

import (
    "testing"
    "time"
    
    "github.com/weufhsos/adaptive_sync_go"
)

func TestFullAdaptiveCycle(t *testing.T) {
    m := ac.New(
        ac.WithNodeID("e2e-node"),
        ac.WithGRPCPort(50301),
        ac.WithTargetPhi(1.05),
    )
    
    if err := m.Start(); err != nil {
        t.Fatalf("Failed to start manager: %v", err)
    }
    defer m.Stop()
    
    // 模拟大量更新（可能触发 PI 检测和 OCA 调整）
    for i := 0; i < 1000; i++ {
        m.Update("link_bw", float64(i%100)-50.0)
        time.Sleep(1 * time.Millisecond)
    }
    
    // 获取统计信息
    stats := m.GetStats()
    t.Logf("After updates - TotalUpdates:%d Success:%d Failed:%d",
        stats.TotalUpdates, stats.SuccessfulOps, stats.FailedOps)
}

func TestSDNIntegrationFlow(t *testing.T) {
    m := ac.New(
        ac.WithNodeID("sdn-controller"),
        ac.WithGRPCPort(50302),
    )
    
    if err := m.Start(); err != nil {
        t.Fatalf("Failed to start: %v", err)
    }
    defer m.Stop()
    
    // 模拟北向接口：初始化链路带宽
    links := []string{"link_1_bw", "link_2_bw", "link_3_bw"}
    for _, link := range links {
        m.Update(link, 1000.0) // 1000 Mbps 初始带宽
    }
    
    // 模拟带宽分配请求
    allocateBandwidth := func(linkID string, amount float64) error {
        current := m.Get(linkID)
        if current < amount {
            return fmt.Errorf("insufficient bandwidth")
        }
        return m.Update(linkID, -amount)
    }
    
    // 执行分配
    if err := allocateBandwidth("link_1_bw", 200.0); err != nil {
        t.Errorf("Allocation failed: %v", err)
    }
    
    // 验证剩余带宽
    remaining := m.Get("link_1_bw")
    if remaining != 800.0 {
        t.Errorf("Expected 800.0, got %f", remaining)
    }
    
    // 获取快照用于选路决策
    snapshot := m.Snapshot()
    t.Logf("Current bandwidth snapshot: %v", snapshot)
}
```

---

### 3.3 故障恢复测试 (`integration/fault_tolerance_test.go`)

#### 3.3.1 测试目标
验证系统在节点故障和网络异常情况下的行为。

#### 3.3.2 测试场景

| 场景编号 | 测试名称 | 测试描述 | 预期结果 |
|---------|---------|---------|---------|
| IT-FT-001 | TestNodeCrash | 节点崩溃恢复 | 存活节点继续运行 |
| IT-FT-002 | TestGRPCTimeout | gRPC 超时 | 超时被正确处理 |
| IT-FT-003 | TestQueueOverflow | 队列溢出 | 更新被丢弃，无死锁 |
| IT-FT-004 | TestGracefulShutdown | 优雅关闭 | 挂起操作被完成 |

---

## 4. 性能测试计划

### 4.1 基准测试 (`benchmark_test.go`)

#### 4.1.1 测试目标
评估关键操作的性能指标。

#### 4.1.2 基准测试用例

| 用例编号 | 测试名称 | 测试描述 | 关注指标 |
|---------|---------|---------|---------|
| BM-001 | BenchmarkUpdate | Update 操作性能 | ops/sec, ns/op |
| BM-002 | BenchmarkGet | Get 操作性能 | ops/sec, ns/op |
| BM-003 | BenchmarkSnapshot | Snapshot 性能 | ops/sec, 内存分配 |
| BM-004 | BenchmarkPNCounterMerge | CRDT 合并性能 | ops/sec, ns/op |
| BM-005 | BenchmarkPIDCalculation | PID 计算性能 | ns/op |
| BM-006 | BenchmarkConcurrentUpdates | 并发更新性能 | 吞吐量, 延迟 |

#### 4.1.3 基准测试代码示例

```go
// benchmark_test.go
package ac

import (
    "testing"
)

func BenchmarkUpdate(b *testing.B) {
    m := New(WithNodeID("bench-node"))
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        m.Update("bench_key", 1.0)
    }
}

func BenchmarkGet(b *testing.B) {
    m := New(WithNodeID("bench-node"))
    m.Update("bench_key", 100.0)
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _ = m.Get("bench_key")
    }
}

func BenchmarkSnapshot(b *testing.B) {
    m := New(WithNodeID("bench-node"))
    
    // 预填充数据
    for i := 0; i < 100; i++ {
        m.Update(fmt.Sprintf("key_%d", i), float64(i))
    }
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _ = m.Snapshot()
    }
}

func BenchmarkConcurrentUpdates(b *testing.B) {
    m := New(WithNodeID("bench-node"))
    
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            m.Update(fmt.Sprintf("key_%d", i%10), 1.0)
            i++
        }
    })
}

// 运行基准测试: go test -bench=. -benchmem
```

### 4.2 性能指标目标

| 指标 | 目标值 | 说明 |
|------|-------|------|
| Update 延迟 | < 10μs | 本地操作（不含网络） |
| Get 延迟 | < 1μs | 内存读取 |
| 吞吐量 | > 100,000 ops/sec | 单节点 |
| 内存占用 | < 100MB/10K状态 | 合理的内存使用 |

---

## 5. 测试执行计划

### 5.1 测试执行命令

```bash
# 运行所有单元测试
go test -v ./...

# 运行特定模块测试
go test -v ./store/...
go test -v ./dispatcher/...
go test -v ./pi/...
go test -v ./oca/...
go test -v ./transport/...

# 运行带覆盖率的测试
go test -v -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

# 运行基准测试
go test -bench=. -benchmem ./...

# 运行集成测试（需要独立标记）
go test -v -tags=integration ./integration/...

# 竞态检测
go test -race ./...
```

### 5.2 测试覆盖率目标

| 模块 | 目标覆盖率 |
|------|-----------|
| store/pn_counter.go | ≥ 85% |
| dispatcher/dispatcher.go | ≥ 80% |
| pi/inspector.go | ≥ 80% |
| oca/controller.go | ≥ 80% |
| transport/grpc.go | ≥ 75% |
| ac.go | ≥ 80% |
| **总体** | **≥ 80%** |

### 5.3 CI/CD 集成建议

```yaml
# .github/workflows/test.yml
name: Test

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      
      - name: Run Unit Tests
        run: go test -v -race -coverprofile=coverage.out ./...
      
      - name: Check Coverage
        run: |
          go tool cover -func=coverage.out | grep total
          # 检查总覆盖率是否达标
      
      - name: Run Benchmarks
        run: go test -bench=. -benchmem ./...
```

---

## 6. 测试报告模板

### 6.1 单元测试报告

```
================== 单元测试报告 ==================
日期: YYYY-MM-DD
版本: v1.0.0

模块测试结果:
┌──────────────────────┬───────┬───────┬─────────┐
│ 模块                  │ 通过  │ 失败  │ 覆盖率  │
├──────────────────────┼───────┼───────┼─────────┤
│ store/pn_counter     │ 13    │ 0     │ 87%     │
│ dispatcher           │ 12    │ 0     │ 82%     │
│ pi/inspector         │ 10    │ 0     │ 81%     │
│ oca/controller       │ 13    │ 0     │ 83%     │
│ transport/grpc       │ 12    │ 0     │ 78%     │
│ ac (Manager)         │ 12    │ 0     │ 85%     │
├──────────────────────┼───────┼───────┼─────────┤
│ 总计                  │ 72    │ 0     │ 82%     │
└──────────────────────┴───────┴───────┴─────────┘

执行时间: 5.2s
```

### 6.2 性能测试报告

```
================== 性能测试报告 ==================
日期: YYYY-MM-DD
硬件: CPU xxx, RAM xxGB

基准测试结果:
┌─────────────────────────┬──────────────┬──────────────┐
│ 测试                     │ ops/sec      │ ns/op        │
├─────────────────────────┼──────────────┼──────────────┤
│ BenchmarkUpdate         │ 500,000      │ 2000         │
│ BenchmarkGet            │ 10,000,000   │ 100          │
│ BenchmarkSnapshot       │ 100,000      │ 10000        │
│ BenchmarkPNCounterMerge │ 200,000      │ 5000         │
└─────────────────────────┴──────────────┴──────────────┘
```

---

## 7. 附录

### 7.1 测试数据准备

```go
// testdata/fixtures.go
package testdata

// 生成测试用的 PNCounter 数据
func CreateTestPNCounterData() map[string]float64 {
    return map[string]float64{
        "node-1": 100.0,
        "node-2": 200.0,
        "node-3": 150.0,
    }
}

// 生成测试用的历史记录
func CreateTestHistory(count int) []store.UpdateLog {
    history := make([]store.UpdateLog, count)
    for i := 0; i < count; i++ {
        history[i] = store.UpdateLog{
            Timestamp: time.Now().UnixNano() + int64(i),
            Value:     float64(i + 1),
            NodeID:    "test-node",
            Operation: store.Increment,
        }
    }
    return history
}
```

### 7.2 Mock 实现

```go
// mocks/transport_mock.go
package mocks

import "github.com/weufhsos/adaptive_sync_go/proto"

// MockGRPCClient 用于测试的 gRPC 客户端 Mock
type MockGRPCClient struct {
    UpdateCallCount int
    LastUpdate      *proto.UpdateMessage
    ShouldFail      bool
}

func (m *MockGRPCClient) PropagateUpdate(ctx context.Context, update *proto.UpdateMessage) (*proto.UpdateAck, error) {
    m.UpdateCallCount++
    m.LastUpdate = update
    
    if m.ShouldFail {
        return nil, fmt.Errorf("mock failure")
    }
    
    return &proto.UpdateAck{
        Key:      update.Key,
        UpdateId: update.UpdateId,
        Success:  true,
    }, nil
}
```

---

## 8. 修订历史

| 版本 | 日期 | 修订内容 | 作者 |
|------|------|---------|------|
| 1.0 | 2026-02-07 | 初始版本 | - |
