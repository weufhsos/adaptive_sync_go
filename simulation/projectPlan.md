# 项目名称：Go-SDN 自适应一致性 (AC) 协调层构建指南

## 1. 项目概述与架构定位

### 1.1 核心目标
在现有的分布式 SDN 控制器集群中，引入一个**内存驻留的自适应一致性层（AC Layer）**。该层利用 CRDT 算法和动态调节机制，解决多实例间共享状态（如全网链路剩余带宽、流表计数）的同步延迟与系统吞吐量之间的矛盾。

### 1.2 现有系统架构
当前分布式 SDN 控制器的运行架构如下：

*   **部署形态**：Go 语言实现，以**容器**形式部署（支持 Docker/Kubernetes）
*   **通信方式**：控制器实例间通过 **gRPC** 进行通信
*   **数据存储分工**：
    | 数据类型 | 存储位置 | 说明 |
    |---------|---------|------|
    | 北向接口配置 | MySQL | 用户下发的配置信息，存储后下发至转发平面 |
    | 拓扑消息 | MySQL | 网络拓扑结构信息 |
    | 链路上下线 | MySQL | 链路状态变更事件 |
    | 链路实时信息 | InfluxDB | 转发平面上报的时序数据（带宽、流量等） |

*   **数据流向**：
    ```
    [北向接口] → MySQL → [转发平面]
    [转发平面] → InfluxDB (链路时序数据)
    [转发平面] → MySQL (拓扑/链路状态变更)
    ```

### 1.3 AC 增强架构
*   **原有流程**：业务 → 读写 MySQL/InfluxDB（强依赖数据库 IO，高并发下有瓶颈或延迟）。
*   **AC 增强流程**：
    *   **热数据（Hot State）**：如链路实时负载、资源配额，托管在 **AC 内存存储** 中，通过 gRPC 在控制器副本间同步。
    *   **冷数据/持久化**：定期异步落盘至 MySQL/InfluxDB。
    *   **决策逻辑**：基于 AC 层的内存视图进行计算（极快），但该视图的一致性级别（CL）会根据决策质量自动调整。

### 1.4 技术选型
*   **语言**：Golang (1.21+，支持泛型)
*   **部署**：Docker 容器化，支持 Kubernetes 编排
*   **通信**：gRPC + Protobuf (流式传输)
*   **并发控制**：`sync.RWMutex`, `Channels`, `Context`
*   **持久化存储**：
    *   MySQL：配置数据、拓扑数据、状态变更
    *   InfluxDB：链路时序数据
*   **数学计算**：`math` (标准库), `gonum` (可选，用于复杂统计)

--

## 2. 项目目录结构

AC 层设计为**嵌入式 Go 库**，可直接通过 `go get` 引入 SDN 控制器项目。

```
adaptive_sync_go/
├── ac.go                    # 库主入口，对外暴露 Manager
├── options.go               # 配置选项 (Functional Options Pattern)
├── proto/
│   └── ac.proto             # gRPC 服务定义（跨节点通信）
├── store/
│   └── pn_counter.go        # CRDT PN-Counter 实现
├── dispatcher/
│   └── dispatcher.go        # 分发控制器
├── pi/
│   └── inspector.go         # 性能检查模块
├── oca/
│   └── controller.go        # 在线自适应模块
├── transport/
│   └── grpc.go              # gRPC 通信层（跨节点同步）
├── go.mod                   # module github.com/your-org/ac
└── go.sum
```

**设计理念**：
- 所有核心逻辑编译为单一 Go package
- SDN 控制器通过 `import` 引入，无需额外部署
- 跨节点同步仍通过 gRPC 实现，但作为库内部管理

---

## 3. 详细实现阶段 (Phase-by-Phase Implementation)

### 第一阶段：基础数据结构与通信协议 (Foundation)

**目标**：定义"状态"如何在网络中传输，以及如何在内存中存储。

#### 3.1.1 Protobuf 定义 (`proto/ac.proto`)
你需要定义用于同步 CRDT 状态的 gRPC 消息。

