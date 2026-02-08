package controller

import (
	"fmt"
	"log"
	"sync"

	ac "github.com/weufhsos/adaptive_sync_go"
	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/simulator"
)

// LinkStatus 链路状态
type LinkStatus struct {
	ID             string  `json:"id"`
	TargetNodeID   string  `json:"target_node_id"`
	Capacity       float64 `json:"capacity"`
	Allocated      float64 `json:"allocated"`
	Available      float64 `json:"available"`
	BackgroundLoad float64 `json:"background_load"`
	Latency        float64 `json:"latency"`
}

// LinkManager 链路管理器
type LinkManager struct {
	acManager *ac.Manager
	simulator *simulator.NetworkSim
	links     map[string]*ManagedLink
	capacity  float64
	nodeID    string
	mu        sync.RWMutex

	// 全局共享状态（用于测试AC一致性自适应）
	globalStateKey string
}

// ManagedLink 受管理的链路
type ManagedLink struct {
	ID           string
	TargetNodeID string
	Capacity     float64
	StateKey     string // AC中的状态键
}

// NewLinkManager 创建链路管理器
func NewLinkManager(
	acManager *ac.Manager,
	netSim *simulator.NetworkSim,
	capacity float64,
	peers []string,
	nodeID string,
) *LinkManager {
	lm := &LinkManager{
		acManager: acManager,
		simulator: netSim,
		links:     make(map[string]*ManagedLink),
		capacity:  capacity,
		nodeID:    nodeID,
		// 全局共享状态key，所有节点共享同一个状态
		globalStateKey: "global_shared_bandwidth",
	}

	// 为每个对等节点创建链路管理
	for _, peer := range peers {
		linkID := fmt.Sprintf("link_%s_to_%s", nodeID, peer)
		stateKey := fmt.Sprintf("link_%s_available", linkID)

		lm.links[linkID] = &ManagedLink{
			ID:           linkID,
			TargetNodeID: peer,
			Capacity:     capacity,
			StateKey:     stateKey,
		}
	}

	log.Printf("[LinkManager] Created with %d links", len(lm.links))
	return lm
}

// InitializeLinks 初始化链路状态到AC
func (lm *LinkManager) InitializeLinks() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// 初始化每个链路的本地状态
	for linkID, link := range lm.links {
		if err := lm.acManager.Update(link.StateKey, lm.capacity); err != nil {
			log.Printf("[LinkManager] Warning: failed to initialize %s: %v", linkID, err)
		}
	}

	// 初始化全局共享状态（用于AC一致性测试）
	// 所有节点共享同一个key，这样才能产生不一致性检测
	globalCapacity := lm.capacity * float64(len(lm.links)+1) // 总容量 = 单链路容量 * 链路数
	log.Printf("[LinkManager] Initializing global state '%s' with capacity %.2f", lm.globalStateKey, globalCapacity)
	if err := lm.acManager.Update(lm.globalStateKey, globalCapacity); err != nil {
		log.Printf("[LinkManager] Warning: failed to initialize global state: %v", err)
	} else {
		log.Printf("[LinkManager] Global state '%s' initialized successfully", lm.globalStateKey)
	}

	log.Printf("[LinkManager] Initialized %d links and global state in AC", len(lm.links))
}

// GetAvailableBandwidth 获取链路可用带宽
func (lm *LinkManager) GetAvailableBandwidth(linkID string) float64 {
	lm.mu.RLock()
	link, exists := lm.links[linkID]
	lm.mu.RUnlock()

	if !exists {
		return 0
	}

	// 从AC获取当前可用带宽
	available := lm.acManager.Get(link.StateKey)

	// 减去模拟器中的背景负载
	simLink, simExists := lm.simulator.GetLinkState(linkID)
	if simExists {
		available -= simLink.BaseLoad
	}

	if available < 0 {
		return 0
	}
	return available
}

