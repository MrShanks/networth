// Package web serves the net worth tracker UI.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MrShanks/networh/internal/fx"
	"github.com/MrShanks/networh/internal/money"
	"github.com/MrShanks/networh/internal/store"
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

func NewServer(s *store.Store, rates *fx.Client, log *slog.Logger) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"spark":     sparkPoints,
		"monthName": monthName,
		"dict":      dict,
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}

	srv := &Server{store: s, fx: rates, tmpl: tmpl, log: log, mux: http.NewServeMux()}
	srv.routes()
	return srv, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	s.mux.Handle("GET /static/", http.FileServer(http.FS(assets)))
	s.mux.HandleFunc("GET /{$}", s.handleDashboard)
	s.mux.HandleFunc("POST /accounts", s.handleCreateAccount)
	s.mux.HandleFunc("POST /accounts/{id}/delete", s.handleDeleteAccount)
	s.mux.HandleFunc("POST /funds", s.handleCreateFund)
	s.mux.HandleFunc("POST /funds/{id}/delete", s.handleDeleteFund)
	s.mux.HandleFunc("POST /balances", s.handleSetBalance)
	s.mux.HandleFunc("POST /balances/{id}/delete", s.handleDeleteBalance)
	s.mux.HandleFunc("POST /fund-updates", s.handleFundUpdate)
	s.mux.HandleFunc("POST /trades/{id}/delete", s.handleDeleteTrade)
	s.mux.HandleFunc("POST /prices/{id}/delete", s.handleDeletePrice)
	s.mux.HandleFunc("GET /api/rates", s.handleRatesAPI)
	s.mux.HandleFunc("GET /expenses", s.handleExpenses)
	s.mux.HandleFunc("POST /expenses", s.handleAddExpense)
	s.mux.HandleFunc("POST /expenses/{id}/delete", s.handleDeleteExpense)
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
	Now          store.Valuation
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

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ledger, err := s.store.Load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	fxView := s.rates(r.Context(), ledger)
	history := ledger.History()
	data := dashboardData{
		Base:         store.Base,
		Currencies:   store.Currencies,
		Now:          ledger.At(""),
		Accounts:     ledger.Accounts,
		Funds:        ledger.Funds,
		Entries:      ledger.Entries(50),
		FX:           fxView,
		MissingRates: ledger.MissingRates(),
		Chart:        buildChart(history),
		InvestChart:  buildInvestChart(ledger.InvestHistory()),
		Today:        time.Now().Format("2006-01-02"),
		Error:        r.URL.Query().Get("err"),
	}
	if n := len(history); n >= 2 {
		data.Change = history[n-1].NetWorth() - history[n-2].NetWorth()
		data.HasChange = true
	}

	s.render(w, "dashboard.html", data)
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.redirect(w, r, "name is required")
		return
	}
	err := s.store.CreateAccount(r.Context(), name, r.FormValue("kind"), r.FormValue("currency"))
	if err != nil {
		s.redirect(w, r, "could not add account: "+err.Error())
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

	if err := s.store.CreateFund(r.Context(), accountID, name, ticker, r.FormValue("currency")); err != nil {
		s.redirect(w, r, "could not add fund: "+err.Error())
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
