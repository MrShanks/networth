package importer

import (
	"strings"
	"testing"
)

const brokerExport = `Data,Ora,Prodotto,ISIN,Borsa di riferimento,Borsa,Quantità,Quotazione,,Valore locale,,Valore CHF,Tasso di cambio,Commissione AutoFX,Costi di transazione e/o di terze parti CHF,Totale CHF,ID Ordine
28-08-2026,16:13,ISHARES CORE MSCI WORLD UCITS ETF USD (ACC),IE00B4L5Y983,SWX,XSWX,25,148.1400,USD,-3703.50,USD,-2989.48,1.2388,-7.47,-2.82,-2999.77,1cfec95c
30-06-2026,12:05,ISHARES CORE MSCI WORLD UCITS ETF USD (ACC),IE00B4L5Y983,SWX,XSWX,-435,142.2800,USD,61891.80,USD,50107.85,1.2352,-125.27,-2.78,49979.80,aacd4ffb
28-07-2026,09:38,AMUNDI CORE STOXX EUROPE 600 UCITS ETF A,LU0908500753,TDG,XGAT,10,317.8000,EUR,-3178.00,EUR,-2961.28,1.0732,-7.40,-0.93,-2969.61,99da5abc
15-07-2026,09:38,SOMETHING IN STERLING,GB00B0000000,LSE,XLON,10,10.0000,GBP,-100.00,GBP,-90.00,0.9,0,0,-90.00,deadbeef
nonsense,09:38,BROKEN LINE,IE00B4L5Y983,SWX,XSWX,1,1.0000,USD,-1.00,USD,-1.00,1,0,0,-1.00,badline
`

func TestParseTradesReadsUnitsAndPricePaid(t *testing.T) {
	got, err := ParseTrades(strings.NewReader(brokerExport), Options{Currencies: []string{"CHF", "EUR", "USD"}})
	if err != nil {
		t.Fatalf("ParseTrades returned error: %v", err)
	}

	want := []Trade{
		{Date: "2026-08-28", Product: "ISHARES CORE MSCI WORLD UCITS ETF USD (ACC)", ISIN: "IE00B4L5Y983", Units: 25, Price: 14814, Currency: "USD"},
		{Date: "2026-06-30", Product: "ISHARES CORE MSCI WORLD UCITS ETF USD (ACC)", ISIN: "IE00B4L5Y983", Units: -435, Price: 14228, Currency: "USD"},
		{Date: "2026-07-28", Product: "AMUNDI CORE STOXX EUROPE 600 UCITS ETF A", ISIN: "LU0908500753", Units: 10, Price: 31780, Currency: "EUR"},
	}
	if len(got.Trades) != len(want) {
		t.Fatalf("got %d trades, want %d: %+v", len(got.Trades), len(want), got.Trades)
	}
	for i, trade := range got.Trades {
		if trade != want[i] {
			t.Errorf("trade %d: got %+v, want %+v", i, trade, want[i])
		}
	}

	if len(got.Skips) != 2 {
		t.Fatalf("got %d skips, want 2: %+v", len(got.Skips), got.Skips)
	}
	if !strings.Contains(got.Skips[0].Reason, "GBP") {
		t.Errorf("unsupported currency skip: %+v", got.Skips[0])
	}
	if !strings.Contains(got.Skips[1].Reason, "date") {
		t.Errorf("unreadable date skip: %+v", got.Skips[1])
	}
}

func TestParseTradesReadsCommaDecimals(t *testing.T) {
	export := "Date;Product;ISIN;Quantity;Price;;Value\n" +
		"03-01-2025;A WORLD ETF;IE00B4L5Y983;20;107,8650;USD;-2157,30\n"

	got, err := ParseTrades(strings.NewReader(export), Options{Currencies: []string{"CHF", "EUR", "USD"}})
	if err != nil {
		t.Fatalf("ParseTrades returned error: %v", err)
	}
	if len(got.Trades) != 1 {
		t.Fatalf("got %d trades, want 1: %+v", len(got.Trades), got.Trades)
	}
	// 107.865 rounds to the nearest cent.
	if got.Trades[0].Price != 10787 {
		t.Errorf("price: got %d, want 10787", got.Trades[0].Price)
	}
	if got.Trades[0].Currency != "USD" {
		t.Errorf("currency: got %q, want USD", got.Trades[0].Currency)
	}
}
