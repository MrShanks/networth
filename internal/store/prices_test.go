package store

import (
	"context"
	"testing"
)

func TestImportPricesLeavesTradedAndManualMarksAlone(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.CreateAccount(ctx, "Degiro", "", "", KindAsset, "CHF", ClassCash, "", nil); err != nil {
		t.Fatal(err)
	}
	ledger, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ImportTrades(ctx, ledger.Accounts[0].ID, []BrokerTrade{
		{Name: "World ETF", Ticker: "IE00B4L5Y983", Currency: "USD", AsOf: "2025-01-03", Units: 20, Price: 10786},
	}); err != nil {
		t.Fatal(err)
	}
	ledger, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fundID := ledger.Funds[0].ID

	added, err := s.ImportPrices(ctx, fundID, []PricePoint{
		{AsOf: "2025-01-03", Price: 20050}, // the day of the trade
		{AsOf: "2025-01-06", Price: 20150},
	})
	if err != nil {
		t.Fatalf("ImportPrices returned error: %v", err)
	}
	if added != 1 {
		t.Errorf("added %d prices, want 1", added)
	}

	ledger, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	marks := ledger.prices[fundID]
	if len(marks) != 2 {
		t.Fatalf("got %d marks, want 2: %+v", len(marks), marks)
	}
	if marks[0].AsOf != "2025-01-03" || marks[0].Price != 10786 {
		t.Errorf("the traded price was overwritten: %+v", marks[0])
	}
	if marks[1].Source != SourceFetched {
		t.Errorf("fetched mark = %+v, want source %q", marks[1], SourceFetched)
	}

	// A refetch may correct what it wrote before.
	if _, err := s.ImportPrices(ctx, fundID, []PricePoint{{AsOf: "2025-01-06", Price: 20200}}); err != nil {
		t.Fatal(err)
	}
	ledger, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := ledger.prices[fundID][1].Price; got != 20200 {
		t.Errorf("refetched price = %v, want 202.00", got)
	}
}
