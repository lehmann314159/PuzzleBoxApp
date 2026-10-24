// Package store keeps boxes and the request trace in one SQLite file. The schema matches the
// Python demo's, so either server can open the other's database.
package store

import (
	"database/sql"
	"encoding/json"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"puzzleboxapp/internal/box"
)

type Store struct{ db *sql.DB }

// Open opens (and creates) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?_busy_timeout=10000&_journal_mode=WAL&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS boxes(id TEXT PRIMARY KEY, state TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS trace(id INTEGER PRIMARY KEY AUTOINCREMENT, ts REAL, instance TEXT,
			box TEXT, status INTEGER, ms REAL, doc TEXT)`,
	} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Load returns nil, nil when there is no such box.
func (s *Store) Load(id string) (*box.State, error) {
	var raw string
	err := s.db.QueryRow(`SELECT state FROM boxes WHERE id=?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := &box.State{}
	return st, json.Unmarshal([]byte(raw), st)
}

func (s *Store) Save(id string, st *box.State) error {
	b, _ := json.Marshal(st)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO boxes(id,state) VALUES(?,?)`, id, string(b))
	return err
}

// Update loads, mutates and saves one box inside a single write transaction.
func (s *Store) Update(id string, fn func(*box.State)) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRow(`SELECT state FROM boxes WHERE id=?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	st := &box.State{}
	if err := json.Unmarshal([]byte(raw), st); err != nil {
		return false, err
	}
	fn(st)
	b, _ := json.Marshal(st)
	if _, err := tx.Exec(`UPDATE boxes SET state=? WHERE id=?`, string(b), id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// TraceRow is one recorded request.
type TraceRow struct {
	ID       int64           `json:"id"`
	TS       float64         `json:"ts"`
	Instance string          `json:"instance"`
	Box      string          `json:"box"`
	Status   int             `json:"status"`
	MS       float64         `json:"ms"`
	Doc      json.RawMessage `json:"doc"`
}

// MarshalJSON flattens the recorded document into the row, which is the shape the viewer reads.
func (r TraceRow) MarshalJSON() ([]byte, error) {
	flat := map[string]any{}
	_ = json.Unmarshal(r.Doc, &flat)
	flat["id"], flat["ts"], flat["instance"], flat["box"], flat["status"], flat["ms"] =
		r.ID, r.TS, r.Instance, r.Box, r.Status, r.MS
	return json.Marshal(flat)
}

// RecordTrace stores one request and keeps only the newest 2000.
func (s *Store) RecordTrace(instance, boxID string, status int, ms float64, doc any) error {
	b, _ := json.Marshal(doc)
	if _, err := s.db.Exec(`INSERT INTO trace(ts,instance,box,status,ms,doc) VALUES(?,?,?,?,?,?)`,
		float64(time.Now().UnixNano())/1e9, instance, boxID, status, ms, string(b)); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM trace WHERE id < (SELECT MAX(id) FROM trace) - 2000`)
	return err
}

// Trace returns up to 200 rows after since, optionally for one box, and the newest id overall.
func (s *Store) Trace(since int64, boxID string) ([]TraceRow, int64, error) {
	q, args := `SELECT id,ts,instance,box,status,ms,doc FROM trace WHERE id>?`, []any{since}
	if boxID != "" {
		q, args = q+` AND box=?`, append(args, boxID)
	}
	rows, err := s.db.Query(q+` ORDER BY id LIMIT 200`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []TraceRow{}
	for rows.Next() {
		var r TraceRow
		var doc string
		if err := rows.Scan(&r.ID, &r.TS, &r.Instance, &r.Box, &r.Status, &r.MS, &doc); err != nil {
			return nil, 0, err
		}
		r.Doc = json.RawMessage(doc)
		out = append(out, r)
	}
	var last sql.NullInt64
	s.db.QueryRow(`SELECT MAX(id) FROM trace`).Scan(&last)
	return out, last.Int64, rows.Err()
}

// RecentBoxes lists the 12 boxes most recently seen in the trace.
func (s *Store) RecentBoxes() ([]string, error) {
	rows, err := s.db.Query(`SELECT box FROM trace WHERE box!='' GROUP BY box ORDER BY MAX(id) DESC LIMIT 12`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LatestBox is the box in the newest trace row that has one, or "".
func (s *Store) LatestBox() string {
	var b string
	s.db.QueryRow(`SELECT box FROM trace WHERE box!='' ORDER BY id DESC LIMIT 1`).Scan(&b)
	return b
}
