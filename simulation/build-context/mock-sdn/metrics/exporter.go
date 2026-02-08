package metrics

import (
	"fmt"
	"sync"
	"time"

	ac "github.com/weufhsos/adaptive_sync_go"
)

// Exporter Prometheus指标导出器
type Exporter struct {
	nodeID string
	mu     sync.RWMutex

	// 请求指标
	requestTotal   int64
	requestSuccess int64
	requestFailed  int64
	requestLatency []float64 // 最近的延迟样本

	// 带宽指标
	bandwidthAllocated map[string]float64
	bandwidthReleased  map[string]float64
	linkLoad           map[string]float64

	// AC指标
	acActiveStates  int64
	acTotalUpdates  int64
	acSuccessfulOps int64
	acFailedOps     int64

	// AC一致性指标（新增）
	acPhiValue        float64 // 当前不一致性比率
	acConsistencyQS   int     // 当前队列大小CL_QS
	acConsistencyTO   float64 // 当前超时时间CL_TO（毫秒）
	acConflicts       int64   // 冲突次数
	acSyncLatency     float64 // 同步延迟（毫秒）
}

// NewExporter 创建指标导出器
func NewExporter(nodeID string) *Exporter {
	return &Exporter{
		nodeID:             nodeID,
		bandwidthAllocated: make(map[string]float64),
		bandwidthReleased:  make(map[string]float64),
		linkLoad:           make(map[string]float64),
		requestLatency:     make([]float64, 0, 100),
	}
}

// RecordRequest 记录请求
func (e *Exporter) RecordRequest(success bool, latency time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.requestTotal++
	if success {
		e.requestSuccess++
	} else {
		e.requestFailed++
	}

	// 保存延迟样本（保留最近100个）
	latencyMs := float64(latency.Microseconds()) / 1000.0
	e.requestLatency = append(e.requestLatency, latencyMs)
	if len(e.requestLatency) > 100 {
		e.requestLatency = e.requestLatency[1:]
	}
}

// RecordBandwidthAllocated 记录带宽分配
func (e *Exporter) RecordBandwidthAllocated(linkID string, amount float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bandwidthAllocated[linkID] += amount
}

// RecordBandwidthReleased 记录带宽释放
func (e *Exporter) RecordBandwidthReleased(linkID string, amount float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bandwidthReleased[linkID] += amount
}

// RecordLinkLoad 记录链路负载
func (e *Exporter) RecordLinkLoad(linkID string, load float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.linkLoad[linkID] = load
}

// RecordACStats 记录AC统计信息
func (e *Exporter) RecordACStats(stats *ac.ManagerStats) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.acActiveStates = int64(stats.ActiveStates)
	e.acTotalUpdates = int64(stats.TotalUpdates)
	e.acSuccessfulOps = int64(stats.SuccessfulOps)
	e.acFailedOps = int64(stats.FailedOps)
}

// RecordACConsistency 记录AC一致性指标
func (e *Exporter) RecordACConsistency(phi float64, queueSize int, timeout time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.acPhiValue = phi
	e.acConsistencyQS = queueSize
	e.acConsistencyTO = float64(timeout.Milliseconds())
}

// RecordACConflict 记录AC冲突
func (e *Exporter) RecordACConflict() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.acConflicts++
}

// RecordACSyncLatency 记录AC同步延迟
func (e *Exporter) RecordACSyncLatency(latency time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.acSyncLatency = float64(latency.Milliseconds())
}

// GetMetrics 获取所有指标（用于HTTP接口）
func (e *Exporter) GetMetrics() map[string]interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// 计算平均延迟
	var avgLatency float64
	if len(e.requestLatency) > 0 {
		sum := 0.0
		for _, l := range e.requestLatency {
			sum += l
		}
		avgLatency = sum / float64(len(e.requestLatency))
	}

	// 计算P95延迟
	var p95Latency float64
	if len(e.requestLatency) > 0 {
		sorted := make([]float64, len(e.requestLatency))
		copy(sorted, e.requestLatency)
		// 简单排序
		for i := 0; i < len(sorted)-1; i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[i] > sorted[j] {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		idx := int(float64(len(sorted)) * 0.95)
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		p95Latency = sorted[idx]
	}

	return map[string]interface{}{
		"node_id": e.nodeID,
		"requests": map[string]interface{}{
			"total":        e.requestTotal,
			"success":      e.requestSuccess,
			"failed":       e.requestFailed,
			"avg_latency":  avgLatency,
			"p95_latency":  p95Latency,
			"success_rate": e.getSuccessRate(),
		},
		"bandwidth": map[string]interface{}{
			"allocated": e.bandwidthAllocated,
			"released":  e.bandwidthReleased,
		},
		"links": e.linkLoad,
		"ac": map[string]interface{}{
			"active_states":  e.acActiveStates,
			"total_updates":  e.acTotalUpdates,
			"successful_ops": e.acSuccessfulOps,
			"failed_ops":     e.acFailedOps,
		},
	}
}

