package web

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/money"
	"github.com/MrShanks/networth/internal/store"
)

type expensePeriodData struct {
	Base            string
	Period          store.ExpenseMonth
	Comparison      monthComparison
	Entries         []store.Expense
	EntryTitle      string
	Category        string
	Subcategory     string
	Kind            string
	Query           string
	SearchAction    string
	Categories      []string
	Breakdown       []categoryBreakdown
	IncomeBreakdown []store.CategoryTotal
	SalaryAverages  []salaryAverage
	PeriodURL       string
	FilterPrefix    string
	EntryTotal      int
	EntryFrom       int
	EntryTo         int
	PreviousPageURL string
	NextPageURL     string
	Bars            BarChart
	IsYear          bool
	IsAll           bool
	Notice          string
	Error           string
}

type salaryAverage struct {
	Subcategory string
	Average     money.Amount
}

type categoryBreakdown struct {
	store.CategoryTotal
	Subcategories []store.SubcategorySummary
}

type transactionsData struct {
	Base            string
	Currencies      []string
	Categories      []string
	Rules           []ruleView
	HasTransactions bool
	Today           string
	Notice          string
	Error           string
}

func periodName(period string, yearly, all bool) string {
	if yearly || all {
		return period
	}
	return monthName(period)
}

type monthComparison struct {
	Left       store.ExpenseMonth
	Right      store.ExpenseMonth
	Categories []categoryComparison
}

type categoryComparison struct {
	Category string
	Left     money.Amount
	Right    money.Amount
	Change   money.Amount
}

type ruleView struct {
	store.Rule
	Matches int
}

func (s *Server) handleExpenses(w http.ResponseWriter, r *http.Request) {
	s.handleExpensePeriod(w, r, false, false)
}

func (s *Server) handleYearExpenses(w http.ResponseWriter, r *http.Request) {
	s.handleExpensePeriod(w, r, true, false)
}

func (s *Server) handleAllExpenses(w http.ResponseWriter, r *http.Request) {
	s.handleExpensePeriod(w, r, false, true)
}

