package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/docker/go-connections/nat"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
}

func NewClient(host, version string, timeout time.Duration) (*Client, error) {
	var baseURL string
	var dialer func(ctx context.Context, network, addr string) (net.Conn, error)

	if strings.HasPrefix(host, "unix://") {
		socketPath := strings.TrimPrefix(host, "unix://")
		baseURL = "http://unix"
		dialer = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}
	} else if strings.HasPrefix(host, "tcp://") {
		baseURL = strings.TrimPrefix(host, "tcp://")
		if !strings.Contains(baseURL, "://") {
			baseURL = "http://" + baseURL
		}
	} else {
		baseURL = "http://" + host
	}

	transport := &http.Transport{
		DialContext: dialer,
		TLSClientConfig: nil,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return &Client{
		httpClient: httpClient,
		baseURL:    baseURL,
	}, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, v interface{}) error {
	var buf io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker API error: %s - %s", resp.Status, string(bodyBytes))
	}

	if v != nil {
		return json.NewDecoder(resp.Body).Decode(v)
	}
	return nil
}

func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.doRequest(ctx, "GET", "/_ping", nil, nil)
}

func (c *Client) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	query := ""
	if all {
		query = "?all=1"
	}
	var containers []Container
	err := c.doRequest(ctx, "GET", "/containers/json"+query, nil, &containers)
	return containers, err
}

func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerJSON, error) {
	var container ContainerJSON
	err := c.doRequest(ctx, "GET", "/containers/"+id+"/json", nil, &container)
	return &container, err
}

func (c *Client) CreateContainer(ctx context.Context, cfg *ContainerConfig, hostCfg *HostConfig, networkingCfg *NetworkingConfig, name string) (ContainerCreateResponse, error) {
	query := ""
	if name != "" {
		query = "?name=" + url.QueryEscape(name)
	}
	var resp ContainerCreateResponse
	
	// Merge config fields at top level (newer Docker API format)
	requestBody := map[string]interface{}{
		"HostConfig":       hostCfg,
		"NetworkingConfig": networkingCfg,
	}
	// Add all non-nil fields from cfg
	if cfg != nil {
		if cfg.Image != "" {
			requestBody["Image"] = cfg.Image
		}
		if cfg.Cmd != nil {
			requestBody["Cmd"] = cfg.Cmd
		}
		if cfg.Entrypoint != nil {
			requestBody["Entrypoint"] = cfg.Entrypoint
		}
		if cfg.Env != nil {
			requestBody["Env"] = cfg.Env
		}
		if cfg.Labels != nil {
			requestBody["Labels"] = cfg.Labels
		}
		if cfg.ExposedPorts != nil {
			requestBody["ExposedPorts"] = cfg.ExposedPorts
		}
		if cfg.User != "" {
			requestBody["User"] = cfg.User
		}
		if cfg.WorkingDir != "" {
			requestBody["WorkingDir"] = cfg.WorkingDir
		}
		if cfg.Hostname != "" {
			requestBody["Hostname"] = cfg.Hostname
		}
		if cfg.Domainname != "" {
			requestBody["Domainname"] = cfg.Domainname
		}
		if cfg.AttachStdin {
			requestBody["AttachStdin"] = cfg.AttachStdin
		}
		if cfg.AttachStdout {
			requestBody["AttachStdout"] = cfg.AttachStdout
		}
		if cfg.AttachStderr {
			requestBody["AttachStderr"] = cfg.AttachStderr
		}
		if cfg.Tty {
			requestBody["Tty"] = cfg.Tty
		}
		if cfg.OpenStdin {
			requestBody["OpenStdin"] = cfg.OpenStdin
		}
		if cfg.StdinOnce {
			requestBody["StdinOnce"] = cfg.StdinOnce
		}
		if cfg.Volumes != nil {
			requestBody["Volumes"] = cfg.Volumes
		}
		if cfg.NetworkDisabled {
			requestBody["NetworkDisabled"] = cfg.NetworkDisabled
		}
		if cfg.MacAddress != "" {
			requestBody["MacAddress"] = cfg.MacAddress
		}
		if cfg.OnBuild != nil {
			requestBody["OnBuild"] = cfg.OnBuild
		}
		if cfg.StopSignal != "" {
			requestBody["StopSignal"] = cfg.StopSignal
		}
		if cfg.StopTimeout != 0 {
			requestBody["StopTimeout"] = cfg.StopTimeout
		}
		if cfg.Shell != nil {
			requestBody["Shell"] = cfg.Shell
		}
	}
	
	err := c.doRequest(ctx, "POST", "/containers/create"+query, requestBody, &resp)
	return resp, err
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.doRequest(ctx, "POST", "/containers/"+id+"/start", nil, nil)
}

