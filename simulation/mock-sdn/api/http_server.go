package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/controller"
	"github.com/weufhsos/adaptive_sync_go/simulation/mock-sdn/metrics"
)

// Server HTTP API服务器
type Server struct {
	port       int
	controller *controller.SDNController
	metrics    *metrics.Exporter
	router     *mux.Router
	server     *http.Server
}

// EmbedRequest 服务嵌入请求（替代 AllocateRequest）
type EmbedRequest struct {
	Cost      float64 `json:"cost"`        // 所需资源百分比 (如 10.0 表示 10%)
	Duration  int     `json:"duration_ms"` // 服务持续时间（毫秒）
	UseGlobal bool    `json:"use_global"`  // 是否使用全局共享资源（用于AC测试）
}

// EmbedResponse 服务嵌入响应（替代 AllocateResponse）
type EmbedResponse struct {
	Success   bool    `json:"success"`
	Message   string  `json:"message"`
	RequestID string  `json:"request_id,omitempty"`
	ServerID  string  `json:"server_id"` // 分配到的服务器ID
	Cost      float64 `json:"cost"`      // 实际分配的资源
	Remaining float64 `json:"remaining"` // 该服务器剩余容量
}

// ReleaseRequest 资源释放请求（保持ReleaseRequest不变，但语义从"释放链路带宽"变为"释放服务器资源"）
type ReleaseRequest struct {
	ServerID string  `json:"server_id"` // 服务器ID
	Cost     float64 `json:"cost"`      // 释放的资源量
}

// NewServer 创建HTTP服务器
func NewServer(port int, ctrl *controller.SDNController, metricsExporter *metrics.Exporter) *Server {
	s := &Server{
		port:       port,
		controller: ctrl,
		metrics:    metricsExporter,
		router:     mux.NewRouter(),
	}

	s.setupRoutes()
	return s
}

// setupRoutes 设置路由
func (s *Server) setupRoutes() {
	api := s.router.PathPrefix("/api/v1").Subrouter()

	// 服务管理（替代带宽管理）
	api.HandleFunc("/embed", s.handleEmbed).Methods("POST") // 新端点
	api.HandleFunc("/release", s.handleRelease).Methods("POST")
	api.HandleFunc("/best-server", s.handleGetBestServer).Methods("GET") // 新端点

	// 状态查询
	api.HandleFunc("/status", s.handleStatus).Methods("GET")
	api.HandleFunc("/servers", s.handleGetServers).Methods("GET")     // 新端点
	api.HandleFunc("/servers/{id}", s.handleGetServer).Methods("GET") // 新端点

	// 一致性信息
	api.HandleFunc("/consistency", s.handleConsistency).Methods("GET")

	// 指标
	api.HandleFunc("/metrics", s.handleMetrics).Methods("GET")
	s.router.HandleFunc("/metrics", s.handlePrometheusMetrics).Methods("GET")

	// 健康检查
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")

	// 保留旧端点以保持向后兼容（可选）
	api.HandleFunc("/allocate", s.handleEmbed).Methods("POST") // 兼容旧客户端
	api.HandleFunc("/best-link", s.handleGetBestServer).Methods("GET")
	api.HandleFunc("/links", s.handleGetServers).Methods("GET")
	api.HandleFunc("/links/{id}", s.handleGetServer).Methods("GET")
}

// Start 启动服务器
func (s *Server) Start() {
	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Printf("[API] Starting HTTP server on port %d", s.port)
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("[API] HTTP server error: %v", err)
	}
}

// Stop 停止服务器
func (s *Server) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(ctx)
		log.Printf("[API] HTTP server stopped")
	}
}

// handleEmbed 处理服务嵌入请求（替代 handleAllocate）
func (s *Server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	var req EmbedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	log.Printf("[API] Embed request: use_global=%v, cost=%.2f%%, duration=%dms",
		req.UseGlobal, req.Cost, req.Duration)

	var serverID string
	var err error

	// 如果请求使用全局资源
	if req.UseGlobal {
		serverID = "global"
		log.Printf("[API] Using global resource allocation")
		err = s.controller.AllocateGlobalResources(req.Cost)
	} else {
		// 自动选择最佳服务器
		serverID, err = s.controller.AllocateServer(req.Cost)
		if err != nil {
			s.respondJSON(w, http.StatusConflict, EmbedResponse{
				Success:  false,
				Message:  err.Error(),
				ServerID: serverID,
			})
			return
		}
	}

	if err != nil {
		s.respondJSON(w, http.StatusConflict, EmbedResponse{
			Success:  false,
			Message:  err.Error(),
			ServerID: serverID,
		})
		return
	}

	// 如果设置了持续时间，安排自动释放
	if req.Duration > 0 {
		go func() {
			time.Sleep(time.Duration(req.Duration) * time.Millisecond)
			if req.UseGlobal {
				s.controller.ReleaseGlobalResources(req.Cost)
			} else {
				// TODO: 实现 ReleaseServer 方法
				// s.controller.ReleaseServer(serverID, req.Cost)
				log.Printf("[API] Auto-release scheduled for server %s: %.2f%%", serverID, req.Cost)
			}
		}()
	}

	// 获取剩余资源
	remaining := 0.0
	if req.UseGlobal {
		remaining = s.controller.GetGlobalAvailable()
	} else {
		// TODO: 实现 GetServerStatus 方法
		// if status, err := s.controller.GetServerStatus(serverID); err == nil {
		// 	remaining = status.Available
		// }
		remaining = 100.0 - req.Cost // 临时值
	}

	s.respondJSON(w, http.StatusOK, EmbedResponse{
		Success:   true,
		Message:   "Service embedded successfully",
		RequestID: fmt.Sprintf("%s-%d", serverID, time.Now().UnixNano()),
		ServerID:  serverID,
		Cost:      req.Cost,
		Remaining: remaining,
	})
}

