package web

import (
	"net/http"
	"time"

	"github.com/MrShanks/networth/internal/store"
)

type portfolioData struct {
	Base         string
	Currencies   []string
	AssetClasses []string
	Now          store.Valuation
	Accounts     []store.Account
	InvestChart  Chart
	Today        string
	Notice       string
	Error        string
}

func (s *Server) handlePortfolio(w http.ResponseWriter, r *http.Request) {
	v, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	s.render(w, "portfolio.html", portfolioData{
		Base:         store.Base,
		Currencies:   store.Currencies,
		AssetClasses: store.AssetClasses,
		Now:          v.ledger.At(""),
		Accounts:     v.ledger.Accounts,
		InvestChart:  buildInvestChart(v.ledger.InvestHistory()),
		Today:        time.Now().Format("2006-01-02"),
		Notice:       r.URL.Query().Get("msg"),
		Error:        r.URL.Query().Get("err"),
	})
}
