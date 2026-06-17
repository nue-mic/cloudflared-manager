// Package metrics provides time-series storage and a sampler that polls each
// running cloudflared worker's metrics endpoint for traffic/connection stats, plus a simple
// threshold alert engine. Storage is pure-Go SQLite (modernc.org/sqlite), so
// the single-binary, cgo-free build is preserved.
package metrics

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// TrafficPoint is one sampled metric row. In/Out are interval deltas (bytes
// since the previous sample, midnight-reset corrected); Conns is the
// instantaneous connection count at sample time.
type TrafficPoint struct {
	Ts     int64  `json:"ts"`
	InstID string `json:"inst_id"`
	Scope  string `json:"scope"` // "server" | "proxy"
	Key    string `json:"key"`   // proxy name, or "" for server scope
	In     int64  `json:"in"`
	Out    int64  `json:"out"`
	Conns  int64  `json:"conns"`
}

// Store wraps the SQLite database holding traffic_points, alert_rules and
// alert_events.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the metrics SQLite database at path and
// ensures the schema exists.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc sqlite: serialize to avoid lock churn
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS traffic_points (
  ts        INTEGER NOT NULL,
  inst_id   TEXT    NOT NULL,
  scope     TEXT    NOT NULL,
  key       TEXT    NOT NULL,
  in_bytes  INTEGER NOT NULL,
  out_bytes INTEGER NOT NULL,
  conns     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tp_lookup ON traffic_points(inst_id, scope, key, ts);

CREATE TABLE IF NOT EXISTS alert_rules (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  enabled     INTEGER NOT NULL,
  inst_id     TEXT NOT NULL,
  metric      TEXT NOT NULL,
  op          TEXT NOT NULL,
  threshold   REAL NOT NULL,
  for_seconds INTEGER NOT NULL,
  target      TEXT NOT NULL,
  webhook     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_events (
  id          TEXT PRIMARY KEY,
  rule_id     TEXT NOT NULL,
  inst_id     TEXT NOT NULL,
  target      TEXT NOT NULL,
  fired_at    INTEGER NOT NULL,
  resolved_at INTEGER NOT NULL,
  value       REAL NOT NULL,
  state       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ae_lookup ON alert_events(state, fired_at);
`
	_, err := s.db.Exec(schema)
	return err
}

// InsertTraffic appends sampled traffic points in a single transaction.
func (s *Store) InsertTraffic(points []TrafficPoint) error {
	if len(points) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO traffic_points(ts,inst_id,scope,key,in_bytes,out_bytes,conns) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, p := range points {
		if _, err := stmt.Exec(p.Ts, p.InstID, p.Scope, p.Key, p.In, p.Out, p.Conns); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SeriesPoint is one downsampled bucket returned by QueryTraffic.
type SeriesPoint struct {
	Ts    int64 `json:"ts"`
	In    int64 `json:"in"`
	Out   int64 `json:"out"`
	Conns int64 `json:"conns"`
}

// QueryTraffic returns downsampled traffic for one (inst,scope,key) between
// [from,to] (unix seconds), bucketed by step seconds. In/Out are summed
// within a bucket (interval deltas), Conns is the max within the bucket.
func (s *Store) QueryTraffic(instID, scope, key string, from, to, step int64) ([]SeriesPoint, error) {
	if step <= 0 {
		step = 60
	}
	rows, err := s.db.Query(
		`SELECT (ts/?)*? AS bucket, SUM(in_bytes), SUM(out_bytes), MAX(conns)
		   FROM traffic_points
		  WHERE inst_id=? AND scope=? AND key=? AND ts>=? AND ts<=?
		  GROUP BY bucket ORDER BY bucket`,
		step, step, instID, scope, key, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SeriesPoint, 0)
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(&p.Ts, &p.In, &p.Out, &p.Conns); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PruneBefore deletes traffic points older than the given unix-second cutoff,
// returning the number of rows removed. Keeps the db from growing unbounded.
func (s *Store) PruneBefore(cutoff int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM traffic_points WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ping is a tiny sanity helper used by tests.
func (s *Store) ping() error {
	if s.db == nil {
		return fmt.Errorf("nil db")
	}
	return s.db.Ping()
}
