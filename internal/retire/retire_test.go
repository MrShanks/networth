package retire

import (
	"testing"
	"time"

	"github.com/MrShanks/networh/internal/money"
)

func params() Params {
	return Params{
		NetWorth:        100_000_00,
		MonthlyExpenses: 4_000_00,
		MonthlySavings:  2_000_00,
		ReturnPct:       7,
		InflationPct:    2,
		WithdrawalPct:   4,
		Start:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestRealReturnStripsInflation(t *testing.T) {
	p := params()
	if got := p.RealReturnPct(); got < 4.8 || got > 5.0 {
		t.Errorf("RealReturnPct() = %.2f, want about 4.9", got)
	}

	p.InflationPct = 0
	if got := p.RealReturnPct(); got < 6.99 || got > 7.01 {
		t.Errorf("RealReturnPct() without inflation = %.2f, want 7", got)
	}
}

func TestInflationDelaysRetirement(t *testing.T) {
	base := Project(params())

	p := params()
	p.InflationPct = 5
	slower := Project(p)

	if slower.Months <= base.Months {
		t.Errorf("higher inflation took %d months, base took %d", slower.Months, base.Months)
	}
	// The target is in today's money, so it does not move.
	if slower.Target != base.Target {
		t.Errorf("Target = %s, want it unchanged at %s", slower.Target, base.Target)
	}
}

func TestTargetFollowsWithdrawalRate(t *testing.T) {
	plan := Project(params())

	// 4,000 a month is 48,000 a year; at 4% that needs 25 times as much.
	if got, want := plan.Target, money.Amount(1_200_000_00); got != want {
		t.Errorf("Target = %s, want %s", got, want)
	}
	if !plan.Reachable {
		t.Fatal("plan should be reachable")
	}
	// Roughly 22 years of saving 2k at about 5% real.
	if plan.Years() < 20 || plan.Years() > 24 {
		t.Errorf("Years() = %d, want about 22", plan.Years())
	}
	if got, want := plan.Date, plan.Points[plan.Months].Date; got != want {
		t.Errorf("Date = %q, want the month the target is reached %q", got, want)
	}
}

func TestAlreadyRetired(t *testing.T) {
	p := params()
	p.NetWorth = 2_000_000_00

	plan := Project(p)
	if !plan.Reachable || plan.Months != 0 {
		t.Errorf("Months = %d, reachable = %v, want 0 and true", plan.Months, plan.Reachable)
	}
	if got := plan.Progress(); got != 100 {
		t.Errorf("Progress() = %v, want 100", got)
	}
	if got := plan.Shortfall(); got != 0 {
		t.Errorf("Shortfall() = %s, want 0.00", got)
	}
	// The chart still needs a line to draw.
	if len(plan.Points) < 2 {
		t.Errorf("projected %d points, want a full tail", len(plan.Points))
	}
}

func TestProjectionRunsPastTheTarget(t *testing.T) {
	plan := Project(params())

	if !plan.Reachable {
		t.Fatal("plan should be reachable")
	}
	if got, want := len(plan.Points), plan.Months+tailMonths+1; got != want {
		t.Errorf("projected %d points, want %d", got, want)
	}
	if last := plan.Points[len(plan.Points)-1]; last.Value <= plan.Target {
		t.Errorf("last point %s should be past the target %s", last.Value, plan.Target)
	}
}

func TestUnreachableWithoutSavingsOrReturn(t *testing.T) {
	p := params()
	p.MonthlySavings, p.ReturnPct, p.InflationPct = 0, 0, 0

	plan := Project(p)
	if plan.Reachable {
		t.Error("plan should not be reachable")
	}
	if plan.Date != "" {
		t.Errorf("Date = %q, want empty", plan.Date)
	}
	if len(plan.Points) != maxMonths+1 {
		t.Errorf("projected %d points, want %d", len(plan.Points), maxMonths+1)
	}
}

func TestSavingMoreRetiresSooner(t *testing.T) {
	base := Project(params())

	p := params()
	p.MonthlySavings *= 2
	faster := Project(p)

	if faster.Months >= base.Months {
		t.Errorf("saving double took %d months, base took %d", faster.Months, base.Months)
	}
}
