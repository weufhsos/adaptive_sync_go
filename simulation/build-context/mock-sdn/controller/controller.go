package controller

import (
	"fmt"
	"log"
	"sync"
	"time"

	ac "github.com/weufhsos/adaptive_sync_go"
	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/metrics"
	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/simulator"
)

// SDNController mock-SDN控制器，集成AC协调层
type SDNController struct {
	nodeID        string
	acManager     *ac.Manager
	serverManager *ServerManager // 替代 linkManager
	simulator     *simulator.NetworkSim
	metrics       *metrics.Exporter
	mu            sync.RWMutex
	isRunning     bool
	stopChan      chan struct{}
	stoppedChan   chan struct{}
}

// NewSDNController 创建新的SDN控制器
func NewSDNController(
	nodeID string,
	grpcPort int,
	peers []string,
	targetPhi float64,
	serverCapacity float64, // 改为服务器容量
	serverList []string, // 新增：服务器列表
	netSim *simulator.NetworkSim,
	metricsExporter *metrics.Exporter,
) (*SDNController, error) {

	// 创建AC协调层管理器
	acManager := ac.New(
		ac.WithNodeID(nodeID),
		ac.WithGRPCPort(grpcPort),
		ac.WithPeers(peers),
		ac.WithTargetPhi(targetPhi),
		ac.WithInitialCL(100, 50*time.Millisecond),
	)

	// 创建服务器管理器（替代链路管理器）
	serverManager := NewServerManager(acManager, netSim, serverCapacity, serverList, nodeID)

	ctrl := &SDNController{
		nodeID:        nodeID,
		acManager:     acManager,
		serverManager: serverManager, // 使用 serverManager
		simulator:     netSim,
		metrics:       metricsExporter,
		stopChan:      make(chan struct{}),
		stoppedChan:   make(chan struct{}),
	}

	// 设置模拟器回调
	netSim.SetOnLinkUpdate(func(linkID string, load float64) {
		// ctrl.onServerLoadUpdate(linkID, load)  // TODO: 实现此方法
		_ = linkID
		_ = load
	})

	log.Printf("[SDNController] Created controller for node %s with %d servers", nodeID, len(serverList))
	return ctrl, nil
}

// Start 启动控制器
func (c *SDNController) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isRunning {
		return fmt.Errorf("controller already running")
	}

	log.Printf("[SDNController] Starting controller %s", c.nodeID)

	// 启动AC协调层
	if err := c.acManager.Start(); err != nil {
		return fmt.Errorf("failed to start AC manager: %w", err)
	}

	// 初始化服务器状态到AC
	c.serverManager.InitializeServers()

	c.isRunning = true

	// 启动后台任务
	go c.runBackgroundTasks()

	log.Printf("[SDNController] Controller %s started", c.nodeID)
	return nil
}

// Stop 停止控制器
func (c *SDNController) Stop() {
	c.mu.Lock()
	if !c.isRunning {
		c.mu.Unlock()
		return
	}
	c.isRunning = false
	c.mu.Unlock()

	log.Printf("[SDNController] Stopping controller %s", c.nodeID)

	close(c.stopChan)
	<-c.stoppedChan

	c.acManager.Stop()

	log.Printf("[SDNController] Controller %s stopped", c.nodeID)
}

// AllocateServer 分配服务器资源（核心API）
func (c *SDNController) AllocateServer(cost float64) (serverID string, err error) {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return "", fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	startTime := time.Now()

	// 自动选择最佳服务器（负载最低）
	selectedServerID, available, err := c.serverManager.SelectBestServer()
	if err != nil {
		c.metrics.RecordRequest(false, time.Since(startTime))
		return "", fmt.Errorf("failed to select server: %w", err)
	}

	// 检查可用资源
	if available < cost {
		c.metrics.RecordRequest(false, time.Since(startTime))
		return "", fmt.Errorf("insufficient resources on server %s: requested %.2f%%, available %.2f%%", selectedServerID, cost, available)
	}

	// 通过AC协调层更新状态（使用负值表示消耗）
	stateKey := fmt.Sprintf("server_%s_available", selectedServerID)
	if err := c.acManager.Update(stateKey, -cost); err != nil {
		c.metrics.RecordRequest(false, time.Since(startTime))
		return "", fmt.Errorf("failed to update AC state: %w", err)
	}

	// 更新模拟器中的负载（复用接口）
	if err := c.simulator.AddExternalLoad(selectedServerID, cost); err != nil {
		log.Printf("[SDNController] Warning: failed to update simulator: %v", err)
	}

	c.metrics.RecordRequest(true, time.Since(startTime))
	// c.metrics.RecordServerAllocated(selectedServerID, cost) // TODO: 实现新指标方法

	log.Printf("[SDNController] Allocated %.2f%% on %s, remaining: %.2f%%", cost, selectedServerID, available-cost)
	return selectedServerID, nil
}

// ReleaseBandwidth 释放带宽
func (c *SDNController) ReleaseBandwidth(linkID string, amount float64) error {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	// 通过AC协调层更新状态（正值表示释放）
	stateKey := fmt.Sprintf("link_%s_available", linkID)
	if err := c.acManager.Update(stateKey, amount); err != nil {
		return fmt.Errorf("failed to update AC state: %w", err)
	}

	// 更新模拟器中的负载
	if err := c.simulator.AddExternalLoad(linkID, -amount); err != nil {
		log.Printf("[SDNController] Warning: failed to update simulator: %v", err)
	}

	c.metrics.RecordServerReleased(linkID, amount)

	log.Printf("[SDNController] Released %.2f Mbps on %s", amount, linkID)
	return nil
}

