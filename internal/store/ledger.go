package store

import (
	"context"
	"math"
	"slices"
	"sort"

	"github.com/MrShanks/networth/internal/money"
)

// Trade is a purchase (positive units) or sale, in the fund's currency.
type Trade struct {
	ID    int64
	AsOf  string
	Units float64
	Price money.Amount
}

// Cost is what the trade was worth when it was made.
func (t Trade) Cost() money.Amount { return value(math.Abs(t.Units), t.Price) }

// PriceMark is what one unit of a fund was worth on a date.
type PriceMark struct {
	ID    int64
	AsOf  string
	Price money.Amount
}

// ValuePoint tracks a holding's worth against the money put into it.
type ValuePoint struct {
	AsOf     string
	Value    money.Amount
	Invested money.Amount
}

// CashPoint is one dated cash balance, in the account's currency.
type CashPoint struct {
	ID     int64
	AsOf   string
	Amount money.Amount
}

// Ledger is the whole database in memory; valuations are derived from it.
type Ledger struct {
	Accounts []Account
	Funds    []Fund

	// Rates is how many CHF one unit of each currency buys, applied to every
	// date: holdings are always shown at the current exchange rate.
	Rates     map[string]float64
	RatesAsOf string
	RatesLive bool

	cash   map[int64][]CashPoint // by account, oldest first
	trades map[int64][]Trade     // by fund, oldest first
	prices map[int64][]PriceMark // by fund, oldest first
}

// Load reads everything the dashboard needs in one go.
func (s *Store) Load(ctx context.Context) (*Ledger, error) {
	l := &Ledger{
		Rates:  map[string]float64{Base: 1},
		cash:   map[int64][]CashPoint{},
		trades: map[int64][]Trade{},
		prices: map[int64][]PriceMark{},
	}

	if err := query(ctx, s.db, `SELECT id, name, owner, bank_ref, kind, currency, asset_class FROM accounts ORDER BY owner, kind, name`,
		func(scan scanner) error {
			var a Account
			if err := scan(&a.ID, &a.Name, &a.Owner, &a.BankRef, &a.Kind, &a.Currency, &a.AssetClass); err != nil {
				return err
			}
			l.Accounts = append(l.Accounts, a)
			return nil
		}); err != nil {
		return nil, err
	}

	if err := query(ctx, s.db, `
        SELECT f.id, f.account_id, a.name, f.name, f.ticker, f.currency, f.asset_class
        FROM funds f JOIN accounts a ON a.id = f.account_id
        ORDER BY a.name, f.name`,
		func(scan scanner) error {
			var f Fund
			if err := scan(&f.ID, &f.AccountID, &f.AccountName, &f.Name, &f.Ticker, &f.Currency, &f.AssetClass); err != nil {
				return err
			}
			l.Funds = append(l.Funds, f)
			return nil
		}); err != nil {
		return nil, err
	}

	if err := query(ctx, s.db, `SELECT id, account_id, as_of, cents FROM balances ORDER BY as_of, id`,
		func(scan scanner) error {
			var (
				accountID int64
				p         CashPoint
				cents     int64
			)
			if err := scan(&p.ID, &accountID, &p.AsOf, &cents); err != nil {
				return err
			}
			p.Amount = money.Amount(cents)
			l.cash[accountID] = append(l.cash[accountID], p)
			return nil
		}); err != nil {
		return nil, err
	}

	if err := query(ctx, s.db, `
        SELECT id, fund_id, as_of, units, price_cents FROM trades ORDER BY as_of, id`,
		func(scan scanner) error {
			var (
				fundID int64
				t      Trade
				price  int64
			)
			if err := scan(&t.ID, &fundID, &t.AsOf, &t.Units, &price); err != nil {
				return err
			}
			t.Price = money.Amount(price)
			l.trades[fundID] = append(l.trades[fundID], t)
			return nil
		}); err != nil {
		return nil, err
	}

	if err := query(ctx, s.db, `
        SELECT id, fund_id, as_of, price_cents FROM prices ORDER BY as_of, id`,
		func(scan scanner) error {
			var (
				fundID int64
				m      PriceMark
				price  int64
			)
			if err := scan(&m.ID, &fundID, &m.AsOf, &price); err != nil {
				return err
			}
			m.Price = money.Amount(price)
			l.prices[fundID] = append(l.prices[fundID], m)
			return nil
		}); err != nil {
		return nil, err
	}

	// Cached rates, superseded by UseRates when a live fetch succeeds.
	if err := query(ctx, s.db, `SELECT currency, as_of, rate FROM rates ORDER BY as_of`,
		func(scan scanner) error {
			var r Rate
			if err := scan(&r.Currency, &r.AsOf, &r.Rate); err != nil {
				return err
			}
			l.Rates[r.Currency] = r.Rate
			if r.AsOf > l.RatesAsOf {
				l.RatesAsOf = r.AsOf
			}
			return nil
		}); err != nil {
		return nil, err
	}

	return l, nil
}

