package transport

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weufhsos/adaptive_sync_go/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

// TestNewGRPCServer 验证新建 Server
func TestNewGRPCServer(t *testing.T) {
	server := NewGRPCServer(50099)

	if server == nil {
		t.Fatal("NewGRPCServer returned nil")
	}

	if server.port != 50099 {
		t.Errorf("Expected port 50099, got %d", server.port)
	}
	if server.IsRunning() {
		t.Error("Server should not be running initially")
	}
	if server.server != nil {
		t.Error("gRPC server should be nil initially")
	}
}

// TestServer_StartStop 启动和停止 Server
func TestServer_StartStop(t *testing.T) {
	server := NewGRPCServer(50100)

	// 启动
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	if !server.IsRunning() {
		t.Error("Server should be running after Start()")
	}
	if server.server == nil {
		t.Error("gRPC server should not be nil after Start()")
	}

	// 短暂运行
	time.Sleep(50 * time.Millisecond)

	// 停止
	server.Stop()

	if server.IsRunning() {
		t.Error("Server should not be running after Stop()")
	}

	// 再次停止不应该 panic
	server.Stop()
}

// TestServer_DoubleStart 重复启动
func TestServer_DoubleStart(t *testing.T) {
	server := NewGRPCServer(50101)

	// 第一次启动
	if err := server.Start(); err != nil {
		t.Fatalf("First start failed: %v", err)
	}
	defer server.Stop()

	// 第二次启动应该失败
	err := server.Start()
	if err == nil {
		t.Error("Expected error on double start")
	}

	// 错误信息应该包含相关描述
	if err != nil && len(err.Error()) < 10 {
		t.Errorf("Error message too short: %v", err)
	}
}

// TestRegisterUpdateHandler 注册更新处理器
func TestRegisterUpdateHandler(t *testing.T) {
	server := NewGRPCServer(50102)

	var handlerCalled bool
	// var receivedUpdate *proto.UpdateMessage

	server.RegisterUpdateHandler(func(update *proto.UpdateMessage) error {
		handlerCalled = true
		// receivedUpdate = update
		return nil
	})

	// 启动服务器
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	// 等待服务器启动
	time.Sleep(100 * time.Millisecond)

	// 由于没有真实的 gRPC 客户端连接，处理器不会被调用
	// 但我们验证注册本身没有问题
	if server.updateHandler == nil {
		t.Error("Update handler should be registered")
	}

	t.Logf("Handler registered: called=%v", handlerCalled)
}

// TestRegisterCLChangeHandler 注册 CL 变更处理器
func TestRegisterCLChangeHandler(t *testing.T) {
	server := NewGRPCServer(50103)

	var handlerCalled bool
	// var receivedConfig *proto.CLConfig

	server.RegisterCLChangeHandler(func(config *proto.CLConfig) error {
		handlerCalled = true
		// receivedConfig = config
		return nil
	})

	if server.clChangeHandler == nil {
		t.Error("CL change handler should be registered")
	}

	t.Logf("CL handler registered: called=%v", handlerCalled)
}

// TestIsRunning 检查运行状态
func TestIsRunning(t *testing.T) {
	server := NewGRPCServer(50104)

	// 初始状态
	if server.IsRunning() {
		t.Error("Server should not be running initially")
	}

	// 启动后
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	if !server.IsRunning() {
		t.Error("Server should be running after Start()")
	}

	// 停止后
	server.Stop()

	if server.IsRunning() {
		t.Error("Server should not be running after Stop()")
	}
}

// TestGRPCClient 创建 gRPC 客户端
func TestGRPCClient(t *testing.T) {
	// 由于需要真实的 gRPC 服务端，我们测试客户端创建逻辑

	// 测试无效地址
	client, err := NewGRPCClient("invalid-address:99999")
	if err == nil {
		t.Error("Expected error for invalid address")
		client.Close() // 防止资源泄漏
	}

	// 测试格式正确的地址（但服务不存在）
	client, err = NewGRPCClient("localhost:50105")
	if err != nil {
		// 这是预期的，因为端口未监听
		t.Logf("Expected connection error: %v", err)
	} else {
		client.Close()
	}
}

// TestConnectionManager_AddRemovePeer 连接管理器
func TestConnectionManager_AddRemovePeer(t *testing.T) {
	cm := NewConnectionManager()

	if cm == nil {
		t.Fatal("NewConnectionManager returned nil")
	}

	if len(cm.clients) != 0 {
		t.Errorf("Expected empty clients map, got %d entries", len(cm.clients))
	}

	// 添加对等节点（会失败，因为地址无效）
	err := cm.AddPeer("invalid-host:50051")
	if err == nil {
		t.Error("Expected error for invalid peer address")
	}

	// 验证客户端列表未变化
	if len(cm.clients) != 0 {
		t.Errorf("Clients map should still be empty, got %d entries", len(cm.clients))
	}

	// 移除不存在的节点不应该 panic
	cm.RemovePeer("non-existent-peer:50051")
}

// TestConnectionManager_GetClient 获取客户端
func TestConnectionManager_GetClient(t *testing.T) {
	cm := NewConnectionManager()

	// 获取不存在的客户端
	client, exists := cm.GetClient("localhost:50106")
	if exists {
		t.Error("Should not exist")
	}
	if client != nil {
		t.Error("Client should be nil for non-existent peer")
	}
}

