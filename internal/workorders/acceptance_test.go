package workorders

import (
	"slices"
	"testing"
)

func check(criterion string, source EvidenceSource, status CheckStatus) Evidence {
	return Evidence{Name: "check-" + criterion, Criterion: criterion, Source: source, Status: status}
}

func TestAssessFollowsTheAcceptanceTable(t *testing.T) {
	tests := []struct {
		name     string
		criteria []string
		evidence []Evidence
		want     []Standing
		needs    Need
		failed   int
	}{
		{
			name: "a forge result counts by itself", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed)},
			want:     []Standing{StandingCovered}, needs: NeedNothing,
		},
		{
			name: "a check only the lane reports needs a second principal", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceLane, CheckPassed)},
			want:     []Standing{StandingSecondPrincipal}, needs: NeedSecondPrincipal,
		},
		{
			name: "a criterion no check maps to needs a member", criteria: []string{"c1"},
			want: []Standing{StandingMember}, needs: NeedMember,
		},
		{
			name: "a forge result stands beside the lane's own word", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceLane, CheckPassed), check("c1", SourceForge, CheckPassed)},
			want:     []Standing{StandingCovered}, needs: NeedNothing,
		},
		{
			name: "a failed forge result fails the criterion", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceForge, CheckFailed)},
			want:     []Standing{StandingFailed}, needs: NeedMember, failed: 1,
		},
		{
			name: "a failed lane check fails the criterion", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceLane, CheckFailed)},
			want:     []Standing{StandingFailed}, needs: NeedMember, failed: 1,
		},
		{
			name: "a pass does not outweigh a failure", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed), check("c1", SourceLane, CheckFailed)},
			want:     []Standing{StandingFailed}, needs: NeedMember, failed: 1,
		},
		{
			name: "a check naming no criterion covers nothing", criteria: []string{"c1"},
			evidence: []Evidence{check("", SourceForge, CheckPassed)},
			want:     []Standing{StandingMember}, needs: NeedMember,
		},
		{
			name: "a check naming another criterion covers nothing", criteria: []string{"c1"},
			evidence: []Evidence{check("c9", SourceForge, CheckPassed)},
			want:     []Standing{StandingMember}, needs: NeedMember,
		},
		{
			name: "a failed check with no criterion still failed", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed), check("", SourceLane, CheckFailed)},
			want:     []Standing{StandingCovered}, needs: NeedMember, failed: 1,
		},
		{
			name: "the weakest evidence decides who is needed", criteria: []string{"c1", "c2"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed), check("c2", SourceLane, CheckPassed)},
			want:     []Standing{StandingCovered, StandingSecondPrincipal}, needs: NeedSecondPrincipal,
		},
		{
			name: "one criterion with no check needs a member for the whole work order", criteria: []string{"c1", "c2", "c3"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed), check("c2", SourceLane, CheckPassed)},
			want:     []Standing{StandingCovered, StandingSecondPrincipal, StandingMember}, needs: NeedMember,
		},
		{
			name: "no criteria at all counts for nothing", needs: NeedMember,
			evidence: []Evidence{check("c1", SourceForge, CheckPassed)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Assess(tt.criteria, tt.evidence)
			var standings []Standing
			for i, row := range got.Criteria {
				if row.Criterion != tt.criteria[i] {
					t.Errorf("row %d names %s", i, row.Criterion)
				}
				standings = append(standings, row.Standing)
			}
			if !slices.Equal(standings, tt.want) {
				t.Errorf("standings %v, want %v", standings, tt.want)
			}
			if got.Needs != tt.needs {
				t.Errorf("needs %s, want %s", got.Needs, tt.needs)
			}
			if len(got.Failed) != tt.failed {
				t.Errorf("failed %v, want %d", got.Failed, tt.failed)
			}
		})
	}
}
