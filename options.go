package ac

import (
	"time"

	"github.com/your-org/ac/dispatcher"
)

// options.go 实现Functional Options Pattern
// 提供灵活的配置选项机制

// ========== 预设配置模板 ==========

// ProductionConfig 生产环境配置模板
func ProductionConfig() Option {
	return func(c *Config) {
		// 生产环境的默认配置
		c.InitialCL = dispatcher.ConsistencyLevel{
			QueueSize: 50,                    // 较严格的队列限制
			Timeout:   20 * time.Millisecond, // 较短的超时时间
		}
		c.TargetPhi = 1.02 // 更严格的目标不一致性比率

		if c.Advanced == nil {
			c.Advanced = &AdvancedConfig{}
		}
		c.Advanced.LogLevel = "WARN"
		c.Advanced.EnableMonitoring = true
		c.Advanced.HistoryWindowSize = 20
		c.Advanced.MaxHistorySize = 5000
	}
}

// DevelopmentConfig 开发环境配置模板
func DevelopmentConfig() Option {
	return func(c *Config) {
		// 开发环境的宽松配置
		c.InitialCL = dispatcher.ConsistencyLevel{
			QueueSize: 200,                    // 宽松的队列限制
			Timeout:   100 * time.Millisecond, // 较长的超时时间
		}
		c.TargetPhi = 1.10 // 宽松的目标不一致性比率

		if c.Advanced == nil {
			c.Advanced = &AdvancedConfig{}
		}
		c.Advanced.LogLevel = "DEBUG"
		c.Advanced.EnableMonitoring = true
		c.Advanced.HistoryWindowSize = 5
		c.Advanced.MaxHistorySize = 1000
		c.Advanced.StatsInterval = 10 * time.Second // 更频繁的统计打印
	}
}

// HighThroughputConfig 高吞吐量配置模板
func HighThroughputConfig() Option {
	return func(c *Config) {
		// 优化吞吐量的配置
		c.InitialCL = dispatcher.ConsistencyLevel{
			QueueSize: 500,                    // 大队列以提高吞吐量
			Timeout:   200 * time.Millisecond, // 长超时以减少网络交互
		}
		c.TargetPhi = 1.20 // 宽松的一致性要求

		if c.Advanced == nil {
			c.Advanced = &AdvancedConfig{}
		}
		c.Advanced.PIDParams = &PIDParameters{
			Kp: 0.5, // 较低的比例系数
			Ki: 0.05,
			Kd: 0.02,
		}
	}
}

// LowLatencyConfig 低延迟配置模板
func LowLatencyConfig() Option {
	return func(c *Config) {
		// 优化延迟的配置
		c.InitialCL = dispatcher.ConsistencyLevel{
			QueueSize: 20,                   // 小队列以减少延迟
			Timeout:   5 * time.Millisecond, // 短超时以快速响应
		}
		c.TargetPhi = 1.01 // 严格的一致性要求

		if c.Advanced == nil {
			c.Advanced = &AdvancedConfig{}
		}
		c.Advanced.PIDParams = &PIDParameters{
			Kp: 2.0, // 较高的比例系数
			Ki: 0.2,
			Kd: 0.1,
		}
	}
}

// ========== 环境相关配置 ==========

// FromEnvironment 从环境变量加载配置
func FromEnvironment() Option {
	return func(c *Config) {
		// 这里可以实现从环境变量读取配置的逻辑
		// 例如：NODE_ID, PEER_ADDRESSES, GRPC_PORT 等
	}
}

// FromConfigFile 从配置文件加载配置
func FromConfigFile(filepath string) Option {
	return func(c *Config) {
		// 这里可以实现从JSON/YAML配置文件读取配置的逻辑
	}
}
