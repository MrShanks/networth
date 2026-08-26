package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/money"
)

// Entry kinds: money going out, and money coming in.
const (
	KindExpense = "expense"
	KindIncome  = "income"
	KindTax     = "tax"
)

// Expense is a single cash flow record, in its own currency. A negative amount
// on an expense is a refund, which nets off against the rest of the month.
type Expense struct {
	ID       int64
	Kind     string
	AsOf     string
	Category string
	Note     string
	Currency string
	Amount   money.Amount
}

// IsRefund reports whether this entry gave money back.
func (e Expense) IsRefund() bool { return e.Kind != KindIncome && e.Amount < 0 }

// IsIncome reports whether this entry is money coming in.
func (e Expense) IsIncome() bool { return e.Kind == KindIncome }

// IsTax reports whether this entry is a tax payment kept outside spending.
func (e Expense) IsTax() bool { return e.Kind == KindTax }

// Month is the YYYY-MM the entry belongs to.
func (e Expense) Month() string { return e.AsOf[:7] }

const expensesSchema = `
CREATE TABLE IF NOT EXISTS expenses (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    as_of    TEXT NOT NULL,
    category TEXT NOT NULL,
    note     TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL,
    cents    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS expenses_as_of ON expenses(as_of);
`

// AddEntry records money spent or earned.
func (s *Store) AddEntry(ctx context.Context, kind, asOf, category, note, currency string, amount money.Amount) error {
	if kind != KindExpense && kind != KindIncome && kind != KindTax {
		return fmt.Errorf("unknown entry kind %q", kind)
	}
	if err := checkDate(asOf); err != nil {
		return err
	}
	if err := checkCurrency(currency); err != nil {
		return err
	}
	category = strings.TrimSpace(category)
	if category == "" {
		return errors.New("category is required")
	}
	kind = classifyEntry(kind, category)
	if amount == 0 {
		return errors.New("amount cannot be zero")
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO expenses (kind, as_of, category, note, currency, cents) VALUES (?, ?, ?, ?, ?, ?)`,
		kind, asOf, category, strings.TrimSpace(note), currency, int64(amount))
	return err
}

func (s *Store) DeleteExpense(ctx context.Context, id int64) error {
	return s.deleteRow(ctx, `DELETE FROM expenses WHERE id = ?`, id)
}

// SetExpenseCategory moves one entry to another category and keeps its tax kind
// in sync. Income remains income regardless of category.
func (s *Store) SetExpenseCategory(ctx context.Context, id int64, category string) error {
	category = strings.TrimSpace(category)
	if category == "" {
		return errors.New("category is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM expenses WHERE id = ?`, id).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if kind != KindIncome {
		kind = classifyEntry(KindExpense, category)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE expenses SET category = ?, kind = ? WHERE id = ?`, category, kind, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteExpensesForMonth removes every entry in a YYYY-MM month.
func (s *Store) DeleteExpensesForMonth(ctx context.Context, month string) (int, error) {
	if _, err := time.Parse("2006-01", month); err != nil {
		return 0, fmt.Errorf("invalid month %q", month)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM expenses WHERE as_of LIKE ?`, month+"-%")
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ImportExpenses adds entries in bulk, leaving out any that are already
// stored, so re-importing the same export changes nothing. Identical entries
// recorded twice on purpose are kept: only the surplus is treated as duplicate.
func (s *Store) ImportExpenses(ctx context.Context, rows []Expense) (added, duplicates int, err error) {
	existing, err := s.Expenses(ctx)
	if err != nil {
		return 0, 0, err
	}

	key := func(e Expense) string {
		return fmt.Sprintf("%s|%s|%s|%s|%s|%d", e.Kind, e.AsOf, e.Category, e.Note, e.Currency, e.Amount)
	}
	seen := map[string]int{}
	for _, e := range existing {
		seen[key(e)]++
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	for _, row := range rows {
		row.Kind = classifyEntry(row.Kind, row.Category)
		if k := key(row); seen[k] > 0 {
			seen[k]--
			duplicates++
			continue
		}
		_, err := tx.ExecContext(ctx, `
            INSERT INTO expenses (kind, as_of, category, note, currency, cents) VALUES (?, ?, ?, ?, ?, ?)`,
			row.Kind, row.AsOf, row.Category, row.Note, row.Currency, int64(row.Amount))
		if err != nil {
			return 0, 0, err
		}
		added++
	}
	return added, duplicates, tx.Commit()
}

// Expenses returns every entry, newest first.
func (s *Store) Expenses(ctx context.Context) ([]Expense, error) {
	var out []Expense
	err := query(ctx, s.db, `
        SELECT id, kind, as_of, category, note, currency, cents
        FROM expenses ORDER BY as_of DESC, id DESC`,
		func(scan scanner) error {
			var (
				e     Expense
				cents int64
			)
			if err := scan(&e.ID, &e.Kind, &e.AsOf, &e.Category, &e.Note, &e.Currency, &cents); err != nil {
				return err
			}
			e.Amount = money.Amount(cents)
			out = append(out, e)
			return nil
		})
	return out, err
}

// CategoryTotal is what was spent on one category within a month.
type CategoryTotal struct {
	Category string
	Total    money.Amount // in the base currency
	Share    float64      // percent of the month's total
}

// ExpenseMonth aggregates one calendar month of cash flow.
type ExpenseMonth struct {
	Month      string
	Total      money.Amount // spending, net of refunds, in the base currency
	Refunds    money.Amount // what came back, as a positive number
	Income     money.Amount
	Taxes      money.Amount
	Categories []CategoryTotal
	Expenses   []Expense // every entry of the month, newest first
}

// Saved is what was left of the month's income.
func (m ExpenseMonth) Saved() money.Amount { return m.Income - m.Total - m.Taxes }

// SavedPct is the share of income that was not spent.
func (m ExpenseMonth) SavedPct() float64 {
	if m.Income <= 0 {
		return 0
	}
	return float64(m.Saved()) / float64(m.Income) * 100
}

// ExpenseReport summarises spending across months.
type ExpenseReport struct {
	Months  []ExpenseMonth // oldest first
	Total   money.Amount
	Income  money.Amount
	Average money.Amount // spending per month with any entries
}

// Year returns the combined cash flow for one calendar year.
func (r ExpenseReport) Year(year string) ExpenseMonth {
	total := ExpenseMonth{Month: year}
	for _, month := range r.Months {
		if strings.HasPrefix(month.Month, year) {
			total.Total += month.Total
			total.Refunds += month.Refunds
			total.Income += month.Income
			total.Taxes += month.Taxes
		}
	}
	return total
}

// RecentAverage is the average monthly spending over the last n months that
// have any entries recorded.
func (r ExpenseReport) RecentAverage(n int) money.Amount {
	return r.recent(n, func(m ExpenseMonth) money.Amount { return m.Total })
}

// RecentSaved is the average monthly saving over the last n months, counting
// only months with income recorded: without it a month looks like pure loss.
func (r ExpenseReport) RecentSaved(n int) money.Amount {
	var total money.Amount
	count := 0
	for _, m := range r.lastMonths(n) {
		if m.Income > 0 {
			total += m.Saved()
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / money.Amount(count)
}

// RecentSavedMonths is how many months RecentSaved could actually use.
func (r ExpenseReport) RecentSavedMonths(n int) int {
	count := 0
	for _, m := range r.lastMonths(n) {
		if m.Income > 0 {
			count++
		}
	}
	return count
}

// RecentIncome is the average monthly income over the last n months.
func (r ExpenseReport) RecentIncome(n int) money.Amount {
	return r.recent(n, func(m ExpenseMonth) money.Amount { return m.Income })
}

func (r ExpenseReport) lastMonths(n int) []ExpenseMonth {
	if len(r.Months) > n {
		return r.Months[len(r.Months)-n:]
	}
	return r.Months
}

func (r ExpenseReport) recent(n int, of func(ExpenseMonth) money.Amount) money.Amount {
	months := r.lastMonths(n)
	if len(months) == 0 {
		return 0
	}

	var total money.Amount
	for _, m := range months {
		total += of(m)
	}
	return total / money.Amount(len(months))
}

// RecentMonths is how many months RecentAverage actually averaged over.
func (r ExpenseReport) RecentMonths(n int) int {
	return min(len(r.Months), n)
}

// CategorySummary is one category's spending across every month tracked.
type CategorySummary struct {
	Category string
	Total    money.Amount
	Share    float64        // percent of everything spent
	PerMonth money.Amount   // averaged over every month tracked, not just the ones it appears in
	Months   int            // months it appears in
	Series   []money.Amount // one entry per month of the report, oldest first
}

// ByCategory totals every category over the whole report, biggest first.
func (r ExpenseReport) ByCategory() []CategorySummary {
	if len(r.Months) == 0 {
		return nil
	}

	index := map[string]int{}
	var out []CategorySummary

	for i, month := range r.Months {
		for _, c := range month.Categories {
			at, ok := index[c.Category]
			if !ok {
				at = len(out)
				index[c.Category] = at
				out = append(out, CategorySummary{
					Category: c.Category,
					Series:   make([]money.Amount, len(r.Months)),
				})
			}
			out[at].Total += c.Total
			out[at].Series[i] = c.Total
			out[at].Months++
		}
	}

	for i := range out {
		out[i].PerMonth = out[i].Total / money.Amount(len(r.Months))
		if r.Total > 0 {
			out[i].Share = float64(out[i].Total) / float64(r.Total) * 100
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Total > out[j].Total })
	return out
}

// Month returns the requested month, or the latest one when month is empty.
func (r ExpenseReport) Find(month string) (ExpenseMonth, bool) {
	if len(r.Months) == 0 {
		return ExpenseMonth{}, false
	}
	if month == "" {
		return r.Months[len(r.Months)-1], true
	}
	for _, m := range r.Months {
		if m.Month == month {
			return m, true
		}
	}
	return ExpenseMonth{Month: month}, false
}

// Categories lists every category ever used, alphabetically.
func (r ExpenseReport) UsedCategories() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range r.Months {
		for _, e := range m.Expenses {
			if !seen[e.Category] {
				seen[e.Category] = true
				out = append(out, e.Category)
			}
		}
	}
	sort.Strings(out)
	return out
}

// BuildExpenseReport groups expenses by month, converting to the base currency
// at the given rates.
func BuildExpenseReport(expenses []Expense, rates map[string]float64) ExpenseReport {
	byMonth := map[string]*ExpenseMonth{}
	byCategory := map[string]map[string]money.Amount{}

	for _, e := range expenses {
		amount, _ := convert(e.Amount, e.Currency, rates)
		m, ok := byMonth[e.Month()]
		if !ok {
			m = &ExpenseMonth{Month: e.Month()}
			byMonth[e.Month()] = m
			byCategory[e.Month()] = map[string]money.Amount{}
		}
		m.Expenses = append(m.Expenses, e)

		if e.IsIncome() {
			m.Income += amount
			continue // income is not spending, and has no place in the categories
		}
		if classifyEntry(e.Kind, e.Category) == KindTax {
			m.Taxes += amount
			continue // taxes are tracked separately from ordinary spending
		}
		m.Total += amount
		if amount < 0 {
			m.Refunds -= amount
		}
		byCategory[e.Month()][e.Category] += amount
	}

	report := ExpenseReport{}
	for _, m := range byMonth {
		for category, total := range byCategory[m.Month] {
			share := 0.0
			if m.Total > 0 {
				share = float64(total) / float64(m.Total) * 100
			}
			m.Categories = append(m.Categories, CategoryTotal{Category: category, Total: total, Share: share})
		}
		sort.Slice(m.Categories, func(i, j int) bool { return m.Categories[i].Total > m.Categories[j].Total })
		sort.Slice(m.Expenses, func(i, j int) bool {
			if m.Expenses[i].AsOf != m.Expenses[j].AsOf {
				return m.Expenses[i].AsOf > m.Expenses[j].AsOf
			}
			return m.Expenses[i].ID > m.Expenses[j].ID
		})
		report.Months = append(report.Months, *m)
		report.Total += m.Total
		report.Income += m.Income
	}
	sort.Slice(report.Months, func(i, j int) bool { return report.Months[i].Month < report.Months[j].Month })

	if n := len(report.Months); n > 0 {
		report.Average = report.Total / money.Amount(n)
	}
	return report
}

func isTaxCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "tax", "taxes", "tax payment", "tax payments", "taxes & authorities":
		return true
	default:
		return false
	}
}

func classifyEntry(kind, category string) string {
	if kind == KindIncome {
		return kind
	}
	if kind == KindTax || isTaxCategory(category) {
		return KindTax
	}
	return KindExpense
}
