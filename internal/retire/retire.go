// Package retire projects how long it takes to build a portfolio that can cover
// your expenses indefinitely.
package retire

import (
	"math"
	"time"

	"github.com/MrShanks/networth/internal/money"
)

const (
	maxMonths = 70 * 12
	// tailMonths keeps the projection going past the target so the chart shows
	// where the lines cross instead of stopping on it.
	tailMonths = 60
)

type Params struct {
	NetWorth        money.Amount
	MonthlyExpenses money.Amount
	MonthlySavings  money.Amount
	ReturnPct       float64 // expected annual return, before inflation
	InflationPct    float64 // expected annual inflation
	WithdrawalPct   float64 // share of the portfolio spent per year in retirement
	Start           time.Time
}

// RealReturnPct is the growth left once inflation is taken out. Everything is
// projected with it, so the figures stay in today's money.
func (p Params) RealReturnPct() float64 {
	return realReturn(p.ReturnPct, p.InflationPct)
}

func realReturn(returnPct, inflationPct float64) float64 {
	return ((1+returnPct/100)/(1+inflationPct/100) - 1) * 100
}

// monthlyRate compounds an annual percentage down to a monthly one.
func monthlyRate(annualPct float64) float64 {
	return math.Pow(1+annualPct/100, 1.0/12) - 1
}

// Point is the projected portfolio value at a point in time.
type Point struct {
	Date  string
	Value money.Amount
}

type Plan struct {
	Params
	Target    money.Amount // portfolio needed to live off the withdrawal rate
	Months    int
	Reachable bool
	Date      string // when the target is reached
	Points    []Point
}

// Years and RemainingMonths split the wait into something readable.
func (p Plan) Years() int                   { return p.Months / 12 }
func (p Plan) RemainingMonths() int         { return p.Months % 12 }
func (p Plan) AnnualExpenses() money.Amount { return p.MonthlyExpenses * 12 }

// Progress is how far the current net worth gets you, in percent.
func (p Plan) Progress() float64 {
	if p.Target <= 0 {
		return 0
	}
	return math.Min(100, float64(p.NetWorth)/float64(p.Target)*100)
}

// Shortfall is what is still missing.
func (p Plan) Shortfall() money.Amount {
	if p.NetWorth >= p.Target {
		return 0
	}
	return p.Target - p.NetWorth
}

// Project compounds the current net worth plus monthly savings until it covers
// the yearly expenses at the chosen withdrawal rate. Amounts are in today's
// money: growth is net of inflation and savings are assumed to keep pace with
// it, so the target does not move.
func Project(p Params) Plan {
	if p.Start.IsZero() {
		p.Start = time.Now()
	}

	plan := Plan{Params: p}
	if p.WithdrawalPct > 0 {
		plan.Target = money.Amount(math.Round(float64(p.MonthlyExpenses) * 12 * 100 / p.WithdrawalPct))
	}

	monthlyReturn := monthlyRate(p.RealReturnPct())
	value := float64(p.NetWorth)
	target := float64(plan.Target)
	hit := func(v float64) bool { return plan.Target > 0 && v >= target }

	plan.Points = []Point{{Date: p.Start.Format("2006-01"), Value: p.NetWorth}}
	reached := -1
	if hit(value) {
		reached = 0
		plan.Reachable, plan.Date = true, plan.Points[0].Date
	}

	for month := 1; month <= maxMonths; month++ {
		value = value*(1+monthlyReturn) + float64(p.MonthlySavings)
		date := p.Start.AddDate(0, month, 0).Format("2006-01")
		plan.Points = append(plan.Points, Point{Date: date, Value: money.Amount(math.Round(value))})

		if reached < 0 && hit(value) {
			reached = month
			plan.Months, plan.Reachable, plan.Date = month, true, date
		}
		if reached >= 0 && month >= reached+tailMonths {
			return plan
		}
	}

	if !plan.Reachable {
		// Out of reach: keep the projection for the chart but report no date.
		plan.Months = maxMonths
	}
	return plan
}
