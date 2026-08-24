package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/MrShanks/networh/internal/store"
)

// Pair is a quoted currency pair, e.g. USD/CHF 0.7995.
type Pair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type fxView struct {
	Pairs   []Pair `json:"pairs"`
	AsOf    string `json:"asOf"`
	Live    bool   `json:"live"`
	Checked string `json:"checked"`
	Note    string `json:"note,omitempty"`
}

// rates fetches the current rates, falling back to whatever the ledger already
// holds when the exchange rate service cannot be reached.
func (s *Server) rates(ctx context.Context, l *store.Ledger) fxView {
	view := fxView{Checked: time.Now().Format("15:04:05")}

	quotes, err := s.fx.Rates(ctx)
	switch {
	case err != nil && quotes == nil:
		view.Note = "live rates unavailable, using the last saved ones"
	case err != nil:
		view.Note = "live rates unavailable, showing the last fetch"
		l.UseRates(quotes.PerUnit, quotes.AsOf, false)
	default:
		l.UseRates(quotes.PerUnit, quotes.AsOf, true)
		if err := s.store.SaveRates(ctx, quotes.AsOf, quotes.PerUnit); err != nil {
			s.log.Error("cache exchange rates", "error", err)
		}
	}

	view.AsOf, view.Live = l.RatesAsOf, l.RatesLive
	view.Pairs = pairs(l.Rates)
	return view
}

// pairs quotes the three crosses the dashboard shows, from CHF-per-unit rates.
func pairs(perUnit map[string]float64) []Pair {
	usd, eur := perUnit["USD"], perUnit["EUR"]
	if usd <= 0 || eur <= 0 {
		return nil
	}
	return []Pair{
		{Name: "USD/CHF", Value: fmt.Sprintf("%.4f", usd)},
		{Name: "EUR/USD", Value: fmt.Sprintf("%.4f", eur/usd)},
		{Name: "CHF/EUR", Value: fmt.Sprintf("%.4f", 1/eur)},
	}
}

func (s *Server) handleRatesAPI(w http.ResponseWriter, r *http.Request) {
	ledger, err := s.store.Load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.rates(r.Context(), ledger)); err != nil {
		s.log.Error("encode rates", "error", err)
	}
}
