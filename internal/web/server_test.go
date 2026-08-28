package web

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MrShanks/networth/internal/fx"
	"github.com/MrShanks/networth/internal/store"
)

// newTestServer wires a server to a throwaway database and a stub rate service.
func newTestServer(t *testing.T) *Server {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	rates := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"amount":1,"base":"CHF","date":"2026-08-21","rates":{"EUR":1.07,"USD":1.25}}`)
	}))
	t.Cleanup(rates.Close)

	srv, err := NewServer(db, fx.NewClient(rates.URL, store.Base, store.Foreign()),
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv
}

func get(t *testing.T, srv *Server, path string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, rec.Code)
	}
	return rec.Body.String()
}

func post(t *testing.T, srv *Server, path string, form url.Values) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST %s = %d, want 303", path, rec.Code)
	}
	if location := rec.Header().Get("Location"); strings.Contains(location, "err=") {
		t.Fatalf("POST %s redirected with an error: %s", path, location)
	}
}

// seed fills the store through the HTTP handlers, the way a browser would.
func seed(t *testing.T, srv *Server) {
	t.Helper()

	post(t, srv, "/accounts", url.Values{
		"name": {"Degiro"}, "kind": {"asset"}, "currency": {"CHF"},
	})
	post(t, srv, "/funds", url.Values{
		"account_id": {"1"}, "name": {"World"}, "ticker": {"vwce"}, "currency": {"USD"},
	})
	post(t, srv, "/balances", url.Values{
		"account_id": {"1"}, "amount": {"5000"}, "as_of": {"2026-01-31"},
	})
	post(t, srv, "/fund-updates", url.Values{
		"fund_id": {"1"}, "price": {"100"}, "units": {"10"}, "as_of": {"2026-01-31"},
	})
	post(t, srv, "/fund-updates", url.Values{
		"fund_id": {"1"}, "price": {"120"}, "as_of": {"2026-02-28"},
	})
	post(t, srv, "/expenses", url.Values{
		"amount": {"1200"}, "currency": {"CHF"}, "new_category": {"Rent"}, "as_of": {"2026-02-05"},
	})
}

// TestPagesRender is the guard against template mistakes: a broken template
// truncates the response instead of failing, so check each page ends properly.
func TestPagesRender(t *testing.T) {
	srv := newTestServer(t)

	pages := []struct{ path, wants string }{
		{"/", "Net worth"},
		{"/expenses", "Monthly Expenses"},
		{"/expenses/year", "Yearly Expenses"},
		{"/transactions", "Add transaction"},
		{"/graphs", "Salary growth by month"},
		{"/records", "Best months for investments"},
		{"/retire", "How long the money lasts"},
	}
	for _, empty := range pages {
		t.Run("empty"+empty.path, func(t *testing.T) {
			body := get(t, srv, empty.path)
			if !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
				t.Error("page was cut short, a template failed halfway")
			}
		})
	}

	seed(t, srv)
	for _, page := range pages {
		t.Run(page.path, func(t *testing.T) {
			body := get(t, srv, page.path)
			if !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
				t.Fatal("page was cut short, a template failed halfway")
			}
			if !strings.Contains(body, page.wants) {
				t.Errorf("page does not mention %q", page.wants)
			}
		})
	}
}

func TestEveryWidgetHasAnEditableTitle(t *testing.T) {
	srv := newTestServer(t)
	seed(t, srv)

	for _, path := range []string{"/", "/expenses", "/graphs", "/records", "/retire"} {
		body := get(t, srv, path)
		widgets := strings.Count(body, `<section class="widget`)
		titles := strings.Count(body, `data-widget-title`)
		if widgets == 0 || titles != widgets {
			t.Errorf("%s has %d widgets and %d editable titles", path, widgets, titles)
		}
	}
}

func TestGraphsSeparateSalaryIncomeAndYearlyTaxes(t *testing.T) {
	srv := newTestServer(t)
	seed(t, srv)
	for _, entry := range []url.Values{
		{"amount": {"5000"}, "currency": {"CHF"}, "new_category": {"Salary & pensions"}, "kind": {"income"}, "as_of": {"2025-12-20"}},
		{"amount": {"500"}, "currency": {"CHF"}, "new_category": {"Bonus"}, "kind": {"income"}, "as_of": {"2025-12-21"}},
		{"amount": {"5200"}, "currency": {"CHF"}, "new_category": {"Salary"}, "kind": {"income"}, "as_of": {"2026-01-20"}},
		{"amount": {"1000"}, "currency": {"CHF"}, "new_category": {"Taxes"}, "kind": {"tax"}, "as_of": {"2025-06-10"}},
		{"amount": {"1200"}, "currency": {"CHF"}, "new_category": {"Taxes"}, "kind": {"tax"}, "as_of": {"2026-03-10"}},
		{"amount": {"300"}, "currency": {"CHF"}, "new_category": {"Taxes"}, "kind": {"tax"}, "as_of": {"2026-07-10"}},
		{"amount": {"800"}, "currency": {"CHF"}, "new_category": {"Groceries"}, "as_of": {"2025-12-22"}},
	} {
		post(t, srv, "/expenses", entry)
	}

	body := get(t, srv, "/graphs")
	for _, want := range []string{
		`data-widget="net-worth-history"`, `data-widget="investment-growth"`,
		`data-widget="income-spending"`, `data-widget="savings-rate"`,
		`data-widget="expense-composition"`, `data-widget="allocation-history"`,
		`data-widget="effective-tax-rate"`,
		`href="/graphs">Graphs</a>`, `data-widget="salary-growth"`, `data-widget="monthly-income"`,
		`data-widget="yearly-taxes"`, `class="bar tax-bar"`,
		"2025: 1,000.00", "2026: 1,500.00", `>0</text>`, `data-tooltip="2025-12: 5,000.00"`,
		`class="graph-tooltip" role="tooltip" hidden`, `/static/graphs.js`,
		`2025-12 income: 5,500.00`, `2025-12 spending`, `800.00`,
		`2025-12: 85.5%`, `2025: 18.2%`, `class="stack-band`, `Groceries`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("graphs page is missing %q", want)
		}
	}
	salaryStart := strings.Index(body, `data-widget="salary-growth"`)
	incomeStart := strings.Index(body, `data-widget="monthly-income"`)
	taxesStart := strings.Index(body, `data-widget="yearly-taxes"`)
	salaryPanel := body[salaryStart:incomeStart]
	incomePanel := body[incomeStart:taxesStart]
	for _, want := range []string{`class="line salary-line"`, "2025-12: 5,000.00", "2026-01: 5,200.00"} {
		if !strings.Contains(salaryPanel, want) {
			t.Errorf("salary graph is missing %q", want)
		}
	}
	if strings.Contains(salaryPanel, "5,500.00") {
		t.Error("salary graph includes non-salary income")
	}
	for _, want := range []string{`class="line income-line"`, "2025-12: 5,500.00", "2026-01: 5,200.00"} {
		if !strings.Contains(incomePanel, want) {
			t.Errorf("income graph is missing %q", want)
		}
	}
	if lines := append(chartLines(salaryPanel), chartLines(incomePanel)...); len(lines) != 2 {
		t.Errorf("graph lines = %v, want separate salary and income lines", lines)
	}
}

