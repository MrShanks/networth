package web

import (
	"net/http"
	"sort"

	"github.com/MrShanks/networh/internal/money"
	"github.com/MrShanks/networh/internal/store"
)

// ranked is one row of a leaderboard.
type ranked struct {
	Rank   int
	Month  string
	Amount money.Amount
	Detail string
	Share  float64 // bar width relative to the biggest row, in percent
}

type recordsData struct {
	Base        string
	BestGains   []ranked
	WorstGains  []ranked
	TopSpending []ranked
	Error       string
}

const leaderboardSize = 10

func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	ledger, err := s.store.Load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	expenses, err := s.store.Expenses(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	s.rates(r.Context(), ledger)
	gains := ledger.MonthlyGains()
	report := store.BuildExpenseReport(expenses, ledger.Rates)

	best := make([]ranked, 0, len(gains))
	for _, g := range gains {
		if g.Gain == 0 {
			continue // nothing moved, not worth a place in the table
		}
		best = append(best, ranked{
			Month:  g.Month,
			Amount: g.Gain,
			Detail: g.Value.String() + " invested value",
		})
	}
	worst := append([]ranked(nil), best...)

	sort.SliceStable(best, func(i, j int) bool { return best[i].Amount > best[j].Amount })
	sort.SliceStable(worst, func(i, j int) bool { return worst[i].Amount < worst[j].Amount })

	spending := make([]ranked, 0, len(report.Months))
	for _, m := range report.Months {
		biggest := ""
		if len(m.Categories) > 0 {
			biggest = "most on " + m.Categories[0].Category
		}
		spending = append(spending, ranked{Month: m.Month, Amount: m.Total, Detail: biggest})
	}
	sort.SliceStable(spending, func(i, j int) bool { return spending[i].Amount > spending[j].Amount })

	s.render(w, "records.html", recordsData{
		Base:        store.Base,
		BestGains:   leaderboard(best, func(r ranked) bool { return r.Amount > 0 }),
		WorstGains:  leaderboard(worst, func(r ranked) bool { return r.Amount < 0 }),
		TopSpending: leaderboard(spending, func(r ranked) bool { return r.Amount > 0 }),
		Error:       r.URL.Query().Get("err"),
	})
}

// leaderboard keeps the top rows that pass keep, numbers them and scales the bars.
func leaderboard(rows []ranked, keep func(ranked) bool) []ranked {
	out := make([]ranked, 0, leaderboardSize)
	for _, row := range rows {
		if !keep(row) {
			continue
		}
		row.Rank = len(out) + 1
		out = append(out, row)
		if len(out) == leaderboardSize {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}

	top := out[0].Amount.Float()
	if top < 0 {
		top = -top
	}
	for i := range out {
		v := out[i].Amount.Float()
		if v < 0 {
			v = -v
		}
		if top > 0 {
			out[i].Share = v / top * 100
		}
	}
	return out
}
