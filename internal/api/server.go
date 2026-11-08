package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aviary/agent/internal/auth"
	"github.com/aviary/agent/internal/container"
	"github.com/aviary/agent/internal/metrics"
	"github.com/aviary/agent/pkg/docker"
	"github.com/gorilla/websocket"
)

type Server struct {
	manager   *container.Manager
	auth      *auth.Authenticator
	metrics   *metrics.Collector
	upgrader  websocket.Upgrader
	server    *http.Server
}

func NewServer(manager *container.Manager, authenticator *auth.Authenticator, collector *metrics.Collector, addr string, readTimeout, writeTimeout, idleTimeout time.Duration, tlsEnabled bool, certFile, keyFile string) *Server {
	s := &Server{
		manager: manager,
		auth:    authenticator,
		metrics: collector,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.authMiddleware(mux),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	return s
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/api/v1/containers/create", s.containerCreate)
	mux.HandleFunc("/api/v1/containers/", s.containerByID)
	mux.HandleFunc("/api/v1/images", s.imagesList)
	mux.HandleFunc("/api/v1/images/pull", s.imagePull)
	mux.HandleFunc("/api/v1/images/", s.imageByID)
	mux.HandleFunc("/api/v1/system/info", s.systemInfo)
	mux.HandleFunc("/api/v1/system/version", s.systemVersion)
	mux.HandleFunc("/api/v1/system/df", s.systemDF)
	mux.HandleFunc("/api/v1/system/prune", s.systemPrune)
	mux.HandleFunc("/api/v1/files", s.fileManager)
mux.HandleFunc("/api/v1/files/", s.fileManager)
	mux.HandleFunc("/ws", s.websocket)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		token := r.Header.Get("Authorization")
		if token == "" {
			s.writeError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}

		token = strings.TrimPrefix(token, "Bearer ")
		claims, err := s.auth.ValidateToken(token)
		if err != nil {
			s.writeError(w, http.StatusUnauthorized, err.Error())
			return
		}

		if err := s.auth.CheckIP(r); err != nil {
			s.writeError(w, http.StatusForbidden, err.Error())
			return
		}

		r.Header.Set("X-Panel-ID", claims.PanelID)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *Server) containersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all := r.URL.Query().Get("all") == "true"
	containers, err := s.manager.List(ctx, all)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, containers)
}

func (s *Server) containerCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req container.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	resp, err := s.manager.Create(ctx, &req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) containerByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/containers/")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "container ID required")
		return
	}

	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		resp, err := s.manager.Inspect(ctx, id)
		if err != nil {
			if errors.Is(err, container.ErrContainerNotFound) {
				s.writeError(w, http.StatusNotFound, err.Error())
				return
			}
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, resp)

	case http.MethodDelete:
		force := r.URL.Query().Get("force") == "true"
		if err := s.manager.Remove(ctx, id, force); err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})

	case http.MethodPost:
		action := r.URL.Query().Get("action")
		var err error
		switch action {
		case "start":
			err = s.manager.Start(ctx, id)
		case "stop":
			timeout := parseTimeout(r.URL.Query().Get("timeout"))
			err = s.manager.Stop(ctx, id, timeout)
		case "restart":
			timeout := parseTimeout(r.URL.Query().Get("timeout"))
			err = s.manager.Restart(ctx, id, timeout)
		case "kill":
			signal := r.URL.Query().Get("signal")
			if signal == "" {
				signal = "SIGKILL"
			}
			err = s.manager.Kill(ctx, id, signal)
		default:
			s.writeError(w, http.StatusBadRequest, "invalid action")
			return
		}
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]string{"status": action + "ed"})

	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) imagesList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	images, err := s.manager.ListImages(ctx)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, images)
}

