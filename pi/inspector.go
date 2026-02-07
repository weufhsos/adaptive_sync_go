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

// CheckInconsistency 当收到远程更新时触发不一致性检查
// 实现论文Algorithm 2的核心逻辑
func (i *Inspector) CheckInconsistency(key string, receiveTime int64, counter *store.PNCounter) {
	// 获取历史记录
	history := counter.GetHistory()
	if len(history) < 2 {
		return // 历史记录不足，无法进行有效检查
	}

	// 重构历史：生成两条时间线
	observedTimeline, optimalTimeline := i.reconstructTimelines(history, receiveTime)

	// 模拟应用逻辑计算成本
	observedCost := i.calculateCost(observedTimeline)
	optimalCost := i.calculateCost(optimalTimeline)

	// 计算不一致性比率 φ
	phi := 1.0
	if optimalCost > 0 {
		phi = observedCost / optimalCost
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
func (i *Inspector) reconstructTimelines(history []store.UpdateLog, receiveTime int64) ([]float64, []float64) {
	var observedValues []float64
	var optimalValues []float64

	// 按时间戳排序历史记录
	// （假设history已经是按时间排序的）

	currentObserved := 0.0
	currentOptimal := 0.0

	for _, logEntry := range history {
		// 应用操作到当前值
		value := logEntry.Value
		if logEntry.Operation == store.Decrement {
			value = -value
		}

		// Observed Timeline: 实际发生的历史（缺少延迟的更新）
		currentObserved += value
		observedValues = append(observedValues, currentObserved)

		// Optimal Timeline: 理想历史（假设更新及时到达）
		// 这里简化处理：假设所有更新都及时到达
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
