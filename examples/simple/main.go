package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/your-org/ac"
)

func main() {
	// 解析命令行参数
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <node-id> [peer1:port] [peer2:port] ...")
		fmt.Println("Example: go run main.go node-1 localhost:50052 localhost:50053")
		os.Exit(1)
	}

	nodeID := os.Args[1]
	var peers []string
	if len(os.Args) > 2 {
		peers = os.Args[2:]
	}

	// 创建AC管理器
	acManager := ac.New(
		ac.WithNodeID(nodeID),
		ac.WithPeers(peers),
		ac.WithGRPCPort(50051),
		ac.WithTargetPhi(1.05),
		ac.WithInitialCL(100, 50*time.Millisecond),
		ac.DevelopmentConfig(), // 使用开发环境配置
	)

	// 启动AC模块
	log.Printf("Starting AC Manager for node: %s", nodeID)
	if err := acManager.Start(); err != nil {
		log.Fatalf("Failed to start AC Manager: %v", err)
	}

	// 模拟一些SDN业务操作
	go simulateSDNOperations(acManager, nodeID)

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
	acManager.Stop()
	log.Println("Shutdown complete")
}

// simulateSDNOperations 模拟SDN控制器的操作
func simulateSDNOperations(manager *ac.Manager, nodeID string) {
	log.Printf("[%s] Starting SDN operations simulation", nodeID)

	// 模拟链路带宽更新
	go simulateLinkBandwidthUpdates(manager, nodeID)

	// 模拟流表项计数更新
	go simulateFlowTableUpdates(manager, nodeID)

	// 定期打印状态快照
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		snapshot := manager.Snapshot()
		stats := manager.GetStats()

		log.Printf("[%s] Status Snapshot:", nodeID)
		log.Printf("  Active States: %d", stats.ActiveStates)
		log.Printf("  Total Updates: %d", stats.TotalUpdates)
		log.Printf("  Successful Ops: %d", stats.SuccessfulOps)
		log.Printf("  Sample Values: %v", getSampleValues(snapshot))
	}
}

// simulateLinkBandwidthUpdates 模拟链路带宽更新
func simulateLinkBandwidthUpdates(manager *ac.Manager, nodeID string) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	linkIDs := []string{"link_1", "link_2", "link_3", "link_4"}

	for range ticker.C {
		// 随机选择一个链路
		linkID := linkIDs[rand.Intn(len(linkIDs))]

		// 随机生成带宽变化（-10到+10 Mbps）
		delta := float64(rand.Intn(21) - 10)

		// 更新AC状态
		key := linkID + "_bw"
		if err := manager.Update(key, delta); err != nil {
			log.Printf("[%s] Failed to update %s: %v", nodeID, key, err)
		} else {
			currentValue := manager.Get(key)
			log.Printf("[%s] Updated %s: %+f Mbps (current: %.2f)", nodeID, key, delta, currentValue)
		}
	}
}

// simulateFlowTableUpdates 模拟流表项计数更新
func simulateFlowTableUpdates(manager *ac.Manager, nodeID string) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	tableIDs := []string{"table_1", "table_2", "table_3"}

	for range ticker.C {
		// 随机选择一个流表
		tableID := tableIDs[rand.Intn(len(tableIDs))]

		// 随机生成流表项变化（-5到+5个）
		delta := float64(rand.Intn(11) - 5)

		// 更新AC状态
		key := tableID + "_flows"
		if err := manager.Update(key, delta); err != nil {
			log.Printf("[%s] Failed to update %s: %v", nodeID, key, err)
		} else {
			currentValue := manager.Get(key)
			log.Printf("[%s] Updated %s: %+f flows (current: %.0f)", nodeID, key, delta, currentValue)
		}
	}
}

// getSampleValues 获取快照中的样本值用于日志输出
func getSampleValues(snapshot map[string]float64) map[string]float64 {
	result := make(map[string]float64)
	count := 0

	// 只显示前5个状态值
	for key, value := range snapshot {
		if count >= 5 {
			break
		}
		result[key] = value
		count++
	}

	if len(snapshot) > 5 {
		result["..."] = float64(len(snapshot) - 5)
	}

	return result
}
