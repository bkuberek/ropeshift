package questions

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

func TestParseScope(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Scope
		bad  bool
	}{
		{"a plan", "plan:factory", Scope{ScopePlan, "factory"}, false},
		{"a contract", "contract:c-1", Scope{ScopeContract, "c-1"}, false},
		{"a work order", "wo:01J_wo-9", Scope{ScopeWorkOrder, "01J_wo-9"}, false},
		{"a repository", "repo:api", Scope{ScopeRepository, "api"}, false},
		{"none binds nothing", "", Scope{}, false},
		{"an unknown kind", "team:core", Scope{}, true},
		{"a bare ID", "factory", Scope{}, true},
		{"a path", "payments/retries", Scope{}, true},
		{"no ID", "plan:", Scope{}, true},
		{"an ID with a space", "plan:has space", Scope{}, true},
		{"an ID with a slash", "repo:a/b", Scope{}, true},
		{"a kind in capitals", "Plan:factory", Scope{}, true},
		{"a second prefix", "plan:wo:x", Scope{}, true},
		{"an ID too long", "plan:" + strings.Repeat("a", 200), Scope{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseScope(tt.in)
			if tt.bad {
				if !errors.Is(err, ErrBadScope) || !errors.Is(err, ErrInvalid) {
					t.Fatalf("ParseScope(%q) = %+v, %v, want ErrBadScope", tt.in, got, err)
				}
				if !strings.Contains(err.Error(), "plan:<id>") {
					t.Errorf("the refusal %q does not name the grammar", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ParseScope(%q) = %+v, %v, want %+v", tt.in, got, err, tt.want)
			}
			if got.String() != tt.in {
				t.Errorf("String() = %q, want %q", got.String(), tt.in)
			}
			if got.Zero() != (tt.in == "") {
				t.Errorf("Zero() = %v for %q", got.Zero(), tt.in)
			}
		})
	}
}

func TestScopeOfASubject(t *testing.T) {
	tests := []struct{ kind, id, want string }{
		{planning.SubjectPlan, "p1", "plan:p1"},
		{planning.SubjectContract, "c1", "contract:c1"},
		{workorders.SubjectKind, "w1", "wo:w1"},
		{workorders.SubjectKind, "", ""},
		{"", "", ""},
		{"teleport", "x", ""},
		// A record no scope can name binds nothing, so the scope the control plane builds from it is never refused.
		{planning.SubjectPlan, "2026.q4", ""},
		{planning.SubjectContract, "has space", ""},
		{workorders.SubjectKind, "wo:1", ""},
	}
	for _, tt := range tests {
		if got := ScopeOf(tt.kind, tt.id); got != tt.want {
			t.Errorf("ScopeOf(%q, %q) = %q, want %q", tt.kind, tt.id, got, tt.want)
		}
	}
}
