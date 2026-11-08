package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server:
  address: "127.0.0.1"
  port: 9090
  read_timeout: "10s"
  write_timeout: "10s"
  idle_timeout: "60s"
  tls:
    enabled: true
    cert_file: "/etc/cert.pem"
    key_file: "/etc/key.pem"
docker:
  host: "tcp://docker:2376"
  api_version: "1.43"
  timeout: "30s"
  max_concurrent: 5
  default_memory_mb: 1024
  default_cpu_cores: 2.0
  default_pids: 200
  default_disk_mb: 20480
auth:
  panel_public_key: "base64key"
  token_expiry: "30m"
  allowed_ips: ["10.0.0.0/8"]
metrics:
  enabled: false
  address: "0.0.0.0"
  port: 9091
  path: "/metrics"
logging:
  level: "debug"
  format: "text"
  output: "file"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Address != "127.0.0.1" {
		t.Errorf("address: expected 127.0.0.1, got %s", cfg.Server.Address)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("port: expected 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 10*time.Second {
		t.Errorf("read_timeout: expected 10s, got %v", cfg.Server.ReadTimeout)
	}
	if cfg.Server.TLS.Enabled != true {
		t.Errorf("tls.enabled: expected true, got %v", cfg.Server.TLS.Enabled)
	}
	if cfg.Docker.Host != "tcp://docker:2376" {
		t.Errorf("docker.host: expected tcp://docker:2376, got %s", cfg.Docker.Host)
	}
	if cfg.Docker.MaxConcurrent != 5 {
		t.Errorf("max_concurrent: expected 5, got %d", cfg.Docker.MaxConcurrent)
	}
	if cfg.Auth.TokenExpiry != 30*time.Minute {
		t.Errorf("token_expiry: expected 30m, got %v", cfg.Auth.TokenExpiry)
	}
	if len(cfg.Auth.AllowedIPs) != 1 || cfg.Auth.AllowedIPs[0] != "10.0.0.0/8" {
		t.Errorf("allowed_ips: expected [10.0.0.0/8], got %v", cfg.Auth.AllowedIPs)
	}
	if cfg.Metrics.Enabled != false {
		t.Errorf("metrics.enabled: expected false, got %v", cfg.Metrics.Enabled)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("logging.level: expected debug, got %s", cfg.Logging.Level)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server:
  address: "0.0.0.0"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("default port: expected 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("default read_timeout: expected 30s, got %v", cfg.Server.ReadTimeout)
	}
	if cfg.Docker.Host != "unix:///var/run/docker.sock" {
		t.Errorf("default docker.host: expected unix:///var/run/docker.sock, got %s", cfg.Docker.Host)
	}
	if cfg.Docker.MaxConcurrent != 10 {
		t.Errorf("default max_concurrent: expected 10, got %d", cfg.Docker.MaxConcurrent)
	}
	if cfg.Docker.DefaultMemoryMB != 512 {
		t.Errorf("default memory: expected 512, got %d", cfg.Docker.DefaultMemoryMB)
	}
	if cfg.Docker.DefaultCPUCores != 1.0 {
		t.Errorf("default cpu_cores: expected 1.0, got %v", cfg.Docker.DefaultCPUCores)
	}
	if cfg.Auth.TokenExpiry != time.Hour {
		t.Errorf("default token_expiry: expected 1h, got %v", cfg.Auth.TokenExpiry)
	}
	if cfg.Metrics.Enabled != true {
		t.Errorf("default metrics.enabled: expected true, got %v", cfg.Metrics.Enabled)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path := DefaultConfigPath()
	if path == "" {
		t.Error("DefaultConfigPath should not be empty")
	}
}