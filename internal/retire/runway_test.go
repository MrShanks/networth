package retire

import (
	"testing"
	"time"

	"github.com/MrShanks/networh/internal/money"
)

func runwayParams() RunwayParams {
	return RunwayParams{
		NetWorth:        120_000_00,
		MonthlyExpenses: 4_000_00,
		MonthlySavings:  2_000_00,
		PassionIncome:   2_000_00,
		WorkMonths:      12,
		ReturnPct:       2,
		InflationPct:    2, // no real growth, so the arithmetic is plain
		Start:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestBurnScenarios(t *testing.T) {
	run := Burn(runwayParams())

	if len(run.Scenarios) != 3 {
		t.Fatalf("got %d scenarios, want 3", len(run.Scenarios))
	}

	// 120k at 4k a month lasts 30 months.
	if got, want := run.Scenarios[0].Months, 30; got != want {
		t.Errorf("stopping now lasts %d months, want %d", got, want)
	}
	// A year of saving 2k adds 24k, which buys another 6 months on top of the 12.
	if got, want := run.Scenarios[1].Months, 48; got != want {
		t.Errorf("working a year lasts %d months, want %d", got, want)
	}
	// Earning half the expenses halves the burn.
	if got, want := run.Scenarios[2].Months, 60; got != want {
		t.Errorf("passion project lasts %d months, want %d", got, want)
	}

	for _, s := range run.Scenarios {
		if len(s.Values) != len(run.Dates) {
			t.Errorf("%s has %d values, want %d to match the dates",
				s.Name, len(s.Values), len(run.Dates))
		}
		if last := s.Values[len(s.Values)-1]; last != 0 {
			t.Errorf("%s ends at %s, want it flat at zero", s.Name, last)
		}
	}
	if got, want := run.Scenarios[0].Years(), 2; got != want {
		t.Errorf("Years() = %d, want %d", got, want)
	}
	if got, want := run.Scenarios[0].RemainingMonths(), 6; got != want {
		t.Errorf("RemainingMonths() = %d, want %d", got, want)
	}
}

func TestBurnNeverRunsOut(t *testing.T) {
	p := runwayParams()
	p.PassionIncome = p.MonthlyExpenses // the project covers everything

	run := Burn(p)
	passion := run.Scenarios[2]
	if !passion.Forever {
		t.Errorf("passion project runs out after %d months, want never", passion.Months)
	}
	if got := passion.Values[len(passion.Values)-1]; got != money.Amount(120_000_00) {
		t.Errorf("portfolio ends at %s, want it untouched", got)
	}
}
