package store

import (
	"context"
	"testing"
)

func TestImportTradesBuildsFundsAndSkipsWhatIsStored(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.CreateAccount(ctx, "Degiro", "", "", KindAsset, "CHF", ClassCash, "", nil); err != nil {
		t.Fatal(err)
	}
	ledger, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ledger.Accounts[0].ID

	trades := []BrokerTrade{
		{Name: "World ETF", Ticker: "IE00B4L5Y983", Currency: "USD", AsOf: "2025-01-03", Units: 20, Price: 10786},
		{Name: "World ETF", Ticker: "IE00B4L5Y983", Currency: "USD", AsOf: "2026-06-30", Units: -5, Price: 14228},
		{Name: "Europe ETF", Ticker: "LU0908500753", Currency: "EUR", AsOf: "2026-07-28", Units: 10, Price: 31780},
	}
	added, duplicates, err := s.ImportTrades(ctx, accountID, trades)
	if err != nil {
		t.Fatalf("ImportTrades returned error: %v", err)
	}
	if added != 3 || duplicates != 0 {
		t.Fatalf("first import: added %d, duplicates %d; want 3 and 0", added, duplicates)
	}

	ledger, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Funds) != 2 {
		t.Fatalf("got %d funds, want 2: %+v", len(ledger.Funds), ledger.Funds)
	}
	world := ledger.Funds[1]
	if world.Name != "World ETF" || world.Ticker != "IE00B4L5Y983" || world.Currency != "USD" || world.AssetClass != ClassStocks {
		t.Errorf("fund: got %+v", world)
	}
	if got := ledger.trades[world.ID]; len(got) != 2 {
		t.Fatalf("got %d trades for the world fund, want 2: %+v", len(got), got)
	}

	added, duplicates, err = s.ImportTrades(ctx, accountID, trades)
	if err != nil {
		t.Fatalf("re-import returned error: %v", err)
	}
	if added != 0 || duplicates != 3 {
		t.Fatalf("re-import: added %d, duplicates %d; want 0 and 3", added, duplicates)
	}
	ledger, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Funds) != 2 {
		t.Errorf("re-import created funds: %+v", ledger.Funds)
	}
}

func TestImportTradesKeepsTwoIdenticalTradesOfOneExport(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.CreateAccount(ctx, "Degiro", "", "", KindAsset, "CHF", ClassCash, "", nil); err != nil {
		t.Fatal(err)
	}
	ledger, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}

	same := BrokerTrade{Name: "World ETF", Ticker: "IE00B4L5Y983", Currency: "USD", AsOf: "2025-07-29", Units: 2, Price: 12068}
	added, duplicates, err := s.ImportTrades(ctx, ledger.Accounts[0].ID, []BrokerTrade{same, same})
	if err != nil {
		t.Fatalf("ImportTrades returned error: %v", err)
	}
	if added != 2 || duplicates != 0 {
		t.Fatalf("added %d, duplicates %d; want 2 and 0", added, duplicates)
	}
}

func TestImportTradesRejectsAnUnknownAccount(t *testing.T) {
	s := openTestStore(t)
	_, _, err := s.ImportTrades(context.Background(), 42, []BrokerTrade{
		{Name: "World ETF", Ticker: "IE00B4L5Y983", Currency: "USD", AsOf: "2025-01-03", Units: 20, Price: 10786},
	})
	if err == nil {
		t.Fatal("importing into a missing account should fail")
	}
}
