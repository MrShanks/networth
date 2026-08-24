// Package store persists accounts, funds and their snapshots in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	_ "modernc.org/sqlite"

	"github.com/MrShanks/networh/internal/money"
)

const (
	KindAsset     = "asset"
	KindLiability = "liability"

	// Base is the currency every total is reported in.
	Base = "CHF"
)

// Currencies are the currencies the app knows how to handle.
var Currencies = []string{"CHF", "EUR", "USD"}

// Foreign lists the currencies that need an exchange rate.
func Foreign() []string {
	return slices.DeleteFunc(slices.Clone(Currencies), func(c string) bool { return c == Base })
}

var ErrNotFound = errors.New("not found")

type Account struct {
	ID       int64
	Name     string
	Kind     string
	Currency string
}

func (a Account) IsLiability() bool { return a.Kind == KindLiability }

// Fund is an ETF or similar instrument held inside an account.
type Fund struct {
	ID          int64
	AccountID   int64
	AccountName string
	Name        string
	Ticker      string
	Currency    string
}

// Rate is the value of one unit of Currency expressed in the base currency.
type Rate struct {
	Currency string
	AsOf     string
	Rate     float64
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS accounts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL CHECK (kind IN ('asset','liability')),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS balances (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    as_of      TEXT NOT NULL,
    cents      INTEGER NOT NULL,
    UNIQUE (account_id, as_of)
);

CREATE TABLE IF NOT EXISTS funds (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    ticker     TEXT NOT NULL DEFAULT '',
    currency   TEXT NOT NULL,
    UNIQUE (account_id, name)
);

CREATE TABLE IF NOT EXISTS trades (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    fund_id     INTEGER NOT NULL REFERENCES funds(id) ON DELETE CASCADE,
    as_of       TEXT NOT NULL,
    units       REAL NOT NULL,    -- positive when bought, negative when sold
    price_cents INTEGER NOT NULL  -- price paid per unit, in the fund's currency
);

CREATE TABLE IF NOT EXISTS prices (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    fund_id     INTEGER NOT NULL REFERENCES funds(id) ON DELETE CASCADE,
    as_of       TEXT NOT NULL,
    price_cents INTEGER NOT NULL,
    UNIQUE (fund_id, as_of)
);

CREATE TABLE IF NOT EXISTS rates (
    currency TEXT NOT NULL,
    as_of    TEXT NOT NULL,
    rate     REAL NOT NULL,
    PRIMARY KEY (currency, as_of)
);

CREATE INDEX IF NOT EXISTS balances_as_of ON balances(as_of);
CREATE INDEX IF NOT EXISTS trades_as_of ON trades(as_of);
CREATE INDEX IF NOT EXISTS prices_as_of ON prices(as_of);
`

// addedColumns extend the original schema; SQLite has no "ADD COLUMN IF NOT
// EXISTS", so they are skipped when already present.
var addedColumns = []struct{ table, column, ddl string }{
	{"accounts", "currency", "ALTER TABLE accounts ADD COLUMN currency TEXT NOT NULL DEFAULT 'EUR'"},
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if _, err := db.Exec(expensesSchema); err != nil {
		return err
	}
	for _, c := range addedColumns {
		ok, err := hasColumn(db, c.table, c.column)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if _, err := db.Exec(c.ddl); err != nil {
			return fmt.Errorf("add %s.%s: %w", c.table, c.column, err)
		}
	}
	if err := migrateLegacyETFAccounts(db); err != nil {
		return err
	}
	if err := migrateSnapshotsToTrades(db); err != nil {
		return err
	}
	return adoptBase(db)
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
	return n > 0, err
}

// migrateLegacyETFAccounts moves the first-generation layout, where an ETF was
// an account of its own, into the funds tables.
func migrateLegacyETFAccounts(db *sql.DB) error {
	legacy, err := hasColumn(db, "accounts", "asset_class")
	if err != nil || !legacy {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmts := []string{
		// Staged as snapshots; migrateSnapshotsToTrades converts and drops them.
		`CREATE TABLE IF NOT EXISTS fund_snapshots (
		    id          INTEGER PRIMARY KEY AUTOINCREMENT,
		    fund_id     INTEGER NOT NULL,
		    as_of       TEXT NOT NULL,
		    units       REAL NOT NULL,
		    price_cents INTEGER NOT NULL
		)`,
		`INSERT INTO funds (account_id, name, ticker, currency)
		 SELECT id, name, ticker, '` + Base + `' FROM accounts WHERE asset_class = 'etf'`,
		`INSERT INTO fund_snapshots (fund_id, as_of, units, price_cents)
		 SELECT f.id, b.as_of, b.units, b.price_cents
		 FROM balances b
		 JOIN funds f ON f.account_id = b.account_id
		 WHERE b.units IS NOT NULL`,
		`DELETE FROM balances WHERE units IS NOT NULL`,
		`ALTER TABLE accounts DROP COLUMN asset_class`,
		`ALTER TABLE accounts DROP COLUMN ticker`,
		`ALTER TABLE balances DROP COLUMN units`,
		`ALTER TABLE balances DROP COLUMN price_cents`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("legacy migration: %w", err)
		}
	}
	return tx.Commit()
}

// migrateSnapshotsToTrades turns the older "units held on a date" records into
// explicit trades plus price marks; a rise in units becomes a purchase at that
// day's price, which is what the basis was inferred from anyway.
func migrateSnapshotsToTrades(db *sql.DB) error {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'fund_snapshots'`).Scan(&n)
	if err != nil || n == 0 {
		return err
	}

	type snapshot struct {
		fundID int64
		asOf   string
		units  float64
		price  int64
	}
	var snapshots []snapshot
	rows, err := db.Query(
		`SELECT fund_id, as_of, units, price_cents FROM fund_snapshots ORDER BY fund_id, as_of, id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s snapshot
		if err := rows.Scan(&s.fundID, &s.asOf, &s.units, &s.price); err != nil {
			rows.Close()
			return err
		}
		snapshots = append(snapshots, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var (
		current int64
		held    float64
	)
	for _, s := range snapshots {
		if s.fundID != current {
			current, held = s.fundID, 0
		}
		if delta := s.units - held; delta != 0 {
			if _, err := tx.Exec(
				`INSERT INTO trades (fund_id, as_of, units, price_cents) VALUES (?, ?, ?, ?)`,
				s.fundID, s.asOf, delta, s.price); err != nil {
				return fmt.Errorf("snapshot migration: %w", err)
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO prices (fund_id, as_of, price_cents) VALUES (?, ?, ?)`,
			s.fundID, s.asOf, s.price); err != nil {
			return fmt.Errorf("snapshot migration: %w", err)
		}
		held = s.units
	}

	if _, err := tx.Exec(`DROP TABLE fund_snapshots`); err != nil {
		return err
	}
	return tx.Commit()
}

