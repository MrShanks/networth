package importer

import (
	"strings"
	"testing"

	"github.com/MrShanks/networth/internal/money"
)

const bankExport = `"Transaction date","Account or card number","Description","Income or expense","Amount","Currency","Category"
"31.07.2026","CH66 0022 7227 1184 2740 Y","SALDO DIENSTLEISTUNGSPREISABSCHLUSS","Expense","-15.00","CHF","Fees"
"30.07.2026","****3388","PROKO                    SAN DIEGO","Expense","97.10","CHF","Household"
"28.07.2026","CH36 0022 7227 1184 2440 J","DIGITEC GALAXUS PFINGSTWEIDSTRASSE 60, 8005 ZUERICH","Expense","-88.20","CHF","Shopping"
"27.07.2026","CH36 0022 7227 1184 2440 J","STICHTING DEGIRO","Expense","-3'000.00","CHF","Savings & investments"
"27.07.2026","CH36 0022 7227 1184 2440 J","OPEN SYSTEMS AG 8045 ZUERICH","Income","7'553.10","CHF","Salary & pensions"
"22.07.2026","****3388","RISTORANTE CAMPEGGIO VOL ORBETELLO","Expense","-32.68","EUR","Restaurants & bars"
"16.07.2026","CH36 0022 7227 1184 2440 J","UBS SWITZERLAND AG","Expense","-715.50","CHF","Credit card invoice payments"
"15.07.2026","****3388","SOMETHING ABROAD","Expense","-10.00","GBP","Shopping"
"nonsense","****3388","BROKEN LINE","Expense","-1.00","CHF","Shopping"
`

func parse(t *testing.T, opts Options) Result {
	t.Helper()

	if opts.Currencies == nil {
		opts.Currencies = []string{"CHF", "EUR", "USD"}
	}
	got, err := Parse(strings.NewReader(bankExport), opts)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	return got
}

func TestParseKeepsSpendingRefundsAndIncome(t *testing.T) {
	got := parse(t, Options{})

	want := []Row{
		{Date: "2026-07-31", AccountRef: "CH66 0022 7227 1184 2740 Y", Category: "Fees", Currency: "CHF", Amount: 1500},
		{Date: "2026-07-30", AccountRef: "****3388", Category: "Household", Currency: "CHF", Amount: -9710}, // a refund
		{Date: "2026-07-28", AccountRef: "CH36 0022 7227 1184 2440 J", Category: "Shopping", Currency: "CHF", Amount: 8820},
		{Date: "2026-07-27", AccountRef: "CH36 0022 7227 1184 2440 J", Category: "Savings & investments", Currency: "CHF", Amount: 300000, Transfer: true},
		{Date: "2026-07-27", AccountRef: "CH36 0022 7227 1184 2440 J", Category: "Salary & pensions", Currency: "CHF", Amount: 755310, Income: true},
		{Date: "2026-07-22", AccountRef: "****3388", Category: "Restaurants & bars", Currency: "EUR", Amount: 3268},
		{Date: "2026-07-16", AccountRef: "CH36 0022 7227 1184 2440 J", Category: "Credit card invoice payments", Currency: "CHF", Amount: 71550, Transfer: true},
	}
	if len(got.Rows) != len(want) {
		t.Fatalf("kept %d rows, want %d: %+v", len(got.Rows), len(want), got.Rows)
	}
	for i, w := range want {
		row := got.Rows[i]
		if row.Date != w.Date || row.Category != w.Category || row.Currency != w.Currency ||
			row.Amount != w.Amount || row.Income != w.Income || row.Transfer != w.Transfer || row.AccountRef != w.AccountRef {
			t.Errorf("row %d = %+v, want %+v", i, row, w)
		}
	}

	// The description survives, commas and all.
	if note := got.Rows[2].Note; !strings.Contains(note, "8005 ZUERICH") {
		t.Errorf("description = %q, want the full text including the comma", note)
	}
}

func TestParseTreatsBareCreditsAsIncome(t *testing.T) {
	// With no "Income or expense" column the sign is all there is to go on.
	const file = "Date,Description,Amount,Currency,Category\n" +
		"05.03.2026,Mystery credit,25.00,CHF,Shopping\n"

	got, err := Parse(strings.NewReader(file), Options{Currencies: []string{"CHF"}})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("kept %d rows, want 1", len(got.Rows))
	}
	if row := got.Rows[0]; !row.Income || row.Amount != money.Amount(2500) {
		t.Errorf("row = %+v, want income of 25.00", row)
	}
}

func TestParseExplainsEverySkip(t *testing.T) {
	got := parse(t, Options{})

	reasons := map[string]string{}
	for _, s := range got.Skips {
		reasons[s.Detail] = s.Reason
	}

	cases := map[string]string{
		"SOMETHING ABROAD": "GBP is not supported",
		"BROKEN LINE":      "cannot read the date",
	}
	if len(got.Skips) != len(cases) {
		t.Fatalf("skipped %d rows, want %d: %+v", len(got.Skips), len(cases), got.Skips)
	}
	for detail, want := range cases {
		if reason, ok := reasons[detail]; !ok {
			t.Errorf("%q was not skipped", detail)
		} else if !strings.Contains(reason, want) {
			t.Errorf("%q skipped because %q, want it to mention %q", detail, reason, want)
		}
	}
}

func TestParseKeepsTransfersAsNeutralRows(t *testing.T) {
	got := parse(t, Options{})

	var found bool
	for _, row := range got.Rows {
		if row.Category == "Savings & investments" && row.Amount == money.Amount(300000) && row.Transfer {
			found = true
		}
	}
	if !found {
		t.Error("the DEGIRO transfer was not retained as neutral")
	}
}

func TestParseSemicolonFile(t *testing.T) {
	const file = "Date;Description;Amount;Currency;Category\n" +
		"05.03.2026;Coffee;-4,50;CHF;Groceries\n"

	got, err := Parse(strings.NewReader(file), Options{Currencies: []string{"CHF"}})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("kept %d rows, want 1", len(got.Rows))
	}
	if row := got.Rows[0]; row.Amount != 450 || row.Date != "2026-03-05" {
		t.Errorf("row = %+v, want 4.50 on 2026-03-05", row)
	}
}

func TestParseRejectsFilesWithoutTheBasics(t *testing.T) {
	_, err := Parse(strings.NewReader("Fruit,Colour\napple,red\n"), Options{})
	if err == nil {
		t.Error("Parse accepted a file with no date or amount column")
	}
}
