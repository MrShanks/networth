package store

import (
	"testing"

	"github.com/MrShanks/networth/internal/money"
)

func TestTrack(t *testing.T) {
	tests := []struct {
		name            string
		trades          []Trade
		basis, realized money.Amount
		units           float64
	}{
		{
			name:   "a single purchase",
			trades: []Trade{{Units: 10, Price: 10000}},
			basis:  100000,
			units:  10,
		},
		{
			name: "buying more adds what was paid, not the new market price",
			trades: []Trade{
				{Units: 10, Price: 10000}, // 1,000.00
				{Units: 10, Price: 12000}, // 1,200.00
			},
			basis: 220000,
			units: 20,
		},
		{
			name: "selling realises the gain at average cost",
			trades: []Trade{
				{Units: 10, Price: 10000},
				{Units: -5, Price: 20000},
			},
			basis:    50000, // half of the 1,000.00 cost remains
			realized: 50000, // sold 5 units for 1,000.00 that cost 500.00
			units:    5,
		},
		{
			name: "selling everything leaves no basis",
			trades: []Trade{
				{Units: 10, Price: 10000},
				{Units: -10, Price: 15000},
			},
			realized: 50000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			basis, realized, units := track(tc.trades)
			if basis != tc.basis {
				t.Errorf("basis = %s, want %s", basis, tc.basis)
			}
			if realized != tc.realized {
				t.Errorf("realized = %s, want %s", realized, tc.realized)
			}
			if units != tc.units {
				t.Errorf("units = %v, want %v", units, tc.units)
			}
		})
	}
}

func TestPositionGain(t *testing.T) {
	trades := []Trade{
		{Units: 10, Price: 10000}, // paid 1,000.00
		{Units: 10, Price: 15000}, // paid another 1,500.00
	}
	basis, realized, units := track(trades)

	p := Position{
		Units:    units,
		Price:    15000,
		Value:    value(units, 15000), // 3,000.00
		Basis:    basis,
		Realized: realized,
	}

	if got, want := p.Gain(), money.Amount(50000); got != want {
		t.Errorf("Gain() = %s, want %s", got, want)
	}
	if got, want := p.GainPct(), 20.0; got != want {
		t.Errorf("GainPct() = %.2f, want %.2f", got, want)
	}
	if got, want := p.AvgCost(), money.Amount(12500); got != want {
		t.Errorf("AvgCost() = %s, want %s", got, want)
	}
}

// testLedger builds an in-memory ledger without touching SQLite.
func testLedger() *Ledger {
	return &Ledger{
		Accounts: []Account{
			{ID: 1, Name: "Degiro", Kind: KindAsset, Currency: "CHF"},
			{ID: 2, Name: "Mortgage", Kind: KindLiability, Currency: "CHF"},
		},
		Funds: []Fund{
			{ID: 1, AccountID: 1, AccountName: "Degiro", Name: "Swiss", Ticker: "CHSPI", Currency: "CHF"},
			{ID: 2, AccountID: 1, AccountName: "Degiro", Name: "S&P 500", Ticker: "VUSA", Currency: "USD"},
			{ID: 3, AccountID: 1, AccountName: "Degiro", Name: "World", Ticker: "VWCE", Currency: "EUR"},
		},
		cash: map[int64][]CashPoint{
			1: {{AsOf: "2026-01-01", Amount: 50000}},     // 500.00 CHF cash
			2: {{AsOf: "2026-01-01", Amount: 10_000_00}}, // 10,000.00 CHF owed
		},
		trades: map[int64][]Trade{
			1: {{AsOf: "2026-01-01", Units: 10, Price: 10000}}, // 1,000.00 CHF
			2: {{AsOf: "2026-01-01", Units: 10, Price: 10000}}, // 1,000.00 USD
			3: {{AsOf: "2026-01-01", Units: 10, Price: 10000}}, // 1,000.00 EUR
		},
		prices: map[int64][]PriceMark{},
		Rates:  map[string]float64{"CHF": 1, "USD": 0.8, "EUR": 0.95},
	}
}

