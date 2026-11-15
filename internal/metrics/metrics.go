package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
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
	Kernel      string  `json:"kernel"`
	Arch        string  `json:"arch"`

	// Network throughput in bytes per second, summed over the physical interfaces
	NetRxBps float64 `json:"net_rx_bps"`
	NetTxBps float64 `json:"net_tx_bps"`

	// NodeTime is the node clock at scrape in the node's time zone, zero if unknown
	NodeTime    time.Time     `json:"node_time"`
	TimeZone    string        `json:"time_zone"`
	Uptime      time.Duration `json:"uptime"`
	ClockOffset time.Duration `json:"clock_offset"` // node clock minus dashboard clock
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
	NetRx  []DataPoint `json:"net_rx"`
	NetTx  []DataPoint `json:"net_tx"`
}

// NodeMetrics represents metrics for a single node
type NodeMetrics struct {
	Name   string       `json:"name"`
	Data   *MetricsData `json:"data"`
	Online bool         `json:"online"`
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

	// CPU counters from the previous scrape, used to compute usage between scrapes
	cpuMutex  sync.Mutex
	prevIdle  float64
	prevTotal float64
	hasPrev   bool

	// Network byte counters from the previous scrape, used to compute throughput
	netMutex   sync.Mutex
	prevNetRx  float64
	prevNetTx  float64
	prevNetAt  time.Time
	hasPrevNet bool
}

// MultiNodeExporterCollector implements the MultiNodeCollector interface
type MultiNodeExporterCollector struct {
	nodeConfigs []NodeConfig
	collectors  []*NodeExporterCollector // one per node config, kept to preserve CPU counter state
}

// rawMetrics holds the values parsed from a single node_exporter scrape
type rawMetrics struct {
	cpuIdle      float64 // idle seconds summed over all cores
	cpuTotal     float64 // seconds in all modes summed over all cores
	cpuCores     int
	memTotal     float64
	memAvailable float64
	diskTotal    float64
	diskFree     float64
	netRx        float64 // received bytes summed over the counted interfaces
	netTx        float64 // transmitted bytes summed over the counted interfaces
	nodeTime     float64 // unix seconds
	bootTime     float64 // unix seconds
	timeZone     string
	tzOffset     int // seconds east of UTC
	nodeName     string
	osName       string
	osPrettyName string
	sysname      string
	kernel       string
	arch         string
}

// excludedNetPrefixes lists loopback and virtual interfaces whose traffic is not counted,
// it either stays on the node or is already counted on the physical interface
var excludedNetPrefixes = []string{
	"lo", "veth", "docker", "br-", "cni", "flannel", "cali", "virbr", "vnet",
	"tun", "tap", "utun", "gif", "stf", "kube-", "cilium", "weave", "lxc",
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
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	collectors := make([]*NodeExporterCollector, len(nodeConfigs))
	for i, config := range nodeConfigs {
		collectors[i] = &NodeExporterCollector{
			nodeExporterURL: config.URL,
			httpClient:      httpClient,
		}
	}

	return &MultiNodeExporterCollector{
		nodeConfigs: nodeConfigs,
		collectors:  collectors,
	}
}

