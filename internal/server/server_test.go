package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webishdev/node_dashboard/internal/metrics"
)

func TestNew(t *testing.T) {
	nodeConfigs := []metrics.NodeConfig{
		{URL: "http://localhost:9100"},
	}

	server := New(8080, nodeConfigs, 10, 5, false, "", "")

	if server == nil {
		t.Fatal("New() returned nil")
	}

	dashboardServer, ok := server.(*DashboardServer)
	if !ok {
		t.Fatal("New() did not return a *DashboardServer")
	}

	if dashboardServer.port != 8080 {
		t.Errorf("Expected port 8080, got %d", dashboardServer.port)
	}

	if len(dashboardServer.nodeConfigs) != 1 {
		t.Errorf("Expected 1 node config, got %d", len(dashboardServer.nodeConfigs))
	}

	if dashboardServer.nodeConfigs[0].URL != "http://localhost:9100" {
		t.Errorf("Expected URL http://localhost:9100, got %s", dashboardServer.nodeConfigs[0].URL)
	}

	if dashboardServer.updateFrequency != 10 {
		t.Errorf("Expected update frequency 10, got %d", dashboardServer.updateFrequency)
	}

	if dashboardServer.dataFetchFrequency != 5 {
		t.Errorf("Expected data fetch frequency 5, got %d", dashboardServer.dataFetchFrequency)
	}

	if dashboardServer.enableAuth != false {
		t.Errorf("Expected enableAuth false, got %t", dashboardServer.enableAuth)
	}
}

func TestNewWithAuth(t *testing.T) {
	nodeConfigs := []metrics.NodeConfig{
		{URL: "http://localhost:9100"},
	}

	server := New(8080, nodeConfigs, 10, 5, true, "admin", "password")

	dashboardServer, ok := server.(*DashboardServer)
	if !ok {
		t.Fatal("New() did not return a *DashboardServer")
	}

	if dashboardServer.enableAuth != true {
		t.Errorf("Expected enableAuth true, got %t", dashboardServer.enableAuth)
	}

	if dashboardServer.authUser != "admin" {
		t.Errorf("Expected authUser admin, got %s", dashboardServer.authUser)
	}

	if dashboardServer.authPassword != "password" {
		t.Errorf("Expected authPassword password, got %s", dashboardServer.authPassword)
	}
}

func TestBasicAuthMiddleware(t *testing.T) {
	server := &DashboardServer{
		enableAuth:   true,
		authUser:     "admin",
		authPassword: "password",
	}

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	// Wrap with auth middleware
	authHandler := server.basicAuthMiddleware(testHandler)

	// Test without auth
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	authHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	// Test with correct auth
	req = httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "password")
	w = httptest.NewRecorder()
	authHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Body.String() != "success" {
		t.Errorf("Expected body 'success', got '%s'", w.Body.String())
	}

	// Test with incorrect auth
	req = httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "wrongpassword")
	w = httptest.NewRecorder()
	authHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

func TestBasicAuthMiddlewareDisabled(t *testing.T) {
	server := &DashboardServer{
		enableAuth: false,
	}

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	// Wrap with auth middleware
	authHandler := server.basicAuthMiddleware(testHandler)

	// Test without auth - should pass through
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	authHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Body.String() != "success" {
		t.Errorf("Expected body 'success', got '%s'", w.Body.String())
	}
}

func TestGetCachedMetrics(t *testing.T) {
	server := &DashboardServer{
		cachedMetrics: &metrics.MultiNodeMetrics{
			Nodes: []metrics.NodeMetrics{
				{
					Name: "test-node",
					Data: &metrics.MetricsData{
						CPU:    50.0,
						Memory: 75.0,
						Disk:   80.0,
					},
				},
			},
		},
	}

	cached := server.getCachedMetrics()

	if cached == nil {
		t.Fatal("getCachedMetrics() returned nil")
	}

	if len(cached.Nodes) != 1 {
		t.Errorf("Expected 1 node, got %d", len(cached.Nodes))
	}

	testNode := cached.Nodes[0]
	if testNode.Name != "test-node" {
		t.Errorf("Expected node name 'test-node', got '%s'", testNode.Name)
	}

	if testNode.Data.CPU != 50.0 {
		t.Errorf("Expected CPU 50.0, got %f", testNode.Data.CPU)
	}
}
