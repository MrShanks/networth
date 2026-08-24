package store

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/MrShanks/networh/internal/money"
)

// Expense is a single spending record, in its own currency.
type Expense struct {
	ID       int64
	AsOf     string
	Category string
	Note     string
	Currency string
	Amount   money.Amount
}

// Month is the YYYY-MM the expense belongs to.
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

func (s *Store) AddExpense(ctx context.Context, asOf, category, note, currency string, amount money.Amount) error {
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
	if amount <= 0 {
		return errors.New("amount must be greater than zero")
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO expenses (as_of, category, note, currency, cents) VALUES (?, ?, ?, ?, ?)`,
		asOf, category, strings.TrimSpace(note), currency, int64(amount))
	return err
}

func (s *Store) DeleteExpense(ctx context.Context, id int64) error {
	return s.deleteRow(ctx, `DELETE FROM expenses WHERE id = ?`, id)
}

// Expenses returns every expense, newest first.
func (s *Store) Expenses(ctx context.Context) ([]Expense, error) {
	var out []Expense
	err := query(ctx, s.db, `
        SELECT id, as_of, category, note, currency, cents
        FROM expenses ORDER BY as_of DESC, id DESC`,
		func(scan scanner) error {
			var (
				e     Expense
				cents int64
			)
			if err := scan(&e.ID, &e.AsOf, &e.Category, &e.Note, &e.Currency, &cents); err != nil {
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

// ExpenseMonth aggregates one calendar month of spending.
type ExpenseMonth struct {
	Month      string
	Total      money.Amount // in the base currency
	Categories []CategoryTotal
	Expenses   []Expense // newest first
}

// ExpenseReport summarises spending across months.
type ExpenseReport struct {
	Months  []ExpenseMonth // oldest first
	Total   money.Amount
	Average money.Amount // per month with any spending
}

// RecentAverage is the average monthly spending over the last n months that
// have any expenses recorded.
func (r ExpenseReport) RecentAverage(n int) money.Amount {
	months := r.Months
	if len(months) > n {
		months = months[len(months)-n:]
	}
	if len(months) == 0 {
		return 0
	}

	var total money.Amount
	for _, m := range months {
		total += m.Total
	}
	return total / money.Amount(len(months))
}

// RecentMonths is how many months RecentAverage actually averaged over.
func (r ExpenseReport) RecentMonths(n int) int {
	return min(len(r.Months), n)
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
		amount := convertAt(e.Amount, e.Currency, rates)
		m, ok := byMonth[e.Month()]
		if !ok {
			m = &ExpenseMonth{Month: e.Month()}
			byMonth[e.Month()] = m
			byCategory[e.Month()] = map[string]money.Amount{}
		}
		m.Total += amount
		m.Expenses = append(m.Expenses, e)
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
	}
	sort.Slice(report.Months, func(i, j int) bool { return report.Months[i].Month < report.Months[j].Month })

	if n := len(report.Months); n > 0 {
		report.Average = report.Total / money.Amount(n)
	}
	return report
}

func convertAt(a money.Amount, currency string, rates map[string]float64) money.Amount {
	rate := rates[currency]
	if rate <= 0 || rate == 1 {
		return a
	}
	return money.Amount(math.Round(float64(a) * rate))
}
