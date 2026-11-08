package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/aviary/agent/pkg/docker"
)

// DaemonClient proxies requests to the in-container daemon
type DaemonClient struct {
	dockerClient *docker.Client
	httpClient   *http.Client
	daemonPort   string
	networkName  string // preferred Docker network for IP lookup
}

// FileEntry represents a file/directory entry from the daemon
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime string    `json:"mod_time"` // RFC3339 format
	IsDir   bool      `json:"is_dir"`
}

// ProcessInfo represents process status from the daemon
type ProcessInfo struct {
	PID        int      `json:"pid,omitempty"`
	State      string   `json:"state"`
	ExitCode   int      `json:"exit_code,omitempty"`
	StartedAt  string   `json:"started_at,omitempty"`
	Command    []string `json:"command,omitempty"`
}

// NewDaemonClient creates a new daemon client
func NewDaemonClient(dockerClient *docker.Client, daemonPort string) *DaemonClient {
	if daemonPort == "" {
		daemonPort = "8080"
	}
	return &DaemonClient{
		dockerClient: dockerClient,
		daemonPort:   daemonPort,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetNetwork sets the preferred Docker network for daemon IP resolution
func (dc *DaemonClient) SetNetwork(networkName string) {
	dc.networkName = networkName
}

// getDaemonURL returns the base URL for the daemon in a container
// Requires daemon_network to be configured in config.yaml
func (dc *DaemonClient) getDaemonURL(ctx context.Context, containerID string) (string, error) {
	if dc.networkName == "" {
		return "", fmt.Errorf("daemon_network not configured in config.yaml (required)")
	}

	inspect, err := dc.dockerClient.InspectContainer(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("failed to inspect container: %w", err)
	}

	// Use the configured network only
	if net := inspect.NetworkSettings.Networks[dc.networkName]; net != nil && net.IPAddress != "" {
		return fmt.Sprintf("http://%s:%s", net.IPAddress, dc.daemonPort), nil
	}

	return "", fmt.Errorf("container %s not connected to required network %s", containerID, dc.networkName)
}

// doRequest makes an HTTP request to the daemon
func (dc *DaemonClient) doRequest(ctx context.Context, containerID, method, path string, body io.Reader, v interface{}) error {
	baseURL, err := dc.getDaemonURL(ctx, containerID)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := dc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("daemon error: %s - %s", resp.Status, string(bodyBytes))
	}

	if v != nil {
		return json.NewDecoder(resp.Body).Decode(v)
	}
	return nil
}

// doRequestRaw makes a request and returns the raw response body for streaming
func (dc *DaemonClient) doRequestRaw(ctx context.Context, containerID, method, path string, body io.Reader) (io.ReadCloser, error) {
	baseURL, err := dc.getDaemonURL(ctx, containerID)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := dc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("daemon error: %s - %s", resp.Status, string(bodyBytes))
	}

	return resp.Body, nil
}

// ===== File Operations =====

// List lists files in a directory
func (dc *DaemonClient) List(ctx context.Context, containerID, path string) ([]FileEntry, error) {
	query := ""
	if path != "" && path != "/" {
		query = "?path=" + url.QueryEscape(path)
	}

	var entries []FileEntry
	err := dc.doRequest(ctx, containerID, "GET", "/api/v1/files"+query, nil, &entries)
	return entries, err
}

// Stat returns file metadata
func (dc *DaemonClient) Stat(ctx context.Context, containerID, path string) (*FileEntry, error) {
	path = strings.TrimPrefix(path, "/")
	var entry FileEntry
	err := dc.doRequest(ctx, containerID, "GET", "/api/v1/files/"+url.PathEscape(path), nil, &entry)
	return &entry, err
}

// Read reads a file content
func (dc *DaemonClient) Read(ctx context.Context, containerID, path string) (io.ReadCloser, error) {
	path = strings.TrimPrefix(path, "/")
	return dc.doRequestRaw(ctx, containerID, "GET", "/api/v1/files/"+url.PathEscape(path)+"?raw=true", nil)
}

// Write writes a file
func (dc *DaemonClient) Write(ctx context.Context, containerID, path string, content io.Reader, size int64) error {
	path = strings.TrimPrefix(path, "/")
	// For writes, we need to stream the content directly
	baseURL, err := dc.getDaemonURL(ctx, containerID)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/v1/files/"+url.PathEscape(path), content)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if size > 0 {
		req.ContentLength = size
	}

	resp, err := dc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("daemon error: %s - %s", resp.Status, string(bodyBytes))
	}

	var result map[string]string
	return json.NewDecoder(resp.Body).Decode(&result)
}

