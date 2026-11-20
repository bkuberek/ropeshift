package questions

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The bounds of what a question holds. A member reads every field in a batch, so each one stays short enough to
// read at a glance.
const (
	// MaxFieldBytes is the longest a question's text, one option, a recommendation, or a rationale may be.
	MaxFieldBytes = 2000
	// MaxOptions is the most options a question may offer.
	MaxOptions = 20
	// MaxTitleBytes is the longest a question's title may be: a few words a list shows in place of its text.
	MaxTitleBytes = 120
)

// checkShape trims a draft's title, tradeoffs, and what it unblocks, drops a tradeoff that says nothing, and refuses a
// title on more than one line or over MaxTitleBytes, a field over MaxFieldBytes, and a tradeoff of an option the
// question does not offer. It reads the options checkRecommendation tidied.
func checkShape(draft *Question) error {
	draft.Title, draft.Unblocks = strings.TrimSpace(draft.Title), strings.TrimSpace(draft.Unblocks)
	switch {
	case strings.ContainsAny(draft.Title, "\r\n"):
		return fmt.Errorf("%w: a question's title is one line", ErrInvalid)
	case len(draft.Title) > MaxTitleBytes:
		return fmt.Errorf("%w: the title is over %d bytes", ErrInvalid, MaxTitleBytes)
	}
	if err := checkField("unblocks", draft.Unblocks); err != nil {
		return err
	}
	tradeoffs, err := tidyTradeoffs(draft.Tradeoffs, draft.Options)
	if err != nil {
		return err
	}
	draft.Tradeoffs = tradeoffs
	return nil
}

// tidyTradeoffs keeps each tradeoff under its option, both trimmed, and drops one that is empty. Every one must name an
// option the question offers, as its recommendation must.
func tidyTradeoffs(tradeoffs map[string]string, options []string) (map[string]string, error) {
	if len(tradeoffs) == 0 {
		return nil, nil
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("%w: tradeoffs need the options they weigh", ErrInvalid)
	}
	kept := map[string]string{}
	for _, option := range slices.Sorted(maps.Keys(tradeoffs)) {
		name, tradeoff := strings.TrimSpace(option), strings.TrimSpace(tradeoffs[option])
		if !slices.Contains(options, name) {
			return nil, fmt.Errorf("%w: the tradeoff of %q is of no option the question offers", ErrInvalid, name)
		}
		if err := checkField("tradeoff of "+name, tradeoff); err != nil {
			return nil, err
		}
		if tradeoff != "" {
			kept[name] = tradeoff
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}
	return kept, nil
}

// TradeoffsOf is a question's tradeoffs of the options it offers now, since an escalation may replace its options.
func TradeoffsOf(q Question) map[string]string {
	kept := map[string]string{}
	for option, tradeoff := range q.Tradeoffs {
		if slices.Contains(q.Options, option) {
			kept[option] = tradeoff
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// checkField refuses a field over MaxFieldBytes, and names it.
func checkField(name, value string) error {
	if len(value) > MaxFieldBytes {
		return fmt.Errorf("%w: the %s is over %d bytes", ErrInvalid, name, MaxFieldBytes)
	}
	return nil
}

// tidyOptions trims each option and drops one that is empty once trimmed. It refuses more than MaxOptions options,
// and an option over MaxFieldBytes, naming the option by its place among those kept. A nil list stays nil.
func tidyOptions(options []string) ([]string, error) {
	if options == nil {
		return nil, nil
	}
	kept := make([]string, 0, len(options))
	for _, option := range options {
		if option = strings.TrimSpace(option); option != "" {
			kept = append(kept, option)
		}
	}
	if len(kept) > MaxOptions {
		return nil, fmt.Errorf("%w: a question offers at most %d options, and this one offers %d", ErrInvalid, MaxOptions, len(kept))
	}
	for i, option := range kept {
		if err := checkField(fmt.Sprintf("option %d", i+1), option); err != nil {
			return nil, err
		}
	}
	return kept, nil
}

// Tidy returns the escalation with its options, recommendation, and rationale trimmed and its empty options
// dropped, or refuses one that is over a bound. An escalation that states no options keeps the question's.
func (e Escalation) Tidy() (Escalation, error) {
	options, err := tidyOptions(e.Options)
	if err != nil {
		return Escalation{}, err
	}
	e.Options = options
	e.Recommendation = strings.TrimSpace(e.Recommendation)
	e.RecommendationRationale = strings.TrimSpace(e.RecommendationRationale)
	if err := checkField("recommendation", e.Recommendation); err != nil {
		return Escalation{}, err
	}
	if err := checkField("recommendation_rationale", e.RecommendationRationale); err != nil {
		return Escalation{}, err
	}
	return e, nil
}
