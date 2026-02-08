package controller

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	ac "github.com/weufhsos/adaptive_sync_go"
	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/simulator"
)

// ServerStatus 服务器状态
type ServerStatus struct {
	ID        string  `json:"id"`
	Capacity  float64 `json:"capacity"`  // 总容量 (100%)
	Allocated float64 `json:"allocated"` // 已分配 (%)
	Available float64 `json:"available"` // 可用容量 (%)
	Load      float64 `json:"load"`      // 当前负载 (%)
}

// ServerManager 服务器管理器
type ServerManager struct {
	acManager *ac.Manager
	simulator *simulator.NetworkSim
	servers   map[string]*ManagedServer
	capacity  float64 // 每个服务器的总容量 (%)
	nodeID    string
	mu        sync.RWMutex

	// 全局共享状态（用于测试AC一致性自适应）
	globalStateKey string
}

// ManagedServer 受管理的服务器
type ManagedServer struct {
	ID       string
	Capacity float64
	StateKey string // AC中的状态键
}

// NewServerManager 创建服务器管理器
func NewServerManager(
	acManager *ac.Manager,
	netSim *simulator.NetworkSim,
	capacity float64,
	serverList []string, // 服务器ID列表
	nodeID string,
) *ServerManager {
	sm := &ServerManager{
		acManager: acManager,
		simulator: netSim,
		servers:   make(map[string]*ManagedServer),
		capacity:  capacity,
		nodeID:    nodeID,
		// 全局共享状态key，所有节点共享同一个状态
		globalStateKey: "global_shared_resources",
	}

	// 创建全局服务器视图：所有控制器都知道所有服务器
	for _, serverID := range serverList {
		stateKey := fmt.Sprintf("server_%s_available", serverID)

		sm.servers[serverID] = &ManagedServer{
			ID:       serverID,
			Capacity: capacity,
			StateKey: stateKey,
		}
	}

	log.Printf("[ServerManager] Created global view with %d servers", len(sm.servers))
	return sm
}

// InitializeServers 初始化服务器状态到AC
func (sm *ServerManager) InitializeServers() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 初始化每个服务器的本地状态
	for serverID, server := range sm.servers {
		if err := sm.acManager.Update(server.StateKey, sm.capacity); err != nil {
			log.Printf("[ServerManager] Warning: failed to initialize %s: %v", serverID, err)
		}
	}

	// 初始化全局共享状态（用于AC一致性测试）
	globalCapacity := sm.capacity * float64(len(sm.servers))
	log.Printf("[ServerManager] Initializing global state '%s' with capacity %.2f", sm.globalStateKey, globalCapacity)
	if err := sm.acManager.Update(sm.globalStateKey, globalCapacity); err != nil {
		log.Printf("[ServerManager] Warning: failed to initialize global state: %v", err)
	} else {
		log.Printf("[ServerManager] Global state '%s' initialized successfully", sm.globalStateKey)
	}

	log.Printf("[ServerManager] Initialized %d servers and global state in AC", len(sm.servers))
}

// GetAvailableResources 获取服务器可用资源
func (sm *ServerManager) GetAvailableResources(serverID string) float64 {
	sm.mu.RLock()
	server, exists := sm.servers[serverID]
	sm.mu.RUnlock()

	if !exists {
		return 0
	}

	// 从AC获取当前可用资源
	available := sm.acManager.Get(server.StateKey)

	// 减去模拟器中的背景负载（如果有的话）
	simServer, simExists := sm.simulator.GetLinkState(serverID) // 复用GetLinkState接口
	if simExists {
		available -= simServer.BaseLoad
	}

	if available < 0 {
		return 0
	}
	return available
}

// SelectBestServer 选择最佳服务器（可用资源最多）
func (sm *ServerManager) SelectBestServer() (string, float64, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var bestServerID string
	var maxAvailable float64 = -1

	for serverID := range sm.servers {
		available := sm.GetAvailableResources(serverID)
		if available > maxAvailable {
			maxAvailable = available
			bestServerID = serverID
		}
	}

	if bestServerID == "" {
		return "", 0, fmt.Errorf("no available servers")
	}

	return bestServerID, maxAvailable, nil
}

