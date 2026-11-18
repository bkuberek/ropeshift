package ports

import (
	"reflect"
	"testing"
)

// A code tree is the topmost directory below the root that holds a build file, and a build file at the root
// makes the whole repository one.
func TestCodeTreesAreTheTopmostDirectoriesHoldingABuildFile(t *testing.T) {
	for _, one := range []struct {
		name  string
		paths []string
		want  []CodeTree
	}{
		{"no build file", []string{"README.md", "docs/a.md"}, nil},
		{"one tree", []string{"README.md", "api/go.mod", "api/main.go"}, []CodeTree{{Path: "api", BuildFile: "api/go.mod"}}},
		{"two trees in order", []string{"web/package.json", "api/go.mod"}, []CodeTree{{Path: "api", BuildFile: "api/go.mod"}, {Path: "web", BuildFile: "web/package.json"}}},
		{"a tree inside a tree is one tree", []string{"api/go.mod", "api/tools/go.mod", "api/web/package.json"}, []CodeTree{{Path: "api", BuildFile: "api/go.mod"}}},
		{"a tree below a directory with no build file", []string{"services/api/pyproject.toml", "services/README.md"}, []CodeTree{{Path: "services/api", BuildFile: "services/api/pyproject.toml"}}},
		{"a build file at the root makes the repository the tree", []string{"go.mod", "api/package.json"}, []CodeTree{{Path: ".", BuildFile: "go.mod"}}},
		{"a directory with two build files names the first the list gives", []string{"app/package.json", "app/go.mod"}, []CodeTree{{Path: "app", BuildFile: "app/go.mod"}}},
		{"a project file of any name", []string{"src/Shop.csproj"}, []CodeTree{{Path: "src", BuildFile: "src/Shop.csproj"}}},
		{"a name that only looks like a build file", []string{"api/go.mod.bak", "api/mygo.mod", "web/package.json.md"}, nil},
	} {
		if got := CodeTreesOf(one.paths); !reflect.DeepEqual(got, one.want) {
			t.Errorf("%s: %+v, want %+v", one.name, got, one.want)
		}
	}
}

func TestInstructionsAreAnAgentsOrClaudeFileAtTheRootInAnyCase(t *testing.T) {
	for _, one := range []struct {
		name  string
		paths []string
		want  bool
	}{
		{"none", []string{"README.md", "docs/AGENTS.md"}, false},
		{"agents", []string{"AGENTS.md"}, true},
		{"claude", []string{"src/a.go", "CLAUDE.md"}, true},
		{"a lowercase agents file", []string{"agents.md"}, true},
		{"a mixed case claude file", []string{"Claude.MD"}, true},
		{"a case variant below a directory", []string{"docs/agents.md", "docs/claude.md"}, false},
	} {
		if got := HoldsInstructions(one.paths); got != one.want {
			t.Errorf("%s: %v, want %v", one.name, got, one.want)
		}
	}
}
