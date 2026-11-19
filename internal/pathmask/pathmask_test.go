package pathmask_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/pathmask"
)

// The checkout's directory is replaced in each spelling a tool writes it: as given, cleaned, and as symbolic links
// resolve it.
func TestTextReplacesEverySpellingOfTheCheckout(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real", "widgets")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "real"), link); err != nil {
		t.Fatal(err)
	}
	given := filepath.Join(link, "widgets")
	got := pathmask.Text("fatal: "+given+"/ and "+real+"/.git and "+filepath.Join(link, "widgets", ".git"), given)
	if want := "fatal: <checkout>/ and <checkout>/.git and <checkout>/.git"; got != want {
		t.Errorf("the text reads %q, want %q", got, want)
	}
}

// The folders of the machine a text names are replaced too: the home folder, the temporary folder, and the
// configuration folder, the longest path first.
func TestTextReplacesTheMachinesFolders(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 2 {
		t.Skip("no home folder to replace")
	}
	got := pathmask.Text("read "+filepath.Join(home, ".gitconfig")+" and "+filepath.Join(os.TempDir(), "x"), "/checkouts/widgets")
	if strings.Contains(got, home) || strings.Contains(got, "~") == false {
		t.Errorf("the text reads %q, which still names the home folder %q", got, home)
	}
}

// A text that names no path stays as it is, and so does an empty one.
func TestTextLeavesATextWithNoPath(t *testing.T) {
	for _, text := range []string{"", "the repository holds no commit"} {
		if got := pathmask.Text(text, "/checkouts/widgets"); got != text {
			t.Errorf("%q reads %q", text, got)
		}
	}
}