func TestGraphsShowSalaryBySecondaryCategory(t *testing.T) {
	srv := newTestServer(t)
	for _, entry := range []url.Values{
		{"amount": {"5000"}, "currency": {"CHF"}, "new_category": {"Salary"}, "kind": {"income"}, "as_of": {"2026-01-20"}},
		{"amount": {"3000"}, "currency": {"CHF"}, "new_category": {"Salary & pensions"}, "kind": {"income"}, "as_of": {"2026-01-21"}},
		{"amount": {"5100"}, "currency": {"CHF"}, "new_category": {"Salary"}, "kind": {"income"}, "as_of": {"2026-02-20"}},
	} {
		post(t, srv, "/expenses", entry)
	}
	entries, err := srv.store.Expenses(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		subcategory := "Primary job"
		if entry.Category == "Salary & pensions" {
			subcategory = "Pension"
		}
		if err := srv.store.SetExpenseSubcategory(t.Context(), entry.ID, subcategory); err != nil {
			t.Fatal(err)
		}
	}

	page := get(t, srv, "/graphs")
	start := strings.Index(page, `data-widget="salary-by-subcategory"`)
	end := strings.Index(page, `data-widget="monthly-income"`)
	if start < 0 || end <= start {
		t.Fatal("salary-by-secondary-category graph is missing")
	}
	panel := page[start:end]
	for _, want := range []string{"Salary by secondary category", "Primary job", "Pension", `class="line series-1"`, `class="line series-2"`, "2026-01, Primary job: 5000.00", "2026-02, Primary job: 5100.00"} {
		if !strings.Contains(panel, want) {
			t.Errorf("salary secondary-category graph is missing %q", want)
		}
	}
	if strings.Contains(panel, `class="stack-band`) {
		t.Error("salary secondary-category graph should use lines, not stacked bands")
	}
}

func TestDashboardShowsConvertedTotals(t *testing.T) {
	srv := newTestServer(t)
	seed(t, srv)

	body := get(t, srv, "/")
	// 5,000 CHF cash plus 10 units at 120 USD, converted at 1/1.25.
	if !strings.Contains(body, "5,960.00") {
		t.Error("net worth is not the expected 5,960.00")
	}
	if !strings.Contains(body, `class="line"`) {
		t.Error("the net worth chart has no line")
	}
	if !strings.Contains(body, "Summary (CHF)") {
		t.Error("the summary widget should state the currency once, in its header")
	}
}

func TestDashboardGroupsAccountsByOwner(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/accounts", url.Values{
		"name": {"Savings"}, "owner": {"Sam, Alex, Sam"}, "kind": {"asset"}, "currency": {"CHF"},
	})
	post(t, srv, "/balances", url.Values{
		"account_id": {"1"}, "amount": {"1000"}, "as_of": {"2026-01-01"},
	})
	post(t, srv, "/accounts", url.Values{
		"name": {"Card"}, "owner": {"Sam"}, "kind": {"liability"}, "currency": {"CHF"},
	})
	post(t, srv, "/balances", url.Values{
		"account_id": {"2"}, "amount": {"200"}, "as_of": {"2026-01-01"},
	})

	body := get(t, srv, "/")
	overview := body[strings.Index(body, `data-widget="owners"`):]
	overview = overview[:strings.Index(overview, "</section>")]
	for _, want := range []string{"Total", "800.00", "Alex", "500.00", "Sam", "300.00"} {
		if !strings.Contains(overview, want) {
			t.Errorf("owner overview does not contain %q", want)
		}
	}
	if strings.Index(overview, "Total") > strings.Index(overview, "Alex") {
		t.Error("combined total should be displayed above the individual owners")
	}

	post(t, srv, "/accounts/1/owner", url.Values{"owner": {"Alex"}})
	post(t, srv, "/accounts/2/owner", url.Values{"owner": {"Alex"}})
	body = get(t, srv, "/")
	overview = body[strings.Index(body, `data-widget="owners"`):]
	overview = overview[:strings.Index(overview, "</section>")]
	if strings.Contains(overview, ">Sam<") || strings.Count(overview, ">Alex<") != 1 {
		t.Error("editing an owner should regroup the account")
	}
}

func TestExpenseSummarySeparatesTaxPayments(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/expenses", url.Values{
		"kind": {"expense"}, "amount": {"100"}, "currency": {"CHF"},
		"new_category": {"Groceries"}, "as_of": {"2026-08-01"},
	})
	post(t, srv, "/expenses", url.Values{
		"kind": {"expense"}, "amount": {"25"}, "currency": {"CHF"},
		"new_category": {"Taxes"}, "as_of": {"2026-08-02"},
	})

	month := get(t, srv, "/expenses?month=2026-08")
	if !strings.Contains(month, `August 2026 summary (CHF)`) ||
		!strings.Contains(month, `class="value down">25.00</strong>`) {
		t.Error("tax payments are not displayed as a red summary value")
	}
	if !strings.Contains(month, `class="value">100.00</strong>`) {
		t.Error("monthly spending should exclude tax payments")
	}
	if strings.Contains(month, `>2026 summary (CHF)<`) || strings.Contains(month, `name="year"`) {
		t.Error("monthly page contains yearly data or controls")
	}
	year := get(t, srv, "/expenses/year?year=2026")
	yearToolbar := year[:strings.Index(year, `</header>`)]
	if !strings.Contains(year, `2026 summary (CHF)`) || !strings.Contains(yearToolbar, `name="year"`) || strings.Contains(yearToolbar, `name="month"`) {
		t.Error("yearly page does not use one consistent year scope")
	}
}

func TestExpenseSummarySeparatesSalaryAndOtherIncome(t *testing.T) {
	srv := newTestServer(t)
	for _, entry := range []url.Values{
		{"kind": {"income"}, "amount": {"5000"}, "currency": {"CHF"}, "new_category": {"Salary"}, "as_of": {"2026-08-01"}},
		{"kind": {"income"}, "amount": {"750"}, "currency": {"CHF"}, "new_category": {"Bonus"}, "as_of": {"2026-08-02"}},
	} {
		post(t, srv, "/expenses", entry)
	}

	for _, path := range []string{"/expenses?month=2026-08", "/expenses/year?year=2026"} {
		page := get(t, srv, path)
		summaryStart := strings.Index(page, `data-widget="period-summary"`)
		comparisonStart := strings.Index(page, `data-widget="period-comparison"`)
		if summaryStart < 0 || comparisonStart < 0 {
			t.Fatalf("%s is missing summary panels", path)
		}
		summary := page[summaryStart:comparisonStart]
		for _, want := range []string{"Salary", "5,000.00", "Other income", "750.00"} {
			if !strings.Contains(summary, want) {
				t.Errorf("%s summary is missing %q", path, want)
			}
		}
	}
}

