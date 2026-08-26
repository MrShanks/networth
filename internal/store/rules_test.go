package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRuleMatchesIgnoringCase(t *testing.T) {
	rule := Rule{Pattern: "IHLY THOMAS", Category: "Rent"}

	for _, note := range []string{
		"IHLY THOMAS CH KILLWANGEN 8956",
		"ihly thomas rebaeckerstrasse 6a",
		"PAYMENT TO IHLY THOMAS",
	} {
		if !rule.Matches(note) {
			t.Errorf("%q should match", note)
		}
	}
	for _, note := range []string{"THOMAS IHLY", "MIGROS", ""} {
		if rule.Matches(note) {
			t.Errorf("%q should not match", note)
		}
	}
}

func TestCategoriseUsesTheFirstMatchingRule(t *testing.T) {
	rows := []Expense{
		{Note: "IHLY THOMAS CH KILLWANGEN 8956", Category: "Other transactions"},
		{Note: "Migros M EX Sihlpassage", Category: "Groceries"},
		{Note: "IHLY THOMAS REBAECKERSTRASSE", Category: "Other transactions"},
	}
	rules := []Rule{
		{Pattern: "ihly thomas", Category: "Rent"},
		{Pattern: "thomas", Category: "Gifts"}, // also matches, but comes second
	}

	got := Categorise(rows, rules)
	if got[0].Category != "Rent" || got[2].Category != "Rent" {
		t.Errorf("categories = %q and %q, want both Rent", got[0].Category, got[2].Category)
	}
	if got[1].Category != "Groceries" {
		t.Errorf("unmatched row became %q, want it left alone", got[1].Category)
	}
}

func TestCategoriseAssignsSubcategory(t *testing.T) {
	rows := []Expense{{Note: "MIGROS CITY", Category: "Other"}}
	rules := []Rule{{Pattern: "migros", Category: "Groceries", Subcategory: "Supermarket"}}

	got := Categorise(rows, rules)
	if got[0].Category != "Groceries" || got[0].Subcategory != "Supermarket" {
		t.Fatalf("categorised expense = %+v", got[0])
	}
}

func TestRuleMatchModes(t *testing.T) {
	tests := []struct {
		name    string
		rule    Rule
		matches []string
		misses  []string
	}{
		{
			name:    "any term",
			rule:    Rule{Mode: RuleAny, Pattern: "migros\ncoop"},
			matches: []string{"MIGROS CITY", "Coop Supermarkt"},
			misses:  []string{"Aldi"},
		},
		{
			name:    "all terms",
			rule:    Rule{Mode: RuleAll, Pattern: "ihly\nthomas"},
			matches: []string{"THOMAS payment to IHLY"},
			misses:  []string{"Thomas Müller", "IHLY AG"},
		},
		{
			name:    "regular expression",
			rule:    Rule{Mode: RuleRegex, Pattern: `^(migros|coop)\b.*\d{4}$`},
			matches: []string{"MIGROS CITY 8001", "coop 3000"},
			misses:  []string{"Payment to Migros", "Coop Supermarkt"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, note := range test.matches {
				if !test.rule.Matches(note) {
					t.Errorf("%q should match", note)
				}
			}
			for _, note := range test.misses {
				if test.rule.Matches(note) {
					t.Errorf("%q should not match", note)
				}
			}
		})
	}
}

func TestAddRuleRejectsInvalidRegex(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "rules.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if err := store.AddRule(t.Context(), RuleRegex, "(", "Other"); err == nil {
		t.Fatal("invalid regex was accepted")
	}
	rules, err := store.Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules = %#v, want none", rules)
	}
}

func TestLegacyRulesMigrateToAnyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-rules.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE category_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pattern TEXT NOT NULL COLLATE NOCASE UNIQUE,
			category TEXT NOT NULL
		);
		INSERT INTO category_rules (pattern, category) VALUES ('MIGROS', 'Groceries');
	`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open migrated store: %v", err)
	}
	defer store.Close()
	rules, err := store.Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Mode != RuleAny || rules[0].Pattern != "MIGROS" {
		t.Fatalf("migrated rules = %#v", rules)
	}
}
