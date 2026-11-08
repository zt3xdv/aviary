package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aviary/agent/internal/api"
	"github.com/aviary/agent/internal/auth"
	"github.com/aviary/agent/internal/config"
	"github.com/aviary/agent/internal/container"
	"github.com/aviary/agent/internal/metrics"
	"github.com/aviary/agent/pkg/docker"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", "", "Path to config file")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Aviary %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	if *configPath == "" {
		*configPath = config.DefaultConfigPath()
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	dockerClient, err := docker.NewClient(cfg.Docker.Host, cfg.Docker.APIVersion, cfg.Docker.Timeout)
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	defer dockerClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := dockerClient.Ping(ctx); err != nil {
		log.Fatalf("Failed to connect to Docker: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(&cfg.Auth)
	if err != nil {
		log.Fatalf("Failed to create authenticator: %v", err)
	}

	containerManager := container.NewManager(
		dockerClient,
		cfg.Docker.MaxConcurrent,
		cfg.Docker.DefaultMemoryMB,
		cfg.Docker.DefaultCPUCores,
		cfg.Docker.DefaultPids,
		cfg.Docker.DefaultDiskMB,
		cfg.Storage.Root,
		cfg.Storage.ContainerUID,
		cfg.Storage.ContainerGID,
		cfg.Docker.AllowedMounts,
	)
	if cfg.Docker.DaemonNetwork != "" {
		containerManager.SetDaemonNetwork(cfg.Docker.DaemonNetwork)
	}

	metricsCollector := metrics.NewCollector(
		cfg.Metrics.Enabled,
		cfg.Metrics.Address,
		cfg.Metrics.Port,
		cfg.Metrics.Path,
	)

	apiServer := api.NewServer(
		containerManager,
		authenticator,
		metricsCollector,
		fmt.Sprintf("%s:%d", cfg.Server.Address, cfg.Server.Port),
		cfg.Server.ReadTimeout,
		cfg.Server.WriteTimeout,
		cfg.Server.IdleTimeout,
		cfg.Server.TLS.Enabled,
		cfg.Server.TLS.CertFile,
		cfg.Server.TLS.KeyFile,
	)

	go func() {
		if cfg.Metrics.Enabled {
			if err := metricsCollector.Start(context.Background()); err != nil && err != http.ErrServerClosed {
				log.Printf("Metrics server error: %v", err)
			}
		}
	}()

	go func() {
		var err error
		if cfg.Server.TLS.Enabled {
			err = apiServer.StartTLS(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
		} else {
			err = apiServer.Start()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("API server error: %v", err)
		}
	}()

	log.Printf("Aviary started on %s:%d", cfg.Server.Address, cfg.Server.Port)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := apiServer.Stop(shutdownCtx); err != nil {
		log.Printf("API server shutdown error: %v", err)
	}
	metricsCollector.Stop()
	log.Println("Aviary stopped")
}