package store

import "testing"

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
