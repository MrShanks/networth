package store

import (
	"sort"

	"github.com/MrShanks/networth/internal/money"
)

// ClassTotal is what one asset class is worth, across every account and fund.
type ClassTotal struct {
	Class string
	Label string
	Total money.Amount // in the base currency
	Share float64      // percent of total assets
}

// Allocation breaks total assets down by asset class, in the base currency.
type Allocation struct {
	Total   money.Amount
	Classes []ClassTotal // biggest first, zero classes left out
}

// Liquidity is how much of your assets are sitting in cash, ready to spend,
// versus tied up in anything else — including a cash balance you have marked
// as invested (a pension held entirely in equities, for example).
type Liquidity struct {
	Liquid   money.Amount
	Illiquid money.Amount
	Total    money.Amount
	Pct      float64 // liquid share of total assets
}

// Allocation groups every asset — account cash and fund holdings alike — by
// asset class. Liabilities are not assets and are left out.
func (v Valuation) Allocation() Allocation {
	totals := map[string]money.Amount{}
	for _, av := range v.Accounts {
		if av.IsLiability() {
			continue
		}
		totals[av.AssetClass] += av.CashBase
		for _, p := range av.Positions {
			totals[p.AssetClass] += p.ValueBase
		}
	}

	var a Allocation
	for class, amount := range totals {
		if amount == 0 {
			continue
		}
		a.Total += amount
		a.Classes = append(a.Classes, ClassTotal{Class: class, Label: ClassLabel(class), Total: amount})
	}
	for i := range a.Classes {
		if a.Total > 0 {
			a.Classes[i].Share = float64(a.Classes[i].Total) / float64(a.Total) * 100
		}
	}
	sort.Slice(a.Classes, func(i, j int) bool { return a.Classes[i].Total > a.Classes[j].Total })
	return a
}

// Liquidity summarises the allocation into liquid versus everything else.
func (v Valuation) Liquidity() Liquidity {
	alloc := v.Allocation()
	l := Liquidity{Total: alloc.Total}
	for _, c := range alloc.Classes {
		if c.Class == ClassCash {
			l.Liquid = c.Total
		}
	}
	l.Illiquid = l.Total - l.Liquid
	if l.Total > 0 {
		l.Pct = float64(l.Liquid) / float64(l.Total) * 100
	}
	return l
}

// IlliquidPct is the share of assets that are not liquid.
func (l Liquidity) IlliquidPct() float64 { return 100 - l.Pct }
