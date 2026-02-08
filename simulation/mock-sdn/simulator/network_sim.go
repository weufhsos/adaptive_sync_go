package simulator

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"
)

// LinkState 链路状态
type LinkState struct {
	ID             string
	TargetNodeID   string
	Capacity       float64 // 总容量 (Mbps)
	BaseLoad       float64 // 基础负载
	CurrentLoad    float64 // 当前负载
	Latency        float64 // 延迟 (ms)
	LastUpdateTime time.Time
}

// NetworkSim 网络状态模拟器
type NetworkSim struct {
	nodeID         string
	links          map[string]*LinkState
	linkCapacity   float64
	noiseLevel     float64
	updateInterval time.Duration
	strategy       SimulationStrategy
	mu             sync.RWMutex
	stopChan       chan struct{}
	stoppedChan    chan struct{}

	// 回调函数
	onLinkUpdate func(linkID string, load float64)
}

// SimulationStrategy 模拟策略接口
type SimulationStrategy interface {
	GenerateUpdate(link *LinkState, elapsed time.Duration) float64
}

// RandomWalkStrategy 随机游走策略
type RandomWalkStrategy struct {
	StepSize float64
	MinLoad  float64
	MaxLoad  float64
}

// GenerateUpdate 生成随机游走更新
func (s *RandomWalkStrategy) GenerateUpdate(link *LinkState, elapsed time.Duration) float64 {
	// 随机增减负载
	delta := (rand.Float64()*2 - 1) * s.StepSize
	newLoad := link.CurrentLoad + delta

	// 限制在范围内
	if newLoad < s.MinLoad {
		newLoad = s.MinLoad
	}
	if newLoad > s.MaxLoad {
		newLoad = s.MaxLoad
	}

	return newLoad
}

// BurstStrategy 突发流量策略
type BurstStrategy struct {
	BaseLine      float64
	BurstPeak     float64
	BurstDuration time.Duration
	BurstInterval time.Duration
	lastBurst     time.Time
	inBurst       bool
}

// GenerateUpdate 生成突发流量
func (s *BurstStrategy) GenerateUpdate(link *LinkState, elapsed time.Duration) float64 {
	now := time.Now()

	// 检查是否应该开始/结束突发
	if !s.inBurst && now.Sub(s.lastBurst) >= s.BurstInterval {
		s.inBurst = true
		s.lastBurst = now
	} else if s.inBurst && now.Sub(s.lastBurst) >= s.BurstDuration {
		s.inBurst = false
	}

	if s.inBurst {
		return s.BurstPeak + rand.Float64()*50
	}
	return s.BaseLine + rand.Float64()*20
}

// NewNetworkSim 创建网络模拟器
func NewNetworkSim(nodeID string, linkCapacity float64, peers []string) *NetworkSim {
	sim := &NetworkSim{
		nodeID:         nodeID,
		links:          make(map[string]*LinkState),
		linkCapacity:   linkCapacity,
		noiseLevel:     0.1,
		updateInterval: 100 * time.Millisecond,
		stopChan:       make(chan struct{}),
		stoppedChan:    make(chan struct{}),
		strategy: &RandomWalkStrategy{
			StepSize: 10.0,
			MinLoad:  0,
			MaxLoad:  linkCapacity * 0.3, // 背景流量最高30%
		},
	}

	// 为每个对等节点创建链路
	for _, peer := range peers {
		linkID := fmt.Sprintf("link_%s_to_%s", nodeID, peer)
		sim.links[linkID] = &LinkState{
			ID:             linkID,
			TargetNodeID:   peer,
			Capacity:       linkCapacity,
			BaseLoad:       rand.Float64() * linkCapacity * 0.1, // 初始负载0-10%
			CurrentLoad:    0,
			Latency:        1.0 + rand.Float64()*5, // 1-6ms延迟
			LastUpdateTime: time.Now(),
		}
	}

	log.Printf("[NetworkSim] Created with %d links, capacity=%.0f Mbps", len(sim.links), linkCapacity)
	return sim
}

