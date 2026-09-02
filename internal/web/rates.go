package web

import (
	"context"

	"github.com/MrShanks/networth/internal/store"
)

// view is everything the pages start from: the ledger valued at the current
// exchange rates, and the expenses grouped by month.
type view struct {
	ledger *store.Ledger
	report store.ExpenseReport
}

// load reads the database and applies the current exchange rates to it.
func (s *Server) load(ctx context.Context) (view, error) {
	ledger, err := s.store.Load(ctx)
	if err != nil {
		return view{}, err
	}
	expenses, err := s.store.Expenses(ctx)
	if err != nil {
		return view{}, err
	}

	s.applyRates(ctx, ledger)
	return view{ledger: ledger, report: store.BuildExpenseReport(expenses, ledger.Rates)}, nil
}

// rates fetches the current rates, falling back to whatever the ledger already
// holds when the exchange rate service cannot be reached.
func (s *Server) applyRates(ctx context.Context, l *store.Ledger) {
	quotes, err := s.fx.Rates(ctx)
	switch {
	case err != nil && quotes == nil:
	case err != nil:
		l.UseRates(quotes.PerUnit, quotes.AsOf, false)
	default:
		l.UseRates(quotes.PerUnit, quotes.AsOf, true)
		if err := s.store.SaveRates(ctx, quotes.AsOf, quotes.PerUnit); err != nil {
			s.log.Error("cache exchange rates", "error", err)
		}
	}
}
