// Package taobao is the library behind the taobao command line:
// the HTTP client, block detection, and the typed data models for s.taobao.com.
//
// Taobao is Tier C (anti-bot). Requests from non-CN IPs are typically blocked
// by Alibaba's WAF, the __jsl_clearance_s JavaScript challenge, or an access
// restriction page. The client detects all known block patterns and returns
// errs.RateLimited so callers exit with code 5.
package taobao

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tamnd/any-cli/kit/errs"
)

// DefaultUserAgent mimics a real desktop Chrome browser on macOS.
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) " +
	"Chrome/124.0.0.0 Safari/537.36"

const (
	// Host is the primary Taobao search host.
	Host    = "s.taobao.com"
	BaseURL = "https://" + Host
)

// Config holds tunable parameters for Client.
type Config struct {
	BaseURL   string
	UserAgent string
	Rate      time.Duration
	Retries   int
	Timeout   time.Duration
}

// DefaultConfig returns production-ready defaults.
func DefaultConfig() Config {
	return Config{
		BaseURL:   BaseURL,
		UserAgent: DefaultUserAgent,
		Rate:      800 * time.Millisecond,
		Retries:   2,
		Timeout:   20 * time.Second,
	}
}

// Client is a rate-limited HTTP client for Taobao public search.
type Client struct {
	cfg  Config
	http *http.Client
	mu   sync.Mutex
	last time.Time
}

// NewClient returns a Client configured with cfg.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// get fetches url with pacing and retries.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", url, lastErr)
}

func (c *Client) do(ctx context.Context, url string) (body []byte, retry bool, err error) {
	c.pace()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Referer", "https://www.taobao.com/")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	var r io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err2 := gzip.NewReader(resp.Body)
		if err2 != nil {
			return nil, false, fmt.Errorf("gzip: %w", err2)
		}
		defer func() { _ = gz.Close() }()
		r = gz
	}

	b, err := io.ReadAll(io.LimitReader(r, 8<<20))
	if err != nil {
		return nil, true, err
	}

	if IsBlocked(resp.StatusCode, b) {
		return nil, false, errs.RateLimited("taobao: bot-blocked (status %d)", resp.StatusCode)
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	return b, false, nil
}

// IsBlocked reports whether the response signals an Alibaba WAF/bot block.
func IsBlocked(status int, body []byte) bool {
	switch status {
	case http.StatusForbidden, 405:
		return true
	}
	// Small body with no listing data: almost certainly a challenge page.
	if len(body) < 5000 && !bytes.Contains(body, []byte("g_page_config")) {
		return true
	}
	for _, sig := range []string{
		"__jsl_clearance_s",
		"ALIBABA_SECURITY",
		"aligator",
		"\xe8\xae\xbf\xe9\x97\xae\xe5\x8f\x97\xe9\x99\x90", // 访问受限
		"\xe6\x8b\xa6\xe6\x88\xaa\xe9\xa1\xb5\xe9\x9d\xa2", // 拦截页面
	} {
		if bytes.Contains(body, []byte(sig)) {
			return true
		}
	}
	return false
}

// pace blocks until at least Rate has elapsed since the last request.
func (c *Client) pace() {
	if c.cfg.Rate <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if wait := c.cfg.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

// backoff returns the wait duration for a retry attempt.
func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// normURL prepends https: to protocol-relative URLs.
func normURL(s string) string {
	if strings.HasPrefix(s, "//") {
		return "https:" + s
	}
	return s
}
