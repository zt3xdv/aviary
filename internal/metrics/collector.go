package metrics

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Collector struct {
	containersCreated   map[string]int64
	containersRemoved   map[string]int64
	containersStarted   map[string]int64
	containersStopped   map[string]int64
	containerMemoryUsage map[string]float64
	containerCPUUsage    map[string]float64
	containerPidsUsage   map[string]float64
	apiRequests         map[string]int64
	apiDuration         map[string]time.Duration
	activeConnections   int64
	mu                  sync.RWMutex
	server              *http.Server
	enabled             bool
}

func NewCollector(enabled bool, addr string, port int, path string) *Collector {
	c := &Collector{
		enabled:              enabled,
		containersCreated:    make(map[string]int64),
		containersRemoved:    make(map[string]int64),
		containersStarted:    make(map[string]int64),
		containersStopped:    make(map[string]int64),
		containerMemoryUsage: make(map[string]float64),
		containerCPUUsage:    make(map[string]float64),
		containerPidsUsage:   make(map[string]float64),
		apiRequests:          make(map[string]int64),
		apiDuration:          make(map[string]time.Duration),
	}

	if enabled {
		mux := http.NewServeMux()
		mux.HandleFunc(path, c.handleMetrics)
		c.server = &http.Server{
			Addr:    addr + ":" + strconv.Itoa(port),
			Handler: mux,
		}
	}

	return c
}

func (c *Collector) handleMetrics(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	for img, count := range c.containersCreated {
		w.Write([]byte(`aviary_containers_created_total{image="` + img + `"}` + " " + strconv.FormatInt(count, 10) + "\n"))
	}
	for img, count := range c.containersRemoved {
		w.Write([]byte(`aviary_containers_removed_total{image="` + img + `"}` + " " + strconv.FormatInt(count, 10) + "\n"))
	}
	for img, count := range c.containersStarted {
		w.Write([]byte(`aviary_containers_started_total{image="` + img + `"}` + " " + strconv.FormatInt(count, 10) + "\n"))
	}
	for img, count := range c.containersStopped {
		w.Write([]byte(`aviary_containers_stopped_total{image="` + img + `"}` + " " + strconv.FormatInt(count, 10) + "\n"))
	}
	for key, val := range c.containerMemoryUsage {
		w.Write([]byte(`aviary_container_memory_usage_bytes{` + key + `}` + " " + strconv.FormatFloat(val, 'f', -1, 64) + "\n"))
	}
	for key, val := range c.containerCPUUsage {
		w.Write([]byte(`aviary_container_cpu_usage_percent{` + key + `}` + " " + strconv.FormatFloat(val, 'f', -1, 64) + "\n"))
	}
	for key, val := range c.containerPidsUsage {
		w.Write([]byte(`aviary_container_pids_usage{` + key + `}` + " " + strconv.FormatFloat(val, 'f', -1, 64) + "\n"))
	}
	for key, count := range c.apiRequests {
		w.Write([]byte(`aviary_api_requests_total{` + key + `}` + " " + strconv.FormatInt(count, 10) + "\n"))
	}
	for key, dur := range c.apiDuration {
		w.Write([]byte(`aviary_api_request_duration_seconds{` + key + `}` + " " + strconv.FormatFloat(dur.Seconds(), 'f', -1, 64) + "\n"))
	}
	w.Write([]byte(`aviary_active_websocket_connections ` + strconv.FormatInt(c.activeConnections, 10) + "\n"))
}

func (c *Collector) RecordContainerCreated(image string) {
	if !c.enabled {
		return
	}
	c.mu.Lock()
	c.containersCreated[image]++
	c.mu.Unlock()
}

func (c *Collector) RecordContainerRemoved(image string) {
	if !c.enabled {
		return
	}
	c.mu.Lock()
	c.containersRemoved[image]++
	c.mu.Unlock()
}

func (c *Collector) RecordContainerStarted(image string) {
	if !c.enabled {
		return
	}
	c.mu.Lock()
	c.containersStarted[image]++
	c.mu.Unlock()
}

func (c *Collector) RecordContainerStopped(image string) {
	if !c.enabled {
		return
	}
	c.mu.Lock()
	c.containersStopped[image]++
	c.mu.Unlock()
}

func (c *Collector) RecordMemoryUsage(containerID, containerName string, bytes float64) {
	if !c.enabled {
		return
	}
	key := `container_id="` + containerID + `",container_name="` + containerName + `"`
	c.mu.Lock()
	c.containerMemoryUsage[key] = bytes
	c.mu.Unlock()
}

func (c *Collector) RecordCPUUsage(containerID, containerName string, percent float64) {
	if !c.enabled {
		return
	}
	key := `container_id="` + containerID + `",container_name="` + containerName + `"`
	c.mu.Lock()
	c.containerCPUUsage[key] = percent
	c.mu.Unlock()
}

func (c *Collector) RecordPidsUsage(containerID, containerName string, pids float64) {
	if !c.enabled {
		return
	}
	key := `container_id="` + containerID + `",container_name="` + containerName + `"`
	c.mu.Lock()
	c.containerPidsUsage[key] = pids
	c.mu.Unlock()
}

func (c *Collector) RecordAPIRequest(method, path string, status int) {
	if !c.enabled {
		return
	}
	key := `method="` + method + `",path="` + path + `",status="` + strconv.Itoa(status) + `"`
	c.mu.Lock()
	c.apiRequests[key]++
	c.mu.Unlock()
}

func (c *Collector) RecordAPIDuration(method, path string, duration time.Duration) {
	if !c.enabled {
		return
	}
	key := `method="` + method + `",path="` + path + `"`
	c.mu.Lock()
	c.apiDuration[key] = duration
	c.mu.Unlock()
}

func (c *Collector) IncActiveConnections() {
	if !c.enabled {
		return
	}
	c.mu.Lock()
	c.activeConnections++
	c.mu.Unlock()
}

func (c *Collector) DecActiveConnections() {
	if !c.enabled {
		return
	}
	c.mu.Lock()
	c.activeConnections--
	c.mu.Unlock()
}

func (c *Collector) Start(ctx context.Context) error {
	if c.server == nil {
		return nil
	}
	go func() {
		<-ctx.Done()
		c.server.Shutdown(context.Background())
	}()
	return c.server.ListenAndServe()
}

func (c *Collector) Stop() error {
	if c.server == nil {
		return nil
	}
	return c.server.Close()
}