package metrics

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MetricsData represents the system metrics
type MetricsData struct {
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
	Disk   float64 `json:"disk"`
}

// NodeMetrics represents metrics for a single node
type NodeMetrics struct {
	Name string       `json:"name"`
	Data *MetricsData `json:"data"`
}

// MultiNodeMetrics represents metrics for multiple nodes
type MultiNodeMetrics struct {
	Nodes []NodeMetrics `json:"nodes"`
}

// NodeConfig represents a node exporter configuration
type NodeConfig struct {
	Name string
	URL  string
}

// Collector interface for metrics collection
type Collector interface {
	GetMetrics() (*MetricsData, error)
}

// MultiNodeCollector interface for multi-node metrics collection
type MultiNodeCollector interface {
	GetMetrics() (*MultiNodeMetrics, error)
}

// NodeExporterCollector implements the Collector interface
type NodeExporterCollector struct {
	nodeExporterURL string
	httpClient      *http.Client
}

// MultiNodeExporterCollector implements the MultiNodeCollector interface
type MultiNodeExporterCollector struct {
	nodeConfigs []NodeConfig
	httpClient  *http.Client
}

// NewCollector creates a new metrics collector
func NewCollector(nodeExporterURL string) Collector {
	return &NodeExporterCollector{
		nodeExporterURL: nodeExporterURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// NewMultiNodeCollector creates a new multi-node metrics collector
func NewMultiNodeCollector(nodeConfigs []NodeConfig) MultiNodeCollector {
	return &MultiNodeExporterCollector{
		nodeConfigs: nodeConfigs,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetMetrics fetches and parses metrics from multiple node_exporters
func (c *MultiNodeExporterCollector) GetMetrics() (*MultiNodeMetrics, error) {
	var nodeMetrics []NodeMetrics

	for _, config := range c.nodeConfigs {
		// Create a single node collector for this node
		singleCollector := &NodeExporterCollector{
			nodeExporterURL: config.URL,
			httpClient:      c.httpClient,
		}

		// Get metrics for this node
		data, err := singleCollector.GetMetrics()
		if err != nil {
			// Log error but continue with other nodes
			fmt.Printf("Error fetching metrics from %s (%s): %v\n", config.Name, config.URL, err)
			// Add empty metrics for failed nodes
			nodeMetrics = append(nodeMetrics, NodeMetrics{
				Name: config.Name,
				Data: &MetricsData{CPU: 0, Memory: 0, Disk: 0},
			})
			continue
		}

		nodeMetrics = append(nodeMetrics, NodeMetrics{
			Name: config.Name,
			Data: data,
		})
	}

	return &MultiNodeMetrics{Nodes: nodeMetrics}, nil
}

// GetMetrics fetches and parses metrics from node_exporter
func (c *NodeExporterCollector) GetMetrics() (*MetricsData, error) {
	resp, err := c.httpClient.Get(c.nodeExporterURL + "/metrics")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node_exporter returned status %d", resp.StatusCode)
	}

	metrics, err := c.parseMetrics(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse metrics: %w", err)
	}

	return metrics, nil
}

// parseMetrics parses Prometheus metrics format
func (c *NodeExporterCollector) parseMetrics(reader io.Reader) (*MetricsData, error) {
	scanner := bufio.NewScanner(reader)

	var cpuIdle float64
	var memTotal, memAvailable float64
	var diskTotal, diskFree float64

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		// Parse CPU idle time (we'll calculate usage from this)
		if strings.HasPrefix(line, "node_cpu_seconds_total{") && strings.Contains(line, `mode="idle"`) {
			if value := extractMetricValue(line); value != -1 {
				cpuIdle = value
			}
		}

		// Parse memory metrics
		if strings.HasPrefix(line, "node_memory_MemTotal_bytes") {
			if value := extractMetricValue(line); value != -1 {
				memTotal = value
			}
		}

		if strings.HasPrefix(line, "node_memory_MemAvailable_bytes") {
			if value := extractMetricValue(line); value != -1 {
				memAvailable = value
			}
		}

		// Parse filesystem metrics (root filesystem)
		if strings.HasPrefix(line, "node_filesystem_size_bytes{") && strings.Contains(line, `mountpoint="/"`) {
			if value := extractMetricValue(line); value != -1 {
				diskTotal = value
			}
		}

		if strings.HasPrefix(line, "node_filesystem_free_bytes{") && strings.Contains(line, `mountpoint="/"`) {
			if value := extractMetricValue(line); value != -1 {
				diskFree = value
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Calculate percentages
	var cpuUsage, memUsage, diskUsage float64

	// CPU usage is approximated (this is simplified - real calculation would need multiple samples)
	if cpuIdle > 0 {
		cpuUsage = 100.0 - (cpuIdle * 0.1) // Simplified calculation
		if cpuUsage < 0 {
			cpuUsage = 0
		}
		if cpuUsage > 100 {
			cpuUsage = 100
		}
	}

	// Memory usage percentage
	if memTotal > 0 && memAvailable >= 0 {
		memUsed := memTotal - memAvailable
		memUsage = (memUsed / memTotal) * 100
	}

	// Disk usage percentage
	if diskTotal > 0 && diskFree >= 0 {
		diskUsed := diskTotal - diskFree
		diskUsage = (diskUsed / diskTotal) * 100
	}

	return &MetricsData{
		CPU:    roundToDecimal(cpuUsage, 1),
		Memory: roundToDecimal(memUsage, 1),
		Disk:   roundToDecimal(diskUsage, 1),
	}, nil
}

// extractMetricValue extracts the numeric value from a Prometheus metric line
func extractMetricValue(line string) float64 {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return -1
	}

	value, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return -1
	}

	return value
}

// roundToDecimal rounds a float64 to specified decimal places
func roundToDecimal(value float64, decimals int) float64 {
	multiplier := 1.0
	for i := 0; i < decimals; i++ {
		multiplier *= 10
	}
	return float64(int(value*multiplier+0.5)) / multiplier
}
