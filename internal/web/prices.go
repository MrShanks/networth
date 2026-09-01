package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/prices"
	"github.com/MrShanks/networth/internal/store"
)

// handleFetchPrices fills in a fund's price history from the day it was first
// bought, so its value is drawn day by day rather than jumping between trades.
func (s *Server) handleFetchPrices(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirect(w, r, "invalid id")
		return
	}
	fund, err := s.store.Fund(r.Context(), id)
	if err != nil {
		s.redirect(w, r, "could not read the fund: "+err.Error())
		return
	}

	symbol := fund.Symbol
	if symbol == "" {
		query := fund.Ticker
		if query == "" {
			query = fund.Name
		}
		symbol, err = s.prices.Symbol(r.Context(), query, fund.Currency)
		if errors.Is(err, prices.ErrNoListing) {
			s.redirect(w, r, fmt.Sprintf("no %s listing found for %s", fund.Currency, fund.Name))
			return
		}
		if err != nil {
			s.redirect(w, r, "could not look up "+fund.Name+": "+err.Error())
			return
		}
	}

	from, err := s.store.FirstTradeDate(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if from == "" {
		from = time.Now().AddDate(-5, 0, 0).Format("2006-01-02")
	}

	history, currency, err := s.prices.History(r.Context(), symbol, from)
	if err != nil {
		s.redirect(w, r, "could not fetch prices for "+fund.Name+": "+err.Error())
		return
	}
	if !strings.EqualFold(currency, fund.Currency) {
		s.redirect(w, r, fmt.Sprintf("%s quotes %s in %s, not %s", symbol, fund.Name, currency, fund.Currency))
		return
	}

	points := make([]store.PricePoint, 0, len(history))
	for _, p := range history {
		points = append(points, store.PricePoint{AsOf: p.AsOf, Price: p.Close})
	}
	added, err := s.store.ImportPrices(r.Context(), id, points)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.store.SetFundSymbol(r.Context(), id, symbol); err != nil {
		s.fail(w, err)
		return
	}

	s.noticeTo(w, r, s.origin(r), fmt.Sprintf("%s: %d daily price%s from %s since %s.",
		fund.Name, added, plural(added, "", "s"), symbol, from))
}
