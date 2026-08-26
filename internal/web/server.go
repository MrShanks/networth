// Package web serves the net worth tracker UI.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/fx"
	"github.com/MrShanks/networth/internal/money"
	"github.com/MrShanks/networth/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	store *store.Store
	fx    *fx.Client
	tmpl  *template.Template
	log   *slog.Logger
	mux   *http.ServeMux
}

func absAmount(amount money.Amount) money.Amount {
	if amount < 0 {
		return -amount
	}
	return amount
}

func subAmount(a, b money.Amount) money.Amount { return a - b }

func NewServer(s *store.Store, rates *fx.Client, log *slog.Logger) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"spark":      sparkPoints,
		"trend":      sparkAmounts,
		"monthName":  monthName,
		"absAmount":  absAmount,
		"subAmount":  subAmount,
		"dict":       dict,
		"classLabel": store.ClassLabel,
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}

	srv := &Server{store: s, fx: rates, tmpl: tmpl, log: log, mux: http.NewServeMux()}
	srv.routes()
	return srv, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && !sameSite(r) {
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// sameSite blocks writes triggered by another site in the same browser. Both
// headers are absent outside browsers, which leaves scripting with curl free.
func sameSite(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func (s *Server) routes() {
	s.mux.Handle("GET /static/", http.FileServer(http.FS(assets)))
	s.mux.HandleFunc("GET /{$}", s.handleDashboard)
	s.mux.HandleFunc("POST /dashboard/change/reset", s.handleResetDashboardChange)
	s.mux.HandleFunc("POST /accounts", s.handleCreateAccount)
	s.mux.HandleFunc("POST /accounts/{id}/delete", s.handleDeleteAccount)
	s.mux.HandleFunc("POST /accounts/{id}/currency", s.handleSetAccountCurrency)
	s.mux.HandleFunc("POST /accounts/{id}/owner", s.handleSetAccountOwner)
	s.mux.HandleFunc("POST /accounts/{id}/class", s.handleSetAccountClass)
	s.mux.HandleFunc("POST /funds", s.handleCreateFund)
	s.mux.HandleFunc("POST /funds/{id}/delete", s.handleDeleteFund)
	s.mux.HandleFunc("POST /funds/{id}/class", s.handleSetFundClass)
	s.mux.HandleFunc("POST /balances", s.handleSetBalance)
	s.mux.HandleFunc("POST /balances/{id}/delete", s.handleDeleteBalance)
	s.mux.HandleFunc("POST /fund-updates", s.handleFundUpdate)
	s.mux.HandleFunc("POST /trades/{id}/delete", s.handleDeleteTrade)
	s.mux.HandleFunc("POST /prices/{id}/delete", s.handleDeletePrice)
	s.mux.HandleFunc("GET /api/rates", s.handleRatesAPI)
	s.mux.HandleFunc("GET /expenses", s.handleExpenses)
	s.mux.HandleFunc("GET /graphs", s.handleGraphs)
	s.mux.HandleFunc("POST /expenses", s.handleAddExpense)
	s.mux.HandleFunc("POST /expenses/{id}/category", s.handleSetExpenseCategory)
	s.mux.HandleFunc("POST /expenses/{id}/subcategory", s.handleSetExpenseSubcategory)
	s.mux.HandleFunc("POST /expenses/{id}/delete", s.handleDeleteExpense)
	s.mux.HandleFunc("POST /expenses/month/{month}/delete", s.handleDeleteMonth)
	s.mux.HandleFunc("POST /rules", s.handleAddRule)
	s.mux.HandleFunc("POST /rules/{id}", s.handleUpdateRule)
	s.mux.HandleFunc("POST /rules/{id}/delete", s.handleDeleteRule)
	s.mux.HandleFunc("POST /expenses/import", s.handleImportExpenses)
	s.mux.HandleFunc("GET /retire", s.handleRetire)
	s.mux.HandleFunc("GET /records", s.handleRecords)
}

// dict builds a map so a template can be called with several named values.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("dict needs an even number of arguments")
	}
	out := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict key %v is not a string", pairs[i])
		}
		out[key] = pairs[i+1]
	}
	return out, nil
}

