package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/MrShanks/networth/internal/money"
	"github.com/MrShanks/networth/internal/store"
)

type graphsData struct {
	Base                string
	Salary              Chart
	SalaryBySubcategory MultiLineChart
	Income              Chart
	NetWorth            Chart
	Investments         Chart
	CashFlow            GroupedBarChart
	SavingsRate         Chart
	TaxRate             Chart
	AllocationHistory   StackedChart
	ExpenseHistory      StackedChart
	YearlyTaxes         BarChart
	Error               string
}

func (s *Server) handleGraphs(w http.ResponseWriter, r *http.Request) {
	v, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	s.render(w, "graphs.html", graphsData{
		Base:                store.Base,
		Salary:              buildSalaryChart(v.report.Months),
		SalaryBySubcategory: buildSalaryBySubcategory(v.report.Months),
		Income:              buildIncomeChart(v.report.Months),
		NetWorth:            buildChart(v.ledger.NetWorthHistory()),
		Investments:         buildInvestChart(v.ledger.InvestHistory()),
		CashFlow:            buildCashFlowBars(v.report.Months),
		SavingsRate:         buildSavingsRateChart(v.report.Months),
		TaxRate:             buildTaxRateChart(v.report.Months),
		AllocationHistory:   buildAllocationHistory(v.ledger),
		ExpenseHistory:      buildExpenseHistory(v.report.Months),
		YearlyTaxes:         buildYearlyTaxBars(v.report.Months),
		Error:               r.URL.Query().Get("err"),
	})
}

type GroupedBar struct {
	X, W, IncomeY, IncomeH, SpendingY, SpendingH float64
	Label, IncomeTitle, SpendingTitle            string
}

type GroupedBarChart struct {
	Width, Height float64
	Bars          []GroupedBar
	YTicks        []chartLabel
	XTicks        []chartLabel
	Empty         bool
}

type StackedBand struct {
	Class, Label, Points string
}

type StackedChart struct {
	Width, Height float64
	Bands         []StackedBand
	YTicks        []chartLabel
	XTicks        []chartLabel
	Empty         bool
}

type MultiLineSeries struct {
	Class, Label, Points string
	Dots                 []chartLabel
}

type MultiLineChart struct {
	Width, Height float64
	Series        []MultiLineSeries
	YTicks        []chartLabel
	XTicks        []chartLabel
	Empty         bool
}

func buildSalaryChart(months []store.ExpenseMonth) Chart {
	return buildMonthlyAmountChart(months, func(month store.ExpenseMonth) money.Amount { return month.Salary })
}

func buildSalaryBySubcategory(months []store.ExpenseMonth) MultiLineChart {
	totals := map[string]money.Amount{}
	for _, month := range months {
		for _, subcategory := range month.IncomeSubcategories {
			if salaryCategory(subcategory.Category) {
				totals[subcategory.Subcategory] += subcategory.Total
			}
		}
	}
	labels := make([]string, 0, len(totals))
	for label := range totals {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool { return totals[labels[i]] > totals[labels[j]] })
	if len(labels) > 7 {
		labels = labels[:7]
	}
	values := make([][]float64, len(labels))
	indexes := make(map[string]int, len(labels))
	for i, label := range labels {
		indexes[label] = i
	}
	dates := make([]string, len(months))
	for monthIndex, month := range months {
		dates[monthIndex] = month.Month
		for _, subcategory := range month.IncomeSubcategories {
			seriesIndex, ok := indexes[subcategory.Subcategory]
			if ok && salaryCategory(subcategory.Category) {
				values[seriesIndex] = appendValue(values[seriesIndex], monthIndex, subcategory.Total.Float())
			}
		}
		for i := range values {
			values[i] = appendValue(values[i], monthIndex, 0)
		}
	}
	return buildMultiLineChart(dates, labels, values)
}

