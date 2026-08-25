package store

import (
	"context"
	"database/sql"
	"sort"

	"github.com/MrShanks/networth/internal/money"
)

type scanner func(dest ...any) error

// query runs a read query and hands each row to fn.
func query(ctx context.Context, db *sql.DB, q string, fn func(scanner) error, args ...any) error {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		if err := fn(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Entry is a recorded balance, trade or price mark, for the activity list.
type Entry struct {
	ID       int64
	Path     string // where a delete of this entry posts to
	Kind     string
	AsOf     string
	Name     string
	Currency string
	Amount   money.Amount
	Units    float64
	Price    money.Amount
}

// impliedBy reports whether a trade already carries this price, in which case
// listing the mark separately would only repeat it.
func impliedBy(trades []Trade, m PriceMark) bool {
	for _, t := range trades {
		if t.AsOf == m.AsOf && t.Price == m.Price {
			return true
		}
	}
	return false
}

// Entries lists the most recent entries, newest first.
func (l *Ledger) Entries(limit int) []Entry {
	var out []Entry

	for _, acc := range l.Accounts {
		for _, p := range l.cash[acc.ID] {
			out = append(out, Entry{
				ID:       p.ID,
				Path:     "/balances",
				Kind:     "balance",
				AsOf:     p.AsOf,
				Name:     acc.Name,
				Currency: acc.Currency,
				Amount:   p.Amount,
			})
		}
	}
	for _, f := range l.Funds {
		name := f.AccountName + " · " + f.Name
		for _, t := range l.trades[f.ID] {
			kind := "bought"
			if t.Units < 0 {
				kind = "sold"
			}
			out = append(out, Entry{
				ID:       t.ID,
				Path:     "/trades",
				Kind:     kind,
				AsOf:     t.AsOf,
				Name:     name,
				Currency: f.Currency,
				Amount:   t.Cost(),
				Units:    t.Units,
				Price:    t.Price,
			})
		}
		for _, m := range l.prices[f.ID] {
			if impliedBy(l.trades[f.ID], m) {
				continue
			}
			out = append(out, Entry{
				ID:       m.ID,
				Path:     "/prices",
				Kind:     "price",
				AsOf:     m.AsOf,
				Name:     name,
				Currency: f.Currency,
				Amount:   m.Price,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].AsOf != out[j].AsOf {
			return out[i].AsOf > out[j].AsOf
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
