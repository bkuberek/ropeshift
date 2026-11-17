package checkouts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A search for clones stops when its context ends, and says so, instead of walking the whole tree.
func TestFindStopsWhenItsContextEnds(t *testing.T) {
	root := real(t, t.TempDir())
	origin := cloneInto(t, filepath.Join(root, "api"))
	for _, dir := range []string{"node_modules/a/b", "target/c/d"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f := &File{Path: filepath.Join(t.TempDir(), FileName), Env: origin.Env()}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := f.Find(ctx, []string{root}, 4); !errors.Is(err, context.Canceled) {
		t.Errorf("a search with an ended context answered %v", err)
	}
	if clones, _, err := f.Find(t.Context(), []string{root}, 4); err != nil || len(clones) != 1 {
		t.Errorf("the same search with a live context found %v: %v", clones, err)
	}
}
