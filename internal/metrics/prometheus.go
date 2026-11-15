package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PrometheusConfig represents a Prometheus server whose node_exporter targets are shown
type PrometheusConfig struct {
	URL          string
	Selector     string        // label matchers limiting the targets, e.g. job="node", surrounding braces are optional
	RateInterval time.Duration // window for rate(), must cover at least two scrapes
}

// PrometheusCollector reads node metrics and their history from the Prometheus HTTP API.
// Unlike NodeExporterCollector it keeps no counter state, Prometheus computes the rates.
type PrometheusCollector struct {
	config     PrometheusConfig
	selector   string // config.Selector without braces
	httpClient *http.Client

	// Nodes found by the last GetMetrics, by instance label, used to name the history series
	nodesMutex sync.RWMutex
	nodes      map[string]*prometheusNode
}

// prometheusNode is a node_exporter target known to the collector
type prometheusNode struct {
	nodename string // from node_uname_info, or the instance if missing
	name     string // nodename, with the instance added when several instances share it
	device   string // filesystem device shown as disk usage, empty if unknown
}

// promSeries is one series of an instant vector or range matrix result
type promSeries struct {
	Metric map[string]string `json:"metric"`
	Value  promSample        `json:"value"`
	Values []promSample      `json:"values"`
}

// promSample is a [unix seconds, "value"] pair as returned by the API
type promSample struct {
	Time  time.Time
	Value float64
}

// UnmarshalJSON decodes a sample pair
func (s *promSample) UnmarshalJSON(b []byte) error {
	var pair [2]json.RawMessage
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	var ts float64
	if err := json.Unmarshal(pair[0], &ts); err != nil {
		return err
	}
	var value string
	if err := json.Unmarshal(pair[1], &value); err != nil {
		return err
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	s.Time, s.Value = unixTime(ts), v
	return nil
}

// promResponse is the envelope of the query and query_range API responses
type promResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		Result []promSeries `json:"result"`
	} `json:"data"`
}

// NewPrometheusCollector creates a new Prometheus metrics collector
func NewPrometheusCollector(config PrometheusConfig) *PrometheusCollector {
	selector := strings.TrimSpace(config.Selector)
	selector = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(selector, "{"), "}"))

	return &PrometheusCollector{
		config:   config,
		selector: selector,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		nodes: make(map[string]*prometheusNode),
	}
}

// netDeviceRegex matches the interfaces whose traffic is not counted, see excludedNetPrefixes
func netDeviceRegex() string {
	// The prefixes contain no regex metacharacters, so they need no quoting
	return "(" + strings.Join(excludedNetPrefixes, "|") + ").*"
}

// sel returns a label selector with the given matchers and the configured selector
func (c *PrometheusCollector) sel(matchers ...string) string {
	if c.selector != "" {
		matchers = append(matchers, c.selector)
	}
	return "{" + strings.Join(matchers, ",") + "}"
}

// promDuration formats a duration as a PromQL range in whole seconds
func promDuration(d time.Duration) string {
	seconds := int64(d / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%ds", seconds)
}

// get calls a Prometheus API endpoint and returns the result series
func (c *PrometheusCollector) get(endpoint string, params url.Values) ([]promSeries, error) {
	resp, err := c.httpClient.Get(c.config.URL + endpoint + "?" + params.Encode())
	if err != nil {
		return nil, fmt.Errorf("failed to query prometheus: %w", err)
	}
	defer resp.Body.Close()

	// Errors in the query come with a JSON body, so decode it before checking the status
	var body promResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("prometheus returned status %d: %w", resp.StatusCode, err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus returned status %d: %s", resp.StatusCode, body.Error)
	}
	return body.Data.Result, nil
}

// query evaluates an instant query
func (c *PrometheusCollector) query(promql string) ([]promSeries, error) {
	return c.get("/api/v1/query", url.Values{"query": {promql}})
}

// queryRange evaluates a range query
func (c *PrometheusCollector) queryRange(promql string, start, end time.Time, step time.Duration) ([]promSeries, error) {
	return c.get("/api/v1/query_range", url.Values{
		"query": {promql},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {promDuration(step)},
	})
}

// queryAll evaluates the queries concurrently and returns their results by key.
// A failed query is left out, so the other values still show, and its error is returned.
func (c *PrometheusCollector) queryAll(queries map[string]string, run func(string) ([]promSeries, error)) (map[string][]promSeries, []error) {
	var mutex sync.Mutex
	var wg sync.WaitGroup
	results := make(map[string][]promSeries, len(queries))
	var errs []error

	for key, promql := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			series, err := run(promql)
			mutex.Lock()
			defer mutex.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("querying %s from %s: %w", key, c.config.URL, err))
				return
			}
			results[key] = series
		}()
	}
	wg.Wait()

	return results, errs
}

