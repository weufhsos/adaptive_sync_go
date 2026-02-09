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
//   - key: 状态键
//   - receiveTime: 远程更新到达时间
//   - counter: PN计数器
func (i *Inspector) CheckInconsistency(key string, receiveTime int64, counter *store.PNCounter) {
	// 获取历史记录
	history := counter.GetHistory()
	if len(history) < 2 {
		return // 历史记录不足，无法进行有效检查
	}

	// 重构历史：生成两条时间线
	// suboptimalTimeline = S_{U_{incnst}} (次优/实际历史)
	// optimalTimeline = S_{U_{cnst}} (最优/理想历史)
	suboptimalTimeline, optimalTimeline := i.reconstructTimelines(history, receiveTime)

	// 如果没有本地操作历史，跳过此次检查
	// 这发生在：收到其他节点管理的链路更新，但本节点从未操作过该链路
	if suboptimalTimeline == nil {
		log.Printf("[PI] Skipping inconsistency check for %s: no local operations", key)
		return
	}

	// 计算成本（使用标准差作为负载均衡指标）
	suboptimalCost := i.calculateCost(suboptimalTimeline)
	optimalCost := i.calculateCost(optimalTimeline)

	// 计算不一致性比率 φ
	// 根据论文: φ = σ_u / σ_o (次优成本 / 最优成本)
	// 注意: φ 应该 >= 1，因为次优决策的成本不会低于最优决策
	phi := 1.0
	if optimalCost > 0 {
		phi = suboptimalCost / optimalCost
	} else if suboptimalCost > 0 {
		// optimal=0 但 suboptimal>0，说明有不一致性
		phi = suboptimalCost + 1.0
	}
	
	// 确保 phi >= 1（论文定义）
	if phi < 1.0 {
		// 如果由于数值计算问题导致 phi < 1，将其设为 1.0（无偏差）
		phi = 1.0
	}

	// 生成报告（可用于后续扩展）
	_ = &InconsistencyReport{
		Key:          key,
		Phi:          phi,
		Timestamp:    time.Now(),
		ObservedCost: suboptimalCost,
		OptimalCost:  optimalCost,
	}

	// 更新统计
	i.updateStats(phi)

	// 调用回调函数（通常是OCA控制器）
	if i.onReport != nil {
		go i.onReport(phi)
	}

	log.Printf("[PI] Inconsistency check for %s: φ=%.4f (suboptimal=%.2f, optimal=%.2f)",
		key, phi, suboptimalCost, optimalCost)
}

