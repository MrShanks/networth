package fx

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRatesInvertsQuotes(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("base"); got != "CHF" {
			t.Errorf("base = %q, want CHF", got)
		}
		w.Write([]byte(`{"amount":1.0,"base":"CHF","date":"2026-08-21","rates":{"EUR":1.0692,"USD":1.2508}}`))
	}))
	defer srv.Close()

	c := NewClient("CHF", []string{"EUR", "USD"})
	c.endpoint = srv.URL

	rates, err := c.Rates(context.Background())
	if err != nil {
		t.Fatalf("Rates() returned error: %v", err)
	}
	if rates.AsOf != "2026-08-21" {
		t.Errorf("AsOf = %q, want 2026-08-21", rates.AsOf)
	}
	if got, want := rates.PerUnit["EUR"], 1/1.0692; math.Abs(got-want) > 1e-9 {
		t.Errorf("EUR = %v, want %v", got, want)
	}
	if got := rates.PerUnit["CHF"]; got != 1 {
		t.Errorf("CHF = %v, want 1", got)
	}

	if _, err := c.Rates(context.Background()); err != nil {
		t.Fatalf("second Rates() returned error: %v", err)
	}
	if calls != 1 {
		t.Errorf("fetched %d times, want 1 (cached)", calls)
	}
}

func TestRatesFallsBackToCache(t *testing.T) {
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer fail.Close()

	c := NewClient("CHF", []string{"EUR"})
	c.endpoint = fail.URL
	c.cached = &Rates{Base: "CHF", AsOf: "2026-01-01", PerUnit: map[string]float64{"EUR": 0.95}}

	rates, err := c.Rates(context.Background())
	if err == nil {
		t.Fatal("Rates() should report the failure")
	}
	if rates == nil || rates.PerUnit["EUR"] != 0.95 {
		t.Errorf("Rates() = %v, want the cached rates", rates)
	}
}
