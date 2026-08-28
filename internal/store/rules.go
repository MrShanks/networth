package store

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

const (
	RuleAny   = "any"
	RuleAll   = "all"
	RuleRegex = "regex"
)

// Rule puts every expense whose description matches Pattern into Category.
type Rule struct {
	ID          int64
	Mode        string
	Pattern     string
	Category    string
	Subcategory string
}

// Matches reports whether a description falls under the rule.
func (r Rule) Matches(note string) bool {
	if r.Mode == RuleRegex {
		matched, err := regexp.MatchString("(?i)"+r.Pattern, note)
		return err == nil && matched
	}
	terms := ruleTerms(r.Pattern)
	if len(terms) == 0 {
		return false
	}
	note = strings.ToLower(note)
	for _, term := range terms {
		if strings.Contains(note, strings.ToLower(term)) {
			if r.Mode != RuleAll {
				return true
			}
		} else if r.Mode == RuleAll {
			return false
		}
	}
	return r.Mode == RuleAll
}

func ruleTerms(pattern string) []string {
	parts := strings.FieldsFunc(pattern, func(r rune) bool { return r == '\n' || r == '\r' })
	terms := parts[:0]
	for _, part := range parts {
		if term := strings.TrimSpace(part); term != "" {
			terms = append(terms, term)
		}
	}
	return terms
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
	err := query(ctx, s.db, `SELECT id, match_mode, pattern, category, subcategory FROM category_rules ORDER BY id`,
		func(scan scanner) error {
			var r Rule
			if err := scan(&r.ID, &r.Mode, &r.Pattern, &r.Category, &r.Subcategory); err != nil {
				return err
			}
			out = append(out, r)
			return nil
		})
	return out, err
}

func (s *Store) AddRule(ctx context.Context, mode, pattern, category string, subcategories ...string) error {
	mode, pattern, category, err := validateRule(mode, pattern, category)
	if err != nil {
		return err
	}
	subcategory := optionalSubcategory(subcategories)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO category_rules (match_mode, pattern, category, subcategory) VALUES (?, ?, ?, ?)`,
		mode, pattern, category, subcategory)
	return err
}

func (s *Store) UpdateRule(ctx context.Context, id int64, mode, pattern, category string, subcategories ...string) error {
	mode, pattern, category, err := validateRule(mode, pattern, category)
	if err != nil {
		return err
	}
	subcategory := optionalSubcategory(subcategories)
	result, err := s.db.ExecContext(ctx,
		`UPDATE category_rules SET match_mode = ?, pattern = ?, category = ?, subcategory = ? WHERE id = ?`,
		mode, pattern, category, subcategory, id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return ErrNotFound
	}
	return nil
}

func optionalSubcategory(subcategories []string) string {
	if len(subcategories) == 0 {
		return ""
	}
	return strings.TrimSpace(subcategories[0])
}

func validateRule(mode, pattern, category string) (string, string, string, error) {
	mode, pattern, category = strings.TrimSpace(mode), strings.TrimSpace(pattern), strings.TrimSpace(category)
	if mode == "" {
		mode = RuleAny
	}
	if mode != RuleAny && mode != RuleAll && mode != RuleRegex {
		return "", "", "", errors.New("unknown rule match mode")
	}
	if pattern == "" {
		return "", "", "", errors.New("the text to look for is required")
	}
	if mode != RuleRegex && len(ruleTerms(pattern)) == 0 {
		return "", "", "", errors.New("at least one term is required")
	}
	if mode == RuleRegex {
		if _, err := regexp.Compile("(?i)" + pattern); err != nil {
			return "", "", "", errors.New("regular expression is invalid")
		}
	}
	if category == "" {
		return "", "", "", errors.New("category is required")
	}
	return mode, pattern, category, nil
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
		if e.Kind == KindTransfer || !rule.Matches(e.Note) || (e.Category == rule.Category && e.Subcategory == rule.Subcategory) {
			continue
		}
		kind := classifyEntry(e.Kind, rule.Category)
		if e.Kind == KindTax && !isTaxCategory(rule.Category) {
			kind = KindExpense
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE expenses SET category = ?, subcategory = ?, kind = ? WHERE id = ?`,
			rule.Category, rule.Subcategory, kind, e.ID); err != nil {
			return 0, err
		}
		moved++
	}
	return moved, tx.Commit()
}

// Categorise applies the rules to freshly parsed rows, first match winning.
func Categorise(rows []Expense, rules []Rule) []Expense {
	for i, row := range rows {
		if row.Kind == KindTransfer {
			continue
		}
		for _, rule := range rules {
			if rule.Matches(row.Note) {
				rows[i].Category = rule.Category
				rows[i].Subcategory = rule.Subcategory
				rows[i].Kind = classifyEntry(row.Kind, rule.Category)
				break
			}
		}
	}
	return rows
}
