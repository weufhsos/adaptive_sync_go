package generator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// RequestResult 请求结果
type RequestResult struct {
	RequestID   string
	TargetNode  string
	Success     bool
	Error       string
	Latency     time.Duration
	Bandwidth   float64
	AllocatedOn string
}

// Stats 统计信息
type Stats struct {
	TotalRequests int64
	Successful    int64
	Failed        int64
	AvgLatency    time.Duration
	P95Latency    time.Duration
	latencies     []time.Duration
}

// RequestGenerator 请求生成器
type RequestGenerator struct {
	targetNodes  []string
	rps          float64
	duration     time.Duration
	pattern      string
	minBandwidth float64
	maxBandwidth float64
	minHoldTime  time.Duration
	maxHoldTime  time.Duration

	client   *http.Client
	stats    Stats
	statsMu  sync.Mutex
	onResult func(*RequestResult)

	stopChan  chan struct{}
	doneChan  chan struct{}
	isRunning int32
}

// NewRequestGenerator 创建请求生成器
func NewRequestGenerator(
	targetNodes []string,
	rps float64,
	duration time.Duration,
	pattern string,
	minBandwidth, maxBandwidth float64,
	minHoldTime, maxHoldTime time.Duration,
) *RequestGenerator {
	return &RequestGenerator{
		targetNodes:  targetNodes,
		rps:          rps,
		duration:     duration,
		pattern:      pattern,
		minBandwidth: minBandwidth,
		maxBandwidth: maxBandwidth,
		minHoldTime:  minHoldTime,
		maxHoldTime:  maxHoldTime,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
	}
}

// SetOnResult 设置结果回调
func (g *RequestGenerator) SetOnResult(callback func(*RequestResult)) {
	g.onResult = callback
}

// Start 启动生成器
func (g *RequestGenerator) Start() error {
	if !atomic.CompareAndSwapInt32(&g.isRunning, 0, 1) {
		return fmt.Errorf("generator already running")
	}

	go g.run()
	return nil
}

// Stop 停止生成器
func (g *RequestGenerator) Stop() {
	if atomic.CompareAndSwapInt32(&g.isRunning, 1, 0) {
		close(g.stopChan)
		<-g.doneChan
	}
}

// Done 返回完成通道
func (g *RequestGenerator) Done() <-chan struct{} {
	return g.doneChan
}

// GetStats 获取统计信息
func (g *RequestGenerator) GetStats() Stats {
	g.statsMu.Lock()
	defer g.statsMu.Unlock()

	stats := g.stats
	stats.TotalRequests = atomic.LoadInt64(&g.stats.TotalRequests)
	stats.Successful = atomic.LoadInt64(&g.stats.Successful)
	stats.Failed = atomic.LoadInt64(&g.stats.Failed)

	// 计算平均延迟和P95
	if len(g.stats.latencies) > 0 {
		var sum time.Duration
		for _, l := range g.stats.latencies {
			sum += l
		}
		stats.AvgLatency = sum / time.Duration(len(g.stats.latencies))

		// P95
		sorted := make([]time.Duration, len(g.stats.latencies))
		copy(sorted, g.stats.latencies)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i] < sorted[j]
		})
		idx := int(float64(len(sorted)) * 0.95)
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		stats.P95Latency = sorted[idx]
	}

	return stats
}