func TestAllTimeExpensesAndSalaryAverages(t *testing.T) {
	srv := newTestServer(t)
	for _, entry := range []url.Values{
		{"kind": {"expense"}, "amount": {"100"}, "currency": {"CHF"}, "new_category": {"Rent"}, "note": {"Old rent"}, "as_of": {"2025-12-01"}},
		{"kind": {"income"}, "amount": {"5000"}, "currency": {"CHF"}, "new_category": {"Salary"}, "as_of": {"2025-12-20"}},
		{"kind": {"expense"}, "amount": {"150"}, "currency": {"CHF"}, "new_category": {"Rent"}, "note": {"New rent"}, "as_of": {"2026-01-01"}},
		{"kind": {"income"}, "amount": {"5200"}, "currency": {"CHF"}, "new_category": {"Salary"}, "as_of": {"2026-01-20"}},
	} {
		post(t, srv, "/expenses", entry)
	}
	entries, err := srv.store.Expenses(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsIncome() {
			if err := srv.store.SetExpenseSubcategory(t.Context(), entry.ID, "Primary job"); err != nil {
				t.Fatal(err)
			}
		}
	}

	page := get(t, srv, "/expenses/all")
	for _, want := range []string{`<title>All-time Expenses</title>`, `class="current" href="/expenses/all">All time`, "All time summary (CHF)", "10,200.00", "Primary job average", "5,100.00", "Old rent", "New rent", `href="/expenses/all?category=Rent#entries"`} {
		if !strings.Contains(page, want) {
			t.Errorf("all-time expenses are missing %q", want)
		}
	}
	if strings.Contains(page, `data-widget="period-comparison"`) || strings.Contains(page, `class="month-picker"`) {
		t.Error("all-time expenses include period-specific controls")
	}
	filtered := get(t, srv, "/expenses/all?category=Rent")
	if !strings.Contains(filtered, "Old rent") || !strings.Contains(filtered, "New rent") || !strings.Contains(filtered, `href="/expenses/all#entries">Clear`) {
		t.Error("all-time category drill-down did not preserve its scope")
	}
}

func TestYearAndAllTimeEntriesArePaginated(t *testing.T) {
	srv := newTestServer(t)
	for day := 1; day <= 101; day++ {
		month := 1 + (day-1)/28
		date := fmt.Sprintf("2026-%02d-%02d", month, 1+(day-1)%28)
		post(t, srv, "/expenses", url.Values{
			"kind": {"expense"}, "amount": {"1"}, "currency": {"CHF"},
			"new_category": {"Food"}, "note": {fmt.Sprintf("Entry %03d", day)}, "as_of": {date},
		})
	}

	for _, path := range []string{"/expenses/year?year=2026", "/expenses/all"} {
		page := get(t, srv, path)
		if rows := strings.Count(page, `data-entry-id=`); rows != 100 {
			t.Errorf("%s rows = %d, want 100", path, rows)
		}
		if strings.Count(page, "Showing 1–100 of 101") != 2 || strings.Count(page, `>Next</a>`) != 2 {
			t.Errorf("%s is missing first-page navigation", path)
		}
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		second := get(t, srv, path+separator+"page=2")
		if rows := strings.Count(second, `data-entry-id=`); rows != 1 {
			t.Errorf("%s second-page rows = %d, want 1", path, rows)
		}
		if strings.Count(second, "Showing 101–101 of 101") != 2 || strings.Count(second, `>Previous</a>`) != 2 {
			t.Errorf("%s is missing second-page navigation", path)
		}
	}
}

func TestExpenseIncomeByCategoryWidget(t *testing.T) {
	srv := newTestServer(t)
	for _, entry := range []url.Values{
		{"kind": {"income"}, "amount": {"5000"}, "currency": {"CHF"}, "new_category": {"Salary"}, "as_of": {"2026-08-01"}},
		{"kind": {"income"}, "amount": {"750"}, "currency": {"CHF"}, "new_category": {"Bonus"}, "as_of": {"2026-08-02"}},
	} {
		post(t, srv, "/expenses", entry)
	}

	for _, path := range []string{"/expenses?month=2026-08", "/expenses/year?year=2026"} {
		page := get(t, srv, path)
		start := strings.Index(page, `data-widget="period-income-categories"`)
		end := strings.Index(page, `data-widget="period-categories"`)
		if start < 0 || end <= start {
			t.Fatalf("%s is missing the income-category widget", path)
		}
		widget := page[start:end]
		for _, want := range []string{"Income by category", "Salary", "5,000.00", "87%", "Bonus", "750.00", "13%", `category=Salary#entries`} {
			if !strings.Contains(widget, want) {
				t.Errorf("%s income categories are missing %q", path, want)
			}
		}
	}
}

func TestIncomeCategoryLinkEncodesAmpersand(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/expenses", url.Values{
		"kind": {"income"}, "amount": {"5000"}, "currency": {"CHF"},
		"new_category": {"Salary & pensions"}, "note": {"Monthly pension"}, "as_of": {"2026-08-01"},
	})

	page := get(t, srv, "/expenses/all")
	if !strings.Contains(page, `href="/expenses/all?category=Salary&#43;%26&#43;pensions#entries"`) {
		t.Fatal("Salary & pensions link is not safely URL-encoded")
	}
	filtered := get(t, srv, "/expenses/all?category=Salary+%26+pensions")
	if !strings.Contains(filtered, "Monthly pension") || !strings.Contains(filtered, "All time entries · Salary &amp; pensions") {
		t.Error("encoded Salary & pensions filter did not show its transaction")
	}
}

func TestExpenseCategoryDrillDown(t *testing.T) {
	srv := newTestServer(t)
	for _, entry := range []url.Values{
		{"kind": {"expense"}, "amount": {"10"}, "currency": {"CHF"}, "new_category": {"Food & drink"}, "note": {"July lunch"}, "as_of": {"2026-07-01"}},
		{"kind": {"expense"}, "amount": {"20"}, "currency": {"CHF"}, "new_category": {"Food & drink"}, "note": {"August dinner"}, "as_of": {"2026-08-01"}},
		{"kind": {"expense"}, "amount": {"30"}, "currency": {"CHF"}, "new_category": {"Travel"}, "note": {"Train"}, "as_of": {"2026-08-02"}},
	} {
		post(t, srv, "/expenses", entry)
	}

	body := get(t, srv, "/expenses?month=2026-08")
	if strings.Contains(body, "%2526") {
		t.Error("category ampersand was double encoded")
	}
	if !strings.Contains(body, `>Food &amp; drink</a>`) {
		t.Error("month category is not linked to its entries")
	}
	month := get(t, srv, "/expenses?month=2026-08&category=Food+%26+drink")
	if !strings.Contains(month, "August dinner") || strings.Contains(month, "July lunch") || strings.Contains(month, "Train") {
		t.Error("month category drill-down did not filter entries correctly")
	}

	year := get(t, srv, "/expenses/year?year=2026&category=Food+%26+drink")
	if !strings.Contains(year, "August dinner") || !strings.Contains(year, "July lunch") || strings.Contains(year, "Train") {
		t.Error("year category drill-down did not filter entries correctly")
	}
}

