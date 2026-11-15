package metrics

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// promResult is a canned API result for queries containing match; the first matching result answers
type promResult struct {
	match  string
	result string
}

// fakePrometheus serves canned results and records the parameters of the requests
type fakePrometheus struct {
	t       *testing.T
	mutex   sync.Mutex
	results []promResult
	params  []url.Values
}

func newFakePrometheus(t *testing.T, results ...promResult) (*fakePrometheus, *httptest.Server) {
	f := &fakePrometheus{t: t, results: results}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakePrometheus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	f.mutex.Lock()
	f.params = append(f.params, params)
	results := f.results
	f.mutex.Unlock()

	query := params.Get("query")
	for _, res := range results {
		if strings.Contains(query, res.match) {
			w.Write([]byte(res.result))
			return
		}
	}
	// Like Prometheus for a metric without series
	w.Write([]byte(vector()))
}

func (f *fakePrometheus) setResults(results ...promResult) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.results = results
}

// vector builds an instant query response
func vector(series ...string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(series, ",") + `]}}`
}

// matrix builds a range query response
func matrix(series ...string) string {
	return `{"status":"success","data":{"resultType":"matrix","result":[` + strings.Join(series, ",") + `]}}`
}

// sample builds an instant vector series with the given labels, e.g. `"instance":"a:9100"`
func sample(labels, value string) string {
	return `{"metric":{` + labels + `},"value":[1700000000,"` + value + `"]}`
}

// samples builds a range series with the given labels and values, e.g. `[60060,"1"]`
func samples(labels string, values ...string) string {
	return `{"metric":{` + labels + `},"values":[` + strings.Join(values, ",") + `]}`
}

// alphaResults is a node_exporter target alpha on instance a:9100, besides Prometheus itself without node metrics
func alphaResults(upAlpha string) []promResult {
	return []promResult{
		{"up{", vector(sample(`"instance":"a:9100"`, upAlpha), sample(`"instance":"prom:9090"`, "1"))},
		{"rate(node_cpu_seconds_total", vector(sample(`"instance":"a:9100"`, "42.34"))},
		{"count by (instance) (node_cpu", vector(sample(`"instance":"a:9100"`, "4"))},
		{"node_memory_MemTotal_bytes", vector(sample(`"instance":"a:9100"`, "1000"))},
		{"node_memory_MemAvailable_bytes", vector(sample(`"instance":"a:9100"`, "250"))},
		{"node_filesystem_size_bytes", vector(
			sample(`"instance":"a:9100","device":"/dev/sda1","mountpoint":"/"`, "100"),
			sample(`"instance":"a:9100","device":"/dev/sdb1","mountpoint":"/data"`, "1000"),
		)},
		{"node_filesystem_free_bytes", vector(
			sample(`"instance":"a:9100","device":"/dev/sda1","mountpoint":"/"`, "40"),
			sample(`"instance":"a:9100","device":"/dev/sdb1","mountpoint":"/data"`, "0"),
		)},
		{"node_network_receive_bytes_total", vector(sample(`"instance":"a:9100"`, "1500.4"))},
		{"node_network_transmit_bytes_total", vector(sample(`"instance":"a:9100"`, "300"))},
		{"timestamp(node_time_seconds", vector(sample(`"instance":"a:9100"`, "1700000000"))},
		{"node_time_seconds", vector(sample(`"instance":"a:9100"`, "1700000003"))},
		{"node_boot_time_seconds", vector(sample(`"instance":"a:9100"`, "1699900000"))},
		{"node_time_zone_offset_seconds", vector(sample(`"instance":"a:9100","time_zone":"CET"`, "3600"))},
		{"node_uname_info", vector(sample(`"instance":"a:9100","nodename":"alpha","sysname":"Linux","release":"6.8.0","machine":"x86_64"`, "1"))},
		{"node_os_info", vector(sample(`"instance":"a:9100","name":"Ubuntu","pretty_name":"Ubuntu 24.04.1 LTS"`, "1"))},
	}
}

func newTestPrometheusCollector(url string) *PrometheusCollector {
	return NewPrometheusCollector(PrometheusConfig{URL: url, Selector: `job="node"`, RateInterval: 2 * time.Minute})
}

func TestPrometheusGetMetrics(t *testing.T) {
	_, server := newFakePrometheus(t, alphaResults("1")...)
	c := newTestPrometheusCollector(server.URL)

	result, err := c.GetMetrics()
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}
	// Prometheus itself has no node_uname_info, so it is no node
	if len(result.Nodes) != 1 {
		t.Fatalf("Expected 1 node, got %+v", result.Nodes)
	}

	node := result.Nodes[0]
	if node.Name != "alpha" || !node.Online || node.Source != SourcePrometheus {
		t.Errorf("Unexpected node %+v", node)
	}

	data := node.Data
	checks := []struct {
		name      string
		got, want any
	}{
		{"CPU", data.CPU, 42.3},
		{"CPUCores", data.CPUCores, 4},
		{"Memory", data.Memory, 75.0},
		{"Disk", data.Disk, 60.0}, // root filesystem, not the larger /data
		{"NetRxBps", data.NetRxBps, 1500.0},
		{"NetTxBps", data.NetTxBps, 300.0},
		{"OSName", data.OSName, "Ubuntu 24.04.1 LTS"},
		{"Kernel", data.Kernel, "6.8.0"},
		{"Arch", data.Arch, "x86_64"},
		{"TimeZone", data.TimeZone, "CET"},
		{"Uptime", data.Uptime, 100003 * time.Second},
		{"ClockOffset", data.ClockOffset, 3 * time.Second},
		{"NodeTime", data.NodeTime.Format("15:04:05 MST"), "23:13:23 CET"},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
}

