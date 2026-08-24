package retire

import (
	"math"
	"time"

	"github.com/MrShanks/networh/internal/money"
)

// Scenario is one way of drawing down the portfolio.
type Scenario struct {
	Name    string
	Class   string // css class for its line
	Note    string
	Months  int  // months until the money runs out
	Forever bool // the portfolio grows faster than it is spent
	Values  []money.Amount
}

func (s Scenario) Years() int           { return s.Months / 12 }
func (s Scenario) RemainingMonths() int { return s.Months % 12 }

// Runway is how long the money lasts under each scenario.
type Runway struct {
	Start     time.Time
	Scenarios []Scenario
	Dates     []string // one per projected month, shared by every scenario
}

type RunwayParams struct {
	NetWorth        money.Amount
	MonthlyExpenses money.Amount
	MonthlySavings  money.Amount
	PassionIncome   money.Amount
	WorkMonths      int // how much longer you keep working in the second scenario
	ReturnPct       float64
	InflationPct    float64
	Start           time.Time
}

func (p RunwayParams) real() float64 {
	return ((1+p.ReturnPct/100)/(1+p.InflationPct/100) - 1) * 100
}

// Burn projects the portfolio down to zero under three scenarios, in today's
// money. A month's flow is what lands in the account: savings while working,
// otherwise expenses less whatever you still earn.
func Burn(p RunwayParams) Runway {
	if p.Start.IsZero() {
		p.Start = time.Now()
	}

	monthly := math.Pow(1+p.real()/100, 1.0/12) - 1
	expenses := float64(p.MonthlyExpenses)

	scenarios := []Scenario{
		{
			Name:  "Stop working now",
			Class: "stop",
			Note:  "living off the portfolio from today",
		},
		{
			Name:  "Work one more year",
			Class: "work",
			Note:  "saving as you do now, then stopping",
		},
		{
			Name:  "Passion project",
			Class: "passion",
			Note:  "stopping now, but still earning a little",
		},
	}
	flows := []func(month int) float64{
		func(int) float64 { return -expenses },
		func(month int) float64 {
			if month < p.WorkMonths {
				return float64(p.MonthlySavings)
			}
			return -expenses
		},
		func(int) float64 { return float64(p.PassionIncome) - expenses },
	}

	horizon := 0
	for i := range scenarios {
		scenarios[i] = burn(scenarios[i], p.NetWorth, monthly, flows[i])
		horizon = max(horizon, len(scenarios[i].Values))
	}
	// Give the chart a little room past the last scenario to run dry.
	horizon = min(horizon+6, maxMonths+1)

	run := Runway{Start: p.Start}
	for m := 0; m < horizon; m++ {
		run.Dates = append(run.Dates, p.Start.AddDate(0, m, 0).Format("2006-01"))
	}
	for _, s := range scenarios {
		for len(s.Values) < horizon {
			s.Values = append(s.Values, 0) // broke, and staying that way
		}
		s.Values = s.Values[:horizon]
		run.Scenarios = append(run.Scenarios, s)
	}
	return run
}

func burn(s Scenario, start money.Amount, monthlyReturn float64, flow func(int) float64) Scenario {
	value := float64(start)
	s.Values = []money.Amount{start}

	for month := 0; month < maxMonths; month++ {
		value = value*(1+monthlyReturn) + flow(month)
		if value <= 0 {
			s.Months = month + 1
			s.Values = append(s.Values, 0)
			return s
		}
		s.Values = append(s.Values, money.Amount(math.Round(value)))
	}

	s.Months, s.Forever = maxMonths, true
	return s
}