type dashboardData struct {
	Base         string
	Currencies   []string
	AssetClasses []string
	Now          store.Valuation
	Owners       []ownerTotal
	Allocation   store.Allocation
	Liquidity    store.Liquidity
	NetWorthEUR  money.Amount
	NetWorthUSD  money.Amount
	HasEUR       bool
	HasUSD       bool
	Change       money.Amount
	HasChange    bool
	Accounts     []store.Account
	Funds        []store.Fund
	Entries      []store.Entry
	FX           fxView
	MissingRates []string
	Chart        Chart
	InvestChart  Chart
	Today        string
	Error        string
}

type ownerTotal struct {
	Name  string
	Total money.Amount
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	v, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	ledger := v.ledger

	history := ledger.History()
	investHistory := ledger.InvestHistory()
	reset, err := s.store.DashboardChangeReset(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	now := ledger.At("")
	data := dashboardData{
		Base:         store.Base,
		Currencies:   store.Currencies,
		AssetClasses: store.AssetClasses,
		Now:          now,
		Owners:       ownerTotals(now),
		Allocation:   now.Allocation(),
		Liquidity:    now.Liquidity(),
		Accounts:     ledger.Accounts,
		Funds:        ledger.Funds,
		Entries:      ledger.Entries(50),
		FX:           v.fx,
		MissingRates: ledger.MissingRates(),
		Chart:        buildChart(historySince(history, reset)),
		InvestChart:  buildInvestChart(investHistorySince(investHistory, reset)),
		Today:        time.Now().Format("2006-01-02"),
		Error:        r.URL.Query().Get("err"),
	}
	data.NetWorthEUR, data.HasEUR = inCurrency(now.NetWorth(), ledger.Rates["EUR"])
	data.NetWorthUSD, data.HasUSD = inCurrency(now.NetWorth(), ledger.Rates["USD"])
	if n := len(history); n >= 2 {
		if history[n-1].Date > reset {
			data.Change = history[n-1].NetWorth() - history[n-2].NetWorth()
			data.HasChange = true
		}
	}

	s.render(w, "dashboard.html", data)
}

func ownerTotals(v store.Valuation) []ownerTotal {
	totals := make(map[string]money.Amount)
	for _, account := range v.Accounts {
		owners := ownerNames(account.Owner)
		value := account.ValueBase
		if account.IsLiability() {
			value = -value
		}
		share, remainder := value/money.Amount(len(owners)), value%money.Amount(len(owners))
		for i, owner := range owners {
			totals[owner] += share
			if money.Amount(i) < remainder {
				totals[owner]++
			} else if money.Amount(-i) > remainder {
				totals[owner]--
			}
		}
	}
	owners := make([]ownerTotal, 0, len(totals))
	for name, total := range totals {
		owners = append(owners, ownerTotal{Name: name, Total: total})
	}
	slices.SortFunc(owners, func(a, b ownerTotal) int { return strings.Compare(a.Name, b.Name) })
	return owners
}

func ownerNames(raw string) []string {
	seen := make(map[string]bool)
	var owners []string
	for _, part := range strings.Split(raw, ",") {
		owner := strings.TrimSpace(part)
		if owner != "" && !seen[owner] {
			owners = append(owners, owner)
			seen[owner] = true
		}
	}
	if len(owners) == 0 {
		return []string{"Unassigned"}
	}
	slices.Sort(owners)
	return owners
}

func normalizeOwners(raw string) string {
	owners := ownerNames(raw)
	if len(owners) == 1 && owners[0] == "Unassigned" {
		return ""
	}
	return strings.Join(owners, ", ")
}

func inCurrency(chf money.Amount, chfPerUnit float64) (money.Amount, bool) {
	if chfPerUnit <= 0 {
		return 0, false
	}
	return money.Amount(math.Round(float64(chf) / chfPerUnit)), true
}

func historySince(points []store.Point, date string) []store.Point {
	if date == "" {
		return points
	}
	for i, point := range points {
		if point.Date >= date {
			return points[i:]
		}
	}
	return nil
}

func investHistorySince(points []store.ValuePoint, date string) []store.ValuePoint {
	if date == "" {
		return points
	}
	for i, point := range points {
		if point.AsOf >= date {
			return points[i:]
		}
	}
	return nil
}

func (s *Server) handleResetDashboardChange(w http.ResponseWriter, r *http.Request) {
	v, err := s.load(r.Context())
	if err != nil {
		s.redirect(w, r, err.Error())
		return
	}
	history := v.ledger.History()
	if len(history) > 0 {
		if err := s.store.SetDashboardChangeReset(r.Context(), history[len(history)-1].Date); err != nil {
			s.redirect(w, r, err.Error())
			return
		}
	}
	s.redirect(w, r, "")
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.redirect(w, r, "name is required")
		return
	}
	class := r.FormValue("class")
	if class == "" {
		class = store.ClassCash
	}
	var openingBalance *money.Amount
	if raw := strings.TrimSpace(r.FormValue("amount")); raw != "" {
		amount, err := money.Parse(raw)
		if err != nil {
			s.redirect(w, r, "opening balance must look like 1234.56")
			return
		}
		openingBalance = &amount
	}
	err := s.store.CreateAccount(r.Context(), name, normalizeOwners(r.FormValue("owner")), r.FormValue("kind"),
		r.FormValue("currency"), class, s.date(r), openingBalance)
	if err != nil {
		s.redirect(w, r, "could not add account: "+err.Error())
		return
	}
	s.redirect(w, r, "")
}

