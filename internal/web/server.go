// Package web serves the net worth tracker UI.
package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"syscall"
	"time"

	"github.com/MrShanks/networth/internal/fx"
	"github.com/MrShanks/networth/internal/money"
	"github.com/MrShanks/networth/internal/prices"
	"github.com/MrShanks/networth/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	store  *store.Store
	fx     *fx.Client
	prices *prices.Client
	tmpl   *template.Template
	log    *slog.Logger
	mux    *http.ServeMux
}

func absAmount(amount money.Amount) money.Amount {
	if amount < 0 {
		return -amount
	}
	return amount
}

func subAmount(a, b money.Amount) money.Amount { return a - b }

func NewServer(s *store.Store, rates *fx.Client, priceClient *prices.Client, log *slog.Logger) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"spark":      sparkPoints,
		"trend":      sparkAmounts,
		"monthName":  monthName,
		"periodName": periodName,
		"absAmount":  absAmount,
		"subAmount":  subAmount,
		"dict":       dict,
		"classLabel": store.ClassLabel,
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}

	srv := &Server{store: s, fx: rates, prices: priceClient, tmpl: tmpl, log: log, mux: http.NewServeMux()}
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
	static := http.FileServer(http.FS(assets))
	s.mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
	s.mux.HandleFunc("GET /{$}", s.handleWorkspace)
	s.mux.HandleFunc("POST /accounts", s.handleCreateWorkspaceAccount)
	s.mux.HandleFunc("POST /accounts/{id}", s.handleSetWorkspaceAccountDetails)
	s.mux.HandleFunc("POST /balances", s.handleSetWorkspaceBalance)
	s.mux.HandleFunc("POST /accounts/{id}/balances", s.handleSetWorkspaceBalance)
	s.mux.HandleFunc("POST /balances/import", s.handleImportWorkspaceBalances)
	s.mux.HandleFunc("GET /investments", s.handleInvestments)
	s.mux.HandleFunc("POST /investments/import", s.handleImportInvestments)
	s.mux.HandleFunc("POST /investments/trades", s.handleAddInvestmentTrade)
	s.mux.HandleFunc("POST /investments/prices", s.handleRefreshInvestmentPrices)
	s.mux.HandleFunc("GET /expenses", s.handleExpenses)
	s.mux.HandleFunc("GET /expenses/year", s.handleYearExpenses)
	s.mux.HandleFunc("GET /expenses/all", s.handleAllExpenses)
	s.mux.HandleFunc("GET /transactions", s.handleTransactions)
	s.mux.HandleFunc("GET /graphs", s.handleGraphs)
	s.mux.HandleFunc("POST /expenses", s.handleAddExpense)
	s.mux.HandleFunc("POST /expenses/{id}/category", s.handleSetExpenseCategory)
	s.mux.HandleFunc("POST /expenses/{id}/subcategory", s.handleSetExpenseSubcategory)
	s.mux.HandleFunc("POST /expenses/{id}/delete", s.handleDeleteExpense)
	s.mux.HandleFunc("POST /expenses/month/{month}/delete", s.handleDeleteMonth)
	s.mux.HandleFunc("POST /expenses/delete-all", s.handleDeleteAllTransactions)
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

func (s *Server) date(r *http.Request) string {
	if date := r.FormValue("as_of"); date != "" {
		return date
	}
	return time.Now().Format("2006-01-02")
}

// redirect sends the browser back to the page the form was submitted from,
// optionally with a message.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, errMsg string) {
	s.redirectTo(w, r, s.origin(r), errMsg)
}

// origin is the page a form was posted from, so acting on a fund does not
// bounce the reader back to the page the form came from.
func (s *Server) origin(r *http.Request) string {
	referer, err := url.Parse(r.Referer())
	if err != nil || referer.Path == "" || (referer.Host != "" && referer.Host != r.Host) {
		return "/"
	}
	return referer.Path
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
		if isClientDisconnect(err) {
			return
		}
		s.log.Error("render template", "template", name, "error", err)
	}
}

func isClientDisconnect(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