func (c *Client) StopContainer(ctx context.Context, id string, timeout *time.Duration) error {
	query := ""
	if timeout != nil {
		query = "?t=" + fmt.Sprintf("%d", int(timeout.Seconds()))
	}
	return c.doRequest(ctx, "POST", "/containers/"+id+"/stop"+query, nil, nil)
}

func (c *Client) RestartContainer(ctx context.Context, id string, timeout *time.Duration) error {
	query := ""
	if timeout != nil {
		query = "?t=" + fmt.Sprintf("%d", int(timeout.Seconds()))
	}
	return c.doRequest(ctx, "POST", "/containers/"+id+"/restart"+query, nil, nil)
}

func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	query := ""
	if force {
		query = "?force=1"
	}
	return c.doRequest(ctx, "DELETE", "/containers/"+id+query, nil, nil)
}

func (c *Client) KillContainer(ctx context.Context, id, signal string) error {
	query := "?signal=" + url.QueryEscape(signal)
	return c.doRequest(ctx, "POST", "/containers/"+id+"/kill"+query, nil, nil)
}

func (c *Client) ContainerLogs(ctx context.Context, id string, options ContainerLogsOptions) (io.ReadCloser, error) {
	query := "?stdout=1&stderr=1"
	if options.Follow {
		query += "&follow=1"
	}
	if options.Timestamps {
		query += "&timestamps=1"
	}
	if options.Tail != "" {
		query += "&tail=" + url.QueryEscape(options.Tail)
	}
	if options.Since != "" {
		query += "&since=" + url.QueryEscape(options.Since)
	}
	if options.Until != "" {
		query += "&until=" + url.QueryEscape(options.Until)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/containers/"+id+"/logs"+query, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("docker API error: %s - %s", resp.Status, string(bodyBytes))
	}

	return resp.Body, nil
}

func (c *Client) ContainerStats(ctx context.Context, id string, stream bool) (ContainerStatsReader, error) {
	query := ""
	if !stream {
		query = "?stream=false"
	}
	
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/containers/"+id+"/stats"+query, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("docker API error: %s - %s", resp.Status, string(bodyBytes))
	}

	return &statsReader{resp: resp}, nil
}

func (c *Client) ExecCreate(ctx context.Context, id string, config ExecConfig) (IDResponse, error) {
	var resp IDResponse
	err := c.doRequest(ctx, "POST", "/containers/"+id+"/exec", config, &resp)
	return resp, err
}

func (c *Client) ExecAttach(ctx context.Context, id string, config ExecStartCheck) (HijackedResponse, error) {
	path := "/exec/" + id + "/start"
	query := fmt.Sprintf("?detach=%v&tty=%v", config.Detach, config.Tty)
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path+query, nil)
	if err != nil {
		return HijackedResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return HijackedResponse{}, err
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return HijackedResponse{}, fmt.Errorf("docker API error: %s - %s", resp.Status, string(bodyBytes))
	}

	hj, ok := resp.Body.(interface {
		Hijack() (net.Conn, *bufio.Reader, error)
	})
	if !ok {
		resp.Body.Close()
		return HijackedResponse{}, fmt.Errorf("response body does not support hijacking")
	}

	conn, reader, err := hj.Hijack()
	if err != nil {
		resp.Body.Close()
		return HijackedResponse{}, err
	}

	return HijackedResponse{
		Conn:   conn,
		Reader: reader,
	}, nil
}

func (c *Client) ExecStart(ctx context.Context, id string, config ExecStartCheck) error {
	return c.doRequest(ctx, "POST", "/exec/"+id+"/start", config, nil)
}

func (c *Client) ExecResize(ctx context.Context, id string, h, w int) error {
	query := fmt.Sprintf("?h=%d&w=%d", h, w)
	return c.doRequest(ctx, "POST", "/exec/"+id+"/resize"+query, nil, nil)
}

func (c *Client) ExecInspect(ctx context.Context, id string) (ContainerExecInspect, error) {
	var inspect ContainerExecInspect
	err := c.doRequest(ctx, "GET", "/exec/"+id+"/json", nil, &inspect)
	return inspect, err
}

