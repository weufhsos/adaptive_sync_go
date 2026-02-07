package oca

import (
	"log"
	"sync"
	"time"

	"github.com/weufhsos/adaptive_sync_go/dispatcher"
)

// Controller 实现论文中的Algorithm 4: PID-based CL Adaptation
type Controller struct {
	// PID控制器参数
	Kp float64 // 比例系数
	Ki float64 // 积分系数
	Kd float64 // 微分系数

	// 目标值
	TargetPhi float64

	// 历史数据
	historyPhi    []float64
	historyWindow int // 滑动窗口大小

	// PID状态
	integral   float64
	lastError  float64
	lastUpdate time.Time

	// 当前一致性级别范围
	minQueueSize int
	maxQueueSize int
	minTimeout   time.Duration
	maxTimeout   time.Duration

	// 控制通道
	stopChan    chan struct{}
	stoppedChan chan struct{}

	// 统计信息
	stats      *ControllerStats
	statsMutex sync.RWMutex
}

// ControllerStats 控制器统计信息
type ControllerStats struct {
	TotalAdjustments int64
	LastAdjustment   time.Time
	CurrentPhi       float64
	CurrentOutput    float64
}

// NewController 创建新的自适应控制器
func NewController(targetPhi float64) *Controller {
	c := &Controller{
		// 默认PID参数（可根据实际场景调整）
		Kp: 1.0,
		Ki: 0.1,
		Kd: 0.05,

		TargetPhi: targetPhi,

		// 滑动窗口大小
		historyWindow: 10,

		// 一致性级别范围
		minQueueSize: 10,
		maxQueueSize: 1000,
		minTimeout:   10 * time.Millisecond,
		maxTimeout:   1000 * time.Millisecond,

		stopChan:    make(chan struct{}),
		stoppedChan: make(chan struct{}),
		stats:       &ControllerStats{},
		historyPhi:  make([]float64, 0, 10),
	}

	return c
}

// Start 启动控制器
func (c *Controller) Start() {
	log.Printf("[OCA] Online Consistency Adaptation Controller started")
	log.Printf("[OCA] Target Phi: %.4f, PID(Kp:%.2f, Ki:%.2f, Kd:%.2f)",
		c.TargetPhi, c.Kp, c.Ki, c.Kd)
	go c.monitorStats()
}

// Stop 停止控制器
func (c *Controller) Stop() {
	log.Printf("[OCA] Stopping OCA Controller")
	close(c.stopChan)
	<-c.stoppedChan
	log.Printf("[OCA] OCA Controller stopped")
}

// Adjust 根据不一致性比率调整一致性级别
// 实现论文Algorithm 4: PID-based CL Adaptation
func (c *Controller) Adjust(phi float64) *dispatcher.ConsistencyLevel {
	c.statsMutex.Lock()
	c.stats.CurrentPhi = phi
	c.statsMutex.Unlock()

	// 更新历史记录
	c.updateHistory(phi)

	// 计算PID输出
	output := c.calculatePID(phi)

	c.statsMutex.Lock()
	c.stats.CurrentOutput = output
	c.statsMutex.Unlock()

	// 将PID输出映射到一致性级别参数
	newCL := c.mapOutputToCL(output)

	// 记录调整
	c.statsMutex.Lock()
	c.stats.TotalAdjustments++
	c.stats.LastAdjustment = time.Now()
	c.statsMutex.Unlock()

	log.Printf("[OCA] Adjustment - Phi:%.4f Output:%.2f -> QS:%d TO:%v",
		phi, output, newCL.QueueSize, newCL.Timeout)

	return newCL
}

// updateHistory 更新历史记录
func (c *Controller) updateHistory(phi float64) {
	c.historyPhi = append(c.historyPhi, phi)

	// 维护滑动窗口
	if len(c.historyPhi) > c.historyWindow {
		c.historyPhi = c.historyPhi[1:]
	}
}

// calculatePID 计算PID控制器输出
func (c *Controller) calculatePID(phi float64) float64 {
	// 计算误差
	error := phi - c.TargetPhi

	// 计算时间间隔
	now := time.Now()
	dt := time.Since(c.lastUpdate).Seconds()
	if dt <= 0 {
		dt = 0.001 // 防止除零
	}
	c.lastUpdate = now

	// 比例项
	pTerm := c.Kp * error

	// 积分项
	c.integral += error * dt
	iTerm := c.Ki * c.integral

	// 微分项
	dTerm := c.Kd * (error - c.lastError) / dt
	c.lastError = error

	// 总输出
	output := pTerm + iTerm + dTerm

	// 输出限幅（防止过大调整）
	if output > 100.0 {
		output = 100.0
	} else if output < -100.0 {
		output = -100.0
	}

	return output
}

