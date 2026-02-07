package ac

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/your-org/ac/dispatcher"
	"github.com/your-org/ac/oca"
	"github.com/your-org/ac/pi"
	"github.com/your-org/ac/proto"
	"github.com/your-org/ac/store"
	"github.com/your-org/ac/transport"
)

// Config AC模块配置
type Config struct {
	NodeID        string
	PeerAddresses []string
	GRPCPort      int
	TargetPhi     float64
	InitialCL     dispatcher.ConsistencyLevel

	// 高级配置
	Advanced *AdvancedConfig
}

// Manager AC模块主管理器
type Manager struct {
	// 配置
	config *Config

	// 核心组件
	stores      map[string]*store.PNCounter // 状态存储
	dispatcher  *dispatcher.Dispatcher      // 分发控制器
	piInspector *pi.Inspector               // 性能检查模块
	ocaCtrl     *oca.Controller             // 在线自适应控制器
	grpcServer  *transport.GRPCServer       // gRPC服务端

	// 同步原语
	storeMutex  sync.RWMutex
	stopChan    chan struct{}
	stoppedChan chan struct{}

	// 统计信息
	stats      *ManagerStats
	statsMutex sync.RWMutex
}

// ManagerStats 管理器统计信息
type ManagerStats struct {
	TotalUpdates   int64
	SuccessfulOps  int64
	FailedOps      int64
	ActiveStates   int
	LastUpdateTime time.Time
}

// AdvancedConfig 高级配置选项
type AdvancedConfig struct {
	// PID控制器参数
	PIDParams *PIDParameters

	// 历史记录配置
	HistoryWindowSize int
	MaxHistorySize    int

	// 日志和监控
	LogLevel         string
	StatsInterval    time.Duration
	EnableMonitoring bool

	// 自定义组件
	CustomTransport Transport
}

// PIDParameters PID控制器参数
type PIDParameters struct {
	Kp float64 // 比例系数
	Ki float64 // 积分系数
	Kd float64 // 微分系数
}

// Transport 传输层接口（用于自定义传输实现）
type Transport interface {
	Start() error
	Stop() error
	SendUpdate(update interface{}) error
	RegisterHandler(handler interface{})
}

// Option 配置选项函数类型
type Option func(*Config)

// New 创建新的AC管理器实例
func New(opts ...Option) *Manager {
	// 默认配置
	config := &Config{
		GRPCPort:  50051,
		TargetPhi: 1.05,
		InitialCL: dispatcher.ConsistencyLevel{
			QueueSize: 100,
			Timeout:   50 * time.Millisecond,
		},
	}

	// 应用选项
	for _, opt := range opts {
		opt(config)
	}

	if config.NodeID == "" {
		log.Fatal("NodeID is required")
	}

	m := &Manager{
		config:      config,
		stores:      make(map[string]*store.PNCounter),
		stopChan:    make(chan struct{}),
		stoppedChan: make(chan struct{}),
		stats:       &ManagerStats{},
	}

	// 初始化组件
	m.initComponents()

	return m
}

// 初始化各组件
func (m *Manager) initComponents() {
	// 初始化分发器
	m.dispatcher = dispatcher.NewDispatcher(m.config.NodeID, m.config.PeerAddresses)
	m.dispatcher.SetConsistencyLevel(m.config.InitialCL)

	// 初始化性能检查模块
	m.piInspector = pi.NewInspector()

	// 初始化自适应控制器
	m.ocaCtrl = oca.NewController(m.config.TargetPhi)

	// 初始化gRPC服务端
	m.grpcServer = transport.NewGRPCServer(m.config.GRPCPort)

	// 设置回调函数
	m.setupCallbacks()
}

// 设置回调函数
func (m *Manager) setupCallbacks() {
	// 设置分发器的更新处理回调
	m.dispatcher.SetOnUpdateProcessed(func(key string, success bool) {
		m.statsMutex.Lock()
		m.stats.TotalUpdates++
		if success {
			m.stats.SuccessfulOps++
		} else {
			m.stats.FailedOps++
		}
		m.stats.LastUpdateTime = time.Now()
		m.statsMutex.Unlock()
	})

	// 设置PI模块的不一致性报告回调
	m.piInspector.SetOnInconsistencyReport(func(phi float64) {
		// 将不一致性报告传递给OCA控制器
		newCL := m.ocaCtrl.Adjust(phi)
		if newCL != nil {
			m.dispatcher.SetConsistencyLevel(*newCL)
			log.Printf("[Manager] OCA adjusted CL to QS=%d, TO=%v based on phi=%.4f",
				newCL.QueueSize, newCL.Timeout, phi)
		}
	})
}

// Start 启动AC模块
func (m *Manager) Start() error {
	log.Printf("[Manager] Starting AC Manager for node %s", m.config.NodeID)

	// 启动gRPC服务端
	if err := m.grpcServer.Start(); err != nil {
		return fmt.Errorf("failed to start gRPC server: %w", err)
	}

	// 注册gRPC服务处理器
	m.registerGRPCHandlers()

	// 启动分发器
	if err := m.dispatcher.Start(); err != nil {
		return fmt.Errorf("failed to start dispatcher: %w", err)
	}

	// 启动PI检查器
	m.piInspector.Start()

	// 启动OCA控制器
	m.ocaCtrl.Start()

	// 启动后台监控协程
	go m.monitorLoop()

	log.Printf("[Manager] AC Manager started successfully")
	return nil
}

