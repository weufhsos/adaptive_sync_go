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
	nodeID      string
	acManager   *ac.Manager
	linkManager *LinkManager
	simulator   *simulator.NetworkSim
	metrics     *metrics.Exporter
	mu          sync.RWMutex
	isRunning   bool
	stopChan    chan struct{}
	stoppedChan chan struct{}
}

// NewSDNController 创建新的SDN控制器
func NewSDNController(
	nodeID string,
	grpcPort int,
	peers []string,
	targetPhi float64,
	linkCapacity float64,
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

	// 创建链路管理器
	linkManager := NewLinkManager(acManager, netSim, linkCapacity, peers, nodeID)

	ctrl := &SDNController{
		nodeID:      nodeID,
		acManager:   acManager,
		linkManager: linkManager,
		simulator:   netSim,
		metrics:     metricsExporter,
		stopChan:    make(chan struct{}),
		stoppedChan: make(chan struct{}),
	}

	// 设置模拟器回调
	netSim.SetOnLinkUpdate(func(linkID string, load float64) {
		ctrl.onLinkLoadUpdate(linkID, load)
	})

	log.Printf("[SDNController] Created controller for node %s with %d peers", nodeID, len(peers))
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

	// 初始化链路状态到AC
	c.linkManager.InitializeLinks()

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

// AllocateBandwidth 分配带宽
func (c *SDNController) AllocateBandwidth(linkID string, amount float64) error {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	startTime := time.Now()

	// 检查可用带宽
	available := c.linkManager.GetAvailableBandwidth(linkID)
	if available < amount {
		c.metrics.RecordRequest(false, time.Since(startTime))
		return fmt.Errorf("insufficient bandwidth: requested %.2f, available %.2f", amount, available)
	}

	// 通过AC协调层更新状态（使用负值表示消耗）
	stateKey := fmt.Sprintf("link_%s_available", linkID)
	if err := c.acManager.Update(stateKey, -amount); err != nil {
		c.metrics.RecordRequest(false, time.Since(startTime))
		return fmt.Errorf("failed to update AC state: %w", err)
	}

	// 更新模拟器中的负载
	if err := c.simulator.AddExternalLoad(linkID, amount); err != nil {
		log.Printf("[SDNController] Warning: failed to update simulator: %v", err)
	}

	c.metrics.RecordRequest(true, time.Since(startTime))
	c.metrics.RecordBandwidthAllocated(linkID, amount)

	log.Printf("[SDNController] Allocated %.2f Mbps on %s, remaining: %.2f", amount, linkID, available-amount)
	return nil
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

	c.metrics.RecordBandwidthReleased(linkID, amount)

	log.Printf("[SDNController] Released %.2f Mbps on %s", amount, linkID)
	return nil
}

// AllocateGlobalBandwidth 分配全局带宽（用于AC一致性测试）
func (c *SDNController) AllocateGlobalBandwidth(amount float64) error {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	log.Printf("[SDNController] Allocating global bandwidth: %.2f Mbps", amount)
	startTime := time.Now()

	if err := c.linkManager.AllocateGlobalBandwidth(amount); err != nil {
		c.metrics.RecordRequest(false, time.Since(startTime))
		return err
	}

	c.metrics.RecordRequest(true, time.Since(startTime))
	c.metrics.RecordBandwidthAllocated("global", amount)

	log.Printf("[SDNController] Allocated %.2f Mbps from global bandwidth", amount)
	return nil
}

// ReleaseGlobalBandwidth 释放全局带宽
func (c *SDNController) ReleaseGlobalBandwidth(amount float64) error {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	if err := c.linkManager.ReleaseGlobalBandwidth(amount); err != nil {
		return err
	}

	c.metrics.RecordBandwidthReleased("global", amount)

	log.Printf("[SDNController] Released %.2f Mbps to global bandwidth", amount)
	return nil
}

// GetGlobalAvailable 获取全局可用带宽
func (c *SDNController) GetGlobalAvailable() float64 {
	return c.linkManager.GetGlobalAvailable()
}

// GetBestLink 获取最佳链路（负载最低）
func (c *SDNController) GetBestLink() (string, float64, error) {
	c.mu.RLock()
	if !c.isRunning {
		c.mu.RUnlock()
		return "", 0, fmt.Errorf("controller not running")
	}
	c.mu.RUnlock()

	return c.linkManager.SelectBestLink()
}

// GetLinkStatus 获取链路状态
func (c *SDNController) GetLinkStatus(linkID string) (*LinkStatus, error) {
	return c.linkManager.GetLinkStatus(linkID)
}

// GetAllLinksStatus 获取所有链路状态
func (c *SDNController) GetAllLinksStatus() map[string]*LinkStatus {
	return c.linkManager.GetAllLinksStatus()
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

// onLinkLoadUpdate 处理链路负载更新
func (c *SDNController) onLinkLoadUpdate(linkID string, load float64) {
	// 更新指标
	c.metrics.RecordLinkLoad(linkID, load)
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
			c.linkManager.SyncFromAC()
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
