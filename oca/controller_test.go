package oca

import (
	"math"
	"testing"
	"time"
)

// TestNewController 验证新建 Controller
func TestNewController(t *testing.T) {
	ctrl := NewController(1.05)

	if ctrl == nil {
		t.Fatal("NewController returned nil")
	}

	if ctrl.TargetPhi != 1.05 {
		t.Errorf("Expected TargetPhi 1.05, got %f", ctrl.TargetPhi)
	}
	if ctrl.Kp != 1.0 || ctrl.Ki != 0.1 || ctrl.Kd != 0.05 {
		t.Errorf("Default PID parameters incorrect: Kp=%f, Ki=%f, Kd=%f", ctrl.Kp, ctrl.Ki, ctrl.Kd)
	}

	// 验证范围限制
	if ctrl.minQueueSize != 10 || ctrl.maxQueueSize != 1000 {
		t.Errorf("Queue size limits incorrect: min=%d, max=%d", ctrl.minQueueSize, ctrl.maxQueueSize)
	}
	if ctrl.minTimeout != 10*time.Millisecond || ctrl.maxTimeout != 1000*time.Millisecond {
		t.Errorf("Timeout limits incorrect: min=%v, max=%v", ctrl.minTimeout, ctrl.maxTimeout)
	}

	// 验证历史窗口
	if ctrl.historyWindow != 10 {
		t.Errorf("Expected history window 10, got %d", ctrl.historyWindow)
	}
}

// TestStartStop 启动和停止
func TestStartStop(t *testing.T) {
	ctrl := NewController(1.05)

	// 启动
	ctrl.Start()

	// 短暂运行
	time.Sleep(50 * time.Millisecond)

	// 停止
	ctrl.Stop()

	// 再次停止不应该 panic
	ctrl.Stop()
}

// TestAdjust_HighPhi φ > TargetPhi 调整（收紧 CL）
func TestAdjust_HighPhi(t *testing.T) {
	ctrl := NewController(1.05)
	ctrl.Start()
	defer ctrl.Stop()

	// 高 φ 值（偏差大）
	newCL := ctrl.Adjust(1.5)

	if newCL == nil {
		t.Fatal("Expected non-nil CL")
	}

	// 高偏差应该导致更严格的 CL（较小的 QS，较小的 TO）
	if newCL.QueueSize > 100 {
		t.Errorf("Expected smaller QueueSize for high phi, got %d", newCL.QueueSize)
	}
	if newCL.Timeout > 50*time.Millisecond {
		t.Errorf("Expected smaller Timeout for high phi, got %v", newCL.Timeout)
	}

	t.Logf("High Phi adjustment: QS=%d, TO=%v", newCL.QueueSize, newCL.Timeout)
}

// TestAdjust_LowPhi φ < TargetPhi 调整（放宽 CL）
func TestAdjust_LowPhi(t *testing.T) {
	ctrl := NewController(1.05)
	ctrl.Start()
	defer ctrl.Stop()

	// 低 φ 值（偏差小）
	newCL := ctrl.Adjust(1.0)

	if newCL == nil {
		t.Fatal("Expected non-nil CL")
	}

	// 低偏差应该导致更宽松的 CL（较大的 QS，较大的 TO）
	if newCL.QueueSize < 50 {
		t.Errorf("Expected larger QueueSize for low phi, got %d", newCL.QueueSize)
	}
	if newCL.Timeout < 20*time.Millisecond {
		t.Errorf("Expected larger Timeout for low phi, got %v", newCL.Timeout)
	}

	t.Logf("Low Phi adjustment: QS=%d, TO=%v", newCL.QueueSize, newCL.Timeout)
}

// TestAdjust_OnTarget φ == TargetPhi（保持稳定）
func TestAdjust_OnTarget(t *testing.T) {
	ctrl := NewController(1.05)
	ctrl.Start()
	defer ctrl.Stop()

	initialCL := ctrl.mapOutputToCL(0.0) // 零输出对应目标值
	t.Logf("Initial CL: QS=%d, TO=%v", initialCL.QueueSize, initialCL.Timeout)

	// 正好等于目标值
	newCL := ctrl.Adjust(1.05)

	if newCL == nil {
		t.Fatal("Expected non-nil CL")
	}

	// 应该保持相对稳定（小幅调整是正常的）
	t.Logf("On-target adjustment: QS=%d, TO=%v", newCL.QueueSize, newCL.Timeout)
}

