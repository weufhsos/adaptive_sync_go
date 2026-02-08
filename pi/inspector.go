package pi

import (
	"log"
	"sync"
	"time"

	"github.com/weufhsos/adaptive_sync_go/store"
)

// InconsistencyReport 不一致性报告
type InconsistencyReport struct {
	Key          string
	Phi          float64 // 不一致性比率
	Timestamp    time.Time
	ObservedCost float64 // 实际成本 σ_u
	OptimalCost  float64 // 理想成本 σ_o
}

// Inspector 实现论文中的Algorithm 2: 不一致性计算逻辑
type Inspector struct {
	// 回调函数
	onReport func(float64)

	// 控制通道
	stopChan    chan struct{}
	stoppedChan chan struct{}

	// 统计信息
	stats      *InspectorStats
	statsMutex sync.RWMutex
}

// InspectorStats 检查器统计信息
type InspectorStats struct {
	TotalChecks      int64
	ReportsGenerated int64
	AveragePhi       float64
	LastCheckTime    time.Time
}

// NewInspector 创建新的性能检查器
func NewInspector() *Inspector {
	return &Inspector{
		stopChan:    make(chan struct{}),
		stoppedChan: make(chan struct{}),
		stats:       &InspectorStats{},
	}
}

// Start 启动检查器
func (i *Inspector) Start() {
	log.Printf("[PI] Performance Inspector started")
	go i.monitorStats()
}

// Stop 停止检查器
func (i *Inspector) Stop() {
	log.Printf("[PI] Stopping Performance Inspector")

	// 避免重复关闭channel
	select {
	case <-i.stopChan:
		// 已经关闭了
	default:
		close(i.stopChan)
	}

	<-i.stoppedChan
	log.Printf("[PI] Performance Inspector stopped")
}

// RemoteUpdate 远程更新信息（用于不一致性检查）
type RemoteUpdate struct {
	Key          string
	Value        float64
	Operation    store.Operation
	OriginTime   int64 // 远程更新的原始时间戳
	ReceiveTime  int64 // 本地收到的时间戳
	OriginNodeID string
}

// CheckInconsistency 当收到远程更新时触发不一致性检查
// 实现论文Algorithm 2的核心逻辑
// 参数:
//   - remoteUpdate: 迟到的远程更新信息
//   - localHistory: 本地操作历史
func (i *Inspector) CheckInconsistency(key string, receiveTime int64, counter *store.PNCounter) {
	// 获取历史记录
	history := counter.GetHistory()
	if len(history) < 2 {
		return // 历史记录不足，无法进行有效检查
	}

	// 重构历史：生成两条时间线
	observedTimeline, optimalTimeline := i.reconstructTimelines(history, receiveTime)

	// 如果没有本地操作历史，跳过此次检查
	// 这发生在：收到其他节点管理的链路更新，但本节点从未操作过该链路
	if observedTimeline == nil {
		log.Printf("[PI] Skipping inconsistency check for %s: no local operations", key)
		return
	}

	// 模拟应用逻辑计算成本
	observedCost := i.calculateCost(observedTimeline)
	optimalCost := i.calculateCost(optimalTimeline)

	// 计算不一致性比率 φ
	// φ = σ_u / σ_o (实际成本 / 理想成本)
	phi := 1.0
	if optimalCost > 0 {
		phi = observedCost / optimalCost
	} else if observedCost > 0 {
		// optimal=0 但 observed>0，说明有不一致性
		phi = observedCost + 1.0
	}

	// 生成报告（可用于后续扩展）
	_ = &InconsistencyReport{
		Key:          key,
		Phi:          phi,
		Timestamp:    time.Now(),
		ObservedCost: observedCost,
		OptimalCost:  optimalCost,
	}

	// 更新统计
	i.updateStats(phi)

	// 调用回调函数（通常是OCA控制器）
	if i.onReport != nil {
		go i.onReport(phi)
	}

	log.Printf("[PI] Inconsistency check for %s: φ=%.4f (observed=%.2f, optimal=%.2f)",
		key, phi, observedCost, optimalCost)
}

