package stats

import (
	"strings"
)

// PricesUpdated is when the table below was last checked against the
// public price list.
const PricesUpdated = "2026-11-20"

// CostNote is shown wherever the estimate is (D63).
const CostNote = "est., list prices — your plan may be flat-rate"

// Price is USD per million tokens.
type Price struct {
	Input, Output, CacheWrite5m, CacheWrite1h, CacheRead float64
}

// prices are Anthropic's public first-party API list prices (USD per
// million tokens). They only feed an *estimate* of what the counted
// tokens would cost at list price: subscription plans are flat-rate, and
// batch, long-context and regional surcharges are not modelled. Cache
// writes are 1.25x (5 minutes) and 2x (1 hour) the input price; cache
// reads are per model. Tiered prices (Claude Haiku 5.5 above 100K prompt
// tokens) use the base tier. Keys are model ids without a date suffix.
var prices = map[string]Price{
	"claude-fable-5-1":  {10, 50, 12.5, 20, 0.25},
	"claude-mythos-5-1": {10, 50, 12.5, 20, 0.25},
	"claude-fable-5":    {10, 50, 12.5, 20, 1},
	"claude-mythos-5":   {10, 50, 12.5, 20, 1},
	"claude-opus-5-5":   {4, 20, 5, 8, 0.20},
	"claude-opus-5":     {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-8":   {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-7":   {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-6":   {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-5":   {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-1":   {15, 75, 18.75, 30, 1.50},
	"claude-opus-4":     {15, 75, 18.75, 30, 1.50},
	"claude-sonnet-5-5": {2, 10, 2.5, 4, 0.20},
	"claude-sonnet-5":   {2, 10, 2.5, 4, 0.20},
	"claude-sonnet-4-6": {3, 15, 3.75, 6, 0.30},
	"claude-sonnet-4-5": {3, 15, 3.75, 6, 0.30},
	"claude-sonnet-4":   {3, 15, 3.75, 6, 0.30},
	"claude-3-7-sonnet": {3, 15, 3.75, 6, 0.30},
	"claude-haiku-5-5":  {0.10, 0.50, 0.125, 0.20, 0.01},
	"claude-haiku-4-5":  {1, 5, 1.25, 2, 0.10},
	"claude-3-5-haiku":  {0.80, 4, 1, 1.6, 0.08},
}

// PriceFor finds the price of a model id: the longest table key that is
// the id itself or the id minus a "-<digits>" date suffix
// (claude-sonnet-4-5-20250929 → claude-sonnet-4-5). A "[1m]"-style
// suffix is ignored.
func PriceFor(model string) (Price, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(m, '['); i >= 0 {
		m = m[:i]
	}
	if p, ok := prices[m]; ok {
		return p, true
	}
	best, bestLen := Price{}, 0
	for k, p := range prices {
		if len(k) <= bestLen || !strings.HasPrefix(m, k+"-") {
			continue
		}
		if isDigits(m[len(k)+1:]) {
			best, bestLen = p, len(k)
		}
	}
	return best, bestLen > 0
}

func isDigits(s string) bool {
	if len(s) < 6 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// cost of one message's tokens at p.
func (p Price) cost(in, out, cw, cw1h, cr int64) float64 {
	return (float64(in)*p.Input + float64(out)*p.Output + float64(cw-cw1h)*p.CacheWrite5m +
		float64(cw1h)*p.CacheWrite1h + float64(cr)*p.CacheRead) / 1e6
}
