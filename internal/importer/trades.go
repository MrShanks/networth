package importer

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/MrShanks/networth/internal/money"
)

// Trade is one line of a broker export: units bought (positive) or sold
// (negative) of an instrument, at the price paid in its own currency.
type Trade struct {
	Date     string
	Product  string
	ISIN     string
	Units    float64
	Price    money.Amount
	Currency string
}

type TradeResult struct {
	Trades []Trade
	Skips  []Skip
}

// ParseTrades reads a broker transaction export (Degiro's, in English or
// Italian) so a fund's whole history can be rebuilt from it.
func ParseTrades(r io.Reader, opts Options) (TradeResult, error) {
	records, err := readRecords(r)
	if err != nil {
		return TradeResult{}, err
	}

	columns, err := tradeHeader(records[0])
	if err != nil {
		return TradeResult{}, err
	}

	var out TradeResult
	for i, record := range records[1:] {
		trade, skip := parseTrade(record, columns, opts)
		if skip != nil {
			skip.Line = i + 2 // 1-based, and past the header
			out.Skips = append(out.Skips, *skip)
			continue
		}
		out.Trades = append(out.Trades, trade)
	}
	return out, nil
}

// tradeColumns holds where each field we need sits in a record.
type tradeColumns struct {
	date, product, isin, units, price, currency int
}

func tradeHeader(record []string) (tradeColumns, error) {
	c := tradeColumns{date: -1, product: -1, isin: -1, units: -1, price: -1, currency: -1}

	for i, name := range record {
		switch normalise(name) {
		case "data", "date", "transaction date":
			if c.date < 0 {
				c.date = i
			}
		case "prodotto", "product", "name":
			c.product = i
		case "isin":
			c.isin = i
		case "quantità", "quantita", "quantity", "number", "numero":
			c.units = i
		case "quotazione", "price", "prezzo":
			c.price = i
		case "valuta", "currency":
			c.currency = i
		}
	}

	// The broker leaves the currency column next to the price unnamed.
	if c.currency < 0 && c.price >= 0 && c.price+1 < len(record) && normalise(record[c.price+1]) == "" {
		c.currency = c.price + 1
	}
	if c.date < 0 || c.units < 0 || c.price < 0 {
		return c, errors.New("the file needs at least a date, a quantity and a price column")
	}
	if c.product < 0 && c.isin < 0 {
		return c, errors.New("the file needs a product or ISIN column")
	}
	return c, nil
}

func parseTrade(record []string, c tradeColumns, opts Options) (Trade, *Skip) {
	field := func(at int) string {
		if at < 0 || at >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[at])
	}

	if strings.TrimSpace(strings.Join(record, "")) == "" {
		return Trade{}, &Skip{Reason: "empty line"}
	}

	product := field(c.product)
	isin := strings.ToUpper(field(c.isin))
	detail := product
	if detail == "" {
		detail = isin
	}

	date, err := parseDate(field(c.date))
	if err != nil {
		return Trade{}, &Skip{Reason: err.Error(), Detail: detail}
	}

	units, err := parseUnits(field(c.units))
	if err != nil {
		return Trade{}, &Skip{Reason: err.Error(), Detail: detail}
	}
	if units == 0 {
		return Trade{}, &Skip{Reason: "no units traded", Detail: detail}
	}

	price, err := parsePrice(field(c.price))
	if err != nil {
		return Trade{}, &Skip{Reason: err.Error(), Detail: detail}
	}
	if price < 0 {
		return Trade{}, &Skip{Reason: "price cannot be negative", Detail: detail}
	}

	currency := strings.ToUpper(field(c.currency))
	if currency == "" && len(opts.Currencies) > 0 {
		currency = opts.Currencies[0]
	}
	if !contains(opts.Currencies, currency) {
		return Trade{}, &Skip{Reason: "currency " + currency + " is not supported", Detail: detail}
	}

	if product == "" {
		product = isin
	}
	return Trade{
		Date:     date,
		Product:  product,
		ISIN:     isin,
		Units:    units,
		Price:    price,
		Currency: currency,
	}, nil
}

func parseUnits(raw string) (float64, error) {
	cleaned := cleanNumber(raw)
	units, err := strconv.ParseFloat(cleaned, 64)
	if err != nil || math.IsNaN(units) || math.IsInf(units, 0) {
		return 0, fmt.Errorf("cannot read the quantity %q", raw)
	}
	return units, nil
}

// parsePrice keeps cents: brokers quote more decimals than an amount holds.
func parsePrice(raw string) (money.Amount, error) {
	cleaned := cleanNumber(raw)
	price, err := strconv.ParseFloat(cleaned, 64)
	if err != nil || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, fmt.Errorf("cannot read the price %q", raw)
	}
	return money.Amount(math.Round(price * 100)), nil
}

// cleanNumber drops thousands separators and settles on the dot as the decimal
// mark, whichever of the two European styles the export uses.
func cleanNumber(raw string) string {
	cleaned := strings.NewReplacer("'", "", "’", "", "\u00a0", "", " ", "").Replace(raw)

	dot, comma := strings.LastIndex(cleaned, "."), strings.LastIndex(cleaned, ",")
	switch {
	case dot >= 0 && comma >= 0:
		if comma > dot {
			return strings.ReplaceAll(cleaned[:comma], ".", "") + "." + cleaned[comma+1:]
		}
		return strings.ReplaceAll(cleaned[:dot], ",", "") + "." + cleaned[dot+1:]
	case comma >= 0:
		return strings.ReplaceAll(cleaned, ",", ".")
	}
	return cleaned
}