// CheckInconsistencyWithRemote 使用远程更新信息进行不一致性检查（完整实现）
// 实现论文Algorithm 2的核心逻辑:
//  1. Observed Timeline: 本地在收到远程更新之前的操作历史（不含迟到的远程更新）
//  2. Optimal Timeline: 将远程更新按原始时间戳插入后的理想历史
func (i *Inspector) CheckInconsistencyWithRemote(remoteUpdate *RemoteUpdate, localHistory []store.UpdateLog) {
	if len(localHistory) < 1 {
		return // 历史记录不足
	}

	// 重构历史：生成两条时间线
	observedTimeline, optimalTimeline := i.reconstructTimelinesWithRemote(localHistory, remoteUpdate)

	// 模拟应用逻辑计算成本
	observedCost := i.calculateCost(observedTimeline)
	optimalCost := i.calculateCost(optimalTimeline)

	// 计算不一致性比率 φ
	// φ = σ_u / σ_o (实际成本 / 理想成本)
	phi := 1.0
	if optimalCost > 0 {
		phi = observedCost / optimalCost
	}
	// 如果 optimalCost 为 0 但 observedCost > 0，说明有偏差
	if optimalCost == 0 && observedCost > 0 {
		phi = observedCost + 1.0 // 表示有不一致性
	}

	// 生成报告
	report := &InconsistencyReport{
		Key:          remoteUpdate.Key,
		Phi:          phi,
		Timestamp:    time.Now(),
		ObservedCost: observedCost,
		OptimalCost:  optimalCost,
	}
	_ = report

	// 更新统计
	i.updateStats(phi)

	// 调用回调函数（通常是OCA控制器）
	if i.onReport != nil {
		go i.onReport(phi)
	}

	log.Printf("[PI] Inconsistency check for %s: φ=%.4f (observed=%.2f, optimal=%.2f)",
		remoteUpdate.Key, phi, observedCost, optimalCost)
}

// SetOnInconsistencyReport 设置不一致性报告回调
func (i *Inspector) SetOnInconsistencyReport(callback func(float64)) {
	i.onReport = callback
}

// GetStats 获取统计信息
func (i *Inspector) GetStats() *InspectorStats {
	i.statsMutex.RLock()
	defer i.statsMutex.RUnlock()

	stats := *i.stats
	return &stats
}

// ========== 核心算法实现 ==========

// reconstructTimelines 重构历史时间线（Algorithm 2的关键步骤）
// 生成实际观察到的历史和理想历史
// 
// 核心逻辑：
// - Observed Timeline: 只包含本地操作（在远程更新到达之前执行的操作）
// - Optimal Timeline: 包含所有操作（假设远程更新及时到达）
//
// 注意：如果observedValues为空（没有本地操作历史），返回nil表示无法计算有效φ
func (i *Inspector) reconstructTimelines(history []store.UpdateLog, receiveTime int64) ([]float64, []float64) {
	var observedValues []float64
	var optimalValues []float64

	currentObserved := 0.0
	currentOptimal := 0.0
	hasLocalOperation := false

	for _, logEntry := range history {
		// 应用操作到当前值
		value := logEntry.Value
		if logEntry.Operation == store.Decrement {
			value = -value
		}

		// Observed Timeline: 只包含本地操作（非远程更新）
		// 这模拟了"如果远程更新没有延迟到达，本地会看到什么"
		if !logEntry.IsRemote {
			currentObserved += value
			observedValues = append(observedValues, currentObserved)
			hasLocalOperation = true
		}

		// Optimal Timeline: 包含所有操作（本地+远程）
		// 这模拟了"如果所有更新都及时到达，理想状态是什么"
		currentOptimal += value
		optimalValues = append(optimalValues, currentOptimal)
	}

	// 如果没有本地操作历史，返回nil表示无法计算有效φ
	// 这种情况发生在：收到其他节点管理的链路更新，但本节点从未操作过该链路
	if !hasLocalOperation {
		return nil, optimalValues
	}

	return observedValues, optimalValues
}

