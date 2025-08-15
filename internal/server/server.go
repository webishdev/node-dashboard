package server

import (
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

// DashboardServer implements the Server interface
type DashboardServer struct {
	port               int
	nodeConfigs        []metrics.NodeConfig
	httpServer         *http.Server
	metricsCollector   metrics.MultiNodeCollector
	template           *template.Template
	updateFrequency    int
	dataFetchFrequency int
	enableAuth         bool
	authUser           string
	authPassword       string
	cachedMetrics      *metrics.MultiNodeMetrics
	metricsMutex       sync.RWMutex
	historicalStore    *metrics.HistoricalStore
}

// New creates a new dashboard server instance
func New(port int, nodeConfigs []metrics.NodeConfig, updateFrequency int, dataFetchFrequency int, enableAuth bool, authUser, authPassword string) Server {
	// Create template with custom functions
	funcMap := template.FuncMap{
		"generateChart": generateChartForTemplate,
	}

	tmpl, err := template.New("dashboard").Funcs(funcMap).Parse(templateContent)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse template: %v", err))
	}

	// Store up to 100 historical data points (about 50 minutes at 30s intervals)
	maxHistoricalPoints := 100

	return &DashboardServer{
		port:               port,
		nodeConfigs:        nodeConfigs,
		metricsCollector:   metrics.NewMultiNodeCollector(nodeConfigs),
		template:           tmpl,
		updateFrequency:    updateFrequency,
		dataFetchFrequency: dataFetchFrequency,
		enableAuth:         enableAuth,
		authUser:           authUser,
		authPassword:       authPassword,
		historicalStore:    metrics.NewHistoricalStore(maxHistoricalPoints),
	}
}

// generateChartForTemplate is a template function that generates SVG charts
func generateChartForTemplate(dataPoints []metrics.DataPoint, metricType string) template.HTML {
	configs := GetChartConfigs()
	config, exists := configs[metricType]
	if !exists {
		config = ChartConfig{Width: 200, Height: 60, Color: "#6B7280"}
	}

	svg := GenerateSVGLineChart(dataPoints, config)
	return template.HTML(svg)
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
}

// fetchAndCacheMetrics fetches metrics and stores them in cache
func (s *DashboardServer) fetchAndCacheMetrics() {
	metrics, err := s.metricsCollector.GetMetrics()
	if err != nil {
		fmt.Printf("Error fetching metrics: %v\n", err)
		return
	}

	// Store historical data points for each node
	for _, node := range metrics.Nodes {
		if node.Data != nil {
			s.historicalStore.AddDataPoint(node.Name, node.Data)
		}
	}

	s.metricsMutex.Lock()
	s.cachedMetrics = metrics
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
	fmt.Printf("Data fetch frequency: %d seconds\n", s.dataFetchFrequency)
	fmt.Printf("Dashboard update frequency: %d seconds\n", s.updateFrequency)

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

	// Get historical data for all nodes
	historicalData := s.historicalStore.GetAllHistoricalData()

	templateData := struct {
		UpdateFrequency int
		Metrics         *metrics.MultiNodeMetrics
		HistoricalData  map[string]*metrics.HistoricalMetrics
	}{
		UpdateFrequency: s.updateFrequency,
		Metrics:         cachedMetrics,
		HistoricalData:  historicalData,
	}

	if err := s.template.Execute(w, templateData); err != nil {
		http.Error(w, fmt.Sprintf("Failed to execute template: %v", err), http.StatusInternalServerError)
		return
	}
}