// mapOutputToCL 将PID输出映射到一致性级别参数
func (c *Controller) mapOutputToCL(output float64) *dispatcher.ConsistencyLevel {
	// 输出解释：
	// output > 0: φ > TargetPhi，偏差大，需要更严格的一致性
	// output < 0: φ < TargetPhi，偏差小，可以更宽松的一致性

	// 将输出标准化到[0,1]范围
	normalized := (output + 100.0) / 200.0 // [-100,100] -> [0,1]

	// 计算队列大小（反向映射：output高->QS小）
	queueSizeRange := float64(c.maxQueueSize - c.minQueueSize)
	queueSize := int(float64(c.maxQueueSize) - normalized*queueSizeRange)
	if queueSize < c.minQueueSize {
		queueSize = c.minQueueSize
	}
	if queueSize > c.maxQueueSize {
		queueSize = c.maxQueueSize
	}

	// 计算超时时间（反向映射：output高->TO小）
	timeoutRange := c.maxTimeout - c.minTimeout
	timeout := c.maxTimeout - time.Duration(normalized*float64(timeoutRange))
	if timeout < c.minTimeout {
		timeout = c.minTimeout
	}
	if timeout > c.maxTimeout {
		timeout = c.maxTimeout
	}

	return &dispatcher.ConsistencyLevel{
		QueueSize: queueSize,
		Timeout:   timeout,
	}
}

// SetPIDParameters 设置PID参数
func (c *Controller) SetPIDParameters(kp, ki, kd float64) {
	c.Kp = kp
	c.Ki = ki
	c.Kd = kd
	log.Printf("[OCA] PID parameters updated: Kp=%.2f, Ki=%.2f, Kd=%.2f", kp, ki, kd)
}

// SetTargetPhi 设置目标不一致性比率
func (c *Controller) SetTargetPhi(targetPhi float64) {
	c.TargetPhi = targetPhi
	log.Printf("[OCA] Target Phi updated: %.4f", targetPhi)
}

// GetStats 获取统计信息
func (c *Controller) GetStats() *ControllerStats {
	c.statsMutex.RLock()
	defer c.statsMutex.RUnlock()

	stats := *c.stats
	return &stats
}

// GetHistory 获取历史φ值
func (c *Controller) GetHistory() []float64 {
	// 返回副本
	history := make([]float64, len(c.historyPhi))
	copy(history, c.historyPhi)
	return history
}

// monitorStats 监控统计信息
func (c *Controller) monitorStats() {
	defer close(c.stoppedChan)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			stats := c.GetStats()
			history := c.GetHistory()

			log.Printf("[OCA] Stats - Adjustments:%d Last:%v CurrentPhi:%.4f Output:%.2f",
				stats.TotalAdjustments, stats.LastAdjustment.Format("15:04:05"),
				stats.CurrentPhi, stats.CurrentOutput)

			if len(history) > 0 {
				log.Printf("[OCA] Recent Phi history: %v", history)
			}
		}
	}
}

// ========== 阈值控制算法（备选方案） ==========

// ThresholdController 基于阈值的一致性自适应（Algorithm 3）
type ThresholdController struct {
	upperThreshold float64 // TU: 上限阈值
	lowerThreshold float64 // TL: 下限阈值
	windowSize     int     // W: 滑动窗口大小

	historyPhi []float64
	currentCL  dispatcher.ConsistencyLevel
}

// NewThresholdController 创建阈值控制器
func NewThresholdController(upper, lower float64, window int) *ThresholdController {
	return &ThresholdController{
		upperThreshold: upper,
		lowerThreshold: lower,
		windowSize:     window,
		historyPhi:     make([]float64, 0, window),
		currentCL: dispatcher.ConsistencyLevel{
			QueueSize: 100,
			Timeout:   50 * time.Millisecond,
		},
	}
}

// AdjustThreshold 基于阈值的调整逻辑
func (tc *ThresholdController) AdjustThreshold(phi float64) *dispatcher.ConsistencyLevel {
	// 更新历史
	tc.historyPhi = append(tc.historyPhi, phi)
	if len(tc.historyPhi) > tc.windowSize {
		tc.historyPhi = tc.historyPhi[1:]
	}

	// 计算窗口平均值
	if len(tc.historyPhi) < tc.windowSize {
		return &tc.currentCL // 数据不足，保持当前配置
	}

	sum := 0.0
	for _, val := range tc.historyPhi {
		sum += val
	}
	avgPhi := sum / float64(len(tc.historyPhi))

	// 阈值判断和调整
	if avgPhi >= tc.upperThreshold {
		// 偏差过大，提高一致性级别（收紧限制）
		tc.raiseCL()
	} else if avgPhi <= tc.lowerThreshold {
		// 偏差过小，降低一致性级别（放宽限制）
		tc.lowerCL()
	}
	// 否则保持不变

	return &tc.currentCL
}

// raiseCL 提高一致性级别（收紧限制）
func (tc *ThresholdController) raiseCL() {
	// 减小队列大小，减小超时时间
	if tc.currentCL.QueueSize > 10 {
		tc.currentCL.QueueSize -= 10
	}
	if tc.currentCL.Timeout > 10*time.Millisecond {
		tc.currentCL.Timeout -= 5 * time.Millisecond
	}
}

// lowerCL 降低一致性级别（放宽限制）
func (tc *ThresholdController) lowerCL() {
	// 增大队列大小，增大超时时间
	if tc.currentCL.QueueSize < 1000 {
		tc.currentCL.QueueSize += 20
	}
	if tc.currentCL.Timeout < 1000*time.Millisecond {
		tc.currentCL.Timeout += 10 * time.Millisecond
	}
}