// Stop 停止AC模块
func (m *Manager) Stop() {
	log.Printf("[Manager] Stopping AC Manager for node %s", m.config.NodeID)

	close(m.stopChan)

	// 停止各组件
	m.ocaCtrl.Stop()
	m.piInspector.Stop()
	m.dispatcher.Stop()
	m.grpcServer.Stop()

	// 等待停止完成
	<-m.stoppedChan

	log.Printf("[Manager] AC Manager stopped")
}

// Update 增量更新状态值（核心API）
func (m *Manager) Update(key string, delta float64) error {
	// 获取或创建状态存储
	counter := m.getOrCreateStore(key)

	// 本地更新
	if delta >= 0 {
		counter.Increment(m.config.NodeID, delta)
	} else {
		counter.Decrement(m.config.NodeID, -delta)
	}

	// 生成唯一更新ID
	updateID := fmt.Sprintf("%s_%d", key, time.Now().UnixNano())

	// 加入分发队列
	if err := m.dispatcher.Enqueue(key, delta, updateID); err != nil {
		// 如果分发失败，回滚本地更新
		if delta >= 0 {
			counter.Decrement(m.config.NodeID, delta)
		} else {
			counter.Increment(m.config.NodeID, -delta)
		}
		return fmt.Errorf("failed to enqueue update: %w", err)
	}

	return nil
}

// Get 读取当前状态值（内存读取，纳秒级）
func (m *Manager) Get(key string) float64 {
	counter := m.getOrCreateStore(key)
	return counter.Value()
}

// Snapshot 获取所有状态的一致性快照
func (m *Manager) Snapshot() map[string]float64 {
	m.storeMutex.RLock()
	defer m.storeMutex.RUnlock()

	snapshot := make(map[string]float64)
	for key, counter := range m.stores {
		snapshot[key] = counter.Value()
	}
	return snapshot
}

// HandleRemoteUpdate 处理来自远程节点的更新（内部方法，供gRPC调用）
func (m *Manager) HandleRemoteUpdate(update *proto.UpdateMessage) error {
	// 获取或创建状态存储
	counter := m.getOrCreateStore(update.Key)

	// 记录更新时间用于PI模块
	receiveTime := time.Now().UnixNano()

	// 本地合并远程状态
	remoteIncr := make(map[string]float64)
	remoteDecr := make(map[string]float64)

	value := update.Value
	if update.Op == proto.Operation_DECREMENT {
		value = -value
	}

	if value >= 0 {
		remoteIncr[update.OriginNodeId] = value
	} else {
		remoteDecr[update.OriginNodeId] = -value
	}

	counter.Merge(remoteIncr, remoteDecr, update.OriginNodeId)

	// 通知PI模块进行不一致性检查
	go m.piInspector.CheckInconsistency(update.Key, receiveTime, counter)

	return nil
}

// HandleTopologyEvent 处理拓扑变更事件
func (m *Manager) HandleTopologyEvent(event interface{}) error {
	// TODO: 实现拓扑变更处理逻辑
	// 这里可以更新网络拓扑相关的状态
	log.Printf("[Manager] Topology event received: %+v", event)
	return nil
}

// GetTopology 获取当前拓扑视图
func (m *Manager) GetTopology() interface{} {
	// TODO: 实现拓扑获取逻辑
	return nil
}

// GetStats 获取统计信息
func (m *Manager) GetStats() *ManagerStats {
	m.statsMutex.RLock()
	defer m.statsMutex.RUnlock()

	// 获取存储状态数
	m.storeMutex.RLock()
	activeStates := len(m.stores)
	m.storeMutex.RUnlock()

	stats := *m.stats
	stats.ActiveStates = activeStates
	return &stats
}

// ========== 内部辅助方法 ==========

// getOrCreateStore 获取或创建状态存储
func (m *Manager) getOrCreateStore(key string) *store.PNCounter {
	m.storeMutex.Lock()
	defer m.storeMutex.Unlock()

	if counter, exists := m.stores[key]; exists {
		return counter
	}

	// 创建新的PN-Counter
	counter := store.NewPNCounter(key)
	m.stores[key] = counter

	return counter
}

// registerGRPCHandlers 注册gRPC服务处理器
func (m *Manager) registerGRPCHandlers() {
	// 注册更新处理handler
	m.grpcServer.RegisterUpdateHandler(func(update *proto.UpdateMessage) error {
		return m.HandleRemoteUpdate(update)
	})

	// 注册一致性级别变更handler
	m.grpcServer.RegisterCLChangeHandler(func(config *proto.CLConfig) error {
		// 将proto配置转换为内部配置
		newCL := dispatcher.ConsistencyLevel{
			QueueSize: int(config.MaxQueueSize),
			Timeout:   time.Duration(config.TimeoutMs) * time.Millisecond,
		}
		m.dispatcher.SetConsistencyLevel(newCL)
		return nil
	})
}

// monitorLoop 后台监控循环
func (m *Manager) monitorLoop() {
	defer close(m.stoppedChan)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.logStats()
		}
	}
}

// logStats 记录统计信息
func (m *Manager) logStats() {
	stats := m.GetStats()
	dispatcherStats := m.dispatcher.GetStats()

	log.Printf("[Manager] Stats - Updates:%d Success:%d Failed:%d ActiveStates:%d",
		stats.TotalUpdates, stats.SuccessfulOps, stats.FailedOps, stats.ActiveStates)
	log.Printf("[Manager] Dispatcher - Total:%d Success:%d Failed:%d Drops:%d",
		dispatcherStats.TotalUpdates, dispatcherStats.SuccessfulSends,
		dispatcherStats.FailedSends, dispatcherStats.QueueFullDrops)
}
