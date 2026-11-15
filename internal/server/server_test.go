package server

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/webishdev/node-dashboard/internal/metrics"
	"github.com/webishdev/node-dashboard/internal/storage"
)

const testRetention = 30 * 24 * time.Hour

func openTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// fakeCollector returns fixed metrics
type fakeCollector struct {
	metrics *metrics.MultiNodeMetrics
}

func (f *fakeCollector) GetMetrics() (*metrics.MultiNodeMetrics, error) {
	return f.metrics, nil
}

func TestNew(t *testing.T) {
	nodeConfigs := []metrics.NodeConfig{
		{URL: "http://localhost:9100"},
	}

	server := New(8080, nodeConfigs, 10, 5, openTestStore(t), testRetention, false, "", "")

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

	server := New(8080, nodeConfigs, 10, 5, openTestStore(t), testRetention, true, "admin", "password")

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

func TestAvailableRanges(t *testing.T) {
	keys := func(ranges []ChartRange) string {
		var k []string
		for _, r := range ranges {
			k = append(k, r.Key)
		}
		return strings.Join(k, ",")
	}

	tests := []struct {
		retention time.Duration
		expected  string
	}{
		{30 * 24 * time.Hour, "1h,24h,7d,30d"},
		{7 * 24 * time.Hour, "1h,24h,7d"},
		{24 * time.Hour, "1h,24h"},
		{time.Hour, "1h"},
	}

	for _, test := range tests {
		if got := keys(availableRanges(test.retention)); got != test.expected {
			t.Errorf("availableRanges(%s) = %s, expected %s", test.retention, got, test.expected)
		}
	}
}

func TestSelectRange(t *testing.T) {
	ranges := availableRanges(24 * time.Hour)

	if r := selectRange(ranges, "24h"); r.Key != "24h" {
		t.Errorf("Expected 24h, got %s", r.Key)
	}
	if r := selectRange(ranges, ""); r.Key != "1h" {
		t.Errorf("Expected default 1h, got %s", r.Key)
	}
	// Ranges beyond the retention fall back to the default
	if r := selectRange(ranges, "30d"); r.Key != "1h" {
		t.Errorf("Expected fallback 1h for unavailable range, got %s", r.Key)
	}
}

func TestBucketSize(t *testing.T) {
	if b := bucketSize(time.Hour, 5); b != 12*time.Second {
		t.Errorf("Expected 12s bucket for 1h, got %s", b)
	}
	if b := bucketSize(30*24*time.Hour, 5); b != 144*time.Minute {
		t.Errorf("Expected 144m bucket for 30d, got %s", b)
	}
	if b := bucketSize(time.Hour, 30); b != 30*time.Second {
		t.Errorf("Expected bucket not finer than fetch frequency, got %s", b)
	}
}

func TestFetchStoresOnlyOnlineNodes(t *testing.T) {
	store := openTestStore(t)
	server := &DashboardServer{
		historyStore: store,
		metricsCollector: &fakeCollector{metrics: &metrics.MultiNodeMetrics{
			Nodes: []metrics.NodeMetrics{
				{Name: "online", Online: true, Data: &metrics.MetricsData{CPU: 10}},
				{Name: "http://offline:9100", Online: false, Data: &metrics.MetricsData{}},
			},
		}},
	}

	server.fetchAndCacheMetrics()

	data, err := store.Query(time.Now().Add(-time.Minute), time.Second)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(data) != 1 || data["online"] == nil {
		t.Errorf("Expected only the online node to be stored, got %v", data)
	}
}

func TestHandleDashboardRange(t *testing.T) {
	store := openTestStore(t)
	if err := store.Add("test-node", time.Now().Add(-2*time.Hour), &metrics.MetricsData{CPU: 42}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	server := New(8080, nil, 10, 5, store, testRetention, false, "", "").(*DashboardServer)
	server.cachedMetrics = &metrics.MultiNodeMetrics{
		Nodes: []metrics.NodeMetrics{
			{Name: "test-node", Online: true, Data: &metrics.MetricsData{CPU: 42}},
		},
	}

	render := func(query string) string {
		req := httptest.NewRequest("GET", "/"+query, nil)
		w := httptest.NewRecorder()
		server.handleDashboard(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", w.Code)
		}
		return w.Body.String()
	}

	// The sample is two hours old, so the default 1h range has no data
	body := render("")
	if !strings.Contains(body, `href="?range=1h" aria-current="page"`) {
		t.Error("Expected 1h to be the selected range by default")
	}
	if !strings.Contains(body, "No historical data") {
		t.Error("Expected empty charts for the 1h range")
	}

	body = render("?range=7d")
	if !strings.Contains(body, `href="?range=7d" aria-current="page"`) {
		t.Error("Expected 7d to be the selected range")
	}
	if !strings.Contains(body, "Last 1 week") {
		t.Error("Expected chart caption for the 7d range")
	}
	if strings.Contains(body, "No historical data") {
		t.Error("Expected charts with data for the 7d range")
	}
}

func TestHandleDashboardRenderError(t *testing.T) {
	server := New(8080, nil, 10, 5, openTestStore(t), testRetention, false, "", "").(*DashboardServer)
	// Fails after part of the page was rendered
	server.template = template.Must(template.New("broken").Parse(`<p>partial</p>{{.Missing}}`))

	w := httptest.NewRecorder()
	server.handleDashboard(w, httptest.NewRequest("GET", "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "partial") {
		t.Error("Expected no partial page in the error response")
	}
}

func TestFormatBytesRate(t *testing.T) {
	tests := []struct {
		bps      float64
		expected string
	}{
		{0, "0 B/s"},
		{999, "999 B/s"},
		{1500, "1.5 KB/s"},
		{340_000, "340 KB/s"},
		{12_345_678, "12.3 MB/s"},
		{2.5e9, "2.5 GB/s"},
		{5e12, "5000 GB/s"},
	}

	for _, test := range tests {
		if result := formatBytesRate(test.bps); result != test.expected {
			t.Errorf("formatBytesRate(%v) = %q, expected %q", test.bps, result, test.expected)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{30 * time.Second, "0m"},
		{7 * time.Minute, "7m"},
		{5*time.Hour + 12*time.Minute, "5h 12m"},
		{3*24*time.Hour + 4*time.Hour + 59*time.Minute, "3d 4h"},
	}

	for _, test := range tests {
		if result := formatDuration(test.d); result != test.expected {
			t.Errorf("formatDuration(%v) = %q, expected %q", test.d, result, test.expected)
		}
	}
}

func TestFormatClockOffset(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{0, ""},
		{999 * time.Millisecond, ""},
		{-999 * time.Millisecond, ""},
		{2500 * time.Millisecond, "+2.5s"},
		{-90 * time.Second, "-90.0s"},
	}

	for _, test := range tests {
		if result := formatClockOffset(test.d); result != test.expected {
			t.Errorf("formatClockOffset(%v) = %q, expected %q", test.d, result, test.expected)
		}
	}
}

func TestHandleDashboardNodeInfo(t *testing.T) {
	server := New(8080, nil, 10, 5, openTestStore(t), testRetention, false, "", "").(*DashboardServer)
	nodeTime := time.Date(2026, 11, 13, 14, 3, 0, 0, time.FixedZone("CET", 3600))
	server.cachedMetrics = &metrics.MultiNodeMetrics{
		Nodes: []metrics.NodeMetrics{
			{Name: "test-node", Online: true, Data: &metrics.MetricsData{
				OSName:      "Ubuntu 24.04.1 LTS",
				Kernel:      "6.8.0-45-generic",
				Arch:        "x86_64",
				NetRxBps:    1_200_000,
				NetTxBps:    340_000,
				NodeTime:    nodeTime,
				TimeZone:    "CET",
				Uptime:      76 * time.Hour,
				ClockOffset: 3 * time.Second,
			}},
		},
	}

	w := httptest.NewRecorder()
	server.handleDashboard(w, httptest.NewRequest("GET", "/", nil))
	body := w.Body.String()

	for _, want := range []string{"Ubuntu 24.04.1 LTS", "Kernel 6.8.0-45-generic", "x86_64", "Up 3d 4h", "Time 14:03 CET", "3.0s)", "1.2 MB/s", "340 KB/s"} {
		if !strings.Contains(body, want) {
			t.Errorf("Expected %q in the dashboard", want)
		}
	}
}
