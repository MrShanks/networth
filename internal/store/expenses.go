package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/money"
)

// Entry kinds: money going out, and money coming in.
const (
	KindExpense  = "expense"
	KindIncome   = "income"
	KindTax      = "tax"
	KindTransfer = "transfer"
)

// Expense is a single cash flow record, in its own currency. A negative amount
// on an expense is a refund, which nets off against the rest of the month.
type Expense struct {
	ID          int64
	Kind        string
	AsOf        string
	Category    string
	Subcategory string
	AccountRef  string
	Note        string
	Currency    string
	Amount      money.Amount
}

// IsRefund reports whether this entry gave money back.
func (e Expense) IsRefund() bool { return e.Kind != KindIncome && e.Amount < 0 }

// IsIncome reports whether this entry is money coming in.
func (e Expense) IsIncome() bool { return e.Kind == KindIncome }

// IsTax reports whether this entry is a tax payment kept outside spending.
func (e Expense) IsTax() bool { return e.Kind == KindTax }

func (e Expense) IsTransfer() bool { return e.Kind == KindTransfer }

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
	if kind != KindExpense && kind != KindIncome && kind != KindTax && kind != KindTransfer {
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
	if kind != KindIncome && kind != KindTransfer {
		kind = classifyEntry(KindExpense, category)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE expenses SET category = ?, kind = ? WHERE id = ?`, category, kind, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetExpenseSubcategory(ctx context.Context, id int64, subcategory string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE expenses SET subcategory = ? WHERE id = ?`,
		strings.TrimSpace(subcategory), id)
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

// DeleteAllExpenses removes every stored cash-flow entry, including transfers.
func (s *Store) DeleteAllExpenses(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM expenses`)
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
	accountRefs, err := s.accountRefs(ctx)
	if err != nil {
		return 0, 0, err
	}
	legacyKey := func(e Expense) string {
		return fmt.Sprintf("%s|%s|%s|%s|%s|%d", e.AsOf, e.Category, e.Subcategory, e.Note, e.Currency, e.Amount)
	}
	legacy := map[string][]int{}
	for i, entry := range existing {
		if entry.AccountRef == "" {
			legacy[legacyKey(entry)] = append(legacy[legacyKey(entry)], i)
		}
	}
	enriched := map[int64]string{}
	for _, row := range rows {
		if row.AccountRef == "" {
			continue
		}
		key := legacyKey(row)
		if len(legacy[key]) == 0 {
			continue
		}
		index := legacy[key][0]
		legacy[key] = legacy[key][1:]
		existing[index].AccountRef = row.AccountRef
		enriched[existing[index].ID] = row.AccountRef
	}
	matchedExisting := neutraliseInternalTransfers(existing, rows, accountRefs)

	key := func(e Expense) string {
		return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d", e.AsOf, e.Category, e.Subcategory, normalizeAccountRef(e.AccountRef), e.Note, e.Currency, e.Amount)
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
	for id, accountRef := range enriched {
		if _, err := tx.ExecContext(ctx, `UPDATE expenses SET account_ref = ? WHERE id = ?`, strings.TrimSpace(accountRef), id); err != nil {
			return 0, 0, err
		}
	}
	for id := range matchedExisting {
		if _, err := tx.ExecContext(ctx, `UPDATE expenses SET kind = ? WHERE id = ?`, KindTransfer, id); err != nil {
			return 0, 0, err
		}
	}

	for _, row := range rows {
		row.Kind = classifyEntry(row.Kind, row.Category)
		if k := key(row); seen[k] > 0 {
			seen[k]--
			duplicates++
			continue
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO expenses (kind, as_of, category, subcategory, account_ref, note, currency, cents) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			row.Kind, row.AsOf, row.Category, strings.TrimSpace(row.Subcategory), strings.TrimSpace(row.AccountRef), row.Note, row.Currency, int64(row.Amount))
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
		SELECT id, kind, as_of, category, subcategory, account_ref, note, currency, cents
        FROM expenses ORDER BY as_of DESC, id DESC`,
		func(scan scanner) error {
			var (
				e     Expense
				cents int64
			)
			if err := scan(&e.ID, &e.Kind, &e.AsOf, &e.Category, &e.Subcategory, &e.AccountRef, &e.Note, &e.Currency, &cents); err != nil {
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

type SubcategoryTotal struct {
	Category    string
	Subcategory string
	Total       money.Amount
}

// ExpenseMonth aggregates one calendar month of cash flow.
type ExpenseMonth struct {
	Month         string
	Total         money.Amount // spending, net of refunds, in the base currency
	Refunds       money.Amount // what came back, as a positive number
	Income        money.Amount
	Salary        money.Amount
	Taxes         money.Amount
	Categories    []CategoryTotal
	Subcategories []SubcategoryTotal
	Expenses      []Expense // every entry of the month, newest first
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
	byCategory := make(map[string]money.Amount)
	bySubcategory := make(map[string]map[string]money.Amount)
	for _, month := range r.Months {
		if strings.HasPrefix(month.Month, year) {
			total.Total += month.Total
			total.Refunds += month.Refunds
			total.Income += month.Income
			total.Salary += month.Salary
			total.Taxes += month.Taxes
			for _, category := range month.Categories {
				byCategory[category.Category] += category.Total
			}
			for _, subcategory := range month.Subcategories {
				if bySubcategory[subcategory.Category] == nil {
					bySubcategory[subcategory.Category] = make(map[string]money.Amount)
				}
				bySubcategory[subcategory.Category][subcategory.Subcategory] += subcategory.Total
			}
			total.Expenses = append(total.Expenses, month.Expenses...)
		}
	}
	for category, amount := range byCategory {
		row := CategoryTotal{Category: category, Total: amount}
		if total.Total != 0 {
			row.Share = float64(amount) / float64(total.Total) * 100
		}
		total.Categories = append(total.Categories, row)
	}
	for category, subcategories := range bySubcategory {
		for subcategory, amount := range subcategories {
			total.Subcategories = append(total.Subcategories, SubcategoryTotal{
				Category: category, Subcategory: subcategory, Total: amount,
			})
		}
	}
	sort.Slice(total.Categories, func(i, j int) bool {
		return total.Categories[i].Total > total.Categories[j].Total
	})
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

// SubcategorySummary is one secondary category's spending within a primary category.
type SubcategorySummary struct {
	Subcategory  string
	CurrentMonth money.Amount
	Total        money.Amount
	Share        float64
}

// BySubcategory totals secondary categories within one primary category.
func (r ExpenseReport) BySubcategory(category, currentMonth string) []SubcategorySummary {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil
	}

	totals := map[string]money.Amount{}
	currentTotals := map[string]money.Amount{}
	var categoryTotal money.Amount
	for _, month := range r.Months {
		for _, total := range month.Subcategories {
			if strings.EqualFold(total.Category, category) {
				totals[total.Subcategory] += total.Total
				if month.Month == currentMonth {
					currentTotals[total.Subcategory] += total.Total
				}
				categoryTotal += total.Total
			}
		}
	}

	out := make([]SubcategorySummary, 0, len(totals))
	for subcategory, total := range totals {
		row := SubcategorySummary{
			Subcategory:  subcategory,
			CurrentMonth: currentTotals[subcategory],
			Total:        total,
		}
		if categoryTotal != 0 {
			row.Share = float64(total) / float64(categoryTotal) * 100
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Total > out[j].Total })
	return out
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
	bySubcategory := map[string]map[string]map[string]money.Amount{}

	for _, e := range expenses {
		amount, _ := convert(e.Amount, e.Currency, rates)
		m, ok := byMonth[e.Month()]
		if !ok {
			m = &ExpenseMonth{Month: e.Month()}
			byMonth[e.Month()] = m
			byCategory[e.Month()] = map[string]money.Amount{}
			bySubcategory[e.Month()] = map[string]map[string]money.Amount{}
		}
		if e.IsTransfer() {
			continue
		}
		m.Expenses = append(m.Expenses, e)

		if e.IsIncome() {
			m.Income += amount
			if isSalaryCategory(e.Category) {
				m.Salary += amount
			}
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
		subcategory := strings.TrimSpace(e.Subcategory)
		if subcategory != "" {
			if bySubcategory[e.Month()][e.Category] == nil {
				bySubcategory[e.Month()][e.Category] = map[string]money.Amount{}
			}
			bySubcategory[e.Month()][e.Category][subcategory] += amount
		}
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
		for category, subcategories := range bySubcategory[m.Month] {
			for subcategory, total := range subcategories {
				m.Subcategories = append(m.Subcategories, SubcategoryTotal{
					Category: category, Subcategory: subcategory, Total: total,
				})
			}
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

func isSalaryCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "salary", "salary & pensions":
		return true
	default:
		return false
	}
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
	if kind == KindIncome || kind == KindTransfer {
		return kind
	}
	if kind == KindTax || isTaxCategory(category) {
		return KindTax
	}
	return KindExpense
}

func (s *Store) accountRefs(ctx context.Context) (map[string]bool, error) {
	refs := map[string]bool{}
	err := query(ctx, s.db, `SELECT bank_ref FROM accounts WHERE bank_ref <> ''`, func(scan scanner) error {
		var ref string
		if err := scan(&ref); err != nil {
			return err
		}
		refs[normalizeAccountRef(ref)] = true
		return nil
	})
	return refs, err
}

type transferReconciler interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func reconcileInternalTransfers(ctx context.Context, db transferReconciler) error {
	refs := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT bank_ref FROM accounts WHERE bank_ref <> ''`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return err
		}
		refs[normalizeAccountRef(ref)] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}

	var entries []Expense
	rows, err = db.QueryContext(ctx, `
		SELECT id, kind, as_of, category, subcategory, account_ref, note, currency, cents
		FROM expenses WHERE kind IN (?, ?) AND account_ref <> '' ORDER BY as_of, id`, KindExpense, KindIncome)
	if err != nil {
		return err
	}
	for rows.Next() {
		var entry Expense
		var cents int64
		if err := rows.Scan(&entry.ID, &entry.Kind, &entry.AsOf, &entry.Category, &entry.Subcategory,
			&entry.AccountRef, &entry.Note, &entry.Currency, &cents); err != nil {
			rows.Close()
			return err
		}
		entry.Amount = money.Amount(cents)
		entries = append(entries, entry)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	matched := neutraliseInternalTransfers(nil, entries, refs)
	for id := range matched {
		if _, err := db.ExecContext(ctx, `UPDATE expenses SET kind = ? WHERE id = ?`, KindTransfer, id); err != nil {
			return err
		}
	}
	return nil
}

func neutraliseInternalTransfers(existing, incoming []Expense, accountRefs map[string]bool) map[int64]bool {
	matchedExisting := map[int64]bool{}
	all := append(slices.Clone(existing), incoming...)
	for i := range all {
		if all[i].Kind == KindTransfer || !accountRefs[normalizeAccountRef(all[i].AccountRef)] {
			continue
		}
		for j := i + 1; j < len(all); j++ {
			if !accountRefs[normalizeAccountRef(all[j].AccountRef)] || !oppositeInternalTransfer(all[i], all[j]) {
				continue
			}
			all[i].Kind = KindTransfer
			all[j].Kind = KindTransfer
			if all[i].ID != 0 {
				matchedExisting[all[i].ID] = true
			}
			if all[j].ID != 0 {
				matchedExisting[all[j].ID] = true
			}
			break
		}
	}
	copy(incoming, all[len(existing):])
	return matchedExisting
}

func oppositeInternalTransfer(a, b Expense) bool {
	if !((a.Kind == KindIncome && b.Kind == KindExpense) || (a.Kind == KindExpense && b.Kind == KindIncome)) ||
		normalizeAccountRef(a.AccountRef) == normalizeAccountRef(b.AccountRef) || a.Currency != b.Currency || a.Amount != b.Amount {
		return false
	}
	left, leftErr := time.Parse("2006-01-02", a.AsOf)
	right, rightErr := time.Parse("2006-01-02", b.AsOf)
	if leftErr != nil || rightErr != nil {
		return false
	}
	days := left.Sub(right).Hours() / 24
	return days >= -3 && days <= 3
}