// adoptBase records the reporting currency and drops rates cached against a
// different one.
func adoptBase(db *sql.DB) error {
	var stored string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = 'base_currency'`).Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('base_currency', ?)`, Base)
		return err
	case err != nil:
		return err
	case stored != Base:
		if _, err := db.Exec(`DELETE FROM rates`); err != nil {
			return err
		}
		_, err = db.Exec(`UPDATE settings SET value = ? WHERE key = 'base_currency'`, Base)
		return err
	}
	return nil
}

func (s *Store) CreateAccount(ctx context.Context, name, kind, currency string) error {
	if kind != KindAsset && kind != KindLiability {
		return fmt.Errorf("unknown account kind %q", kind)
	}
	if err := checkCurrency(currency); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO accounts (name, kind, currency) VALUES (?, ?, ?)`, name, kind, currency)
	return err
}

func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	return s.deleteRow(ctx, `DELETE FROM accounts WHERE id = ?`, id)
}

func (s *Store) CreateFund(ctx context.Context, accountID int64, name, ticker, currency string) error {
	if err := checkCurrency(currency); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO funds (account_id, name, ticker, currency) VALUES (?, ?, ?, ?)`,
		accountID, name, ticker, currency)
	return err
}

func (s *Store) DeleteFund(ctx context.Context, id int64) error {
	return s.deleteRow(ctx, `DELETE FROM funds WHERE id = ?`, id)
}