// reconstructTimelinesWithRemote 使用远程更新信息重构历史时间线（完整实现）
// 实现论文Algorithm 2的核心逻辑:
//
// 输入:
//   - localHistory: 本地操作历史记录
//   - remoteUpdate: 迟到的远程更新（触发器）
//
// 输出:
//   - observedTimeline: 观察历史 S_{U_{incnst}} - 本地在收到远程更新之前的操作序列
//   - optimalTimeline: 理想历史 S_{U_{cnst}} - 将远程更新按原始时间戳插入后的序列
func (i *Inspector) reconstructTimelinesWithRemote(localHistory []store.UpdateLog, remoteUpdate *RemoteUpdate) ([]float64, []float64) {
	var observedValues []float64
	var optimalValues []float64

	// ========== 1. 构建观察历史 (Observed Timeline) ==========
	// 只包含本地在远程更新到达之前的操作
	currentObserved := 0.0
	for _, logEntry := range localHistory {
		// 只包含在远程更新到达之前的本地操作
		if logEntry.Timestamp < remoteUpdate.ReceiveTime {
			value := logEntry.Value
			if logEntry.Operation == store.Decrement {
				value = -value
			}
			currentObserved += value
			observedValues = append(observedValues, currentObserved)
		}
	}

	// ========== 2. 构建理想历史 (Optimal Timeline) ==========
	// 将远程更新按其原始时间戳插入到本地历史中

	// 创建合并后的时间线
	type timelineEntry struct {
		Timestamp int64
		Value     float64
		Operation store.Operation
	}

	var mergedTimeline []timelineEntry

	// 添加本地历史（只包含远程更新到达之前的操作）
	for _, logEntry := range localHistory {
		if logEntry.Timestamp < remoteUpdate.ReceiveTime {
			mergedTimeline = append(mergedTimeline, timelineEntry{
				Timestamp: logEntry.Timestamp,
				Value:     logEntry.Value,
				Operation: logEntry.Operation,
			})
		}
	}

	// 添加远程更新（使用其原始时间戳）
	mergedTimeline = append(mergedTimeline, timelineEntry{
		Timestamp: remoteUpdate.OriginTime, // 使用原始时间戳，而不是到达时间
		Value:     remoteUpdate.Value,
		Operation: remoteUpdate.Operation,
	})

	// 按时间戳排序
	for i := 0; i < len(mergedTimeline)-1; i++ {
		for j := i + 1; j < len(mergedTimeline); j++ {
			if mergedTimeline[i].Timestamp > mergedTimeline[j].Timestamp {
				mergedTimeline[i], mergedTimeline[j] = mergedTimeline[j], mergedTimeline[i]
			}
		}
	}

	// 计算理想时间线的值
	currentOptimal := 0.0
	for _, entry := range mergedTimeline {
		value := entry.Value
		if entry.Operation == store.Decrement {
			value = -value
		}
		currentOptimal += value
		optimalValues = append(optimalValues, currentOptimal)
	}

	return observedValues, optimalValues
}

// calculateCost 计算决策成本（使用标准差作为成本指标）
// 模拟SDN应用逻辑（如负载均衡器的选路决策）
func (i *Inspector) calculateCost(timeline []float64) float64 {
	if len(timeline) == 0 {
		return 0
	}

	// 计算平均值
	sum := 0.0
	for _, value := range timeline {
		sum += value
	}
	mean := sum / float64(len(timeline))

	// 计算方差
	variance := 0.0
	for _, value := range timeline {
		diff := value - mean
		variance += diff * diff
	}
	variance /= float64(len(timeline))

	// 返回标准差作为成本
	return variance // 注意：实际应该是 sqrt(variance)，但相对比较时平方根不影响结果
}

// updateStats 更新统计信息
func (i *Inspector) updateStats(phi float64) {
	i.statsMutex.Lock()
	defer i.statsMutex.Unlock()

	i.stats.TotalChecks++
	i.stats.ReportsGenerated++

	// 更新平均φ值（移动平均）
	if i.stats.ReportsGenerated == 1 {
		i.stats.AveragePhi = phi
	} else {
		// 简单移动平均
		i.stats.AveragePhi = (i.stats.AveragePhi*(float64(i.stats.ReportsGenerated-1)) + phi) /
			float64(i.stats.ReportsGenerated)
	}

	i.stats.LastCheckTime = time.Now()
}

// monitorStats 监控统计信息
func (i *Inspector) monitorStats() {
	defer close(i.stoppedChan)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-i.stopChan:
			return
		case <-ticker.C:
			stats := i.GetStats()
			log.Printf("[PI] Stats - Checks:%d Reports:%d AvgPhi:%.4f",
				stats.TotalChecks, stats.ReportsGenerated, stats.AveragePhi)
		}
	}
}
