package adoption_test

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/planning"
)

func TestArrivalsReadsPathsThatDifferOnlyInCaseAsOnePath(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		ours, theirs, leftOut []string
		takes                 []string
		collisions            []planning.Collision
	}{
		"the same spelling": {
			ours: []string{"AGENTS.md"}, theirs: []string{"AGENTS.md"},
			collisions: []planning.Collision{{Path: "AGENTS.md", Kept: planning.KeptWorkspace}},
		},
		"instructions in another case": {
			ours: []string{"AGENTS.md"}, theirs: []string{"agents.md"},
			collisions: []planning.Collision{{Path: "AGENTS.md", Kept: planning.KeptWorkspace}},
		},
		"a Makefile in another case": {
			ours: []string{"Makefile"}, theirs: []string{"makefile"},
			collisions: []planning.Collision{{Path: "Makefile", Kept: planning.KeptWorkspace}},
		},
		"the ignore file in another case": {
			ours: []string{".gitignore"}, theirs: []string{".GITIGNORE"},
			collisions: []planning.Collision{{Path: ".gitignore", Kept: planning.KeptUnion}},
		},
		"the workspace's ignore file in capitals": {
			ours: []string{".GITIGNORE"}, theirs: []string{".gitignore"},
			collisions: []planning.Collision{{Path: ".GITIGNORE", Kept: planning.KeptUnion}},
		},
		"a folder in another case": {
			ours: []string{"Docs/guide.md"}, theirs: []string{"docs/guide.md", "docs/other.md"},
			takes:      []string{"docs/other.md"},
			collisions: []planning.Collision{{Path: "Docs/guide.md", Kept: planning.KeptWorkspace}},
		},
		"the manifest folder in another case": {
			ours: []string{"Plan/repositories.md"}, theirs: []string{"plan/repositories.md"},
			collisions: []planning.Collision{{Path: "Plan/repositories.md", Kept: planning.KeptWorkspace}},
		},
		"a path the workspace lacks": {
			ours: []string{"AGENTS.md"}, theirs: []string{"CLAUDE.md", "agents.md"},
			takes:      []string{"CLAUDE.md"},
			collisions: []planning.Collision{{Path: "AGENTS.md", Kept: planning.KeptWorkspace}},
		},
		"a variant inside a tree left out": {
			ours: []string{"Poc/NOTES.md"}, theirs: []string{"poc/NOTES.md"}, leftOut: []string{"poc"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			takes, collisions := adoption.Arrivals(tt.ours, tt.theirs, tt.leftOut)
			if !reflect.DeepEqual(takes, tt.takes) || !reflect.DeepEqual(collisions, tt.collisions) {
				t.Errorf("takes %v and collisions %+v, want %v and %+v", takes, collisions, tt.takes, tt.collisions)
			}
		})
	}
}

func TestHoldsReadsAPathInAnyCase(t *testing.T) {
	t.Parallel()
	if !adoption.Holds([]string{"Plan/Repositories.md"}, planning.ManifestPath) {
		t.Error("the manifest under another case is not held")
	}
	if adoption.Holds([]string{"plan/goal.md"}, planning.ManifestPath) {
		t.Error("another file is held as the manifest")
	}
}