func buildMultiLineChart(dates, labels []string, values [][]float64) MultiLineChart {
	chart := MultiLineChart{Width: chartWidth, Height: chartHeight}
	if len(dates) == 0 || len(labels) == 0 {
		chart.Empty = true
		return chart
	}
	top := 0.0
	for _, seriesValues := range values {
		for _, value := range seriesValues {
			top = max(top, value)
		}
	}
	if top <= 0 {
		chart.Empty = true
		return chart
	}
	plotW := float64(chartWidth - 2*chartPadX)
	plotH := float64(chartHeight - 2*chartPadY)
	xAt := func(index int) float64 {
		if len(dates) == 1 {
			return chartPadX + plotW/2
		}
		return chartPadX + plotW*float64(index)/float64(len(dates)-1)
	}
	yAt := func(value float64) float64 { return chartPadY + plotH*(1-value/top) }
	for seriesIndex, label := range labels {
		series := MultiLineSeries{Class: fmt.Sprintf("series-%d", seriesIndex+1), Label: label}
		var points strings.Builder
		for dateIndex, date := range dates {
			value := 0.0
			if seriesIndex < len(values) && dateIndex < len(values[seriesIndex]) {
				value = values[seriesIndex][dateIndex]
			}
			x, y := xAt(dateIndex), yAt(value)
			fmt.Fprintf(&points, "%.1f,%.1f ", x, y)
			series.Dots = append(series.Dots, chartLabel{X: x, Y: y, Text: fmt.Sprintf("%s, %s: %.2f", date, label, value)})
		}
		series.Points = strings.TrimSpace(points.String())
		chart.Series = append(chart.Series, series)
	}
	for i := 0; i <= 4; i++ {
		value := top * float64(i) / 4
		chart.YTicks = append(chart.YTicks, chartLabel{X: chartPadX, Y: yAt(value), Text: shortNumber(value)})
	}
	step := max(1, len(dates)/6)
	for i := 0; i < len(dates); i += step {
		chart.XTicks = append(chart.XTicks, chartLabel{X: xAt(i), Y: chartHeight - 4, Text: dates[i]})
	}
	return chart
}

func salaryCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "salary", "salary & pensions":
		return true
	default:
		return false
	}
}

func buildIncomeChart(months []store.ExpenseMonth) Chart {
	return buildMonthlyAmountChart(months, func(month store.ExpenseMonth) money.Amount { return month.Income })
}

func buildSavingsRateChart(months []store.ExpenseMonth) Chart {
	points := make([]datedValue, 0, len(months))
	for _, month := range months {
		if month.Income <= 0 {
			continue
		}
		points = append(points, datedValue{date: month.Month, value: month.SavedPct(), label: fmt.Sprintf("%.1f%%", month.SavedPct())})
	}
	return plotWithOptions(points, true, true)
}

func buildTaxRateChart(months []store.ExpenseMonth) Chart {
	byYear := map[string]struct{ income, taxes money.Amount }{}
	for _, month := range months {
		if len(month.Month) < 4 {
			continue
		}
		year := month.Month[:4]
		total := byYear[year]
		total.income += month.Income
		total.taxes += month.Taxes
		byYear[year] = total
	}
	years := make([]string, 0, len(byYear))
	for year, total := range byYear {
		if total.income > 0 {
			years = append(years, year)
		}
	}
	sort.Strings(years)
	points := make([]datedValue, 0, len(years))
	for _, year := range years {
		total := byYear[year]
		rate := float64(total.taxes) / float64(total.income) * 100
		points = append(points, datedValue{date: year, value: rate, label: fmt.Sprintf("%.1f%%", rate)})
	}
	return plotWithOptions(points, true, true)
}

func buildCashFlowBars(months []store.ExpenseMonth) GroupedBarChart {
	chart := GroupedBarChart{Width: chartWidth, Height: chartHeight}
	var top float64
	for _, month := range months {
		top = max(top, month.Income.Float(), (month.Total + month.Taxes).Float())
	}
	if len(months) == 0 || top <= 0 {
		chart.Empty = true
		return chart
	}
	plotW := float64(chartWidth - 2*chartPadX)
	plotH := float64(chartHeight - 2*chartPadY)
	slot := plotW / float64(len(months))
	width := min(22, slot*0.32)
	for i, month := range months {
		income, spending := month.Income.Float(), (month.Total + month.Taxes).Float()
		x := chartPadX + slot*float64(i) + slot/2
		incomeH, spendingH := plotH*max(0, income)/top, plotH*max(0, spending)/top
		chart.Bars = append(chart.Bars, GroupedBar{
			X: x, W: width, IncomeY: chartPadY + plotH - incomeH, IncomeH: incomeH,
			SpendingY: chartPadY + plotH - spendingH, SpendingH: spendingH,
			Label: month.Month, IncomeTitle: month.Month + " income: " + month.Income.String(),
			SpendingTitle: month.Month + " spending + tax: " + (month.Total + month.Taxes).String(),
		})
	}
	for i := 0; i <= 4; i++ {
		chart.YTicks = append(chart.YTicks, chartLabel{X: chartPadX, Y: chartPadY + plotH*(1-float64(i)/4), Text: shortNumber(top * float64(i) / 4)})
	}
	step := max(1, len(months)/6)
	for i := 0; i < len(months); i += step {
		chart.XTicks = append(chart.XTicks, chartLabel{X: chart.Bars[i].X, Y: chartHeight - 4, Text: months[i].Month})
	}
	return chart
}

