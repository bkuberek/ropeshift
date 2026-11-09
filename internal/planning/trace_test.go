package planning_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

func TestTraceHoldsEveryCriterionToTheRequirementsOfThePlan(t *testing.T) {
	known := map[string]bool{"PAY-12": true, "PAY-13": true}
	cases := []struct {
		name     string
		criteria []planning.Criterion
		want     []string
	}{
		{"every criterion serves a requirement the plan has", []planning.Criterion{
			{ID: "c1", Requirements: []string{"PAY-12"}}, {ID: "c2", Requirements: []string{"PAY-13", "PAY-12"}},
		}, nil},
		{"a criterion that serves none", []planning.Criterion{{ID: "c1", Requirements: []string{"PAY-12"}}, {ID: "c2"}},
			[]string{"criterion c2 maps to no requirement"}},
		{"a criterion that names a requirement the plan does not have", []planning.Criterion{{ID: "c1", Requirements: []string{"PAY-99"}}},
			[]string{"criterion c1 names requirement PAY-99, which the plan does not have"}},
		{"each problem is named, in the order the criteria are written", []planning.Criterion{
			{ID: "c1"}, {ID: "c2", Requirements: []string{"PAY-12", "PAY-98", "PAY-99"}},
		}, []string{
			"criterion c1 maps to no requirement",
			"criterion c2 names requirement PAY-98, which the plan does not have",
			"criterion c2 names requirement PAY-99, which the plan does not have",
		}},
		{"a contract with no criterion has nothing to trace", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, problem := range planning.Trace(c.criteria, known) {
				got = append(got, problem.String())
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
