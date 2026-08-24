package store

import (
	"testing"

	"github.com/MrShanks/networh/internal/money"
)

func testExpenses() []Expense {
	return []Expense{
		{ID: 1, AsOf: "2026-07-03", Category: "Groceries", Currency: "CHF", Amount: 12000},
		{ID: 2, AsOf: "2026-08-01", Category: "Rent", Currency: "CHF", Amount: 200000},
		{ID: 3, AsOf: "2026-08-04", Category: "Groceries", Currency: "CHF", Amount: 8000},
		{ID: 4, AsOf: "2026-08-09", Category: "Travel", Currency: "USD", Amount: 10000}, // 80.00 CHF
	}
}

func TestBuildExpenseReport(t *testing.T) {
	rates := map[string]float64{"CHF": 1, "USD": 0.8}
	report := BuildExpenseReport(testExpenses(), rates)

	if len(report.Months) != 2 {
		t.Fatalf("report has %d months, want 2", len(report.Months))
	}
	if got, want := report.Months[0].Month, "2026-07"; got != want {
		t.Errorf("first month = %q, want %q (oldest first)", got, want)
	}

	august, ok := report.Find("2026-08")
	if !ok {
		t.Fatal("Find(2026-08) did not find the month")
	}
	if got, want := august.Total, money.Amount(216000); got != want {
		t.Errorf("August total = %s, want %s", got, want) // 2000 + 80 + 80 converted
	}
	if got, want := august.Categories[0].Category, "Rent"; got != want {
		t.Errorf("largest category = %q, want %q", got, want)
	}
	if got := august.Categories[0].Share; got < 92 || got > 93 {
		t.Errorf("Rent share = %.1f%%, want about 92.6%%", got)
	}
	if got, want := len(august.Expenses), 3; got != want {
		t.Errorf("August has %d entries, want %d", got, want)
	}

	if got, want := report.Total, money.Amount(228000); got != want {
		t.Errorf("total = %s, want %s", got, want)
	}
	if got, want := report.Average, money.Amount(114000); got != want {
		t.Errorf("average = %s, want %s", got, want)
	}
}

func TestRecentAverage(t *testing.T) {
	report := BuildExpenseReport(testExpenses(), map[string]float64{"CHF": 1, "USD": 0.8})

	// Both months fit in the window, so it matches the overall average.
	if got, want := report.RecentAverage(12), money.Amount(114000); got != want {
		t.Errorf("RecentAverage(12) = %s, want %s", got, want)
	}
	if got, want := report.RecentMonths(12), 2; got != want {
		t.Errorf("RecentMonths(12) = %d, want %d", got, want)
	}

	// A shorter window only looks at the most recent month.
	if got, want := report.RecentAverage(1), money.Amount(216000); got != want {
		t.Errorf("RecentAverage(1) = %s, want %s", got, want)
	}

	if got := (ExpenseReport{}).RecentAverage(12); got != 0 {
		t.Errorf("RecentAverage on an empty report = %s, want 0.00", got)
	}
}

func TestExpenseReportDefaultsToLatestMonth(t *testing.T) {
	report := BuildExpenseReport(testExpenses(), map[string]float64{"CHF": 1, "USD": 0.8})

	month, ok := report.Find("")
	if !ok || month.Month != "2026-08" {
		t.Errorf("Find(\"\") = %q, want the latest month 2026-08", month.Month)
	}

	if cats := report.UsedCategories(); len(cats) != 3 || cats[0] != "Groceries" {
		t.Errorf("UsedCategories() = %v, want 3 sorted categories", cats)
	}
}
