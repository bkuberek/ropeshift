package lanes_test

import (
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
)

// nativeFacts reach the last row: every earlier row's condition is false. Each case changes
// what it needs, so a row that moved or went missing shows.
func nativeFacts() lanes.ResumeFacts {
	return lanes.ResumeFacts{
		NativeResume: true, Local: true, Carries: true, SinceResponse: time.Minute, ResponseKnown: true,
		CacheLifetime: time.Hour, ConversationTokens: 1000, HandoffTokens: 100, Ratio: 20,
	}
}

func TestTheResumeTable(t *testing.T) {
	tests := []struct {
		name string
		edit func(*lanes.ResumeFacts)
		want lanes.ResumePath
		rule lanes.ResumeRule
	}{
		{"row 1: a driver with no native resume", func(f *lanes.ResumeFacts) { f.NativeResume = false }, lanes.ResumeHandoff, lanes.RuleNoNativeResume},
		{"row 1 beats a degraded handoff", func(f *lanes.ResumeFacts) { f.NativeResume, f.Degraded = false, true }, lanes.ResumeHandoff, lanes.RuleNoNativeResume},
		{"row 2: another worker holds the session", func(f *lanes.ResumeFacts) { f.Local = false }, lanes.ResumeHandoff, lanes.RuleNotLocal},
		{"row 2 beats a degraded handoff", func(f *lanes.ResumeFacts) { f.Local, f.Degraded = false, true }, lanes.ResumeHandoff, lanes.RuleNotLocal},
		{"row 3: a degraded handoff on the machine that holds the session", func(f *lanes.ResumeFacts) { f.Degraded = true }, lanes.ResumeNative, lanes.RuleDegraded},
		{
			"row 3 beats an expired cache, because nothing else knows the work",
			func(f *lanes.ResumeFacts) { f.Degraded, f.SinceResponse = true, 5*time.Hour },
			lanes.ResumeNative, lanes.RuleDegraded,
		},
		{"row 4: a handoff that does not carry the next step", func(f *lanes.ResumeFacts) { f.Carries = false }, lanes.ResumeNative, lanes.RuleNotCarried},
		{
			"row 4 beats an expired cache",
			func(f *lanes.ResumeFacts) { f.Carries, f.SinceResponse = false, 5*time.Hour },
			lanes.ResumeNative, lanes.RuleNotCarried,
		},
		{"row 5: a response past the cache lifetime", func(f *lanes.ResumeFacts) { f.SinceResponse = 2 * time.Hour }, lanes.ResumeHandoff, lanes.RuleCacheExpired},
		{"row 5 with no response on record", func(f *lanes.ResumeFacts) { f.ResponseKnown = false }, lanes.ResumeHandoff, lanes.RuleCacheExpired},
		{"row 6: a conversation past the ratio", func(f *lanes.ResumeFacts) { f.ConversationTokens = 2001 }, lanes.ResumeHandoff, lanes.RuleOutgrown},
		{"row 7: nothing else", func(*lanes.ResumeFacts) {}, lanes.ResumeNative, lanes.RuleCheaper},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := nativeFacts()
			tt.edit(&facts)
			got := lanes.ChooseResume(facts)
			if got.Path != tt.want || got.Rule != tt.rule {
				t.Errorf("got %+v, want %s by %s", got, tt.want, tt.rule)
			}
		})
	}
}

// TestTheCacheMargin holds the clock to 10 percent below the documented lifetime (D12).
func TestTheCacheMargin(t *testing.T) {
	tests := []struct {
		name     string
		lifetime time.Duration
		since    time.Duration
		want     lanes.ResumePath
	}{
		{"a subscription, a millisecond inside", time.Hour, 54*time.Minute - time.Millisecond, lanes.ResumeNative},
		{"a subscription, at the margin", time.Hour, 54 * time.Minute, lanes.ResumeHandoff},
		{"a subscription, a millisecond past", time.Hour, 54*time.Minute + time.Millisecond, lanes.ResumeHandoff},
		{"an API key, a millisecond inside", 5 * time.Minute, 270*time.Second - time.Millisecond, lanes.ResumeNative},
		{"an API key, at the margin", 5 * time.Minute, 270 * time.Second, lanes.ResumeHandoff},
		{"a lifetime nobody documented reads as expired", 0, time.Second, lanes.ResumeHandoff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := nativeFacts()
			facts.CacheLifetime, facts.SinceResponse = tt.lifetime, tt.since
			if got := lanes.ChooseResume(facts); got.Path != tt.want {
				t.Errorf("got %+v, want %s", got, tt.want)
			}
		})
	}
}

