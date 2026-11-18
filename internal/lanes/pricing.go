package lanes

import (
	"strconv"
	"strings"
)

// Price is what a model charges for a million tokens of each kind, in US dollars.
type Price struct {
	Input      float64
	CacheWrite float64
	CacheRead  float64
	Output     float64
}

// Rate is how a model's calls are priced: its price, and the price of a call long enough to pay more.
type Rate struct {
	Price
	// LongAbove is the input size, in tokens, above which one call pays the long-context rate, or zero for a model with
	// none. LongInput multiplies every input-side price of such a call, and LongOutput its output price.
	LongAbove             int64
	LongInput, LongOutput float64
	// Fast says the model ID is a fast-mode deployment, whose calls carry no speed in their usage.
	Fast bool
}

// SpeedFast is the speed a call's usage names when it ran in fast mode.
const SpeedFast = "fast"

// An estimate made from these prices is never below what the model charges, because the stream it is made from is a
// bound and the turn's own total replaces it (FR-SPEND-5). The prices are Anthropic's published list prices, read on
// 2026-11-18 from
//
//	https://platform.claude.com/docs/en/about-claude/pricing      (model pricing, prompt caching, long context, fast mode,
//	                                                               web search, cloud platform pricing, data residency)
//	https://platform.claude.com/docs/en/build-with-claude/fast-mode
//	https://platform.claude.com/docs/en/build-with-claude/context-windows
//
// A web search is billed beside the tokens: $10 for 1,000 searches, counted by `usage.server_tool_use.web_search_requests`
// (the pricing page's "Web search tool" section, read on 2026-11-18, which adds that a search that fails with an error is
// not billed, and that the tokens of its results are billed as input). The count is priced as it stands, and a failed
// search is not told apart, so the estimate can be a little above the bill, which is the safe side. The fast and long-context
// multipliers are on tokens and do not apply to it.
//
// Each family is priced at the dearest model of it a session can still run, and each choice below is a bound, not a
// quotation:
//
//   - A cache write is priced at the one-hour rate, twice the input price. A five-minute write is 1.25 times, so the
//     bound holds for either.
//   - Haiku is Haiku 4.5. Haiku 5.5 charges more for a prompt over 100,000 tokens ($0.50 in, $2.50 out), which is still
//     below Haiku 4.5.
//   - Opus is Opus 4.5 and after, and an ID that names Opus 4, Opus 4.1, Claude 3 Opus, or no version at all is priced at
//     Opus 4's $15 in and $75 out. Opus 4 and 4.1 are retired on the first-party API and run on Bedrock and on Google
//     Cloud. Claude 3 Opus is not on the page now: its $15 and $75 are unverified, and kept because they are the higher.
//   - Fable and Mythos are the dearest of their versions, Fable 5 and 5.1: $10 in, $1 to read the cache, $50 out.
//   - A model not named above, and a call that names none, is priced at the highest figure of any family above in each
//     field, and at the long-context rate below.
//
// A call over 200,000 input tokens pays more on a model with a long-context tier. The page now lists none for any model
// with a one-million-token window (Sonnet 4.6 and 5, Opus 4.6 and after, Fable, Mythos), which pay standard prices for the
// whole window, and Haiku 5.5's tier is covered above. Sonnet 4 and 4.5 had a one-million-token beta that cost twice the input
// price and half again the output price above 200,000 tokens. The page no longer says so for them, and it is kept as the
// safer figure: it is unverified. Opus 4 and 4.5 have windows of 200,000, so no call of theirs can reach one.
//
// Not detectable from the stream, and so not covered: first-party US-only inference (`inference_geo: "us"`, which costs 1.1
// times on 4.6 and later) is a request parameter, and a Claude Code session sets none. A call that ran in fast mode says so
// in its usage (`speed: "fast"`), which the stream carries, and is priced at twice the standard rate on every kind of token:
// fast mode doubles the input and output prices, and the cache prices are multiples of the input price. A lane's session
// reads its user's settings, so fast mode can be on for it. On Microsoft Foundry a fast deployment has an ID of its own that
// ends in `-fast` (`claude-opus-5-5-fast`) and leaves `speed` out of the usage, because the deployment sets it (the fast-mode
// page's Microsoft Foundry table), so an ID with that suffix is priced as fast whatever its usage says. Where a client leaves
// `speed` out of the usage of a model with no such ID the estimate would price a fast call as standard, and the turn's total
// corrects it.
var (
	priceHaiku        = Price{Input: 1, CacheWrite: 2, CacheRead: 0.10, Output: 5}
	priceSonnet       = Price{Input: 3, CacheWrite: 6, CacheRead: 0.30, Output: 15}
	priceOpus         = Price{Input: 5, CacheWrite: 10, CacheRead: 0.50, Output: 25}
	priceOpusRetired  = Price{Input: 15, CacheWrite: 30, CacheRead: 1.50, Output: 75}
	priceFable        = Price{Input: 10, CacheWrite: 20, CacheRead: 1.00, Output: 50}
	priceUnknown      = dearest(priceHaiku, priceSonnet, priceOpus, priceOpusRetired, priceFable)
	longContextAbove  = int64(200_000)
	longContextInput  = 2.0
	longContextOutput = 1.5
)