func (c *Client) PullImage(ctx context.Context, ref string, options PullOptions) (io.ReadCloser, error) {
	query := "?fromImage=" + url.QueryEscape(ref)
	if options.RegistryAuth != "" {
		query += "&registryAuth=" + url.QueryEscape(options.RegistryAuth)
	}
	
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/images/create"+query, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("docker API error: %s - %s", resp.Status, string(bodyBytes))
	}

	return resp.Body, nil
}

func (c *Client) ListImages(ctx context.Context, options ListOptions) ([]ImageSummary, error) {
	query := ""
	if options.All {
		query = "?all=1"
	}
	var images []ImageSummary
	err := c.doRequest(ctx, "GET", "/images/json"+query, nil, &images)
	return images, err
}

func (c *Client) RemoveImage(ctx context.Context, id string, options RemoveOptions) ([]ImageDeleteResponse, error) {
	query := ""
	if options.Force {
		query = "?force=1"
	}
	if options.PruneChildren {
		if query == "" {
			query = "?"
		} else {
			query += "&"
		}
		query += "prune=1"
	}
	var resp []ImageDeleteResponse
	err := c.doRequest(ctx, "DELETE", "/images/"+id+query, nil, &resp)
	return resp, err
}

func (c *Client) InspectImage(ctx context.Context, id string) (ImageInspectResponse, error) {
	var resp ImageInspectResponse
	err := c.doRequest(ctx, "GET", "/images/"+id+"/json", nil, &resp)
	return resp, err
}

func (c *Client) SystemInfo(ctx context.Context) (json.RawMessage, error) {
	var info json.RawMessage
	err := c.doRequest(ctx, "GET", "/info", nil, &info)
	return info, err
}

func (c *Client) SystemVersion(ctx context.Context) (Version, error) {
	var version Version
	err := c.doRequest(ctx, "GET", "/version", nil, &version)
	return version, err
}

func (c *Client) SystemDF(ctx context.Context) (DiskUsage, error) {
	var usage DiskUsage
	err := c.doRequest(ctx, "GET", "/system/df", nil, &usage)
	return usage, err
}

func (c *Client) PruneContainers(ctx context.Context) (ContainersPruneReport, error) {
	var report ContainersPruneReport
	err := c.doRequest(ctx, "POST", "/containers/prune", nil, &report)
	return report, err
}

func (c *Client) PruneImages(ctx context.Context) (ImagesPruneReport, error) {
	var report ImagesPruneReport
	err := c.doRequest(ctx, "POST", "/images/prune", nil, &report)
	return report, err
}

func (c *Client) PruneNetworks(ctx context.Context) (NetworksPruneReport, error) {
	var report NetworksPruneReport
	err := c.doRequest(ctx, "POST", "/networks/prune", nil, &report)
	return report, err
}

func (c *Client) PruneVolumes(ctx context.Context) (VolumesPruneReport, error) {
	var report VolumesPruneReport
	err := c.doRequest(ctx, "POST", "/volumes/prune", nil, &report)
	return report, err
}

func (c *Client) ContainerExec(ctx context.Context, containerID string, cmd []string) error {
	execConfig := ExecConfig{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	resp, err := c.ExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return err
	}
	return c.ExecStart(ctx, resp.ID, ExecStartCheck{Detach: false, Tty: false})
}

func (c *Client) ContainerArchive(ctx context.Context, containerID, path string) (io.ReadCloser, error) {
	query := "?path=" + url.QueryEscape(path)
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/containers/"+containerID+"/archive"+query, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("docker API error: %s - %s", resp.Status, string(bodyBytes))
	}

	return resp.Body, nil
}

func (c *Client) ContainerPutArchive(ctx context.Context, containerID, path string, archive io.Reader) error {
	query := "?path=" + url.QueryEscape(path)
	req, err := http.NewRequestWithContext(ctx, "PUT", c.baseURL+"/containers/"+containerID+"/archive"+query, archive)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker API error: %s - %s", resp.Status, string(bodyBytes))
	}

	return nil
}

// Types matching Docker API

type Container struct {
	ID         string            `json:"Id"`
	Names      []string          `json:"Names"`
	Image      string            `json:"Image"`
	ImageID    string            `json:"ImageID"`
	Command    string            `json:"Command"`
	Created    int64             `json:"Created"`
	State      string            `json:"State"`
	Status     string            `json:"Status"`
	Ports      []Port            `json:"Ports"`
	Labels     map[string]string `json:"Labels"`
	SizeRw     int64             `json:"SizeRw"`
	SizeRootFs int64             `json:"SizeRootFs"`
}

