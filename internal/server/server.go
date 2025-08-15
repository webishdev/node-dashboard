package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"github.com/webishdev/node_dashboard/internal/metrics"
)

// Server represents the web server interface
type Server interface {
	Start() error
	Stop() error
}

// DashboardServer implements the Server interface
type DashboardServer struct {
	port             int
	nodeConfigs      []metrics.NodeConfig
	httpServer       *http.Server
	metricsCollector metrics.MultiNodeCollector
	template         *template.Template
	updateFrequency  int
}

// New creates a new dashboard server instance
func New(port int, nodeConfigs []metrics.NodeConfig, updateFrequency int) Server {
	tmpl, err := template.ParseFiles("templates/dashboard.html")
	if err != nil {
		panic(fmt.Sprintf("Failed to parse template: %v", err))
	}

	return &DashboardServer{
		port:             port,
		nodeConfigs:      nodeConfigs,
		metricsCollector: metrics.NewMultiNodeCollector(nodeConfigs),
		template:         tmpl,
		updateFrequency:  updateFrequency,
	}
}

// Start starts the web server
func (s *DashboardServer) Start() error {
	mux := http.NewServeMux()

	// Serve static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static/"))))

	// Dashboard route
	mux.HandleFunc("/", s.handleDashboard)

	// API routes for metrics
	mux.HandleFunc("/api/metrics", s.handleMetrics)

	s.httpServer = &http.Server{
		Addr:    ":" + strconv.Itoa(s.port),
		Handler: mux,
	}

	fmt.Printf("Starting server on port %d\n", s.port)
	fmt.Printf("Node exporters configured: %d\n", len(s.nodeConfigs))
	for _, config := range s.nodeConfigs {
		fmt.Printf("  - %s: %s\n", config.Name, config.URL)
	}

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

	templateData := struct {
		UpdateFrequency int
	}{
		UpdateFrequency: s.updateFrequency,
	}

	if err := s.template.Execute(w, templateData); err != nil {
		http.Error(w, fmt.Sprintf("Failed to execute template: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleMetrics serves the metrics API endpoint
func (s *DashboardServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	multiNodeMetrics, err := s.metricsCollector.GetMetrics()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get metrics: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(multiNodeMetrics); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode metrics: %v", err), http.StatusInternalServerError)
		return
	}
}
