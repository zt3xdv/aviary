package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/aviary/agent/internal/daemon"
	"github.com/aviary/agent/internal/hostfs"
	"github.com/aviary/agent/pkg/docker"
	"github.com/docker/go-connections/nat"
)

var (
	ErrContainerNotFound = errors.New("container not found")
	ErrInvalidConfig     = errors.New("invalid container config")
	ErrOperationFailed   = errors.New("operation failed")
)

type Manager struct {
	client        *docker.Client
	hostFS        *hostfs.HostFS
	maxConcurrent int
	defaultMemMB  int
	defaultCPUCores float64
	defaultPids   int64
	defaultDiskMB int
	containerUID  int
	containerGID  int
	allowedMounts []string
}

func NewManager(client *docker.Client, maxConcurrent int, defaultMemMB int, defaultCPUCores float64, defaultPids int64, defaultDiskMB int, storageRoot string, containerUID, containerGID int, allowedMounts []string) *Manager {
	m := &Manager{
		client:         client,
		maxConcurrent:  maxConcurrent,
		defaultMemMB:   defaultMemMB,
		defaultCPUCores: defaultCPUCores,
		defaultPids:    defaultPids,
		defaultDiskMB:  defaultDiskMB,
		containerUID:   containerUID,
		containerGID:   containerGID,
		allowedMounts:  allowedMounts,
	}
	m.hostFS, _ = hostfs.NewHostFS(storageRoot)
	return m
}

// SetDaemonNetwork is deprecated and kept for compatibility
func (m *Manager) SetDaemonNetwork(networkName string) {
	// No-op: daemon client removed, using host FS and Docker APIs
}

