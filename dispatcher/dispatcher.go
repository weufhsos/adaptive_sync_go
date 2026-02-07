package dispatcher

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/weufhsos/adaptive_sync_go/proto"
)

// ConsistencyLevel 一致性级别配置
type ConsistencyLevel struct {
	QueueSize int           // CL_QS: 队列大小阈值
	Timeout   time.Duration // CL_TO: 超时时间
}

// UpdateRequest 更新请求结构
type UpdateRequest struct {
	Key       string
	Delta     float64
	Timestamp int64
	NodeID    string
	UpdateID  string
}

// Dispatcher 实现论文中的Algorithm 5: Fast and Batched Distribution
type Dispatcher struct {
	// 配置
	nodeID string
	peers  []string

	// 状态管理
	currentCL ConsistencyLevel
	clMutex   sync.RWMutex

	// 分发队列
	queue     chan *UpdateRequest
	queueSize int // 当前队列大小

	// 控制通道
	stopChan    chan struct{}
	stoppedChan chan struct{}

	// gRPC客户端（用于向其他节点发送更新）
	grpcClients map[string]proto.ACServiceClient
	clientMutex sync.RWMutex

	// 统计信息
	stats      *DispatcherStats
	statsMutex sync.RWMutex

	// 回调函数
	onUpdateProcessed func(key string, success bool)
}

// DispatcherStats 分发器统计信息
type DispatcherStats struct {
	TotalUpdates    int64
	SuccessfulSends int64
	FailedSends     int64
	QueueFullDrops  int64
	BatchedSends    int64
	FastModeSends   int64
	LastSendTime    time.Time
	AverageLatency  time.Duration
}

// NewDispatcher 创建新的分发控制器
func NewDispatcher(nodeID string, peers []string) *Dispatcher {
	d := &Dispatcher{
		nodeID:      nodeID,
		peers:       peers,
		queue:       make(chan *UpdateRequest, 1000), // 默认队列大小
		stopChan:    make(chan struct{}),
		stoppedChan: make(chan struct{}),
		grpcClients: make(map[string]proto.ACServiceClient),
		stats:       &DispatcherStats{},
		currentCL: ConsistencyLevel{
			QueueSize: 100,                   // 默认队列大小
			Timeout:   50 * time.Millisecond, // 默认超时
		},
	}

	// 初始化统计信息
	d.stats.LastSendTime = time.Now()

	return d
}

// Start 启动分发控制器
func (d *Dispatcher) Start() error {
	log.Printf("[Dispatcher] Starting dispatcher for node %s", d.nodeID)

	// 启动主分发循环
	go d.run()

	// 启动统计监控协程
	go d.monitorStats()

	return nil
}

// Stop 停止分发控制器
func (d *Dispatcher) Stop() {
	log.Printf("[Dispatcher] Stopping dispatcher for node %s", d.nodeID)

	close(d.stopChan)

	// 等待停止完成
	<-d.stoppedChan

	// 清理gRPC连接
	d.clientMutex.Lock()
	for _, client := range d.grpcClients {
		// 注意：gRPC客户端通常不需要显式关闭，连接池会自动管理
		_ = client
	}
	d.clientMutex.Unlock()

	log.Printf("[Dispatcher] Dispatcher stopped for node %s", d.nodeID)
}

// Enqueue 将更新加入分发队列（准入控制点）
// 根据论文要求，在写入队列前检查队列长度
func (d *Dispatcher) Enqueue(key string, delta float64, updateID string) error {
	d.clMutex.RLock()
	maxQueueSize := d.currentCL.QueueSize
	d.clMutex.RUnlock()

	// 检查队列是否已满（有界过时性保证）
	if len(d.queue) >= maxQueueSize {
		d.statsMutex.Lock()
		d.stats.QueueFullDrops++
		d.statsMutex.Unlock()

		return fmt.Errorf("dispatch queue full (size: %d, max: %d), update dropped to maintain bounded staleness",
			len(d.queue), maxQueueSize)
	}

	// 创建更新请求
	request := &UpdateRequest{
		Key:       key,
		Delta:     delta,
		Timestamp: time.Now().UnixNano(),
		NodeID:    d.nodeID,
		UpdateID:  updateID,
	}

	// 尝试加入队列（非阻塞）
	select {
	case d.queue <- request:
		d.statsMutex.Lock()
		d.stats.TotalUpdates++
		d.statsMutex.Unlock()
		return nil
	default:
		// 队列满了（理论上不应该发生，因为我们已经检查过了）
		d.statsMutex.Lock()
		d.stats.QueueFullDrops++
		d.statsMutex.Unlock()
		return fmt.Errorf("failed to enqueue update, queue is full")
	}
}