// GetMetrics fetches and parses metrics from multiple node_exporters
func (c *MultiNodeExporterCollector) GetMetrics() (*MultiNodeMetrics, error) {
	var nodeMetrics []NodeMetrics

	for i, config := range c.nodeConfigs {
		// Get metrics for this node
		data, err := c.collectors[i].GetMetrics()
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
			Name:   nodeName,
			Data:   data,
			Online: true,
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
	scrapedAt := time.Now()

	raw, err := parseMetrics(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse metrics: %w", err)
	}

	return c.toMetricsData(raw, scrapedAt), nil
}

// parseMetrics parses Prometheus metrics format using expfmt
func parseMetrics(reader io.Reader) (*rawMetrics, error) {
	var parser expfmt.TextParser
	metricFamilies, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse prometheus metrics: %w", err)
	}

	raw := &rawMetrics{}
	cpuCoreMap := make(map[string]bool) // Track unique CPU cores

	// Parse CPU metrics: sum the counters of all cores, in total and for idle only
	if cpuFamily, exists := metricFamilies["node_cpu_seconds_total"]; exists {
		for _, metric := range cpuFamily.GetMetric() {
			labels := metricLabels(metric)

			// Count unique CPU cores
			if cpu, exists := labels["cpu"]; exists {
				cpuCoreMap[cpu] = true
			}

			value := sampleValue(metric)
			raw.cpuTotal += value
			if labels["mode"] == "idle" {
				raw.cpuIdle += value
			}
		}
	}
	raw.cpuCores = len(cpuCoreMap)

	// Parse memory metrics
	if memTotalFamily, exists := metricFamilies["node_memory_MemTotal_bytes"]; exists {
		for _, metric := range memTotalFamily.GetMetric() {
			raw.memTotal = sampleValue(metric)
			break
		}
	}

	if memAvailableFamily, exists := metricFamilies["node_memory_MemAvailable_bytes"]; exists {
		for _, metric := range memAvailableFamily.GetMetric() {
			raw.memAvailable = sampleValue(metric)
			break
		}
	}

	// Parse filesystem metrics, per device
	filesystems := make(map[string]*filesystem)
	collectFilesystems(metricFamilies["node_filesystem_size_bytes"], filesystems, func(fs *filesystem, v float64) { fs.size = v })
	collectFilesystems(metricFamilies["node_filesystem_free_bytes"], filesystems, func(fs *filesystem, v float64) { fs.free = v })
	if fs := selectRootFilesystem(filesystems); fs != nil {
		raw.diskTotal = fs.size
		raw.diskFree = fs.free
	}

	// Parse network metrics, summed over the counted interfaces
	raw.netRx = sumNetDevices(metricFamilies["node_network_receive_bytes_total"])
	raw.netTx = sumNetDevices(metricFamilies["node_network_transmit_bytes_total"])

	// Parse time metrics
	if timeFamily, exists := metricFamilies["node_time_seconds"]; exists {
		for _, metric := range timeFamily.GetMetric() {
			raw.nodeTime = sampleValue(metric)
			break
		}
	}

	if bootFamily, exists := metricFamilies["node_boot_time_seconds"]; exists {
		for _, metric := range bootFamily.GetMetric() {
			raw.bootTime = sampleValue(metric)
			break
		}
	}

	if tzFamily, exists := metricFamilies["node_time_zone_offset_seconds"]; exists {
		for _, metric := range tzFamily.GetMetric() {
			raw.timeZone = metricLabels(metric)["time_zone"]
			raw.tzOffset = int(sampleValue(metric))
			break
		}
	}

	// Parse node info
	if unameFamily, exists := metricFamilies["node_uname_info"]; exists {
		for _, metric := range unameFamily.GetMetric() {
			labels := metricLabels(metric)
			raw.nodeName = labels["nodename"]
			raw.sysname = labels["sysname"]
			raw.kernel = labels["release"]
			raw.arch = labels["machine"]
			break
		}
	}

	// node_os_info is only available on Linux
	if osFamily, exists := metricFamilies["node_os_info"]; exists {
		for _, metric := range osFamily.GetMetric() {
			labels := metricLabels(metric)
			raw.osName = labels["name"]
			raw.osPrettyName = labels["pretty_name"]
			break
		}
	}

	return raw, nil
}

// countedNetDevice reports whether the traffic of an interface counts toward the node throughput
func countedNetDevice(device string) bool {
	for _, prefix := range excludedNetPrefixes {
		if strings.HasPrefix(device, prefix) {
			return false
		}
	}
	return device != ""
}

// sumNetDevices sums a network counter family over the counted interfaces
func sumNetDevices(family *dto.MetricFamily) float64 {
	var sum float64
	for _, metric := range family.GetMetric() {
		if countedNetDevice(metricLabels(metric)["device"]) {
			sum += sampleValue(metric)
		}
	}
	return sum
}

// filesystem holds the size and free bytes of one device
type filesystem struct {
	root bool // mounted at "/"
	size float64
	free float64
}

// collectFilesystems adds the samples of a filesystem metric family to the devices, skipping devices with errors
func collectFilesystems(family *dto.MetricFamily, filesystems map[string]*filesystem, set func(*filesystem, float64)) {
	for _, metric := range family.GetMetric() {
		labels := metricLabels(metric)
		if labels["device_error"] != "" {
			continue
		}

		// A device can be mounted several times, e.g. the bind mounts of a container
		fs := filesystems[labels["device"]]
		if fs == nil {
			fs = &filesystem{}
			filesystems[labels["device"]] = fs
		}
		if labels["mountpoint"] == "/" {
			fs.root = true
		}
		set(fs, sampleValue(metric))
	}
}

// selectRootFilesystem returns the filesystem mounted at "/", or the largest one if there is none,
// which happens when node_exporter runs in a container without access to the host root
func selectRootFilesystem(filesystems map[string]*filesystem) *filesystem {
	var largest *filesystem
	for _, fs := range filesystems {
		if fs.root {
			return fs
		}
		if largest == nil || fs.size > largest.size {
			largest = fs
		}
	}
	return largest
}

// metricLabels returns the labels of a metric as a map
func metricLabels(metric *dto.Metric) map[string]string {
	labels := make(map[string]string)
	for _, label := range metric.GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	return labels
}

// sampleValue returns the value of a counter, gauge or untyped metric
func sampleValue(metric *dto.Metric) float64 {
	switch {
	case metric.Counter != nil:
		return metric.GetCounter().GetValue()
	case metric.Gauge != nil:
		return metric.GetGauge().GetValue()
	default:
		return metric.GetUntyped().GetValue()
	}
}

