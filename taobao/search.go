package taobao

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/tamnd/any-cli/kit/errs"
)

// Listing is one Taobao product listing record.
type Listing struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
	Sales     int64   `json:"sales"`
	Shop      string  `json:"shop"`
	Location  string  `json:"location"`
	Thumbnail string  `json:"thumbnail"`
	URL       string  `json:"url"`
}

// sortParam maps CLI sort names to Taobao URL param values.
var sortParam = map[string]string{
	"sale":       "sale-desc",
	"price-asc":  "price-asc",
	"price-desc": "price-desc",
	"new":        "default",
}

// ValidSort reports whether sort is a known sort value.
func ValidSort(sort string) bool {
	_, ok := sortParam[sort]
	return ok
}

var pageConfigRE = regexp.MustCompile(`g_page_config\s*=\s*(\{[\s\S]+?\})\s*;`)

// Search fetches Taobao search results for query.
func (c *Client) Search(ctx context.Context, query, sort string, limit int) ([]*Listing, error) {
	if query == "" {
		return nil, errs.Usage("query is required")
	}
	sp, ok := sortParam[sort]
	if !ok {
		sp = "sale-desc"
	}

	var out []*Listing
	seen := map[string]bool{}
	offset := 0

	for len(out) < limit {
		n, err := c.searchPage(ctx, query, sp, offset, &out, seen)
		if err != nil {
			if len(out) > 0 {
				break
			}
			return nil, err
		}
		if n == 0 {
			break
		}
		offset += 44
		if len(out) >= limit {
			break
		}
	}

	if len(out) == 0 {
		return nil, errs.NotFound("no results for %q", query)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (c *Client) searchPage(ctx context.Context, query, sort string, offset int, out *[]*Listing, seen map[string]bool) (int, error) {
	rawURL := fmt.Sprintf(
		"%s/search?q=%s&sort=%s&s=%d&imgfile=&js=1&stats_click=search_radio_all%%3A1&initiative_id=staobaoz_20230101&ie=utf8",
		c.cfg.BaseURL,
		url.QueryEscape(query),
		sort,
		offset,
	)

	body, err := c.get(ctx, rawURL)
	if err != nil {
		return 0, err
	}

	listings, err := parseListings(body)
	if err != nil {
		return 0, err
	}

	added := 0
	for _, l := range listings {
		if seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		*out = append(*out, l)
		added++
	}
	return added, nil
}

// parseListings extracts listings from the g_page_config JSON island.
func parseListings(body []byte) ([]*Listing, error) {
	m := pageConfigRE.FindSubmatch(body)
	if m == nil {
		return nil, errs.NotFound("no g_page_config found in response")
	}

	var root map[string]any
	if err := json.Unmarshal(m[1], &root); err != nil {
		return nil, fmt.Errorf("parse g_page_config: %w", err)
	}

	// Try primary path: mods.itemlist.data.auctions
	auctions := walkAuctions(root, "mods", "itemlist", "data", "auctions")
	if len(auctions) == 0 {
		// Fallback: mods.auctions.data.auctions
		auctions = walkAuctions(root, "mods", "auctions", "data", "auctions")
	}

	var out []*Listing
	for _, a := range auctions {
		l := parseListing(a)
		if l != nil && l.ID != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// ParseListings is exported for tests.
func ParseListings(body []byte) ([]*Listing, error) {
	return parseListings(body)
}

func walkAuctions(root map[string]any, keys ...string) []map[string]any {
	var node any = root
	for _, k := range keys {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[k]
	}
	arr, ok := node.([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, el := range arr {
		if m, ok := el.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func parseListing(raw map[string]any) *Listing {
	id := rawStr(raw, "nid")
	if id == "" {
		id = rawStr(raw, "itemid")
	}
	title := html.UnescapeString(rawStr(raw, "raw_title"))
	if title == "" {
		title = html.UnescapeString(rawStr(raw, "title"))
	}
	price := ParsePrice(rawStr(raw, "view_price"))
	if price == 0 {
		price = ParsePrice(rawStr(raw, "price"))
	}
	sales := ParseSales(rawStr(raw, "view_sales"))
	shop := rawStr(raw, "nick")
	loc := rawStr(raw, "item_loc")
	pic := normURL(rawStr(raw, "pic_url"))
	itemURL := "https://item.taobao.com/item.htm?id=" + id
	return &Listing{
		ID:        id,
		Title:     title,
		Price:     price,
		Currency:  "CNY",
		Sales:     sales,
		Shop:      shop,
		Location:  loc,
		Thumbnail: pic,
		URL:       itemURL,
	}
}

func rawStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// ParsePrice parses a price string like "2999.00", "¥199", "1,299" into float64.
func ParsePrice(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "¥", "")
	s = strings.ReplaceAll(s, "￥", "")
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimSpace(s)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

var (
	wanRE = regexp.MustCompile(`^([\d.]+)\s*万`)
	qianRE = regexp.MustCompile(`^([\d.]+)\s*千`)
	numRE  = regexp.MustCompile(`^(\d+)`)
)

// ParseSales parses sales strings like "1000+", "5万+", "1.2千" into int64.
func ParseSales(s string) int64 {
	s = strings.TrimSpace(s)
	if m := wanRE.FindStringSubmatch(s); m != nil {
		f, err := strconv.ParseFloat(m[1], 64)
		if err == nil {
			return int64(f * 10000)
		}
	}
	if m := qianRE.FindStringSubmatch(s); m != nil {
		f, err := strconv.ParseFloat(m[1], 64)
		if err == nil {
			return int64(f * 1000)
		}
	}
	if m := numRE.FindStringSubmatch(s); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil {
			return n
		}
	}
	return 0
}
