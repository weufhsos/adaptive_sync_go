package main

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/api"
	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/controller"
	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/metrics"
	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/simulator"
)

// Config 配置结构
type Config struct {
	NodeID       string
	GRPCPort     int
	HTTPPort     int
	Peers        []string
	TargetPhi    float64
	LinkCapacity float64
}

func main() {
	log.Printf("[Main] Starting mock-SDN Controller...")

	// 加载配置
	config := loadConfig()
	log.Printf("[Main] Config: NodeID=%s, GRPCPort=%d, HTTPPort=%d, Peers=%v",
		config.NodeID, config.GRPCPort, config.HTTPPort, config.Peers)

	// 创建指标导出器
	metricsExporter := metrics.NewExporter(config.NodeID)

	// 创建网络模拟器
	netSim := simulator.NewNetworkSim(config.NodeID, config.LinkCapacity, config.Peers)

	// 解析服务器配置
	serverList := controller.ParseServerList(os.Getenv("SERVER_LIST"))
	if len(serverList) == 0 {
		serverCount := controller.ParseServerCount(os.Getenv("SERVER_COUNT"))
		serverList = controller.GenerateServerList(serverCount)
	}
	serverCapacity := getEnvFloat("SERVER_CAPACITY", 100.0)

	// 创建SDN控制器
	ctrl, err := controller.NewSDNController(
		config.NodeID,
		config.GRPCPort,
		config.Peers,
		config.TargetPhi,
		serverCapacity, // 替代 linkCapacity
		serverList,     // 新增参数
		netSim,
		metricsExporter,
	)
	if err != nil {
		log.Fatalf("[Main] Failed to create controller: %v", err)
	}

	// 创建HTTP服务器
	httpServer := api.NewServer(config.HTTPPort, ctrl, metricsExporter)

	// 启动组件
	if err := ctrl.Start(); err != nil {
		log.Fatalf("[Main] Failed to start controller: %v", err)
	}

	if err := netSim.Start(); err != nil {
		log.Fatalf("[Main] Failed to start network simulator: %v", err)
	}

	go httpServer.Start()

	log.Printf("[Main] mock-SDN Controller %s started successfully", config.NodeID)
	log.Printf("[Main] HTTP API: http://0.0.0.0:%d", config.HTTPPort)
	log.Printf("[Main] gRPC: 0.0.0.0:%d", config.GRPCPort)

	// 等待终止信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Printf("[Main] Shutting down...")

	// 优雅关闭
	httpServer.Stop()
	netSim.Stop()
	ctrl.Stop()

	log.Printf("[Main] mock-SDN Controller stopped")
}

// loadConfig 从环境变量加载配置
func loadConfig() *Config {
	config := &Config{
		NodeID:       getEnv("NODE_ID", "sdn-1"),
		GRPCPort:     getEnvInt("GRPC_PORT", 50051),
		HTTPPort:     getEnvInt("HTTP_PORT", 8080),
		TargetPhi:    getEnvFloat("TARGET_PHI", 1.05),
		LinkCapacity: getEnvFloat("LINK_CAPACITY", 1000.0),
	}

	// 解析对等节点列表
	peersStr := getEnv("PEERS", "")
	if peersStr != "" {
		config.Peers = strings.Split(peersStr, ",")
	}

	return config
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
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

func init() {
	// 设置日志格式
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}

// 确保导入time包被使用
var _ = time.Second
