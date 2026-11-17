package fake

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// The fake machine answers Ignored as git does: a path is ignored when the clone ignores it or a folder above it,
// and no path is ignored until a test says so.
func TestIgnoredAnswersForTheFoldersAClonesRepositoryIgnores(t *testing.T) {
	machine := NewCheckouts()
	machine.Place(ports.Clone{Path: "/work/plan", ID: "plan"})
	machine.Ignore("/work/plan", "repos", "scratch/tmp")
	for path, want := range map[string]bool{
		"repos":              true,
		"repos/api":          true,
		"repos/payments/api": true,
		"scratch/tmp":        true,
		"scratch/tmp/x":      true,
		"scratch":            false,
		"reposition":         false,
		"vendor/ledger":      false,
	} {
		if got, err := machine.Ignored(t.Context(), "/work/plan", path); err != nil || got != want {
			t.Errorf("%s is ignored: %v (%v), want %v", path, got, err, want)
		}
	}
	if got, err := machine.Ignored(t.Context(), "/work/other", "repos/api"); err != nil || got {
		t.Errorf("a clone that ignores nothing ignores repos/api: %v (%v)", got, err)
	}
}

// The fake machine answers SameFile as the real one does: two paths are one file when what the machine holds at
// each is the same, through links and spellings, and a path nothing is at is ErrNotFound.
func TestSameFileAnswersAsTheRealMachineDoes(t *testing.T) {
	machine := NewCheckouts()
	machine.Place(ports.Clone{Path: "/work/plan", ID: "plan"})
	machine.Place(ports.Clone{Path: "/work/api", ID: "api"})
	machine.Link("/alias", "/work")
	machine.Place(ports.Clone{Path: "/work/App", ID: "app"})
	machine.Spell("/work/app", "/work/App")
	for _, one := range []struct {
		name, a, b string
		same       bool
	}{
		{"a clone and itself", "/work/plan", "/work/plan", true},
		{"a clone through a link", "/alias/plan", "/work/plan", true},
		{"two clones", "/work/plan", "/work/api", false},
		{"a directory above clones", "/work", "/alias", true},
		{"a spelling of a clone", "/work/app", "/work/App", true},
		{"a directory inside a clone", "/work/plan/docs", "/work/plan/docs", true},
	} {
		if same, err := machine.SameFile(t.Context(), one.a, one.b); err != nil || same != one.same {
			t.Errorf("%s: %s and %s are the same file: %v (%v), want %v", one.name, one.a, one.b, same, err, one.same)
		}
	}
	for _, one := range []struct{ name, a, b string }{
		{"a path nothing is at, against a clone", "/work/ghost", "/work/plan"},
		{"a clone, against a path nothing is at", "/work/plan", "/work/ghost"},
		{"a path nothing is at, against itself", "/nowhere/a", "/nowhere/a"},
		{"two paths nothing is at", "/nowhere/a", "/nowhere/b"},
		{"a link to nothing", "/alias-to-nothing/plan", "/work/plan"},
	} {
		if one.name == "a link to nothing" {
			machine.Link("/alias-to-nothing", "/gone")
		}
		if _, err := machine.SameFile(t.Context(), one.a, one.b); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("%s: %s and %s read %v, want ErrNotFound", one.name, one.a, one.b, err)
		}
	}
}
