package questions

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestADraftRecommendsOneOfItsOptions(t *testing.T) {
	draft := Question{ID: "q", Text: "How often does a call retry?", Options: []string{"Twice", "Until the deadline"}}
	for name, tc := range map[string]struct {
		recommendation, rationale string
		options                   []string
		ok                        bool
	}{
		"an option with its reason":      {recommendation: "Twice", rationale: "It fits the deadline.", ok: true},
		"an option written with spacing": {recommendation: " Twice ", rationale: " It fits. ", ok: true},
		"an option with no reason":       {recommendation: "Twice", ok: true},
		"no recommendation":              {ok: true},
		"something that is no option":    {recommendation: "Never", rationale: "It is safest."},
		"a reason with nothing to say":   {rationale: "It fits the deadline."},
		"a recommendation with no options": {
			recommendation: "Never", options: []string{}, ok: true,
		},
		"an option and its recommendation both written with spacing": {
			recommendation: "Twice ", options: []string{"Twice "}, ok: true,
		},
		"options of which one is empty": {
			recommendation: "Twice", options: []string{"Twice", " "}, ok: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			q := draft
			q.Recommendation, q.RecommendationRationale = tc.recommendation, tc.rationale
			if tc.options != nil {
				q.Options = tc.options
			}
			got, err := New(q)
			switch {
			case tc.ok && err != nil:
				t.Fatalf("refused: %v", err)
			case !tc.ok && !errors.Is(err, ErrInvalid):
				t.Fatalf("accepted %+v, %v", got, err)
			case tc.ok && slices.ContainsFunc(got.Options, func(option string) bool { return option == "" || option != strings.TrimSpace(option) }):
				t.Errorf("recorded the options %q", got.Options)
			case tc.ok && (got.Recommendation != strings.TrimSpace(tc.recommendation) || got.RecommendationRationale != strings.TrimSpace(tc.rationale)):
				t.Errorf("recorded %q because %q", got.Recommendation, got.RecommendationRationale)
			}
		})
	}
}

func TestEscalationKeepsTheRecommendationItDoesNotReplace(t *testing.T) {
	asked := Question{
		Options: []string{"Twice", "Until the deadline"}, Recommendation: "Twice", RecommendationRationale: "It fits the deadline.",
	}
	for name, tc := range map[string]struct {
		escalation                Escalation
		recommendation, rationale string
	}{
		"nothing stated":                {Escalation{}, "Twice", "It fits the deadline."},
		"a reason only":                 {Escalation{RecommendationRationale: "The deadline allows two."}, "Twice", "The deadline allows two."},
		"its own":                       {Escalation{Recommendation: "Until the deadline", RecommendationRationale: "It is the limit."}, "Until the deadline", "It is the limit."},
		"its own with no reason":        {Escalation{Recommendation: "Until the deadline"}, "Until the deadline", ""},
		"the same options":              {Escalation{Options: []string{"Until the deadline", "Twice"}}, "Twice", "It fits the deadline."},
		"the same options with spacing": {Escalation{Options: []string{"Twice ", " Until the deadline"}}, "Twice", "It fits the deadline."},
		"other options":                 {Escalation{Options: []string{"Once", "Never"}}, "", ""},
		"other options and its own": {
			Escalation{Options: []string{"Once", "Never"}, Recommendation: "Once", RecommendationRationale: "It is cheapest."}, "Once", "It is cheapest.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			recommendation, rationale := RecommendationAfter(asked, tc.escalation)
			if recommendation != tc.recommendation || rationale != tc.rationale {
				t.Errorf("recommends %q because %q, want %q because %q", recommendation, rationale, tc.recommendation, tc.rationale)
			}
		})
	}
}

func TestAnEscalationOfAQuestionWithNoViewStatesItsOwn(t *testing.T) {
	recommendation, rationale := RecommendationAfter(Question{}, Escalation{RecommendationRationale: "No recommendation to explain."})
	if recommendation != "" || rationale != "" {
		t.Errorf("recommends %q because %q", recommendation, rationale)
	}
}