// handleRelease 处理资源释放请求（语义从"释放链路带宽"变为"释放服务器资源"）
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req ReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// TODO: 实现 ReleaseServer 方法
	// if err := s.controller.ReleaseServer(req.ServerID, req.Cost); err != nil {
	// 	s.respondError(w, http.StatusInternalServerError, err.Error())
	// 	return
	// }

	// 临时实现：仅记录日志
	log.Printf("[API] Release request: server_id=%s, cost=%.2f%% (not implemented yet)", req.ServerID, req.Cost)

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"message":   "Resource release scheduled",
		"server_id": req.ServerID,
		"released":  req.Cost,
	})
}

// handleGetBestLink 获取最佳链路
func (s *Server) handleGetBestLink(w http.ResponseWriter, r *http.Request) {
	linkID, available, err := s.controller.GetBestLink()
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"link_id":   linkID,
		"available": available,
	})
}

// handleStatus 获取控制器状态
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"node_id":     s.controller.GetNodeID(),
		"status":      "running",
		"consistency": s.controller.GetConsistencyInfo(),
	}

	s.respondJSON(w, http.StatusOK, status)
}

// handleGetBestServer 获取最佳服务器（替代 handleGetBestLink）
func (s *Server) handleGetBestServer(w http.ResponseWriter, r *http.Request) {
	// TODO: 实现 GetBestServer 方法
	// serverID, available, err := s.controller.GetBestServer()
	// if err != nil {
	// 	s.respondError(w, http.StatusServiceUnavailable, err.Error())
	// 	return
	// }

	// 临时实现：返回固定值
	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"server_id": "server-1",
		"available": 85.5,
		"message":   "Temporary implementation",
	})
}

// handleGetServers 获取所有服务器状态（替代 handleGetLinks）
func (s *Server) handleGetServers(w http.ResponseWriter, r *http.Request) {
	// TODO: 实现 GetAllServersStatus 方法
	// servers := s.controller.GetAllServersStatus()

	// 临时实现：返回示例数据
	servers := map[string]interface{}{
		"server-1": map[string]interface{}{
			"id":        "server-1",
			"capacity":  100.0,
			"allocated": 15.0,
			"available": 85.0,
			"load":      15.0,
		},
		"server-2": map[string]interface{}{
			"id":        "server-2",
			"capacity":  100.0,
			"allocated": 30.0,
			"available": 70.0,
			"load":      30.0,
		},
	}
	s.respondJSON(w, http.StatusOK, servers)
}

// handleGetServer 获取单个服务器状态（替代 handleGetLink）
func (s *Server) handleGetServer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID := vars["id"]

	// TODO: 实现 GetServerStatus 方法
	// status, err := s.controller.GetServerStatus(serverID)
	// if err != nil {
	// 	s.respondError(w, http.StatusNotFound, err.Error())
	// 	return
	// }

	// 临时实现：返回示例数据
	status := map[string]interface{}{
		"id":        serverID,
		"capacity":  100.0,
		"allocated": 25.0,
		"available": 75.0,
		"load":      25.0,
	}
	s.respondJSON(w, http.StatusOK, status)
}

// handleConsistency 获取一致性信息
func (s *Server) handleConsistency(w http.ResponseWriter, r *http.Request) {
	info := s.controller.GetConsistencyInfo()
	s.respondJSON(w, http.StatusOK, info)
}

// handleMetrics 获取指标（JSON格式）
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := s.metrics.GetMetrics()
	s.respondJSON(w, http.StatusOK, metrics)
}

// handlePrometheusMetrics 获取Prometheus格式指标
func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(s.metrics.GetPrometheusMetrics()))
}

// handleHealth 健康检查
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.respondJSON(w, http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// respondJSON 返回JSON响应
func (s *Server) respondJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

// respondError 返回错误响应
func (s *Server) respondError(w http.ResponseWriter, code int, message string) {
	s.respondJSON(w, code, map[string]interface{}{
		"success": false,
		"error":   message,
	})
}