const (
	tokensPerPrice = 1_000_000
	// regionalPremium is what a regional or multi-region endpoint of a cloud platform costs over its global one, on Claude
	// Sonnet 4.5, Haiku 4.5, Opus 4.5 and the models after them. A model ID in a cloud platform's shape is priced with it
	// unless it names the global endpoint, because the ID does not say which endpoint a call went to.
	regionalPremium = 1.1
	// fastFactor is what fast mode costs over standard speed.
	fastFactor = 2
	// fastSuffix ends the ID of a fast-mode deployment on Microsoft Foundry.
	fastSuffix = "-fast"
	// webSearchPrice is what one web search costs: $10 for 1,000.
	webSearchPrice = 10.0 / 1000
)

// RateOf is the rate of a model by its ID, in the shape a platform spells it: the first-party API's
// (`claude-opus-4-1-20250805`), Google Cloud's (`claude-opus-4-1@20250805`), and Bedrock's, bare, behind a regional or a
// global inference profile, or as an ARN. A model of a family it does not know, and a session that named none, are priced
// as the dearest family: a figure that is too high stops a lane early, and one that is too low misses the limit it exists
// to hold.
func RateOf(model string) Rate {
	id := strings.ToLower(model)
	family := familyOf(id)
	major, minor, versioned := generation(id, family)
	var rate Rate
	switch family {
	case "haiku":
		rate = Rate{Price: priceHaiku}
	case "sonnet":
		rate = Rate{Price: priceSonnet}
		if !versioned || (major == 4 && minor < 6) {
			rate = longContext(rate)
		}
	case "opus":
		rate = Rate{Price: priceOpusRetired}
		if versioned && !before(major, minor, 4, 5) {
			rate.Price = priceOpus
		}
	case "fable", "mythos":
		rate = Rate{Price: priceFable}
	default:
		rate = longContext(Rate{Price: priceUnknown})
	}
	// A model before 4.5 has one price on a cloud platform, and the rest pay more at a regional endpoint.
	if onCloud(id) && (!versioned || !before(major, minor, 4, 5)) {
		rate.Price = rate.scaled(regionalPremium)
	}
	rate.Fast = strings.HasSuffix(id, fastSuffix)
	return rate
}

func longContext(rate Rate) Rate {
	rate.LongAbove, rate.LongInput, rate.LongOutput = longContextAbove, longContextInput, longContextOutput
	return rate
}

// Cost is what one API call cost at this rate: its tokens, at twice the price if the call ran in fast mode, and its web
// searches, which are billed beside the tokens at a price of their own.
func (r Rate) Cost(u Usage) float64 {
	p := r.Price
	if r.LongAbove > 0 && u.Context() > r.LongAbove {
		p = Price{Input: p.Input * r.LongInput, CacheWrite: p.CacheWrite * r.LongInput, CacheRead: p.CacheRead * r.LongInput, Output: p.Output * r.LongOutput}
	}
	tokens := (float64(u.InputTokens)*p.Input + float64(u.CacheCreationTokens)*p.CacheWrite +
		float64(u.CacheReadTokens)*p.CacheRead + float64(u.OutputTokens)*p.Output) / tokensPerPrice
	if r.Fast || u.Speed == SpeedFast {
		tokens *= fastFactor
	}
	return tokens + float64(u.ServerToolUse.WebSearchRequests)*webSearchPrice
}

