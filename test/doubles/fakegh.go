package doubles

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

// PrebuiltFakeGH is the fake GitHub CLI's name in a prebuilt directory.
const PrebuiltFakeGH = "fakegh"

var builtGH struct {
	once sync.Once
	dir  string
	path string
	err  error
}

func buildFakeGH() (string, error) {
	builtGH.once.Do(func() {
		builtGH.dir, builtGH.err = os.MkdirTemp("", "rsfakegh")
		if builtGH.err != nil {
			return
		}
		builtGH.path, builtGH.err = Binary(builtGH.dir, PrebuiltFakeGH, "ropeshift.com/ropeshift/test/doubles/fakegh")
	})
	return builtGH.path, builtGH.err
}

// InstallFakeGH puts a `gh` that answers from a fake forge in a new directory, and returns the directory.
// A test puts it first on PATH, or names its `gh` to the adapter, and reads and changes the forge with
// ghstate. Nothing it runs reaches GitHub.
func InstallFakeGH(t testing.TB) string {
	t.Helper()
	path, err := buildFakeGH()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(path, filepath.Join(dir, "gh")); err != nil {
		t.Fatal(err)
	}
	if err := ghstate.Edit(dir, func(*ghstate.State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return dir
}

// EditFakeGH changes the fake forge, as a person on GitHub would.
func EditFakeGH(t testing.TB, dir string, change func(*ghstate.State)) {
	t.Helper()
	if err := ghstate.Edit(dir, func(s *ghstate.State) error { change(s); return nil }); err != nil {
		t.Fatal(err)
	}
}

// FakeGH reads the fake forge as it stands.
func FakeGH(t testing.TB, dir string) ghstate.State {
	t.Helper()
	state, err := ghstate.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// FakeGHCalls reads every call the fake `gh` took, in order.
func FakeGHCalls(t testing.TB, dir string) []ghstate.Call {
	t.Helper()
	calls, err := ghstate.Calls(dir)
	if err != nil {
		t.Fatal(err)
	}
	return calls
}