// logErrors logs the errors of the queries that failed
func logErrors(errs []error) {
	for _, err := range errs {
		fmt.Printf("Error %v\n", err)
	}
}

// byInstance returns the first series of each instance
func byInstance(series []promSeries) map[string]promSeries {
	result := make(map[string]promSeries)
	for _, s := range series {
		if _, exists := result[s.Metric["instance"]]; !exists {
			result[s.Metric["instance"]] = s
		}
	}
	return result
}

// GetMetrics fetches the current metrics of all node_exporter targets matching the selector
func (c *PrometheusCollector) GetMetrics() (*MultiNodeMetrics, error) {
	// Query the targets first, so an unreachable Prometheus is not asked for every metric
	up, err := c.query("up" + c.sel())
	if err != nil {
		// Log but show the nodes known so far as offline, like unreachable node exporters
		fmt.Printf("Error fetching metrics from %s: %v\n", c.config.URL, err)
		return &MultiNodeMetrics{Nodes: c.offlineNodes()}, nil
	}

	r := promDuration(c.config.RateInterval)
	results, errs := c.queryAll(map[string]string{
		"cpu":       `100 * (1 - avg by (instance) (rate(node_cpu_seconds_total` + c.sel(`mode="idle"`) + `[` + r + `])))`,
		"cores":     `count by (instance) (node_cpu_seconds_total` + c.sel(`mode="idle"`) + `)`,
		"memTotal":  "node_memory_MemTotal_bytes" + c.sel(),
		"memAvail":  "node_memory_MemAvailable_bytes" + c.sel(),
		"fsSize":    "node_filesystem_size_bytes" + c.sel(),
		"fsFree":    "node_filesystem_free_bytes" + c.sel(),
		"netRx":     `sum by (instance) (rate(node_network_receive_bytes_total` + c.sel(`device!~"`+netDeviceRegex()+`"`) + `[` + r + `]))`,
		"netTx":     `sum by (instance) (rate(node_network_transmit_bytes_total` + c.sel(`device!~"`+netDeviceRegex()+`"`) + `[` + r + `]))`,
		"time":      "node_time_seconds" + c.sel(),
		"scrapedAt": "timestamp(node_time_seconds" + c.sel() + ")",
		"bootTime":  "node_boot_time_seconds" + c.sel(),
		"timeZone":  "node_time_zone_offset_seconds" + c.sel(),
		"uname":     "node_uname_info" + c.sel(),
		"osInfo":    "node_os_info" + c.sel(),
	}, c.query)
	logErrors(errs)

	uname := byInstance(results["uname"])
	filesystems := c.filesystems(results["fsSize"], results["fsFree"])

	// Remember new node_exporter targets; targets without node_uname_info, like Prometheus itself, are no node_exporters
	c.nodesMutex.Lock()
	for instance, s := range uname {
		node := c.nodes[instance]
		if node == nil {
			node = &prometheusNode{}
			c.nodes[instance] = node
		}
		node.nodename = s.Metric["nodename"]
		if node.nodename == "" {
			node.nodename = instance
		}
		if fs := selectRootFilesystem(filesystems[instance]); fs != nil {
			node.device = fs.device
		}
	}
	c.makeNamesUnique()
	names := make(map[string]string, len(c.nodes))
	for instance, node := range c.nodes {
		names[instance] = node.name
	}
	c.nodesMutex.Unlock()

	value := func(key, instance string) float64 {
		return byInstance(results[key])[instance].Value.Value
	}

	var nodeMetrics []NodeMetrics
	for _, s := range sortedByInstance(up) {
		instance := s.Metric["instance"]
		name, known := names[instance]
		if !known {
			continue
		}

		if s.Value.Value != 1 {
			nodeMetrics = append(nodeMetrics, offlineNode(name))
			continue
		}

		raw := &rawMetrics{
			cpuCores:     int(value("cores", instance)),
			memTotal:     value("memTotal", instance),
			memAvailable: value("memAvail", instance),
			nodeTime:     value("time", instance),
			bootTime:     value("bootTime", instance),
		}
		if fs := selectRootFilesystem(filesystems[instance]); fs != nil {
			raw.diskTotal, raw.diskFree = fs.size, fs.free
		}
		if tz, ok := byInstance(results["timeZone"])[instance]; ok {
			raw.timeZone = tz.Metric["time_zone"]
			raw.tzOffset = int(tz.Value.Value)
		}
		if u, ok := uname[instance]; ok {
			raw.nodeName = u.Metric["nodename"]
			raw.sysname = u.Metric["sysname"]
			raw.kernel = u.Metric["release"]
			raw.arch = u.Metric["machine"]
		}
		if osInfo, ok := byInstance(results["osInfo"])[instance]; ok {
			raw.osName = osInfo.Metric["name"]
			raw.osPrettyName = osInfo.Metric["pretty_name"]
		}

		// The clock offset compares the node clock with the time of its scrape, not with the query
		scrapedAt := time.Now()
		if at := value("scrapedAt", instance); at > 0 {
			scrapedAt = unixTime(at)
		}

		cpu := clamp(value("cpu", instance), 0, 100)
		data := buildMetricsData(raw, cpu, value("netRx", instance), value("netTx", instance), scrapedAt)
		nodeMetrics = append(nodeMetrics, NodeMetrics{
			Name:   name,
			Data:   data,
			Online: true,
			Source: SourcePrometheus,
		})
	}

	return &MultiNodeMetrics{Nodes: nodeMetrics}, nil
}

