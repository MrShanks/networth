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

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
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

func TestBuildExpenseReportExcludesTransfers(t *testing.T) {
	report := BuildExpenseReport([]Expense{
		{ID: 1, Kind: KindExpense, AsOf: "2026-08-01", Category: "Groceries", Currency: "CHF", Amount: 5000},
		{ID: 2, Kind: KindTransfer, AsOf: "2026-08-02", Category: "Account transfers", Currency: "CHF", Amount: 100000},
	}, map[string]float64{"CHF": 1})
	month, _ := report.Find("2026-08")
	if month.Total != 5000 || len(month.Expenses) != 1 || month.Expenses[0].Kind == KindTransfer {
		t.Fatalf("month includes neutral transfer: %+v", month)
	}
}

func TestImportPairsSameDayOppositeAmountsAcrossImports(t *testing.T) {
	store := openTestStore(t)
	outgoing := Expense{Kind: KindExpense, AsOf: "2026-08-01", Category: "Other", Note: "To savings", Currency: "CHF", Amount: 100000}
	if added, _, err := store.ImportExpenses(t.Context(), []Expense{outgoing}); err != nil || added != 1 {
		t.Fatalf("first import = added %d, err %v", added, err)
	}
	incoming := Expense{Kind: KindIncome, AsOf: "2026-08-01", Category: "Other", Note: "From checking", Currency: "EUR", Amount: 100000}
	if added, _, err := store.ImportExpenses(t.Context(), []Expense{incoming}); err != nil || added != 1 {
		t.Fatalf("second import = added %d, err %v", added, err)
	}

	entries, err := store.Expenses(t.Context())
	if err != nil || len(entries) != 2 || entries[0].Kind != KindTransfer || entries[1].Kind != KindTransfer {
		t.Fatalf("paired entries = %+v, err %v", entries, err)
	}
	if added, duplicates, err := store.ImportExpenses(t.Context(), []Expense{incoming}); err != nil || added != 0 || duplicates != 1 {
		t.Fatalf("reimport after pairing = added %d, duplicates %d, err %v", added, duplicates, err)
	}
}

