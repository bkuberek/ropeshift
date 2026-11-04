package planning_test

import (
	"testing"

	"ropeshift.dev/ropeshift/internal/planning"
)

func TestKindOfPath(t *testing.T) {
	cases := []struct {
		path string
		want planning.DocumentKind
	}{
		{"docs/requirements.md", planning.DocumentRequirements},
		{"docs/product-requirements.md", planning.DocumentRequirements},
		{"requirements/retries.md", planning.DocumentRequirements},
		{"docs/specs/retry.md", planning.DocumentSpec},
		{"spec/retry.md", planning.DocumentSpec},
		{"docs/retry-spec.md", planning.DocumentSpec},
		{"docs/specification.md", planning.DocumentSpec},
		{"design.md", planning.DocumentDesign},
		{"docs/design/retry.md", planning.DocumentDesign},
		{"docs/designs/retry.md", planning.DocumentDesign},
		{"docs/architecture.md", planning.DocumentDesign},
		{"Docs/SPECS/Retry.md", planning.DocumentSpec},
		// The nearest name decides, so a spec filed under a design folder reads as a spec.
		{"docs/design/retry-spec.md", planning.DocumentSpec},
		{"docs/specs/design.md", planning.DocumentDesign},
		{"README.md", planning.DocumentOther},
		{"docs/decisions/0001-go.md", planning.DocumentOther},
		// A word inside a word names nothing.
		{"docs/redesigned.md", planning.DocumentOther},
		{"docs/inspect.md", planning.DocumentOther},
		{"", planning.DocumentOther},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := planning.KindOfPath(c.path); got != c.want {
				t.Errorf("%q reads as %q, want %q", c.path, got, c.want)
			}
		})
	}
}
