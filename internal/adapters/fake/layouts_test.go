package fake

import (
	"errors"
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// The fake reads a clone as the real one does: documents, code trees, and instruction files come from the
// tracked paths.
func TestFakeLayoutsReadWhatAClonePlaced(t *testing.T) {
	layouts := NewLayouts()
	layouts.Set("/work/design", CloneLayout{
		Ignored: []string{"scratch"},
		Plan:    ports.PlanContent{Files: 2, Manifest: true},
		Tracked: []string{"AGENTS.md", "docs/a.md", "docs/b.md", "tools/go.mod", "README.txt"},
	})
	ctx := t.Context()

	if got, err := layouts.Ignored(ctx, "/work/design"); err != nil || !reflect.DeepEqual(got, []string{"scratch"}) {
		t.Errorf("ignored directories read %v: %v", got, err)
	}
	if got, err := layouts.Plan(ctx, "/work/design"); err != nil || !got.Held() || !got.Manifest {
		t.Errorf("plan content reads %+v: %v", got, err)
	}
	if got, err := layouts.Documents(ctx, "/work/design"); err != nil || got != 3 {
		t.Errorf("documents count %d: %v", got, err)
	}
	if got, err := layouts.CodeTrees(ctx, "/work/design"); err != nil || !reflect.DeepEqual(got, []ports.CodeTree{{Path: "tools", BuildFile: "tools/go.mod"}}) {
		t.Errorf("code trees read %+v: %v", got, err)
	}
	if got, err := layouts.Instructions(ctx, "/work/design"); err != nil || !got {
		t.Errorf("instructions read %v: %v", got, err)
	}
	tip, err := layouts.Tip(ctx, "/work/design")
	if err != nil || tip == "" {
		t.Fatalf("tip reads %q: %v", tip, err)
	}
	if got, err := layouts.Tracked(ctx, "/work/design", tip); err != nil || len(got) != 5 {
		t.Errorf("tracked paths read %v: %v", got, err)
	}
	if _, err := layouts.Tracked(ctx, "/work/design", "not-a-commit"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a commit the clone does not hold: %v", err)
	}
	// A clone nothing was placed for holds nothing.
	if got, err := layouts.Plan(ctx, "/work/ghost"); err != nil || got.Held() {
		t.Errorf("a clone nothing was placed for reads plan content %+v: %v", got, err)
	}
	if ignored, err := layouts.Ignored(ctx, "/work/ghost"); err != nil || len(ignored) != 0 {
		t.Errorf("a clone nothing was placed for ignores %v: %v", ignored, err)
	}
	ghost, err := layouts.Tip(ctx, "/work/ghost")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := layouts.Tracked(ctx, "/work/ghost", ghost); err != nil || len(got) != 0 {
		t.Errorf("a clone nothing was placed for tracks %v: %v", got, err)
	}
}
