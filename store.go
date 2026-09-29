package funnel

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store 触点存储，底层为本地 SQLite 文件。
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS touchpoints (
	seq         INTEGER PRIMARY KEY AUTOINCREMENT,
	customer_id TEXT    NOT NULL,
	occurred_at INTEGER NOT NULL, -- Unix 秒
	channel     TEXT    NOT NULL,
	event_type  TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_touchpoints_time ON touchpoints (occurred_at, seq);
CREATE INDEX IF NOT EXISTS idx_touchpoints_customer ON touchpoints (customer_id, occurred_at, seq);
`

// Open 打开（必要时创建）位于 path 的 SQLite 触点库。
// path 传 ":memory:" 时使用内存库（主要用于测试）。
func Open(path string) (*Store, error) {
	dsn := path
	if path == ":memory:" {
		// 单连接下内存库内容稳定可见。
		dsn = ":memory:"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("funnel: open store: %w", err)
	}
	// 单连接：保证 :memory: 库一致性，也避免 SQLite 写锁竞争。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("funnel: init schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close 关闭存储。
func (s *Store) Close() error {
	return s.db.Close()
}

// InsertBatch 批量写入触点，每批最多 MaxBatchSize 条。
// 批内任一记录不合法（渠道或事件类型不在清单、客户为空）则整批拒收，
// 返回 *BatchError 指明第一条不合法记录的下标。
func (s *Store) InsertBatch(ctx context.Context, batch []Touchpoint) error {
	if len(batch) == 0 {
		return nil
	}
	if len(batch) > MaxBatchSize {
		return fmt.Errorf("funnel: batch size %d exceeds max %d", len(batch), MaxBatchSize)
	}
	for i, t := range batch {
		if t.CustomerID == "" {
			return &BatchError{Index: i, Reason: "empty customer id"}
		}
		if !validChannels[t.Channel] {
			return &BatchError{Index: i, Reason: fmt.Sprintf("invalid channel %q", t.Channel)}
		}
		if !validEventTypes[t.EventType] {
			return &BatchError{Index: i, Reason: fmt.Sprintf("invalid event type %q", t.EventType)}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("funnel: begin tx: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO touchpoints (customer_id, occurred_at, channel, event_type) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("funnel: prepare insert: %w", err)
	}
	defer stmt.Close()
	for _, t := range batch {
		if _, err := stmt.ExecContext(ctx, t.CustomerID, t.OccurredAt.Unix(), string(t.Channel), string(t.EventType)); err != nil {
			return fmt.Errorf("funnel: insert touchpoint: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("funnel: commit: %w", err)
	}
	return nil
}

// StoredTouchpoint 从库中读出的触点，Seq 为全局写入顺序（同一秒先写先排）。
type StoredTouchpoint struct {
	Touchpoint
	Seq int64
}

// Touchpoints 读取 [start, end) 内的触点，按 (OccurredAt, Seq) 升序返回。
func (s *Store) Touchpoints(ctx context.Context, start, end time.Time) ([]StoredTouchpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, customer_id, occurred_at, channel, event_type
		 FROM touchpoints
		 WHERE occurred_at >= ? AND occurred_at < ?
		 ORDER BY occurred_at, seq`, start.Unix(), end.Unix())
	if err != nil {
		return nil, fmt.Errorf("funnel: query touchpoints: %w", err)
	}
	defer rows.Close()
	var out []StoredTouchpoint
	for rows.Next() {
		var st StoredTouchpoint
		var sec int64
		var ch, ev string
		if err := rows.Scan(&st.Seq, &st.CustomerID, &sec, &ch, &ev); err != nil {
			return nil, fmt.Errorf("funnel: scan touchpoint: %w", err)
		}
		st.OccurredAt = time.Unix(sec, 0).UTC()
		st.Channel = Channel(ch)
		st.EventType = EventType(ev)
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("funnel: iterate touchpoints: %w", err)
	}
	return out, nil
}

// scanByCustomer 按 (customer_id, occurred_at, seq) 顺序流式扫描 [start, end)
// 内的触点，每集齐一个客户就回调一次。内存占用只与单客户最大触点数有关。
func (s *Store) scanByCustomer(ctx context.Context, start, end time.Time, fn func(customerID string, pts []StoredTouchpoint) error) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, customer_id, occurred_at, channel, event_type
		 FROM touchpoints
		 WHERE occurred_at >= ? AND occurred_at < ?
		 ORDER BY customer_id, occurred_at, seq`, start.Unix(), end.Unix())
	if err != nil {
		return fmt.Errorf("funnel: query touchpoints: %w", err)
	}
	defer rows.Close()

	var curID string
	var buf []StoredTouchpoint
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		err := fn(curID, buf)
		buf = buf[:0]
		return err
	}
	for rows.Next() {
		var st StoredTouchpoint
		var sec int64
		var ch, ev string
		if err := rows.Scan(&st.Seq, &st.CustomerID, &sec, &ch, &ev); err != nil {
			return fmt.Errorf("funnel: scan touchpoint: %w", err)
		}
		st.OccurredAt = time.Unix(sec, 0).UTC()
		st.Channel = Channel(ch)
		st.EventType = EventType(ev)
		if len(buf) > 0 && st.CustomerID != curID {
			if err := flush(); err != nil {
				return err
			}
		}
		curID = st.CustomerID
		buf = append(buf, st)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("funnel: iterate touchpoints: %w", err)
	}
	return flush()
}