// Delete deletes a file or directory
func (dc *DaemonClient) Delete(ctx context.Context, containerID, path string) error {
	path = strings.TrimPrefix(path, "/")
	var result map[string]string
	return dc.doRequest(ctx, containerID, "DELETE", "/api/v1/files/"+url.PathEscape(path), nil, &result)
}

// CreateDir creates a directory
func (dc *DaemonClient) CreateDir(ctx context.Context, containerID, path string, mode uint32) error {
	path = strings.TrimPrefix(path, "/")
	body := map[string]uint32{"mode": mode}
	jsonBody, _ := json.Marshal(body)

	var result map[string]string
	return dc.doRequest(ctx, containerID, "POST", "/api/v1/files/"+url.PathEscape(path)+"/mkdir", strings.NewReader(string(jsonBody)), &result)
}

// Chmod changes file permissions
func (dc *DaemonClient) Chmod(ctx context.Context, containerID, path string, mode uint32) error {
	path = strings.TrimPrefix(path, "/")
	body := map[string]uint32{"mode": mode}
	jsonBody, _ := json.Marshal(body)

	var result map[string]string
	return dc.doRequest(ctx, containerID, "POST", "/api/v1/files/"+url.PathEscape(path)+"/chmod", strings.NewReader(string(jsonBody)), &result)
}

// Move moves/renames a file
func (dc *DaemonClient) Move(ctx context.Context, containerID, srcPath, destPath string) error {
	srcPath = strings.TrimPrefix(srcPath, "/")
	destPath = strings.TrimPrefix(destPath, "/")
	query := "?dest=" + url.QueryEscape(destPath)

	var result map[string]string
	return dc.doRequest(ctx, containerID, "PUT", "/api/v1/files/"+url.PathEscape(srcPath)+query, nil, &result)
}

// Copy copies a file
func (dc *DaemonClient) Copy(ctx context.Context, containerID, srcPath, destPath string) error {
	srcPath = strings.TrimPrefix(srcPath, "/")
	destPath = strings.TrimPrefix(destPath, "/")
	query := "?action=copy&dest=" + url.QueryEscape(destPath)

	var result map[string]string
	return dc.doRequest(ctx, containerID, "POST", "/api/v1/files/"+url.PathEscape(srcPath)+query, nil, &result)
}

// Update updates a file (same as write but for text editor semantics)
func (dc *DaemonClient) Update(ctx context.Context, containerID, path string, content io.Reader, size int64) error {
	return dc.Write(ctx, containerID, path, content, size)
}

// ===== Process Operations =====

// ProcessInfo returns process status
func (dc *DaemonClient) ProcessInfo(ctx context.Context, containerID string) (*ProcessInfo, error) {
	var info ProcessInfo
	err := dc.doRequest(ctx, containerID, "GET", "/api/v1/process", nil, &info)
	return &info, err
}

// ProcessStart starts the application process
func (dc *DaemonClient) ProcessStart(ctx context.Context, containerID string) error {
	var result map[string]string
	return dc.doRequest(ctx, containerID, "POST", "/api/v1/process/start", nil, &result)
}

// ProcessStop stops the application process
func (dc *DaemonClient) ProcessStop(ctx context.Context, containerID string, timeout time.Duration) error {
	query := ""
	if timeout > 0 {
		query = "?timeout=" + timeout.String()
	}
	var result map[string]string
	return dc.doRequest(ctx, containerID, "POST", "/api/v1/process/stop"+query, nil, &result)
}

// ProcessRestart restarts the application process
func (dc *DaemonClient) ProcessRestart(ctx context.Context, containerID string, timeout time.Duration) error {
	query := ""
	if timeout > 0 {
		query = "?timeout=" + timeout.String()
	}
	var result map[string]string
	return dc.doRequest(ctx, containerID, "POST", "/api/v1/process/restart"+query, nil, &result)
}

// ProcessSignal sends a signal to the application process
func (dc *DaemonClient) ProcessSignal(ctx context.Context, containerID, signal string) error {
	body := map[string]string{"signal": signal}
	jsonBody, _ := json.Marshal(body)

	var result map[string]string
	return dc.doRequest(ctx, containerID, "POST", "/api/v1/process/signal", strings.NewReader(string(jsonBody)), &result)
}

// Health checks daemon health
func (dc *DaemonClient) Health(ctx context.Context, containerID string) error {
	var result map[string]string
	return dc.doRequest(ctx, containerID, "GET", "/health", nil, &result)
}

// ReverseProxy returns an httputil.ReverseProxy for WebSocket and streaming endpoints
func (dc *DaemonClient) ReverseProxy(ctx context.Context, containerID string) (*httputil.ReverseProxy, error) {
	baseURL, err := dc.getDaemonURL(ctx, containerID)
	if err != nil {
		return nil, err
	}

	targetURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	return proxy, nil
}

var _ interface{} = (*DaemonClient)(nil)