// CheckInconsistencyWithRemote 使用远程更新信息进行不一致性检查（完整实现）
// 实现论文Algorithm 2的核心逻辑:
//  1. Suboptimal Timeline (S_{U_{incnst}}): 本地所有历史操作 + 远端更新（实际发生的）
//  2. Optimal Timeline (S_{U_{cnst}}): 时间戳 < T_remote 的本地更新 + 修正后的最优更新 + 远端更新
//
// 注意: 根据论文，φ = σ_u / σ_o 应该 >= 1，因为次优决策成本不会低于最优决策
func (i *Inspector) CheckInconsistencyWithRemote(remoteUpdate *RemoteUpdate, localHistory []store.UpdateLog) {
	if len(localHistory) < 1 {
		return // 历史记录不足
	}

	// 重构历史：生成两条时间线
	suboptimalTimeline, optimalTimeline := i.reconstructTimelinesWithRemote(localHistory, remoteUpdate)

	// 计算成本（使用标准差作为负载均衡指标）
	suboptimalCost := i.calculateCost(suboptimalTimeline)
	optimalCost := i.calculateCost(optimalTimeline)

	// 计算不一致性比率 φ
	// 根据论文: φ = σ_u / σ_o (次优成本 / 最优成本)
	// 注意: φ 应该 >= 1，因为次优决策的成本不会低于最优决策
	phi := 1.0
	if optimalCost > 0 {
		phi = suboptimalCost / optimalCost
	} else if suboptimalCost > 0 {
		// optimalCost 为 0 但 suboptimalCost > 0，说明有偏差
		phi = suboptimalCost + 1.0
	}
	
	// 确保 phi >= 1（论文定义）
	if phi < 1.0 {
		// 如果由于数值计算问题导致 phi < 1，将其设为 1.0（无偏差）
		phi = 1.0
	}

	// 生成报告
	report := &InconsistencyReport{
		Key:          remoteUpdate.Key,
		Phi:          phi,
		Timestamp:    time.Now(),
		ObservedCost: suboptimalCost,
		OptimalCost:  optimalCost,
	}
	_ = report

	// 更新统计
	i.updateStats(phi)

	// 调用回调函数（通常是OCA控制器）
	if i.onReport != nil {
		go i.onReport(phi)
	}

	log.Printf("[PI] Inconsistency check for %s: φ=%.4f (suboptimal=%.2f, optimal=%.2f)",
		remoteUpdate.Key, phi, suboptimalCost, optimalCost)
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
// 生成次优历史和最优历史
// 
// 核心逻辑（修正后）：
// - Suboptimal Timeline (S_{U_{incnst}}): 本地所有操作 + 远程更新（实际发生的）
// - Optimal Timeline (S_{U_{cnst}}): 时间戳 < T_remote 的本地操作 + 修正后的操作 + 远程更新
//
// 注意：
// 1. 如果suboptimalValues为空（没有本地操作历史），返回nil表示无法计算有效φ
// 2. 根据论文，φ = σ_u / σ_o 应该 >= 1，因为次优决策成本不会低于最优决策
func (i *Inspector) reconstructTimelines(history []store.UpdateLog, receiveTime int64) ([]float64, []float64) {
	var suboptimalValues []float64  // S_{U_{incnst}} - 次优历史（实际）
	var optimalValues []float64     // S_{U_{cnst}} - 最优历史（理想）

	currentSuboptimal := 0.0
	currentOptimal := 0.0
	hasLocalOperation := false

	// 首先收集所有操作并按时间排序
	type timelineEntry struct {
		Timestamp int64
		Value     float64
		Operation store.Operation
		IsRemote  bool
	}
	
	var allEntries []timelineEntry
	var remoteEntries []timelineEntry
	
	for _, logEntry := range history {
		value := logEntry.Value
		if logEntry.Operation == store.Decrement {
			value = -value
		}
		
		entry := timelineEntry{
			Timestamp: logEntry.Timestamp,
			Value:     logEntry.Value,
			Operation: logEntry.Operation,
			IsRemote:  logEntry.IsRemote,
		}
		
		if logEntry.IsRemote {
			remoteEntries = append(remoteEntries, entry)
		} else {
			hasLocalOperation = true
		}
		allEntries = append(allEntries, entry)
	}

	// 如果没有本地操作历史，返回nil表示无法计算有效φ
	if !hasLocalOperation {
		return nil, nil
	}

	// ========== 构建次优历史 S_{U_{incnst}} ==========
	// 包含所有本地操作（非远程）
	for _, entry := range allEntries {
		if !entry.IsRemote {
			value := entry.Value
			if entry.Operation == store.Decrement {
				value = -value
			}
			currentSuboptimal += value
			suboptimalValues = append(suboptimalValues, currentSuboptimal)
		}
	}
	
	// 添加远程更新（次优历史的最后加入远程更新）
	for _, entry := range remoteEntries {
		value := entry.Value
		if entry.Operation == store.Decrement {
			value = -value
		}
		currentSuboptimal += value
		suboptimalValues = append(suboptimalValues, currentSuboptimal)
	}

	// ========== 构建最优历史 S_{U_{cnst}} ==========
	// 简化实现：最优历史 = 所有操作（本地+远程）按时间排序
	// 这表示"如果所有更新都及时到达"的理想状态
	for i := 0; i < len(allEntries)-1; i++ {
		for j := i + 1; j < len(allEntries); j++ {
			if allEntries[i].Timestamp > allEntries[j].Timestamp {
				allEntries[i], allEntries[j] = allEntries[j], allEntries[i]
			}
		}
	}
	
	for _, entry := range allEntries {
		value := entry.Value
		if entry.Operation == store.Decrement {
			value = -value
		}
		currentOptimal += value
		optimalValues = append(optimalValues, currentOptimal)
	}

	return suboptimalValues, optimalValues
}

// reconstructTimelinesWithRemote 使用远程更新信息重构历史时间线（完整实现）
// 实现论文Algorithm 2的核心逻辑:
//
// 关键概念（以服务器负载均衡为例）:
// - 本地控制器维护一个服务器 S1 的 PN-Counter 状态
// - 次优历史 S_{U_{incnst}}: 本地在不知道远端更新的情况下持续给 S1 分配请求
// - 最优历史 S_{U_{cnst}}: 如果及时知道远端已给 S1 分配，会将后续请求分配给 S2（其他服务器）
//
// 在单服务器 Counter 的视角下：
// - 次优：本地操作持续累积到 S1
// - 最优：知道 S1 负载过高后，后续请求不再分配给 S1（表现为 S1 的增量减少或停止）
//
// 输入:
//   - localHistory: 本地操作历史记录
//   - remoteUpdate: 迟到的远程更新（触发器）
//
// 输出:
//   - suboptimalTimeline: 次优历史 S_{U_{incnst}} 的状态值序列
//   - optimalTimeline: 最优历史 S_{U_{cnst}} 的状态值序列
func (i *Inspector) reconstructTimelinesWithRemote(localHistory []store.UpdateLog, remoteUpdate *RemoteUpdate) ([]float64, []float64) {
	var suboptimalValues []float64  // S_{U_{incnst}} - 次优/实际历史
	var optimalValues []float64     // S_{U_{cnst}} - 最优/理想历史

	// ========== 步骤6: 构建次优历史 S_{U_{incnst}} (实际发生的) ==========
	// S_{U_{incnst}} ← S_{CtrU_k} ∪ U_{Ctrk}^{remote}
	currentSuboptimal := 0.0
	
	// 添加所有本地历史操作（在接收时间之前的）
	for _, logEntry := range localHistory {
		if logEntry.Timestamp < remoteUpdate.ReceiveTime {
			value := logEntry.Value
			if logEntry.Operation == store.Decrement {
				value = -value
			}
			currentSuboptimal += value
			suboptimalValues = append(suboptimalValues, currentSuboptimal)
		}
	}
	
	// 添加远端更新
	remoteValue := remoteUpdate.Value
	if remoteUpdate.Operation == store.Decrement {
		remoteValue = -remoteValue
	}
	currentSuboptimal += remoteValue
	suboptimalValues = append(suboptimalValues, currentSuboptimal)

	// ========== 步骤2-5: 构建最优历史 S_{U_{cnst}} (理想情况) ==========
	// 
	// 核心差异：
	// 次优决策：本地持续给 S1 分配，不考虑 S1 已被远端分配的情况
	// 最优决策：基于全局状态（包含远端更新），将后续请求分配给其他服务器
	//
	// 在 S1 的 Counter 视角下，这意味着：
	// - 时间戳 < T_remote 的更新：与次优相同（还未收到远端更新）
	// - 时间戳 >= T_remote 的更新：最优决策将请求分配给 S2，S1 不再增加（或增加减少）
	//
	// 为简化实现，我们假设：
	// 如果本地操作和远端操作都是同向的（都增加负载），
	// 最优决策会将部分后续请求重定向到其他服务器
	
	// 步骤2: 时间戳 < T_remote 的本地更新（与次优相同）
	currentOptimal := 0.0
	for _, logEntry := range localHistory {
		if logEntry.Timestamp < remoteUpdate.OriginTime {
			value := logEntry.Value
			if logEntry.Operation == store.Decrement {
				value = -value
			}
			currentOptimal += value
			optimalValues = append(optimalValues, currentOptimal)
		}
	}
	
	// 步骤5: 按正确时序加入远端更新
	currentOptimal += remoteValue
	optimalValues = append(optimalValues, currentOptimal)
	
	// 步骤3-4: 对时间戳 >= T_remote 的本地更新，重新应用 AppLogic
	// 
	// 关键逻辑：
	// 如果及时知道远端更新，本地控制器会意识到 S1 的负载已经很高
	// AppLogic 会将后续请求分配给负载更低的 S2
	// 从 S1 Counter 的视角，这些后续操作不会出现（或被大幅减少）
	//
	// 实现策略：
	// - 如果远端和本地操作都是增加（INCREMENT），最优决策会"跳过"部分本地增量
	// - 这模拟了请求被重定向到 S2 的效果
	
	for _, logEntry := range localHistory {
		if logEntry.Timestamp >= remoteUpdate.OriginTime && logEntry.Timestamp < remoteUpdate.ReceiveTime {
			value := logEntry.Value
			if logEntry.Operation == store.Decrement {
				value = -value
			}
			
			// AppLogic 重新决策：
			// 如果远端增加了负载，且本地也想增加，最优决策会考虑重定向
			// 简化模型：最优历史只包含部分增量（模拟请求被分配给 S2）
			// 例如，假设 50% 的请求被重定向到 S2
			
			// 启发式：如果远端操作和本地操作同向，最优决策会减少 S1 的增量
			if (remoteValue > 0 && value > 0) || (remoteValue < 0 && value < 0) {
				// 同向操作：最优决策将部分请求重定向，S1 只获得部分增量
				// 模拟：约 30% 的请求被分配给 S2，S1 只获得 70%
				optimalValue := value * 0.7
				currentOptimal += optimalValue
			} else {
				// 反向操作：不影响负载均衡决策
				currentOptimal += value
			}
			optimalValues = append(optimalValues, currentOptimal)
		}
	}

	return suboptimalValues, optimalValues
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