func TestExpenseActionsRenderInOwningPanels(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/expenses", "/expenses/year"} {
		body := get(t, srv, path)
		for _, absent := range []string{`data-widget="add-transaction"`, `data-widget="import-transactions"`, `data-widget="rules"`} {
			if strings.Contains(body, absent) {
				t.Errorf("%s still contains %s", path, absent)
			}
		}
	}
	transactions := get(t, srv, "/transactions")
	for _, want := range []string{`data-widget="add-transaction"`, `data-widget="import-transactions"`, `data-widget="rules"`, `id="rule-form" hidden`} {
		if !strings.Contains(transactions, want) {
			t.Errorf("transactions page is missing %q", want)
		}
	}
}

func TestSecondaryCategoriesExpandWithinPrimaryCategory(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/expenses", url.Values{
		"amount": {"40"}, "currency": {"CHF"}, "new_category": {"Food"}, "note": {"Tagged shop"},
		"as_of": {"2026-08-01"},
	})
	post(t, srv, "/expenses/1/subcategory", url.Values{"subcategory": {"Supermarket"}})
	post(t, srv, "/expenses", url.Values{
		"amount": {"10"}, "currency": {"CHF"}, "new_category": {"Food"}, "note": {"Untagged shop"},
		"as_of": {"2026-08-02"},
	})
	post(t, srv, "/expenses", url.Values{
		"amount": {"20"}, "currency": {"CHF"}, "new_category": {"Travel"}, "note": {"No secondary"},
		"as_of": {"2026-08-03"},
	})

	body := get(t, srv, "/expenses?month=2026-08")
	start := strings.Index(body, `data-widget="period-categories"`)
	panel := body[start : strings.Index(body[start:], "</section>")+start]
	for _, want := range []string{"<details>", `<summary><a class="category-link"`, "Supermarket", "40.00", `subcategory=Supermarket`} {
		if !strings.Contains(panel, want) {
			t.Errorf("category breakdown is missing %q", want)
		}
	}
	if strings.Contains(panel, "Uncategorized") {
		t.Error("blank secondary categories render as Uncategorized")
	}
	if strings.Count(panel, "<details>") != 1 {
		t.Error("a category without secondary categories is expandable")
	}
	primary := get(t, srv, "/expenses?month=2026-08&category=Food")
	if !strings.Contains(primary, "Tagged shop") || !strings.Contains(primary, "Untagged shop") || strings.Contains(primary, "No secondary") {
		t.Error("primary category selection did not filter entries")
	}
	if !strings.Contains(primary, "<details open>") {
		t.Error("selected primary category is not expanded")
	}
	secondary := get(t, srv, "/expenses?month=2026-08&category=Food&subcategory=Supermarket")
	if !strings.Contains(secondary, "Tagged shop") || strings.Contains(secondary, "Untagged shop") {
		t.Error("secondary category selection did not filter entries")
	}
	if strings.Contains(body, `data-widget="period-subcategories"`) || strings.Contains(body, `name="secondary_category"`) {
		t.Error("standalone secondary-category controls still render")
	}
}

func TestExpenseCategoryCanBeReassigned(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/expenses", url.Values{
		"kind": {"expense"}, "amount": {"25"}, "currency": {"CHF"},
		"new_category": {"Other"}, "note": {"Quarterly bill"}, "as_of": {"2026-08-02"},
	})

	form := url.Values{"category": {"Taxes"}, "month": {"2026-08"}}
	req := httptest.NewRequest(http.MethodPost, "/expenses/1/category", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if got, want := rec.Header().Get("Location"), "/expenses?month=2026-08#entries"; got != want {
		t.Fatalf("redirect = %q, want %q", got, want)
	}

	body := get(t, srv, "/expenses?month=2026-08")
	if !strings.Contains(body, `<option value="Taxes" selected>Taxes</option>`) {
		t.Error("reassigned category is not selected in the entries table")
	}
	if !strings.Contains(body, `class="pill entry-category liability"`) {
		t.Error("reassigned tax entry is not rendered as tax")
	}
}

func TestExpenseSubcategoryCanBeSetInline(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/expenses", url.Values{
		"kind": {"expense"}, "amount": {"25"}, "currency": {"CHF"},
		"new_category": {"Groceries"}, "note": {"MIGROS"}, "as_of": {"2026-08-02"},
	})

	req := httptest.NewRequest(http.MethodPost, "/expenses/1/subcategory", strings.NewReader(url.Values{
		"subcategory": {"Supermarket"}, "month": {"2026-08"},
	}.Encode()))
	req.SetPathValue("id", "1")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if got := rec.Header().Get("Location"); got != "/expenses?month=2026-08#entries" {
		t.Fatalf("Location = %q", got)
	}
	page := get(t, srv, "/expenses?month=2026-08")
	if !strings.Contains(page, `name="subcategory" value="Supermarket"`) {
		t.Error("inline subcategory was not persisted or rendered")
	}
}

func TestExpenseMonthComparison(t *testing.T) {
	srv := newTestServer(t)
	for _, entry := range []url.Values{
		{"kind": {"expense"}, "amount": {"100"}, "currency": {"CHF"}, "new_category": {"Groceries"}, "as_of": {"2026-07-01"}},
		{"kind": {"expense"}, "amount": {"150"}, "currency": {"CHF"}, "new_category": {"Groceries"}, "as_of": {"2026-08-01"}},
		{"kind": {"expense"}, "amount": {"30"}, "currency": {"CHF"}, "new_category": {"Travel"}, "as_of": {"2026-08-02"}},
	} {
		post(t, srv, "/expenses", entry)
	}

	body := get(t, srv, "/expenses?month=2026-08")
	comparison := body[strings.Index(body, `data-widget="period-comparison"`):]
	comparison = comparison[:strings.Index(comparison, "</section>")]
	for _, want := range []string{
		"July 2026", "August 2026", "100.00", "180.00", "80.00",
	} {
		if !strings.Contains(comparison, want) {
			t.Errorf("comparison does not contain %q", want)
		}
	}
	if strings.Contains(body, `name="compare_`) {
		t.Error("monthly page has an independent comparison filter")
	}
}

func TestExpenseYearComparison(t *testing.T) {
	srv := newTestServer(t)
	for _, entry := range []url.Values{
		{"kind": {"expense"}, "amount": {"100"}, "currency": {"CHF"}, "new_category": {"Groceries"}, "as_of": {"2025-07-01"}},
		{"kind": {"expense"}, "amount": {"150"}, "currency": {"CHF"}, "new_category": {"Groceries"}, "as_of": {"2026-08-01"}},
		{"kind": {"expense"}, "amount": {"30"}, "currency": {"CHF"}, "new_category": {"Travel"}, "as_of": {"2026-08-02"}},
	} {
		post(t, srv, "/expenses", entry)
	}

	body := get(t, srv, "/expenses/year?year=2026")
	comparison := body[strings.Index(body, `data-widget="period-comparison"`):]
	comparison = comparison[:strings.Index(comparison, "</section>")]
	for _, want := range []string{
		"2025", "2026", "100.00", "180.00", "80.00",
	} {
		if !strings.Contains(comparison, want) {
			t.Errorf("year comparison does not contain %q", want)
		}
	}
	if strings.Contains(body, `name="compare_`) {
		t.Error("yearly page has an independent comparison filter")
	}
}