type Port struct {
	IP          string `json:"IP"`
	PrivatePort uint16 `json:"PrivatePort"`
	PublicPort  uint16 `json:"PublicPort"`
	Type        string `json:"Type"`
}

type ContainerJSON struct {
	ID              string                 `json:"Id"`
	Created         string                 `json:"Created"`
	Path            string                 `json:"Path"`
	Args            []string               `json:"Args"`
	State           ContainerState         `json:"State"`
	Image           string                 `json:"Image"`
	ResolvConfPath  string                 `json:"ResolvConfPath"`
	HostnamePath    string                 `json:"HostnamePath"`
	HostsPath       string                 `json:"HostsPath"`
	LogPath         string                 `json:"LogPath"`
	Name            string                 `json:"Name"`
	RestartCount    int                    `json:"RestartCount"`
	Driver          string                 `json:"Driver"`
	Platform        string                 `json:"Platform"`
	MountLabel      string                 `json:"MountLabel"`
	ProcessLabel    string                 `json:"ProcessLabel"`
	AppArmorProfile string                 `json:"AppArmorProfile"`
	ExecIDs         []string               `json:"ExecIDs"`
	HostConfig      *HostConfig            `json:"HostConfig"`
	GraphDriver     GraphDriverData        `json:"GraphDriver"`
	Mounts          []MountPoint           `json:"Mounts"`
	Config          *ContainerConfig       `json:"Config"`
	NetworkSettings NetworkSettings        `json:"NetworkSettings"`
}

type ContainerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	Paused     bool   `json:"Paused"`
	Restarting bool   `json:"Restarting"`
	OOMKilled  bool   `json:"OOMKilled"`
	Dead       bool   `json:"Dead"`
	Pid        int    `json:"Pid"`
	ExitCode   int    `json:"ExitCode"`
	Error      string `json:"Error"`
	StartedAt  string `json:"StartedAt"`
	FinishedAt string `json:"FinishedAt"`
}

type HostConfig struct {
	Memory            int64              `json:"Memory"`
	NanoCPUs          int64              `json:"NanoCpus"`
	PidsLimit         *int64             `json:"PidsLimit"`
	PortBindings      nat.PortMap        `json:"PortBindings"`
	Mounts            []MountPoint       `json:"Mounts"`
	Privileged        bool               `json:"Privileged"`
	ReadonlyRootfs    bool               `json:"ReadonlyRootfs"`
	AutoRemove        bool               `json:"AutoRemove"`
	RestartPolicy     RestartPolicy      `json:"RestartPolicy"`
	NetworkMode       string             `json:"NetworkMode"`
	Devices           []DeviceMapping    `json:"Devices"`
	Tmpfs             map[string]string  `json:"Tmpfs"`
	DNS               []string           `json:"Dns"`
	DNSSearch         []string           `json:"DnsSearch"`
	ExtraHosts        []string           `json:"ExtraHosts"`
	VolumesFrom       []string           `json:"VolumesFrom"`
	CapAdd            []string           `json:"CapAdd"`
	CapDrop           []string           `json:"CapDrop"`
	GroupAdd          []string           `json:"GroupAdd"`
	IpcMode           string             `json:"IpcMode"`
	CgroupnsMode      string             `json:"CgroupnsMode"`
	UTSMode           string             `json:"UTSMode"`
	UsernsMode        string             `json:"UsernsMode"`
	ShmSize           int64              `json:"ShmSize"`
	Sysctls           map[string]string  `json:"Sysctls"`
	Runtime           string             `json:"Runtime"`
	ConsoleSize       []int              `json:"ConsoleSize"`
	Isolation         string             `json:"Isolation"`
	CpuShares         int64              `json:"CpuShares"`
	CpuPeriod         int64              `json:"CpuPeriod"`
	CpuQuota          int64              `json:"CpuQuota"`
	CpuRealtimePeriod int64              `json:"CpuRealtimePeriod"`
	CpuRealtimeRuntime int64             `json:"CpuRealtimeRuntime"`
	CpusetCpus        string             `json:"CpusetCpus"`
	CpusetMems        string             `json:"CpusetMems"`
	BlkioWeight       uint16             `json:"BlkioWeight"`
	BlkioWeightDevice []WeightDevice     `json:"BlkioWeightDevice"`
	BlkioDeviceReadBps []ThrottleDevice  `json:"BlkioDeviceReadBps"`
	BlkioDeviceWriteBps []ThrottleDevice `json:"BlkioDeviceWriteBps"`
	BlkioDeviceReadIOps []ThrottleDevice `json:"BlkioDeviceReadIOps"`
	BlkioDeviceWriteIOps []ThrottleDevice `json:"BlkioDeviceWriteIOps"`
	MemorySwap        int64              `json:"MemorySwap"`
	MemorySwappiness  *int64             `json:"MemorySwappiness"`
	OomKillDisable    bool               `json:"OomKillDisable"`
	OomScoreAdj       int                `json:"OomScoreAdj"`
	Init              bool               `json:"Init"`
	StorageOpt        map[string]string  `json:"StorageOpt"`
	SecurityOpt       []string           `json:"SecurityOpt"`
}