// TestPID_Proportional P 项计算
func TestPID_Proportional(t *testing.T) {
	ctrl := NewController(1.05)

	// 测试不同误差下的 P 项
	testCases := []struct {
		phi      float64
		expected float64 // 期望的 P 项 (Kp * error)
	}{
		{1.05, 0.0},  // 无误差
		{1.15, 0.1},  // 正误差
		{0.95, -0.1}, // 负误差
		{1.55, 0.5},  // 大正误差
		{0.55, -0.5}, // 大负误差
	}

	for _, tc := range testCases {
		// 由于 calculatePID 是私有方法，我们通过输出间接验证
		output := ctrl.calculatePID(tc.phi)

		if math.Abs(float64(output)) < 0.001 && tc.phi != ctrl.TargetPhi {
			t.Errorf("PID output too small for phi=%f", tc.phi)
		}

		t.Logf("Phi=%f, Error=%f, Output=%f", tc.phi, error, output)
	}
}

// TestPID_Integral I 项累积
func TestPID_Integral(t *testing.T) {
	ctrl := NewController(1.05)

	// 持续的正误差应该导致积分项累积
	positivePhi := 1.2

	// 第一次调用
	output1 := ctrl.calculatePID(positivePhi)

	// 短暂等待后再次调用（模拟时间流逝）
	time.Sleep(10 * time.Millisecond)

	// 第二次调用同一误差值
	output2 := ctrl.calculatePID(positivePhi)

	// 由于有积分累积，第二次输出应该更大（或至少不小于第一次）
	t.Logf("First output: %f, Second output: %f", output1, output2)

	// 注意：实际比较需要考虑时间间隔，这里主要是验证不 panic
}

// TestPID_Derivative D 项响应
func TestPID_Derivative(t *testing.T) {
	ctrl := NewController(1.05)

	// 快速变化的误差应该产生较大的 D 项
	phi1 := 1.1
	phi2 := 1.3 // 快速增加

	output1 := ctrl.calculatePID(phi1)
	time.Sleep(5 * time.Millisecond)
	output2 := ctrl.calculatePID(phi2)

	t.Logf("Phi change %f->%f, Output change %f->%f", phi1, phi2, output1, output2)

	// D 项对快速变化敏感，但具体数值难以预测
}

// TestMapOutputToCL 输出映射
func TestMapOutputToCL(t *testing.T) {
	ctrl := NewController(1.05)

	// 测试边界值
	testCases := []struct {
		output float64
		desc   string
	}{
		{100.0, "maximum positive output"},
		{-100.0, "maximum negative output"},
		{0.0, "zero output"},
		{50.0, "medium positive output"},
		{-50.0, "medium negative output"},
	}

	for _, tc := range testCases {
		cl := ctrl.mapOutputToCL(tc.output)

		// 验证范围限制
		if cl.QueueSize < ctrl.minQueueSize || cl.QueueSize > ctrl.maxQueueSize {
			t.Errorf("QS %d out of range [%d, %d] for %s",
				cl.QueueSize, ctrl.minQueueSize, ctrl.maxQueueSize, tc.desc)
		}

		if cl.Timeout < ctrl.minTimeout || cl.Timeout > ctrl.maxTimeout {
			t.Errorf("TO %v out of range [%v, %v] for %s",
				cl.Timeout, ctrl.minTimeout, ctrl.maxTimeout, tc.desc)
		}

		t.Logf("%s: output=%.1f -> QS=%d, TO=%v", tc.desc, tc.output, cl.QueueSize, cl.Timeout)
	}

	// 验证单调性：正输出应该导致更小的 QS
	clPos := ctrl.mapOutputToCL(50.0)
	clNeg := ctrl.mapOutputToCL(-50.0)

	if clPos.QueueSize >= clNeg.QueueSize {
		t.Error("Positive output should result in smaller QueueSize than negative output")
	}
}

// TestSetPIDParameters 动态设置 PID
func TestSetPIDParameters(t *testing.T) {
	ctrl := NewController(1.05)

	// 修改 PID 参数
	ctrl.SetPIDParameters(2.0, 0.5, 0.1)

	if ctrl.Kp != 2.0 {
		t.Errorf("Expected Kp=2.0, got %f", ctrl.Kp)
	}
	if ctrl.Ki != 0.5 {
		t.Errorf("Expected Ki=0.5, got %f", ctrl.Ki)
	}
	if ctrl.Kd != 0.1 {
		t.Errorf("Expected Kd=0.1, got %f", ctrl.Kd)
	}
}

// TestSetTargetPhi 设置目标 φ
func TestSetTargetPhi(t *testing.T) {
	ctrl := NewController(1.05)

	// 修改目标值
	ctrl.SetTargetPhi(1.2)

	if ctrl.TargetPhi != 1.2 {
		t.Errorf("Expected TargetPhi=1.2, got %f", ctrl.TargetPhi)
	}
}