// UseRates switches the ledger to freshly fetched exchange rates.
func (l *Ledger) UseRates(perUnit map[string]float64, asOf string, live bool) {
	for currency, rate := range perUnit {
		if rate > 0 {
			l.Rates[currency] = rate
		}
	}
	l.Rates[Base] = 1
	l.RatesAsOf, l.RatesLive = asOf, live
}

// Position is a fund holding valued on a given date.
type Position struct {
	Fund
	AsOf     string
	Units    float64
	Price    money.Amount
	Value    money.Amount // all money below is in the fund's own currency
	Basis    money.Amount
	Realized money.Amount
	History  []ValuePoint

	Rate      float64
	ValueBase money.Amount
	GainBase  money.Amount
}

// AvgCost is what one held unit cost on average.
func (p Position) AvgCost() money.Amount {
	if p.Units <= 0 {
		return 0
	}
	return money.Amount(math.Round(float64(p.Basis) / p.Units))
}

// Invested is the money still tied up in the fund, net of anything sold.
func (p Position) Invested() money.Amount { return p.Basis - p.Realized }

// Gain is the total return, realized and unrealized.
func (p Position) Gain() money.Amount { return p.Value - p.Basis + p.Realized }

func (p Position) GainPct() float64 {
	if p.Invested() <= 0 {
		return 0
	}
	return float64(p.Gain()) / float64(p.Invested()) * 100
}

// AccountValue is an account with its cash balance and fund positions.
type AccountValue struct {
	Account
	Cash      money.Amount // in the account's currency
	CashAsOf  string
	CashBase  money.Amount
	Positions []Position

	ValueBase    money.Amount // cash plus funds, in the base currency
	InvestedBase money.Amount
	GainBase     money.Amount
}

func (a AccountValue) HasFunds() bool { return len(a.Positions) > 0 }

// Valuation is the state of everything on a single date.
type Valuation struct {
	Date        string
	Accounts    []AccountValue
	Assets      money.Amount
	Liabilities money.Amount

	MarketBase   money.Amount // total fund value
	InvestedBase money.Amount
	GainBase     money.Amount
}

func (v Valuation) NetWorth() money.Amount { return v.Assets - v.Liabilities }

func (v Valuation) GainPct() float64 {
	if v.InvestedBase <= 0 {
		return 0
	}
	return float64(v.GainBase) / float64(v.InvestedBase) * 100
}

// Positions returns every fund position across all accounts.
func (v Valuation) Positions() []Position {
	var out []Position
	for _, a := range v.Accounts {
		out = append(out, a.Positions...)
	}
	return out
}

// At values every account and fund using the latest data on or before date.
func (l *Ledger) At(date string) Valuation {
	if date == "" {
		date = today()
	}
	v := Valuation{Date: date}

	for _, acc := range l.Accounts {
		av := AccountValue{Account: acc}

		if p, ok := latest(l.cash[acc.ID], date, func(p CashPoint) string { return p.AsOf }); ok {
			av.Cash, av.CashAsOf = p.Amount, p.AsOf
			av.CashBase, _ = l.convert(p.Amount, acc.Currency)
			av.ValueBase += av.CashBase
		}

		for _, f := range l.Funds {
			if f.AccountID != acc.ID {
				continue
			}
			pos, ok := l.position(f, date)
			if !ok {
				continue
			}
			av.Positions = append(av.Positions, pos)
			av.ValueBase += pos.ValueBase
			av.GainBase += pos.GainBase
			invested, _ := l.convert(pos.Invested(), f.Currency)
			av.InvestedBase += invested
		}

		if acc.IsLiability() {
			v.Liabilities += av.ValueBase
		} else {
			v.Assets += av.ValueBase
		}
		v.InvestedBase += av.InvestedBase
		v.GainBase += av.GainBase
		for _, p := range av.Positions {
			v.MarketBase += p.ValueBase
		}
		v.Accounts = append(v.Accounts, av)
	}
	return v
}

func (l *Ledger) position(f Fund, date string) (Position, bool) {
	trades := upTo(l.trades[f.ID], date, func(t Trade) string { return t.AsOf })
	if len(trades) == 0 {
		return Position{}, false
	}

	basis, realized, units := track(trades)
	last := trades[len(trades)-1]
	price, asOf := last.Price, last.AsOf
	if m, ok := latest(l.prices[f.ID], date, func(m PriceMark) string { return m.AsOf }); ok && m.AsOf >= asOf {
		price, asOf = m.Price, m.AsOf
	}

	p := Position{
		Fund:     f,
		AsOf:     asOf,
		Units:    units,
		Price:    price,
		Value:    value(units, price),
		Basis:    basis,
		Realized: realized,
		History:  l.fundSeries(f, date),
	}
	p.ValueBase, p.Rate = l.convert(p.Value, f.Currency)
	p.GainBase, _ = l.convert(p.Gain(), f.Currency)
	return p, true
}