func TestImportPairsBankLabelledTransferWithIncome(t *testing.T) {
	store := openTestStore(t)
	rows := []Expense{
		{Kind: KindTransfer, AsOf: "2026-07-15", Category: "Account transfers", Currency: "CHF", Amount: 100000},
		{Kind: KindIncome, AsOf: "2026-07-15", Category: "Other income", Currency: "CHF", Amount: 100000},
	}
	if _, _, err := store.ImportExpenses(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Expenses(t.Context())
	if err != nil || len(entries) != 2 || entries[0].Kind != KindTransfer || entries[1].Kind != KindTransfer {
		t.Fatalf("paired entries = %+v, err %v", entries, err)
	}
	report := BuildExpenseReport(entries, map[string]float64{"CHF": 1})
	month, _ := report.Find("2026-07")
	if month.Income != 0 || len(month.Expenses) != 0 {
		t.Fatalf("neutral pair remains in report: %+v", month)
	}
}

func TestImportNeutralisesOnlyOneCounterpartPerTransfer(t *testing.T) {
	store := openTestStore(t)
	rows := []Expense{
		{Kind: KindIncome, AsOf: "2026-07-15", AccountRef: "ACCOUNT-A", Category: "External income", Currency: "CHF", Amount: 1000000},
		{Kind: KindIncome, AsOf: "2026-07-15", AccountRef: "ACCOUNT-B", Category: "Incoming transfer", Currency: "CHF", Amount: 1000000},
		{Kind: KindTransfer, AsOf: "2026-07-15", AccountRef: "ACCOUNT-A", Category: "Account transfers", Currency: "CHF", Amount: 1000000},
	}
	if _, _, err := store.ImportExpenses(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Expenses(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	transfers, incomes := 0, 0
	for _, entry := range entries {
		switch entry.Kind {
		case KindTransfer:
			transfers++
		case KindIncome:
			incomes++
		}
	}
	if transfers != 2 || incomes != 1 {
		t.Fatalf("kinds after pairing = %+v, want exactly two transfers and one income", entries)
	}
	report := BuildExpenseReport(entries, map[string]float64{"CHF": 1})
	month, _ := report.Find("2026-07")
	if month.Income != 1000000 {
		t.Fatalf("reported income = %s, want 10,000.00", month.Income)
	}
	if len(month.IncomeCategories) != 1 || month.IncomeCategories[0].Category != "External income" {
		t.Fatalf("remaining income = %+v, want external income", month.IncomeCategories)
	}
}

func TestImportDoesNotPairTransfersOnDifferentDaysOrAmounts(t *testing.T) {
	store := openTestStore(t)
	rows := []Expense{
		{Kind: KindExpense, AsOf: "2026-08-01", Category: "Other", Currency: "CHF", Amount: 100000},
		{Kind: KindIncome, AsOf: "2026-08-02", Category: "Other", Currency: "CHF", Amount: 100000},
		{Kind: KindIncome, AsOf: "2026-08-01", Category: "Other", Currency: "CHF", Amount: 100001},
	}
	if _, _, err := store.ImportExpenses(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	entries, _ := store.Expenses(t.Context())
	for _, entry := range entries {
		if entry.Kind == KindTransfer {
			t.Fatalf("non-matching entries were paired: %+v", entries)
		}
	}
}

func TestImportReconcilesHistoricalSameDayAmountsWithoutReferences(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`INSERT INTO expenses (kind, as_of, category, note, currency, cents) VALUES
		('expense', '2026-08-01', 'Other', '', 'CHF', 100000),
		('income', '2026-08-01', 'Other', '', 'EUR', 100000)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ImportExpenses(t.Context(), []Expense{
		{Kind: KindExpense, AsOf: "2026-08-02", Category: "Other", Currency: "CHF", Amount: 5000},
	}); err != nil {
		t.Fatal(err)
	}
	entries, _ := store.Expenses(t.Context())
	if entries[1].Kind != KindTransfer || entries[2].Kind != KindTransfer {
		t.Fatalf("historical pair was not reconciled: %+v", entries)
	}
}

func TestReimportEnrichesLegacyTransactionAccountReference(t *testing.T) {
	store := openTestStore(t)
	legacy := Expense{Kind: KindExpense, AsOf: "2026-08-01", Category: "Other", Note: "Transfer", Currency: "CHF", Amount: 100000}
	if _, _, err := store.ImportExpenses(t.Context(), []Expense{legacy}); err != nil {
		t.Fatal(err)
	}
	legacy.AccountRef = "CH111"
	if added, duplicates, err := store.ImportExpenses(t.Context(), []Expense{legacy}); err != nil || added != 0 || duplicates != 1 {
		t.Fatalf("reimport = added %d, duplicates %d, err %v", added, duplicates, err)
	}
	entries, _ := store.Expenses(t.Context())
	if len(entries) != 1 || entries[0].AccountRef != "CH111" {
		t.Fatalf("legacy entry was not enriched: %+v", entries)
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

func TestIncomeCategoriesAreConvertedSortedAndAggregatedByYear(t *testing.T) {
	report := BuildExpenseReport([]Expense{
		{Kind: KindIncome, AsOf: "2026-07-01", Category: "Salary", Subcategory: "Primary job", Currency: "CHF", Amount: 500000},
		{Kind: KindIncome, AsOf: "2026-07-02", Category: "Bonus", Currency: "USD", Amount: 100000},
		{Kind: KindIncome, AsOf: "2026-08-01", Category: "Salary", Currency: "CHF", Amount: 510000},
	}, map[string]float64{"CHF": 1, "USD": 0.8})

	july, _ := report.Find("2026-07")
	if len(july.IncomeCategories) != 2 || july.IncomeCategories[0].Category != "Salary" ||
		july.IncomeCategories[0].Total != 500000 || july.IncomeCategories[1].Total != 80000 {
		t.Fatalf("July income categories = %+v", july.IncomeCategories)
	}
	if july.IncomeCategories[1].Share < 13.7 || july.IncomeCategories[1].Share > 13.9 {
		t.Errorf("Bonus share = %.1f, want about 13.8", july.IncomeCategories[1].Share)
	}
	if len(july.IncomeSubcategories) != 1 || july.IncomeSubcategories[0].Subcategory != "Primary job" || july.IncomeSubcategories[0].Total != 500000 {
		t.Fatalf("July income subcategories = %+v", july.IncomeSubcategories)
	}
	year := report.Year("2026")
	if len(year.IncomeCategories) != 2 || year.IncomeCategories[0].Category != "Salary" ||
		year.IncomeCategories[0].Total != 1010000 || year.IncomeCategories[1].Total != 80000 {
		t.Fatalf("year income categories = %+v", year.IncomeCategories)
	}
	if len(year.IncomeSubcategories) != 1 || year.IncomeSubcategories[0].Total != 500000 {
		t.Fatalf("year income subcategories = %+v", year.IncomeSubcategories)
	}
}

func TestAllAggregatesEveryRecordedMonth(t *testing.T) {
	report := BuildExpenseReport([]Expense{
		{Kind: KindExpense, AsOf: "2025-12-01", Category: "Rent", Currency: "CHF", Amount: 100000},
		{Kind: KindIncome, AsOf: "2025-12-20", Category: "Salary", Subcategory: "Primary job", Currency: "CHF", Amount: 500000},
		{Kind: KindExpense, AsOf: "2026-01-01", Category: "Rent", Currency: "CHF", Amount: 110000},
		{Kind: KindIncome, AsOf: "2026-01-20", Category: "Salary", Subcategory: "Primary job", Currency: "CHF", Amount: 520000},
	}, map[string]float64{"CHF": 1})

	all := report.All()
	if all.Month != "All time" || all.Total != 210000 || all.Income != 1020000 || all.Salary != 1020000 {
		t.Fatalf("all-time totals = %+v", all)
	}
	if len(all.Expenses) != 4 || len(all.Categories) != 1 || all.Categories[0].Total != 210000 {
		t.Fatalf("all-time details = %+v", all)
	}
	if len(all.IncomeSubcategories) != 1 || all.IncomeSubcategories[0].Total != 1020000 {
		t.Fatalf("all-time income subcategories = %+v", all.IncomeSubcategories)
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
	if len(got) != 2 {
		t.Fatalf("got %d subcategories, want 2: %+v", len(got), got)
	}
	if got[0].Subcategory != "Supermarket" || got[0].Total != 10000 {
		t.Errorf("first subcategory = %+v, want Supermarket CHF 100", got[0])
	}
	if got[1].Subcategory != "Restaurants" || got[1].Total != 4000 {
		t.Errorf("converted subcategory = %+v, want Restaurants CHF 40", got[1])
	}
	if got[0].CurrentMonth != 0 || got[1].CurrentMonth != 0 {
		t.Errorf("current month amounts = %+v, want no tagged spending", got)
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
