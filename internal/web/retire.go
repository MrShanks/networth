package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/MrShanks/networth/internal/money"
	"github.com/MrShanks/networth/internal/retire"
	"github.com/MrShanks/networth/internal/store"
)

type retireData struct {
	Base        string
	Plan        retire.Plan
	Chart       Chart
	Runway      retire.Runway
	BurnChart   Chart
	Passion     money.Amount
	WorkMonths  int
	AvgMonths   int
	SavedMonths int
	HasExpense  bool
	Error       string
}

func (s *Server) handleRetire(w http.ResponseWriter, r *http.Request) {
	v, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	report := v.report

	q := r.URL.Query()
	const window = 12
	params := retire.Params{
		NetWorth:        v.ledger.At("").NetWorth(),
		MonthlyExpenses: report.RecentAverage(window),
		MonthlySavings:  amountParam(q.Get("savings"), report.RecentSaved(window)),
		ReturnPct:       floatParam(q.Get("return"), 7),
		InflationPct:    floatParam(q.Get("inflation"), 2),
		WithdrawalPct:   floatParam(q.Get("withdrawal"), 4),
		Start:           time.Now(),
	}

	plan := retire.Project(params)

	passion := amountParam(q.Get("passion"), params.MonthlyExpenses/2)
	workMonths := intParam(q.Get("work"), 12)
	runway := retire.Burn(retire.RunwayParams{
		NetWorth:        params.NetWorth,
		MonthlyExpenses: params.MonthlyExpenses,
		MonthlySavings:  params.MonthlySavings,
		PassionIncome:   passion,
		WorkMonths:      workMonths,
		ReturnPct:       params.ReturnPct,
		InflationPct:    params.InflationPct,
		Start:           params.Start,
	})

	s.render(w, "retire.html", retireData{
		Base:        store.Base,
		Plan:        plan,
		Chart:       buildPlanChart(plan),
		Runway:      runway,
		BurnChart:   buildBurnChart(runway),
		Passion:     passion,
		WorkMonths:  workMonths,
		AvgMonths:   report.RecentMonths(window),
		SavedMonths: report.RecentSavedMonths(window),
		HasExpense:  len(report.Months) > 0,
		Error:       q.Get("err"),
	})
}

// buildBurnChart draws every scenario's portfolio down to zero.
func buildBurnChart(run retire.Runway) Chart {
	if len(run.Scenarios) == 0 || len(run.Dates) < 2 {
		return Chart{Empty: true}
	}
	step := max(1, len(run.Dates)/120)

	var dated []datedValue
	for i := 0; i < len(run.Dates); i += step {
		dated = append(dated, datedValue{
			date:  run.Dates[i],
			value: run.Scenarios[0].Values[i].Float(),
			label: run.Scenarios[0].Values[i].String() + " if you stop now",
		})
	}

	extra := make([]series, 0, len(run.Scenarios)-1)
	for _, s := range run.Scenarios[1:] {
		values := make([]float64, 0, len(dated))
		for i := 0; i < len(run.Dates); i += step {
			values = append(values, s.Values[i].Float())
		}
		extra = append(extra, series{class: s.Class, values: values})
	}
	return plot(dated, extra...)
}

// buildPlanChart draws the projected portfolio against the target it must reach.
func buildPlanChart(plan retire.Plan) Chart {
	points := plan.Points
	// Keep the SVG small on long projections.
	step := max(1, len(points)/120)

	dated := make([]datedValue, 0, len(points)/step+1)
	target := make([]float64, 0, len(points)/step+1)
	for i := 0; i < len(points); i += step {
		dated = append(dated, datedValue{
			date:  points[i].Date,
			value: points[i].Value.Float(),
			label: points[i].Value.String(),
		})
		target = append(target, plan.Target.Float())
	}
	return plot(dated, series{class: "invested", values: target})
}

func amountParam(raw string, fallback money.Amount) money.Amount {
	if raw == "" {
		return fallback
	}
	amount, err := money.Parse(raw)
	if err != nil || amount < 0 {
		return fallback
	}
	return amount
}

func floatParam(raw string, fallback float64) float64 {
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 100 {
		return fallback
	}
	return v
}

func intParam(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 || v > 600 {
		return fallback
	}
	return v
}
