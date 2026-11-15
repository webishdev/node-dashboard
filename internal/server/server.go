package server

import (
	"bytes"
	"crypto/subtle"
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/webishdev/node-dashboard/internal/metrics"
)

//go:embed templates/dashboard.html
var templateContent string

// Server represents the web server interface
type Server interface {
	Start() error
	Stop() error
}

// HistoryStore persists metric samples and returns them downsampled for the charts
type HistoryStore interface {
	Add(node string, ts time.Time, data *metrics.MetricsData) error
	Query(since time.Time, bucket time.Duration) (map[string]*metrics.HistoricalMetrics, error)
	Prune(before time.Time) (int64, error)
}

// HistorySource returns history from a source that keeps it itself, like Prometheus, so it is not stored
type HistorySource interface {
	QueryHistory(start, end time.Time, step time.Duration) (map[string]*metrics.HistoricalMetrics, error)
}

// ChartRange is a selectable time range for the history charts
type ChartRange struct {
	Key      string
	Label    string
	Duration time.Duration
}

// chartRanges lists all selectable ranges, the first one is the default
var chartRanges = []ChartRange{
	{Key: "1h", Label: "1h", Duration: time.Hour},
	{Key: "24h", Label: "24h", Duration: 24 * time.Hour},
	{Key: "7d", Label: "1 week", Duration: 7 * 24 * time.Hour},
	{Key: "30d", Label: "1 month", Duration: 30 * 24 * time.Hour},
}

const (
	// maxChartPoints matches the chart width, more points would not be visible
	maxChartPoints = 300
	// historyCacheTTL limits how often long ranges are aggregated from the database
	historyCacheTTL = time.Minute
	// pruneInterval is how often samples older than the retention are deleted
	pruneInterval = time.Hour
)

// historyCacheEntry holds a cached history query for one range
type historyCacheEntry struct {
	data    map[string]*metrics.HistoricalMetrics
	expires time.Time
}

// DashboardServer implements the Server interface
type DashboardServer struct {
	port                 int
	nodeConfigs          []metrics.NodeConfig
	prometheusConfigs    []metrics.PrometheusConfig
	httpServer           *http.Server
	metricsCollector     metrics.MultiNodeCollector
	prometheusCollectors []metrics.MultiNodeCollector // their nodes are shown but not stored
	historySources       []HistorySource              // history of the Prometheus nodes
	template             *template.Template
	updateFrequency      int
	dataFetchFrequency   int
	enableAuth           bool
	authUser             string
	authPassword         string
	cachedMetrics        *metrics.MultiNodeMetrics
	renamedNodes         []map[string]string // per history source, names changed to avoid a clash with another source
	metricsMutex         sync.RWMutex
	historyStore         HistoryStore
	retention            time.Duration
	historyCache         map[string]historyCacheEntry
	historyCacheMutex    sync.Mutex
}

// New creates a new dashboard server instance
func New(port int, nodeConfigs []metrics.NodeConfig, prometheusConfigs []metrics.PrometheusConfig, updateFrequency int, dataFetchFrequency int, store HistoryStore, retention time.Duration, enableAuth bool, authUser, authPassword string) Server {
	// Create template with custom functions
	funcMap := template.FuncMap{
		"generateChart":    generateChartForTemplate,
		"generateNetChart": generateNetChartForTemplate,
		"level":            usageLevel,
		"pct":              clampPercent,
		"bytesRate":        formatBytesRate,
		"duration":         formatDuration,
		"offset":           formatClockOffset,
	}

	tmpl, err := template.New("dashboard").Funcs(funcMap).Parse(templateContent)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse template: %v", err))
	}

	var prometheusCollectors []metrics.MultiNodeCollector
	var historySources []HistorySource
	for _, config := range prometheusConfigs {
		collector := metrics.NewPrometheusCollector(config)
		prometheusCollectors = append(prometheusCollectors, collector)
		historySources = append(historySources, collector)
	}

	return &DashboardServer{
		port:                 port,
		nodeConfigs:          nodeConfigs,
		prometheusConfigs:    prometheusConfigs,
		metricsCollector:     metrics.NewMultiNodeCollector(nodeConfigs),
		prometheusCollectors: prometheusCollectors,
		historySources:       historySources,
		template:             tmpl,
		updateFrequency:      updateFrequency,
		dataFetchFrequency:   dataFetchFrequency,
		enableAuth:           enableAuth,
		authUser:             authUser,
		authPassword:         authPassword,
		historyStore:         store,
		retention:            retention,
		historyCache:         make(map[string]historyCacheEntry),
	}
}

