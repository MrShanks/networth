// Package prices fetches an instrument's daily closing prices from Yahoo
// Finance, which needs no API key.
package prices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/money"
)

const (
	DefaultSearchEndpoint = "https://query2.finance.yahoo.com/v1/finance/search"
	DefaultChartEndpoint  = "https://query1.finance.yahoo.com/v8/finance/chart/"

	// Yahoo turns away clients that do not look like a browser.
	userAgent = "Mozilla/5.0 (compatible; networth/1.0)"
)

// Point is one day's closing price, in the currency the listing is quoted in.
type Point struct {
	AsOf  string
	Close money.Amount
}

type Client struct {
	search string
	chart  string
	http   *http.Client
}

func NewClient(search, chart string) *Client {
	return &Client{search: search, chart: chart, http: &http.Client{Timeout: 15 * time.Second}}
}

var ErrNoListing = errors.New("no listing found in that currency")

// Symbol finds the ticker of the listing of an instrument that is quoted in
// the wanted currency, so its prices can be stored as they are.
func (c *Client) Symbol(ctx context.Context, query, currency string) (string, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("quotesCount", "10")
	q.Set("newsCount", "0")

	var body struct {
		Quotes []struct {
			Symbol string `json:"symbol"`
		} `json:"quotes"`
	}
	if err := c.get(ctx, c.search+"?"+q.Encode(), &body); err != nil {
		return "", err
	}

	for i, quote := range body.Quotes {
		if quote.Symbol == "" || i >= 5 { // a search rarely gets better further down
			break
		}
		found, err := c.currencyOf(ctx, quote.Symbol)
		if err != nil {
			continue
		}
		if strings.EqualFold(found, currency) {
			return quote.Symbol, nil
		}
	}
	return "", ErrNoListing
}

// History returns the daily closes from the given date onwards, along with the
// currency they are quoted in.
func (c *Client) History(ctx context.Context, symbol, from string) ([]Point, string, error) {
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, "", fmt.Errorf("invalid start date %q", from)
	}

	q := url.Values{}
	q.Set("period1", fmt.Sprint(start.Unix()))
	q.Set("period2", fmt.Sprint(time.Now().Add(24*time.Hour).Unix()))
	q.Set("interval", "1d")

	chart, err := c.chartOf(ctx, symbol, q)
	if err != nil {
		return nil, "", err
	}
	if len(chart.Indicators.Quote) == 0 {
		return nil, chart.Meta.Currency, nil
	}

	closes := chart.Indicators.Quote[0].Close
	out := make([]Point, 0, len(chart.Timestamp))
	for i, ts := range chart.Timestamp {
		if i >= len(closes) || closes[i] == nil || *closes[i] <= 0 {
			continue // a holiday or a day the listing did not trade
		}
		// Timestamps are the exchange's opening bell, so the day is local.
		day := time.Unix(ts+chart.Meta.GMTOffset, 0).UTC().Format("2006-01-02")
		out = append(out, Point{AsOf: day, Close: money.Amount(math.Round(*closes[i] * 100))})
	}
	return out, chart.Meta.Currency, nil
}

type chartResult struct {
	Meta struct {
		Currency  string `json:"currency"`
		Symbol    string `json:"symbol"`
		GMTOffset int64  `json:"gmtoffset"`
	} `json:"meta"`
	Timestamp  []int64 `json:"timestamp"`
	Indicators struct {
		Quote []struct {
			Close []*float64 `json:"close"`
		} `json:"quote"`
	} `json:"indicators"`
}

func (c *Client) currencyOf(ctx context.Context, symbol string) (string, error) {
	q := url.Values{}
	q.Set("range", "5d")
	q.Set("interval", "1d")
	chart, err := c.chartOf(ctx, symbol, q)
	if err != nil {
		return "", err
	}
	return chart.Meta.Currency, nil
}

func (c *Client) chartOf(ctx context.Context, symbol string, q url.Values) (*chartResult, error) {
	var body struct {
		Chart struct {
			Result []chartResult `json:"result"`
			Error  *struct {
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}
	if err := c.get(ctx, c.chart+url.PathEscape(symbol)+"?"+q.Encode(), &body); err != nil {
		return nil, err
	}
	if body.Chart.Error != nil {
		return nil, fmt.Errorf("%s: %s", symbol, body.Chart.Error.Description)
	}
	if len(body.Chart.Result) == 0 {
		return nil, fmt.Errorf("no prices for %s", symbol)
	}
	return &body.Chart.Result[0], nil
}

func (c *Client) get(ctx context.Context, target string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the price service returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("decode prices: %w", err)
	}
	return nil
}
