package web

import (
	"net/http"
	"sort"
	"strings"

	"github.com/MrShanks/networth/internal/money"
	"github.com/MrShanks/networth/internal/store"
)

type graphsData struct {
	Base        string
	Salary      Chart
	Income      Chart
	YearlyTaxes BarChart
	Error       string
}

func (s *Server) handleGraphs(w http.ResponseWriter, r *http.Request) {
	v, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	s.render(w, "graphs.html", graphsData{
		Base:        store.Base,
		Salary:      buildSalaryChart(v.report.Months),
		Income:      buildIncomeChart(v.report.Months),
		YearlyTaxes: buildYearlyTaxBars(v.report.Months),
		Error:       r.URL.Query().Get("err"),
	})
}

func buildSalaryChart(months []store.ExpenseMonth) Chart {
	return buildMonthlyAmountChart(months, func(month store.ExpenseMonth) money.Amount { return month.Salary })
}

func buildIncomeChart(months []store.ExpenseMonth) Chart {
	return buildMonthlyAmountChart(months, func(month store.ExpenseMonth) money.Amount { return month.Income })
}

func buildMonthlyAmountChart(months []store.ExpenseMonth, amount func(store.ExpenseMonth) money.Amount) Chart {
	points := make([]datedValue, 0, len(months))
	for _, month := range months {
		value := amount(month)
		if value == 0 {
			continue
		}
		points = append(points, datedValue{
			date:  month.Month,
			value: value.Float(),
			label: value.String(),
		})
	}
	return plot(points)
}

func buildYearlyTaxBars(months []store.ExpenseMonth) BarChart {
	byYear := map[string]money.Amount{}
	for _, month := range months {
		if len(month.Month) >= 4 && month.Taxes != 0 {
			byYear[month.Month[:4]] += month.Taxes
		}
	}

	years := make([]string, 0, len(byYear))
	for year := range byYear {
		years = append(years, year)
	}
	sort.Strings(years)
	return buildAmountBars(years, func(year string) money.Amount { return byYear[year] })
}

func buildAmountBars(labels []string, amount func(string) money.Amount) BarChart {
	chart := BarChart{Width: chartWidth, Height: chartHeight, Empty: len(labels) == 0}
	if chart.Empty {
		return chart
	}

	top := 0.0
	for _, label := range labels {
		top = max(top, amount(label).Float())
	}
	if top <= 0 {
		top = 1
	}

	plotWidth := float64(chartWidth - 2*chartPadX)
	plotHeight := float64(chartHeight - 2*chartPadY)
	slot := plotWidth / float64(len(labels))
	width := min(64, slot*0.6)
	for i, label := range labels {
		value := amount(label)
		height := plotHeight * max(0, value.Float()) / top
		chart.Bars = append(chart.Bars, Bar{
			X:     chartPadX + slot*float64(i) + (slot-width)/2,
			Y:     chartPadY + plotHeight - height,
			W:     width,
			H:     height,
			Label: label,
			Title: strings.TrimSpace(label + ": " + value.String()),
		})
	}
	for i := 0; i <= 4; i++ {
		chart.YTicks = append(chart.YTicks, chartLabel{
			X: chartPadX, Y: chartPadY + plotHeight*(1-float64(i)/4),
			Text: shortNumber(top * float64(i) / 4),
		})
	}
	return chart
}