// AllocateGlobalResources 分配全局带宽（用于AC一致性测试）
func (c *SDNController) AllocateGlobalResources(amount float64) error {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	log.Printf("[SDNController] Allocating global bandwidth: %.2f Mbps", amount)
	startTime := time.Now()

	if err := c.serverManager.AllocateGlobalResources(amount); err != nil {
		c.metrics.RecordRequest(false, time.Since(startTime))
		return err
	}

	c.metrics.RecordRequest(true, time.Since(startTime))
	c.metrics.RecordServerAllocated("global", amount)

	log.Printf("[SDNController] Allocated %.2f Mbps from global bandwidth", amount)
	return nil
}

// ReleaseGlobalResources 释放全局带宽
func (c *SDNController) ReleaseGlobalResources(amount float64) error {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	if err := c.serverManager.ReleaseGlobalResources(amount); err != nil {
		return err
	}

	c.metrics.RecordServerReleased("global", amount)

	log.Printf("[SDNController] Released %.2f Mbps to global bandwidth", amount)
	return nil
}

// GetGlobalAvailable 获取全局可用带宽
func (c *SDNController) GetGlobalAvailable() float64 {
	return c.serverManager.GetGlobalAvailable()
}

// GetBestLink 获取最佳链路（负载最低）
func (c *SDNController) GetBestLink() (string, float64, error) {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return "", 0, fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	return c.serverManager.SelectBestServer()
}

// GetServerStatus 获取服务器状态（兼容 LinkStatus 接口）
func (c *SDNController) GetServerStatus(serverID string) (*LinkStatus, error) {
	status, err := c.serverManager.GetServerStatus(serverID)
	if err != nil {
		return nil, err
	}

	// 转换 ServerStatus 为 LinkStatus（保持API兼容性）
	return &LinkStatus{
		ID:             status.ID,
		TargetNodeID:   "", // 服务器没有目标节点概念
		Capacity:       status.Capacity,
		Allocated:      status.Allocated,
		Available:      status.Available,
		BackgroundLoad: 0,   // 服务器场景暂不使用
		Latency:        1.0, // 固定延迟
	}, nil
}

// GetAllServersStatus 获取所有服务器状态（兼容 LinkStatus 接口）
func (c *SDNController) GetAllServersStatus() map[string]*LinkStatus {
	serverMap := c.serverManager.GetAllServersStatus()
	linkMap := make(map[string]*LinkStatus, len(serverMap))

	for serverID, serverStatus := range serverMap {
		linkMap[serverID] = &LinkStatus{
			ID:             serverStatus.ID,
			TargetNodeID:   "",
			Capacity:       serverStatus.Capacity,
			Allocated:      serverStatus.Allocated,
			Available:      serverStatus.Available,
			BackgroundLoad: 0,
			Latency:        1.0,
		}
	}

	return linkMap
}

// GetNodeID 获取节点ID
func (c *SDNController) GetNodeID() string {
	return c.nodeID
}

// GetACStats 获取AC协调层统计信息
func (c *SDNController) GetACStats() *ac.ManagerStats {
	return c.acManager.GetStats()
}

// GetConsistencyInfo 获取一致性信息
func (c *SDNController) GetConsistencyInfo() map[string]interface{} {
	stats := c.acManager.GetStats()
	return map[string]interface{}{
		"node_id":        c.nodeID,
		"active_states":  stats.ActiveStates,
		"total_updates":  stats.TotalUpdates,
		"successful_ops": stats.SuccessfulOps,
		"failed_ops":     stats.FailedOps,
	}
}

// ReleaseServer 释放服务器资源
func (c *SDNController) ReleaseServer(serverID string, cost float64) error {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	// 通过AC协调层更新状态（正值表示释放）
	stateKey := fmt.Sprintf("server_%s_available", serverID)
	if err := c.acManager.Update(stateKey, cost); err != nil {
		return fmt.Errorf("failed to update AC state: %w", err)
	}

	// 更新模拟器中的负载
	if err := c.simulator.AddExternalLoad(serverID, -cost); err != nil {
		log.Printf("[SDNController] Warning: failed to update simulator: %v", err)
	}

	// c.metrics.RecordServerReleased(serverID, cost) // TODO: 实现新指标方法

	log.Printf("[SDNController] Released %.2f%% on %s", cost, serverID)
	return nil
}

// onServerLoadUpdate 处理服务器负载更新
func (c *SDNController) onServerLoadUpdate(serverID string, load float64) {
	// 更新指标
	// c.metrics.RecordServerLoad(serverID, load) // TODO: 实现新指标方法
}

// runBackgroundTasks 运行后台任务
func (c *SDNController) runBackgroundTasks() {
	defer close(c.stoppedChan)

	syncTicker := time.NewTicker(1 * time.Second)
	defer syncTicker.Stop()

	metricsTicker := time.NewTicker(5 * time.Second)
	defer metricsTicker.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-syncTicker.C:
			// 定期同步链路状态
			c.serverManager.SyncFromAC()
		case <-metricsTicker.C:
			// 定期更新AC相关指标
			c.updateACMetrics()
		}
	}
}

// updateACMetrics 更新AC相关指标
func (c *SDNController) updateACMetrics() {
	// 记录基础AC统计
	stats := c.acManager.GetStats()
	c.metrics.RecordACStats(stats)

	// 记录一致性指标
	phi := c.acManager.GetCurrentPhi()
	cl := c.acManager.GetCurrentConsistencyLevel()
	c.metrics.RecordACConsistency(phi, cl.QueueSize, cl.Timeout)
}