func TestAccountTableDoesNotRepeatItsOwnCurrency(t *testing.T) {
	srv := newTestServer(t)
	// A CHF account: the currency selector already says CHF, so neither Cash
	// nor Account total (also CHF) should spell out the currency again.
	post(t, srv, "/accounts", url.Values{"name": {"Viac"}, "kind": {"asset"}, "currency": {"CHF"}})
	post(t, srv, "/balances", url.Values{"account_id": {"1"}, "amount": {"100"}, "as_of": {"2026-01-01"}})

	// A EUR account: Cash stays unlabelled (the selector already says EUR),
	// but Account total is converted to CHF and needs to say so.
	post(t, srv, "/accounts", url.Values{"name": {"N26"}, "kind": {"asset"}, "currency": {"EUR"}})
	post(t, srv, "/balances", url.Values{"account_id": {"2"}, "amount": {"50"}, "as_of": {"2026-01-01"}})

	body := get(t, srv, "/")
	row := func(id string) string {
		s := body[strings.Index(body, `data-account="`+id+`"`):]
		return s[:strings.Index(s, "</tr>")]
	}

	if strings.Contains(row("1"), "CHF</strong>") {
		t.Error("a CHF account should not repeat CHF next to its own values")
	}
	if strings.Contains(row("2"), "EUR</strong>") {
		t.Error("cash should not repeat the account's own currency, already shown by the selector")
	}
	if strings.Contains(row("2"), "CHF</strong>") {
		t.Error("the table header already identifies converted totals as CHF")
	}
}

func TestAccountsRenderInOneTableWithInlineAddRow(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/accounts", url.Values{"name": {"Viac"}, "kind": {"asset"}, "currency": {"CHF"}})
	post(t, srv, "/accounts", url.Values{"name": {"N26"}, "kind": {"asset"}, "currency": {"EUR"}})

	body := get(t, srv, "/")
	if got := strings.Count(body, `data-widget="accounts"`); got != 1 {
		t.Fatalf("Accounts widgets = %d, want 1", got)
	}
	for _, want := range []string{
		`data-account="1"`, `data-account="2"`,
		`id="new-account-form" method="post" action="/accounts"`,
		`id="show-new-account"`,
		`class="new-account-row" id="new-account-row" hidden`,
		`form="new-account-form" name="name"`,
		`form="new-account-form" name="bank_ref"`,
		`action="/accounts/1/bank-ref"`,
		`id="account-funds-1" hidden`, `id="account-funds-2" hidden`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(body, "<h3>Add an account</h3>") {
		t.Error("account creation is still duplicated in the balance dialog")
	}
	if strings.Index(body, `id="new-account-row"`) > strings.Index(body, `data-account="1"`) {
		t.Error("new account row is not at the top of the accounts table")
	}
	for _, id := range []string{"1", "2"} {
		start := strings.Index(body, `data-account="`+id+`"`)
		accountRow := body[start : start+strings.Index(body[start:], "</tr>")]
		if strings.Contains(accountRow, `data-account-funds-toggle=`) {
			t.Errorf("empty account %s renders a fund disclosure toggle", id)
		}
	}
	if strings.Contains(body, `>Record balance</button>`) {
		t.Error("dashboard still renders the redundant Record balance button")
	}
}

func TestAccountCanBeCreatedWithOpeningBalance(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/accounts", url.Values{
		"name": {"New bank"}, "kind": {"asset"}, "currency": {"CHF"}, "class": {"cash"},
		"amount": {"123.45"}, "as_of": {"2026-08-26"},
	})

	body := get(t, srv, "/")
	row := body[strings.Index(body, `data-account="1"`):]
	row = row[:strings.Index(row, "</tr>")]
	if !strings.Contains(row, `data-balance-account="1"`) {
		t.Error("account total does not open balance entry")
	}
	if !strings.Contains(row, `data-balance-name="New bank" data-balance-currency="CHF"`) {
		t.Error("account total is missing balance dialog context")
	}
	if !strings.Contains(row, "123.45") || !strings.Contains(row, "as of 2026-08-26") {
		t.Errorf("opening balance is missing from account row: %s", row)
	}
	if strings.Contains(body, "No balance") {
		t.Error("accounts table still renders No balance text")
	}
	if strings.Contains(body, `<select name="account_id"`) {
		t.Error("balance dialog still allows account selection")
	}
	if !strings.Contains(body, `<input type="hidden" name="account_id">`) {
		t.Error("balance dialog is missing its fixed account ID")
	}
	if strings.Contains(body, "Record a cash balance") || strings.Contains(body, "Save balance") {
		t.Error("balance dialog still contains duplicated copy")
	}
	if strings.Contains(body, `<th class="num">Cash</th>`) {
		t.Error("Cash column is still rendered")
	}
}

func TestFundsCanBeUpdatedInline(t *testing.T) {
	srv := newTestServer(t)
	seed(t, srv)
	body := get(t, srv, "/")

	for _, want := range []string{
		`class="fund-name" data-fund-update="1"`,
		`class="fund-update-row" id="fund-update-1" hidden`,
		`action="/fund-updates" class="inline-fund-update"`,
		`name="fund_id" value="1"`,
		`action="/funds" class="inline-new-fund"`,
		`class="new-fund-row" id="new-fund-1" hidden`,
		`data-new-fund-account="1"`,
		`data-account-funds-toggle="1"`,
		`aria-controls="account-funds-1"`,
		`class="icon close-new-fund"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(body, "<h3>Update a fund</h3>") {
		t.Error("fund updates are still duplicated in the modal")
	}
}

func TestFundCanBeCreatedInlineWithInitialHolding(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/accounts", url.Values{"name": {"Broker"}, "kind": {"asset"}, "currency": {"CHF"}})
	post(t, srv, "/funds", url.Values{
		"account_id": {"1"}, "name": {"World ETF"}, "ticker": {"vwce"}, "currency": {"CHF"},
		"class": {"stocks"}, "units": {"10"}, "price": {"87.40"}, "as_of": {"2026-08-26"},
	})

	body := get(t, srv, "/")
	for _, want := range []string{"World ETF", "VWCE", "87.40", "874.00", `data-fund-update="1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("new fund is missing %q", want)
		}
	}
}

func TestChartsAreNeverEmptyLines(t *testing.T) {
	srv := newTestServer(t)
	seed(t, srv)

	// A single point would draw an invisible polyline; every chart needs two.
	for _, path := range []string{"/", "/retire"} {
		for _, line := range chartLines(get(t, srv, path)) {
			if strings.Count(line, ",") < 2 {
				t.Errorf("%s has a polyline with fewer than two points: %q", path, line)
			}
		}
	}
}