func buildAllocationHistory(ledger *store.Ledger) StackedChart {
	history := ledger.History()
	labels := []string{"Cash", "Stocks", "Bonds", "Other"}
	classes := []string{store.ClassCash, store.ClassStocks, store.ClassBonds, store.ClassOther}
	values := make([][]float64, len(classes))
	dates := make([]string, len(history))
	for i, point := range history {
		dates[i] = point.Date
		byClass := map[string]money.Amount{}
		for _, item := range ledger.At(point.Date).Allocation().Classes {
			byClass[item.Class] = item.Total
		}
		for j, class := range classes {
			values[j] = append(values[j], byClass[class].Float())
		}
	}
	return buildStackedChart(dates, labels, classes, values)
}

func buildExpenseHistory(months []store.ExpenseMonth) StackedChart {
	totals := map[string]money.Amount{}
	for _, month := range months {
		for _, category := range month.Categories {
			totals[category.Category] += category.Total
		}
	}
	categories := make([]string, 0, len(totals))
	for category := range totals {
		categories = append(categories, category)
	}
	sort.Slice(categories, func(i, j int) bool { return totals[categories[i]] > totals[categories[j]] })
	if len(categories) > 6 {
		categories = categories[:6]
	}
	values := make([][]float64, len(categories)+1)
	labels := append(append([]string{}, categories...), "Other")
	classes := make([]string, len(labels))
	for i := range classes {
		classes[i] = fmt.Sprintf("series-%d", i+1)
	}
	dates := make([]string, len(months))
	for i, month := range months {
		dates[i] = month.Month
		known := map[string]int{}
		for j, category := range categories {
			known[category] = j
		}
		for _, category := range month.Categories {
			index, ok := known[category.Category]
			if !ok {
				index = len(categories)
			}
			values[index] = appendValue(values[index], i, category.Total.Float())
		}
		for j := range values {
			values[j] = appendValue(values[j], i, 0)
		}
	}
	return buildStackedChart(dates, labels, classes, values)
}

func appendValue(values []float64, index int, value float64) []float64 {
	for len(values) <= index {
		values = append(values, 0)
	}
	values[index] += value
	return values
}

func buildStackedChart(dates, labels, classes []string, values [][]float64) StackedChart {
	chart := StackedChart{Width: chartWidth, Height: chartHeight}
	if len(dates) == 0 || len(labels) == 0 {
		chart.Empty = true
		return chart
	}
	top := 0.0
	for i := range dates {
		total := 0.0
		for _, series := range values {
			if i < len(series) {
				total += max(0, series[i])
			}
		}
		top = max(top, total)
	}
	if top <= 0 {
		chart.Empty = true
		return chart
	}
	plotW := float64(chartWidth - 2*chartPadX)
	plotH := float64(chartHeight - 2*chartPadY)
	xAt := func(i int) float64 {
		if len(dates) == 1 {
			return chartPadX + plotW/2
		}
		return chartPadX + plotW*float64(i)/float64(len(dates)-1)
	}
	yAt := func(value float64) float64 { return chartPadY + plotH*(1-value/top) }
	cumulative := make([]float64, len(dates))
	for seriesIndex, series := range values {
		var points strings.Builder
		for i := range dates {
			value := 0.0
			if i < len(series) {
				value = max(0, series[i])
			}
			cumulative[i] += value
			fmt.Fprintf(&points, "%.1f,%.1f ", xAt(i), yAt(cumulative[i]))
		}
		for i := len(dates) - 1; i >= 0; i-- {
			value := 0.0
			if i < len(series) {
				value = max(0, series[i])
			}
			fmt.Fprintf(&points, "%.1f,%.1f ", xAt(i), yAt(cumulative[i]-value))
		}
		chart.Bands = append(chart.Bands, StackedBand{Class: classes[seriesIndex], Label: labels[seriesIndex], Points: strings.TrimSpace(points.String())})
	}
	for i := 0; i <= 4; i++ {
		chart.YTicks = append(chart.YTicks, chartLabel{X: chartPadX, Y: chartPadY + plotH*(1-float64(i)/4), Text: shortNumber(top * float64(i) / 4)})
	}
	step := max(1, len(dates)/6)
	for i := 0; i < len(dates); i += step {
		chart.XTicks = append(chart.XTicks, chartLabel{X: xAt(i), Y: chartHeight - 4, Text: dates[i]})
	}
	return chart
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
	return plotFromZero(points)
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
