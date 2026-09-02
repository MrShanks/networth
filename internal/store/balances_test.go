package store

import (
	"path/filepath"
	"testing"

	"github.com/MrShanks/networth/internal/money"
)

func TestSetBalanceKeepsOneSnapshotPerMonth(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "balances.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	if err := store.CreateAccount(t.Context(), "Bank", "", "", KindAsset, "CHF", ClassCash, "", nil); err != nil {
		t.Fatal(err)
	}
	ledger, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	accountID := ledger.Accounts[0].ID

	if err := store.SetBalance(t.Context(), accountID, "2026-07", money.Amount(10000)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBalance(t.Context(), accountID, "2026-07-31", money.Amount(12500)); err != nil {
		t.Fatal(err)
	}

	ledger, err = store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	points := ledger.cash[accountID]
	if len(points) != 1 {
		t.Fatalf("balance points = %d, want 1", len(points))
	}
	if points[0].AsOf != "2026-07-01" || points[0].Amount != money.Amount(12500) {
		t.Errorf("balance point = %+v, want July 2026 at 125.00", points[0])
	}
}

func TestImportBalancesCreatesAccountsAtomically(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "import-balances.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	created, err := store.ImportBalances(t.Context(), []BalanceSnapshot{
		{AccountName: "Savings", Month: "2025-01", Amount: money.Amount(80000)},
		{AccountName: "Mortgage", Currency: "EUR", Kind: KindLiability, Month: "2025-01", Amount: money.Amount(20000000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created != 2 {
		t.Fatalf("created accounts = %d, want 2", created)
	}
	ledger, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(ledger.Accounts))
	}
	accounts := make(map[string]Account, len(ledger.Accounts))
	for _, account := range ledger.Accounts {
		accounts[account.Name] = account
	}
	if account := accounts["Mortgage"]; account.Currency != "EUR" || account.Kind != KindLiability {
		t.Errorf("explicit account = %+v", account)
	}
	if account := accounts["Savings"]; account.Currency != Base || account.Kind != KindAsset {
		t.Errorf("default account = %+v", account)
	}

	_, err = store.ImportBalances(t.Context(), []BalanceSnapshot{
		{AccountName: "Temporary", Month: "2025-02", Amount: money.Amount(10000)},
		{AccountName: "Broken", Month: "not-a-month", Amount: money.Amount(10000)},
	})
	if err == nil {
		t.Fatal("invalid import succeeded")
	}
	ledger, err = store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Accounts) != 2 {
		t.Errorf("failed import left %d accounts, want 2", len(ledger.Accounts))
	}
}