```protobuf
syntax = "proto3";
package ac;

service ACService {
  // 核心同步接口：接收远程更新
  rpc PropagateUpdate (stream UpdateMessage) returns (stream UpdateAck);
  // 接收配置变更（OCA模块下发）
  rpc ChangeConsistencyLevel (CLConfig) returns (Ack);
}

message UpdateMessage {
  string key = 1;           // 状态ID，例如 "link_1001_bw"
  string origin_node_id = 2; // 发起节点ID
  int64 timestamp = 3;       // 纳秒级时间戳
  Operation op = 4;          // 操作类型
  double value = 5;          // 变更值
}

enum Operation {
  INCREMENT = 0;
  DECREMENT = 1;
}

message CLConfig {
  string key = 1;
  int32 max_queue_size = 2; // CL_QS
  int32 timeout_ms = 3;     // CL_TO
}

message Ack {
  bool success = 1;
}

message UpdateAck {
    string key = 1;
    string update_id = 2; // 用于确认具体哪次更新
}
```

#### 3.1.2 本地 CRDT 存储 (`store/pn_counter.go`)
实现论文中的 **PN-Counter**。使用 Go 的 Struct 和 Mutex。

```go
type PNCounter struct {
    sync.RWMutex
    Key       string
    Incr      map[string]float64 // Map[NodeID]Value
    Decr      map[string]float64
    LocalIncr float64            // 本地未同步的增量缓冲
    LocalDecr float64
    // 历史日志，用于 PI 模块回溯计算
    History   []UpdateLog 
}

// Merge 逻辑：合并来自远程的 map，取 Max 值
func (pn *PNCounter) Merge(remoteIncr, remoteDecr map[string]float64) {
    pn.Lock()
    defer pn.Unlock()
    // CRDT Merge 核心：取各节点各维度的最大值
    // ...实现代码...
}

// Query 逻辑
func (pn *PNCounter) Value() float64 {
    pn.RLock()
    defer pn.RUnlock()
    // sum(Incr) - sum(Decr)
    // ...实现代码...
}
```

---

### 第二阶段：一致性控制与分发 (AC Core Logic)

**目标**：实现准入控制和更新分发，参考5. 算法与公式参考的 `Algorithm 5`。

#### 3.2.1 状态管理器 (`ac.go`)
每个受管状态（如每条链路）都有自己的配置。

```go
type StateContext struct {
    Store        *store.PNCounter
    Queue        chan *proto.UpdateMessage // 分发队列
    CurrentCL    ConsistencyLevel          // 当前一致性配置
    StopChan     chan struct{}
}

type ConsistencyLevel struct {
    QueueSize int           // CL_QS
    Timeout   time.Duration // CL_TO
}
```

#### 3.2.2 准入控制与分发循环 (`dispatcher/dispatcher.go`)
这是**最关键**的逻辑，决定了系统的吞吐量和延迟。

*   **Fast-Mode**:
    *   启动一个 Goroutine 监听 `Queue`。
    *   一旦 `len(Queue) > 0` 且未达到 `CL_QS` 上限，立即调用 gRPC `PropagateUpdate`。
*   **Batched-Mode**:
    *   使用 `time.NewTicker(CurrentCL.Timeout)`。
    *   `select` 监听 `Queue` 和 `Ticker.C`。
    *   当队列满 OR 定时器到期时，打包发送一批 updates。

**实现提示**：
> "在写入 `Queue` 之前，必须检查 `len(Queue)`。如果超过 `CurrentCL.QueueSize`，则不仅要拒绝写入，还应触发强制同步或阻塞当前请求（根据业务需求选择），以保证论文提到的 'Bounded Staleness'（有界过时性）。"

---

### 第三阶段：性能检查模块 (PI Module)

**目标**：事后分析（Hindsight），计算决策偏差率 $\phi$。

#### 3.3.1 历史重构器 (`pi/inspector.go`)
利用 Go 的高性能计算能力。
*   **触发时机**：当收到远程 gRPC 的 `UpdateMessage` 时，记录其 `timestamp`（记为 $t_{remote}$）。
*   **数据源**：
    1.  `PNCounter.History`: 包含本地所有操作的时间戳和值。
    2.  **InfluxDB** (适配点): 如果你的链路真实负载在 InfluxDB 中，这里可以查询 $t_{remote}$ 之前的真实流量数据作为 Ground Truth。
*   **计算逻辑 (Algorithm 2)**：
    1.  **Replay (重放)**：在内存中构建两个临时时间序列。
        *   序列 A (Observed): 此时此刻本地实际看到的历史（缺少该远程更新）。
        *   序列 B (Optimal): 假设该远程更新在 $t_{remote}$ 准确到达时的历史。
    2.  **Apply Logic**: 模拟你的 SDN 业务逻辑（比如：选路算法）。
        *   基于序列 A，你会选哪条路？算出该路当时的负载标准差 $\sigma_u$。
        *   基于序列 B，你本该选哪条路？算出该路当时的负载标准差 $\sigma_o$。
    3.  **计算比率**: $\phi = \frac{\sum \sigma_u}{\sum \sigma_o}$。