func TestGraphLinesStartAtZeroAndKeepEveryTooltipPoint(t *testing.T) {
	points := make([]datedValue, 48)
	for i := range points {
		points[i] = datedValue{date: fmt.Sprintf("2026-%02d", i+1), value: float64(1000 + i), label: fmt.Sprintf("%d.00", 1000+i)}
	}
	chart := plotFromZero(points)
	if len(chart.Dots) != len(points) {
		t.Fatalf("tooltip dots = %d, want %d", len(chart.Dots), len(points))
	}
	if chart.YTicks[0].Text != "0" || chart.YTicks[0].Y != chartHeight-chartPadY {
		t.Errorf("lowest tick = %+v, want zero at chart baseline", chart.YTicks[0])
	}
	if chart.Dots[0].Text != "2026-01: 1000.00" {
		t.Errorf("first tooltip = %q", chart.Dots[0].Text)
	}
}

func chartLines(body string) []string {
	var out []string
	for _, part := range strings.Split(body, `<polyline`)[1:] {
		start := strings.Index(part, `points="`)
		end := strings.Index(part[start+8:], `"`)
		out = append(out, part[start+8:start+8+end])
	}
	return out
}

func TestRatesAPIServesPairs(t *testing.T) {
	srv := newTestServer(t)

	body := get(t, srv, "/api/rates")
	for _, pair := range []string{"USD/CHF", "EUR/USD", "CHF/EUR"} {
		if !strings.Contains(body, pair) {
			t.Errorf("rates API does not quote %s: %s", pair, body)
		}
	}
}