type RestartPolicy struct {
	Name              string `json:"Name"`
	MaximumRetryCount int    `json:"MaximumRetryCount"`
}

type MountPoint struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Target      string `json:"Target"`
	Destination string `json:"Destination,omitempty"` // deprecated, use Target
	Driver      string `json:"Driver"`
	Mode        string `json:"Mode"`
	RW          bool   `json:"RW"`
	Propagation string `json:"Propagation"`
}

type GraphDriverData struct {
	Name string            `json:"Name"`
	Data map[string]string `json:"Data"`
}

type DeviceMapping struct {
	PathOnHost        string `json:"PathOnHost"`
	PathInContainer   string `json:"PathInContainer"`
	CgroupPermissions string `json:"CgroupPermissions"`
}

type WeightDevice struct {
	Path   string `json:"Path"`
	Weight uint16 `json:"Weight"`
}

type ThrottleDevice struct {
	Path string `json:"Path"`
	Rate uint64 `json:"Rate"`
}

type ContainerConfig struct {
	Hostname        string              `json:"Hostname"`
	Domainname      string              `json:"Domainname"`
	User            string              `json:"User"`
	AttachStdin     bool                `json:"AttachStdin"`
	AttachStdout    bool                `json:"AttachStdout"`
	AttachStderr    bool                `json:"AttachStderr"`
	ExposedPorts    nat.PortSet         `json:"ExposedPorts"`
	Tty             bool                `json:"Tty"`
	OpenStdin       bool                `json:"OpenStdin"`
	StdinOnce       bool                `json:"StdinOnce"`
	Env             []string            `json:"Env"`
	Cmd             []string            `json:"Cmd"`
	Image           string              `json:"Image"`
	Volumes         map[string]struct{} `json:"Volumes"`
	WorkingDir      string              `json:"WorkingDir"`
	Entrypoint      []string            `json:"Entrypoint"`
	NetworkDisabled bool                `json:"NetworkDisabled"`
	MacAddress      string              `json:"MacAddress"`
	OnBuild         []string            `json:"OnBuild"`
	Labels          map[string]string   `json:"Labels"`
	StopSignal      string              `json:"StopSignal"`
	StopTimeout     int                 `json:"StopTimeout"`
	Shell           []string            `json:"Shell"`
}

type NetworkingConfig struct {
	EndpointsConfig map[string]*EndpointSettings `json:"EndpointsConfig"`
}

type EndpointSettings struct {
	IPAMConfig        *EndpointIPAMConfig `json:"IPAMConfig"`
	Links             []string            `json:"Links"`
	Aliases           []string            `json:"Aliases"`
	NetworkID         string              `json:"NetworkID"`
	EndpointID        string              `json:"EndpointID"`
	Gateway           string              `json:"Gateway"`
	IPAddress         string              `json:"IPAddress"`
	IPPrefixLen       int                 `json:"IPPrefixLen"`
	IPv6Gateway       string              `json:"IPv6Gateway"`
	GlobalIPv6Address string              `json:"GlobalIPv6Address"`
	GlobalIPv6PrefixLen int               `json:"GlobalIPv6PrefixLen"`
	MacAddress        string              `json:"MacAddress"`
	DriverOpts        map[string]string   `json:"DriverOpts"`
}

type EndpointIPAMConfig struct {
	IPv4Address  string `json:"IPv4Address"`
	IPv6Address  string `json:"IPv6Address"`
	LinkLocalIPs []string `json:"LinkLocalIPs"`
}