---

### 第四阶段：在线自适应模块 (OCA Module)

**目标**：动态调整 `CL_QS` 和 `CL_TO`。

#### 3.4.1 自适应控制器 (`oca/controller.go`)
实现 PID 或 阈值算法。

```go
type OCAController struct {
    HistoryPhi []float64 // 历史不一致性比率
    // PID 参数
    Kp, Ki, Kd float64
    TargetPhi  float64
}

// Run 周期性执行或事件触发
func (oca *OCAController) Adjust(newPhi float64) *ConsistencyLevel {
    // 1. 实现 Algorithm 4 (PID)
    // error = newPhi - oca.TargetPhi
    // output = P + I + D
    
    // 2. 映射 output 到具体的 QS 和 TO
    // 假设 output 范围 0-100
    // output 高 -> 偏差大 -> 需要更严格 -> 减小 QS, 减小 TO
    // output 低 -> 偏差小 -> 可以更宽松 -> 增加 QS, 增加 TO
    
    return &newConsistencyLevel
}
```

---

### 第五阶段：集成到 SDN 业务流 (Integration)

**目标**：将 AC 框架嵌入到 Go SDN 控制器流程中。

#### 3.5.1 拦截北向接口
当用户通过 API 下发配置或请求资源（如请求 100M 带宽）时：

```
[用户请求] → [北向API] → [AC层更新] → [MySQL持久化] → [下发转发平面]
```

1.  接收北向接口请求。
2.  **优先**调用 `ACManager.Update("link_1_bw", -100)` (扣减带宽)。
3.  AC 层返回成功后，**同步写入 MySQL**（保持现有流程兼容）。
4.  MySQL 写入成功后，下发至转发平面。

#### 3.5.2 拦截南向数据
转发平面上报的链路信息需要同步到 AC 层：

**链路时序数据（InfluxDB）**：
```
[转发平面] → [InfluxDB] → [AC Monitor] → [AC层更新]
```
1.  创建一个 Monitor Goroutine。
2.  订阅 InfluxDB 的变动或定时拉取链路实时数据。
3.  计算 Delta（变化量）。
4.  调用 `ACManager.Update("link_1_actual_load", delta)`。

**拓扑/链路状态变更（MySQL）**：
```
[转发平面] → [MySQL] → [AC Monitor] → [AC层更新]
```
1.  监听 MySQL binlog 或定时轮询拓扑表。
2.  检测链路上下线事件。
3.  调用 `ACManager.UpdateTopology(event)` 更新 AC 层视图。

#### 3.5.3 决策点替换
原有的业务代码：
```go
// Old: 直接查询数据库
func SelectPath() {
    // 查询 MySQL 获取拓扑
    topo := mysql.Query("SELECT * FROM topology")
    // 查询 InfluxDB 获取链路负载
    load := influxdb.Query("SELECT * FROM link_metrics")
    // ... calculate ...
}
```
替换为：
```go
// New: 基于 AC 层内存视图
func SelectPath() {
    // 极快，内存读取
    load := acManager.Get("link_1_actual_load")
    topo := acManager.GetTopology()
    // ... calculate ...
}
```

---

## 4. 库 API 设计与使用方式

### 4.1 对外暴露的 API

```go
package ac

// Manager 是 AC 模块的主入口
type Manager struct {
    // 内部字段...
}

// ==================== 创建与生命周期 ====================

// New 创建 AC Manager 实例
func New(opts ...Option) *Manager

// Start 启动 AC 模块（gRPC服务、同步协程、自适应调整）
func (m *Manager) Start() error

// Stop 优雅关闭 AC 模块
func (m *Manager) Stop()

// ==================== 状态操作（核心 API） ====================

// Update 增量更新状态值
// key: 状态标识，如 "link_1001_bw"
// delta: 变化量，正数为增加，负数为减少
func (m *Manager) Update(key string, delta float64) error

// Get 读取当前状态值（内存读取，纳秒级）
func (m *Manager) Get(key string) float64

// Snapshot 获取所有状态的一致性快照
func (m *Manager) Snapshot() map[string]float64

// ==================== 拓扑管理 ====================

// HandleTopologyEvent 处理拓扑变更事件（链路上下线）
func (m *Manager) HandleTopologyEvent(event TopologyEvent)

// GetTopology 获取当前拓扑视图
func (m *Manager) GetTopology() *Topology

// ==================== 配置选项 ====================

type Option func(*config)

func WithNodeID(id string) Option                          // 节点标识
func WithPeers(addrs []string) Option                      // 对等节点地址
func WithGRPCPort(port int) Option                         // gRPC 监听端口
func WithTargetPhi(phi float64) Option                     // 目标不一致性比率
func WithInitialCL(qs int, timeout time.Duration) Option   // 初始一致性级别
```

