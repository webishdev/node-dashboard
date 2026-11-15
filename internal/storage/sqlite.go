package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/webishdev/node-dashboard/internal/metrics"
	_ "modernc.org/sqlite"
)

// DBFileName is the name of the SQLite database file inside the data directory
const DBFileName = "node-dashboard.db"

const schema = `
CREATE TABLE IF NOT EXISTS samples (
	node   TEXT    NOT NULL,
	ts     INTEGER NOT NULL,
	cpu    REAL    NOT NULL,
	memory REAL    NOT NULL,
	disk   REAL    NOT NULL,
	net_rx REAL    NOT NULL DEFAULT 0,
	net_tx REAL    NOT NULL DEFAULT 0,
	PRIMARY KEY (node, ts)
) WITHOUT ROWID;`

// addedColumns lists columns added after the first release, with their definition,
// so databases created by an older version can be upgraded
var addedColumns = []struct{ name, definition string }{
	{"net_rx", "REAL NOT NULL DEFAULT 0"},
	{"net_tx", "REAL NOT NULL DEFAULT 0"},
}

// SQLiteStore persists metric samples in a SQLite database
type SQLiteStore struct {
	db *sql.DB
}

// Open creates the data directory if needed and opens (or creates) the database in it
func Open(dataDir string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %w", dataDir, err)
	}

	path := filepath.Join(dataDir, DBFileName)
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", path, err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize database %s: %w", path, err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate database %s: %w", path, err)
	}

	return &SQLiteStore{db: db}, nil
}

// migrate adds the columns missing in databases created by an older version
func migrate(db *sql.DB) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info('samples')`)
	if err != nil {
		return err
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, column := range addedColumns {
		if existing[column.name] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE samples ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
			return err
		}
	}
	return nil
}

// Add stores one sample for a node
func (s *SQLiteStore) Add(node string, ts time.Time, data *metrics.MetricsData) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO samples (node, ts, cpu, memory, disk, net_rx, net_tx) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		node, ts.Unix(), data.CPU, data.Memory, data.Disk, data.NetRxBps, data.NetTxBps,
	)
	return err
}

// Query returns samples of all nodes since the given time, averaged into buckets of the given size
func (s *SQLiteStore) Query(since time.Time, bucket time.Duration) (map[string]*metrics.HistoricalMetrics, error) {
	bucketSeconds := int64(bucket / time.Second)
	if bucketSeconds < 1 {
		bucketSeconds = 1
	}

	rows, err := s.db.Query(
		`SELECT node, (ts / ?) * ? AS bucket, AVG(cpu), AVG(memory), AVG(disk), AVG(net_rx), AVG(net_tx)
		 FROM samples WHERE ts >= ?
		 GROUP BY node, bucket ORDER BY node, bucket`,
		bucketSeconds, bucketSeconds, since.Unix(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*metrics.HistoricalMetrics)
	for rows.Next() {
		var node string
		var bucketStart int64
		var cpu, memory, disk, netRx, netTx float64
		if err := rows.Scan(&node, &bucketStart, &cpu, &memory, &disk, &netRx, &netTx); err != nil {
			return nil, err
		}

		historical := result[node]
		if historical == nil {
			historical = &metrics.HistoricalMetrics{}
			result[node] = historical
		}

		ts := time.Unix(bucketStart, 0)
		historical.CPU = append(historical.CPU, metrics.DataPoint{Timestamp: ts, Value: cpu})
		historical.Memory = append(historical.Memory, metrics.DataPoint{Timestamp: ts, Value: memory})
		historical.Disk = append(historical.Disk, metrics.DataPoint{Timestamp: ts, Value: disk})
		historical.NetRx = append(historical.NetRx, metrics.DataPoint{Timestamp: ts, Value: netRx})
		historical.NetTx = append(historical.NetTx, metrics.DataPoint{Timestamp: ts, Value: netTx})
	}

	return result, rows.Err()
}

// Prune deletes all samples older than the given time and returns how many were removed
func (s *SQLiteStore) Prune(before time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM samples WHERE ts < ?`, before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Close closes the database
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