// SetConsistencyLevel 动态调整一致性级别（由OCA模块调用）
func (d *Dispatcher) SetConsistencyLevel(cl ConsistencyLevel) {
	d.clMutex.Lock()
	oldCL := d.currentCL
	d.currentCL = cl
	d.clMutex.Unlock()

	log.Printf("[Dispatcher] CL changed from QS=%d,TO=%v to QS=%d,TO=%v",
		oldCL.QueueSize, oldCL.Timeout,
		cl.QueueSize, cl.Timeout)
}

// GetCurrentConsistencyLevel 获取当前一致性级别
func (d *Dispatcher) GetCurrentConsistencyLevel() ConsistencyLevel {
	d.clMutex.RLock()
	defer d.clMutex.RUnlock()
	return d.currentCL
}

// SetOnUpdateProcessed 设置更新处理回调
func (d *Dispatcher) SetOnUpdateProcessed(callback func(key string, success bool)) {
	d.onUpdateProcessed = callback
}

// run 主分发循环（实现Algorithm 5）
func (d *Dispatcher) run() {
	defer close(d.stoppedChan)

	log.Printf("[Dispatcher] Main dispatch loop started")

	// 创建定时器
	d.clMutex.RLock()
	ticker := time.NewTicker(d.currentCL.Timeout)
	d.clMutex.RUnlock()
	defer ticker.Stop()

	var batch []*UpdateRequest

	for {
		select {
		case <-d.stopChan:
			// 处理剩余的批量更新
			if len(batch) > 0 {
				d.sendBatch(batch)
			}
			return

		case request := <-d.queue:
			// Fast-Mode: 队列有数据且未达到上限时立即发送
			d.clMutex.RLock()
			queueSizeLimit := d.currentCL.QueueSize
			d.clMutex.RUnlock()

			if len(d.queue) < queueSizeLimit {
				// Fast模式：立即发送单个更新
				d.sendSingle(request)
				d.statsMutex.Lock()
				d.stats.FastModeSends++
				d.statsMutex.Unlock()
			} else {
				// 批处理模式：加入批次等待发送
				batch = append(batch, request)

				// 如果批次达到队列大小限制，立即发送
				if len(batch) >= queueSizeLimit {
					d.sendBatch(batch)
					batch = nil // 重置批次

					d.statsMutex.Lock()
					d.stats.BatchedSends++
					d.statsMutex.Unlock()
				}
			}

		case <-ticker.C:
			// Batched-Mode: 定时器到期，发送累积的批次
			if len(batch) > 0 {
				d.sendBatch(batch)
				batch = nil // 重置批次

				d.statsMutex.Lock()
				d.stats.BatchedSends++
				d.statsMutex.Unlock()
			}

			// 重新创建定时器（可能CL已更新）
			d.clMutex.RLock()
			ticker.Reset(d.currentCL.Timeout)
			d.clMutex.RUnlock()
		}
	}
}

// sendSingle 发送单个更新（Fast-Mode）
func (d *Dispatcher) sendSingle(request *UpdateRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	op := proto.Operation_INCREMENT
	if request.Delta < 0 {
		op = proto.Operation_DECREMENT
		request.Delta = -request.Delta
	}

	updateMsg := &proto.UpdateMessage{
		Key:          request.Key,
		OriginNodeId: request.NodeID,
		Timestamp:    request.Timestamp,
		Op:           op,
		Value:        request.Delta,
		UpdateId:     request.UpdateID,
	}

	success := d.broadcastUpdate(ctx, updateMsg)

	// 调用回调函数
	if d.onUpdateProcessed != nil {
		d.onUpdateProcessed(request.Key, success)
	}

	// 更新统计
	d.statsMutex.Lock()
	if success {
		d.stats.SuccessfulSends++
	} else {
		d.stats.FailedSends++
	}
	d.stats.LastSendTime = time.Now()
	d.statsMutex.Unlock()
}