func (s *Server) handleSetAccountOwner(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirect(w, r, "invalid id")
		return
	}
	if err := s.store.SetAccountOwner(r.Context(), id, normalizeOwners(r.FormValue("owner"))); err != nil {
		s.redirect(w, r, err.Error())
		return
	}
	s.redirect(w, r, "")
}

func (s *Server) handleSetAccountClass(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirect(w, r, "invalid id")
		return
	}
	if err := s.store.SetAccountClass(r.Context(), id, r.FormValue("class")); err != nil {
		s.redirect(w, r, err.Error())
		return
	}
	s.redirect(w, r, "")
}

func (s *Server) handleCreateFund(w http.ResponseWriter, r *http.Request) {
	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil {
		s.redirect(w, r, "pick an account to hold the fund")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.redirect(w, r, "fund name is required")
		return
	}
	ticker := strings.ToUpper(strings.TrimSpace(r.FormValue("ticker")))
	class := r.FormValue("class")
	if class == "" {
		class = store.ClassStocks
	}
	var initialPrice *money.Amount
	var units float64
	priceRaw := strings.TrimSpace(r.FormValue("price"))
	unitsRaw := strings.TrimSpace(r.FormValue("units"))
	if priceRaw != "" || unitsRaw != "" {
		if priceRaw == "" || unitsRaw == "" {
			s.redirect(w, r, "initial units and price are both required")
			return
		}
		price, err := money.Parse(priceRaw)
		if err != nil {
			s.redirect(w, r, "price must look like 87.40")
			return
		}
		units, err = parseFloat(unitsRaw)
		if err != nil {
			s.redirect(w, r, "units must be a number, e.g. 12.5")
			return
		}
		initialPrice = &price
	}

	if err := s.store.CreateFund(r.Context(), accountID, name, ticker, r.FormValue("currency"), class,
		s.date(r), units, initialPrice); err != nil {
		s.redirect(w, r, "could not add fund: "+err.Error())
		return
	}
	s.redirect(w, r, "")
}

func (s *Server) handleSetFundClass(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirect(w, r, "invalid id")
		return
	}
	if err := s.store.SetFundClass(r.Context(), id, r.FormValue("class")); err != nil {
		s.redirect(w, r, err.Error())
		return
	}
	s.redirect(w, r, "")
}

