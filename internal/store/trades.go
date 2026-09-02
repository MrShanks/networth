package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/MrShanks/networth/internal/money"
)

// BrokerTrade is one trade read from a broker export, waiting to be filed
// under the account that holds the instrument.
type BrokerTrade struct {
	Name     string
	Ticker   string // ISIN or symbol, what the fund is matched on
	Currency string
	AsOf     string
	Units    float64
	Price    money.Amount
}

// TradeActivity is one stored broker trade with its fund and account context.
type TradeActivity struct {
	ID          int64
	AccountName string
	FundName    string
	Ticker      string
	Currency    string
	AsOf        string
	Units       float64
	Price       money.Amount
}

func (t TradeActivity) IsSale() bool { return t.Units < 0 }

// TradeActivities returns stored trades newest first.
func (s *Store) TradeActivities(ctx context.Context) ([]TradeActivity, error) {
	var activities []TradeActivity
	err := query(ctx, s.db, `
        SELECT t.id, a.name, f.name, f.ticker, f.currency, t.as_of, t.units, t.price_cents
        FROM trades t
        JOIN funds f ON f.id = t.fund_id
        JOIN accounts a ON a.id = f.account_id
        ORDER BY t.as_of DESC, t.id DESC`, func(scan scanner) error {
		var activity TradeActivity
		var cents int64
		if err := scan(&activity.ID, &activity.AccountName, &activity.FundName, &activity.Ticker,
			&activity.Currency, &activity.AsOf, &activity.Units, &cents); err != nil {
			return err
		}
		activity.Price = money.Amount(cents)
		activities = append(activities, activity)
		return nil
	})
	return activities, err
}

// ImportTrades files broker trades under an account, creating any fund that
// isn't there yet. Trades already stored are counted as duplicates and left
// alone, so importing the same export twice changes nothing.
func (s *Store) ImportTrades(ctx context.Context, accountID int64, trades []BrokerTrade) (added, duplicates int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE id = ?`, accountID).Scan(&exists); err != nil {
		return 0, 0, err
	}
	if exists == 0 {
		return 0, 0, ErrNotFound
	}

	funds := map[string]int64{}
	// How many identical trades are already stored; an export can legitimately
	// repeat one, so only the stored ones are treated as duplicates.
	type tradeKey struct {
		fundID int64
		asOf   string
		units  float64
		price  money.Amount
	}
	stored := map[tradeKey]int{}

	for _, t := range trades {
		if err := checkCurrency(t.Currency); err != nil {
			return 0, 0, err
		}
		if err := checkDate(t.AsOf); err != nil {
			return 0, 0, err
		}
		if t.Units == 0 || math.IsNaN(t.Units) || math.IsInf(t.Units, 0) {
			return 0, 0, errors.New("units cannot be zero")
		}
		if t.Price < 0 {
			return 0, 0, errors.New("price cannot be negative")
		}

		key := t.Ticker
		if key == "" {
			key = t.Name
		}
		fundID, ok := funds[key]
		if !ok {
			fundID, err = findOrCreateFund(ctx, tx, accountID, t)
			if err != nil {
				return 0, 0, err
			}
			funds[key] = fundID
		}

		tk := tradeKey{fundID: fundID, asOf: t.AsOf, units: t.Units, price: t.Price}
		if _, seen := stored[tk]; !seen {
			var n int
			if err := tx.QueryRowContext(ctx, `
                SELECT COUNT(*) FROM trades
                WHERE fund_id = ? AND as_of = ? AND units = ? AND price_cents = ?`,
				fundID, t.AsOf, t.Units, int64(t.Price)).Scan(&n); err != nil {
				return 0, 0, err
			}
			stored[tk] = n
		}
		if stored[tk] > 0 {
			stored[tk]--
			duplicates++
			continue
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO trades (fund_id, as_of, units, price_cents) VALUES (?, ?, ?, ?)`,
			fundID, t.AsOf, t.Units, int64(t.Price)); err != nil {
			return 0, 0, err
		}
		// The traded price is also a valid mark for that day.
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO prices (fund_id, as_of, price_cents) VALUES (?, ?, ?)
            ON CONFLICT (fund_id, as_of) DO UPDATE SET price_cents = excluded.price_cents`,
			fundID, t.AsOf, int64(t.Price)); err != nil {
			return 0, 0, err
		}
		added++
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return added, duplicates, nil
}

// findOrCreateFund matches an imported instrument to a fund of the account by
// ticker first, then by name, and creates it when the account holds it for the
// first time.
func findOrCreateFund(ctx context.Context, tx *sql.Tx, accountID int64, t BrokerTrade) (int64, error) {
	var id int64
	if t.Ticker != "" {
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM funds WHERE account_id = ? AND ticker = ?`, accountID, t.Ticker).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM funds WHERE account_id = ? AND name = ?`, accountID, t.Name).Scan(&id)
	switch {
	case err == nil:
		return id, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}

	result, err := tx.ExecContext(ctx,
		`INSERT INTO funds (account_id, name, ticker, currency, asset_class) VALUES (?, ?, ?, ?, ?)`,
		accountID, t.Name, t.Ticker, t.Currency, ClassStocks)
	if err != nil {
		return 0, fmt.Errorf("add fund %s: %w", t.Name, err)
	}
	return result.LastInsertId()
}
