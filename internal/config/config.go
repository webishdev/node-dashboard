package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/webishdev/node-dashboard/internal/metrics"
)

// DefaultPath is the config file read when no path is given on the command line
const DefaultPath = "node-dashboard.toml"

// DefaultRateInterval is the rate() window in seconds for Prometheus sources that do not set one,
// it covers two scrapes at the Prometheus default scrape interval of 1m
const DefaultRateInterval = 120

// Config holds the dashboard configuration read from a TOML file
type Config struct {
	Port               int                `toml:"port"`
	NodeExporters      []string           `toml:"node_exporters"`
	Prometheus         []PrometheusSource `toml:"prometheus"`
	UpdateFrequency    int                `toml:"update_frequency"`
	DataFetchFrequency int                `toml:"data_fetch_frequency"`
	DataDir            string             `toml:"data_dir"`
	RetentionDays      int                `toml:"retention_days"`
	Auth               Auth               `toml:"auth"`
}

// PrometheusSource is a Prometheus server whose node_exporter targets are shown but not stored
type PrometheusSource struct {
	URL          string `toml:"url"`
	Selector     string `toml:"selector"`
	RateInterval int    `toml:"rate_interval"`
}

// Auth holds the basic authentication settings
type Auth struct {
	Enabled  bool   `toml:"enabled"`
	User     string `toml:"user"`
	Password string `toml:"password"`
}

// Default returns the configuration used for keys missing from the file
func Default() Config {
	return Config{
		Port:               8080,
		UpdateFrequency:    10,
		DataFetchFrequency: 5,
		DataDir:            "./data",
		RetentionDays:      30,
	}
}

// Load reads and validates the TOML config file at path
func Load(path string) (Config, error) {
	cfg := Default()
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("reading config %s: %w", path, err)
	}

	// Reject unknown keys so typos do not silently fall back to defaults
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		return Config{}, fmt.Errorf("config %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}

	var nodes []string
	for _, addr := range cfg.NodeExporters {
		if addr = strings.TrimSpace(addr); addr != "" {
			nodes = append(nodes, addr)
		}
	}
	cfg.NodeExporters = nodes

	// Array entries are not covered by Default, so fill in their defaults here
	for i := range cfg.Prometheus {
		source := &cfg.Prometheus[i]
		source.URL = strings.TrimRight(strings.TrimSpace(source.URL), "/")
		source.Selector = strings.TrimSpace(source.Selector)
		if source.RateInterval == 0 {
			source.RateInterval = DefaultRateInterval
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks that the configuration values are usable
func (c Config) Validate() error {
	var errs []error
	if len(c.NodeExporters) == 0 && len(c.Prometheus) == 0 {
		errs = append(errs, errors.New("node_exporters or prometheus must contain at least one source"))
	}
	for i, source := range c.Prometheus {
		if u, err := url.Parse(source.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("prometheus[%d].url must be an http or https URL", i))
		}
		if source.RateInterval < 1 {
			errs = append(errs, fmt.Errorf("prometheus[%d].rate_interval must be at least 1", i))
		}
	}
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, errors.New("port must be between 1 and 65535"))
	}
	if c.UpdateFrequency < 1 {
		errs = append(errs, errors.New("update_frequency must be at least 1"))
	}
	if c.DataFetchFrequency < 1 {
		errs = append(errs, errors.New("data_fetch_frequency must be at least 1"))
	}
	if c.DataDir == "" {
		errs = append(errs, errors.New("data_dir must not be empty"))
	}
	if c.RetentionDays < 1 {
		errs = append(errs, errors.New("retention_days must be at least 1"))
	}
	if c.Auth.Enabled && (c.Auth.User == "" || c.Auth.Password == "") {
		errs = append(errs, errors.New("auth.user and auth.password are required when auth.enabled is true"))
	}
	return errors.Join(errs...)
}

// Retention returns how long metrics history is kept
func (c Config) Retention() time.Duration {
	return time.Duration(c.RetentionDays) * 24 * time.Hour
}

// NodeConfigs returns the node exporter endpoints to poll
func (c Config) NodeConfigs() []metrics.NodeConfig {
	nodeConfigs := make([]metrics.NodeConfig, len(c.NodeExporters))
	for i, addr := range c.NodeExporters {
		nodeConfigs[i] = metrics.NodeConfig{URL: addr}
	}
	return nodeConfigs
}

// PrometheusConfigs returns the Prometheus servers to read nodes from
func (c Config) PrometheusConfigs() []metrics.PrometheusConfig {
	prometheusConfigs := make([]metrics.PrometheusConfig, len(c.Prometheus))
	for i, source := range c.Prometheus {
		prometheusConfigs[i] = metrics.PrometheusConfig{
			URL:          source.URL,
			Selector:     source.Selector,
			RateInterval: time.Duration(source.RateInterval) * time.Second,
		}
	}
	return prometheusConfigs
}
