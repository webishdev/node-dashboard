package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/common/expfmt"
)

// MetricsData represents the system metrics
type MetricsData struct {
	CPU         float64 `json:"cpu"`
	Memory      float64 `json:"memory"`
	Disk        float64 `json:"disk"`
	CPUCores    int     `json:"cpu_cores"`
	MaxMemoryMB float64 `json:"max_memory_mb"`
	DiskSizeGB  float64 `json:"disk_size_gb"`
	NodeName    string  `json:"node_name"`
	OSName      string  `json:"os_name"`
}

// DataPoint represents a single metric data point with timestamp
type DataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// HistoricalMetrics represents historical data for a single metric type
type HistoricalMetrics struct {
	CPU    []DataPoint `json:"cpu"`
	Memory []DataPoint `json:"memory"`
	Disk   []DataPoint `json:"disk"`
}

// HistoricalStore manages historical data for all nodes
type HistoricalStore struct {
	data      map[string]*HistoricalMetrics // key is node name
	mutex     sync.RWMutex
	maxPoints int
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
			fmt.Printf("Error fetching metrics from %s: %v\n", config.URL, err)
			// Add empty metrics for failed nodes with fallback name
			nodeMetrics = append(nodeMetrics, NodeMetrics{
				Name: fmt.Sprintf("%s", config.URL),
				Data: &MetricsData{CPU: 0, Memory: 0, Disk: 0, CPUCores: 0, MaxMemoryMB: 0, DiskSizeGB: 0, NodeName: "Unknown", OSName: "Unknown"},
			})
			continue
		}

		// Use NodeName from metrics data, fallback to URL if empty
		nodeName := data.NodeName
		if nodeName == "" {
			nodeName = fmt.Sprintf("%s", config.URL)
		}

		nodeMetrics = append(nodeMetrics, NodeMetrics{
			Name: nodeName,
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

// parseMetrics parses Prometheus metrics format using expfmt
func (c *NodeExporterCollector) parseMetrics(reader io.Reader) (*MetricsData, error) {
	var parser expfmt.TextParser
	metricFamilies, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse prometheus metrics: %w", err)
	}

	var cpuIdle float64
	var memTotal, memAvailable float64
	var diskTotal, diskFree float64
	var nodeName, osName string
	var cpuCores int
	cpuCoreMap := make(map[string]bool) // Track unique CPU cores

	// Parse CPU metrics
	if cpuFamily, exists := metricFamilies["node_cpu_seconds_total"]; exists {
		for _, metric := range cpuFamily.GetMetric() {
			labels := make(map[string]string)
			for _, label := range metric.GetLabel() {
				labels[*label.Name] = *label.Value
			}

			// Count unique CPU cores
			if cpu, exists := labels["cpu"]; exists {
				cpuCoreMap[cpu] = true
			}

			// Get idle CPU time
			if mode, exists := labels["mode"]; exists && mode == "idle" {
				cpuIdle = *metric.Counter.Value
			}
		}
	}

	// Parse memory metrics
	if memTotalFamily, exists := metricFamilies["node_memory_MemTotal_bytes"]; exists {
		for _, metric := range memTotalFamily.GetMetric() {
			memTotal = *metric.Gauge.Value
			break
		}
	}

	if memAvailableFamily, exists := metricFamilies["node_memory_MemAvailable_bytes"]; exists {
		for _, metric := range memAvailableFamily.GetMetric() {
			memAvailable = *metric.Gauge.Value
			break
		}
	}

	// Parse filesystem metrics (root filesystem)
	if fsFamily, exists := metricFamilies["node_filesystem_size_bytes"]; exists {
		for _, metric := range fsFamily.GetMetric() {
			labels := make(map[string]string)
			for _, label := range metric.GetLabel() {
				labels[*label.Name] = *label.Value
			}
			if mountpoint, exists := labels["mountpoint"]; exists && mountpoint == "/" {
				diskTotal = *metric.Gauge.Value
				break
			}
		}
	}

	if fsFreeFamily, exists := metricFamilies["node_filesystem_free_bytes"]; exists {
		for _, metric := range fsFreeFamily.GetMetric() {
			labels := make(map[string]string)
			for _, label := range metric.GetLabel() {
				labels[*label.Name] = *label.Value
			}
			if mountpoint, exists := labels["mountpoint"]; exists && mountpoint == "/" {
				diskFree = *metric.Gauge.Value
				break
			}
		}
	}

	// Parse node info
	if unameFamily, exists := metricFamilies["node_uname_info"]; exists {
		for _, metric := range unameFamily.GetMetric() {
			for _, label := range metric.GetLabel() {
				if *label.Name == "nodename" {
					nodeName = *label.Value
					break
				}
			}
			break
		}
	}

	if osFamily, exists := metricFamilies["node_os_info"]; exists {
		for _, metric := range osFamily.GetMetric() {
			for _, label := range metric.GetLabel() {
				if *label.Name == "name" {
					osName = *label.Value
					break
				}
			}
			break
		}
	}

	// Calculate percentages and additional metrics
	var cpuUsage, memUsage, diskUsage float64

	// Count CPU cores
	cpuCores = len(cpuCoreMap)

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

	// Convert bytes to MB and GB
	maxMemoryMB := memTotal / (1024 * 1024)
	diskSizeGB := diskTotal / (1024 * 1024 * 1024)

	return &MetricsData{
		CPU:         roundToDecimal(cpuUsage, 1),
		Memory:      roundToDecimal(memUsage, 1),
		Disk:        roundToDecimal(diskUsage, 1),
		CPUCores:    cpuCores,
		MaxMemoryMB: roundToDecimal(maxMemoryMB, 0),
		DiskSizeGB:  roundToDecimal(diskSizeGB, 1),
		NodeName:    nodeName,
		OSName:      osName,
	}, nil
}

// roundToDecimal rounds a float64 to specified decimal places
func roundToDecimal(value float64, decimals int) float64 {
	multiplier := 1.0
	for i := 0; i < decimals; i++ {
		multiplier *= 10
	}
	return float64(int(value*multiplier+0.5)) / multiplier
}

// NewHistoricalStore creates a new historical data store
func NewHistoricalStore(maxPoints int) *HistoricalStore {
	return &HistoricalStore{
		data:      make(map[string]*HistoricalMetrics),
		maxPoints: maxPoints,
	}
}

// AddDataPoint adds a new data point for a specific node
func (hs *HistoricalStore) AddDataPoint(nodeName string, metricsData *MetricsData) {
	hs.mutex.Lock()
	defer hs.mutex.Unlock()

	// Initialize historical metrics for new nodes
	if hs.data[nodeName] == nil {
		hs.data[nodeName] = &HistoricalMetrics{
			CPU:    make([]DataPoint, 0),
			Memory: make([]DataPoint, 0),
			Disk:   make([]DataPoint, 0),
		}
	}

	now := time.Now()
	historical := hs.data[nodeName]

	// Add new data points
	historical.CPU = append(historical.CPU, DataPoint{Timestamp: now, Value: metricsData.CPU})
	historical.Memory = append(historical.Memory, DataPoint{Timestamp: now, Value: metricsData.Memory})
	historical.Disk = append(historical.Disk, DataPoint{Timestamp: now, Value: metricsData.Disk})

	// Trim to max points if necessary
	if len(historical.CPU) > hs.maxPoints {
		historical.CPU = historical.CPU[len(historical.CPU)-hs.maxPoints:]
	}
	if len(historical.Memory) > hs.maxPoints {
		historical.Memory = historical.Memory[len(historical.Memory)-hs.maxPoints:]
	}
	if len(historical.Disk) > hs.maxPoints {
		historical.Disk = historical.Disk[len(historical.Disk)-hs.maxPoints:]
	}
}

// GetHistoricalData returns historical data for a specific node
func (hs *HistoricalStore) GetHistoricalData(nodeName string) *HistoricalMetrics {
	hs.mutex.RLock()
	defer hs.mutex.RUnlock()

	if data, exists := hs.data[nodeName]; exists {
		// Return a copy to avoid race conditions
		return &HistoricalMetrics{
			CPU:    append([]DataPoint(nil), data.CPU...),
			Memory: append([]DataPoint(nil), data.Memory...),
			Disk:   append([]DataPoint(nil), data.Disk...),
		}
	}
	return &HistoricalMetrics{
		CPU:    make([]DataPoint, 0),
		Memory: make([]DataPoint, 0),
		Disk:   make([]DataPoint, 0),
	}
}

// GetAllHistoricalData returns historical data for all nodes
func (hs *HistoricalStore) GetAllHistoricalData() map[string]*HistoricalMetrics {
	hs.mutex.RLock()
	defer hs.mutex.RUnlock()

	result := make(map[string]*HistoricalMetrics)
	for nodeName, data := range hs.data {
		result[nodeName] = &HistoricalMetrics{
			CPU:    append([]DataPoint(nil), data.CPU...),
			Memory: append([]DataPoint(nil), data.Memory...),
			Disk:   append([]DataPoint(nil), data.Disk...),
		}
	}
	return result
}
