package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/MrShanks/networth/internal/money"
)

// SourceFetched marks a price pulled from the price service rather than
// entered by hand, so a refetch may replace it and the activity list can leave
// it out.
const SourceFetched = "fetched"

// Fund reads a single fund.
func (s *Store) Fund(ctx context.Context, id int64) (Fund, error) {
	var f Fund
	err := s.db.QueryRowContext(ctx, `
        SELECT f.id, f.account_id, a.name, f.name, f.ticker, f.symbol, f.currency, f.asset_class
        FROM funds f JOIN accounts a ON a.id = f.account_id
        WHERE f.id = ?`, id).
		Scan(&f.ID, &f.AccountID, &f.AccountName, &f.Name, &f.Ticker, &f.Symbol, &f.Currency, &f.AssetClass)
	if errors.Is(err, sql.ErrNoRows) {
		return Fund{}, ErrNotFound
	}
	return f, err
}

// SetFundSymbol remembers which listing a fund's prices come from.
func (s *Store) SetFundSymbol(ctx context.Context, id int64, symbol string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE funds SET symbol = ? WHERE id = ?`, symbol, id)
	return err
}

// FirstTradeDate is the day the fund was first bought, empty when it has no
// trades yet.
func (s *Store) FirstTradeDate(ctx context.Context, fundID int64) (string, error) {
	var date sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT MIN(as_of) FROM trades WHERE fund_id = ?`, fundID).Scan(&date)
	if err != nil {
		return "", err
	}
	return date.String, nil
}

// PricePoint is one day's closing price of a fund, in its own currency.
type PricePoint struct {
	AsOf  string
	Price money.Amount
}

// ImportPrices fills in a fund's price history. Marks entered by hand, and
// those a trade wrote, are left alone: only a previous fetch is replaced.
func (s *Store) ImportPrices(ctx context.Context, fundID int64, points []PricePoint) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO prices (fund_id, as_of, price_cents, source) VALUES (?, ?, ?, '`+SourceFetched+`')
        ON CONFLICT (fund_id, as_of) DO UPDATE SET price_cents = excluded.price_cents
        WHERE prices.source = '`+SourceFetched+`'`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	stored := 0
	for _, p := range points {
		if err := checkDate(p.AsOf); err != nil {
			return 0, err
		}
		if p.Price <= 0 {
			continue
		}
		result, err := stmt.ExecContext(ctx, fundID, p.AsOf, int64(p.Price))
		if err != nil {
			return 0, err
		}
		if n, _ := result.RowsAffected(); n > 0 {
			stored++
		}
	}
	return stored, tx.Commit()
}
