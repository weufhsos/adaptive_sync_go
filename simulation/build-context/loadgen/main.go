package main

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/weufhsos/adaptive_sync_go/simulation/loadgen/generator"
)

// Config 配置
type Config struct {
	TargetNodes  []string
	RPS          float64
	Duration     time.Duration
	Pattern      string
	MinBandwidth float64
	MaxBandwidth float64
	MinHoldTime  time.Duration
	MaxHoldTime  time.Duration
}

func main() {
	log.Printf("[LoadGen] Starting load generator...")

	config := loadConfig()
	log.Printf("[LoadGen] Config: Targets=%v, RPS=%.1f, Duration=%v, Pattern=%s",
		config.TargetNodes, config.RPS, config.Duration, config.Pattern)

	// 创建请求生成器
	gen := generator.NewRequestGenerator(
		config.TargetNodes,
		config.RPS,
		config.Duration,
		config.Pattern,
		config.MinBandwidth,
		config.MaxBandwidth,
		config.MinHoldTime,
		config.MaxHoldTime,
	)

	// 设置结果回调
	gen.SetOnResult(func(result *generator.RequestResult) {
		if result.Success {
			log.Printf("[LoadGen] Request %s -> %s: SUCCESS (%.2fms)",
				result.RequestID, result.TargetNode, result.Latency.Seconds()*1000)
		} else {
			log.Printf("[LoadGen] Request %s -> %s: FAILED - %s",
				result.RequestID, result.TargetNode, result.Error)
		}
	})

	// 启动生成器
	if err := gen.Start(); err != nil {
		log.Fatalf("[LoadGen] Failed to start: %v", err)
	}

	log.Printf("[LoadGen] Load generator started")

	// 等待完成或中断
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigChan:
		log.Printf("[LoadGen] Received interrupt signal")
	case <-gen.Done():
		log.Printf("[LoadGen] Test duration completed")
	}

	// 停止生成器
	gen.Stop()

	// 打印统计
	stats := gen.GetStats()
	log.Printf("[LoadGen] ========== Final Statistics ==========")
	log.Printf("[LoadGen] Total Requests: %d", stats.TotalRequests)
	log.Printf("[LoadGen] Successful: %d (%.2f%%)", stats.Successful,
		float64(stats.Successful)/float64(stats.TotalRequests)*100)
	log.Printf("[LoadGen] Failed: %d", stats.Failed)
	log.Printf("[LoadGen] Avg Latency: %.2fms", stats.AvgLatency.Seconds()*1000)
	log.Printf("[LoadGen] P95 Latency: %.2fms", stats.P95Latency.Seconds()*1000)
	log.Printf("[LoadGen] ========================================")
}

func loadConfig() *Config {
	config := &Config{
		RPS:          getEnvFloat("RPS", 10),
		Duration:     getEnvDuration("DURATION", 60*time.Second),
		Pattern:      getEnv("PATTERN", "uniform"),
		MinBandwidth: getEnvFloat("MIN_BANDWIDTH", 10),
		MaxBandwidth: getEnvFloat("MAX_BANDWIDTH", 100),
		MinHoldTime:  getEnvDuration("MIN_HOLD_TIME", 1*time.Second),
		MaxHoldTime:  getEnvDuration("MAX_HOLD_TIME", 10*time.Second),
	}

	// 解析目标节点
	targetsStr := getEnv("TARGET_NODES", "localhost:8080")
	config.TargetNodes = strings.Split(targetsStr, ",")

	return config
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}