### 4.2 在 SDN 控制器中集成使用

#### Step 1：引入依赖

```bash
go get github.com/your-org/ac@latest
```

#### Step 2：初始化 AC 模块

```go
package main

import (
    "os"
    "strings"
    "github.com/your-org/ac"
)

var acManager *ac.Manager

func main() {
    // 初始化 AC 模块（嵌入式，与 SDN 控制器同进程）
    acManager = ac.New(
        ac.WithNodeID(os.Getenv("NODE_ID")),
        ac.WithPeers(strings.Split(os.Getenv("PEER_ADDRESSES"), ",")),
        ac.WithGRPCPort(50051),
        ac.WithTargetPhi(1.05),
    )
    
    // 启动 AC 后台协程
    if err := acManager.Start(); err != nil {
        log.Fatal(err)
    }
    defer acManager.Stop()
    
    // 启动 SDN 控制器主逻辑
    startSDNController()
}
```

#### Step 3：业务代码中使用

```go
// 北向接口：带宽分配
func AllocateBandwidth(linkID string, bandwidth float64) error {
    // 1. AC 层更新（纳秒级，进程内函数调用）
    if err := acManager.Update(linkID+"_bw", -bandwidth); err != nil {
        return err
    }
    
    // 2. MySQL 持久化（保持现有流程）
    if err := mysql.Exec("UPDATE links SET bw = bw - ?", bandwidth); err != nil {
        acManager.Update(linkID+"_bw", bandwidth) // 回滚
        return err
    }
    
    return nil
}

// 决策逻辑：选路
func SelectBestPath(src, dst string) (string, error) {
    // 极快，内存读取
    snapshot := acManager.Snapshot()
    topo := acManager.GetTopology()
    
    // 基于内存视图计算最优路径
    // ...
}

// 南向数据：链路负载更新
func onLinkMetricsReceived(linkID string, newLoad float64) {
    currentLoad := acManager.Get(linkID + "_load")
    delta := newLoad - currentLoad
    acManager.Update(linkID+"_load", delta)
}
```

### 4.3 库模式架构图

```
┌─────────────────────────────────────────────────────────────┐
│               SDN Controller Container (单进程)              │
│                                                             │
│  ┌─────────────┐                                            │
│  │  北向 API    │──────┐                                     │
│  └─────────────┘      │                                     │
│                       ▼                                     │
│  ┌─────────────┐   ┌─────────────────────────────────────┐ │
│  │  决策逻辑    │◀──│         AC Module (嵌入式库)          │ │
│  └─────────────┘   │  ┌─────────┐ ┌────────┐ ┌────────┐  │ │
│                    │  │PNCounter│ │Dispatch│ │  OCA   │  │ │
│  ┌─────────────┐   │  └─────────┘ └────────┘ └────────┘  │ │
│  │  南向接口    │──▶│         内部 gRPC Client/Server       │ │
│  └─────────────┘   └──────────────────┬──────────────────┘ │
│                                       │                     │
└───────────────────────────────────────┼─────────────────────┘
                                        │ gRPC (跨节点同步)
                                        ▼
                              其他 SDN 控制器节点
```

### 4.4 环境变量配置

```yaml
# 容器环境变量
NODE_ID: "controller-1"                    # 节点唯一标识
PEER_ADDRESSES: "ctrl-2:50051,ctrl-3:50051" # 对等节点地址列表
AC_GRPC_PORT: "50051"                       # AC gRPC 端口
AC_TARGET_PHI: "1.05"                       # 目标不一致性比率
AC_INITIAL_QUEUE_SIZE: "100"                # 初始 CL_QS
AC_INITIAL_TIMEOUT_MS: "50"                 # 初始 CL_TO
```

---

## 5. 算法与公式参考

### 5.1 性能检查中的计算公式 (Performance Inspection Math)

