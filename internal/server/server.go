package server

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"github.com/webishdev/node_dashboard/internal/metrics"
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
	port             int
	nodeConfigs      []metrics.NodeConfig
	httpServer       *http.Server
	metricsCollector metrics.MultiNodeCollector
	template         *template.Template
	updateFrequency  int
	enableAuth       bool
	authUser         string
	authPassword     string
}

// New creates a new dashboard server instance
func New(port int, nodeConfigs []metrics.NodeConfig, updateFrequency int, enableAuth bool, authUser, authPassword string) Server {
	tmpl, err := template.New("dashboard").Parse(templateContent)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse template: %v", err))
	}

	return &DashboardServer{
		port:             port,
		nodeConfigs:      nodeConfigs,
		metricsCollector: metrics.NewMultiNodeCollector(nodeConfigs),
		template:         tmpl,
		updateFrequency:  updateFrequency,
		enableAuth:       enableAuth,
		authUser:         authUser,
		authPassword:     authPassword,
	}
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
	mux := http.NewServeMux()

	// Serve static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static/"))))

	// Dashboard route
	mux.HandleFunc("/", s.basicAuthMiddleware(s.handleDashboard))

	// API routes for metrics
	mux.HandleFunc("/api/metrics", s.basicAuthMiddleware(s.handleMetrics))

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
