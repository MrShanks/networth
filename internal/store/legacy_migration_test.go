package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestLegacyETFAccountsAreMigrated builds a first-generation database by hand
// — before funds were their own table, an ETF was an account with an
// asset_class of 'etf' and its own ticker column — and checks Open still
// converts it correctly. This guards against confusing that legacy
// accounts.asset_class column with the modern one added for asset allocation.
func TestLegacyETFAccountsAreMigrated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	_, err = raw.Exec(`
        CREATE TABLE accounts (
            id         INTEGER PRIMARY KEY AUTOINCREMENT,
            name       TEXT NOT NULL UNIQUE,
            kind       TEXT NOT NULL,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            asset_class TEXT NOT NULL DEFAULT 'cash',
            ticker      TEXT NOT NULL DEFAULT ''
        );
        CREATE TABLE balances (
            id         INTEGER PRIMARY KEY AUTOINCREMENT,
            account_id INTEGER NOT NULL,
            as_of      TEXT NOT NULL,
            cents      INTEGER NOT NULL,
            units       REAL,
            price_cents INTEGER
        );
        INSERT INTO accounts (id, name, kind, asset_class, ticker) VALUES
            (1, 'Degiro', 'asset', 'cash', ''),
            (2, 'World ETF', 'asset', 'etf', 'VWCE');
        INSERT INTO balances (account_id, as_of, cents, units, price_cents) VALUES
            (2, '2026-01-01', 100000, 10, 10000);
    `)
	raw.Close()
	if err != nil {
		t.Fatalf("seed legacy db: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a legacy database returned error: %v", err)
	}
	defer s.Close()

	ledger, err := s.Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(ledger.Accounts) != 2 {
		t.Fatalf("accounts after migration = %+v, want both accounts kept", ledger.Accounts)
	}
	if len(ledger.Funds) != 1 {
		t.Fatalf("got %d funds, want the ETF account converted into 1", len(ledger.Funds))
	}
	fund := ledger.Funds[0]
	if fund.Name != "World ETF" || fund.Ticker != "VWCE" {
		t.Errorf("fund = %+v, want World ETF / VWCE", fund)
	}
	// The modern default, not the legacy 'etf' string.
	if fund.AssetClass != ClassStocks {
		t.Errorf("fund AssetClass = %q, want the modern default %q", fund.AssetClass, ClassStocks)
	}
	for _, a := range ledger.Accounts {
		if a.AssetClass != ClassCash {
			t.Errorf("account %s AssetClass = %q, want the modern default %q", a.Name, a.AssetClass, ClassCash)
		}
	}
}