// filesystems groups the filesystem size and free samples by instance and device
func (c *PrometheusCollector) filesystems(size, free []promSeries) map[string]map[string]*filesystem {
	result := make(map[string]map[string]*filesystem)
	add := func(series []promSeries, set func(*filesystem, float64)) {
		for _, s := range series {
			instance := s.Metric["instance"]
			if result[instance] == nil {
				result[instance] = make(map[string]*filesystem)
			}
			addFilesystemSample(result[instance], s.Metric, s.Value.Value, set)
		}
	}
	add(size, func(fs *filesystem, v float64) { fs.size = v })
	add(free, func(fs *filesystem, v float64) { fs.free = v })
	return result
}

// makeNamesUnique names the nodes, adding the instance where several instances share a nodename.
// The caller holds nodesMutex.
func (c *PrometheusCollector) makeNamesUnique() {
	count := make(map[string]int)
	for _, node := range c.nodes {
		count[node.nodename]++
	}
	for instance, node := range c.nodes {
		node.name = node.nodename
		if count[node.nodename] > 1 && node.nodename != instance {
			node.name = fmt.Sprintf("%s (%s)", node.nodename, instance)
		}
	}
}

// offlineNodes returns all known nodes as offline
func (c *PrometheusCollector) offlineNodes() []NodeMetrics {
	c.nodesMutex.RLock()
	defer c.nodesMutex.RUnlock()

	instances := make([]string, 0, len(c.nodes))
	for instance := range c.nodes {
		instances = append(instances, instance)
	}
	sort.Strings(instances)

	nodes := make([]NodeMetrics, len(instances))
	for i, instance := range instances {
		nodes[i] = offlineNode(c.nodes[instance].name)
	}
	return nodes
}

// offlineNode returns the placeholder of a node that cannot be reached
func offlineNode(name string) NodeMetrics {
	return NodeMetrics{
		Name:   name,
		Data:   &MetricsData{NodeName: "Unknown", OSName: "Unknown"},
		Source: SourcePrometheus,
	}
}

// sortedByInstance sorts series by their instance label, so nodes keep their order between fetches
func sortedByInstance(series []promSeries) []promSeries {
	sorted := append([]promSeries(nil), series...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Metric["instance"] < sorted[j].Metric["instance"] })
	return sorted
}