// sendBatch 发送批次更新（Batched-Mode）
func (d *Dispatcher) sendBatch(batch []*UpdateRequest) {
	if len(batch) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 构建批次消息
	var updateMsgs []*proto.UpdateMessage
	for _, request := range batch {
		op := proto.Operation_INCREMENT
		if request.Delta < 0 {
			op = proto.Operation_DECREMENT
			request.Delta = -request.Delta
		}

		updateMsg := &proto.UpdateMessage{
			Key:          request.Key,
			OriginNodeId: request.NodeID,
			Timestamp:    request.Timestamp,
			Op:           op,
			Value:        request.Delta,
			UpdateId:     request.UpdateID,
		}
		updateMsgs = append(updateMsgs, updateMsg)
	}

	// 广播批次更新
	successCount := 0
	for _, updateMsg := range updateMsgs {
		if d.broadcastUpdate(ctx, updateMsg) {
			successCount++
		}
	}

	// 调用回调函数（为每个请求调用）
	if d.onUpdateProcessed != nil {
		for _, request := range batch {
			d.onUpdateProcessed(request.Key, successCount > 0)
		}
	}

	// 更新统计
	d.statsMutex.Lock()
	d.stats.SuccessfulSends += int64(successCount)
	d.stats.FailedSends += int64(len(batch) - successCount)
	d.stats.LastSendTime = time.Now()
	d.statsMutex.Unlock()

	log.Printf("[Dispatcher] Sent batch of %d updates, %d successful", len(batch), successCount)
}

// broadcastUpdate 向所有对等节点广播更新
func (d *Dispatcher) broadcastUpdate(ctx context.Context, update *proto.UpdateMessage) bool {
	d.clientMutex.RLock()
	defer d.clientMutex.RUnlock()

	success := true

	// 向所有对等节点发送更新
	for peerAddr, client := range d.grpcClients {
		// 创建流
		stream, err := client.PropagateUpdate(ctx)
		if err != nil {
			log.Printf("[Dispatcher] Failed to create stream to %s: %v", peerAddr, err)
			success = false
			continue
		}

		// 发送更新
		if err := stream.Send(update); err != nil {
			log.Printf("[Dispatcher] Failed to send update to %s: %v", peerAddr, err)
			success = false
			continue
		}

		// 接收确认
		_, err = stream.Recv()
		if err != nil {
			log.Printf("[Dispatcher] Failed to receive ack from %s: %v", peerAddr, err)
			success = false
		}

		// 关闭流
		stream.CloseSend()
	}

	return success
}

// AddPeer 添加对等节点
func (d *Dispatcher) AddPeer(peerAddr string, client proto.ACServiceClient) {
	d.clientMutex.Lock()
	defer d.clientMutex.Unlock()
	d.grpcClients[peerAddr] = client
	log.Printf("[Dispatcher] Added peer: %s", peerAddr)
}

// RemovePeer 移除对等节点
func (d *Dispatcher) RemovePeer(peerAddr string) {
	d.clientMutex.Lock()
	defer d.clientMutex.Unlock()
	delete(d.grpcClients, peerAddr)
	log.Printf("[Dispatcher] Removed peer: %s", peerAddr)
}

// GetStats 获取分发器统计信息
func (d *Dispatcher) GetStats() *DispatcherStats {
	d.statsMutex.RLock()
	defer d.statsMutex.RUnlock()

	// 返回副本
	stats := *d.stats
	return &stats
}

// monitorStats 监控统计信息
func (d *Dispatcher) monitorStats() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopChan:
			return
		case <-ticker.C:
			stats := d.GetStats()
			log.Printf("[Dispatcher] Stats - Total:%d Success:%d Failed:%d Drops:%d Batched:%d Fast:%d",
				stats.TotalUpdates, stats.SuccessfulSends, stats.FailedSends,
				stats.QueueFullDrops, stats.BatchedSends, stats.FastModeSends)
		}
	}
}
