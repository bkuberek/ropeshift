package questions

import (
	"fmt"
	"strings"
)

// The bounds of what a question holds. A member reads every field in a batch, so each one stays short enough to
// read at a glance.
const (
	// MaxFieldBytes is the longest a question's text, one option, a recommendation, or a rationale may be.
	MaxFieldBytes = 2000
	// MaxOptions is the most options a question may offer.
	MaxOptions = 20
)

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