type NetworkSettings struct {
	Bridge                 string                     `json:"Bridge"`
	SandboxID              string                     `json:"SandboxID"`
	HairpinMode            bool                       `json:"HairpinMode"`
	LinkLocalIPv6Address   string                     `json:"LinkLocalIPv6Address"`
	LinkLocalIPv6PrefixLen int                        `json:"LinkLocalIPv6PrefixLen"`
	Ports                  nat.PortMap                `json:"Ports"`
	SandboxKey             string                     `json:"SandboxKey"`
	SecondaryIPAddresses   []string                   `json:"SecondaryIPAddresses"`
	SecondaryIPv6Addresses []string                   `json:"SecondaryIPv6Addresses"`
	EndpointSettings       map[string]*EndpointSettings `json:"EndpointSettings"`
	Gateway                string                     `json:"Gateway"`
	GlobalIPv6Address      string                     `json:"GlobalIPv6Address"`
	GlobalIPv6PrefixLen    int                        `json:"GlobalIPv6PrefixLen"`
	IPAddress              string                     `json:"IPAddress"`
	IPPrefixLen            int                        `json:"IPPrefixLen"`
	IPv6Gateway            string                     `json:"IPv6Gateway"`
	MacAddress             string                     `json:"MacAddress"`
	Networks               map[string]*EndpointSettings `json:"Networks"`
}

type ContainerCreateRequest struct {
	Config           *ContainerConfig    `json:"Config"`
	HostConfig       *HostConfig         `json:"HostConfig"`
	NetworkingConfig *NetworkingConfig   `json:"NetworkingConfig"`
}

type ContainerCreateResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

type ContainerLogsOptions struct {
	Follow     bool
	Timestamps bool
	Tail       string
	Since      string
	Until      string
}

type ContainerStatsReader interface {
	io.ReadCloser
	ReadStats() (*ContainerStats, error)
}

type statsReader struct {
	resp *http.Response
}

func (r *statsReader) Read(p []byte) (n int, err error) {
	return r.resp.Body.Read(p)
}

func (r *statsReader) Close() error {
	return r.resp.Body.Close()
}

func (r *statsReader) ReadStats() (*ContainerStats, error) {
	var stats ContainerStats
	decoder := json.NewDecoder(r.resp.Body)
	err := decoder.Decode(&stats)
	return &stats, err
}

type ContainerStats struct {
	Read         string             `json:"read"`
	PreRead      string             `json:"preread"`
	PidsStats    PidsStats          `json:"pids_stats"`
	BlkioStats   BlkioStats         `json:"blkio_stats"`
	NumProcs     uint32             `json:"num_procs"`
	StorageStats StorageStats       `json:"storage_stats"`
	CPUStats     CPUStats           `json:"cpu_stats"`
	PrecpuStats  CPUStats           `json:"precpu_stats"`
	MemoryStats  MemoryStats        `json:"memory_stats"`
	Networks     map[string]NetworkStats `json:"networks"`
}

type PidsStats struct {
	Current uint64 `json:"current"`
	Limit   uint64 `json:"limit"`
}

type BlkioStats struct {
	IOServiceBytesRecursive []BlkioStatEntry `json:"io_service_bytes_recursive"`
	IOServicedRecursive     []BlkioStatEntry `json:"io_serviced_recursive"`
	IOQueueRecursive        []BlkioStatEntry `json:"io_queue_recursive"`
	IOServiceTimeRecursive  []BlkioStatEntry `json:"io_service_time_recursive"`
	IOWaitTimeRecursive     []BlkioStatEntry `json:"io_wait_time_recursive"`
	IOMergedRecursive       []BlkioStatEntry `json:"io_merged_recursive"`
	IOTimeRecursive         []BlkioStatEntry `json:"io_time_recursive"`
	SectorsRecursive        []BlkioStatEntry `json:"sectors_recursive"`
}

type BlkioStatEntry struct {
	Major uint64 `json:"major"`
	Minor uint64 `json:"minor"`
	Op    string `json:"op"`
	Value uint64 `json:"value"`
}

type StorageStats struct {
}

type CPUStats struct {
	CPUUsage       CPUUsage `json:"cpu_usage"`
	SystemCPUUsage uint64   `json:"system_cpu_usage"`
	OnlineCPUs     uint32   `json:"online_cpus"`
	ThrottlingData ThrottlingData `json:"throttling_data"`
}

type CPUUsage struct {
	TotalUsage        uint64   `json:"total_usage"`
	UsageInUsermode   uint64   `json:"usage_in_usermode"`
	UsageInKernelmode uint64   `json:"usage_in_kernelmode"`
	PercpuUsage       []uint64 `json:"percpu_usage"`
}