func (s *Server) handleSetBalance(w http.ResponseWriter, r *http.Request) {
	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil {
		s.redirect(w, r, "pick an account first")
		return
	}
	amount, err := money.Parse(r.FormValue("amount"))
	if err != nil {
		s.redirect(w, r, "amount must look like 1234.56")
		return
	}
	if err := s.store.SetBalance(r.Context(), accountID, s.date(r), amount); err != nil {
		s.redirect(w, r, err.Error())
		return
	}
	s.redirect(w, r, "")
}

// handleFundUpdate records a new price for a fund and, when units are given,
// the trade that went with it.
func (s *Server) handleFundUpdate(w http.ResponseWriter, r *http.Request) {
	fundID, err := strconv.ParseInt(r.FormValue("fund_id"), 10, 64)
	if err != nil {
		s.redirect(w, r, "pick a fund first")
		return
	}
	price, err := money.Parse(r.FormValue("price"))
	if err != nil {
		s.redirect(w, r, "price must look like 87.40")
		return
	}

	raw := strings.TrimSpace(r.FormValue("units"))
	if raw == "" {
		if err := s.store.SetPrice(r.Context(), fundID, s.date(r), price); err != nil {
			s.redirect(w, r, err.Error())
			return
		}
		s.redirect(w, r, "")
		return
	}

	units, err := parseFloat(raw)
	if err != nil {
		s.redirect(w, r, "units must be a number, e.g. 12.5 or -4 to sell")
		return
	}
	if err := s.store.AddTrade(r.Context(), fundID, s.date(r), units, price); err != nil {
		s.redirect(w, r, err.Error())
		return
	}
	s.redirect(w, r, "")
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	s.withID(w, r, s.store.DeleteAccount)
}

// handleSetAccountCurrency relabels an account's currency; it does not convert
// the balance already stored, only how it is read from now on.
func (s *Server) handleSetAccountCurrency(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirect(w, r, "invalid id")
		return
	}
	if err := s.store.SetAccountCurrency(r.Context(), id, r.FormValue("currency")); err != nil {
		s.redirect(w, r, err.Error())
		return
	}
	s.redirect(w, r, "")
}

func (s *Server) handleDeleteFund(w http.ResponseWriter, r *http.Request) {
	s.withID(w, r, s.store.DeleteFund)
}

func (s *Server) handleDeleteBalance(w http.ResponseWriter, r *http.Request) {
	s.withID(w, r, s.store.DeleteBalance)
}

func (s *Server) handleDeleteTrade(w http.ResponseWriter, r *http.Request) {
	s.withID(w, r, s.store.DeleteTrade)
}

func (s *Server) handleDeletePrice(w http.ResponseWriter, r *http.Request) {
	s.withID(w, r, s.store.DeletePrice)
}

func (s *Server) withID(w http.ResponseWriter, r *http.Request, fn func(context.Context, int64) error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirect(w, r, "invalid id")
		return
	}
	if err := fn(r.Context(), id); err != nil {
		s.redirect(w, r, err.Error())
		return
	}
	s.redirect(w, r, "")
}

func (s *Server) date(r *http.Request) string {
	if d := r.FormValue("as_of"); d != "" {
		return d
	}
	return time.Now().Format("2006-01-02")
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 64)
}

// redirect sends the browser back to the dashboard, optionally with a message.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, errMsg string) {
	s.redirectTo(w, r, "/", errMsg)
}

func (s *Server) redirectTo(w http.ResponseWriter, r *http.Request, target, errMsg string) {
	if errMsg != "" {
		target += "?err=" + url.QueryEscape(errMsg)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// noticeTo redirects with something that went right.
func (s *Server) noticeTo(w http.ResponseWriter, r *http.Request, target, msg string) {
	http.Redirect(w, r, target+"?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render template", "template", name, "error", err)
	}
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
