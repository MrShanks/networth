package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/money"
	"github.com/MrShanks/networth/internal/store"
)

type expensesData struct {
	Base       string
	Currencies []string
	Report     store.ExpenseReport
	Month      store.ExpenseMonth
	Known      bool
	Categories []string
	ByCategory []store.CategorySummary
	Rules      []store.Rule
	Bars       BarChart
	Today      string
	Notice     string
	Error      string
}

func (s *Server) handleExpenses(w http.ResponseWriter, r *http.Request) {
	v, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	report := v.report
	month, known := report.Find(r.URL.Query().Get("month"))

	rules, err := s.store.Rules(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	s.render(w, "expenses.html", expensesData{
		Base:       store.Base,
		Currencies: store.Currencies,
		Report:     report,
		Month:      month,
		Known:      known,
		Categories: report.UsedCategories(),
		ByCategory: report.ByCategory(),
		Rules:      rules,
		Bars:       buildBars(report.Months, month.Month),
		Today:      time.Now().Format("2006-01-02"),
		Notice:     r.URL.Query().Get("msg"),
		Error:      r.URL.Query().Get("err"),
	})
}

func (s *Server) handleAddExpense(w http.ResponseWriter, r *http.Request) {
	amount, err := money.Parse(r.FormValue("amount"))
	if err != nil {
		s.redirectTo(w, r, "/expenses", "amount must look like 42.90")
		return
	}
	category := r.FormValue("category")
	if other := strings.TrimSpace(r.FormValue("new_category")); other != "" {
		category = other
	}

	kind := store.KindExpense
	if r.FormValue("kind") == store.KindIncome {
		kind = store.KindIncome
	}

	err = s.store.AddEntry(r.Context(), kind, s.date(r), category,
		r.FormValue("note"), r.FormValue("currency"), amount)
	if err != nil {
		s.redirectTo(w, r, "/expenses", err.Error())
		return
	}
	s.redirectTo(w, r, "/expenses", "")
}

func (s *Server) handleDeleteExpense(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirectTo(w, r, "/expenses", "invalid id")
		return
	}
	if err := s.store.DeleteExpense(r.Context(), id); err != nil {
		s.redirectTo(w, r, "/expenses", err.Error())
		return
	}
	s.redirectTo(w, r, "/expenses", "")
}

func (s *Server) handleDeleteMonth(w http.ResponseWriter, r *http.Request) {
	month := r.PathValue("month")
	if _, err := s.store.DeleteExpensesForMonth(r.Context(), month); err != nil {
		s.redirectTo(w, r, "/expenses", err.Error())
		return
	}
	s.redirectTo(w, r, "/expenses", "")
}

// handleAddRule saves a rule and applies it to what is already stored, so a
// category can be fixed everywhere at once.
func (s *Server) handleAddRule(w http.ResponseWriter, r *http.Request) {
	pattern := r.FormValue("pattern")
	category := r.FormValue("category")
	if other := strings.TrimSpace(r.FormValue("new_category")); other != "" {
		category = other
	}

	if err := s.store.AddRule(r.Context(), pattern, category); err != nil {
		s.redirectTo(w, r, "/expenses", "could not add the rule: "+err.Error())
		return
	}

	moved, err := s.store.Recategorise(r.Context(),
		store.Rule{Pattern: strings.TrimSpace(pattern), Category: strings.TrimSpace(category)})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.noticeTo(w, r, "/expenses",
		fmt.Sprintf("Rule saved, %d existing entr%s moved to %s.",
			moved, plural(moved, "y", "ies"), strings.TrimSpace(category)))
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirectTo(w, r, "/expenses", "invalid id")
		return
	}
	if err := s.store.DeleteRule(r.Context(), id); err != nil {
		s.redirectTo(w, r, "/expenses", err.Error())
		return
	}
	s.redirectTo(w, r, "/expenses", "")
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