type ThrottlingData struct {
	Periods          uint64 `json:"periods"`
	ThrottledPeriods uint64 `json:"throttled_periods"`
	ThrottledTime    uint64 `json:"throttled_time"`
}

type MemoryStats struct {
	Usage    uint64 `json:"usage"`
	MaxUsage uint64 `json:"max_usage"`
	Limit    uint64 `json:"limit"`
	Stats    MemoryStatsDetail `json:"stats"`
}

type MemoryStatsDetail struct {
	ActiveAnon   uint64 `json:"active_anon"`
	ActiveFile   uint64 `json:"active_file"`
	Cache        uint64 `json:"cache"`
	Dirty        uint64 `json:"dirty"`
	InactiveAnon uint64 `json:"inactive_anon"`
	InactiveFile uint64 `json:"inactive_file"`
	MappedFile   uint64 `json:"mapped_file"`
	Pgfault      uint64 `json:"pgfault"`
	Pgmajfault   uint64 `json:"pgmajfault"`
	Pgpgin       uint64 `json:"pgpgin"`
	Pgpgout      uint64 `json:"pgpgout"`
	Rss          uint64 `json:"rss"`
	RssHuge      uint64 `json:"rss_huge"`
	TotalActiveAnon uint64 `json:"total_active_anon"`
	TotalActiveFile uint64 `json:"total_active_file"`
	TotalCache     uint64 `json:"total_cache"`
	TotalInactiveAnon uint64 `json:"total_inactive_anon"`
	TotalInactiveFile uint64 `json:"total_inactive_file"`
	TotalMappedFile uint64 `json:"total_mapped_file"`
	TotalPgfault     uint64 `json:"total_pgfault"`
	TotalPgmajfault  uint64 `json:"total_pgmajfault"`
	TotalPgpgin      uint64 `json:"total_pgpgin"`
	TotalPgpgout     uint64 `json:"total_pgpgout"`
	TotalRss         uint64 `json:"total_rss"`
	TotalRssHuge     uint64 `json:"total_rss_huge"`
	Unevictable      uint64 `json:"unevictable"`
}

type NetworkStats struct {
	RxBytes   uint64 `json:"rx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	RxErrors  uint64 `json:"rx_errors"`
	RxDropped uint64 `json:"rx_dropped"`
	TxBytes   uint64 `json:"tx_bytes"`
	TxPackets uint64 `json:"tx_packets"`
	TxErrors  uint64 `json:"tx_errors"`
	TxDropped uint64 `json:"tx_dropped"`
}

type ExecConfig struct {
	AttachStdin  bool     `json:"AttachStdin"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Tty          bool     `json:"Tty"`
	Cmd          []string `json:"Cmd"`
	Privileged   bool     `json:"Privileged"`
	User         string   `json:"User"`
	WorkingDir   string   `json:"WorkingDir"`
	Env          []string `json:"Env"`
}

type ExecStartCheck struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

type IDResponse struct {
	ID string `json:"Id"`
}

type HijackedResponse struct {
	Conn net.Conn
	Reader *bufio.Reader
}

type ContainerExecInspect struct {
	ID              string   `json:"ID"`
	Running         bool     `json:"Running"`
	ExitCode        int      `json:"ExitCode"`
	ProcessConfig   ExecConfig `json:"ProcessConfig"`
	OpenStdin       bool     `json:"OpenStdin"`
	OpenStderr      bool     `json:"OpenStderr"`
	OpenStdout      bool     `json:"OpenStdout"`
	ContainerID     string   `json:"ContainerID"`
	DetachKeys      string   `json:"DetachKeys"`
}

type PullOptions struct {
	RegistryAuth string
}

type ListOptions struct {
	All bool
}

type ImageSummary struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
	Created     int64    `json:"Created"`
	Size        int64    `json:"Size"`
	SharedSize  int64    `json:"SharedSize"`
	Labels      map[string]string `json:"Labels"`
	Containers  int      `json:"Containers"`
}

type RemoveOptions struct {
	Force         bool
	PruneChildren bool
}

type ImageDeleteResponse struct {
	Deleted  string `json:"Deleted"`
	Untagged string `json:"Untagged"`
}