// generateChartForTemplate is a template function that generates SVG charts
func generateChartForTemplate(dataPoints []metrics.DataPoint, metricType string, window ChartWindow) template.HTML {
	configs := GetChartConfigs()
	config, exists := configs[metricType]
	if !exists {
		config = ChartConfig{Width: 300, Height: 64, Color: "#6B7280"}
	}

	svg := GenerateSVGLineChart(dataPoints, config, window)
	return template.HTML(svg)
}

// generateNetChartForTemplate is a template function that generates the network chart with receive and transmit lines
func generateNetChartForTemplate(history *metrics.HistoricalMetrics, window ChartWindow) template.HTML {
	config := GetChartConfigs()["network"]
	svg := GenerateSVGMultiLineChart([]ChartSeries{
		{Points: history.NetRx, Color: netRxColor},
		{Points: history.NetTx, Color: netTxColor},
	}, config, window)
	return template.HTML(svg)
}

// formatBytesRate formats a throughput in bytes per second with decimal units, as is usual for networks
func formatBytesRate(bps float64) string {
	units := []string{"B/s", "KB/s", "MB/s", "GB/s"}
	i := 0
	for bps >= 1000 && i < len(units)-1 {
		bps /= 1000
		i++
	}
	if i == 0 || bps >= 100 {
		return fmt.Sprintf("%.0f %s", bps, units[i])
	}
	return fmt.Sprintf("%.1f %s", bps, units[i])
}