func (s *Server) imagePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Image string `json:"image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	reader, err := s.manager.PullImage(ctx, req.Image)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, reader)
}

func (s *Server) imageByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/images/")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "image ID required")
		return
	}

	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		img, err := s.manager.InspectImage(ctx, id)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, img)

	case http.MethodDelete:
		resp, err := s.manager.RemoveImage(ctx, id)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, resp)

	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	info, err := s.manager.SystemInfo(ctx)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, json.RawMessage(info))
}

func (s *Server) systemVersion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ver, err := s.manager.SystemVersion(ctx)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, ver)
}

func (s *Server) systemDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	df, err := s.manager.SystemDF(ctx)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, df)
}

func (s *Server) systemPrune(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	result, err := s.manager.Prune(ctx)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) fileManager(w http.ResponseWriter, r *http.Request) {
	// Handle both /api/v1/files/ and /api/v1/files/path
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/files")
	path = strings.TrimPrefix(path, "/")
	
	// If no path in URL (e.g., /api/v1/files/), use query parameter
	// For listing root, path can be empty (daemon defaults to "/")
	if path == "" {
		path = r.URL.Query().Get("path")
		path = strings.TrimPrefix(path, "/")
	}

	ctx := r.Context()
	containerID := r.URL.Query().Get("container_id")
	if containerID == "" {
		s.writeError(w, http.StatusBadRequest, "container_id required")
		return
	}

	// Strip action suffixes from path (e.g., /chmod, /mkdir)
	// These are separate endpoints in the daemon
	action := r.URL.Query().Get("action")
	if action == "chmod" || action == "mkdir" {
		path = strings.TrimSuffix(path, "/"+action)
	}

	// Handle process management endpoints
	if strings.HasPrefix(path, "process") {
		s.handleProcess(w, r, ctx, containerID, path)
		return
	}

	switch r.Method {
	case http.MethodGet:
		action := r.URL.Query().Get("action")
		if action == "stat" {
			s.fileStat(w, r, ctx, containerID, path)
		} else if action == "read" || r.URL.Query().Get("raw") == "true" || r.URL.Query().Get("download") == "true" {
			s.fileRead(w, r, ctx, containerID, path)
		} else {
			s.fileList(w, r, ctx, containerID, path)
		}

	case http.MethodPost:
		action := r.URL.Query().Get("action")
		switch action {
		case "write":
			s.fileWrite(w, r, ctx, containerID, path)
		case "mkdir":
			s.fileMkdir(w, r, ctx, containerID, path)
		case "chmod":
			s.fileChmod(w, r, ctx, containerID, path)
		case "copy":
			s.fileCopy(w, r, ctx, containerID, path)
		case "update":
			s.fileUpdate(w, r, ctx, containerID, path)
		default:
			s.writeError(w, http.StatusBadRequest, "invalid action")
		}

	case http.MethodDelete:
		s.fileDelete(w, r, ctx, containerID, path)

	case http.MethodPut:
		s.fileMove(w, r, ctx, containerID, path)

	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) fileList(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	entries, err := s.manager.FileList(ctx, containerID, path)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, entries)
}

func (s *Server) fileStat(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	entry, err := s.manager.FileStat(ctx, containerID, path)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, entry)
}

func (s *Server) fileRead(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	reader, err := s.manager.FileRead(ctx, containerID, path)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, path))
	io.Copy(w, reader)
}

func (s *Server) fileWrite(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	size := r.ContentLength
	if size <= 0 {
		s.writeError(w, http.StatusBadRequest, "content-length required")
		return
	}

	err := s.manager.FileWrite(ctx, containerID, path, r.Body, size)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "written"})
}

func (s *Server) fileMkdir(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	mode := uint32(0755)
	if m := r.URL.Query().Get("mode"); m != "" {
		fmt.Sscanf(m, "%o", &mode)
	}

	err := s.manager.FileCreateDir(ctx, containerID, path, mode)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "created"})
}

func (s *Server) fileDelete(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	err := s.manager.FileDelete(ctx, containerID, path)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) fileChmod(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	mode := uint32(0644)
	if m := r.URL.Query().Get("mode"); m != "" {
		fmt.Sscanf(m, "%o", &mode)
	}

	err := s.manager.FileChmod(ctx, containerID, path, mode)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "changed"})
}

func (s *Server) fileMove(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	dest := r.URL.Query().Get("dest")
	if dest == "" {
		s.writeError(w, http.StatusBadRequest, "dest query parameter required")
		return
	}

	err := s.manager.FileMove(ctx, containerID, path, dest)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "moved"})
}

func (s *Server) fileCopy(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	dest := r.URL.Query().Get("dest")
	if dest == "" {
		s.writeError(w, http.StatusBadRequest, "dest query parameter required")
		return
	}

	err := s.manager.FileCopy(ctx, containerID, path, dest)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "copied"})
}

func (s *Server) fileUpdate(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	size := r.ContentLength
	if size <= 0 {
		s.writeError(w, http.StatusBadRequest, "content-length required")
		return
	}

	err := s.manager.FileUpdate(ctx, containerID, path, r.Body, size)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleProcess handles process management endpoints (replaces daemon proxy)
func (s *Server) handleProcess(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID, path string) {
	// path is like "process", "process/start", "process/stop", etc.
	path = strings.TrimPrefix(path, "process")
	path = strings.TrimPrefix(path, "/")

	switch r.Method {
	case http.MethodGet:
		if path == "" || path == "info" {
			s.processInfo(w, r, ctx, containerID)
		} else {
			s.writeError(w, http.StatusNotFound, "process endpoint not found")
		}

	case http.MethodPost:
		switch path {
		case "start":
			s.processStart(w, r, ctx, containerID)
		case "stop":
			s.processStop(w, r, ctx, containerID)
		case "restart":
			s.processRestart(w, r, ctx, containerID)
		case "signal":
			s.processSignal(w, r, ctx, containerID)
		default:
			s.writeError(w, http.StatusNotFound, "process endpoint not found")
		}

	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) processInfo(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID string) {
	info, err := s.manager.ProcessInfo(ctx, containerID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, info)
}

func (s *Server) processStart(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID string) {
	err := s.manager.ProcessStart(ctx, containerID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) processStop(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID string) {
	timeout := parseTimeout(r.URL.Query().Get("timeout"))
	err := s.manager.ProcessStop(ctx, containerID, timeout)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) processRestart(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID string) {
	timeout := parseTimeout(r.URL.Query().Get("timeout"))
	err := s.manager.ProcessRestart(ctx, containerID, timeout)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

func (s *Server) processSignal(w http.ResponseWriter, r *http.Request, ctx context.Context, containerID string) {
	signal := r.URL.Query().Get("signal")
	if signal == "" {
		s.writeError(w, http.StatusBadRequest, "signal query parameter required")
		return
	}
	err := s.manager.ProcessSignal(ctx, containerID, signal)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "signal sent"})
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	claims, err := s.auth.ValidateToken(token)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	if err := s.auth.CheckIP(r); err != nil {
		s.writeError(w, http.StatusForbidden, err.Error())
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	s.handleWebSocket(conn, claims.PanelID)
}

func (s *Server) handleWebSocket(conn *websocket.Conn, panelID string) {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var req WSRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			conn.WriteJSON(WSResponse{Error: err.Error()})
			continue
		}

		ctx := context.Background()
		var resp WSResponse

		switch req.Type {
		case "logs":
			resp = s.handleLogs(ctx, req)
		case "stats":
			resp = s.handleStats(ctx, req)
		case "exec":
			s.handleExecWS(conn, ctx, req)
			continue
		case "resize":
			resp = s.handleResize(ctx, req)
		default:
			resp.Error = "unknown request type"
		}

		conn.WriteJSON(resp)
	}
}

func (s *Server) handleExecWS(wsConn *websocket.Conn, ctx context.Context, req WSRequest) {
	createResp, err := s.manager.ExecCreate(ctx, req.ContainerID, docker.ExecConfig{
		Cmd:          req.Cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          req.Tty,
		User:         req.User,
		WorkingDir:   req.WorkingDir,
		Env:          req.Env,
	})
	if err != nil {
		wsConn.WriteJSON(WSResponse{Type: "exec", Error: err.Error()})
		return
	}

	hijacked, err := s.manager.ExecAttach(ctx, createResp.ID, docker.ExecStartCheck{
		Detach: false,
		Tty:    req.Tty,
	})
	if err != nil {
		wsConn.WriteJSON(WSResponse{Type: "exec", Error: err.Error()})
		return
	}

	if err := s.manager.ExecStart(ctx, createResp.ID, docker.ExecStartCheck{
		Detach: false,
		Tty:    req.Tty,
	}); err != nil {
		wsConn.WriteJSON(WSResponse{Type: "exec", Error: err.Error()})
		return
	}

	wsConn.WriteJSON(WSResponse{Type: "exec", ExecID: createResp.ID, Success: true})

	done := make(chan struct{}, 2)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := hijacked.Reader.Read(buf)
			if n > 0 {
				wsConn.WriteMessage(websocket.BinaryMessage, buf[:n])
			}
			if err != nil {
				close(done)
				return
			}
		}
	}()

	go func() {
		for {
			_, msg, err := wsConn.ReadMessage()
			if err != nil {
				close(done)
				return
			}
			if len(msg) > 0 {
				hijacked.Conn.Write(msg)
			}
		}
	}()

	<-done
	hijacked.Conn.Close()
}

func (s *Server) handleLogs(ctx context.Context, req WSRequest) WSResponse {
	reader, err := s.manager.Logs(ctx, req.ContainerID, docker.ContainerLogsOptions{
		Follow:     req.Follow,
		Timestamps: req.Timestamps,
		Tail:       req.Tail,
		Since:      req.Since,
		Until:      req.Until,
	})
	if err != nil {
		return WSResponse{Error: err.Error()}
	}
	defer reader.Close()

	return WSResponse{Type: "logs", Data: reader}
}

func (s *Server) handleStats(ctx context.Context, req WSRequest) WSResponse {
	reader, err := s.manager.Stats(ctx, req.ContainerID, req.Stream)
	if err != nil {
		return WSResponse{Error: err.Error()}
	}

	return WSResponse{Type: "stats", Data: reader}
}

func (s *Server) handleResize(ctx context.Context, req WSRequest) WSResponse {
	if err := s.manager.ExecResize(ctx, req.ExecID, req.Height, req.Width); err != nil {
		return WSResponse{Error: err.Error()}
	}
	return WSResponse{Type: "resize", Success: true}
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) Start() error {
	return s.server.ListenAndServe()
}

func (s *Server) StartTLS(certFile, keyFile string) error {
	return s.server.ListenAndServeTLS(certFile, keyFile)
}

func (s *Server) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func parseTimeout(s string) *time.Duration {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil
	}
	return &d
}

type WSRequest struct {
	Type         string   `json:"type"`
	ContainerID  string   `json:"container_id"`
	ExecID       string   `json:"exec_id"`
	Cmd          []string `json:"cmd"`
	Follow       bool     `json:"follow"`
	Timestamps   bool     `json:"timestamps"`
	Tail         string   `json:"tail"`
	Since        string   `json:"since"`
	Until        string   `json:"until"`
	Stream       bool     `json:"stream"`
	AttachStdin  bool     `json:"attach_stdin"`
	AttachStdout bool     `json:"attach_stdout"`
	AttachStderr bool     `json:"attach_stderr"`
	Tty          bool     `json:"tty"`
	User         string   `json:"user"`
	WorkingDir   string   `json:"working_dir"`
	Env          []string `json:"env"`
	Height       int      `json:"height"`
	Width        int      `json:"width"`
}

type WSResponse struct {
	Type     string      `json:"type"`
	Error    string      `json:"error,omitempty"`
	Success  bool        `json:"success,omitempty"`
	ExecID   string      `json:"exec_id,omitempty"`
	Data     interface{} `json:"data,omitempty"`
	Conn     interface{} `json:"-"`
}