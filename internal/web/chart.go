package web

import (
	"fmt"
	"strings"

	"github.com/MrShanks/networh/internal/store"
)

const (
	chartWidth  = 760
	chartHeight = 260
	chartPadX   = 56
	chartPadY   = 20
)

type chartLabel struct {
	X, Y float64
	Text string
}

// Series is an extra line drawn on the same scale as the main one.
type Series struct {
	Class  string
	Points string
}

// Chart holds pre-computed SVG geometry so the template stays free of logic.
type Chart struct {
	Width, Height float64
	Line          string // polyline "x,y" points
	Area          string // same points closed against the baseline
	Extra         []Series
	Dots          []chartLabel
	YTicks        []chartLabel
	XTicks        []chartLabel
	Empty         bool
}

func buildChart(points []store.Point) Chart {
	dated := make([]datedValue, len(points))
	for i, p := range points {
		dated[i] = datedValue{date: p.Date, value: p.NetWorth().Float(), label: p.NetWorth().String()}
	}
	return plot(dated)
}

// buildInvestChart draws the portfolio's market value against what was paid for
// it, so contributions can be told apart from growth.
func buildInvestChart(points []store.ValuePoint) Chart {
	dated := make([]datedValue, len(points))
	invested := make([]float64, len(points))
	for i, p := range points {
		dated[i] = datedValue{
			date:  p.AsOf,
			value: p.Value.Float(),
			label: "value " + p.Value.String() + ", invested " + p.Invested.String(),
		}
		invested[i] = p.Invested.Float()
	}
	return plot(dated, series{class: "invested", values: invested})
}

type datedValue struct {
	date  string
	value float64
	label string
}

type series struct {
	class  string
	values []float64
}

// plot lays out a main series and any extra lines on the same scale.
func plot(points []datedValue, extra ...series) Chart {
	c := Chart{Width: chartWidth, Height: chartHeight}
	if len(points) == 0 {
		c.Empty = true
		return c
	}

	min, max := points[0].value, points[0].value
	for _, p := range points {
		min = minf(min, p.value)
		max = maxf(max, p.value)
	}
	for _, s := range extra {
		for _, v := range s.values {
			min = minf(min, v)
			max = maxf(max, v)
		}
	}
	if max == min {
		max, min = max+1, min-1
	}

	plotW := float64(chartWidth - 2*chartPadX)
	plotH := float64(chartHeight - 2*chartPadY)

	xAt := func(i int) float64 {
		if len(points) == 1 {
			return chartPadX + plotW/2
		}
		return chartPadX + plotW*float64(i)/float64(len(points)-1)
	}
	yAt := func(v float64) float64 {
		return chartPadY + plotH*(1-(v-min)/(max-min))
	}

	var line strings.Builder
	// Dots carry the tooltips, but on a dense series they swallow the line.
	withDots := len(points) <= 40
	for i, p := range points {
		x, y := xAt(i), yAt(p.value)
		fmt.Fprintf(&line, "%.1f,%.1f ", x, y)
		if withDots {
			c.Dots = append(c.Dots, chartLabel{X: x, Y: y, Text: p.date + ": " + p.label})
		}
	}
	c.Line = strings.TrimSpace(line.String())
	baseline := float64(chartHeight - chartPadY)
	c.Area = fmt.Sprintf("%.1f,%.1f %s %.1f,%.1f",
		xAt(0), baseline, c.Line, xAt(len(points)-1), baseline)

	for _, s := range extra {
		if len(s.values) != len(points) {
			continue
		}
		var line strings.Builder
		for i, v := range s.values {
			fmt.Fprintf(&line, "%.1f,%.1f ", xAt(i), yAt(v))
		}
		c.Extra = append(c.Extra, Series{Class: s.class, Points: strings.TrimSpace(line.String())})
	}

	for i := 0; i <= 4; i++ {
		v := min + (max-min)*float64(i)/4
		c.YTicks = append(c.YTicks, chartLabel{
			X:    chartPadX,
			Y:    yAt(v),
			Text: shortNumber(v),
		})
	}

	step := max2(1, len(points)/6)
	for i := 0; i < len(points); i += step {
		c.XTicks = append(c.XTicks, chartLabel{X: xAt(i), Y: chartHeight - 4, Text: points[i].date})
	}
	return c
}

const (
	sparkWidth  = 120
	sparkHeight = 30
)

// sparkPoints renders a fund's value history as polyline coordinates.
func sparkPoints(history []store.ValuePoint) string {
	if len(history) < 2 {
		return ""
	}

	min, max := history[0].Value.Float(), history[0].Value.Float()
	for _, s := range history {
		min = minf(min, s.Value.Float())
		max = maxf(max, s.Value.Float())
	}
	if max == min {
		max, min = max+1, min-1
	}

	var b strings.Builder
	for i, s := range history {
		x := sparkWidth * float64(i) / float64(len(history)-1)
		y := 2 + (sparkHeight-4)*(1-(s.Value.Float()-min)/(max-min))
		fmt.Fprintf(&b, "%.1f,%.1f ", x, y)
	}
	return strings.TrimSpace(b.String())
}

func shortNumber(v float64) string {
	abs := v
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1_000_000:
		return fmt.Sprintf("%.1fM", v/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("%.0fk", v/1_000)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
