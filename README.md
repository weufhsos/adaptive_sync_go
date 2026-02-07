# Go-SDN 自适应一致性 (AC) 协调层

这是一个用于分布式SDN控制器的自适应一致性协调层库，实现了基于CRDT和动态调节机制的一致性控制框架。

## 📋 项目特点

- **嵌入式库设计**：可直接通过 `go get` 引入到SDN控制器项目中
- **CRDT支持**：基于PN-Counter实现最终一致性
- **自适应调节**：动态调整一致性级别以平衡吞吐量和延迟
- **gRPC通信**：高效的跨节点状态同步
- **性能监控**：内置不一致性检测和性能分析

## 🚀 快速开始

### 1. 安装

```bash
go get github.com/your-org/ac@latest
```

### 2. 基本使用

```go
package main

import (
    "log"
    "github.com/your-org/ac"
)

func main() {
    // 创建AC管理器
    acManager := ac.New(
        ac.WithNodeID("controller-1"),
        ac.WithPeers([]string{"controller-2:50051", "controller-3:50051"}),
        ac.WithGRPCPort(50051),
        ac.WithTargetPhi(1.05),
    )
    
    // 启动AC模块
    if err := acManager.Start(); err != nil {
        log.Fatal(err)
    }
    defer acManager.Stop()
    
    // 使用AC层进行状态管理
    acManager.Update("link_1_bw", -100.0)  // 扣减带宽
    currentValue := acManager.Get("link_1_bw")  // 快速读取
    
    log.Printf("Current bandwidth: %.2f", currentValue)
}
```

### 3. 集成到SDN控制器

```go
// 北向接口：带宽分配
func AllocateBandwidth(linkID string, bandwidth float64) error {
    // 1. AC层更新（纳秒级）
    if err := acManager.Update(linkID+"_bw", -bandwidth); err != nil {
        return err
    }
    
    // 2. MySQL持久化（保持现有流程）
    if err := mysql.Exec("UPDATE links SET bw = bw - ?", bandwidth); err != nil {
        acManager.Update(linkID+"_bw", bandwidth) // 回滚
        return err
    }
    
    return nil
}

// 决策逻辑：选路
func SelectBestPath(src, dst string) (string, error) {
    // 极快，内存读取
    load := acManager.Get("link_1_actual_load")
    topo := acManager.GetTopology()
    // ... 基于AC层视图计算最优路径
}
```

## 🏗️ 核心组件

### 1. PN-Counter (store/pn_counter.go)
实现CRDT PN-Counter，支持分布式环境下的最终一致性计数。

### 2. 分发控制器 (dispatcher/dispatcher.go)
实现论文中的Algorithm 5，控制状态更新的分发策略。

### 3. 性能检查模块 (pi/inspector.go)
实现论文中的Algorithm 2，计算不一致性比率φ。

### 4. 自适应控制器 (oca/controller.go)
实现论文中的Algorithm 4，基于PID控制动态调整一致性级别。

### 5. gRPC传输层 (transport/grpc.go)
负责跨节点通信和状态同步。

## ⚙️ 配置选项

### 基础配置
```go
ac.New(
    ac.WithNodeID("node-1"),                    // 节点标识
    ac.WithPeers([]string{"node-2:50051"}),     // 对等节点
    ac.WithGRPCPort(50051),                     // gRPC端口
    ac.WithTargetPhi(1.05),                     // 目标不一致性比率
    ac.WithInitialCL(100, 50*time.Millisecond), // 初始一致性级别
)
```

### 预设配置模板
```go
// 开发环境
ac.New(ac.DevelopmentConfig())

// 生产环境  
ac.New(ac.ProductionConfig())

// 高吞吐量优化
ac.New(ac.HighThroughputConfig())

// 低延迟优化
ac.New(ac.LowLatencyConfig())
```

## 🔧 API参考

### 核心方法

- `Update(key string, delta float64) error` - 增量更新状态
- `Get(key string) float64` - 读取当前状态值
- `Snapshot() map[string]float64` - 获取一致性快照
- `HandleTopologyEvent(event interface{}) error` - 处理拓扑变更
- `GetTopology() interface{}` - 获取拓扑视图

### 生命周期管理

- `Start() error` - 启动AC模块
- `Stop()` - 停止AC模块

## 📊 性能特性

- **内存读取**：纳秒级状态查询
- **并发安全**：支持高并发读写操作
- **自适应调节**：根据决策质量动态调整一致性级别
- **有界过时性**：保证状态不会无限滞后

## 🧪 运行示例

```bash
# 启动第一个节点
cd examples/simple
go run main.go node-1 localhost:50052 localhost:50053

# 启动第二个节点（另开终端）
go run main.go node-2 localhost:50051 localhost:50053

# 启动第三个节点（另开终端）
go run main.go node-3 localhost:50051 localhost:50052
```

## 📚 项目结构

```
adaptive_sync_go/
├── ac.go                    # 主入口和Manager
├── options.go               # 配置选项
├── proto/                   # Protobuf定义
│   ├── ac.proto
│   ├── ac.pb.go
│   └── ac_grpc.pb.go
├── store/                   # CRDT存储
│   └── pn_counter.go
├── dispatcher/              # 分发控制器
│   └── dispatcher.go
├── pi/                      # 性能检查
│   └── inspector.go
├── oca/                     # 自适应控制
│   └── controller.go
├── transport/               # 传输层
│   └── grpc.go
├── examples/                # 使用示例
│   └── simple/
│       └── main.go
└── go.mod
```

## 📖 算法实现

本项目严格按照论文要求实现了以下核心算法：

1. **Algorithm 1**: 分布式CRDT PN-Counter
2. **Algorithm 2**: 不一致性计算逻辑  
3. **Algorithm 3**: 基于阈值的一致性自适应
4. **Algorithm 4**: 基于PID的一致性自适应
5. **Algorithm 5**: 状态更新分发策略

## 🛠️ 开发指南

### 运行测试
```bash
go test -v ./...
```

### 基准测试
```bash
go test -bench=. -benchmem
```

### 生成Protobuf代码
```bash
protoc --go_out=. --go-grpc_out=. proto/ac.proto
```

## 📄 许可证

MIT License

## 🤝 贡献

欢迎提交Issue和Pull Request！