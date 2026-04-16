package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"polypilot/core"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteOrderStore struct {
	db *sql.DB
}

type SQLiteExecutionStore struct {
	db *sql.DB
}

type SQLiteStateStore struct {
	db *sql.DB
}

func NewSQLiteStores(dbPath string) (OrderStore, ExecutionStore, StateStore, error) {
	if err := ensureDBDir(dbPath); err != nil {
		return nil, nil, nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := migrateSQLite(db); err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	return &SQLiteOrderStore{db: db}, &SQLiteExecutionStore{db: db}, &SQLiteStateStore{db: db}, nil
}

func migrateSQLite(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS orders (
			order_id TEXT PRIMARY KEY,
			market_id TEXT,
			token_id TEXT,
			side TEXT,
			price REAL,
			requested_size REAL,
			remaining_size REAL,
			reserved REAL,
			status TEXT,
			updated_at INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_id TEXT,
			parent_order_id TEXT,
			market_id TEXT,
			token_id TEXT,
			price REAL,
			side TEXT,
			requested_size REAL,
			filled_size REAL,
			status TEXT,
			reason TEXT,
			at INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_executions_at ON executions(at);`,
		`CREATE TABLE IF NOT EXISTS state_snapshot (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			available REAL,
			reserved REAL,
			buy REAL,
			sell REAL,
			at INTEGER
		);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteOrderStore) UpsertOrder(rec OrderRecord) error {
	_, err := s.db.Exec(`INSERT INTO orders (
		order_id, market_id, token_id, side, price, requested_size, remaining_size, reserved, status, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(order_id) DO UPDATE SET
		market_id=excluded.market_id,
		token_id=excluded.token_id,
		side=excluded.side,
		price=excluded.price,
		requested_size=excluded.requested_size,
		remaining_size=excluded.remaining_size,
		reserved=excluded.reserved,
		status=excluded.status,
		updated_at=excluded.updated_at`,
		rec.OrderID, rec.MarketID, rec.TokenID, rec.Side, rec.Price, rec.RequestedSize, rec.RemainingSize, rec.Reserved, string(rec.Status), rec.UpdatedAt,
	)
	return err
}

func (s *SQLiteOrderStore) GetOrder(orderID string) (OrderRecord, bool, error) {
	row := s.db.QueryRow(`SELECT order_id, market_id, token_id, side, price, requested_size, remaining_size, reserved, status, updated_at
		FROM orders WHERE order_id = ?`, orderID)
	var rec OrderRecord
	var status string
	err := row.Scan(&rec.OrderID, &rec.MarketID, &rec.TokenID, &rec.Side, &rec.Price, &rec.RequestedSize, &rec.RemainingSize, &rec.Reserved, &status, &rec.UpdatedAt)
	if err == sql.ErrNoRows {
		return OrderRecord{}, false, nil
	}
	if err != nil {
		return OrderRecord{}, false, err
	}
	rec.Status = core.ExecutionStatus(status)
	return rec, true, nil
}

func (s *SQLiteOrderStore) ListOpenOrders() ([]OrderRecord, error) {
	rows, err := s.db.Query(`SELECT order_id, market_id, token_id, side, price, requested_size, remaining_size, reserved, status, updated_at
		FROM orders WHERE status NOT IN (?, ?, ?)`,
		string(core.ExecutionStatusFilled), string(core.ExecutionStatusCancelled), string(core.ExecutionStatusRejected),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]OrderRecord, 0)
	for rows.Next() {
		var rec OrderRecord
		var status string
		if err := rows.Scan(&rec.OrderID, &rec.MarketID, &rec.TokenID, &rec.Side, &rec.Price, &rec.RequestedSize, &rec.RemainingSize, &rec.Reserved, &status, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		rec.Status = core.ExecutionStatus(status)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *SQLiteExecutionStore) AppendExecution(ev core.ExecutionEvent) error {
	_, err := s.db.Exec(`INSERT INTO executions (
		order_id, parent_order_id, market_id, token_id, price, side, requested_size, filled_size, status, reason, at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.OrderID, ev.ParentOrderID, ev.MarketID, ev.TokenID, ev.Price, ev.Side, ev.RequestedSize, ev.FilledSize, string(ev.Status), ev.Reason, ev.At.UnixNano(),
	)
	return err
}

func (s *SQLiteExecutionStore) ListExecutionsSince(unixNano int64) ([]core.ExecutionEvent, error) {
	rows, err := s.db.Query(`SELECT order_id, parent_order_id, market_id, token_id, price, side, requested_size, filled_size, status, reason, at
		FROM executions WHERE at >= ? ORDER BY at ASC, id ASC`, unixNano)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]core.ExecutionEvent, 0)
	for rows.Next() {
		var ev core.ExecutionEvent
		var status string
		var at int64
		if err := rows.Scan(&ev.OrderID, &ev.ParentOrderID, &ev.MarketID, &ev.TokenID, &ev.Price, &ev.Side, &ev.RequestedSize, &ev.FilledSize, &status, &ev.Reason, &at); err != nil {
			return nil, err
		}
		ev.Status = core.ExecutionStatus(status)
		ev.At = time.Unix(0, at)
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *SQLiteStateStore) SaveSnapshot(snapshot SnapshotRecord) error {
	_, err := s.db.Exec(`INSERT INTO state_snapshot (id, available, reserved, buy, sell, at)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			available=excluded.available,
			reserved=excluded.reserved,
			buy=excluded.buy,
			sell=excluded.sell,
			at=excluded.at`,
		snapshot.Available, snapshot.Reserved, snapshot.Buy, snapshot.Sell, snapshot.At,
	)
	return err
}

func (s *SQLiteStateStore) LoadLatestSnapshot() (SnapshotRecord, bool, error) {
	row := s.db.QueryRow(`SELECT available, reserved, buy, sell, at FROM state_snapshot WHERE id = 1`)
	var rec SnapshotRecord
	err := row.Scan(&rec.Available, &rec.Reserved, &rec.Buy, &rec.Sell, &rec.At)
	if err == sql.ErrNoRows {
		return SnapshotRecord{}, false, nil
	}
	if err != nil {
		return SnapshotRecord{}, false, err
	}
	return rec, true, nil
}

func ensureDBDir(dbPath string) error {
	dir := filepath.Dir(dbPath)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create sqlite dir failed: %w", err)
	}
	return nil
}