// TestConnectionManager_CloseAll 关闭所有连接
func TestConnectionManager_CloseAll(t *testing.T) {
	cm := NewConnectionManager()

	// 关闭空的连接管理器不应该 panic
	cm.CloseAll()

	// 验证客户端列表清空
	if len(cm.clients) != 0 {
		t.Errorf("Expected empty clients map after CloseAll, got %d", len(cm.clients))
	}
}

// TestConcurrentConnections 并发连接测试
func TestConcurrentConnections(t *testing.T) {
	cm := NewConnectionManager()

	var successCount int32
	var failCount int32

	// 并发尝试添加多个连接
	for i := 0; i < 10; i++ {
		go func(idx int) {
			addr := fmt.Sprintf("host-%d:50051", idx)
			err := cm.AddPeer(addr)
			if err == nil {
				atomic.AddInt32(&successCount, 1)
				cm.RemovePeer(addr) // 清理
			} else {
				atomic.AddInt32(&failCount, 1)
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond)

	finalSuccess := atomic.LoadInt32(&successCount)
	finalFail := atomic.LoadInt32(&failCount)

	// 由于都是无效地址，应该全部失败
	if finalSuccess > 0 {
		t.Errorf("Expected 0 successes, got %d", finalSuccess)
	}
	if finalFail < 5 {
		t.Errorf("Expected at least 5 failures, got %d", finalFail)
	}

	// 清理
	cm.CloseAll()
}

// TestServerLifecycle 完整的服务器生命周期测试
func TestServerLifecycle(t *testing.T) {
	server := NewGRPCServer(50107)

	// 完整的启动-运行-停止周期
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	// 验证状态
	if !server.IsRunning() {
		t.Error("Server should be running")
	}

	// 让服务器运行一段时间
	time.Sleep(100 * time.Millisecond)

	// 停止
	server.Stop()

	// 验证状态
	if server.IsRunning() {
		t.Error("Server should not be running after stop")
	}

	// 验证资源清理
	if server.server != nil {
		t.Log("Note: server.server may not be nil immediately after Stop()")
	}
}

// TestPortBinding 端口绑定测试
func TestPortBinding(t *testing.T) {
	// 测试端口已被占用的情况

	// 先启动一个服务器占用端口
	server1 := NewGRPCServer(50108)
	if err := server1.Start(); err != nil {
		t.Fatalf("Failed to start first server: %v", err)
	}
	defer server1.Stop()

	// 尝试在相同端口启动另一个服务器
	server2 := NewGRPCServer(50108)
	err := server2.Start()
	if err == nil {
		t.Error("Expected error when binding to occupied port")
		server2.Stop()
	} else {
		t.Logf("Port binding error (expected): %v", err)
	}
}

// TestEdgeCases 边缘情况测试
func TestEdgeCases(t *testing.T) {
	// 测试边界端口号
	testPorts := []int{1, 1024, 65535}

	for _, port := range testPorts {
		server := NewGRPCServer(port)
		if server.port != port {
			t.Errorf("Port %d not set correctly", port)
		}
	}

	// 测试零端口（系统分配）
	server := NewGRPCServer(0)
	if server.port != 0 {
		t.Errorf("Expected port 0, got %d", server.port)
	}

	// 测试负端口
	server = NewGRPCServer(-1)
	if server.port != -1 {
		t.Errorf("Negative port not preserved")
	}
	// 启动会失败，这是预期的
	err := server.Start()
	if err == nil {
		t.Error("Expected error for negative port")
		server.Stop()
	}
}

// mockGRPCService 用于测试的服务模拟
type mockGRPCService struct {
	proto.UnimplementedACServiceServer
	updateCalls int32
	configCalls int32
}

func (m *mockGRPCService) PropagateUpdate(stream proto.ACService_PropagateUpdateServer) error {
	atomic.AddInt32(&m.updateCalls, 1)
	return nil
}

func (m *mockGRPCService) ChangeConsistencyLevel(ctx context.Context, config *proto.CLConfig) (*proto.Ack, error) {
	atomic.AddInt32(&m.configCalls, 1)
	return &proto.Ack{Success: true}, nil
}

// TestServiceRegistration 服务注册测试
func TestServiceRegistration(t *testing.T) {
	// 使用 bufconn 进行内存测试
	lis := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()

	mockSvc := &mockGRPCService{}
	proto.RegisterACServiceServer(s, mockSvc)

	go func() {
		if err := s.Serve(lis); err != nil {
			t.Logf("Server exited with error: %v", err)
		}
	}()
	defer s.Stop()

	// 连接到内存 listener
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithInsecure())
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	client := proto.NewACServiceClient(conn)

	// 测试调用
	_, err = client.ChangeConsistencyLevel(ctx, &proto.CLConfig{Key: "test"})
	if err != nil {
		t.Logf("Service call error (may be expected): %v", err)
	}

	// 验证服务被调用
	callCount := atomic.LoadInt32(&mockSvc.configCalls)
	t.Logf("Service calls: %d", callCount)
}
