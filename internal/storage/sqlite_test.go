package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webishdev/node-dashboard/internal/metrics"
)

func openTestStore(t *testing.T, dir string) *SQLiteStore {
	t.Helper()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func addSample(t *testing.T, store *SQLiteStore, node string, ts time.Time, cpu, memory, disk float64) {
	t.Helper()
	if err := store.Add(node, ts, &metrics.MetricsData{CPU: cpu, Memory: memory, Disk: disk}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
}

func TestOpenCreatesDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	openTestStore(t, dir)

	if _, err := os.Stat(filepath.Join(dir, DBFileName)); err != nil {
		t.Errorf("Expected database file to exist: %v", err)
	}

	// Opening an existing database must not fail on the schema
	openTestStore(t, dir)
}

func TestAddAndQuery(t *testing.T) {
	store := openTestStore(t, t.TempDir())
	base := time.Unix(1_700_000_000, 0)

	addSample(t, store, "node-a", base, 10, 20, 30)
	addSample(t, store, "node-a", base.Add(5*time.Second), 20, 40, 30)
	addSample(t, store, "node-b", base, 50, 60, 70)

	data, err := store.Query(base, time.Second)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(data) != 2 {
		t.Fatalf("Expected 2 nodes, got %d", len(data))
	}

	a := data["node-a"]
	if len(a.CPU) != 2 || a.CPU[0].Value != 10 || a.CPU[1].Value != 20 {
		t.Errorf("Unexpected CPU history for node-a: %+v", a.CPU)
	}
	if a.Memory[1].Value != 40 || a.Disk[1].Value != 30 {
		t.Errorf("Unexpected memory or disk history for node-a: %+v %+v", a.Memory, a.Disk)
	}
	if !a.CPU[0].Timestamp.Equal(base) {
		t.Errorf("Expected first timestamp %v, got %v", base, a.CPU[0].Timestamp)
	}

	if b := data["node-b"]; len(b.CPU) != 1 || b.CPU[0].Value != 50 {
		t.Errorf("Unexpected CPU history for node-b: %+v", b.CPU)
	}
}

func TestQueryAveragesBuckets(t *testing.T) {
	store := openTestStore(t, t.TempDir())
	base := time.Unix(1_699_999_980, 0) // divisible by 60

	addSample(t, store, "node", base, 10, 0, 0)
	addSample(t, store, "node", base.Add(30*time.Second), 30, 0, 0)
	addSample(t, store, "node", base.Add(60*time.Second), 50, 0, 0)

	data, err := store.Query(base, time.Minute)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	cpu := data["node"].CPU
	if len(cpu) != 2 {
		t.Fatalf("Expected 2 buckets, got %d", len(cpu))
	}
	if cpu[0].Value != 20 || cpu[1].Value != 50 {
		t.Errorf("Expected bucket averages 20 and 50, got %v and %v", cpu[0].Value, cpu[1].Value)
	}
	if !cpu[1].Timestamp.Equal(base.Add(time.Minute)) {
		t.Errorf("Expected second bucket to start at %v, got %v", base.Add(time.Minute), cpu[1].Timestamp)
	}
}

func TestQuerySince(t *testing.T) {
	store := openTestStore(t, t.TempDir())
	base := time.Unix(1_700_000_000, 0)

	addSample(t, store, "node", base, 10, 0, 0)
	addSample(t, store, "node", base.Add(time.Hour), 20, 0, 0)

	data, err := store.Query(base.Add(time.Minute), time.Second)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if cpu := data["node"].CPU; len(cpu) != 1 || cpu[0].Value != 20 {
		t.Errorf("Expected only the newer sample, got %+v", cpu)
	}
}

func TestPrune(t *testing.T) {
	store := openTestStore(t, t.TempDir())
	base := time.Unix(1_700_000_000, 0)

	addSample(t, store, "node", base, 10, 0, 0)
	addSample(t, store, "node", base.Add(time.Hour), 20, 0, 0)

	removed, err := store.Prune(base.Add(time.Minute))
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if removed != 1 {
		t.Errorf("Expected 1 removed sample, got %d", removed)
	}

	data, err := store.Query(time.Unix(0, 0), time.Second)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if cpu := data["node"].CPU; len(cpu) != 1 || cpu[0].Value != 20 {
		t.Errorf("Expected only the newer sample after prune, got %+v", cpu)
	}
}

func TestDataSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1_700_000_000, 0)

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	addSample(t, store, "node", base, 42, 0, 0)
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	reopened := openTestStore(t, dir)
	data, err := reopened.Query(base, time.Second)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if cpu := data["node"].CPU; len(cpu) != 1 || cpu[0].Value != 42 {
		t.Errorf("Expected persisted sample, got %+v", cpu)
	}
}

func TestNetworkRoundTrip(t *testing.T) {
	store := openTestStore(t, t.TempDir())
	base := time.Unix(1_700_000_000, 0)

	if err := store.Add("node", base, &metrics.MetricsData{NetRxBps: 1000, NetTxBps: 200}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if err := store.Add("node", base.Add(time.Second), &metrics.MetricsData{NetRxBps: 3000, NetTxBps: 400}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	data, err := store.Query(base, 10*time.Second)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	h := data["node"]
	if len(h.NetRx) != 1 || h.NetRx[0].Value != 2000 || len(h.NetTx) != 1 || h.NetTx[0].Value != 300 {
		t.Errorf("Expected averaged network history 2000/300, got %+v %+v", h.NetRx, h.NetTx)
	}
}

func TestMigrateAddsNetworkColumns(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1_700_000_000, 0)

	// Create a database with the schema of the previous version
	db, err := sql.Open("sqlite", filepath.Join(dir, DBFileName))
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE samples (
		node TEXT NOT NULL, ts INTEGER NOT NULL, cpu REAL NOT NULL, memory REAL NOT NULL, disk REAL NOT NULL,
		PRIMARY KEY (node, ts)) WITHOUT ROWID`); err != nil {
		t.Fatalf("Creating old schema failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO samples VALUES ('node', ?, 42, 0, 0)`, base.Unix()); err != nil {
		t.Fatalf("Inserting old sample failed: %v", err)
	}
	db.Close()

	store := openTestStore(t, dir)
	addSample(t, store, "node", base.Add(time.Second), 10, 0, 0)

	data, err := store.Query(base, time.Second)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	h := data["node"]
	if len(h.CPU) != 2 || h.CPU[0].Value != 42 || len(h.NetRx) != 2 || h.NetRx[0].Value != 0 {
		t.Errorf("Expected old sample with zero network values, got %+v %+v", h.CPU, h.NetRx)
	}
}