// cpuUsage returns the CPU usage in percent since the previous scrape.
// On the first scrape, or when the counters were reset by a reboot, it
// returns the average usage since boot instead.
func (c *NodeExporterCollector) cpuUsage(idle, total float64) float64 {
	c.cpuMutex.Lock()
	defer c.cpuMutex.Unlock()

	deltaIdle := idle - c.prevIdle
	deltaTotal := total - c.prevTotal
	hasPrev := c.hasPrev

	c.prevIdle, c.prevTotal, c.hasPrev = idle, total, true

	var usage float64
	switch {
	case hasPrev && deltaTotal > 0 && deltaIdle >= 0:
		usage = 100 * (1 - deltaIdle/deltaTotal)
	case total > 0:
		usage = 100 * (1 - idle/total)
	}

	return clamp(usage, 0, 100)
}

// netRates returns the receive and transmit throughput in bytes per second since the previous scrape.
// Unlike cpuUsage there is no average since boot, so it returns 0 on the first scrape
// and when a counter was reset by a reboot or an interface going away.
func (c *NodeExporterCollector) netRates(rx, tx float64, at time.Time) (float64, float64) {
	c.netMutex.Lock()
	defer c.netMutex.Unlock()

	deltaRx := rx - c.prevNetRx
	deltaTx := tx - c.prevNetTx
	elapsed := at.Sub(c.prevNetAt).Seconds()
	hasPrev := c.hasPrevNet

	c.prevNetRx, c.prevNetTx, c.prevNetAt, c.hasPrevNet = rx, tx, at, true

	if !hasPrev || elapsed <= 0 || deltaRx < 0 || deltaTx < 0 {
		return 0, 0
	}
	return deltaRx / elapsed, deltaTx / elapsed
}

// unixTime converts fractional unix seconds to a time
func unixTime(seconds float64) time.Time {
	sec, frac := math.Modf(seconds)
	return time.Unix(int64(sec), int64(frac*1e9))
}

// toMetricsData converts raw scrape values into dashboard metrics
func (c *NodeExporterCollector) toMetricsData(raw *rawMetrics, scrapedAt time.Time) *MetricsData {
	var memUsage, diskUsage float64

	cpuUsage := c.cpuUsage(raw.cpuIdle, raw.cpuTotal)
	netRx, netTx := c.netRates(raw.netRx, raw.netTx, scrapedAt)

	// Node clock, shown in the node's own time zone when it is known
	var nodeTime time.Time
	var uptime, clockOffset time.Duration
	if raw.nodeTime > 0 {
		nodeTime = unixTime(raw.nodeTime)
		clockOffset = nodeTime.Sub(scrapedAt)
		if raw.bootTime > 0 && raw.nodeTime > raw.bootTime {
			uptime = time.Duration((raw.nodeTime - raw.bootTime) * float64(time.Second))
		}
		if raw.timeZone != "" {
			nodeTime = nodeTime.In(time.FixedZone(raw.timeZone, raw.tzOffset))
		}
	}

	// Prefer the full distribution name, fall back to the kernel name where node_os_info is missing
	osName := raw.osPrettyName
	if osName == "" {
		osName = raw.osName
	}
	if osName == "" {
		osName = raw.sysname
	}

	// Memory usage percentage
	if raw.memTotal > 0 && raw.memAvailable >= 0 {
		memUsed := raw.memTotal - raw.memAvailable
		memUsage = (memUsed / raw.memTotal) * 100
	}

	// Disk usage percentage
	if raw.diskTotal > 0 && raw.diskFree >= 0 {
		diskUsed := raw.diskTotal - raw.diskFree
		diskUsage = (diskUsed / raw.diskTotal) * 100
	}

	// Convert bytes to MB and GB
	maxMemoryMB := raw.memTotal / (1024 * 1024)
	diskSizeGB := raw.diskTotal / (1024 * 1024 * 1024)

	return &MetricsData{
		CPU:         roundToDecimal(cpuUsage, 1),
		Memory:      roundToDecimal(memUsage, 1),
		Disk:        roundToDecimal(diskUsage, 1),
		CPUCores:    raw.cpuCores,
		MaxMemoryMB: roundToDecimal(maxMemoryMB, 0),
		DiskSizeGB:  roundToDecimal(diskSizeGB, 1),
		NodeName:    raw.nodeName,
		OSName:      osName,
		Kernel:      raw.kernel,
		Arch:        raw.arch,
		NetRxBps:    roundToDecimal(netRx, 0),
		NetTxBps:    roundToDecimal(netTx, 0),
		NodeTime:    nodeTime,
		TimeZone:    raw.timeZone,
		Uptime:      uptime,
		ClockOffset: clockOffset,
	}
}

// clamp limits a value to the range [min, max]
func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
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