// TestTheRatioPerModel holds the row to "more than": at the ratio exactly, a native resume is
// still no dearer. A model with no ratio skips the row.
func TestTheRatioPerModel(t *testing.T) {
	costs := lanes.ResumeCosts{Ratios: map[string]int64{"claude-haiku-5-5": 20, "claude-opus-5-5": 40, "claude-sonnet-5-5": 40}}
	tests := []struct {
		name         string
		model        string
		conversation int64
		want         lanes.ResumePath
	}{
		{"Haiku at its ratio", "claude-haiku-5-5", 2000, lanes.ResumeNative},
		{"Haiku a token past it", "claude-haiku-5-5", 2001, lanes.ResumeHandoff},
		{"Opus at its ratio", "claude-opus-5-5", 4000, lanes.ResumeNative},
		{"Opus a token past it", "claude-opus-5-5", 4001, lanes.ResumeHandoff},
		{"Sonnet at its ratio", "claude-sonnet-5-5", 4000, lanes.ResumeNative},
		{"Sonnet a token past it", "claude-sonnet-5-5", 4001, lanes.ResumeHandoff},
		{"a dated name of a known model", "claude-haiku-5-5-20261001", 2001, lanes.ResumeHandoff},
		{"a model with no ratio skips the row", "claude-fable-5-1", 900000, lanes.ResumeNative},
		{"a name that only starts alike is another model", "claude-haiku-5-50", 900000, lanes.ResumeNative},
		{"no model at all skips the row", "", 900000, lanes.ResumeNative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := nativeFacts()
			facts.Ratio, facts.ConversationTokens, facts.HandoffTokens = costs.Ratio(tt.model), tt.conversation, 100
			if got := lanes.ChooseResume(facts); got.Path != tt.want {
				t.Errorf("got %+v, want %s", got, tt.want)
			}
		})
	}
}

func TestAHandoffWithNoSizeSkipsTheRatio(t *testing.T) {
	facts := nativeFacts()
	facts.HandoffTokens, facts.ConversationTokens = 0, 900000
	if got := lanes.ChooseResume(facts); got.Path != lanes.ResumeNative {
		t.Errorf("got %+v", got)
	}
}

func TestACacheLifetimeFollowsTheSignIn(t *testing.T) {
	costs := lanes.ResumeCosts{Lifetimes: map[string]time.Duration{"subscription": time.Hour, "api_key": 5 * time.Minute}}
	if got := costs.Lifetime("api_key"); got != 5*time.Minute {
		t.Errorf("an API key lasts %s", got)
	}
	if got := costs.Lifetime("subscription"); got != time.Hour {
		t.Errorf("a subscription lasts %s", got)
	}
	if got := costs.Lifetime(""); got != 0 {
		t.Errorf("an unknown sign-in lasts %s, and it must read as expired", got)
	}
}

func TestATokenEstimateCountsAPartToken(t *testing.T) {
	for _, tt := range []struct {
		size int
		want int64
	}{{0, 0}, {1, 1}, {4, 1}, {5, 2}, {400, 100}} {
		if got := lanes.EstimateTokens(tt.size); got != tt.want {
			t.Errorf("%d bytes estimate %d", tt.size, got)
		}
	}
}

func TestAResumePromptRestatesWhatWasInProgress(t *testing.T) {
	prompt := lanes.ResumePrompt("Finish the retry budget.")
	if !strings.Contains(prompt, "Finish the retry budget.") || !strings.Contains(prompt, lanes.ToolLaneReport) {
		t.Errorf("prompt %q", prompt)
	}
	idle := lanes.ResumePrompt("")
	if strings.Contains(idle, "still open") || !strings.Contains(idle, lanes.ToolLaneReport) {
		t.Errorf("prompt %q", idle)
	}
}

func TestAFreshBriefRestatesWhatWasInProgress(t *testing.T) {
	brief := lanes.Brief(lanes.BriefSpec{WorkOrder: "wo", Title: "T", Pending: "Finish the retry budget."})
	if !strings.Contains(brief, "Finish the retry budget.") {
		t.Errorf("brief %q", brief)
	}
	if strings.Contains(lanes.Brief(lanes.BriefSpec{WorkOrder: "wo", Title: "T"}), "still open") {
		t.Error("a brief with nothing pending states something pending")
	}
}