这部分内容主要位于论文的 **V. Realization of an AC Framework** 章节的 **B** 和 **C** 小节。

目标是计算“不一致性比率”（Inefficiency Metric, $\Phi_T$），即“实际发生的决策成本”与“如果拥有完美全局一致性时的理想决策成本”之比。

#### 1. 定义基础变量
假设在一个时间窗口 $T(t-n)$ 内，有 $|S|$ 个状态（服务器资源），在时间点 $i$ 的状态向量为 $S(i)$。
*   **$s_j(i)$**: 第 $j$ 个服务器在时间 $i$ 的资源利用率。
*   **$N_{res}(i)$**: 资源（服务器）的总数量。
*   **$\mu_{S(i)}$**: 当前所有服务器资源利用率的平均值（公式 1）。
   $$ \mu_{S(i)} = \frac{\sum_{j=1}^{N_{res}(i)} s_j(i)}{N_{res}(i)} $$

#### 2. 计算决策的“成本”（Cost）
论文使用**标准差（Standard Deviation）**作为衡量负载均衡器性能的成本指标。标准差越小，负载越均衡（决策越好）。

*   **理想/最优成本 ($\sigma^{R_i}_o$)**: 基于重构后的**强一致性历史**（即包含了被延迟的更新）计算得出的标准差。
   $$ \sigma^{R_i}_o = \sqrt{\frac{1}{N_{res}(i)} \sum_{j=1}^{N_{res}(i)} (s_j(i) \in S_{U_{cnst}} - \mu_{S^{R_i}_{cnst}})^2} $$

*   **实际/次优成本 ($\sigma^{R_i}_u$)**: 基于当时**本地实际视图**（可能缺失了远程更新）计算得出的标准差。
   $$ \sigma^{R_i}_u = \sqrt{\frac{1}{N_{res}(i)} \sum_{j=1}^{N_{res}(i)} (s_j(i) \in S_{U_{incnst}} - \mu_{S^{R_i}_{incnst}})^2} $$

#### 3. 计算不一致性比率 ($\Phi_T$)
这是最终输入给自适应模块的值。它是观察期内所有实际成本之和与所有理想成本之和的比率：

$$ \Phi_T = \frac{\sum_{i=0}^{||T||} \sigma^{R_i}_u}{\sum_{i=0}^{||T||} \sigma^{R_i}_o} $$

*   如果 $\Phi_T = 1$: 说明实际决策与理想决策一致（无偏差）。
*   如果 $\Phi_T > 1$: 说明因为状态同步延迟，导致了负载不均衡（实际成本高于理想成本）。

---

### 5.2 核心算法提取 (Algorithms 1-5)

论文中明确列出了5个伪代码算法，涵盖了从数据存储到自适应调整的全过程。

#### 5.2.1 分布式 CRDT PN-Counter 实现 (Algorithm 1)
*   **作用**: 实现支持并发更新的计数器（如带宽、流表项计数），保证最终一致性。
*   **核心逻辑**:
    *   使用两个集合：`Incr` (增加) 和 `Decr` (减少)。
    *   `Query()`: 返回 $\sum Incr - \sum Decr$。
    *   `Merge()`: 合并来自远程节点的集合。
    *   **关键点**: 在本地更新时，首先调用 `evalAddToDistributionQueue` 检查是否满足当前一致性级别（CL）的队列限制，如果满足则通过，否则拒绝（以此来限制最大过时程度）。

#### 5.2.2 不一致性计算逻辑 (Algorithm 2)
*   **作用**: 当收到一个“迟到”的远程更新（Remote Update）时，触发性能检查。
*   **核心逻辑**:
    *   **重构历史**: 将更新按照时间戳排序，生成两条时间线：
        1.  $S_{U_{incnst}}$ (Suboptimal/Real): 实际发生的、未包含该远程更新的历史。
        2.  $S_{U_{cnst}}$ (Optimal): 假设该更新被及时序列化处理后的理想历史。
    *   **模拟决策**: 调用 `AppLogic` 重新计算在理想情况下应用会做什么决策。
    *   **计算比率**: 调用上述的公式计算 $\phi$，并报告给 OCA 模块。

