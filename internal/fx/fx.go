// Package fx fetches reference exchange rates from the Frankfurter API, which
// republishes the daily ECB rates and needs no API key.
package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultEndpoint = "https://api.frankfurter.dev/v1/latest"

// Rates holds how many units of the base currency one unit of each quoted
// currency is worth.
type Rates struct {
	Base    string
	AsOf    string // publication date of the reference rates
	Fetched time.Time
	PerUnit map[string]float64
}

type Client struct {
	endpoint string
	base     string
	symbols  []string
	ttl      time.Duration
	http     *http.Client

	mu     sync.Mutex
	cached *Rates
}

func NewClient(base string, symbols []string) *Client {
	return &Client{
		endpoint: defaultEndpoint,
		base:     base,
		symbols:  symbols,
		ttl:      15 * time.Minute,
		http:     &http.Client{Timeout: 8 * time.Second},
	}
}

// Cached returns the last successful result without hitting the network.
func (c *Client) Cached() *Rates {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cached
}

// Rates returns fresh rates, refetching at most once per TTL. On failure the
// last successful result is returned alongside the error.
func (c *Client) Rates(ctx context.Context) (*Rates, error) {
	c.mu.Lock()
	cached := c.cached
	fresh := cached != nil && time.Since(cached.Fetched) < c.ttl
	c.mu.Unlock()

	if fresh {
		return cached, nil
	}

	rates, err := c.fetch(ctx)
	if err != nil {
		return cached, err
	}

	c.mu.Lock()
	c.cached = rates
	c.mu.Unlock()
	return rates, nil
}

func (c *Client) fetch(ctx context.Context) (*Rates, error) {
	q := url.Values{}
	q.Set("base", c.base)
	q.Set("symbols", strings.Join(c.symbols, ","))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exchange rate service returned %s", resp.Status)
	}

	var body struct {
		Base  string             `json:"base"`
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode exchange rates: %w", err)
	}

	out := &Rates{
		Base:    c.base,
		AsOf:    body.Date,
		Fetched: time.Now(),
		PerUnit: map[string]float64{c.base: 1},
	}
	// The API quotes units per base; the app needs base per unit.
	for _, sym := range c.symbols {
		rate, ok := body.Rates[sym]
		if !ok || rate == 0 {
			return nil, fmt.Errorf("no rate for %s", sym)
		}
		out.PerUnit[sym] = 1 / rate
	}
	return out, nil
}
