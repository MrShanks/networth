package store

import (
	"testing"

	"github.com/MrShanks/networth/internal/money"
)

// allocationLedger mirrors testLedger but assigns asset classes, including the
// "Viac" scenario: a plain cash balance that is actually fully invested.
func allocationLedger() *Ledger {
	return &Ledger{
		Accounts: []Account{
			{ID: 1, Name: "Checking", Kind: KindAsset, Currency: "CHF", AssetClass: ClassCash},
			{ID: 2, Name: "Viac", Kind: KindAsset, Currency: "CHF", AssetClass: ClassStocks},
			{ID: 3, Name: "Mortgage", Kind: KindLiability, Currency: "CHF", AssetClass: ClassCash},
		},
		Funds: []Fund{
			{ID: 1, AccountID: 1, AccountName: "Checking", Name: "World", Currency: "CHF", AssetClass: ClassStocks},
			{ID: 2, AccountID: 1, AccountName: "Checking", Name: "Govies", Currency: "CHF", AssetClass: ClassBonds},
		},
		cash: map[int64][]CashPoint{
			1: {{AsOf: "2026-01-01", Amount: 100000}},   // 1,000.00 CHF, liquid
			2: {{AsOf: "2026-01-01", Amount: 500000}},   // 5,000.00 CHF, invested in stocks
			3: {{AsOf: "2026-01-01", Amount: 2_000_00}}, // 20,000.00 CHF owed
		},
		trades: map[int64][]Trade{
			1: {{AsOf: "2026-01-01", Units: 10, Price: 20000}}, // 2,000.00 CHF, stocks
			2: {{AsOf: "2026-01-01", Units: 10, Price: 10000}}, // 1,000.00 CHF, bonds
		},
		prices: map[int64][]PriceMark{},
		Rates:  map[string]float64{"CHF": 1},
	}
}

func TestAllocationGroupsCashAndFundsByClass(t *testing.T) {
	alloc := allocationLedger().At("2026-01-01").Allocation()

	// 1,000 liquid cash + 5,000 Viac cash + 2,000 fund = 8,000 stocks.
	want := map[string]money.Amount{
		ClassCash:   100000,
		ClassStocks: 700000,
		ClassBonds:  100000,
	}
	if got, want := alloc.Total, money.Amount(900000); got != want {
		t.Fatalf("Total = %s, want %s", got, want)
	}
	if len(alloc.Classes) != len(want) {
		t.Fatalf("got %d classes, want %d: %+v", len(alloc.Classes), len(want), alloc.Classes)
	}
	for _, c := range alloc.Classes {
		if got, ok := want[c.Class]; !ok || got != c.Total {
			t.Errorf("class %s = %s, want %s", c.Class, c.Total, want[c.Class])
		}
	}
	// Biggest first.
	if alloc.Classes[0].Class != ClassStocks {
		t.Errorf("first class = %s, want stocks (the biggest)", alloc.Classes[0].Class)
	}
	// The mortgage (a liability) must not leak into the allocation.
	var total money.Amount
	for _, c := range alloc.Classes {
		total += c.Total
	}
	if total != alloc.Total {
		t.Errorf("classes sum to %s, want %s (liabilities must be excluded)", total, alloc.Total)
	}
}

func TestLiquidityTreatsInvestedCashAsIlliquid(t *testing.T) {
	l := allocationLedger().At("2026-01-01").Liquidity()

	// Only the Checking account's plain cash is liquid; Viac's cash balance
	// is marked stocks and does not count, even though it is a bare balance.
	if got, want := l.Liquid, money.Amount(100000); got != want {
		t.Errorf("Liquid = %s, want %s", got, want)
	}
	if got, want := l.Illiquid, money.Amount(800000); got != want {
		t.Errorf("Illiquid = %s, want %s", got, want)
	}
	if got, want := l.Pct, 100.0/9; got < want-0.01 || got > want+0.01 {
		t.Errorf("Pct = %.4f, want about %.4f", got, want)
	}
	if got, want := l.IlliquidPct(), 100-l.Pct; got != want {
		t.Errorf("IlliquidPct() = %.4f, want %.4f", got, want)
	}
}

func TestAllocationOfEmptyLedgerIsEmpty(t *testing.T) {
	v := (&Ledger{}).At("2026-01-01")
	if alloc := v.Allocation(); alloc.Total != 0 || len(alloc.Classes) != 0 {
		t.Errorf("Allocation() of an empty ledger = %+v, want zero", alloc)
	}
	if l := v.Liquidity(); l.Total != 0 || l.Pct != 0 {
		t.Errorf("Liquidity() of an empty ledger = %+v, want zero", l)
	}
}