// QueryHistory returns the history of all known nodes between start and end, averaged into steps.
// Each point is stamped with the start of the step it covers, like the buckets of the history store.
func (c *PrometheusCollector) QueryHistory(start, end time.Time, step time.Duration) (map[string]*HistoricalMetrics, error) {
	stepSeconds := int64(step / time.Second)
	if stepSeconds < 1 {
		stepSeconds = 1
	}
	step = time.Duration(stepSeconds) * time.Second

	// Evaluate at the end of each step, aligned to the unix epoch like the history store
	evalStart := time.Unix((start.Unix()/stepSeconds+1)*stepSeconds, 0)
	evalEnd := time.Unix(end.Unix()/stepSeconds*stepSeconds, 0)
	if evalEnd.Before(evalStart) {
		return map[string]*HistoricalMetrics{}, nil
	}

	window := c.config.RateInterval
	if step > window {
		window = step
	}
	w := "[" + promDuration(window) + "]"

	results, errs := c.queryAll(map[string]string{
		"cpu":    `100 * (1 - avg by (instance) (rate(node_cpu_seconds_total` + c.sel(`mode="idle"`) + w + `)))`,
		"memory": `100 * (1 - avg_over_time(node_memory_MemAvailable_bytes` + c.sel() + w + `) / avg_over_time(node_memory_MemTotal_bytes` + c.sel() + w + `))`,
		"disk":   `100 * (1 - avg_over_time(node_filesystem_free_bytes` + c.sel() + w + `) / avg_over_time(node_filesystem_size_bytes` + c.sel() + w + `))`,
		"netRx":  `sum by (instance) (rate(node_network_receive_bytes_total` + c.sel(`device!~"`+netDeviceRegex()+`"`) + w + `))`,
		"netTx":  `sum by (instance) (rate(node_network_transmit_bytes_total` + c.sel(`device!~"`+netDeviceRegex()+`"`) + w + `))`,
	}, func(promql string) ([]promSeries, error) {
		return c.queryRange(promql, evalStart, evalEnd, step)
	})
	if len(results) == 0 {
		// Usually Prometheus is unreachable, then all errors are alike
		return nil, errs[0]
	}
	logErrors(errs)

	c.nodesMutex.RLock()
	nodes := make(map[string]prometheusNode, len(c.nodes))
	for instance, node := range c.nodes {
		nodes[instance] = *node
	}
	c.nodesMutex.RUnlock()

	history := make(map[string]*HistoricalMetrics)
	points := func(key string, filter func(promSeries, prometheusNode) bool, add func(*HistoricalMetrics, []DataPoint)) {
		seen := make(map[string]bool)
		for _, s := range results[key] {
			instance := s.Metric["instance"]
			node, known := nodes[instance]
			if !known || seen[instance] || (filter != nil && !filter(s, node)) {
				continue
			}
			seen[instance] = true

			h := history[node.name]
			if h == nil {
				h = &HistoricalMetrics{}
				history[node.name] = h
			}
			add(h, toDataPoints(s.Values, step))
		}
	}

	points("cpu", nil, func(h *HistoricalMetrics, p []DataPoint) { h.CPU = p })
	points("memory", nil, func(h *HistoricalMetrics, p []DataPoint) { h.Memory = p })
	points("disk", func(s promSeries, node prometheusNode) bool {
		return s.Metric["device_error"] == "" && s.Metric["device"] == node.device
	}, func(h *HistoricalMetrics, p []DataPoint) { h.Disk = p })
	points("netRx", nil, func(h *HistoricalMetrics, p []DataPoint) { h.NetRx = p })
	points("netTx", nil, func(h *HistoricalMetrics, p []DataPoint) { h.NetTx = p })

	return history, nil
}

// toDataPoints converts range samples to data points stamped at the start of their step, skipping NaN and Inf
func toDataPoints(samples []promSample, step time.Duration) []DataPoint {
	points := make([]DataPoint, 0, len(samples))
	for _, s := range samples {
		if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			continue
		}
		points = append(points, DataPoint{Timestamp: s.Time.Add(-step), Value: s.Value})
	}
	return points
}