// SelectBestLink 选择最佳链路（可用带宽最大）
func (lm *LinkManager) SelectBestLink() (string, float64, error) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	var bestLinkID string
	var maxAvailable float64 = -1

	for linkID := range lm.links {
		available := lm.GetAvailableBandwidth(linkID)
		if available > maxAvailable {
			maxAvailable = available
			bestLinkID = linkID
		}
	}

	if bestLinkID == "" {
		return "", 0, fmt.Errorf("no available links")
	}

	return bestLinkID, maxAvailable, nil
}

// GetLinkStatus 获取单个链路状态
func (lm *LinkManager) GetLinkStatus(linkID string) (*LinkStatus, error) {
	lm.mu.RLock()
	link, exists := lm.links[linkID]
	lm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("link %s not found", linkID)
	}

	// 获取AC中的状态
	acAvailable := lm.acManager.Get(link.StateKey)
	allocated := lm.capacity - acAvailable
	if allocated < 0 {
		allocated = 0
	}

	// 获取模拟器中的状态
	var backgroundLoad float64 = 0
	var latency float64 = 1.0
	if simLink, simExists := lm.simulator.GetLinkState(linkID); simExists {
		backgroundLoad = simLink.BaseLoad
		latency = simLink.Latency
	}

	return &LinkStatus{
		ID:             linkID,
		TargetNodeID:   link.TargetNodeID,
		Capacity:       lm.capacity,
		Allocated:      allocated,
		Available:      lm.GetAvailableBandwidth(linkID),
		BackgroundLoad: backgroundLoad,
		Latency:        latency,
	}, nil
}

// GetAllLinksStatus 获取所有链路状态
func (lm *LinkManager) GetAllLinksStatus() map[string]*LinkStatus {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	result := make(map[string]*LinkStatus)
	for linkID := range lm.links {
		if status, err := lm.GetLinkStatus(linkID); err == nil {
			result[linkID] = status
		}
	}
	return result
}

// SyncFromAC 从AC同步状态（用于后台定期同步）
func (lm *LinkManager) SyncFromAC() {
	// 获取快照
	snapshot := lm.acManager.Snapshot()

	// 更新本地缓存的链路状态
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for linkID, link := range lm.links {
		if available, exists := snapshot[link.StateKey]; exists {
			// 可以在这里做一些验证或修正
			_ = available // 使用快照数据
			_ = linkID
		}
	}
}

// GetLinkIDs 获取所有链路ID列表
func (lm *LinkManager) GetLinkIDs() []string {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	ids := make([]string, 0, len(lm.links))
	for id := range lm.links {
		ids = append(ids, id)
	}
	return ids
}

// GetGlobalStateKey 获取全局状态key（用于AC一致性测试）
func (lm *LinkManager) GetGlobalStateKey() string {
	return lm.globalStateKey
}

// GetGlobalAvailable 获取全局可用带宽
func (lm *LinkManager) GetGlobalAvailable() float64 {
	return lm.acManager.Get(lm.globalStateKey)
}

// AllocateGlobalBandwidth 分配全局带宽（所有节点共享同一个状态）
func (lm *LinkManager) AllocateGlobalBandwidth(amount float64) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	log.Printf("[LinkManager] Allocating from global state '%s': amount=%.2f", lm.globalStateKey, amount)
	available := lm.acManager.Get(lm.globalStateKey)
	log.Printf("[LinkManager] Global state '%s' available: %.2f", lm.globalStateKey, available)
	
	if available < amount {
		return fmt.Errorf("insufficient global bandwidth: requested %.2f, available %.2f", amount, available)
	}

	// 更新全局状态（负值表示消耗）
	if err := lm.acManager.Update(lm.globalStateKey, -amount); err != nil {
		return fmt.Errorf("failed to update global state: %w", err)
	}

	newAvailable := lm.acManager.Get(lm.globalStateKey)
	log.Printf("[LinkManager] Allocated %.2f from global bandwidth, new available: %.2f", amount, newAvailable)
	return nil
}

// ReleaseGlobalBandwidth 释放全局带宽
func (lm *LinkManager) ReleaseGlobalBandwidth(amount float64) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if err := lm.acManager.Update(lm.globalStateKey, amount); err != nil {
		return fmt.Errorf("failed to update global state: %w", err)
	}

	log.Printf("[LinkManager] Released %.2f to global bandwidth", amount)
	return nil
}
