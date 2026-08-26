package store

import (
	"path/filepath"
	"testing"

	"github.com/MrShanks/networth/internal/money"
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

func TestRefundsNetOffTheMonth(t *testing.T) {
	expenses := append(testExpenses(),
		Expense{ID: 5, AsOf: "2026-08-11", Category: "Groceries", Currency: "CHF", Amount: -3000},
	)
	report := BuildExpenseReport(expenses, map[string]float64{"CHF": 1, "USD": 0.8})

	august, _ := report.Find("2026-08")
	// 2,000 rent + 80 groceries + 80 travel, less the 30 refunded.
	if got, want := august.Total, money.Amount(213000); got != want {
		t.Errorf("August total = %s, want %s", got, want)
	}
	if got, want := august.Refunds, money.Amount(3000); got != want {
		t.Errorf("August refunds = %s, want %s", got, want)
	}

	for _, c := range august.Categories {
		if c.Category == "Groceries" {
			if got, want := c.Total, money.Amount(5000); got != want {
				t.Errorf("Groceries = %s, want %s after the refund", got, want)
			}
		}
	}
}

func TestIncomeGivesTheMonthsSaving(t *testing.T) {
	expenses := append(testExpenses(),
		Expense{ID: 5, Kind: KindIncome, AsOf: "2026-08-25", Category: "Salary",
			Currency: "CHF", Amount: 500000},
	)
	report := BuildExpenseReport(expenses, map[string]float64{"CHF": 1, "USD": 0.8})

	august, _ := report.Find("2026-08")
	if got, want := august.Income, money.Amount(500000); got != want {
		t.Errorf("income = %s, want %s", got, want)
	}
	// Spending is untouched by the salary landing in the same month.
	if got, want := august.Total, money.Amount(216000); got != want {
		t.Errorf("spending = %s, want %s", got, want)
	}
	if got, want := august.Saved(), money.Amount(284000); got != want {
		t.Errorf("Saved() = %s, want %s", got, want)
	}
	if got := august.SavedPct(); got < 56.7 || got > 56.9 {
		t.Errorf("SavedPct() = %.1f, want about 56.8", got)
	}

	// Income has no business in the spending categories.
	for _, c := range august.Categories {
		if c.Category == "Salary" {
			t.Error("the salary turned up as a spending category")
		}
	}
	if got, want := report.RecentSaved(12), money.Amount(284000); got != want {
		t.Errorf("RecentSaved(12) = %s, want %s", got, want) // July has no income to judge by
	}
	if got, want := report.RecentSavedMonths(12), 1; got != want {
		t.Errorf("RecentSavedMonths(12) = %d, want %d", got, want)
	}
}

func TestTaxesAreSeparateFromSpending(t *testing.T) {
	expenses := append(testExpenses(),
		Expense{ID: 5, AsOf: "2026-08-20", Category: "Tax payments", Currency: "CHF", Amount: 50000},
		Expense{ID: 6, Kind: KindIncome, AsOf: "2026-08-25", Category: "Salary", Currency: "CHF", Amount: 500000},
	)
	report := BuildExpenseReport(expenses, map[string]float64{"CHF": 1, "USD": 0.8})

	august, _ := report.Find("2026-08")
	if got, want := august.Taxes, money.Amount(50000); got != want {
		t.Errorf("taxes = %s, want %s", got, want)
	}
	if got, want := august.Total, money.Amount(216000); got != want {
		t.Errorf("spending = %s, want %s without taxes", got, want)
	}
	if got, want := august.Saved(), money.Amount(234000); got != want {
		t.Errorf("Saved() = %s, want %s after spending and taxes", got, want)
	}
	year := report.Year("2026")
	if got, want := year.Taxes, money.Amount(50000); got != want {
		t.Errorf("Year().Taxes = %s, want %s", got, want)
	}
	if got, want := year.Saved(), money.Amount(222000); got != want {
		t.Errorf("Year().Saved() = %s, want %s", got, want)
	}
	for _, category := range august.Categories {
		if category.Category == "Tax payments" {
			t.Error("tax payments turned up as a spending category")
		}
	}
}

func TestOpenMigratesExistingTaxPayments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = store.db.Exec(`
		INSERT INTO expenses (kind, as_of, category, note, currency, cents)
		VALUES ('expense', '2026-08-20', 'Taxes & authorities', 'existing live data', 'CHF', 50000)`)
	if err != nil {
		t.Fatalf("seed existing expense: %v", err)
	}
	store.Close()

	store, err = Open(path)
	if err != nil {
		t.Fatalf("reopen with migration: %v", err)
	}
	defer store.Close()
	expenses, err := store.Expenses(t.Context())
	if err != nil {
		t.Fatalf("Expenses: %v", err)
	}
	if len(expenses) != 1 || expenses[0].Kind != KindTax {
		t.Fatalf("existing entry after migration = %+v, want kind %q", expenses, KindTax)
	}
}

