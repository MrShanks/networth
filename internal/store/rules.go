package store

import (
	"context"
	"errors"
	"strings"
)

// Rule puts every expense whose description contains Pattern into Category.
type Rule struct {
	ID       int64
	Pattern  string
	Category string
}

// Matches reports whether a description falls under the rule.
func (r Rule) Matches(note string) bool {
	return strings.Contains(strings.ToLower(note), strings.ToLower(r.Pattern))
}

const rulesSchema = `
CREATE TABLE IF NOT EXISTS category_rules (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    pattern  TEXT NOT NULL COLLATE NOCASE UNIQUE,
    category TEXT NOT NULL
);
`

func (s *Store) Rules(ctx context.Context) ([]Rule, error) {
	var out []Rule
	err := query(ctx, s.db, `SELECT id, pattern, category FROM category_rules ORDER BY id`,
		func(scan scanner) error {
			var r Rule
			if err := scan(&r.ID, &r.Pattern, &r.Category); err != nil {
				return err
			}
			out = append(out, r)
			return nil
		})
	return out, err
}

func (s *Store) AddRule(ctx context.Context, pattern, category string) error {
	pattern, category = strings.TrimSpace(pattern), strings.TrimSpace(category)
	if pattern == "" {
		return errors.New("the text to look for is required")
	}
	if category == "" {
		return errors.New("category is required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO category_rules (pattern, category) VALUES (?, ?)`, pattern, category)
	return err
}

func (s *Store) DeleteRule(ctx context.Context, id int64) error {
	return s.deleteRow(ctx, `DELETE FROM category_rules WHERE id = ?`, id)
}

// Recategorise moves already stored expenses to where the rule says they belong.
func (s *Store) Recategorise(ctx context.Context, rule Rule) (int, error) {
	expenses, err := s.Expenses(ctx)
	if err != nil {
		return 0, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	moved := 0
	for _, e := range expenses {
		if e.Category == rule.Category || !rule.Matches(e.Note) {
			continue
		}
		kind := classifyEntry(e.Kind, rule.Category)
		if e.Kind == KindTax && !isTaxCategory(rule.Category) {
			kind = KindExpense
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE expenses SET category = ?, kind = ? WHERE id = ?`, rule.Category, kind, e.ID); err != nil {
			return 0, err
		}
		moved++
	}
	return moved, tx.Commit()
}

// Categorise applies the rules to freshly parsed rows, first match winning.
func Categorise(rows []Expense, rules []Rule) []Expense {
	for i, row := range rows {
		for _, rule := range rules {
			if rule.Matches(row.Note) {
				rows[i].Category = rule.Category
				rows[i].Kind = classifyEntry(row.Kind, rule.Category)
				break
			}
		}
	}
	return rows
}
