// Package importer reads expenses out of a bank's CSV export.
package importer

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/money"
)

// Row is one line ready to be stored. Income is positive; on an expense a
// negative amount is a refund.
type Row struct {
	Income     bool
	Transfer   bool
	Date       string
	AccountRef string
	Note       string
	Category   string
	Currency   string
	Amount     money.Amount
}

// Skip explains why a line was left out.
type Skip struct {
	Line   int
	Reason string
	Detail string
}

type Result struct {
	Rows  []Row
	Skips []Skip
}

// transfers move money around rather than spend it: the card invoice is paid
// with money already listed line by line, and investments show up as funds.
var transfers = map[string]bool{
	"credit card invoice payments": true,
	"account transfers":            true,
	"savings & investments":        true,
}

// Options tunes what is kept.
type Options struct {
	Currencies []string // currencies the app can store
}

var errNoHeader = errors.New("the file has no header row")

// Parse reads a bank export. Spending, refunds and income are all kept, so a
// month can show what went out, what came back and what was left over.
func Parse(r io.Reader, opts Options) (Result, error) {
	records, err := readRecords(r)
	if err != nil {
		return Result{}, err
	}

	columns, err := header(records[0])
	if err != nil {
		return Result{}, err
	}

	var out Result
	for i, record := range records[1:] {
		line := i + 2 // 1-based, and past the header
		row, skip := parseRow(record, columns, opts)
		switch {
		case skip != nil:
			skip.Line = line
			out.Skips = append(out.Skips, *skip)
		default:
			out.Rows = append(out.Rows, row)
		}
	}
	return out, nil
}

// columns holds where each field we need sits in a record.
type columns struct {
	date, accountRef, description, kind, amount, currency, category int
}

// readRecords reads a whole CSV, guessing its delimiter, and refuses a file
// without a header row.
func readRecords(r io.Reader) ([][]string, error) {
	buf, err := io.ReadAll(io.LimitReader(r, 8<<20))
	if err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(string(buf), "\ufeff")

	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = delimiter(text)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}
	if len(records) == 0 {
		return nil, errNoHeader
	}
	return records, nil
}

func header(record []string) (columns, error) {
	c := columns{date: -1, accountRef: -1, description: -1, kind: -1, amount: -1, currency: -1, category: -1}

	for i, name := range record {
		switch normalise(name) {
		case "transaction date", "date", "booking date":
			c.date = i
		case "account or card number", "account number", "card number":
			c.accountRef = i
		case "description", "text", "booking text":
			c.description = i
		case "income or expense", "type":
			c.kind = i
		case "amount":
			c.amount = i
		case "currency":
			c.currency = i
		case "category":
			c.category = i
		}
		// Anything else is ignored.
	}

	if c.date < 0 || c.amount < 0 {
		return c, errors.New("the file needs at least a date and an amount column")
	}
	return c, nil
}

func parseRow(record []string, c columns, opts Options) (Row, *Skip) {
	field := func(at int) string {
		if at < 0 || at >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[at])
	}

	if strings.TrimSpace(strings.Join(record, "")) == "" {
		return Row{}, &Skip{Reason: "empty line"}
	}

	description := field(c.description)

	date, err := parseDate(field(c.date))
	if err != nil {
		return Row{}, &Skip{Reason: err.Error(), Detail: description}
	}

	amount, err := parseAmount(field(c.amount))
	if err != nil {
		return Row{}, &Skip{Reason: err.Error(), Detail: description}
	}
	if amount == 0 {
		return Row{}, &Skip{Reason: "nothing moved", Detail: description}
	}

	// A positive amount on an expense line is money given back. Anything the
	// bank calls income is kept as such, so a month's saving can be worked out.
	// Without that column the sign is all there is to go on.
	kind := normalise(field(c.kind))
	income := kind == "income" || (kind == "" && amount > 0)
	if income && amount < 0 {
		return Row{}, &Skip{Reason: "income cannot be negative", Detail: description}
	}

	category := field(c.category)
	if category == "" {
		category = "Uncategorised"
	}
	transfer := transfers[normalise(category)]
	currency := strings.ToUpper(field(c.currency))
	if currency == "" && len(opts.Currencies) > 0 {
		currency = opts.Currencies[0]
	}
	if !contains(opts.Currencies, currency) {
		return Row{}, &Skip{Reason: "currency " + currency + " is not supported", Detail: description}
	}

	// Banks write what leaves the account as negative; both kinds are stored as
	// positive, leaving a negative only for a refund on an expense line.
	stored := -amount
	if income {
		stored = amount
	}

	return Row{
		Income:     income,
		Transfer:   transfer,
		Date:       date,
		AccountRef: field(c.accountRef),
		Note:       description,
		Category:   category,
		Currency:   currency,
		Amount:     stored,
	}, nil
}

// delimiter guesses between the comma and semicolon styles of CSV.
func delimiter(text string) rune {
	line, _, _ := strings.Cut(text, "\n")
	if strings.Count(line, ";") > strings.Count(line, ",") {
		return ';'
	}
	return ','
}

func parseDate(raw string) (string, error) {
	for _, layout := range []string{"02.01.2006", "2006-01-02", "02/01/2006", "01/02/2006", "02-01-2006"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("cannot read the date %q", raw)
}

// parseAmount copes with the Swiss apostrophe as a thousands separator and with
// the European habit of writing the decimals after a comma.
func parseAmount(raw string) (money.Amount, error) {
	cleaned := strings.NewReplacer("'", "", "’", "", "\u00a0", "", " ", "").Replace(raw)

	dot, comma := strings.LastIndex(cleaned, "."), strings.LastIndex(cleaned, ",")
	switch {
	case dot >= 0 && comma >= 0:
		// Whichever comes last is the decimal separator.
		if comma > dot {
			cleaned = strings.ReplaceAll(cleaned[:comma], ".", "") + "." + cleaned[comma+1:]
		}
	case comma >= 0 && len(cleaned)-comma-1 <= 2:
		cleaned = cleaned[:comma] + "." + cleaned[comma+1:]
	}

	amount, err := money.Parse(cleaned)
	if err != nil {
		return 0, fmt.Errorf("cannot read the amount %q", raw)
	}
	return amount, nil
}

func normalise(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.Trim(s, "\ufeff")))
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