#### 5.2.3 基于阈值的一致性自适应 (Algorithm 3: Threshold-based CL Adaptation)
*   **作用**: 最简单的自适应策略。
*   **输入**: 最近 $W$ 次的不一致性报告 $\phi$。
*   **逻辑**:
    *   计算窗口 $W$ 内 $\phi$ 的平均值 $\mu_{SRlv}$。
    *   如果 $\mu_{SRlv} \ge T_U$ (上限阈值): **提高 CL** (raiseCL, 即收紧限制，减小队列/超时)。
    *   如果 $\mu_{SRlv} \le T_L$ (下限阈值): **降低 CL** (lowerCL, 即放宽限制)。
    *   否则: 保持不变。

#### 5.2.4 基于 PID 的一致性自适应 (Algorithm 4: PID-based CL Adaptation)
*   **作用**: 使用控制理论中的 PID 控制器进行更精细的调整。
*   **核心逻辑**:
    *   计算三个项：
        *   $P_{term}$ (比例): 当前误差与目标 $T_O$ 的差。
        *   $I_{term}$ (积分): 历史误差的累积。
        *   $D_{term}$ (微分): 误差的变化率。
    *   计算总输出 $T = P + I + D$。
    *   如果 $T > T_O$: 提高 CL；如果 $T < T_O$: 降低 CL。

#### 5.2.5 状态更新分发策略 (Algorithm 5: Fast and Batched Distribution)
*   **作用**: 控制何时将本地更新发送给其他副本。
*   **两个模式**:
    1.  **Fast-Mode (快速模式)**:
        *   只要 `DistributionQueue` 的大小小于当前 $CL_{QS}$，就立即发送。
    2.  **Batched-Mode (批处理模式)**:
        *   累积更新。
        *   触发发送的条件：要么队列达到 $CL_{QS}$，要么定时器 $CL_{TO}$ 到期。
        *   *优点*: 减少网络包数量；*缺点*: 增加延迟。

这些算法和公式共同构成了 AC 框架的闭环控制系统：**更新 -> 监测偏差 (Alg 1, 2, 公式) -> 计算调整策略 (Alg 3, 4) -> 调整分发参数 (Alg 5) -> 影响下一次更新**。

---

## 6. 系统架构总览

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              SDN Controller Cluster                          │
│  ┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐            │
│  │   Container 1   │   │   Container 2   │   │   Container 3   │            │
│  │  ┌───────────┐  │   │  ┌───────────┐  │   │  ┌───────────┐  │            │
│  │  │ AC Layer  │◄─┼───┼──┤ AC Layer  │◄─┼───┼──┤ AC Layer  │  │            │
│  │  │  (gRPC)   │──┼───┼──►  (gRPC)   │──┼───┼──►  (gRPC)   │  │            │
│  │  └─────┬─────┘  │   │  └─────┬─────┘  │   │  └─────┬─────┘  │            │
│  │        │        │   │        │        │   │        │        │            │
│  │  ┌─────▼─────┐  │   │  ┌─────▼─────┐  │   │  ┌─────▼─────┐  │            │
│  │  │SDN Logic  │  │   │  │SDN Logic  │  │   │  │SDN Logic  │  │            │
│  │  └───────────┘  │   │  └───────────┘  │   │  └───────────┘  │            │
│  └─────────────────┘   └─────────────────┘   └─────────────────┘            │
└─────────────────────────────────────────────────────────────────────────────┘
                    │                                      ▲
                    │ 配置下发                             │ 状态上报
                    ▼                                      │
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Data Plane (转发平面)                           │
└─────────────────────────────────────────────────────────────────────────────┘
                    │                                      │
                    ▼                                      ▼
        ┌───────────────────┐                  ┌───────────────────┐
        │      MySQL        │                  │     InfluxDB      │
        │ (配置/拓扑/状态)    │                  │   (链路时序数据)   │
        └───────────────────┘                  └───────────────────┘
```

---

## 7. 实施路线图

| 阶段 | 内容 | 预估周期 | 交付物 |
|------|------|---------|--------|
| Phase 1 | 基础数据结构与通信协议 | 1-2 周 | `proto/ac.proto`, `store/` |
| Phase 2 | 一致性控制与分发 | 2-3 周 | `dispatcher/`, `ac.go` |
| Phase 3 | 性能检查模块 | 1-2 周 | `pi/` |
| Phase 4 | 在线自适应模块 | 1-2 周 | `oca/` |
| Phase 5 | 库 API 封装与测试 | 1-2 周 | `ac.go`, `options.go`, 单元测试 |
| Phase 6 | SDN 控制器集成测试 | 2-3 周 | 集成测试、性能基准测试 |