// getSuccessRate 获取成功率
func (e *Exporter) getSuccessRate() float64 {
	if e.requestTotal == 0 {
		return 1.0
	}
	return float64(e.requestSuccess) / float64(e.requestTotal)
}

// GetPrometheusMetrics 生成Prometheus格式的指标
func (e *Exporter) GetPrometheusMetrics() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var metrics string

	// 请求指标
	metrics += "# HELP sdn_request_total Total number of requests\n"
	metrics += "# TYPE sdn_request_total counter\n"
	metrics += "sdn_request_total{node=\"" + e.nodeID + "\"} " + itoa(e.requestTotal) + "\n"

	metrics += "# HELP sdn_request_success Number of successful requests\n"
	metrics += "# TYPE sdn_request_success counter\n"
	metrics += "sdn_request_success{node=\"" + e.nodeID + "\"} " + itoa(e.requestSuccess) + "\n"

	metrics += "# HELP sdn_request_rejected Number of rejected requests\n"
	metrics += "# TYPE sdn_request_rejected counter\n"
	metrics += "sdn_request_rejected{node=\"" + e.nodeID + "\"} " + itoa(e.requestFailed) + "\n"

	// 链路负载
	metrics += "# HELP sdn_link_bandwidth_load Current link load in Mbps\n"
	metrics += "# TYPE sdn_link_bandwidth_load gauge\n"
	for linkID, load := range e.linkLoad {
		metrics += "sdn_link_bandwidth_load{node=\"" + e.nodeID + "\",link=\"" + linkID + "\"} " + ftoa(load) + "\n"
	}

	// AC指标
	metrics += "# HELP ac_active_states Number of active states in AC\n"
	metrics += "# TYPE ac_active_states gauge\n"
	metrics += "ac_active_states{node=\"" + e.nodeID + "\"} " + itoa(e.acActiveStates) + "\n"

	metrics += "# HELP ac_total_updates Total AC updates\n"
	metrics += "# TYPE ac_total_updates counter\n"
	metrics += "ac_total_updates{node=\"" + e.nodeID + "\"} " + itoa(e.acTotalUpdates) + "\n"

	// AC一致性指标（新增）
	metrics += "# HELP ac_phi_value Current inconsistency ratio (φ)\n"
	metrics += "# TYPE ac_phi_value gauge\n"
	metrics += "ac_phi_value{node=\"" + e.nodeID + "\"} " + ftoa(e.acPhiValue) + "\n"

	metrics += "# HELP ac_consistency_level_qs Current consistency level queue size\n"
	metrics += "# TYPE ac_consistency_level_qs gauge\n"
	metrics += "ac_consistency_level_qs{node=\"" + e.nodeID + "\"} " + itoa(int64(e.acConsistencyQS)) + "\n"

	metrics += "# HELP ac_consistency_level_to Current consistency level timeout (ms)\n"
	metrics += "# TYPE ac_consistency_level_to gauge\n"
	metrics += "ac_consistency_level_to{node=\"" + e.nodeID + "\"} " + ftoa(e.acConsistencyTO) + "\n"

	metrics += "# HELP ac_conflicts_total Total number of consistency conflicts\n"
	metrics += "# TYPE ac_conflicts_total counter\n"
	metrics += "ac_conflicts_total{node=\"" + e.nodeID + "\"} " + itoa(e.acConflicts) + "\n"

	metrics += "# HELP ac_sync_latency_ms Current sync latency (ms)\n"
	metrics += "# TYPE ac_sync_latency_ms gauge\n"
	metrics += "ac_sync_latency_ms{node=\"" + e.nodeID + "\"} " + ftoa(e.acSyncLatency) + "\n"

	return metrics
}

func itoa(n int64) string {
	return fmt.Sprintf("%d", n)
}

func ftoa(f float64) string {
	return fmt.Sprintf("%.2f", f)
}