// run 运行生成循环
func (g *RequestGenerator) run() {
	defer close(g.doneChan)

	interval := time.Duration(float64(time.Second) / g.rps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	timeout := time.After(g.duration)
	requestCount := 0

	for {
		select {
		case <-g.stopChan:
			return
		case <-timeout:
			return
		case <-ticker.C:
			requestCount++
			// 同步发送请求，确保请求间隔稳定
			// 这避免了goroutine堆积导致的请求风暴
			g.sendRequest(requestCount)
		}
	}
}

// sendRequest 发送单个请求
func (g *RequestGenerator) sendRequest(id int) {
	// 选择目标节点
	targetNode := g.selectTarget()

	// 生成请求参数
	bandwidth := g.minBandwidth + rand.Float64()*(g.maxBandwidth-g.minBandwidth)
	holdTime := g.minHoldTime + time.Duration(rand.Float64()*float64(g.maxHoldTime-g.minHoldTime))

	requestID := fmt.Sprintf("req-%d-%d", time.Now().UnixNano(), id)

	result := &RequestResult{
		RequestID:  requestID,
		TargetNode: targetNode,
		Bandwidth:  bandwidth,
	}

	// DEBUG: 打印配置值
	log.Printf("[LoadGen] DEBUG: minBandwidth=%.2f, maxBandwidth=%.2f, generated bandwidth=%.2f",
		g.minBandwidth, g.maxBandwidth, bandwidth)

	// 发送请求（使用全局资源模式，确保所有节点操作同一个AC key）
	startTime := time.Now()
	cost := bandwidth // 使用bandwidth作为cost值
	durationMs := holdTime.Milliseconds()
	
	// DEBUG: 确认cost值
	log.Printf("[LoadGen] DEBUG: cost=%.2f, durationMs=%d", cost, durationMs)
	embedReq := map[string]interface{}{
		"cost":         cost,
		"duration_ms":  durationMs,
		"use_global":   true, // 关键：使用全局状态，让所有节点操作同一个key
	}
	
	body, err := json.Marshal(embedReq)
	if err != nil {
		log.Printf("[LoadGen] Failed to marshal request: %v", err)
		result.Success = false
		result.Error = err.Error()
		return
	}
	
	log.Printf("[LoadGen] Sending embed request to %s: cost=%.2f%%, duration=%dms, use_global=true, body=%s", 
		targetNode, cost, durationMs, string(body))

	url := fmt.Sprintf("http://%s/api/v1/embed", targetNode)

	resp, err := g.client.Post(url, "application/json", bytes.NewReader(body))
	result.Latency = time.Since(startTime)

	if err != nil {
		result.Success = false
		result.Error = err.Error()
		atomic.AddInt64(&g.stats.Failed, 1)
	} else {
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			result.Success = true
			atomic.AddInt64(&g.stats.Successful, 1)

			// 解析响应获取分配的服务器
			var embedResp map[string]interface{}
			if json.NewDecoder(resp.Body).Decode(&embedResp) == nil {
				if serverID, ok := embedResp["server_id"].(string); ok {
					result.AllocatedOn = serverID
				}
				// 同时尝试旧格式（向后兼容）
				if result.AllocatedOn == "" {
					if linkID, ok := embedResp["link_id"].(string); ok {
						result.AllocatedOn = linkID
					}
				}
			}
		} else {
			result.Success = false
			result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
			atomic.AddInt64(&g.stats.Failed, 1)
		}
	}

	atomic.AddInt64(&g.stats.TotalRequests, 1)

	// 记录延迟
	g.statsMu.Lock()
	g.stats.latencies = append(g.stats.latencies, result.Latency)
	// 保留最近1000个样本
	if len(g.stats.latencies) > 1000 {
		g.stats.latencies = g.stats.latencies[1:]
	}
	g.statsMu.Unlock()

	// 调用回调
	if g.onResult != nil {
		g.onResult(result)
	}
}

// selectTarget 选择目标节点
func (g *RequestGenerator) selectTarget() string {
	switch g.pattern {
	case "round-robin":
		// 简单轮询
		idx := atomic.LoadInt64(&g.stats.TotalRequests) % int64(len(g.targetNodes))
		return g.targetNodes[idx]
	case "random":
		return g.targetNodes[rand.Intn(len(g.targetNodes))]
	case "poisson":
		// Poisson分布的到达时间已经在ticker中实现
		// 这里简单随机选择节点
		return g.targetNodes[rand.Intn(len(g.targetNodes))]
	default: // uniform
		return g.targetNodes[rand.Intn(len(g.targetNodes))]
	}
}
