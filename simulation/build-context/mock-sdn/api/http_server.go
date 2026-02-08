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

// AllocateRequest 带宽分配请求
type AllocateRequest struct {
	LinkID    string  `json:"link_id"`
	Bandwidth float64 `json:"bandwidth"`
	HoldTime  int     `json:"hold_time_ms"` // 持有时间（毫秒）
	UseGlobal bool    `json:"use_global"`   // 是否使用全局共享带宽（用于AC测试）
}

// AllocateResponse 带宽分配响应
type AllocateResponse struct {
	Success      bool    `json:"success"`
	Message      string  `json:"message"`
	AllocationID string  `json:"allocation_id,omitempty"`
	LinkID       string  `json:"link_id"`
	Allocated    float64 `json:"allocated"`
	Remaining    float64 `json:"remaining"`
}

// ReleaseRequest 带宽释放请求
type ReleaseRequest struct {
	LinkID    string  `json:"link_id"`
	Bandwidth float64 `json:"bandwidth"`
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

	// 带宽管理
	api.HandleFunc("/allocate", s.handleAllocate).Methods("POST")
	api.HandleFunc("/release", s.handleRelease).Methods("POST")
	api.HandleFunc("/best-link", s.handleGetBestLink).Methods("GET")

	// 状态查询
	api.HandleFunc("/status", s.handleStatus).Methods("GET")
	api.HandleFunc("/links", s.handleGetLinks).Methods("GET")
	api.HandleFunc("/links/{id}", s.handleGetLink).Methods("GET")

	// 一致性信息
	api.HandleFunc("/consistency", s.handleConsistency).Methods("GET")

	// 指标
	api.HandleFunc("/metrics", s.handleMetrics).Methods("GET")
	s.router.HandleFunc("/metrics", s.handlePrometheusMetrics).Methods("GET")

	// 健康检查
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")
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

// handleAllocate 处理带宽分配请求
func (s *Server) handleAllocate(w http.ResponseWriter, r *http.Request) {
	var req AllocateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	log.Printf("[API] Allocate request: use_global=%v, bandwidth=%.2f, link_id=%s", 
		req.UseGlobal, req.Bandwidth, req.LinkID)

	var linkID string
	var err error

	// 如果请求使用全局带宽
	if req.UseGlobal {
		linkID = "global"
		log.Printf("[API] Using global bandwidth allocation")
		err = s.controller.AllocateGlobalBandwidth(req.Bandwidth)
	} else {
		// 如果未指定链路，选择最佳链路
		linkID = req.LinkID
		if linkID == "" {
			bestLink, _, err := s.controller.GetBestLink()
			if err != nil {
				s.respondError(w, http.StatusServiceUnavailable, "No available links")
				return
			}
			linkID = bestLink
		}
		// 分配带宽
		err = s.controller.AllocateBandwidth(linkID, req.Bandwidth)
	}

	if err != nil {
		s.respondJSON(w, http.StatusConflict, AllocateResponse{
			Success: false,
			Message: err.Error(),
			LinkID:  linkID,
		})
		return
	}

	// 如果设置了持有时间，安排自动释放
	if req.HoldTime > 0 {
		go func() {
			time.Sleep(time.Duration(req.HoldTime) * time.Millisecond)
			if req.UseGlobal {
				s.controller.ReleaseGlobalBandwidth(req.Bandwidth)
			} else {
				s.controller.ReleaseBandwidth(linkID, req.Bandwidth)
			}
		}()
	}

	// 获取剩余带宽
	remaining := 0.0
	if req.UseGlobal {
		remaining = s.controller.GetGlobalAvailable()
	} else if status, err := s.controller.GetLinkStatus(linkID); err == nil {
		remaining = status.Available
	}

	s.respondJSON(w, http.StatusOK, AllocateResponse{
		Success:      true,
		Message:      "Bandwidth allocated",
		AllocationID: fmt.Sprintf("%s-%d", linkID, time.Now().UnixNano()),
		LinkID:       linkID,
		Allocated:    req.Bandwidth,
		Remaining:    remaining,
	})
}

// handleRelease 处理带宽释放请求
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req ReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := s.controller.ReleaseBandwidth(req.LinkID, req.Bandwidth); err != nil {
		s.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"message":  "Bandwidth released",
		"link_id":  req.LinkID,
		"released": req.Bandwidth,
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

// handleGetLinks 获取所有链路状态
func (s *Server) handleGetLinks(w http.ResponseWriter, r *http.Request) {
	links := s.controller.GetAllLinksStatus()
	s.respondJSON(w, http.StatusOK, links)
}

// handleGetLink 获取单个链路状态
func (s *Server) handleGetLink(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	linkID := vars["id"]

	status, err := s.controller.GetLinkStatus(linkID)
	if err != nil {
		s.respondError(w, http.StatusNotFound, err.Error())
		return
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
