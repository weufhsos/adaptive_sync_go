package transport

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/your-org/ac/proto"
)

// GRPCServer gRPC服务端实现
type GRPCServer struct {
	// gRPC服务器
	server *grpc.Server

	// 服务端口
	port int

	// 处理器回调
	updateHandler   func(*proto.UpdateMessage) error
	clChangeHandler func(*proto.CLConfig) error

	// 同步原语
	mutex sync.RWMutex

	// 状态
	isRunning bool
	stopChan  chan struct{}
}

// NewGRPCServer 创建新的gRPC服务端
func NewGRPCServer(port int) *GRPCServer {
	return &GRPCServer{
		port:     port,
		stopChan: make(chan struct{}),
	}
}

// Start 启动gRPC服务端
func (s *GRPCServer) Start() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.isRunning {
		return fmt.Errorf("gRPC server is already running")
	}

	// 创建gRPC服务器
	s.server = grpc.NewServer()

	// 注册AC服务
	proto.RegisterACServiceServer(s.server, &acServiceServer{parent: s})

	// 注册反射服务（便于调试和工具使用）
	reflection.Register(s.server)

	// 监听端口
	addr := fmt.Sprintf(":%d", s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.port, err)
	}

	log.Printf("[gRPC] Server listening on port %d", s.port)

	// 启动服务（非阻塞）
	go func() {
		if err := s.server.Serve(listener); err != nil {
			log.Printf("[gRPC] Server stopped with error: %v", err)
		}
	}()

	s.isRunning = true
	return nil
}

// Stop 停止gRPC服务端
func (s *GRPCServer) Stop() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if !s.isRunning {
		return
	}

	log.Printf("[gRPC] Stopping server")
	s.server.GracefulStop()
	s.isRunning = false
	log.Printf("[gRPC] Server stopped")
}

// RegisterUpdateHandler 注册更新处理回调
func (s *GRPCServer) RegisterUpdateHandler(handler func(*proto.UpdateMessage) error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.updateHandler = handler
}

// RegisterCLChangeHandler 注册一致性级别变更处理回调
func (s *GRPCServer) RegisterCLChangeHandler(handler func(*proto.CLConfig) error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.clChangeHandler = handler
}

// IsRunning 检查服务是否正在运行
func (s *GRPCServer) IsRunning() bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.isRunning
}

// ========== gRPC服务实现 ==========

// acServiceServer 实现ACService gRPC服务
type acServiceServer struct {
	proto.UnimplementedACServiceServer
	parent *GRPCServer
}

// PropagateUpdate 实现流式更新传播
func (s *acServiceServer) PropagateUpdate(stream proto.ACService_PropagateUpdateServer) error {
	log.Printf("[gRPC] Received PropagateUpdate stream")

	for {
		// 接收更新消息
		update, err := stream.Recv()
		if err != nil {
			log.Printf("[gRPC] Stream recv error: %v", err)
			return err
		}

		log.Printf("[gRPC] Received update: key=%s, value=%f, from=%s",
			update.Key, update.Value, update.OriginNodeId)

		// 调用处理回调
		s.parent.mutex.RLock()
		handler := s.parent.updateHandler
		s.parent.mutex.RUnlock()

		var success bool
		if handler != nil {
			if err := handler(update); err != nil {
				log.Printf("[gRPC] Update handler error: %v", err)
				success = false
			} else {
				success = true
			}
		} else {
			log.Printf("[gRPC] Warning: No update handler registered")
			success = false
		}

		// 发送确认响应
		ack := &proto.UpdateAck{
			Key:      update.Key,
			UpdateId: update.UpdateId,
			Success:  success,
		}

		if err := stream.Send(ack); err != nil {
			log.Printf("[gRPC] Stream send error: %v", err)
			return err
		}
	}
}

// ChangeConsistencyLevel 实现一致性级别变更
func (s *acServiceServer) ChangeConsistencyLevel(ctx context.Context, config *proto.CLConfig) (*proto.Ack, error) {
	log.Printf("[gRPC] Received CL change request: key=%s, QS=%d, TO=%dms",
		config.Key, config.MaxQueueSize, config.TimeoutMs)

	// 调用处理回调
	s.parent.mutex.RLock()
	handler := s.parent.clChangeHandler
	s.parent.mutex.RUnlock()

	var success bool
	var message string

	if handler != nil {
		if err := handler(config); err != nil {
			log.Printf("[gRPC] CL change handler error: %v", err)
			success = false
			message = fmt.Sprintf("Error: %v", err)
		} else {
			success = true
			message = "Consistency level changed successfully"
		}
	} else {
		log.Printf("[gRPC] Warning: No CL change handler registered")
		success = false
		message = "No handler registered"
	}

	return &proto.Ack{
		Success: success,
		Message: message,
	}, nil
}