// fundSeries walks a fund's own dates, pairing its worth with the money put in.
func (l *Ledger) fundSeries(f Fund, until string) []ValuePoint {
	trades := upTo(l.trades[f.ID], until, func(t Trade) string { return t.AsOf })
	marks := upTo(l.prices[f.ID], until, func(m PriceMark) string { return m.AsOf })

	dates := make([]string, 0, len(trades)+len(marks))
	for _, t := range trades {
		dates = append(dates, t.AsOf)
	}
	for _, m := range marks {
		dates = append(dates, m.AsOf)
	}
	sort.Strings(dates)
	dates = slices.Compact(dates)

	out := make([]ValuePoint, 0, len(dates))
	for _, d := range dates {
		done := upTo(trades, d, func(t Trade) string { return t.AsOf })
		if len(done) == 0 {
			continue
		}
		basis, realized, units := track(done)
		price := done[len(done)-1].Price
		if m, ok := latest(marks, d, func(m PriceMark) string { return m.AsOf }); ok {
			price = m.Price
		}
		out = append(out, ValuePoint{AsOf: d, Value: value(units, price), Invested: basis - realized})
	}
	return out
}

// History returns the net worth on every date that has an entry.
func (l *Ledger) History() []Point {
	dates := l.dates()
	out := make([]Point, 0, len(dates))
	for _, d := range dates {
		v := l.At(d)
		out = append(out, Point{Date: d, Assets: v.Assets, Liabilities: v.Liabilities})
	}
	return out
}

// InvestHistory tracks the whole portfolio's market value against the money put
// into it, in the base currency, so contributions stand apart from growth.
func (l *Ledger) InvestHistory() []ValuePoint {
	var out []ValuePoint
	for _, d := range l.dates() {
		v := l.At(d)
		if v.MarketBase == 0 && v.InvestedBase == 0 {
			continue
		}
		out = append(out, ValuePoint{AsOf: d, Value: v.MarketBase, Invested: v.InvestedBase})
	}
	return out
}

// dates lists every date the ledger has data for, oldest first.
func (l *Ledger) dates() []string {
	var dates []string
	for _, points := range l.cash {
		for _, p := range points {
			dates = append(dates, p.AsOf)
		}
	}
	for _, trades := range l.trades {
		for _, t := range trades {
			dates = append(dates, t.AsOf)
		}
	}
	for _, marks := range l.prices {
		for _, m := range marks {
			dates = append(dates, m.AsOf)
		}
	}
	sort.Strings(dates)
	return slices.Compact(dates)
}

// Point is the computed net worth on a given date, in the base currency.
type Point struct {
	Date        string
	Assets      money.Amount
	Liabilities money.Amount
}

func (p Point) NetWorth() money.Amount { return p.Assets - p.Liabilities }

// MissingRates lists currencies that are in use but have no exchange rate yet.
func (l *Ledger) MissingRates() []string {
	var out []string
	add := func(c string) {
		if l.Rates[c] == 0 && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, a := range l.Accounts {
		add(a.Currency)
	}
	for _, f := range l.Funds {
		add(f.Currency)
	}
	sort.Strings(out)
	return out
}

// convert turns an amount into the base currency at the current rate.
func (l *Ledger) convert(a money.Amount, currency string) (money.Amount, float64) {
	return convert(a, currency, l.Rates)
}

// convert applies the rate for currency, falling back to 1 when unknown.
func convert(a money.Amount, currency string, rates map[string]float64) (money.Amount, float64) {
	rate := rates[currency]
	if rate <= 0 {
		rate = 1
	}
	if rate == 1 {
		return a, 1
	}
	return money.Amount(math.Round(float64(a) * rate)), rate
}

// latest returns the last element dated on or before date.
func latest[T any](items []T, date string, dateOf func(T) string) (T, bool) {
	var found T
	ok := false
	for _, it := range items {
		if dateOf(it) > date {
			break
		}
		found, ok = it, true
	}
	return found, ok
}

// upTo returns the leading run of items dated on or before date.
func upTo[T any](items []T, date string, dateOf func(T) string) []T {
	n := 0
	for n < len(items) && dateOf(items[n]) <= date {
		n++
	}
	return items[:n]
}

func value(units float64, price money.Amount) money.Amount {
	return money.Amount(math.Round(units * float64(price)))
}

// track replays trades to derive the average-cost basis: a purchase adds what
// was paid, a sale removes units at their average cost and books the rest as a
// realized gain.
func track(trades []Trade) (basis, realized money.Amount, units float64) {
	for _, t := range trades {
		switch {
		case t.Units > 0:
			basis += value(t.Units, t.Price)
		case t.Units < 0 && units > 0:
			sold := math.Min(-t.Units, units)
			removed := money.Amount(math.Round(sold / units * float64(basis)))
			basis -= removed
			realized += value(sold, t.Price) - removed
		}
		units = math.Max(0, units+t.Units)
	}
	return basis, realized, units
}