// CallCost is what one API call of a model cost.
func CallCost(model string, u Usage) float64 { return RateOf(model).Cost(u) }

func (p Price) scaled(factor float64) Price {
	return Price{Input: p.Input * factor, CacheWrite: p.CacheWrite * factor, CacheRead: p.CacheRead * factor, Output: p.Output * factor}
}

// dearest is the highest of each field across prices.
func dearest(prices ...Price) Price {
	var out Price
	for _, p := range prices {
		out.Input, out.CacheWrite = max(out.Input, p.Input), max(out.CacheWrite, p.CacheWrite)
		out.CacheRead, out.Output = max(out.CacheRead, p.CacheRead), max(out.Output, p.Output)
	}
	return out
}

// familyOf is the model family an ID names, or empty.
func familyOf(id string) string {
	for _, family := range []string{"haiku", "sonnet", "opus", "fable", "mythos"} {
		if strings.Contains(id, family) {
			return family
		}
	}
	return ""
}

// generation reads the version an ID names for its family. The versions follow the family in every shape but one:
// `opus-4-1-20250805`, `opus-4-1@20250805`, and `opus-4-1-20250805-v1:0` are 4.1, `opus-4-20250514` and `opus-4@20250514`
// are 4, and `opus-5-5` is 5.5. Claude 3 puts it before the family (`claude-3-opus`), and reads as 3. A run of more than two
// digits is a date, and no version.
func generation(id, family string) (major, minor int, ok bool) {
	at := strings.Index(id, family)
	if family == "" || at < 0 {
		return 0, 0, false
	}
	if strings.Contains(id[:at], "claude-3") {
		return 3, 0, true
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(id[at+len(family):], "-"), ".")
	major, rest, ok = leadingNumber(rest)
	if !ok {
		return 0, 0, false
	}
	if next := strings.TrimPrefix(strings.TrimPrefix(rest, "-"), "."); next != rest {
		if n, _, found := leadingNumber(next); found {
			minor = n
		}
	}
	return major, minor, true
}

// leadingNumber reads a version number of one or two digits.
func leadingNumber(s string) (n int, rest string, ok bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i > 2 {
		return 0, s, false
	}
	n, err := strconv.Atoi(s[:i])
	return n, s[i:], err == nil
}

// before says a version is earlier than another.
func before(major, minor, thanMajor, thanMinor int) bool {
	return major < thanMajor || (major == thanMajor && minor < thanMinor)
}

// onCloud says an ID is in the shape of a cloud platform's: Google Cloud's version after an at sign, and Bedrock's
// `anthropic.` prefix, with or without a profile before it or an ARN around it. A global endpoint costs what the
// first-party API does.
func onCloud(id string) bool {
	if strings.HasPrefix(id, "global.") || strings.Contains(id, "/global.") {
		return false
	}
	return strings.Contains(id, "@") || strings.HasPrefix(id, "anthropic.") || strings.Contains(id, ".anthropic.") || strings.HasPrefix(id, "arn:aws:bedrock")
}

// Max is each field of two usages at its larger value. An API call reports its usage more than once while it
// streams, each report a running figure of the same call, so the larger is the later. A call that is fast in either
// report is fast, and the web searches it made are the larger count, because each report counts the searches so far.
func (u Usage) Max(other Usage) Usage {
	speed := u.Speed
	if other.Speed == SpeedFast || speed == "" {
		speed = other.Speed
	}
	return Usage{
		InputTokens:         max(u.InputTokens, other.InputTokens),
		OutputTokens:        max(u.OutputTokens, other.OutputTokens),
		CacheCreationTokens: max(u.CacheCreationTokens, other.CacheCreationTokens),
		CacheReadTokens:     max(u.CacheReadTokens, other.CacheReadTokens),
		Speed:               speed,
		ServerToolUse:       ServerToolUse{WebSearchRequests: max(u.ServerToolUse.WebSearchRequests, other.ServerToolUse.WebSearchRequests)},
	}
}
