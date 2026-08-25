package store

import (
	"time"

	"github.com/MrShanks/networth/internal/money"
)

// MonthGain is how much the funds moved in a calendar month, with new money
// paid in taken out so only market movement is left.
type MonthGain struct {
	Month    string
	Value    money.Amount // market value at the end of the month
	Invested money.Amount
	Gain     money.Amount
}

// MonthlyGains values the portfolio at the end of every month that has data.
func (l *Ledger) MonthlyGains() []MonthGain {
	dates := l.dates()
	if len(dates) == 0 {
		return nil
	}

	var (
		out                     []MonthGain
		prevValue, prevInvested money.Amount
	)
	for _, month := range monthsBetween(dates[0][:7], dates[len(dates)-1][:7]) {
		v := l.At(monthEnd(month))
		out = append(out, MonthGain{
			Month:    month,
			Value:    v.MarketBase,
			Invested: v.InvestedBase,
			Gain:     (v.MarketBase - prevValue) - (v.InvestedBase - prevInvested),
		})
		prevValue, prevInvested = v.MarketBase, v.InvestedBase
	}
	return out
}

// monthsBetween lists every YYYY-MM from first to last, inclusive.
func monthsBetween(first, last string) []string {
	start, err := time.Parse("2006-01", first)
	if err != nil {
		return nil
	}
	end, err := time.Parse("2006-01", last)
	if err != nil {
		return nil
	}

	var out []string
	for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
		out = append(out, m.Format("2006-01"))
	}
	return out
}

// monthEnd is the last day of the given month, as YYYY-MM-DD.
func monthEnd(month string) string {
	m, err := time.Parse("2006-01", month)
	if err != nil {
		return month
	}
	return m.AddDate(0, 1, -1).Format("2006-01-02")
}
