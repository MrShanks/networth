package web

import (
	"bytes"
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
		{"/expenses", "Add an entry"},
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

	body := get(t, srv, "/expenses?month=2026-08")
	summary := body[strings.Index(body, `data-widget="summary-year"`):]
	summary = summary[:strings.Index(summary, "</section>")]
	if !strings.Contains(summary, `2026 summary (CHF)`) ||
		!strings.Contains(summary, `class="value down">25.00</strong>`) {
		t.Error("tax payments are not displayed as a red summary value")
	}
	if !strings.Contains(summary, `class="value">100.00</strong>`) {
		t.Error("monthly spending should exclude tax payments")
	}
	if !strings.Contains(body, `data-widget="summary-month"`) ||
		!strings.Contains(body, `August 2026 summary (CHF)`) {
		t.Error("latest month summary is missing")
	}
	body = get(t, srv, "/expenses?month=2025-08&year=2026")
	if !strings.Contains(body, `name="month" value="2025-08"`) ||
		!strings.Contains(body, `2025 summary (CHF)`) || strings.Contains(body, `name="year"`) {
		t.Error("the selected month should be the only control and determine the summary year")
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
	if !strings.Contains(body, `class="category-link" href="/expenses?month=2026-08`) ||
		!strings.Contains(body, `#entries">Food &amp; drink</a>`) {
		t.Error("month category is not linked to its entries")
	}
	if !strings.Contains(body, `class="category-link" href="/expenses?category=`) ||
		!strings.Contains(body, `scope=all#entries">Food &amp; drink</a>`) {
		t.Error("all-time category is not linked to its entries")
	}

	month := get(t, srv, "/expenses?month=2026-08&category=Food+%26+drink")
	if !strings.Contains(month, "August dinner") || strings.Contains(month, "July lunch") || strings.Contains(month, "Train") {
		t.Error("month category drill-down did not filter entries correctly")
	}

	all := get(t, srv, "/expenses?category=Food+%26+drink&scope=all")
	if !strings.Contains(all, "August dinner") || !strings.Contains(all, "July lunch") || strings.Contains(all, "Train") {
		t.Error("all-time category drill-down did not filter entries correctly")
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
	if !strings.Contains(body, `id="month-comparison"`) {
		t.Error("comparison widget is missing its navigation anchor")
	}
	comparison := body[strings.Index(body, `data-widget="month-comparison"`):]
	comparison = comparison[:strings.Index(comparison, "</section>")]
	for _, want := range []string{
		`action="/expenses#month-comparison"`,
		`name="compare_a" value="2026-07"`,
		`name="compare_b" value="2026-08"`,
		`<td class="num down">+80.00</td>`,
		"Groceries", "Travel",
	} {
		if !strings.Contains(comparison, want) {
			t.Errorf("comparison does not contain %q", want)
		}
	}

	body = get(t, srv, "/expenses?month=2026-08&compare_a=2026-08&compare_b=2026-07")
	if !strings.Contains(body, `name="compare_a" value="2026-08"`) ||
		!strings.Contains(body, `name="compare_b" value="2026-07"`) {
		t.Error("explicit comparison months were not preserved")
	}
}

func TestAccountWidgetDoesNotRepeatItsOwnCurrency(t *testing.T) {
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
	section := func(id string) string {
		s := body[strings.Index(body, `data-widget="account-`+id+`"`):]
		return s[:strings.Index(s, "</section>")]
	}

	if strings.Contains(section("1"), "CHF</strong>") {
		t.Error("a CHF account should not repeat CHF next to its own values")
	}
	if strings.Contains(section("2"), "EUR</strong>") {
		t.Error("cash should not repeat the account's own currency, already shown by the selector")
	}
	if got := strings.Count(section("2"), "CHF</strong>"); got != 1 {
		t.Errorf("a EUR account's converted total should mention CHF once, got %d", got)
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
	t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "export.csv")
	if err != nil {
		t.Fatalf("build upload: %v", err)
	}
	io.WriteString(part, csv)
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

	if page := get(t, srv, "/expenses?month=2026-08"); !strings.Contains(page, "No expenses recorded") {
		t.Error("August still has entries")
	}
	if page := get(t, srv, "/expenses?month=2026-07"); !strings.Contains(page, "100.00") {
		t.Error("July was deleted too")
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