// GetServerStatus 获取单个服务器状态
func (sm *ServerManager) GetServerStatus(serverID string) (*ServerStatus, error) {
	sm.mu.RLock()
	server, exists := sm.servers[serverID]
	sm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	// 获取AC中的状态
	acAvailable := sm.acManager.Get(server.StateKey)
	allocated := sm.capacity - acAvailable
	if allocated < 0 {
		allocated = 0
	}

	// 获取模拟器中的状态（暂时保留接口兼容性）
	// var backgroundLoad float64 = 0
	// var latency float64 = 1.0
	// if simServer, simExists := sm.simulator.GetLinkState(serverID); simExists {
	// 	backgroundLoad = simServer.BaseLoad
	// 	latency = simServer.Latency
	// }

	load := (sm.capacity - acAvailable) / sm.capacity * 100 // 转换为百分比

	return &ServerStatus{
		ID:        serverID,
		Capacity:  sm.capacity,
		Allocated: allocated,
		Available: sm.GetAvailableResources(serverID),
		Load:      load,
	}, nil
}

// GetAllServersStatus 获取所有服务器状态
func (sm *ServerManager) GetAllServersStatus() map[string]*ServerStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make(map[string]*ServerStatus)
	for serverID := range sm.servers {
		if status, err := sm.GetServerStatus(serverID); err == nil {
			result[serverID] = status
		}
	}
	return result
}

// SyncFromAC 从AC同步状态（用于后台定期同步）
func (sm *ServerManager) SyncFromAC() {
	// 获取快照
	snapshot := sm.acManager.Snapshot()

	// 更新本地缓存的服务器状态
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for serverID, server := range sm.servers {
		if available, exists := snapshot[server.StateKey]; exists {
			// 可以在这里做一些验证或修正
			_ = available // 使用快照数据
			_ = serverID
		}
	}
}

// GetServerIDs 获取所有服务器ID列表
func (sm *ServerManager) GetServerIDs() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	ids := make([]string, 0, len(sm.servers))
	for id := range sm.servers {
		ids = append(ids, id)
	}
	return ids
}

// GetGlobalStateKey 获取全局状态key（用于AC一致性测试）
func (sm *ServerManager) GetGlobalStateKey() string {
	return sm.globalStateKey
}

// GetGlobalAvailable 获取全局可用资源
func (sm *ServerManager) GetGlobalAvailable() float64 {
	return sm.acManager.Get(sm.globalStateKey)
}

// AllocateGlobalResources 分配全局资源（所有节点共享同一个状态）
func (sm *ServerManager) AllocateGlobalResources(amount float64) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Printf("[ServerManager] Allocating from global state '%s': amount=%.2f", sm.globalStateKey, amount)
	available := sm.acManager.Get(sm.globalStateKey)
	log.Printf("[ServerManager] Global state '%s' available: %.2f", sm.globalStateKey, available)

	if available < amount {
		return fmt.Errorf("insufficient global resources: requested %.2f, available %.2f", amount, available)
	}

	// 更新全局状态（负值表示消耗）
	if err := sm.acManager.Update(sm.globalStateKey, -amount); err != nil {
		return fmt.Errorf("failed to update global state: %w", err)
	}

	newAvailable := sm.acManager.Get(sm.globalStateKey)
	log.Printf("[ServerManager] Allocated %.2f from global resources, new available: %.2f", amount, newAvailable)
	return nil
}

// ReleaseGlobalResources 释放全局资源
func (sm *ServerManager) ReleaseGlobalResources(amount float64) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if err := sm.acManager.Update(sm.globalStateKey, amount); err != nil {
		return fmt.Errorf("failed to update global state: %w", err)
	}

	log.Printf("[ServerManager] Released %.2f to global resources", amount)
	return nil
}

// ParseServerList 从环境变量解析服务器列表
func ParseServerList(serverListStr string) []string {
	if serverListStr == "" {
		return []string{"server-1", "server-2", "server-3", "server-4", "server-5"}
	}

	parts := strings.Split(serverListStr, ",")
	servers := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			servers = append(servers, part)
		}
	}
	return servers
}

// ParseServerCount 从环境变量解析服务器数量
func ParseServerCount(countStr string) int {
	count, err := strconv.Atoi(countStr)
	if err != nil || count <= 0 {
		return 5 // 默认5个服务器
	}
	return count
}

// GenerateServerList 根据数量生成服务器列表
func GenerateServerList(count int) []string {
	servers := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		servers = append(servers, fmt.Sprintf("server-%d", i))
	}
	return servers
}
