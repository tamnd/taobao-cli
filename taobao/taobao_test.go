package taobao_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tamnd/any-cli/kit/errs"
	. "github.com/tamnd/taobao-cli/taobao"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	cfg := DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	cfg.Retries = 0
	return NewClient(cfg), srv
}

func isRateLimited(err error) bool { return errs.KindOf(err) == errs.KindRateLimited }
func isNotFound(err error) bool    { return errs.KindOf(err) == errs.KindNotFound }
func isUsage(err error) bool       { return errs.KindOf(err) == errs.KindUsage }

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.BaseURL == "" {
		t.Error("BaseURL is empty")
	}
	if cfg.UserAgent == "" {
		t.Error("UserAgent is empty")
	}
	if cfg.Rate <= 0 {
		t.Error("Rate must be > 0")
	}
	if cfg.Retries <= 0 {
		t.Error("Retries must be > 0")
	}
}

func TestNewClient(t *testing.T) {
	if c := NewClient(DefaultConfig()); c == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestIsBlocked_403(t *testing.T) {
	if !IsBlocked(403, []byte("forbidden")) {
		t.Error("expected blocked on 403")
	}
}

func TestIsBlocked_JSChallenge(t *testing.T) {
	body := []byte("var x = '__jsl_clearance_s'; doSomething(x);")
	if !IsBlocked(200, body) {
		t.Error("expected blocked on __jsl_clearance_s body")
	}
}

func TestIsBlocked_Normal(t *testing.T) {
	// A body with g_page_config and > 5000 bytes is not blocked
	body := []byte("g_page_config = {}" + fmt.Sprintf("%5000s", " "))
	if IsBlocked(200, body) {
		t.Error("expected NOT blocked on normal body with g_page_config")
	}
}

const searchHTML = `<html><body><script>
g_page_config = {"mods":{"itemlist":{"data":{"auctions":[
  {"nid":"123","raw_title":"Laptop","view_price":"2999.00","view_sales":"1000+","nick":"shop1","item_loc":"上海","pic_url":"//img.alicdn.com/p1.jpg"},
  {"nid":"456","raw_title":"Headphones","view_price":"199.00","view_sales":"5万+","nick":"shop2","item_loc":"广州","pic_url":"//img.alicdn.com/p2.jpg"},
  {"nid":"789","raw_title":"Mouse","view_price":"59.00","view_sales":"200","nick":"shop3","item_loc":"北京","pic_url":"//img.alicdn.com/p3.jpg"}
]}}}}
;
</script></body></html>`

func TestSearch_OK(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(searchHTML))
	})
	defer srv.Close()

	listings, err := c.Search(context.Background(), "laptop", "sale", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(listings) != 3 {
		t.Errorf("got %d listings, want 3", len(listings))
	}
	if listings[0].ID != "123" {
		t.Errorf("listings[0].ID = %q, want 123", listings[0].ID)
	}
	if listings[0].Price != 2999.0 {
		t.Errorf("listings[0].Price = %v, want 2999", listings[0].Price)
	}
	if listings[0].Thumbnail != "https://img.alicdn.com/p1.jpg" {
		t.Errorf("Thumbnail protocol-relative not normalized: %q", listings[0].Thumbnail)
	}
}

func TestSearch_Blocked_403(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	defer srv.Close()

	_, err := c.Search(context.Background(), "laptop", "sale", 10)
	if !isRateLimited(err) {
		t.Errorf("expected RateLimited, got: %v", err)
	}
}

func TestSearch_Blocked_JSChallenge(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("var __jsl_clearance_s = 'abc';"))
	})
	defer srv.Close()

	_, err := c.Search(context.Background(), "laptop", "sale", 10)
	if !isRateLimited(err) {
		t.Errorf("expected RateLimited, got: %v", err)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(searchHTML))
	})
	defer srv.Close()

	_, err := c.Search(context.Background(), "", "sale", 10)
	if !isUsage(err) {
		t.Errorf("expected Usage error for empty query, got: %v", err)
	}
}

func TestParsePrice(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"2999.00", 2999.0},
		{"¥199.00", 199.0},
		{"1,299.00", 1299.0},
		{"", 0.0},
		{"abc", 0.0},
	}
	for _, tc := range cases {
		got := ParsePrice(tc.in)
		if got != tc.want {
			t.Errorf("ParsePrice(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseSales(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"1000+", 1000},
		{"5万+", 50000},
		{"1.2千", 1200},
		{"200", 200},
		{"", 0},
	}
	for _, tc := range cases {
		got := ParseSales(tc.in)
		if got != tc.want {
			t.Errorf("ParseSales(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseListings_OK(t *testing.T) {
	listings, err := ParseListings([]byte(searchHTML))
	if err != nil {
		t.Fatalf("ParseListings: %v", err)
	}
	if len(listings) != 3 {
		t.Errorf("got %d listings, want 3", len(listings))
	}
}