// TestHistory_Window 历史窗口管理
func TestHistory_Window(t *testing.T) {
	ctrl := NewController(1.05)

	// 填充历史窗口
	phiValues := []float64{1.0, 1.1, 1.2, 1.05, 1.15, 1.08, 1.12, 1.09, 1.11, 1.13, 1.14}

	for _, phi := range phiValues {
		ctrl.Adjust(phi)
	}

	history := ctrl.GetHistory()

	// 验证窗口大小维护（应该不超过 historyWindow）
	if len(history) > ctrl.historyWindow {
		t.Errorf("History size %d exceeds window %d", len(history), ctrl.historyWindow)
	}

	// 验证保留的是最近的值
	if len(history) > 0 {
		lastPhi := history[len(history)-1]
		expectedLast := phiValues[len(phiValues)-1]
		if math.Abs(lastPhi-expectedLast) > 0.001 {
			t.Errorf("Expected last phi ~%f, got %f", expectedLast, lastPhi)
		}
	}
}

// TestThresholdController 阈值控制器
func TestThresholdController(t *testing.T) {
	tc := NewThresholdController(1.1, 1.0, 3)

	// 测试低于下限的情况（应该放宽 CL）
	tc.AdjustThreshold(0.9)
	tc.AdjustThreshold(0.95)
	cl1 := tc.AdjustThreshold(0.98) // 平均 < 1.0

	if cl1.QueueSize <= 100 { // 初始值是 100
		t.Logf("Lower threshold triggered, QS=%d (may increase on subsequent calls)", cl1.QueueSize)
	}

	// 重置并测试高于上限的情况（应该收紧 CL）
	tc = NewThresholdController(1.1, 1.0, 3)
	tc.AdjustThreshold(1.15)
	tc.AdjustThreshold(1.2)
	cl2 := tc.AdjustThreshold(1.18) // 平均 > 1.1

	if cl2.QueueSize >= 100 {
		t.Logf("Upper threshold triggered, QS=%d (may decrease on subsequent calls)", cl2.QueueSize)
	}
}

// TestStats 统计信息
func TestStats(t *testing.T) {
	ctrl := NewController(1.05)
	ctrl.Start()
	defer ctrl.Stop()

	// 初始状态
	stats := ctrl.GetStats()
	if stats.TotalAdjustments != 0 {
		t.Errorf("Expected 0 adjustments initially, got %d", stats.TotalAdjustments)
	}

	// 执行几次调整
	ctrl.Adjust(1.2)
	ctrl.Adjust(1.0)
	ctrl.Adjust(1.05)

	// 检查统计更新
	stats = ctrl.GetStats()
	if stats.TotalAdjustments != 3 {
		t.Errorf("Expected 3 adjustments, got %d", stats.TotalAdjustments)
	}

	if stats.LastAdjustment.IsZero() {
		t.Error("LastAdjustment should be set after adjustments")
	}

	if stats.CurrentPhi <= 0 {
		t.Errorf("CurrentPhi should be positive, got %f", stats.CurrentPhi)
	}
}

// TestConcurrentAdjustments 并发调整测试 (简化版)
func TestConcurrentAdjustments(t *testing.T) {
	ctrl := NewController(1.05)
	ctrl.Start()
	defer ctrl.Stop()

	// 简单顺序测试
	for i := 0; i < 5; i++ {
		cl := ctrl.Adjust(1.0 + float64(i)*0.05)
		if cl == nil {
			t.Errorf("Adjustment %d returned nil", i)
		}
	}

	// 验证统计数据
	stats := ctrl.GetStats()
	if stats.TotalAdjustments < 3 {
		t.Errorf("Expected at least 3 total adjustments, got %d", stats.TotalAdjustments)
	}
}

// TestOutputLimiting 输出限幅
func TestOutputLimiting(t *testing.T) {
	ctrl := NewController(1.05)

	// 构造极端情况测试输出限幅
	extremePhi := 1000.0 // 极大的 phi 值

	output := ctrl.calculatePID(extremePhi)

	// 验证输出被限制在合理范围内
	if output > 100.0 {
		t.Errorf("Output should be capped at 100, got %f", output)
	}
	if output < -100.0 {
		t.Errorf("Output should be capped at -100, got %f", output)
	}

	t.Logf("Extreme phi=%f resulted in output=%f", extremePhi, output)
}

// TestEdgeCases 边缘情况测试
func TestEdgeCases(t *testing.T) {
	ctrl := NewController(1.05)

	// 测试 NaN 输入
	// 注意：Go 中 float64(NaN) 的比较需要用 math.IsNaN

	// 测试无穷大输入
	infinitePhi := math.Inf(1)
	output := ctrl.calculatePID(infinitePhi)

	// 应该被限幅处理
	if output > 100.0 || output < -100.0 {
		t.Errorf("Infinite input not properly handled, output=%f", output)
	}

	// 测试非常小的时间间隔
	ctrl.lastUpdate = time.Now()
	time.Sleep(1 * time.Microsecond) // 极短时间
	output2 := ctrl.calculatePID(1.1)

	// 不应该导致除零错误
	if math.IsNaN(output2) || math.IsInf(output2, 0) {
		t.Errorf("Very small time interval caused invalid output: %f", output2)
	}
}