// formatDuration formats an uptime with its two largest units, e.g. "3d 4h", "5h 12m" or "7m"
func formatDuration(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	hours := int(d % (24 * time.Hour) / time.Hour)
	minutes := int(d % time.Hour / time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// formatClockOffset formats how far the node clock is off, or returns "" when it is below one second
func formatClockOffset(d time.Duration) string {
	if d > -time.Second && d < time.Second {
		return ""
	}
	return fmt.Sprintf("%+.1fs", d.Seconds())
}

// availableRanges returns the chart ranges that fit within the retention
func availableRanges(retention time.Duration) []ChartRange {
	ranges := []ChartRange{chartRanges[0]}
	for _, r := range chartRanges[1:] {
		if r.Duration <= retention {
			ranges = append(ranges, r)
		}
	}
	return ranges
}

// selectRange returns the range matching the key, or the first range if none matches
func selectRange(ranges []ChartRange, key string) ChartRange {
	for _, r := range ranges {
		if r.Key == key {
			return r
		}
	}
	return ranges[0]
}

// bucketSize returns the averaging interval so a range fits into maxChartPoints,
// but never finer than the fetch frequency
func bucketSize(rangeDuration time.Duration, dataFetchFrequency int) time.Duration {
	bucket := (rangeDuration / maxChartPoints).Truncate(time.Second)
	if fetch := time.Duration(dataFetchFrequency) * time.Second; bucket < fetch {
		bucket = fetch
	}
	return bucket
}

// queryHistory returns the stored history merged with the history sources for a range.
// Long ranges are cached, and with history sources short ranges too, until the next fetch,
// so page refreshes do not query Prometheus every time.
func (s *DashboardServer) queryHistory(r ChartRange, now time.Time, bucket time.Duration) (map[string]*metrics.HistoricalMetrics, error) {
	ttl := historyCacheTTL
	if r.Duration < 24*time.Hour {
		ttl = 0
		if len(s.historySources) > 0 {
			ttl = time.Duration(s.dataFetchFrequency) * time.Second
		}
	}
	cacheable := ttl > 0

	if cacheable {
		s.historyCacheMutex.Lock()
		entry, ok := s.historyCache[r.Key]
		s.historyCacheMutex.Unlock()
		if ok && now.Before(entry.expires) {
			return entry.data, nil
		}
	}

	data, err := s.historyStore.Query(now.Add(-r.Duration), bucket)
	if err != nil {
		return nil, err
	}
	if data == nil {
		data = make(map[string]*metrics.HistoricalMetrics)
	}

	s.metricsMutex.RLock()
	renamedNodes := s.renamedNodes
	s.metricsMutex.RUnlock()

	// A failing source is skipped, so the history of the other nodes still shows
	for i, source := range s.historySources {
		sourceData, err := source.QueryHistory(now.Add(-r.Duration), now, bucket)
		if err != nil {
			fmt.Printf("Error querying history source: %v\n", err)
			continue
		}
		for name, history := range sourceData {
			if i < len(renamedNodes) {
				if renamed, ok := renamedNodes[i][name]; ok {
					name = renamed
				}
			}
			data[name] = history
		}
	}

	if cacheable {
		s.historyCacheMutex.Lock()
		s.historyCache[r.Key] = historyCacheEntry{data: data, expires: now.Add(ttl)}
		s.historyCacheMutex.Unlock()
	}

	return data, nil
}

// pruneHistory deletes samples older than the retention
func (s *DashboardServer) pruneHistory() {
	removed, err := s.historyStore.Prune(time.Now().Add(-s.retention))
	if err != nil {
		fmt.Printf("Error pruning historical data: %v\n", err)
		return
	}
	if removed > 0 {
		fmt.Printf("Pruned %d samples older than %s\n", removed, s.retention)
	}
}

// usageLevel classifies a usage percentage for threshold coloring in the template
func usageLevel(value float64) string {
	switch {
	case value >= 90:
		return "crit"
	case value >= 75:
		return "warn"
	default:
		return "ok"
	}
}

// clampPercent limits a percentage to 0-100 for use as a CSS width
func clampPercent(value float64) string {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return fmt.Sprintf("%.1f", value)
}

// startBackgroundDataFetching starts a goroutine that periodically fetches metrics
func (s *DashboardServer) startBackgroundDataFetching() {
	// Fetch initial data
	s.fetchAndCacheMetrics()

	// Start background ticker
	ticker := time.NewTicker(time.Duration(s.dataFetchFrequency) * time.Second)
	go func() {
		for range ticker.C {
			s.fetchAndCacheMetrics()
		}
	}()

	// Delete samples older than the retention now and then periodically
	s.pruneHistory()
	pruneTicker := time.NewTicker(pruneInterval)
	go func() {
		for range pruneTicker.C {
			s.pruneHistory()
		}
	}()
}

// fetchAndCacheMetrics fetches metrics and stores them in cache
func (s *DashboardServer) fetchAndCacheMetrics() {
	metrics, err := s.metricsCollector.GetMetrics()
	if err != nil {
		fmt.Printf("Error fetching metrics: %v\n", err)
		return
	}

	// Store historical data points for each reachable node; offline nodes only carry placeholder zeros
	now := time.Now()
	for _, node := range metrics.Nodes {
		if node.Online && node.Data != nil {
			if err := s.historyStore.Add(node.Name, now, node.Data); err != nil {
				fmt.Printf("Error storing metrics for %s: %v\n", node.Name, err)
			}
		}
	}

	// Prometheus keeps the history of its nodes itself, so they are only shown
	taken := make(map[string]bool)
	for _, node := range metrics.Nodes {
		taken[node.Name] = true
	}
	renamedNodes := make([]map[string]string, len(s.prometheusCollectors))
	for i, collector := range s.prometheusCollectors {
		prometheusMetrics, err := collector.GetMetrics()
		if err != nil {
			fmt.Printf("Error fetching Prometheus metrics: %v\n", err)
			continue
		}

		// The history is keyed by name, so a node also scraped directly or by another Prometheus needs another name
		renamedNodes[i] = make(map[string]string)
		for _, node := range prometheusMetrics.Nodes {
			name := node.Name
			for n := 1; taken[node.Name]; n++ {
				node.Name = fmt.Sprintf("%s (Prometheus %d)", name, n)
			}
			if node.Name != name {
				renamedNodes[i][name] = node.Name
			}
			taken[node.Name] = true
			metrics.Nodes = append(metrics.Nodes, node)
		}
	}

	s.metricsMutex.Lock()
	s.cachedMetrics = metrics
	s.renamedNodes = renamedNodes
	s.metricsMutex.Unlock()
}

// getCachedMetrics returns the cached metrics in a thread-safe way
func (s *DashboardServer) getCachedMetrics() *metrics.MultiNodeMetrics {
	s.metricsMutex.RLock()
	defer s.metricsMutex.RUnlock()
	return s.cachedMetrics
}

// basicAuthMiddleware provides HTTP basic authentication
func (s *DashboardServer) basicAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enableAuth {
			next(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Node Dashboard"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(s.authUser)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(s.authPassword)) == 1

		if !userMatch || !passMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="Node Dashboard"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

// Start starts the web server
func (s *DashboardServer) Start() error {
	// Start background data fetching
	s.startBackgroundDataFetching()

	mux := http.NewServeMux()

	// Serve static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static/"))))

	// Dashboard route
	mux.HandleFunc("/", s.basicAuthMiddleware(s.handleDashboard))

	s.httpServer = &http.Server{
		Addr:    ":" + strconv.Itoa(s.port),
		Handler: mux,
	}

	fmt.Printf("Starting server on port %d\n", s.port)
	fmt.Printf("Node exporters configured: %d\n", len(s.nodeConfigs))
	for _, config := range s.nodeConfigs {
		fmt.Printf("  - %s: %s\n", config.Name, config.URL)
	}
	fmt.Printf("Prometheus sources configured: %d\n", len(s.prometheusConfigs))
	for _, config := range s.prometheusConfigs {
		fmt.Printf("  - %s (selector %q, rate interval %s)\n", config.URL, config.Selector, config.RateInterval)
	}
	fmt.Printf("Data fetch frequency: %d seconds\n", s.dataFetchFrequency)
	fmt.Printf("Dashboard update frequency: %d seconds\n", s.updateFrequency)
	fmt.Printf("History retention: %s\n", s.retention)

	return s.httpServer.ListenAndServe()
}

// Stop stops the web server
func (s *DashboardServer) Stop() error {
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}

// handleDashboard serves the main dashboard page
func (s *DashboardServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	// Get cached metrics data
	cachedMetrics := s.getCachedMetrics()

	// Get historical data for all nodes in the selected range
	ranges := availableRanges(s.retention)
	selectedRange := selectRange(ranges, r.URL.Query().Get("range"))
	now := time.Now()
	window := ChartWindow{
		Start:  now.Add(-selectedRange.Duration),
		End:    now,
		Bucket: bucketSize(selectedRange.Duration, s.dataFetchFrequency),
	}

	historicalData, err := s.queryHistory(selectedRange, now, window.Bucket)
	if err != nil {
		fmt.Printf("Error querying historical data: %v\n", err)
	}

	templateData := struct {
		UpdateFrequency int
		Metrics         *metrics.MultiNodeMetrics
		HistoricalData  map[string]*metrics.HistoricalMetrics
		Ranges          []ChartRange
		SelectedRange   ChartRange
		ChartWindow     ChartWindow
	}{
		UpdateFrequency: s.updateFrequency,
		Metrics:         cachedMetrics,
		HistoricalData:  historicalData,
		Ranges:          ranges,
		SelectedRange:   selectedRange,
		ChartWindow:     window,
	}

	// Render into a buffer first, so a failed render can still answer with an error status
	var buf bytes.Buffer
	if err := s.template.Execute(&buf, templateData); err != nil {
		fmt.Printf("Error rendering dashboard: %v\n", err)
		http.Error(w, "Failed to render dashboard", http.StatusInternalServerError)
		return
	}

	// A write error only means the client went away, e.g. it navigated during a refresh
	_, _ = buf.WriteTo(w)
}
