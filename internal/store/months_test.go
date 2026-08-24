package store

import (
	"testing"

	"github.com/MrShanks/networh/internal/money"
)

func TestMonthsBetween(t *testing.T) {
	got := monthsBetween("2025-11", "2026-02")
	want := []string{"2025-11", "2025-12", "2026-01", "2026-02"}

	if len(got) != len(want) {
		t.Fatalf("monthsBetween = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("month %d = %q, want %q", i, got[i], want[i])
		}
	}

	if got := monthEnd("2026-02"); got != "2026-02-28" {
		t.Errorf("monthEnd(2026-02) = %q, want 2026-02-28", got)
	}
}

func TestMonthlyGainsIgnoreContributions(t *testing.T) {
	l := testLedger()
	// Only keep the CHF fund so the arithmetic is easy to follow.
	l.Funds = l.Funds[:1]
	l.trades[1] = []Trade{{AsOf: "2026-01-10", Units: 10, Price: 10000}} // paid 1,000.00
	l.prices[1] = []PriceMark{
		{AsOf: "2026-02-15", Price: 12000}, // +200.00 of growth
		{AsOf: "2026-03-15", Price: 11000}, // -100.00
	}
	// Buying more in April must not look like a gain.
	l.trades[1] = append(l.trades[1], Trade{AsOf: "2026-04-10", Units: 10, Price: 11000})

	gains := l.MonthlyGains()
	if len(gains) != 4 {
		t.Fatalf("got %d months, want 4", len(gains))
	}

	want := []money.Amount{0, 20000, -10000, 0}
	for i, w := range want {
		if gains[i].Gain != w {
			t.Errorf("%s gain = %s, want %s", gains[i].Month, gains[i].Gain, w)
		}
	}
	if got, want := gains[3].Value, money.Amount(220000); got != want {
		t.Errorf("April value = %s, want %s", got, want)
	}
}
