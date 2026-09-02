package store

import (
	"context"
	"database/sql"
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