func TestSetExpenseCategoryKeepsKindInSync(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "categories.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	for _, kind := range []string{KindExpense, KindIncome} {
		if err := store.AddEntry(t.Context(), kind, "2026-08-01", "Other", kind, "CHF", 1000); err != nil {
			t.Fatalf("AddEntry(%s): %v", kind, err)
		}
	}
	if err := store.SetExpenseCategory(t.Context(), 1, "Taxes"); err != nil {
		t.Fatalf("move expense to Taxes: %v", err)
	}
	if err := store.SetExpenseCategory(t.Context(), 2, "Taxes"); err != nil {
		t.Fatalf("move income to Taxes: %v", err)
	}
	expenses, err := store.Expenses(t.Context())
	if err != nil {
		t.Fatalf("Expenses: %v", err)
	}
	if expenses[0].Kind != KindIncome || expenses[1].Kind != KindTax {
		t.Fatalf("kinds after moving to Taxes = %q, %q", expenses[0].Kind, expenses[1].Kind)
	}
	if err := store.SetExpenseCategory(t.Context(), 1, "Groceries"); err != nil {
		t.Fatalf("move tax to Groceries: %v", err)
	}
	expenses, _ = store.Expenses(t.Context())
	if expenses[1].Kind != KindExpense {
		t.Errorf("kind after moving out of Taxes = %q, want %q", expenses[1].Kind, KindExpense)
	}
}

func TestSetExpenseSubcategory(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "subcategories.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if err := store.AddEntry(t.Context(), KindExpense, "2026-08-01", "Groceries", "MIGROS", "CHF", 1000); err != nil {
		t.Fatal(err)
	}
	if err := store.SetExpenseSubcategory(t.Context(), 1, " Supermarket "); err != nil {
		t.Fatal(err)
	}
	expenses, err := store.Expenses(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(expenses) != 1 || expenses[0].Subcategory != "Supermarket" {
		t.Fatalf("expenses = %+v", expenses)
	}
	if err := store.SetExpenseSubcategory(t.Context(), 1, ""); err != nil {
		t.Fatal(err)
	}
	expenses, err = store.Expenses(t.Context())
	if err != nil || expenses[0].Subcategory != "" {
		t.Fatalf("cleared expenses = %+v, err = %v", expenses, err)
	}
}

func TestByCategory(t *testing.T) {
	report := BuildExpenseReport(testExpenses(), map[string]float64{"CHF": 1, "USD": 0.8})

	got := report.ByCategory()
	if len(got) != 3 {
		t.Fatalf("got %d categories, want 3", len(got))
	}

	rent := got[0]
	if rent.Category != "Rent" {
		t.Errorf("biggest category = %q, want Rent", rent.Category)
	}
	if want := money.Amount(200000); rent.Total != want {
		t.Errorf("Rent total = %s, want %s", rent.Total, want)
	}
	// Spread over both months, even though it only appears in one.
	if want := money.Amount(100000); rent.PerMonth != want {
		t.Errorf("Rent per month = %s, want %s", rent.PerMonth, want)
	}
	if rent.Months != 1 {
		t.Errorf("Rent appears in %d months, want 1", rent.Months)
	}
	if got, want := rent.Share, 87.7; got < want-0.5 || got > want+0.5 {
		t.Errorf("Rent share = %.1f%%, want about %.1f%%", got, want)
	}

	groceries := got[1]
	if groceries.Category != "Groceries" {
		t.Fatalf("second category = %q, want Groceries", groceries.Category)
	}
	// One entry in each month, so the series has both.
	want := []money.Amount{12000, 8000}
	for i, w := range want {
		if groceries.Series[i] != w {
			t.Errorf("Groceries month %d = %s, want %s", i, groceries.Series[i], w)
		}
	}
	if groceries.Months != 2 {
		t.Errorf("Groceries appears in %d months, want 2", groceries.Months)
	}

	if got := (ExpenseReport{}).ByCategory(); got != nil {
		t.Errorf("ByCategory on an empty report = %v, want nil", got)
	}
}

func TestBySubcategory(t *testing.T) {
	report := BuildExpenseReport([]Expense{
		{AsOf: "2026-07-01", Category: "Food", Subcategory: "Supermarket", Currency: "CHF", Amount: 10000},
		{AsOf: "2026-07-02", Category: "Food", Subcategory: "Restaurants", Currency: "USD", Amount: 5000},
		{AsOf: "2026-08-01", Category: "Food", Currency: "CHF", Amount: 2500},
		{AsOf: "2026-08-02", Category: "Travel", Subcategory: "Train", Currency: "CHF", Amount: 3000},
	}, map[string]float64{"CHF": 1, "USD": 0.8})

	if got := report.BySubcategory("", "2026-08"); got != nil {
		t.Fatalf("BySubcategory with no category = %v, want nil", got)
	}
	got := report.BySubcategory("food", "2026-08")
	if len(got) != 3 {
		t.Fatalf("got %d subcategories, want 3: %+v", len(got), got)
	}
	if got[0].Subcategory != "Supermarket" || got[0].Total != 10000 {
		t.Errorf("first subcategory = %+v, want Supermarket CHF 100", got[0])
	}
	if got[1].Subcategory != "Restaurants" || got[1].Total != 4000 {
		t.Errorf("converted subcategory = %+v, want Restaurants CHF 40", got[1])
	}
	if got[2].Subcategory != "Uncategorized" || got[2].Total != 2500 {
		t.Errorf("empty subcategory = %+v, want Uncategorized CHF 25", got[2])
	}
	if got[0].CurrentMonth != 0 || got[1].CurrentMonth != 0 || got[2].CurrentMonth != 2500 {
		t.Errorf("current month amounts = %+v, want only Uncategorized CHF 25", got)
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