type ImageInspectResponse struct {
	ID              string            `json:"Id"`
	RepoTags        []string          `json:"RepoTags"`
	RepoDigests     []string          `json:"RepoDigests"`
	Parent          string            `json:"Parent"`
	Comment         string            `json:"Comment"`
	Created         string            `json:"Created"`
	Container       string            `json:"Container"`
	ContainerConfig ContainerConfig   `json:"ContainerConfig"`
	DockerVersion   string            `json:"DockerVersion"`
	Author          string            `json:"Author"`
	Config          ContainerConfig   `json:"Config"`
	Architecture    string            `json:"Architecture"`
	Os              string            `json:"Os"`
	Size            int64             `json:"Size"`
	VirtualSize     int64             `json:"VirtualSize"`
	GraphDriver     GraphDriverData   `json:"GraphDriver"`
	RootFS          RootFS            `json:"RootFS"`
	Metadata        ImageMetadata     `json:"Metadata"`
}

type RootFS struct {
	Type   string   `json:"type"`
	Layers []string `json:"Layers"`
}

type ImageMetadata struct {
	LastTagTime string `json:"LastTagTime"`
}

type Version struct {
	Version       string `json:"Version"`
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string `json:"MinAPIVersion"`
	GitCommit     string `json:"GitCommit"`
	GoVersion     string `json:"GoVersion"`
	Os            string `json:"Os"`
	Arch          string `json:"Arch"`
	KernelVersion string `json:"KernelVersion"`
	Experimental  string `json:"Experimental"`
	BuildTime     string `json:"BuildTime"`
}

type DiskUsage struct {
	Layers []LayerData `json:"Layers"`
	Images []ImageData `json:"Images"`
	Containers []ContainerData `json:"Containers"`
	Volumes []VolumeData `json:"Volumes"`
	BuildCache []BuildCacheData `json:"BuildCache"`
}

type LayerData struct {
	ID        string `json:"Id"`
	SharedSize int64  `json:"SharedSize"`
	Size      int64  `json:"Size"`
	Created   int64  `json:"Created"`
	CreatedBy string `json:"CreatedBy"`
	UsedByImages []string `json:"UsedByImages"`
	UsedByContainers []string `json:"UsedByContainers"`
}

type ImageData struct {
	ID            string   `json:"Id"`
	ParentID      string   `json:"ParentId"`
	RepoTags      []string `json:"RepoTags"`
	RepoDigests   []string `json:"RepoDigests"`
	Created       int64    `json:"Created"`
	Size          int64    `json:"Size"`
	SharedSize    int64    `json:"SharedSize"`
	VirtualSize   int64    `json:"VirtualSize"`
	Labels        map[string]string `json:"Labels"`
	Containers    int      `json:"Containers"`
}

type ContainerData struct {
	ID            string   `json:"Id"`
	Names         []string `json:"Names"`
	Image         string   `json:"Image"`
	ImageID       string   `json:"ImageID"`
	Command       string   `json:"Command"`
	Created       int64    `json:"Created"`
	State         string   `json:"State"`
	Status        string   `json:"Status"`
	Ports         []Port   `json:"Ports"`
	Labels        map[string]string `json:"Labels"`
	SizeRw        int64    `json:"SizeRw"`
	SizeRootFs    int64    `json:"SizeRootFs"`
}

type VolumeData struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Mountpoint string `json:"Mountpoint"`
	Labels     map[string]string `json:"Labels"`
	Scope      string `json:"Scope"`
	Size       int64  `json:"Size"`
	UsageData  UsageData `json:"UsageData"`
}

type UsageData struct {
	Size       int64 `json:"Size"`
	RefCount   int    `json:"RefCount"`
}

type BuildCacheData struct {
	ID             string   `json:"Id"`
	Created        int64    `json:"Created"`
	CreatedBy      string   `json:"CreatedBy"`
	InUse          bool     `json:"InUse"`
	Size           int64    `json:"Size"`
	Parents        []string `json:"Parents"`
	UsageCount     int      `json:"UsageCount"`
}

type ContainersPruneReport struct {
	ContainersDeleted []string `json:"ContainersDeleted"`
	SpaceReclaimed    uint64   `json:"SpaceReclaimed"`
}

type ImagesPruneReport struct {
	ImagesDeleted []string `json:"ImagesDeleted"`
	SpaceReclaimed uint64  `json:"SpaceReclaimed"`
}

type NetworksPruneReport struct {
	NetworksDeleted []string `json:"NetworksDeleted"`
}

type VolumesPruneReport struct {
	VolumesDeleted []string `json:"VolumesDeleted"`
	SpaceReclaimed uint64   `json:"SpaceReclaimed"`
}