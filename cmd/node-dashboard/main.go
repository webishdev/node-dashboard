package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/webishdev/node-dashboard/internal/config"
	"github.com/webishdev/node-dashboard/internal/server"
	"github.com/webishdev/node-dashboard/internal/storage"
)

const usage = `node-dashboard - a web dashboard for monitoring node metrics

Displays system metrics like CPU, memory, and disk usage by reading from node_exporter,
directly or through Prometheus.

Usage:
  node-dashboard [config.toml]

The configuration file defaults to ./` + config.DefaultPath + `.
`

func run() error {
	args := os.Args[1:]
	if len(args) > 1 || (len(args) == 1 && (args[0] == "-h" || args[0] == "--help")) {
		fmt.Print(usage)
		if len(args) > 1 {
			os.Exit(1)
		}
		os.Exit(0)
	}

	configPath := config.DefaultPath
	if len(args) == 1 {
		configPath = args[0]
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	fmt.Printf("Config: %s\n", configPath)

	// Open the metrics history database
	store, err := storage.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("opening history database: %w", err)
	}
	defer store.Close()
	fmt.Printf("History database: %s\n", filepath.Join(cfg.DataDir, storage.DBFileName))

	// Start the dashboard server
	srv := server.New(cfg.Port, cfg.NodeConfigs(), cfg.PrometheusConfigs(), cfg.UpdateFrequency, cfg.DataFetchFrequency, store, cfg.Retention(), cfg.Auth.Enabled, cfg.Auth.User, cfg.Auth.Password)
	if err := srv.Start(); err != nil {
		return fmt.Errorf("starting server: %w", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
