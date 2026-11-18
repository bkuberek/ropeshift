package lanes

import "strings"

// Price is what a model charges for a million tokens of each kind, in US dollars.
type Price struct {
	Input      float64
	CacheWrite float64
	CacheRead  float64
	Output     float64
}

const tokensPerPrice = 1_000_000

// The list prices below are Anthropic's published rates (https://platform.claude.com/docs/en/about-claude/pricing,
// read 2026-11-17), at a five-minute cache write and with no batch, regional or fast-mode multiplier. Each family
// takes the dearest rate among its models a session can still run, so a figure estimated from them is never below
// what the model charges and is only a bound: a turn's end carries the exact total, which replaces it.
var (
	priceHaiku   = Price{Input: 1, CacheWrite: 1.25, CacheRead: 0.10, Output: 5}
	priceSonnet  = Price{Input: 3, CacheWrite: 3.75, CacheRead: 0.30, Output: 15}
	priceOpus    = Price{Input: 5, CacheWrite: 6.25, CacheRead: 0.50, Output: 25}
	priceOpus4   = Price{Input: 15, CacheWrite: 18.75, CacheRead: 1.50, Output: 75}
	priceUnknown = Price{Input: 10, CacheWrite: 12.50, CacheRead: 1.00, Output: 50}
)

// ListPrice is the list price of a model by its ID. A model of a family it does not know, and a session that named
// none, are priced as the dearest family: a figure that is too high stops a lane early, and one that is too low
// misses the limit it exists to hold.
func ListPrice(model string) Price {
	id := strings.ToLower(model)
	switch {
	case strings.Contains(id, "haiku"):
		return priceHaiku
	case strings.Contains(id, "sonnet"):
		return priceSonnet
	case strings.Contains(id, "opus"):
		// Opus 4 and 4.1 are retired from the first-party API and run on some cloud platforms at three times the rate of
		// the models after them.
		if strings.Contains(id, "opus-4-1") || strings.Contains(id, "opus-4-2") {
			return priceOpus4
		}
		return priceOpus
	}
	return priceUnknown
}

// Cost is what the tokens of one API call cost at this price.
func (p Price) Cost(u Usage) float64 {
	return (float64(u.InputTokens)*p.Input + float64(u.CacheCreationTokens)*p.CacheWrite +
		float64(u.CacheReadTokens)*p.CacheRead + float64(u.OutputTokens)*p.Output) / tokensPerPrice
}

// Max is each field of two usages at its larger value. An API call reports its usage more than once while it
// streams, each report a running figure of the same call, so the larger is the later.
func (u Usage) Max(other Usage) Usage {
	return Usage{
		InputTokens:         max(u.InputTokens, other.InputTokens),
		OutputTokens:        max(u.OutputTokens, other.OutputTokens),
		CacheCreationTokens: max(u.CacheCreationTokens, other.CacheCreationTokens),
		CacheReadTokens:     max(u.CacheReadTokens, other.CacheReadTokens),
	}
}
