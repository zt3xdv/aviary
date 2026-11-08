package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Docker   DockerConfig   `mapstructure:"docker"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Metrics  MetricsConfig  `mapstructure:"metrics"`
	Logging  LoggingConfig  `mapstructure:"logging"`
	Storage  StorageConfig  `mapstructure:"storage"`
}

type ServerConfig struct {
	Address      string        `mapstructure:"address"`
	Port         int           `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`
	TLS          TLSConfig     `mapstructure:"tls"`
}

type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

type DockerConfig struct {
	Host            string        `mapstructure:"host"`
	APIVersion      string        `mapstructure:"api_version"`
	Timeout         time.Duration `mapstructure:"timeout"`
	MaxConcurrent   int           `mapstructure:"max_concurrent"`
	DefaultMemoryMB int           `mapstructure:"default_memory_mb"`
	DefaultCPUCores float64       `mapstructure:"default_cpu_cores"`
	DefaultPids     int64         `mapstructure:"default_pids"`
	DefaultDiskMB   int           `mapstructure:"default_disk_mb"`
	DaemonNetwork   string        `mapstructure:"daemon_network"`
	AllowedMounts   []string      `mapstructure:"allowed_mounts"`
}

type AuthConfig struct {
	PanelPublicKey string        `mapstructure:"panel_public_key"`
	TokenExpiry    time.Duration `mapstructure:"token_expiry"`
	AllowedIPs     []string      `mapstructure:"allowed_ips"`
}

type MetricsConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Address  string `mapstructure:"address"`
	Port     int    `mapstructure:"port"`
	Path     string `mapstructure:"path"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

type StorageConfig struct {
	Root        string `mapstructure:"root"`
	ContainerUID int   `mapstructure:"container_uid"`
	ContainerGID int   `mapstructure:"container_gid"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	v.AutomaticEnv()
	v.SetEnvPrefix("AVIARY")

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.address", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("server.idle_timeout", "120s")
	v.SetDefault("server.tls.enabled", false)
	v.SetDefault("server.tls.cert_file", "")
	v.SetDefault("server.tls.key_file", "")

	v.SetDefault("docker.host", "unix:///var/run/docker.sock")
	v.SetDefault("docker.api_version", "1.44")
	v.SetDefault("docker.timeout", "60s")
	v.SetDefault("docker.max_concurrent", 10)
	v.SetDefault("docker.default_memory_mb", 512)
	v.SetDefault("docker.default_cpu_cores", 1.0)
	v.SetDefault("docker.default_pids", 100)
	v.SetDefault("docker.default_disk_mb", 10240)
	v.SetDefault("docker.daemon_network", "")
	v.SetDefault("docker.allowed_mounts", []string{})

	v.SetDefault("auth.panel_public_key", "")
	v.SetDefault("auth.token_expiry", "1h")
	v.SetDefault("auth.allowed_ips", []string{})

	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.address", "0.0.0.0")
	v.SetDefault("metrics.port", 9090)
	v.SetDefault("metrics.path", "/metrics")

	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")

	v.SetDefault("storage.root", "/var/lib/aviary/volumes")
	v.SetDefault("storage.container_uid", 10000)
	v.SetDefault("storage.container_gid", 10000)
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	paths := []string{
		"/etc/aviary/config.yaml",
		filepath.Join(home, ".config", "aviary", "config.yaml"),
		"./config.yaml",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return paths[0]
}