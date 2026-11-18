package lanes_test

import (
	"encoding/json"
	"math"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

func near(got, want float64) bool { return math.Abs(got-want) <= 1e-9*math.Max(1, math.Abs(want)) }

// The model IDs a lane can run on, in the shapes each platform spells them: the first-party API's, a Vertex AI ID with its
// version after an at sign, and a Bedrock ID, bare, behind a regional or a global inference profile, and as an ARN.
var knownModels = []string{
	"claude-haiku-4-5-20251001", "claude-haiku-4-5@20251001", "anthropic.claude-haiku-4-5-20251001-v1:0",
	"claude-sonnet-4-20250514", "claude-sonnet-4-5-20250929", "claude-sonnet-4-6", "claude-sonnet-5", "claude-sonnet-5-5",
	"claude-opus-4-20250514", "claude-opus-4@20250514", "claude-opus-4-1-20250805", "claude-opus-4-5-20251101", "claude-opus-4-8",
	"claude-opus-5", "claude-opus-5-5", "claude-3-opus-20240229", "claude-fable-5-2", "claude-mythos-5-2",
}

// Cache writes are priced at the one-hour rate, twice the input price, which is the most a write can cost: a client that
// writes the five-minute cache pays 1.25 times and never more than this.
func TestACacheWriteIsPricedAtTheHourlyRate(t *testing.T) {
	for _, model := range knownModels {
		if rate := lanes.RateOf(model); !near(rate.CacheWrite, 2*rate.Input) {
			t.Errorf("%s writes its cache at %v for an input price of %v, want twice that", model, rate.CacheWrite, rate.Input)
		}
	}
}

// A model nobody knows, and a session that named none, are priced at least as high as every model that is known, in every
// field, so an estimate of one is never the cheapest guess (Opus 4's $15 and $75 are above the dearest current family).
func TestAnUnknownModelIsPricedAtLeastAsHighAsEveryKnownOne(t *testing.T) {
	for _, unknown := range []string{"", "something-new", "claude-quartz-1"} {
		got := lanes.RateOf(unknown)
		for _, model := range knownModels {
			known := lanes.RateOf(model)
			if got.Input < known.Input || got.CacheWrite < known.CacheWrite || got.CacheRead < known.CacheRead || got.Output < known.Output {
				t.Errorf("%q is priced %+v, which is below %s at %+v", unknown, got.Price, model, known.Price)
			}
		}
		if got.LongAbove == 0 {
			t.Errorf("%q has no long-context tier, and the cheapest guess for a request over 200,000 tokens is the standard price", unknown)
		}
	}
}

// Opus 4 and 4.1 cost three times what the Opus models after them do, and an ID that names one of them says so in the
// shape its platform uses: a date after the version, an at sign, a Bedrock prefix, an ARN, or the Claude 3 order. An ID
// that names no version is priced as the dearest Opus, and none after 4.5 is.
func TestOpusFourIsPricedByFamilyInEveryIDShape(t *testing.T) {
	const retired, current = 75.0, 25.0
	for _, tt := range []struct {
		model string
		want  float64
	}{
		{"claude-opus-4-20250514", retired},
		{"claude-opus-4-1-20250805", retired},
		{"claude-opus-4-1", retired},
		{"claude-opus-4@20250514", retired},
		{"claude-opus-4-1@20250805", retired},
		{"anthropic.claude-opus-4-20250514-v1:0", retired},
		{"anthropic.claude-opus-4-1-20250805-v1:0", retired},
		{"us.anthropic.claude-opus-4-1-20250805-v1:0", retired},
		{"arn:aws:bedrock:us-east-1:111111111111:inference-profile/us.anthropic.claude-opus-4-20250514-v1:0", retired},
		{"claude-3-opus-20240229", retired},
		{"opus", retired},
		{"claude-opus", retired},
		{"claude-opus-4-5-20251101", current},
		{"claude-opus-4-5", current},
		{"claude-opus-4-6", current},
		{"claude-opus-4-8", current},
		{"claude-opus-5", current},
		{"claude-opus-5-5", current},
		{"global.anthropic.claude-opus-4-5-20251101-v1:0", current},
		{"us.anthropic.claude-opus-4-5-20251101-v1:0", current * 1.1},
		{"claude-opus-4-5@20251101", current * 1.1},
	} {
		if got := lanes.RateOf(tt.model).Output; !near(got, tt.want) {
			t.Errorf("%s is priced at %v out, want %v", tt.model, got, tt.want)
		}
	}
}

// The other families, by their dearest model a session can still run, and the dearest family for any model of Fable or Mythos.
func TestListPricesByFamily(t *testing.T) {
	for model, want := range map[string]lanes.Price{
		"claude-haiku-4-5-20251001": {Input: 1, CacheWrite: 2, CacheRead: 0.10, Output: 5},
		"claude-sonnet-4-5":         {Input: 3, CacheWrite: 6, CacheRead: 0.30, Output: 15},
		"claude-sonnet-5-5":         {Input: 3, CacheWrite: 6, CacheRead: 0.30, Output: 15},
		"claude-opus-4-8":           {Input: 5, CacheWrite: 10, CacheRead: 0.50, Output: 25},
		"claude-fable-5-1":          {Input: 10, CacheWrite: 20, CacheRead: 1, Output: 50},
		"claude-mythos-5-2":         {Input: 10, CacheWrite: 20, CacheRead: 1, Output: 50},
	} {
		if got := lanes.RateOf(model).Price; got != want {
			t.Errorf("%s is priced %+v, want %+v", model, got, want)
		}
	}
}

// A call over 200,000 input tokens pays the long-context rate on a model that has one: the first Sonnet 4 and 4.5, whose
// million-token window was a beta that priced above standard, and an unknown model. The models after them include the
// whole window at standard prices, so a call that long costs what its tokens do.
func TestALongCallPaysTheLongContextRateOnlyWhereOneExists(t *testing.T) {
	long := lanes.Usage{CacheReadTokens: 250_000, OutputTokens: 1_000}
	short := lanes.Usage{CacheReadTokens: 150_000, OutputTokens: 1_000}
	for _, tt := range []struct {
		model       string
		long, short float64
	}{
		// $0.30 a million to read the cache and $15 out, with the input side doubled and the output half again over 200,000.
		{"claude-sonnet-4-20250514", (250_000*0.60 + 1_000*22.5) / 1e6, (150_000*0.30 + 1_000*15) / 1e6},
		{"claude-sonnet-4-5-20250929", (250_000*0.60 + 1_000*22.5) / 1e6, (150_000*0.30 + 1_000*15) / 1e6},
		{"claude-sonnet-4-6", (250_000*0.30 + 1_000*15) / 1e6, (150_000*0.30 + 1_000*15) / 1e6},
		{"claude-opus-4-6", (250_000*0.50 + 1_000*25) / 1e6, (150_000*0.50 + 1_000*25) / 1e6},
		{"claude-opus-5-5", (250_000*0.50 + 1_000*25) / 1e6, (150_000*0.50 + 1_000*25) / 1e6},
		{"claude-quartz-1", (250_000*1.5*2 + 1_000*75*1.5) / 1e6, (150_000*1.5 + 1_000*75) / 1e6},
	} {
		if got := lanes.CallCost(tt.model, long); !near(got, tt.long) {
			t.Errorf("%s: a call of 250,000 tokens in costs %v, want %v", tt.model, got, tt.long)
		}
		if got := lanes.CallCost(tt.model, short); !near(got, tt.short) {
			t.Errorf("%s: a call of 150,000 tokens in costs %v, want %v", tt.model, got, tt.short)
		}
	}
}

// A call that ran in fast mode costs twice the standard price, on every kind of token, because the cache prices are
// multiples of the input price that fast mode doubles.
func TestAFastCallCostsTwiceTheStandardPrice(t *testing.T) {
	standard := lanes.Usage{InputTokens: 10, CacheCreationTokens: 2_000, CacheReadTokens: 40_000, OutputTokens: 3_000}
	fast := standard
	fast.Speed = lanes.SpeedFast
	for _, model := range []string{"claude-opus-5-5", "claude-opus-4-8", "claude-fable-5-2", ""} {
		if got, want := lanes.CallCost(model, fast), 2*lanes.CallCost(model, standard); !near(got, want) {
			t.Errorf("%q: a fast call costs %v, want twice the standard %v", model, got, want)
		}
	}
	standard.Speed = "standard"
	if got, want := lanes.CallCost("claude-opus-5-5", standard), lanes.CallCost("claude-opus-5-5", lanes.Usage{InputTokens: 10, CacheCreationTokens: 2_000, CacheReadTokens: 40_000, OutputTokens: 3_000}); !near(got, want) {
		t.Errorf("a call that says it ran at standard speed costs %v, want %v", got, want)
	}
}

// A Microsoft Foundry deployment of a fast-mode model has an ID that ends in `-fast`, and its calls carry no speed in their usage,
// because the deployment sets it. A call to one costs twice the standard price, once: a usage that also says fast does not make
// it four times, and a model that merely has `fast` in its name is not one.
func TestAFastSuffixedModelIDIsPricedAtTwiceItsStandardRate(t *testing.T) {
	usage := lanes.Usage{InputTokens: 10, CacheCreationTokens: 2_000, CacheReadTokens: 40_000, OutputTokens: 3_000}
	for _, tt := range []struct{ fast, standard string }{
		{"claude-opus-5-5-fast", "claude-opus-5-5"},
		{"claude-fable-5-2-fast", "claude-fable-5-2"},
		{"claude-mythos-5-2-fast", "claude-mythos-5-2"},
		{"Claude-Opus-5-5-Fast", "claude-opus-5-5"},
	} {
		if got, want := lanes.CallCost(tt.fast, usage), 2*lanes.CallCost(tt.standard, usage); !near(got, want) {
			t.Errorf("%s costs %v, want twice %s's %v", tt.fast, got, tt.standard, want)
		}
		if got, want := lanes.RateOf(tt.fast).Price, lanes.RateOf(tt.standard).Price; got != want {
			t.Errorf("%s is priced %+v apart from its speed, want %s's %+v", tt.fast, got, tt.standard, want)
		}
	}
	both := usage
	both.Speed = lanes.SpeedFast
	if got, want := lanes.CallCost("claude-opus-5-5-fast", both), 2*lanes.CallCost("claude-opus-5-5", usage); !near(got, want) {
		t.Errorf("a fast ID whose usage also says fast costs %v, want twice the standard %v and no more", got, want)
	}
	for _, model := range []string{"claude-opus-5-5", "claude-opus-5-5-fast-preview", "fastapi-opus-5-5", "claude-opus-5-5-faster"} {
		if got, want := lanes.CallCost(model, usage), lanes.CallCost("claude-opus-5-5", usage); !near(got, want) {
			t.Errorf("%s costs %v, want the standard %v", model, got, want)
		}
	}
}

// A web search is billed at $10 for 1,000, beside the tokens of the call that made it, from the count its usage carries. It is
// not a token price, so neither fast mode nor a long call changes it, and a call that made none is not charged for one.
func TestAWebSearchCostsAHundredthOfADollar(t *testing.T) {
	var usage lanes.Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":105,"output_tokens":6039,"cache_read_input_tokens":7123,"cache_creation_input_tokens":7345,"server_tool_use":{"web_search_requests":3},"service_tier":"standard"}`), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.ServerToolUse.WebSearchRequests != 3 {
		t.Fatalf("the usage of a call that searched three times reads %+v", usage)
	}
	without := usage
	without.ServerToolUse = lanes.ServerToolUse{}
	for _, model := range []string{"claude-sonnet-4-5", "claude-opus-5-5", "claude-opus-5-5-fast", ""} {
		if got, want := lanes.CallCost(model, usage)-lanes.CallCost(model, without), 0.03; !near(got, want) {
			t.Errorf("%q: three searches add %v, want %v", model, got, want)
		}
	}
	only := lanes.Usage{ServerToolUse: lanes.ServerToolUse{WebSearchRequests: 1000}}
	if got := lanes.CallCost("claude-opus-5-5", only); !near(got, 10) {
		t.Errorf("a thousand searches cost %v, want $10", got)
	}
	fast := without
	fast.Speed = lanes.SpeedFast
	fast.ServerToolUse.WebSearchRequests = 3
	if got, want := lanes.CallCost("claude-opus-5-5", fast), 2*lanes.CallCost("claude-opus-5-5", without)+0.03; !near(got, want) {
		t.Errorf("a fast call with three searches costs %v, want twice its tokens and the searches at their own price, %v", got, want)
	}
	// Two runs' searches add, as their tokens do.
	one, two := lanes.Usage{ServerToolUse: lanes.ServerToolUse{WebSearchRequests: 1}}, lanes.Usage{ServerToolUse: lanes.ServerToolUse{WebSearchRequests: 2}}
	if got := one.Add(two).ServerToolUse.WebSearchRequests; got != 3 {
		t.Errorf("a run that searched once and a run that searched twice searched %d times", got)
	}
	long := lanes.Usage{CacheReadTokens: 250_000, ServerToolUse: lanes.ServerToolUse{WebSearchRequests: 2}}
	if got, want := lanes.CallCost("claude-sonnet-4-5", long), (250_000*0.60)/1e6+0.02; !near(got, want) {
		t.Errorf("a long call with two searches costs %v, want the long-context tokens and the searches at their own price, %v", got, want)
	}
}

// A call's usage reported twice in a stream is a running figure of the same call, so the larger of each field is the later,
// and a call that is fast in either report is fast.
func TestUsageTakesTheLargerOfEachFieldAndFastIfEitherSaysSo(t *testing.T) {
	a := lanes.Usage{InputTokens: 5, OutputTokens: 1, CacheReadTokens: 100, ServerToolUse: lanes.ServerToolUse{WebSearchRequests: 2}}
	b := lanes.Usage{InputTokens: 3, OutputTokens: 400, CacheCreationTokens: 7, Speed: lanes.SpeedFast, ServerToolUse: lanes.ServerToolUse{WebSearchRequests: 1}}
	want := lanes.Usage{InputTokens: 5, OutputTokens: 400, CacheCreationTokens: 7, CacheReadTokens: 100, Speed: lanes.SpeedFast, ServerToolUse: lanes.ServerToolUse{WebSearchRequests: 2}}
	if got := a.Max(b); got != want {
		t.Errorf("max is %+v, want %+v", got, want)
	}
	if got := b.Max(a); got != want {
		t.Errorf("max the other way is %+v, want %+v", got, want)
	}
}
