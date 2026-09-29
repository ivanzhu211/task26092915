package funnel

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	stmts := []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS touchpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			customer_id TEXT NOT NULL,
			occurred_at INTEGER NOT NULL,
			channel TEXT NOT NULL,
			event TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_touchpoints_time ON touchpoints (occurred_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_touchpoints_customer ON touchpoints (customer_id, occurred_at, id)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) InsertBatch(tps []Touchpoint) error {
	if len(tps) > MaxBatchSize {
		return ErrBatchTooLarge
	}
	if len(tps) == 0 {
		return nil
	}
	for i, tp := range tps {
		if err := validateTouchpoint(tp); err != nil {
			return &BatchError{Index: i, Err: err}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO touchpoints (customer_id, occurred_at, channel, event) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, tp := range tps {
		if _, err := stmt.Exec(tp.CustomerID, tp.OccurredAt.Unix(), string(tp.Channel), string(tp.Event)); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

type row struct {
	customerID string
	occurredAt time.Time
	channel    Channel
	event      EventType
}

func (s *Store) loadRange(start, end time.Time) ([]row, error) {
	rows, err := s.db.Query(
		`SELECT customer_id, occurred_at, channel, event FROM touchpoints
		 WHERE occurred_at >= ? AND occurred_at < ?
		 ORDER BY customer_id, occurred_at, id`,
		start.Unix(), end.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		var ts int64
		var ch, ev string
		if err := rows.Scan(&r.customerID, &ts, &ch, &ev); err != nil {
			return nil, err
		}
		r.occurredAt = time.Unix(ts, 0).UTC()
		r.channel = Channel(ch)
		r.event = EventType(ev)
		out = append(out, r)
	}
	return out, rows.Err()
}