func TestValuationConvertsFundCurrencies(t *testing.T) {
	v := testLedger().At("2026-01-01")

	// 500 cash + 1000 CHF + 1000 USD * 0.8 + 1000 EUR * 0.95
	if got, want := v.Assets, money.Amount(325000); got != want {
		t.Errorf("Assets = %s, want %s", got, want)
	}
	if got, want := v.Liabilities, money.Amount(1_000_000); got != want {
		t.Errorf("Liabilities = %s, want %s", got, want)
	}
	if got, want := v.NetWorth(), money.Amount(-675000); got != want {
		t.Errorf("NetWorth() = %s, want %s", got, want)
	}
	if got, want := v.MarketBase, money.Amount(275000); got != want {
		t.Errorf("MarketBase = %s, want %s", got, want)
	}

	usd := v.Accounts[0].Positions[1]
	if usd.Rate != 0.8 {
		t.Errorf("USD rate = %v, want 0.8", usd.Rate)
	}
	if got, want := usd.ValueBase, money.Amount(80000); got != want {
		t.Errorf("USD fund value = %s (base), want %s", got, want)
	}
}

func TestBuyingMoreCountsAsInvestedNotGrowth(t *testing.T) {
	l := testLedger()
	// The Swiss fund doubles in price, then another 10 units are bought at it.
	l.prices[1] = []PriceMark{{AsOf: "2026-02-01", Price: 20000}}
	l.trades[1] = append(l.trades[1], Trade{AsOf: "2026-03-01", Units: 10, Price: 20000})

	p := l.At("2026-03-01").Accounts[0].Positions[0]
	if got, want := p.Units, 20.0; got != want {
		t.Errorf("Units = %v, want %v", got, want)
	}
	if got, want := p.Invested(), money.Amount(300000); got != want {
		t.Errorf("Invested() = %s, want %s", got, want) // 1,000 + 2,000
	}
	if got, want := p.Value, money.Amount(400000); got != want {
		t.Errorf("Value = %s, want %s", got, want)
	}
	// Growth is only the first 10 units doubling, not the new money.
	if got, want := p.Gain(), money.Amount(100000); got != want {
		t.Errorf("Gain() = %s, want %s", got, want)
	}
}

func TestInvestHistoryTracksContributions(t *testing.T) {
	l := testLedger()
	l.trades[1] = append(l.trades[1], Trade{AsOf: "2026-03-01", Units: 10, Price: 10000})

	history := l.InvestHistory()
	if len(history) != 2 {
		t.Fatalf("InvestHistory() has %d points, want 2", len(history))
	}
	first, last := history[0], history[1]
	if got, want := last.Invested-first.Invested, money.Amount(100000); got != want {
		t.Errorf("invested change = %s, want %s", got, want)
	}
	if got, want := last.Value-first.Value, money.Amount(100000); got != want {
		t.Errorf("value change = %s, want %s", got, want)
	}
}

func TestMissingRates(t *testing.T) {
	l := testLedger()
	delete(l.Rates, "EUR")

	got := l.MissingRates()
	if len(got) != 1 || got[0] != "EUR" {
		t.Errorf("MissingRates() = %v, want [EUR]", got)
	}
}

func TestHistoryCarriesLastKnownValueForward(t *testing.T) {
	l := testLedger()
	l.prices[1] = []PriceMark{{AsOf: "2026-02-01", Price: 12000}}

	history := l.History()
	if len(history) != 2 {
		t.Fatalf("History() has %d points, want 2", len(history))
	}
	// Only the CHF fund moved: +200.00 on top of the January net worth.
	if got, want := history[1].NetWorth()-history[0].NetWorth(), money.Amount(20000); got != want {
		t.Errorf("net worth change = %s, want %s", got, want)
	}
}

func TestNetWorthHistoryUsesCurrentBalancesAsHistoricalBaseline(t *testing.T) {
	l := testLedger()
	l.NetWorthBaseline = "2026-02-01"
	l.cash[1] = append(l.cash[1], CashPoint{AsOf: "2026-02-01", Amount: 80000})
	l.cash[2] = append(l.cash[2], CashPoint{AsOf: "2026-02-01", Amount: 9_000_00})
	l.prices[1] = []PriceMark{
		{AsOf: "2026-02-01", Price: 12000},
		{AsOf: "2026-03-01", Price: 13000},
	}

	history := l.NetWorthHistory()
	if got, want := len(history), 3; got != want {
		t.Fatalf("NetWorthHistory() has %d points, want %d", got, want)
	}
	if got, want := history[0].NetWorth(), money.Amount(-545000); got != want {
		t.Errorf("January net worth = %s, want %s", got, want)
	}
	if got, want := history[2].NetWorth()-history[1].NetWorth(), money.Amount(10000); got != want {
		t.Errorf("post-baseline change = %s, want %s", got, want)
	}
}