func (s *Server) handleExpensePeriod(w http.ResponseWriter, r *http.Request, yearly, all bool) {
	v, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	selected := strings.TrimSpace(r.URL.Query().Get("month"))
	if yearly {
		selected = strings.TrimSpace(r.URL.Query().Get("year"))
		if selected == "" {
			selected = latestYear(v.report)
		}
	}
	period, _ := v.report.Find(selected)
	previousKey := previousMonth(period.Month)
	if yearly {
		period = v.report.Year(selected)
		previousKey = previousYear(selected)
	} else if all {
		period = v.report.All()
	}
	previous, _ := v.report.Find(previousKey)
	if yearly {
		previous = v.report.Year(previousKey)
	}

	category := strings.TrimSpace(r.URL.Query().Get("category"))
	subcategory := strings.TrimSpace(r.URL.Query().Get("subcategory"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	query := cleanNoteQuery(r.URL.Query().Get("q"))
	entries := period.Expenses
	entryTitle := monthName(period.Month) + " entries"
	if yearly || all {
		entryTitle = period.Month + " entries"
	}
	if category != "" {
		entries = filterExpenses(entries, category, subcategory, kind)
		entryTitle += " · " + category
		if kind == store.KindIncome {
			entryTitle += " income"
		}
		if subcategory != "" {
			entryTitle += " · " + subcategory
		}
	}
	if query != "" {
		entries = filterByNote(entries, query)
		entryTitle += fmt.Sprintf(" · %q", query)
	}
	entryTotal := len(entries)
	entryFrom, entryTo := 0, entryTotal
	previousPageURL, nextPageURL := "", ""
	if yearly || all {
		page := queryPage(r)
		entryFrom = min((page-1)*100, entryTotal)
		entryTo = min(entryFrom+100, entryTotal)
		entries = entries[entryFrom:entryTo]
		if page > 1 {
			previousPageURL = expensePageURL(r, page-1)
		}
		if entryTo < entryTotal {
			nextPageURL = expensePageURL(r, page+1)
		}
	}
	bars := BarChart{Width: chartWidth, Height: chartHeight, Empty: true}
	if yearly {
		bars = buildBars(monthsInYear(v.report.Months, selected), "")
	}
	months := 1
	if yearly {
		months = len(monthsInYear(v.report.Months, selected))
	} else if all {
		months = len(v.report.Months)
	}
	periodURL := "/expenses?month=" + period.Month
	searchAction := "/expenses"
	if yearly {
		periodURL = "/expenses/year?year=" + period.Month
		searchAction = "/expenses/year"
	} else if all {
		periodURL = "/expenses/all"
		searchAction = "/expenses/all"
	}
	filterPrefix := periodURL + "&"
	if all {
		filterPrefix = periodURL + "?"
	}

	s.render(w, "expense-period.html", expensePeriodData{
		Base: store.Base, Period: period, Comparison: comparePeriods(previous, period),
		Entries: entries, EntryTitle: entryTitle, Category: category, Subcategory: subcategory,
		Kind: kind, Query: query, SearchAction: searchAction,
		Categories: v.report.UsedCategories(), Breakdown: categoryBreakdowns(period),
		IncomeBreakdown: period.IncomeCategories,
		SalaryAverages:  salaryAverages(period, months),
		PeriodURL:       periodURL, FilterPrefix: filterPrefix,
		EntryTotal: entryTotal, EntryFrom: entryFrom + 1, EntryTo: entryTo,
		PreviousPageURL: previousPageURL, NextPageURL: nextPageURL,
		Bars: bars, IsYear: yearly, IsAll: all,
		Notice: r.URL.Query().Get("msg"), Error: r.URL.Query().Get("err"),
	})
}

func queryPage(r *http.Request) int {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

func expensePageURL(r *http.Request, page int) string {
	query := r.URL.Query()
	if page <= 1 {
		query.Del("page")
	} else {
		query.Set("page", strconv.Itoa(page))
	}
	url := r.URL.Path
	if encoded := query.Encode(); encoded != "" {
		url += "?" + encoded
	}
	return url + "#entries"
}

func salaryAverages(period store.ExpenseMonth, months int) []salaryAverage {
	if months == 0 {
		return nil
	}
	averages := make([]salaryAverage, 0, len(period.IncomeSubcategories))
	for _, subcategory := range period.IncomeSubcategories {
		if salaryCategory(subcategory.Category) && strings.TrimSpace(subcategory.Subcategory) != "" {
			averages = append(averages, salaryAverage{
				Subcategory: subcategory.Subcategory,
				Average:     subcategory.Total / money.Amount(months),
			})
		}
	}
	slices.SortFunc(averages, func(a, b salaryAverage) int { return cmp.Compare(b.Average, a.Average) })
	return averages
}

func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	v, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	rules, err := s.store.Rules(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "transactions.html", transactionsData{
		Base: store.Base, Currencies: store.Currencies, Categories: v.report.UsedCategories(),
		Rules: ruleViews(rules, v.report), HasTransactions: len(v.report.Months) > 0,
		Today:  time.Now().Format("2006-01-02"),
		Notice: r.URL.Query().Get("msg"), Error: r.URL.Query().Get("err"),
	})
}

func latestYear(report store.ExpenseReport) string {
	if len(report.Months) == 0 {
		return time.Now().Format("2006")
	}
	return report.Months[len(report.Months)-1].Month[:4]
}

func previousMonth(month string) string {
	parsed, err := time.Parse("2006-01", month)
	if err != nil {
		return ""
	}
	return parsed.AddDate(0, -1, 0).Format("2006-01")
}

func previousYear(year string) string {
	value, err := strconv.Atoi(year)
	if err != nil {
		return ""
	}
	return strconv.Itoa(value - 1)
}

func monthsInYear(months []store.ExpenseMonth, year string) []store.ExpenseMonth {
	var filtered []store.ExpenseMonth
	for _, month := range months {
		if strings.HasPrefix(month.Month, year) {
			filtered = append(filtered, month)
		}
	}
	return filtered
}

func categoryBreakdowns(period store.ExpenseMonth) []categoryBreakdown {
	breakdowns := make([]categoryBreakdown, 0, len(period.Categories))
	for _, category := range period.Categories {
		row := categoryBreakdown{CategoryTotal: category}
		for _, subcategory := range period.Subcategories {
			if strings.EqualFold(subcategory.Category, category.Category) {
				summary := store.SubcategorySummary{
					Subcategory: subcategory.Subcategory,
					Total:       subcategory.Total,
				}
				if category.Total != 0 {
					summary.Share = float64(subcategory.Total) / float64(category.Total) * 100
				}
				row.Subcategories = append(row.Subcategories, summary)
			}
		}
		slices.SortFunc(row.Subcategories, func(a, b store.SubcategorySummary) int {
			return cmp.Compare(b.Total, a.Total)
		})
		breakdowns = append(breakdowns, row)
	}
	return breakdowns
}

func ruleViews(rules []store.Rule, report store.ExpenseReport) []ruleView {
	views := make([]ruleView, 0, len(rules))
	for _, rule := range rules {
		matches := 0
		for _, month := range report.Months {
			for _, expense := range month.Expenses {
				if rule.Matches(expense.Note) {
					matches++
				}
			}
		}
		views = append(views, ruleView{Rule: rule, Matches: matches})
	}
	return views
}

func comparePeriods(leftMonth, rightMonth store.ExpenseMonth) monthComparison {
	byCategory := make(map[string]*categoryComparison)
	for _, category := range leftMonth.Categories {
		byCategory[category.Category] = &categoryComparison{Category: category.Category, Left: category.Total}
	}
	for _, category := range rightMonth.Categories {
		row := byCategory[category.Category]
		if row == nil {
			row = &categoryComparison{Category: category.Category}
			byCategory[category.Category] = row
		}
		row.Right = category.Total
	}
	comparison := monthComparison{Left: leftMonth, Right: rightMonth}
	for _, row := range byCategory {
		row.Change = row.Right - row.Left
		comparison.Categories = append(comparison.Categories, *row)
	}
	slices.SortFunc(comparison.Categories, func(a, b categoryComparison) int {
		return cmp.Compare(max(b.Left, b.Right), max(a.Left, a.Right))
	})
	return comparison
}

func filterExpenses(expenses []store.Expense, category, subcategory, kind string) []store.Expense {
	var filtered []store.Expense
	for _, expense := range expenses {
		if strings.EqualFold(expense.Category, category) &&
			(subcategory == "" || strings.EqualFold(expense.Subcategory, subcategory)) &&
			(kind == "" || expense.Kind == kind) {
			filtered = append(filtered, expense)
		}
	}
	return filtered
}

// filterByNote keeps entries whose note contains query, case-insensitively.
func filterByNote(expenses []store.Expense, query string) []store.Expense {
	query = strings.ToLower(query)
	var filtered []store.Expense
	for _, expense := range expenses {
		if strings.Contains(strings.ToLower(expense.Note), query) {
			filtered = append(filtered, expense)
		}
	}
	return filtered
}

// cleanNoteQuery drops anything past the first tab or newline. Notes are always
// single-line, so a tab/newline in a pasted search means the user's mouse
// selection spilled over from the Note cell into a neighbouring table cell.
func cleanNoteQuery(query string) string {
	if idx := strings.IndexAny(query, "\t\n\r"); idx >= 0 {
		query = query[:idx]
	}
	return strings.TrimSpace(query)
}

func (s *Server) handleAddExpense(w http.ResponseWriter, r *http.Request) {
	amount, err := money.Parse(r.FormValue("amount"))
	if err != nil {
		s.redirectTo(w, r, "/transactions", "amount must look like 42.90")
		return
	}
	category := r.FormValue("category")
	if other := strings.TrimSpace(r.FormValue("new_category")); other != "" {
		category = other
	}

	kind := store.KindExpense
	if requested := r.FormValue("kind"); requested == store.KindIncome || requested == store.KindTax || requested == store.KindTransfer {
		kind = requested
	}

	err = s.store.AddEntry(r.Context(), kind, s.date(r), category,
		r.FormValue("note"), r.FormValue("currency"), amount)
	if err != nil {
		s.redirectTo(w, r, "/transactions", err.Error())
		return
	}
	s.redirectTo(w, r, "/transactions", "")
}

func (s *Server) handleDeleteExpense(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = s.store.DeleteExpense(r.Context(), id)
	}
	if inlineExpenseUpdate(w, r, err) {
		return
	}
	if err != nil {
		s.redirectTo(w, r, "/expenses", err.Error())
		return
	}
	s.redirectTo(w, r, "/expenses", "")
}

func (s *Server) handleSetExpenseCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = s.store.SetExpenseCategory(r.Context(), id, r.FormValue("category"))
	}
	if inlineExpenseUpdate(w, r, err) {
		return
	}
	values := url.Values{}
	if month := r.FormValue("month"); month != "" {
		values.Set("month", month)
	}
	if err != nil {
		values.Set("err", err.Error())
	}
	target := "/expenses"
	if r.FormValue("scope") == "all" {
		target = "/expenses/all"
	}
	if query := values.Encode(); query != "" {
		target += "?" + query
	}
	http.Redirect(w, r, target+"#entries", http.StatusSeeOther)
}

func (s *Server) handleSetExpenseSubcategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = s.store.SetExpenseSubcategory(r.Context(), id, r.FormValue("subcategory"))
	}
	if inlineExpenseUpdate(w, r, err) {
		return
	}
	values := url.Values{}
	if month := r.FormValue("month"); month != "" {
		values.Set("month", month)
	}
	if err != nil {
		values.Set("err", err.Error())
	}
	target := "/expenses"
	if r.FormValue("scope") == "all" {
		target = "/expenses/all"
	}
	if query := values.Encode(); query != "" {
		target += "?" + query
	}
	http.Redirect(w, r, target+"#entries", http.StatusSeeOther)
}

func inlineExpenseUpdate(w http.ResponseWriter, r *http.Request, err error) bool {
	if r.Header.Get("X-Requested-With") != "fetch" {
		return false
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (s *Server) handleDeleteMonth(w http.ResponseWriter, r *http.Request) {
	month := r.PathValue("month")
	if _, err := s.store.DeleteExpensesForMonth(r.Context(), month); err != nil {
		s.redirectTo(w, r, "/expenses", err.Error())
		return
	}
	s.redirectTo(w, r, "/expenses", "")
}

func (s *Server) handleDeleteAllTransactions(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.store.DeleteAllExpenses(r.Context())
	if err != nil {
		s.redirectTo(w, r, "/transactions", "could not delete transactions: "+err.Error())
		return
	}
	s.noticeTo(w, r, "/transactions", fmt.Sprintf("Deleted %d transaction%s.", deleted, plural(deleted, "", "s")))
}

// handleAddRule saves a rule and applies it to what is already stored, so a
// category can be fixed everywhere at once.
func (s *Server) handleAddRule(w http.ResponseWriter, r *http.Request) {
	mode := strings.TrimSpace(r.FormValue("mode"))
	pattern := r.FormValue("pattern")
	subcategory := strings.TrimSpace(r.FormValue("subcategory"))
	category := r.FormValue("category")
	if other := strings.TrimSpace(r.FormValue("new_category")); other != "" {
		category = other
	}

	if err := s.store.AddRule(r.Context(), mode, pattern, category, subcategory); err != nil {
		s.redirectTo(w, r, "/transactions", "could not add the rule: "+err.Error())
		return
	}

	moved, err := s.store.Recategorise(r.Context(),
		store.Rule{Mode: mode, Pattern: strings.TrimSpace(pattern), Category: strings.TrimSpace(category), Subcategory: subcategory})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.noticeTo(w, r, "/transactions",
		fmt.Sprintf("Rule saved, %d existing entr%s moved to %s.",
			moved, plural(moved, "y", "ies"), strings.TrimSpace(category)))
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirectTo(w, r, "/transactions", "invalid id")
		return
	}
	if err := s.store.DeleteRule(r.Context(), id); err != nil {
		s.redirectTo(w, r, "/transactions", err.Error())
		return
	}
	s.redirectTo(w, r, "/transactions", "")
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirectTo(w, r, "/transactions", "invalid rule")
		return
	}
	mode := strings.TrimSpace(r.FormValue("mode"))
	pattern := strings.TrimSpace(r.FormValue("pattern"))
	category := strings.TrimSpace(r.FormValue("new_category"))
	subcategory := strings.TrimSpace(r.FormValue("subcategory"))
	if err := s.store.UpdateRule(r.Context(), id, mode, pattern, category, subcategory); err != nil {
		s.redirectTo(w, r, "/transactions", "could not update the rule: "+err.Error())
		return
	}
	moved, err := s.store.Recategorise(r.Context(), store.Rule{
		ID: id, Mode: mode, Pattern: pattern, Category: category, Subcategory: subcategory,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.noticeTo(w, r, "/transactions", fmt.Sprintf("Rule updated, %d existing entr%s moved to %s.",
		moved, plural(moved, "y", "ies"), category))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Bar is one month's column in the spending chart.
type Bar struct {
	X, Y, W, H float64
	Label      string
	Title      string
	Current    bool
}

type BarChart struct {
	Width, Height float64
	Bars          []Bar
	YTicks        []chartLabel
	Empty         bool
}

// buildBars charts the last twelve months of spending.
func buildBars(months []store.ExpenseMonth, current string) BarChart {
	c := BarChart{Width: chartWidth, Height: chartHeight}
	if len(months) == 0 {
		c.Empty = true
		return c
	}
	if len(months) > 12 {
		months = months[len(months)-12:]
	}

	top := 0.0
	for _, m := range months {
		top = max(top, m.Total.Float())
	}
	if top <= 0 {
		top = 1
	}

	plotW := float64(chartWidth - 2*chartPadX)
	plotH := float64(chartHeight - 2*chartPadY)
	slot := plotW / float64(len(months))
	width := min(56, slot*0.6)

	for i, m := range months {
		h := plotH * max(0, m.Total.Float()) / top
		c.Bars = append(c.Bars, Bar{
			X:       chartPadX + slot*float64(i) + (slot-width)/2,
			Y:       chartPadY + plotH - h,
			W:       width,
			H:       h,
			Label:   m.Month,
			Title:   m.Month + ": " + m.Total.String(),
			Current: m.Month == current,
		})
	}

	for i := 0; i <= 4; i++ {
		v := top * float64(i) / 4
		c.YTicks = append(c.YTicks, chartLabel{
			X:    chartPadX,
			Y:    chartPadY + plotH*(1-float64(i)/4),
			Text: shortNumber(v),
		})
	}
	return c
}

// MonthName renders 2026-08 as "August 2026".
func monthName(month string) string {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return month
	}
	return fmt.Sprintf("%s %d", t.Month(), t.Year())
}
