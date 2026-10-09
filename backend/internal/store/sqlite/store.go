package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/work/bit/internal/types"
)

// Store persists logs, signals and trades in SQLite.
type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir sqlite dir: %w", err)
		}
	}
	// modernc DSN: file:PATH?pragmas — use forward slashes for Windows compatibility
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(abs) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS app_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  level TEXT NOT NULL,
  msg TEXT NOT NULL,
  attrs_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS signals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  symbol TEXT NOT NULL,
  action TEXT NOT NULL,
  reason TEXT NOT NULL,
  price REAL,
  ema20 REAL,
  ema60 REAL,
  ema200 REAL,
  atr REAL,
  adx REAL,
  stop_loss REAL,
  mode TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS trades (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  symbol TEXT NOT NULL,
  side TEXT NOT NULL,
  quantity REAL NOT NULL,
  price REAL NOT NULL,
  fee REAL NOT NULL DEFAULT 0,
  pnl REAL NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL,
  order_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_app_logs_ts ON app_logs(ts);
CREATE INDEX IF NOT EXISTS idx_signals_ts ON signals(ts);
CREATE INDEX IF NOT EXISTS idx_trades_ts ON trades(ts);
CREATE INDEX IF NOT EXISTS idx_trades_symbol ON trades(symbol);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Added after the first release; existing databases need them backfilled.
	for _, col := range []string{
		"donchian_up REAL",
		"donchian_dn REAL",
		"atr_pct REAL",
		"funding REAL",
	} {
		if err := s.addColumn("signals", col); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) addColumn(table, def string) error {
	_, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, def))
	if err != nil && strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}

func (s *Store) InsertLog(ctx context.Context, ts time.Time, level, msg string, attrs map[string]any) error {
	b, _ := json.Marshal(attrs)
	if b == nil {
		b = []byte("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_logs(ts, level, msg, attrs_json) VALUES(?,?,?,?)`,
		ts.UTC().Format(time.RFC3339Nano), level, msg, string(b),
	)
	return err
}

func (s *Store) InsertSignal(ctx context.Context, mode string, sig types.Signal) error {
	ts := sig.Time
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO signals(ts, symbol, action, reason, price, ema20, ema60, ema200, atr, adx, stop_loss, mode,
                    donchian_up, donchian_dn, atr_pct, funding)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ts.UTC().Format(time.RFC3339Nano),
		sig.Symbol, string(sig.Action), sig.Reason,
		sig.Price, nullNaN(sig.EMA20), nullNaN(sig.EMA60), nullNaN(sig.EMA200),
		nullNaN(sig.ATR), nullNaN(sig.ADX), sig.StopLoss,
		mode,
		nullNaN(sig.DonchianUp), nullNaN(sig.DonchianDn), nullNaN(sig.ATRPct), nullNaN(sig.Funding),
	)
	return err
}

func (s *Store) InsertTrade(ctx context.Context, mode, orderID string, fill types.TradeFill) error {
	ts := fill.Time
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO trades(ts, symbol, side, quantity, price, fee, pnl, reason, mode, order_id)
VALUES(?,?,?,?,?,?,?,?,?,?)`,
		ts.UTC().Format(time.RFC3339Nano),
		fill.Symbol, string(fill.Side), fill.Quantity, fill.Price,
		fill.Fee, fill.PNL, fill.Reason, mode, orderID,
	)
	return err
}

// TradeExists reports whether a row with this order_id is already stored.
func (s *Store) TradeExists(ctx context.Context, mode, orderID string) (bool, error) {
	if s == nil || orderID == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM trades WHERE mode = ? AND order_id = ?`, mode, orderID,
	).Scan(&n)
	return n > 0, err
}

// LastBinanceTradeID is the highest imported exchange fill id (order_id "bt-<id>").
func (s *Store) LastBinanceTradeID(ctx context.Context, mode, symbol string) (int64, error) {
	if s == nil {
		return 0, nil
	}
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT MAX(CAST(SUBSTR(order_id, 4) AS INTEGER)) FROM trades
 WHERE mode = ? AND symbol = ? AND order_id LIKE 'bt-%'`, mode, symbol).Scan(&v)
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

// FirstTradeTime is the oldest local fill for this symbol/mode.
func (s *Store) FirstTradeTime(ctx context.Context, mode, symbol string) (time.Time, bool, error) {
	if s == nil {
		return time.Time{}, false, nil
	}
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT MIN(ts) FROM trades WHERE mode = ? AND symbol = ?`, mode, symbol,
	).Scan(&raw)
	if err != nil {
		return time.Time{}, false, err
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, false, nil
	}
	ts, err := time.Parse(time.RFC3339Nano, raw.String)
	if err != nil {
		ts, err = time.Parse(time.RFC3339, raw.String)
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return ts.UTC(), true, nil
}

func (s *Store) DB() *sql.DB { return s.db }

// nullNaN maps non-finite indicator values to NULL; the driver rejects NaN.
func nullNaN(v float64) any {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return v
}