// Start 启动模拟器
func (sim *NetworkSim) Start() error {
	log.Printf("[NetworkSim] Starting network simulator for node %s", sim.nodeID)
	go sim.runSimulation()
	return nil
}

// Stop 停止模拟器
func (sim *NetworkSim) Stop() {
	log.Printf("[NetworkSim] Stopping network simulator")
	close(sim.stopChan)
	<-sim.stoppedChan
	log.Printf("[NetworkSim] Network simulator stopped")
}

// SetOnLinkUpdate 设置链路更新回调
func (sim *NetworkSim) SetOnLinkUpdate(callback func(linkID string, load float64)) {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	sim.onLinkUpdate = callback
}

// SetStrategy 设置模拟策略
func (sim *NetworkSim) SetStrategy(strategy SimulationStrategy) {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	sim.strategy = strategy
}

// GetLinkState 获取链路状态
func (sim *NetworkSim) GetLinkState(linkID string) (*LinkState, bool) {
	sim.mu.RLock()
	defer sim.mu.RUnlock()
	link, exists := sim.links[linkID]
	if !exists {
		return nil, false
	}
	// 返回副本
	copyLink := *link
	return &copyLink, true
}

// GetAllLinks 获取所有链路状态
func (sim *NetworkSim) GetAllLinks() map[string]*LinkState {
	sim.mu.RLock()
	defer sim.mu.RUnlock()

	result := make(map[string]*LinkState)
	for id, link := range sim.links {
		copyLink := *link
		result[id] = &copyLink
	}
	return result
}

// AddExternalLoad 添加外部负载（来自带宽分配）
func (sim *NetworkSim) AddExternalLoad(linkID string, amount float64) error {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	link, exists := sim.links[linkID]
	if !exists {
		return fmt.Errorf("link %s not found", linkID)
	}

	link.CurrentLoad += amount
	if link.CurrentLoad < 0 {
		link.CurrentLoad = 0
	}
	link.LastUpdateTime = time.Now()

	return nil
}

// runSimulation 运行模拟循环
func (sim *NetworkSim) runSimulation() {
	defer close(sim.stoppedChan)

	ticker := time.NewTicker(sim.updateInterval)
	defer ticker.Stop()

	lastUpdate := time.Now()

	for {
		select {
		case <-sim.stopChan:
			return
		case <-ticker.C:
			now := time.Now()
			elapsed := now.Sub(lastUpdate)
			lastUpdate = now

			sim.updateAllLinks(elapsed)
		}
	}
}

// updateAllLinks 更新所有链路状态
func (sim *NetworkSim) updateAllLinks(elapsed time.Duration) {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	for linkID, link := range sim.links {
		// 使用策略生成背景负载变化
		newBaseLoad := sim.strategy.GenerateUpdate(link, elapsed)
		link.BaseLoad = newBaseLoad

		// 总负载 = 背景负载 + 当前分配的负载
		totalLoad := link.BaseLoad + link.CurrentLoad

		// 添加噪声
		noise := (rand.Float64()*2 - 1) * sim.noiseLevel * 10
		totalLoad += noise

		// 确保在合理范围内
		if totalLoad < 0 {
			totalLoad = 0
		}
		if totalLoad > link.Capacity {
			totalLoad = link.Capacity
		}

		link.LastUpdateTime = time.Now()

		// 触发回调
		if sim.onLinkUpdate != nil {
			sim.onLinkUpdate(linkID, totalLoad)
		}
	}
}

// GetAvailableBandwidth 获取链路可用带宽
func (sim *NetworkSim) GetAvailableBandwidth(linkID string) float64 {
	sim.mu.RLock()
	defer sim.mu.RUnlock()

	link, exists := sim.links[linkID]
	if !exists {
		return 0
	}

	available := link.Capacity - link.BaseLoad - link.CurrentLoad
	if available < 0 {
		return 0
	}
	return available
}