func TestPrometheusOfflineNodes(t *testing.T) {
	fake, server := newFakePrometheus(t, alphaResults("1")...)
	c := newTestPrometheusCollector(server.URL)
	if _, err := c.GetMetrics(); err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}

	// The target is down, its node_exporter metrics went stale
	fake.setResults(promResult{"up{", vector(sample(`"instance":"a:9100"`, "0"))})
	result, err := c.GetMetrics()
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].Name != "alpha" || result.Nodes[0].Online {
		t.Errorf("Expected alpha offline, got %+v", result.Nodes)
	}

	// Prometheus itself is down
	server.Close()
	result, err = c.GetMetrics()
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].Name != "alpha" || result.Nodes[0].Online {
		t.Errorf("Expected alpha offline, got %+v", result.Nodes)
	}
}

func TestPrometheusDuplicateNodenames(t *testing.T) {
	_, server := newFakePrometheus(t,
		promResult{"up{", vector(sample(`"instance":"a:9100"`, "1"), sample(`"instance":"b:9100"`, "1"))},
		promResult{"node_uname_info", vector(
			sample(`"instance":"a:9100","nodename":"localhost"`, "1"),
			sample(`"instance":"b:9100","nodename":"localhost"`, "1"),
		)},
	)
	c := newTestPrometheusCollector(server.URL)

	result, err := c.GetMetrics()
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}
	if len(result.Nodes) != 2 || result.Nodes[0].Name != "localhost (a:9100)" || result.Nodes[1].Name != "localhost (b:9100)" {
		t.Errorf("Expected names with the instance, got %+v", result.Nodes)
	}
}

func TestPrometheusSelector(t *testing.T) {
	tests := []struct {
		selector string
		want     string
	}{
		{`job="node"`, `{mode="idle",job="node"}`},
		{` {job="node", env="prod"} `, `{mode="idle",job="node", env="prod"}`},
		{"", `{mode="idle"}`},
	}

	for _, test := range tests {
		c := NewPrometheusCollector(PrometheusConfig{Selector: test.selector})
		if got := c.sel(`mode="idle"`); got != test.want {
			t.Errorf("sel with selector %q = %s, want %s", test.selector, got, test.want)
		}
	}
}

func TestPrometheusQueryError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	}))
	t.Cleanup(server.Close)

	c := newTestPrometheusCollector(server.URL)
	if _, err := c.query("up{"); err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Errorf("Expected the Prometheus error, got %v", err)
	}
}

func TestPrometheusQueryHistory(t *testing.T) {
	fake, server := newFakePrometheus(t, alphaResults("1")...)
	c := newTestPrometheusCollector(server.URL)
	if _, err := c.GetMetrics(); err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}

	fake.setResults(
		promResult{"node_cpu_seconds_total", matrix(samples(`"instance":"a:9100"`, `[60060,"10"]`, `[60120,"NaN"]`, `[60180,"30"]`))},
		promResult{"node_filesystem", matrix(
			samples(`"instance":"a:9100","device":"/dev/sdb1","mountpoint":"/data"`, `[60060,"99"]`),
			samples(`"instance":"a:9100","device":"/dev/sda1","mountpoint":"/"`, `[60060,"60"]`),
		)},
		promResult{"node_memory", matrix(samples(`"instance":"unknown:9100"`, `[60060,"50"]`))},
	)

	history, err := c.QueryHistory(time.Unix(60030, 0), time.Unix(60610, 0), time.Minute)
	if err != nil {
		t.Fatalf("QueryHistory failed: %v", err)
	}

	alpha := history["alpha"]
	if len(history) != 1 || alpha == nil {
		t.Fatalf("Expected history of alpha only, got %v", history)
	}

	// Points are stamped at the start of their step, NaN is skipped
	if len(alpha.CPU) != 2 || !alpha.CPU[0].Timestamp.Equal(time.Unix(60000, 0)) || alpha.CPU[1].Value != 30 {
		t.Errorf("Unexpected CPU history %+v", alpha.CPU)
	}
	// Only the root filesystem chosen for the current value
	if len(alpha.Disk) != 1 || alpha.Disk[0].Value != 60 {
		t.Errorf("Unexpected disk history %+v", alpha.Disk)
	}

	// The evaluation range is aligned to the step
	for _, params := range fake.params {
		if params.Get("start") == "" {
			continue
		}
		if params.Get("start") != "60060" || params.Get("end") != "60600" || params.Get("step") != "60s" {
			t.Errorf("Unexpected range parameters %v", params)
		}
		// The rate window is at least the rate interval
		if q := params.Get("query"); strings.Contains(q, "rate(") && !strings.Contains(q, "[120s]") {
			t.Errorf("Expected a 120s rate window in %s", q)
		}
	}
}