// SetBalance records (or overwrites) a cash balance in the account's currency.
func (s *Store) SetBalance(ctx context.Context, accountID int64, asOf string, amount money.Amount) error {
	if err := checkDate(asOf); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO balances (account_id, as_of, cents) VALUES (?, ?, ?)
        ON CONFLICT (account_id, as_of) DO UPDATE SET cents = excluded.cents`,
		accountID, asOf, int64(amount))
	return err
}

func (s *Store) DeleteBalance(ctx context.Context, id int64) error {
	return s.deleteRow(ctx, `DELETE FROM balances WHERE id = ?`, id)
}

// AddTrade records a purchase (positive units) or a sale (negative units) at
// the price actually paid, which is what fixes the cost basis.
func (s *Store) AddTrade(ctx context.Context, fundID int64, asOf string, units float64, price money.Amount) error {
	if err := checkDate(asOf); err != nil {
		return err
	}
	if units == 0 {
		return errors.New("units cannot be zero")
	}
	if price < 0 {
		return errors.New("price cannot be negative")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO trades (fund_id, as_of, units, price_cents) VALUES (?, ?, ?, ?)`,
		fundID, asOf, units, int64(price)); err != nil {
		return err
	}
	// The traded price is also a valid mark for that day.
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO prices (fund_id, as_of, price_cents) VALUES (?, ?, ?)
        ON CONFLICT (fund_id, as_of) DO UPDATE SET price_cents = excluded.price_cents`,
		fundID, asOf, int64(price)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteTrade(ctx context.Context, id int64) error {
	return s.deleteRow(ctx, `DELETE FROM trades WHERE id = ?`, id)
}

// SetPrice records what a unit of the fund is worth on a date.
func (s *Store) SetPrice(ctx context.Context, fundID int64, asOf string, price money.Amount) error {
	if err := checkDate(asOf); err != nil {
		return err
	}
	if price < 0 {
		return errors.New("price cannot be negative")
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO prices (fund_id, as_of, price_cents) VALUES (?, ?, ?)
        ON CONFLICT (fund_id, as_of) DO UPDATE SET price_cents = excluded.price_cents`,
		fundID, asOf, int64(price))
	return err
}

func (s *Store) DeletePrice(ctx context.Context, id int64) error {
	return s.deleteRow(ctx, `DELETE FROM prices WHERE id = ?`, id)
}

// SaveRates caches the latest fetched rates so the app still works offline.
func (s *Store) SaveRates(ctx context.Context, asOf string, perUnit map[string]float64) error {
	for currency, rate := range perUnit {
		if currency == Base || rate <= 0 {
			continue
		}
		_, err := s.db.ExecContext(ctx, `
            INSERT INTO rates (currency, as_of, rate) VALUES (?, ?, ?)
            ON CONFLICT (currency, as_of) DO UPDATE SET rate = excluded.rate`,
			currency, asOf, rate)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) deleteRow(ctx context.Context, query string, id int64) error {
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func checkCurrency(c string) error {
	if !slices.Contains(Currencies, c) {
		return fmt.Errorf("unsupported currency %q", c)
	}
	return nil
}

func checkDate(asOf string) error {
	if _, err := time.Parse("2006-01-02", asOf); err != nil {
		return fmt.Errorf("invalid date %q", asOf)
	}
	return nil
}

func today() string { return time.Now().Format("2006-01-02") }