type CreateRequest struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Cmd          []string          `json:"cmd,omitempty"`
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Env          []string          `json:"env,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	MemoryMB     int               `json:"memory_mb,omitempty"`
	CPUCores     float64           `json:"cpu_cores,omitempty"`
	Pids         int64             `json:"pids,omitempty"`
	DiskMB       int               `json:"disk_mb,omitempty"`
	Ports        map[string]string `json:"ports,omitempty"`
	Mounts       []MountRequest    `json:"mounts,omitempty"`
	Network      string            `json:"network,omitempty"`
	Restart      string            `json:"restart,omitempty"`
	ReadOnly     bool              `json:"read_only,omitempty"`
	User         string            `json:"user,omitempty"`
	WorkingDir   string            `json:"working_dir,omitempty"`
	InstallScript string           `json:"install_script,omitempty"`
}

type MountRequest struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	ReadOnly    bool   `json:"read_only,omitempty"`
	Propagation string `json:"propagation,omitempty"`
}

type ContainerResponse struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	State       string            `json:"state"`
	Status      string            `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   time.Time         `json:"started_at,omitempty"`
	FinishedAt  time.Time         `json:"finished_at,omitempty"`
	ExitCode    int               `json:"exit_code,omitempty"`
	Ports       map[string]string `json:"ports,omitempty"`
	Mounts      []MountResponse   `json:"mounts,omitempty"`
	Networks    map[string]string `json:"networks,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Resources   ResourceUsage     `json:"resources,omitempty"`
}

type MountResponse struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	ReadOnly    bool   `json:"read_only"`
	Propagation string `json:"propagation"`
}

type ResourceUsage struct {
	MemoryLimit int64   `json:"memory_limit,omitempty"`
	MemoryUsage int64   `json:"memory_usage,omitempty"`
	CPULimit    float64 `json:"cpu_limit,omitempty"`
	CPUUsage    float64 `json:"cpu_usage,omitempty"`
	PidsLimit   int64   `json:"pids_limit,omitempty"`
	PidsUsage   int64   `json:"pids_usage,omitempty"`
}

func (m *Manager) Create(ctx context.Context, req *CreateRequest) (*ContainerResponse, error) {
	if req.Image == "" {
		return nil, ErrInvalidConfig
	}

	memMB := req.MemoryMB
	if memMB == 0 {
		memMB = m.defaultMemMB
	}
	cpuCores := req.CPUCores
	if cpuCores == 0 {
		cpuCores = m.defaultCPUCores
	}
	pids := req.Pids
	if pids == 0 {
		pids = m.defaultPids
	}
	diskMB := req.DiskMB
	if diskMB == 0 {
		diskMB = m.defaultDiskMB
	}

	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}
	for containerPort, hostPort := range req.Ports {
		p, err := nat.NewPort("tcp", containerPort)
		if err != nil {
			return nil, err
		}
		exposedPorts[p] = struct{}{}
		portBindings[p] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: hostPort}}
	}

	// Always expose the daemon port (8080) internally
	daemonPort, _ := nat.NewPort("tcp", "8080")
	exposedPorts[daemonPort] = struct{}{}

	mounts := make([]docker.MountPoint, 0, len(req.Mounts)+1)

	// Always add the host volume bind mount at /home/container (Pterodactyl-style)
	volumePath := m.hostFS.VolumePath(req.Name)
	mounts = append(mounts, docker.MountPoint{
		Type:        "bind",
		Source:      volumePath,
		Target:      "/home/container",
		RW:          true,
		Propagation: "rprivate",
	})

	// Add user-specified mounts (validate against allowed mounts)
	for _, mnt := range req.Mounts {
		if !m.isAllowedMount(mnt.Source) {
			return nil, fmt.Errorf("mount source not allowed: %s", mnt.Source)
		}
		mounts = append(mounts, docker.MountPoint{
			Type:        mnt.Type,
			Source:      mnt.Source,
			Target:      mnt.Target,
			Mode:        mnt.Propagation,
			RW:          !mnt.ReadOnly,
			Propagation: mnt.Propagation,
		})
	}

	memBytes := int64(memMB) * 1024 * 1024

	diskBytes := int64(diskMB) * 1024 * 1024

	hostConfig := &docker.HostConfig{
		Memory:           memBytes,
		MemorySwap:       memBytes,
		NanoCPUs:         int64(cpuCores * 1e9),
		PidsLimit:        &pids,
		PortBindings:     portBindings,
		Mounts:           mounts,
		Privileged:       false,
		ReadonlyRootfs:   req.ReadOnly,
		AutoRemove:       false,
		SecurityOpt:      []string{"no-new-privileges:true"},
		CapDrop:          []string{"ALL"},
		CapAdd:           []string{},
	}

	if diskMB > 0 {
		hostConfig.StorageOpt = map[string]string{"size": fmt.Sprintf("%db", diskBytes)}
	}

	if req.Restart != "" {
		hostConfig.RestartPolicy = docker.RestartPolicy{Name: req.Restart}
	}

	networkConfig := &docker.NetworkingConfig{}
	if req.Network != "" {
		networkConfig.EndpointsConfig = map[string]*docker.EndpointSettings{
			req.Network: {},
		}
	}

	// Default to configured container UID/GID for security (non-root like Wings)
	user := req.User
	if user == "" {
		user = fmt.Sprintf("%d:%d", m.containerUID, m.containerGID)
	}

	// Use user's command directly as entrypoint (no daemon wrapper)
	entrypoint := req.Entrypoint
	cmd := req.Cmd
	if req.InstallScript != "" {
		script := req.InstallScript
		entrypoint = []string{"sh", "-c"}
		cmd = []string{script}
	}

	// Environment variables
	env := append([]string{}, req.Env...)
	env = append(env, "HOME=/home/container")

	// Add label to track volume name for file operations
	labels := req.Labels
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["aviary.volume"] = req.Name

	// Ensure volume exists and chown to container user
	volumePath = m.hostFS.VolumePath(req.Name)
	if err := m.hostFS.EnsureVolume(req.Name); err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}
	// Chown volume to container user so they can write
	if err := m.hostFS.Chown(ctx, req.Name, "", m.containerUID, m.containerGID); err != nil {
		return nil, fmt.Errorf("failed to chown volume: %w", err)
	}

	resp, err := m.client.CreateContainer(ctx, &docker.ContainerConfig{
		Image:        req.Image,
		Cmd:          cmd,
		Entrypoint:   entrypoint,
		Env:          env,
		Labels:       labels,
		ExposedPorts: exposedPorts,
		User:         user,
		WorkingDir:   req.WorkingDir,
	}, hostConfig, networkConfig, req.Name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOperationFailed, err)
	}

	return m.Inspect(ctx, resp.ID)
}

func (m *Manager) Inspect(ctx context.Context, id string) (*ContainerResponse, error) {
	inspect, err := m.client.InspectContainer(ctx, id)
	if err != nil {
		if strings.Contains(err.Error(), "No such container") {
			return nil, ErrContainerNotFound
		}
		return nil, err
	}

	return m.toResponse(inspect), nil
}

func (m *Manager) List(ctx context.Context, all bool) ([]*ContainerResponse, error) {
	containers, err := m.client.ListContainers(ctx, all)
	if err != nil {
		return nil, err
	}

	result := make([]*ContainerResponse, 0, len(containers))
	for _, c := range containers {
		resp := &ContainerResponse{
			ID:        c.ID[:12],
			Name:      strings.TrimPrefix(c.Names[0], "/"),
			Image:     c.Image,
			State:     c.State,
			Status:    c.Status,
			CreatedAt: time.Unix(c.Created, 0),
			Labels:    c.Labels,
			Ports:     make(map[string]string),
		}
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				resp.Ports[fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)] = fmt.Sprintf("%s:%d", p.IP, p.PublicPort)
			}
		}
		result = append(result, resp)
	}
	return result, nil
}

func (m *Manager) Start(ctx context.Context, id string) error {
	return m.client.StartContainer(ctx, id)
}

func (m *Manager) Stop(ctx context.Context, id string, timeout *time.Duration) error {
	return m.client.StopContainer(ctx, id, timeout)
}

func (m *Manager) Restart(ctx context.Context, id string, timeout *time.Duration) error {
	return m.client.RestartContainer(ctx, id, timeout)
}

func (m *Manager) Remove(ctx context.Context, id string, force bool) error {
	// Get volume name before removing container
	volumeName, err := m.getVolumeName(ctx, id)
	if err == nil && volumeName != "" {
		defer m.hostFS.RemoveVolume(volumeName)
	}
	// Remove container
	return m.client.RemoveContainer(ctx, id, force)
}

func (m *Manager) Kill(ctx context.Context, id, signal string) error {
	return m.client.KillContainer(ctx, id, signal)
}

func (m *Manager) Logs(ctx context.Context, id string, options docker.ContainerLogsOptions) (io.ReadCloser, error) {
	return m.client.ContainerLogs(ctx, id, options)
}

func (m *Manager) Stats(ctx context.Context, id string, stream bool) (docker.ContainerStatsReader, error) {
	return m.client.ContainerStats(ctx, id, stream)
}

func (m *Manager) ExecCreate(ctx context.Context, id string, config docker.ExecConfig) (docker.IDResponse, error) {
	return m.client.ExecCreate(ctx, id, config)
}

func (m *Manager) ExecAttach(ctx context.Context, id string, config docker.ExecStartCheck) (docker.HijackedResponse, error) {
	return m.client.ExecAttach(ctx, id, config)
}

func (m *Manager) ExecStart(ctx context.Context, id string, config docker.ExecStartCheck) error {
	return m.client.ExecStart(ctx, id, config)
}

func (m *Manager) ExecResize(ctx context.Context, id string, h, w int) error {
	return m.client.ExecResize(ctx, id, h, w)
}

func (m *Manager) ExecInspect(ctx context.Context, id string) (docker.ContainerExecInspect, error) {
	return m.client.ExecInspect(ctx, id)
}

func (m *Manager) toResponse(inspect *docker.ContainerJSON) *ContainerResponse {
	resp := &ContainerResponse{
		ID:        inspect.ID[:12],
		Name:      strings.TrimPrefix(inspect.Name, "/"),
		Image:     inspect.Config.Image,
		State:     inspect.State.Status,
		Status:    inspect.State.Status,
		CreatedAt: parseTime(inspect.Created),
		Labels:    inspect.Config.Labels,
		Ports:     make(map[string]string),
		Mounts:    make([]MountResponse, 0),
		Networks:  make(map[string]string),
	}

	if inspect.State.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil {
			resp.StartedAt = t
		}
	}
	if inspect.State.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, inspect.State.FinishedAt); err == nil {
			resp.FinishedAt = t
		}
	}
	resp.ExitCode = inspect.State.ExitCode

	for port, bindings := range inspect.NetworkSettings.Ports {
		if len(bindings) > 0 {
			resp.Ports[string(port)] = fmt.Sprintf("%s:%s", bindings[0].HostIP, bindings[0].HostPort)
		}
	}

	for _, m := range inspect.Mounts {
		resp.Mounts = append(resp.Mounts, MountResponse{
			Type:        string(m.Type),
			Source:      m.Source,
			Target:      m.Destination,
			ReadOnly:    m.RW,
			Propagation: string(m.Propagation),
		})
	}

	for name, net := range inspect.NetworkSettings.Networks {
		if net != nil {
			resp.Networks[name] = net.IPAddress
		}
	}

	if inspect.HostConfig != nil {
		resp.Resources.MemoryLimit = inspect.HostConfig.Memory
		resp.Resources.CPULimit = float64(inspect.HostConfig.NanoCPUs) / 1e9
		if inspect.HostConfig.PidsLimit != nil {
			resp.Resources.PidsLimit = *inspect.HostConfig.PidsLimit
		}
	}

	return resp
}

func parseTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

func (m *Manager) PullImage(ctx context.Context, ref string) (io.ReadCloser, error) {
	return m.client.PullImage(ctx, ref, docker.PullOptions{})
}

func (m *Manager) ListImages(ctx context.Context) ([]docker.ImageSummary, error) {
	return m.client.ListImages(ctx, docker.ListOptions{})
}

func (m *Manager) RemoveImage(ctx context.Context, id string) ([]docker.ImageDeleteResponse, error) {
	return m.client.RemoveImage(ctx, id, docker.RemoveOptions{})
}

func (m *Manager) InspectImage(ctx context.Context, id string) (docker.ImageInspectResponse, error) {
	return m.client.InspectImage(ctx, id)
}

func (m *Manager) Prune(ctx context.Context) (map[string]interface{}, error) {
	containers, _ := m.client.PruneContainers(ctx)
	images, _ := m.client.PruneImages(ctx)
	networks, _ := m.client.PruneNetworks(ctx)
	volumes, _ := m.client.PruneVolumes(ctx)
	return map[string]interface{}{
		"containers": containers,
		"images":     images,
		"networks":   networks,
		"volumes":    volumes,
	}, nil
}

func (m *Manager) SystemInfo(ctx context.Context) (json.RawMessage, error) {
	return m.client.SystemInfo(ctx)
}

func (m *Manager) SystemVersion(ctx context.Context) (docker.Version, error) {
	return m.client.SystemVersion(ctx)
}

func (m *Manager) SystemDF(ctx context.Context) (docker.DiskUsage, error) {
	return m.client.SystemDF(ctx)
}

// FileList lists files in a directory
func (m *Manager) FileList(ctx context.Context, containerID, path string) ([]daemon.FileEntry, error) {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return nil, err
	}
	entries, err := m.hostFS.List(ctx, volumeName, path)
	if err != nil {
		return nil, err
	}
	// Convert hostfs.FileEntry to daemon.FileEntry
	result := make([]daemon.FileEntry, len(entries))
	for i, e := range entries {
		result[i] = daemon.FileEntry(e)
	}
	return result, nil
}

// FileStat returns file metadata
func (m *Manager) FileStat(ctx context.Context, containerID, path string) (*daemon.FileEntry, error) {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return nil, err
	}
	entry, err := m.hostFS.Stat(ctx, volumeName, path)
	if err != nil {
		return nil, err
	}
	return (*daemon.FileEntry)(entry), nil
}

// FileRead reads a file content
func (m *Manager) FileRead(ctx context.Context, containerID, path string) (io.ReadCloser, error) {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return nil, err
	}
	return m.hostFS.Read(ctx, volumeName, path)
}

// FileWrite writes a file
func (m *Manager) FileWrite(ctx context.Context, containerID, path string, content io.Reader, size int64) error {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return err
	}
	return m.hostFS.Write(ctx, volumeName, path, content, size)
}

// FileDelete deletes a file or directory
func (m *Manager) FileDelete(ctx context.Context, containerID, path string) error {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return err
	}
	return m.hostFS.Delete(ctx, volumeName, path)
}

// FileCreateDir creates a directory
func (m *Manager) FileCreateDir(ctx context.Context, containerID, path string, mode uint32) error {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return err
	}
	return m.hostFS.CreateDir(ctx, volumeName, path, mode)
}

// FileChmod changes file permissions
func (m *Manager) FileChmod(ctx context.Context, containerID, path string, mode uint32) error {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return err
	}
	return m.hostFS.Chmod(ctx, volumeName, path, mode)
}

// FileMove moves/renames a file
func (m *Manager) FileMove(ctx context.Context, containerID, srcPath, destPath string) error {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return err
	}
	return m.hostFS.Move(ctx, volumeName, srcPath, destPath)
}

// FileCopy copies a file
func (m *Manager) FileCopy(ctx context.Context, containerID, srcPath, destPath string) error {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return err
	}
	return m.hostFS.Copy(ctx, volumeName, srcPath, destPath)
}

// FileUpdate updates a file (same as write but for text editor semantics)
func (m *Manager) FileUpdate(ctx context.Context, containerID, path string, content io.Reader, size int64) error {
	volumeName, err := m.getVolumeName(ctx, containerID)
	if err != nil {
		return err
	}
	return m.hostFS.Update(ctx, volumeName, path, content, size)
}

// getVolumeName resolves the volume name from container ID by inspecting labels
func (m *Manager) getVolumeName(ctx context.Context, containerID string) (string, error) {
	inspect, err := m.client.InspectContainer(ctx, containerID)
	if err != nil {
		return "", err
	}
	if inspect.Config == nil || inspect.Config.Labels == nil {
		return "", fmt.Errorf("container has no labels")
	}
	volumeName, ok := inspect.Config.Labels["aviary.volume"]
	if !ok || volumeName == "" {
		return "", fmt.Errorf("container missing aviary.volume label")
	}
	return volumeName, nil
}

// isAllowedMount checks if a mount source is in the allowed mounts list
// Similar to Wings' resolveMountPath and isAllowedMountSource
func (m *Manager) isAllowedMount(source string) bool {
	if len(m.allowedMounts) == 0 {
		return true // No restrictions if not configured
	}
	// Resolve symlinks for accurate comparison
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		// If source doesn't exist yet, use the original path
		resolved = source
	}
	for _, allowed := range m.allowedMounts {
		allowedResolved, err := filepath.EvalSymlinks(allowed)
		if err != nil {
			allowedResolved = allowed
		}
		// Check if resolved source is allowedResolved or a subdirectory
		if resolved == allowedResolved || strings.HasPrefix(resolved, allowedResolved+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ProcessInfo returns process status
func (m *Manager) ProcessInfo(ctx context.Context, containerID string) (*daemon.ProcessInfo, error) {
	inspect, err := m.client.InspectContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}

	state := inspect.State
	return &daemon.ProcessInfo{
		PID:       state.Pid,
		State:     state.Status,
		ExitCode:  state.ExitCode,
		StartedAt: state.StartedAt,
	}, nil
}

// ProcessStart starts the container (application process)
func (m *Manager) ProcessStart(ctx context.Context, containerID string) error {
	return m.client.StartContainer(ctx, containerID)
}

// ProcessStop stops the container (application process)
func (m *Manager) ProcessStop(ctx context.Context, containerID string, timeout *time.Duration) error {
	return m.client.StopContainer(ctx, containerID, timeout)
}

// ProcessRestart restarts the container (application process)
func (m *Manager) ProcessRestart(ctx context.Context, containerID string, timeout *time.Duration) error {
	return m.client.RestartContainer(ctx, containerID, timeout)
}

// ProcessSignal sends a signal to the container's main process
func (m *Manager) ProcessSignal(ctx context.Context, containerID, signal string) error {
	return m.client.KillContainer(ctx, containerID, signal)
}