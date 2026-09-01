package prices

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stub answers like the price service does: a search that lists candidate
// listings, and a chart per symbol.
func stub(t *testing.T, charts map[string]string, quotes ...string) *Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search") {
			var list []string
			for _, symbol := range quotes {
				list = append(list, `{"symbol":"`+symbol+`"}`)
			}
			w.Write([]byte(`{"quotes":[` + strings.Join(list, ",") + `]}`))
			return
		}
		body, ok := charts[strings.TrimPrefix(r.URL.Path, "/chart/")]
		if !ok {
			w.Write([]byte(`{"chart":{"result":[],"error":{"description":"No data found"}}}`))
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return NewClient(server.URL+"/search", server.URL+"/chart/")
}

func chart(currency string, offset int, timestamps, closes string) string {
	return fmt.Sprintf(`{"chart":{"result":[{"meta":{"currency":%q,"gmtoffset":%d},
		"timestamp":[%s],"indicators":{"quote":[{"close":[%s]}]}}]}}`,
		currency, offset, timestamps, closes)
}

func TestSymbolPicksTheListingInTheWantedCurrency(t *testing.T) {
	client := stub(t, map[string]string{
		"XG7S.DE": chart("EUR", 0, "1735862400", "215.72"),
		"IWDA.L":  chart("USD", 0, "1735862400", "107.86"),
	}, "XG7S.DE", "IWDA.L")

	got, err := client.Symbol(context.Background(), "IE00B4L5Y983", "USD")
	if err != nil {
		t.Fatalf("Symbol returned error: %v", err)
	}
	if got != "IWDA.L" {
		t.Errorf("got %q, want IWDA.L", got)
	}

	if _, err := client.Symbol(context.Background(), "IE00B4L5Y983", "CHF"); err != ErrNoListing {
		t.Errorf("a currency nothing is quoted in returned %v, want ErrNoListing", err)
	}
}

func TestHistoryReadsClosesAndSkipsDaysWithoutOne(t *testing.T) {
	client := stub(t, map[string]string{
		// 2025-01-03, 2025-01-06 (a holiday in between), 2025-01-07.
		"IWDA.L": chart("USD", 0, "1735862400,1736121600,1736208000", "107.86,null,109.505"),
	})

	points, currency, err := client.History(context.Background(), "IWDA.L", "2025-01-01")
	if err != nil {
		t.Fatalf("History returned error: %v", err)
	}
	if currency != "USD" {
		t.Errorf("currency = %q, want USD", currency)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2: %+v", len(points), points)
	}
	if points[0].AsOf != "2025-01-03" || points[0].Close != 10786 {
		t.Errorf("first point = %+v", points[0])
	}
	// A half cent rounds to the nearest one.
	if points[1].AsOf != "2025-01-07" || points[1].Close != 10951 {
		t.Errorf("last point = %+v", points[1])
	}
}

func TestHistoryReportsAnUnknownSymbol(t *testing.T) {
	client := stub(t, map[string]string{})
	if _, _, err := client.History(context.Background(), "NOPE", "2025-01-01"); err == nil {
		t.Fatal("an unknown symbol should fail")
	}
}
