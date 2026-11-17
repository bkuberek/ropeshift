package projectfolders

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// A folder swapped for a link to a sibling between the check that it is a folder and the opening of it is not written
// into. A link that leads out of the parent is refused at the open, and one that stays inside it, as a relative link to a
// sibling does, opens another folder, which the comparison of the opened handle with what the check found refuses. The
// swap is a seam between the two steps, because no real swap is timed to land there. The sibling stays empty.
func TestWriteRefusesAFolderSwappedForALinkBetweenTheCheckAndTheOpen(t *testing.T) {
	for name, target := range map[string]func(sibling string) string{
		"an absolute link": func(sibling string) string { return sibling },
		"a relative link":  func(string) string { return "sibling" },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			folder, sibling := filepath.Join(root, "harbor"), filepath.Join(root, "sibling")
			if err := os.Mkdir(sibling, 0o755); err != nil {
				t.Fatal(err)
			}
			f := Folders{between: func() {
				if err := os.Remove(folder); err != nil {
					t.Error(err)
				}
				if err := os.Symlink(target(sibling), folder); err != nil {
					t.Error(err)
				}
			}}
			id, err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents"}, {Path: "plan/goal.md", Text: "goal"}}, "")
			if !errors.Is(err, ports.ErrFolderChanged) || id != "" {
				t.Fatalf("the write says %q (%v), want a refusal that the folder changed", id, err)
			}
			if entries, _ := os.ReadDir(sibling); len(entries) != 0 {
				t.Errorf("the sibling the link led to holds %v", entries)
			}
		})
	}
}
