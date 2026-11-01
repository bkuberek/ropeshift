package lanes

import (
	"strings"
	"time"
)

// ResumePath says how a lane comes back.
type ResumePath string

// The paths of a resume.
const (
	// ResumeNative continues the session the lane stopped.
	ResumeNative ResumePath = "native"
	// ResumeHandoff starts fresh from the handoff document.
	ResumeHandoff ResumePath = "handoff"
)

// ResumeRule names the row of the table that decided.
type ResumeRule string

// The rows, in order.
const (
	RuleNoNativeResume ResumeRule = "no_native_resume"
	RuleNotLocal       ResumeRule = "not_local"
	RuleDegraded       ResumeRule = "degraded"
	RuleNotCarried     ResumeRule = "not_carried"
	RuleCacheExpired   ResumeRule = "cache_expired"
	RuleOutgrown       ResumeRule = "outgrown"
	RuleCheaper        ResumeRule = "cheaper"
)

// ResumeChoice is the table's answer.
type ResumeChoice struct {
	Path ResumePath
	Rule ResumeRule
}

// ResumeFacts is what the table reads.
type ResumeFacts struct {
	// NativeResume says the driver can continue a session.
	NativeResume bool
	// Local says this worker holds the session.
	Local bool
	// Degraded says the handoff has no narrative.
	Degraded bool
	// Carries says the handoff holds what the next step needs.
	Carries bool
	// SinceResponse is how long ago the session's last model response was recorded.
	SinceResponse time.Duration
	// ResponseKnown says one was.
	ResponseKnown bool
	// CacheLifetime is the driver's documented lifetime for this sign-in.
	CacheLifetime time.Duration
	// ConversationTokens and HandoffTokens are the two sizes the ratio compares.
	ConversationTokens int64
	HandoffTokens      int64
	// Ratio is the model's one-hour write price over its cache read price. Zero skips the row.
	Ratio int64
}

// ResumeCosts is a driver's data for the table.
type ResumeCosts struct {
	// Lifetimes holds the documented cache lifetime per sign-in kind.
	Lifetimes map[string]time.Duration
	// Ratios holds the ratio per model name.
	Ratios map[string]int64
}

// Lifetime reads a sign-in's cache lifetime. An unknown sign-in has none, which reads as expired,
// because the row it decides chooses a costlier path and never a wrong one.
func (c ResumeCosts) Lifetime(signIn string) time.Duration { return c.Lifetimes[signIn] }

// Ratio reads a model's ratio, by its name or a dated form of it. A model with none skips the row.
func (c ResumeCosts) Ratio(model string) int64 {
	if ratio, ok := c.Ratios[model]; ok {
		return ratio
	}
	for name, ratio := range c.Ratios {
		if strings.HasPrefix(model, name+"-") {
			return ratio
		}
	}
	return 0
}

// cacheMarginDivisor keeps a tenth of a documented lifetime in hand (D12), which covers an expiry
// the spike bracketed and never measured.
const cacheMarginDivisor = 10

func pastCache(f ResumeFacts) bool {
	return !f.ResponseKnown || f.SinceResponse >= f.CacheLifetime-f.CacheLifetime/cacheMarginDivisor
}

func outgrown(f ResumeFacts) bool {
	return f.Ratio != 0 && f.HandoffTokens != 0 && f.ConversationTokens > f.Ratio*f.HandoffTokens
}

// ChooseResume is the resume table: the first row that matches decides (FR-PARK-6). Reachability
// comes first, then whether the handoff carries what the next step needs, and cost last.
func ChooseResume(f ResumeFacts) ResumeChoice {
	switch {
	case !f.NativeResume:
		return ResumeChoice{ResumeHandoff, RuleNoNativeResume}
	case !f.Local:
		return ResumeChoice{ResumeHandoff, RuleNotLocal}
	case f.Degraded:
		return ResumeChoice{ResumeNative, RuleDegraded}
	case !f.Carries:
		return ResumeChoice{ResumeNative, RuleNotCarried}
	case pastCache(f):
		return ResumeChoice{ResumeHandoff, RuleCacheExpired}
	case outgrown(f):
		return ResumeChoice{ResumeHandoff, RuleOutgrown}
	}
	return ResumeChoice{ResumeNative, RuleCheaper}
}

// tokenBytes is the bytes one token holds, on average, until a calibrated estimate replaces it.
const tokenBytes = 4

// EstimateTokens sizes a text from its length, because a resume has no count to read.
func EstimateTokens(size int) int64 { return int64((size + tokenBytes - 1) / tokenBytes) }

// ConversationTokens is what a session holds beyond what every generation starts with. Both paths
// carry that prefix, which raises both costs without changing the difference.
func ConversationTokens(context, baseline int64) int64 { return max(context-baseline, 0) }

// ResumePrompt is what a session is told when it comes back. A resume alone starts no turn, and
// a stopped turn is not continued, so it states what was in progress again.
func ResumePrompt(pending string) string {
	prompt := "This lane parked, and what it waited on has arrived. Your session is back as it stopped.\n"
	if pending != "" {
		prompt += "\n" + pendingNotice + "\n\n" + pending + "\n"
	}
	return prompt + "\nContinue this work order from there, and call the " + ToolLaneReport + " tool when it is done or cannot go on.\n"
}

// pendingNotice introduces what a stopped generation was doing.
const pendingNotice = "The turn in progress when it stopped did not finish, and it will not continue by itself. This instruction is still open:"