func TestCrossSitePostIsRejected(t *testing.T) {
	srv := newTestServer(t)

	for _, header := range []struct{ name, value string }{
		{"Sec-Fetch-Site", "cross-site"},
		{"Origin", "http://evil.example"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/accounts",
			strings.NewReader("name=Sneaky&kind=asset&currency=CHF"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set(header.name, header.value)

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", header.name, rec.Code)
		}
	}

	if body := get(t, srv, "/"); strings.Contains(body, "Sneaky") {
		t.Error("the cross-site request created an account")
	}
}

func TestImportUploadsBankCSV(t *testing.T) {
	srv := newTestServer(t)

	const export = `"Transaction date","Account or card number","Description","Income or expense","Amount","Currency","Category"
"31.07.2026","CH66 0022","SALDO ABSCHLUSS","Expense","-15.00","CHF","Fees"
"28.07.2026","****3388","DIGITEC GALAXUS, ZUERICH","Expense","-1'088.20","CHF","Shopping"
"27.07.2026","CH36 0022","OPEN SYSTEMS AG","Income","7'553.10","CHF","Salary & pensions"
"27.07.2026","CH36 0022","STICHTING DEGIRO","Expense","-3'000.00","CHF","Savings & investments"
`

	body := upload(t, srv, export)
	if !strings.Contains(body, "Imported") {
		t.Fatal("no import summary was rendered")
	}

	page := get(t, srv, "/expenses")
	if !strings.Contains(page, "1,088.20") {
		t.Error("the imported shopping expense is missing")
	}
	if strings.Contains(page, "DEGIRO") {
		t.Error("the investment transfer was imported as spending")
	}
	entries, err := srv.store.Expenses(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var investment *store.Expense
	for i := range entries {
		if strings.Contains(entries[i].Note, "DEGIRO") {
			investment = &entries[i]
			break
		}
	}
	if investment == nil || investment.Kind != store.KindTransfer || investment.AccountRef != "CH36 0022" {
		t.Errorf("investment transfer was not retained with its account reference: %+v", investment)
	}
	// 15.00 + 1,088.20, with the salary counted as income and the transfer left out.
	if !strings.Contains(page, "1,103.20") {
		t.Error("the monthly spending is not the expected 1,103.20")
	}
	if !strings.Contains(page, "7,553.10") {
		t.Error("the salary was not recorded as income")
	}
	if !strings.Contains(page, "6,449.90") {
		t.Error("the month does not show 6,449.90 saved")
	}

	// Importing the same file again must not double anything up.
	upload(t, srv, export)
	page = get(t, srv, "/expenses")
	if !strings.Contains(page, "1,103.20") {
		t.Error("re-importing the same export changed the total")
	}
}

// upload posts a CSV to the importer the way the browser form does.
func upload(t *testing.T, srv *Server, csv string) string {
	return uploadMany(t, srv, csv)
}

func uploadMany(t *testing.T, srv *Server, csvs ...string) string {
	t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for i, csv := range csvs {
		part, err := form.CreateFormFile("file", fmt.Sprintf("export-%d.csv", i+1))
		if err != nil {
			t.Fatalf("build upload: %v", err)
		}
		io.WriteString(part, csv)
	}
	form.Close()

	req := httptest.NewRequest(http.MethodPost, "/expenses/import", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import returned %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

func TestMultipleCSVFilesImportAndMatchTogether(t *testing.T) {
	srv := newTestServer(t)
	const incoming = `"Transaction date","Account or card number","Description","Income or expense","Amount","Currency","Category"
"15.07.2026","ACCOUNT-B","Incoming transfer","Income","10'000.00","CHF","Other income"`
	const outgoing = `"Transaction date","Account or card number","Description","Income or expense","Amount","Currency","Category"
"15.07.2026","ACCOUNT-A","Outgoing transfer","Expense","-10'000.00","CHF","Account transfers"`

	result := uploadMany(t, srv, incoming, outgoing)
	if !strings.Contains(result, `class="value">2</strong>`) {
		t.Error("import result does not report two processed files")
	}
	entries, err := srv.store.Expenses(t.Context())
	if err != nil || len(entries) != 2 || entries[0].Kind != store.KindTransfer || entries[1].Kind != store.KindTransfer {
		t.Fatalf("multi-file transfer pair = %+v, err %v", entries, err)
	}
}

func TestMultipleCSVFilesFailAsOneBatch(t *testing.T) {
	srv := newTestServer(t)
	const valid = `"Transaction date","Description","Income or expense","Amount","Currency","Category"
"15.07.2026","Lunch","Expense","-20.00","CHF","Restaurants"`

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for i, csv := range []string{valid, "not,a,bank,export"} {
		part, err := form.CreateFormFile("file", fmt.Sprintf("export-%d.csv", i+1))
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(part, csv)
	}
	form.Close()
	req := httptest.NewRequest(http.MethodPost, "/expenses/import", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("invalid batch returned %d, want redirect", rec.Code)
	}
	entries, err := srv.store.Expenses(t.Context())
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid batch partially imported entries: %+v, err %v", entries, err)
	}
}

func TestRefundsAreImportedAndNetOff(t *testing.T) {
	srv := newTestServer(t)

	const export = `"Transaction date","Account or card number","Description","Income or expense","Amount","Currency","Category"
"20.07.2026","****3388","PROKO SHOP","Expense","-500.00","CHF","Household"
"30.07.2026","****3388","PROKO REFUND","Expense","97.10","CHF","Household"
"27.07.2026","CH36 0022","SALARY","Income","7'553.10","CHF","Salary & pensions"
`

	upload(t, srv, export)

	page := get(t, srv, "/expenses?month=2026-07")
	if !strings.Contains(page, "-97.10") {
		t.Error("the refund was not imported")
	}
	if !strings.Contains(page, "402.90") {
		t.Error("the month does not net to 402.90")
	}
	if !strings.Contains(page, "after 97.10 of refunds") {
		t.Error("the refund total is not shown")
	}
	if !strings.Contains(page, "7,150.20") {
		t.Error("the saving of 7,150.20 is not shown")
	}
}

func TestSavingsFeedTheRetirementPlan(t *testing.T) {
	srv := newTestServer(t)

	post(t, srv, "/expenses", url.Values{
		"kind": {"income"}, "amount": {"5000"}, "currency": {"CHF"},
		"new_category": {"Salary"}, "as_of": {"2026-07-05"},
	})
	post(t, srv, "/expenses", url.Values{
		"kind": {"expense"}, "amount": {"3000"}, "currency": {"CHF"},
		"new_category": {"Rent"}, "as_of": {"2026-07-06"},
	})

	// The retirement page starts from what is actually left over each month.
	if page := get(t, srv, "/retire"); !strings.Contains(page, `value="2000.00"`) {
		t.Error("monthly savings was not prefilled with 2000.00")
	}
}

func TestFixAccountCurrency(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/accounts", url.Values{"name": {"UBS Main"}, "kind": {"asset"}, "currency": {"EUR"}})
	post(t, srv, "/balances", url.Values{"account_id": {"1"}, "amount": {"100"}, "as_of": {"2026-01-01"}})

	if page := get(t, srv, "/"); !strings.Contains(page, "100.00 EUR") {
		t.Fatal("account was not created in EUR")
	}

	post(t, srv, "/accounts/1/currency", url.Values{"currency": {"CHF"}})

	page := get(t, srv, "/")
	if strings.Contains(page, "100.00 EUR") {
		t.Error("account is still shown in EUR")
	}
	if !strings.Contains(page, "100.00 CHF") {
		t.Error("account was not relabelled to CHF")
	}
}

func TestResetDashboardChangeUntilNextEntry(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/accounts", url.Values{"name": {"UBS"}, "kind": {"asset"}, "currency": {"CHF"}})
	post(t, srv, "/balances", url.Values{"account_id": {"1"}, "amount": {"100"}, "as_of": {"2026-01-01"}})
	post(t, srv, "/balances", url.Values{"account_id": {"1"}, "amount": {"150"}, "as_of": {"2026-02-01"}})

	if page := get(t, srv, "/"); !strings.Contains(page, "since last entry") {
		t.Fatal("dashboard did not show a change before reset")
	}

	post(t, srv, "/dashboard/change/reset", nil)
	if page := get(t, srv, "/"); strings.Contains(page, "since last entry") {
		t.Fatal("dashboard still showed the dismissed change")
	}

	post(t, srv, "/balances", url.Values{"account_id": {"1"}, "amount": {"175"}, "as_of": {"2026-03-01"}})
	if page := get(t, srv, "/"); !strings.Contains(page, "since last entry") {
		t.Fatal("dashboard did not show the change after a newer entry")
	}
}

func TestDashboardResetWindowsChartHistories(t *testing.T) {
	netWorth := []store.Point{
		{Date: "2026-01-01"},
		{Date: "2026-02-01"},
		{Date: "2026-03-01"},
	}
	if got := historySince(netWorth, "2026-03-01"); len(got) != 1 || got[0].Date != "2026-03-01" {
		t.Errorf("historySince reset = %+v, want only current point", got)
	}

	funds := []store.ValuePoint{
		{AsOf: "2026-01-01"},
		{AsOf: "2026-02-01"},
		{AsOf: "2026-03-01"},
	}
	if got := investHistorySince(funds, "2026-03-01"); len(got) != 1 || got[0].AsOf != "2026-03-01" {
		t.Errorf("investHistorySince reset = %+v, want only current point", got)
	}

	if got := historySince(netWorth, "2026-02-01"); len(got) != 2 {
		t.Errorf("historySince newer entry has %d points, want reset point plus newer point", len(got))
	}
	if got := investHistorySince(funds, "2026-02-01"); len(got) != 2 {
		t.Errorf("investHistorySince newer entry has %d points, want reset point plus newer point", len(got))
	}
}

func TestNetWorthCurrencyConversion(t *testing.T) {
	if got, ok := inCurrency(10000, 0.8); !ok || got != 12500 {
		t.Errorf("inCurrency(100 CHF, 0.8) = %s, %v; want 125.00, true", got, ok)
	}
	if got, ok := inCurrency(-10000, 0.8); !ok || got != -12500 {
		t.Errorf("inCurrency(-100 CHF, 0.8) = %s, %v; want -125.00, true", got, ok)
	}
	if _, ok := inCurrency(10000, 0); ok {
		t.Error("inCurrency with a missing rate reported success")
	}
}

func TestAccountClassMovesBalanceOutOfLiquidity(t *testing.T) {
	srv := newTestServer(t)
	post(t, srv, "/accounts", url.Values{"name": {"Viac"}, "kind": {"asset"}, "currency": {"CHF"}})
	post(t, srv, "/balances", url.Values{"account_id": {"1"}, "amount": {"5000"}, "as_of": {"2026-01-01"}})

	// A plain cash balance defaults to the liquid "cash" class.
	page := get(t, srv, "/")
	if !strings.Contains(page, "5,000.00") || !strings.Contains(page, "Liquid") {
		t.Fatal("balance was not shown as liquid cash")
	}

	// Viac is a pension held entirely in stocks: reclassify it.
	post(t, srv, "/accounts/1/class", url.Values{"class": {"stocks"}})

	page = get(t, srv, "/")
	if !strings.Contains(page, `class="pill class-stocks"`) {
		t.Error("account was not relabelled to stocks")
	}
	if !strings.Contains(page, "Not liquid") || !strings.Contains(page, "5,000.00") {
		t.Error("reclassified balance did not move into the not-liquid bucket")
	}
}

func TestFundClassGroupsAllocation(t *testing.T) {
	srv := newTestServer(t)
	seed(t, srv)

	// seed() creates fund 1 as an ETF, which defaults to the "stocks" class.
	page := get(t, srv, "/")
	if !strings.Contains(page, `class="pill class-stocks"`) {
		t.Fatal("fund was not created with the default stocks class")
	}

	post(t, srv, "/funds/1/class", url.Values{"class": {"bonds"}})

	page = get(t, srv, "/")
	if !strings.Contains(page, `class="pill class-bonds"`) {
		t.Error("fund was not relabelled to bonds")
	}
}

func TestDeleteMonthClearsOnlyThatMonth(t *testing.T) {
	srv := newTestServer(t)

	for _, month := range []string{"2026-07-05", "2026-08-05"} {
		post(t, srv, "/expenses", url.Values{
			"amount": {"100"}, "currency": {"CHF"},
			"new_category": {"Rent"}, "as_of": {month},
		})
	}

	post(t, srv, "/expenses/month/2026-08/delete", url.Values{})

	if page := get(t, srv, "/expenses?month=2026-08"); !strings.Contains(page, "No transactions recorded") {
		t.Error("August still has entries")
	}
	if page := get(t, srv, "/expenses?month=2026-07"); !strings.Contains(page, "100.00") {
		t.Error("July was deleted too")
	}
}

func TestDeleteAllTransactionsIncludesTransfers(t *testing.T) {
	srv := newTestServer(t)
	for _, kind := range []string{store.KindExpense, store.KindTransfer} {
		post(t, srv, "/expenses", url.Values{
			"kind": {kind}, "amount": {"100"}, "currency": {"CHF"},
			"new_category": {"Test"}, "as_of": {"2026-08-05"},
		})
	}

	if page := get(t, srv, "/transactions"); !strings.Contains(page, `action="/expenses/delete-all"`) {
		t.Fatal("Transactions page has no delete-all action")
	}
	post(t, srv, "/expenses/delete-all", url.Values{})

	entries, err := srv.store.Expenses(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("stored transactions = %d, want 0", len(entries))
	}
	if page := get(t, srv, "/transactions"); strings.Contains(page, `action="/expenses/delete-all"`) {
		t.Error("delete-all action remains visible with no transactions")
	}
}

func TestCategoryRuleAppliesToOldAndNewEntries(t *testing.T) {
	srv := newTestServer(t)

	const export = `"Transaction date","Account or card number","Description","Income or expense","Amount","Currency","Category"
"31.07.2026","CH66 0022","IHLY THOMAS CH KILLWANGEN 8956","Expense","-100.00","CHF","Other transactions"
"17.07.2026","CH66 0022","IHLY THOMAS REBAECKERSTRASSE 6A","Expense","-1'947.00","CHF","Other transactions"
"10.07.2026","****3388","Migros M EX Sihlpassage","Expense","-1.25","CHF","Groceries"
`
	upload(t, srv, export)

	// The rule arrives after the import and still fixes what is already stored.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rules",
		strings.NewReader(url.Values{"pattern": {"IHLY THOMAS"}, "new_category": {"Rent"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rec, req)

	if location := rec.Header().Get("Location"); !strings.Contains(location, "2+existing") {
		t.Errorf("Location = %q, want it to report two entries moved", location)
	}

	page := get(t, srv, "/expenses?month=2026-07")
	if strings.Contains(page, "Other transactions") {
		t.Error("the old category is still there")
	}
	if !strings.Contains(page, "Rent") {
		t.Error("the entries were not moved to Rent")
	}

	// A later import of the same kind of line lands in Rent straight away.
	const later = `"Transaction date","Description","Income or expense","Amount","Currency","Category"
"15.08.2026","IHLY THOMAS 8956 KILLWANGEN","Expense","-1'947.00","CHF","Other transactions"
`
	upload(t, srv, later)

	august := get(t, srv, "/expenses?month=2026-08")
	if strings.Contains(august, "Other transactions") {
		t.Error("the rule was not applied during the import")
	}
	if !strings.Contains(august, "1,947.00") {
		t.Error("the August entry is missing")
	}
}

func TestCategoryRuleSupportsAllTerms(t *testing.T) {
	srv := newTestServer(t)
	for _, entry := range []url.Values{
		{"kind": {"expense"}, "amount": {"100"}, "currency": {"CHF"}, "new_category": {"Other"}, "note": {"IHLY THOMAS KILLWANGEN"}, "as_of": {"2026-08-01"}},
		{"kind": {"expense"}, "amount": {"50"}, "currency": {"CHF"}, "new_category": {"Other"}, "note": {"THOMAS MUELLER"}, "as_of": {"2026-08-02"}},
	} {
		post(t, srv, "/expenses", entry)
	}

	req := httptest.NewRequest(http.MethodPost, "/rules", strings.NewReader(url.Values{
		"mode": {"all"}, "pattern": {"ihly\nthomas"}, "new_category": {"Rent"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if location := rec.Header().Get("Location"); !strings.Contains(location, "1+existing") {
		t.Fatalf("Location = %q, want one moved entry", location)
	}

	page := get(t, srv, "/expenses?month=2026-08")
	if strings.Count(page, `<option value="Rent" selected>Rent</option>`) != 1 {
		t.Error("AND rule did not move exactly one matching entry")
	}
	if !strings.Contains(page, `<span class="pill cur">AND</span>`) || !strings.Contains(page, "ihly\nthomas") {
		page = get(t, srv, "/transactions")
	}
	if !strings.Contains(page, `<span class="pill cur">AND</span>`) || !strings.Contains(page, "ihly\nthomas") {
		t.Error("saved AND rule is not rendered with its terms")
	}
}

func TestCategoryRuleCanBeEditedAndShowsMatchCount(t *testing.T) {
	srv := newTestServer(t)
	for _, entry := range []url.Values{
		{"kind": {"expense"}, "amount": {"100"}, "currency": {"CHF"}, "new_category": {"Other"}, "note": {"MIGROS CITY ZURICH"}, "as_of": {"2026-08-01"}},
		{"kind": {"expense"}, "amount": {"50"}, "currency": {"CHF"}, "new_category": {"Other"}, "note": {"MIGROS BASEL"}, "as_of": {"2026-08-02"}},
	} {
		post(t, srv, "/expenses", entry)
	}
	post(t, srv, "/rules", url.Values{
		"mode": {"any"}, "pattern": {"coop"}, "new_category": {"Groceries"},
	})

	page := get(t, srv, "/transactions")
	if !strings.Contains(page, `data-rule-id="1"`) || !strings.Contains(page, ">0 entries</td>") {
		t.Error("saved rule does not show its zero match count or edit control")
	}

	req := httptest.NewRequest(http.MethodPost, "/rules/1", strings.NewReader(url.Values{
		"mode": {"all"}, "pattern": {"migros\nzurich"}, "new_category": {"Food"}, "subcategory": {"Supermarket"},
	}.Encode()))
	req.SetPathValue("id", "1")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if location := rec.Header().Get("Location"); !strings.Contains(location, "1+existing") {
		t.Fatalf("Location = %q, want one moved entry", location)
	}

	page = get(t, srv, "/transactions")
	for _, want := range []string{
		`data-rule-mode="all"`, `data-rule-pattern="migros`, "zurich",
		`data-rule-category="Food"`, `data-rule-subcategory="Supermarket"`,
		`>1 entry</td>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("updated rule page missing %q", want)
		}
	}
}

func TestBadInputRedirectsWithMessage(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/balances",
		strings.NewReader("account_id=1&amount=not-a-number"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if location := rec.Header().Get("Location"); !strings.Contains(location, "err=") {
		t.Errorf("Location = %q, want an error message", location)
	}
}
