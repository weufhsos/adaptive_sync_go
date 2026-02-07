package store

import (
	"sync"
	"time"
)

// UpdateLog 记录每次更新的历史信息，用于PI模块的回溯计算
type UpdateLog struct {
	Timestamp int64     // 纳秒级时间戳
	Value     float64   // 更新值
	NodeID    string    // 节点ID
	Operation Operation // 操作类型
}

// Operation 操作类型
type Operation int

const (
	Increment Operation = iota
	Decrement
)

// PNCounter 实现论文中的PN-Counter（Positive-Negative Counter）
// 支持分布式环境下的最终一致性计数器
type PNCounter struct {
	sync.RWMutex

	// 核心数据结构
	Key  string
	Incr map[string]float64 // Map[NodeID]Value - 正数计数
	Decr map[string]float64 // Map[NodeID]Value - 负数计数

	// 本地缓冲区（未同步的增量）
	LocalIncr float64
	LocalDecr float64

	// 历史日志，用于PI模块回溯计算
	History []UpdateLog

	// 配置参数
	maxHistorySize int // 最大历史记录数，防止内存无限增长
}

// NewPNCounter 创建一个新的PN-Counter实例
func NewPNCounter(key string) *PNCounter {
	return &PNCounter{
		Key:            key,
		Incr:           make(map[string]float64),
		Decr:           make(map[string]float64),
		History:        make([]UpdateLog, 0),
		maxHistorySize: 10000, // 默认保存最近10000条记录
	}
}

// Increment 增加计数（本地操作）
func (pn *PNCounter) Increment(nodeID string, value float64) {
	pn.Lock()
	defer pn.Unlock()

	// 更新本地缓冲区
	pn.LocalIncr += value

	// 记录到历史日志
	pn.recordHistory(value, nodeID, Increment)

	// 更新本地视图
	pn.Incr[nodeID] += value
}

// Decrement 减少计数（本地操作）
func (pn *PNCounter) Decrement(nodeID string, value float64) {
	pn.Lock()
	defer pn.Unlock()

	// 更新本地缓冲区
	pn.LocalDecr += value

	// 记录到历史日志
	pn.recordHistory(value, nodeID, Decrement)

	// 更新本地视图
	pn.Decr[nodeID] += value
}

// Merge 合并来自远程节点的状态（CRDT核心操作）
// 根据论文Algorithm 1，取各节点各维度的最大值
func (pn *PNCounter) Merge(remoteIncr, remoteDecr map[string]float64, remoteNodeID string) {
	pn.Lock()
	defer pn.Unlock()

	// 对于每个节点的Incr值，取最大值
	for nodeID, remoteVal := range remoteIncr {
		if localVal, exists := pn.Incr[nodeID]; !exists || remoteVal > localVal {
			pn.Incr[nodeID] = remoteVal
		}
	}

	// 对于每个节点的Decr值，取最大值
	for nodeID, remoteVal := range remoteDecr {
		if localVal, exists := pn.Decr[nodeID]; !exists || remoteVal > localVal {
			pn.Decr[nodeID] = remoteVal
		}
	}

	// 记录合并操作到历史（用于PI模块）
	pn.recordMergeHistory(remoteIncr, remoteDecr, remoteNodeID)
}

// Value 查询当前计数值（sum(Incr) - sum(Decr)）
func (pn *PNCounter) Value() float64 {
	pn.RLock()
	defer pn.RUnlock()

	incrSum := 0.0
	decrSum := 0.0

	// 计算所有节点Incr的总和
	for _, val := range pn.Incr {
		incrSum += val
	}

	// 计算所有节点Decr的总和
	for _, val := range pn.Decr {
		decrSum += val
	}

	return incrSum - decrSum
}

// GetLocalDelta 获取本地未同步的增量（用于分发）
func (pn *PNCounter) GetLocalDelta() (incr, decr float64) {
	pn.RLock()
	defer pn.RUnlock()
	return pn.LocalIncr, pn.LocalDecr
}

// ClearLocalDelta 清除已同步的本地增量
func (pn *PNCounter) ClearLocalDelta() {
	pn.Lock()
	defer pn.Unlock()
	pn.LocalIncr = 0
	pn.LocalDecr = 0
}

// GetState 获取当前完整状态（用于序列化和网络传输）
func (pn *PNCounter) GetState() (map[string]float64, map[string]float64) {
	pn.RLock()
	defer pn.RUnlock()

	// 深拷贝避免外部修改
	incrCopy := make(map[string]float64)
	decrCopy := make(map[string]float64)

	for k, v := range pn.Incr {
		incrCopy[k] = v
	}
	for k, v := range pn.Decr {
		decrCopy[k] = v
	}

	return incrCopy, decrCopy
}

// GetHistory 获取历史记录（用于PI模块）
func (pn *PNCounter) GetHistory() []UpdateLog {
	pn.RLock()
	defer pn.RUnlock()

	// 返回副本避免外部修改
	history := make([]UpdateLog, len(pn.History))
	copy(history, pn.History)
	return history
}

// recordHistory 记录本地操作到历史日志
func (pn *PNCounter) recordHistory(value float64, nodeID string, op Operation) {
	log := UpdateLog{
		Timestamp: time.Now().UnixNano(),
		Value:     value,
		NodeID:    nodeID,
		Operation: op,
	}

	pn.History = append(pn.History, log)

	// 维护历史记录大小，删除最老的记录
	if len(pn.History) > pn.maxHistorySize {
		pn.History = pn.History[1:]
	}
}

// recordMergeHistory 记录合并操作到历史日志
func (pn *PNCounter) recordMergeHistory(remoteIncr, remoteDecr map[string]float64, remoteNodeID string) {
	// 记录合并事件本身
	log := UpdateLog{
		Timestamp: time.Now().UnixNano(),
		Value:     0, // 合并操作不改变本地值
		NodeID:    remoteNodeID,
		Operation: -1, // 特殊标记表示合并操作
	}

	pn.History = append(pn.History, log)

	// 维护历史记录大小
	if len(pn.History) > pn.maxHistorySize {
		pn.History = pn.History[1:]
	}
}

// SetMaxHistorySize 设置历史记录最大大小
func (pn *PNCounter) SetMaxHistorySize(size int) {
	pn.Lock()
	defer pn.Unlock()
	pn.maxHistorySize = size

	// 如果当前历史超过新限制，裁剪
	if len(pn.History) > pn.maxHistorySize {
		startIdx := len(pn.History) - pn.maxHistorySize
		pn.History = pn.History[startIdx:]
	}
}