// ========== gRPC客户端工具函数 ==========

// GRPCClient gRPC客户端封装
type GRPCClient struct {
	conn   *grpc.ClientConn
	client proto.ACServiceClient
}

// NewGRPCClient 创建新的gRPC客户端
func NewGRPCClient(address string) (*GRPCClient, error) {
	// 建立连接
	conn, err := grpc.Dial(address, grpc.WithInsecure()) // 生产环境应使用TLS
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", address, err)
	}

	client := proto.NewACServiceClient(conn)

	return &GRPCClient{
		conn:   conn,
		client: client,
	}, nil
}

// Close 关闭客户端连接
func (c *GRPCClient) Close() error {
	return c.conn.Close()
}

// PropagateUpdate 发送更新消息
func (c *GRPCClient) PropagateUpdate(ctx context.Context, update *proto.UpdateMessage) (*proto.UpdateAck, error) {
	// 创建流
	stream, err := c.client.PropagateUpdate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}

	// 发送更新
	if err := stream.Send(update); err != nil {
		return nil, fmt.Errorf("failed to send update: %w", err)
	}

	// 接收确认
	ack, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("failed to receive ack: %w", err)
	}

	// 关闭流
	stream.CloseSend()

	return ack, nil
}

// ChangeConsistencyLevel 发送一致性级别变更请求
func (c *GRPCClient) ChangeConsistencyLevel(ctx context.Context, config *proto.CLConfig) (*proto.Ack, error) {
	return c.client.ChangeConsistencyLevel(ctx, config)
}

// ========== 连接管理器 ==========

// ConnectionManager 管理到对等节点的gRPC连接
type ConnectionManager struct {
	clients map[string]*GRPCClient
	mutex   sync.RWMutex
}

// NewConnectionManager 创建连接管理器
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		clients: make(map[string]*GRPCClient),
	}
}

// AddPeer 添加对等节点
func (cm *ConnectionManager) AddPeer(address string) error {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	// 如果已存在连接，先关闭
	if existing, exists := cm.clients[address]; exists {
		existing.Close()
	}

	// 创建新连接
	client, err := NewGRPCClient(address)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", address, err)
	}

	cm.clients[address] = client
	log.Printf("[Connection] Connected to peer: %s", address)
	return nil
}

// RemovePeer 移除对等节点
func (cm *ConnectionManager) RemovePeer(address string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	if client, exists := cm.clients[address]; exists {
		client.Close()
		delete(cm.clients, address)
		log.Printf("[Connection] Disconnected from peer: %s", address)
	}
}

// GetClient 获取指定地址的客户端
func (cm *ConnectionManager) GetClient(address string) (*GRPCClient, bool) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	client, exists := cm.clients[address]
	return client, exists
}

// BroadcastUpdate 向所有对等节点广播更新
func (cm *ConnectionManager) BroadcastUpdate(ctx context.Context, update *proto.UpdateMessage) map[string]*proto.UpdateAck {
	cm.mutex.RLock()
	addresses := make([]string, 0, len(cm.clients))
	for addr := range cm.clients {
		addresses = append(addresses, addr)
	}
	cm.mutex.RUnlock()

	results := make(map[string]*proto.UpdateAck)

	// 并发发送
	var wg sync.WaitGroup
	resultMutex := sync.Mutex{}

	for _, addr := range addresses {
		wg.Add(1)
		go func(address string) {
			defer wg.Done()

			client, exists := cm.GetClient(address)
			if !exists {
				return
			}

			ack, err := client.PropagateUpdate(ctx, update)
			if err != nil {
				log.Printf("[Connection] Failed to send update to %s: %v", address, err)
				ack = &proto.UpdateAck{
					Key:      update.Key,
					UpdateId: update.UpdateId,
					Success:  false,
				}
			}

			resultMutex.Lock()
			results[address] = ack
			resultMutex.Unlock()
		}(addr)
	}

	wg.Wait()
	return results
}

// CloseAll 关闭所有连接
func (cm *ConnectionManager) CloseAll() {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	for addr, client := range cm.clients {
		client.Close()
		delete(cm.clients, addr)
	}
	log.Printf("[Connection] All connections closed")
}
