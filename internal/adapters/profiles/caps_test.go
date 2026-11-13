package profiles

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

func capsOf(maxLanes *int64, perSignIn int64) lanes.LaneCapSettings {
	return lanes.LaneCapSettings{MaxLanes: maxLanes, MaxLanesPerSignIn: perSignIn}
}

func lanesCap(value int64) *int64 { return &value }

const kept = `# my layer
name = "mine"

[agent]
model = "sonnet"

[admission]
# keep this comment
max_lanes = 4   # four
max_lanes_per_sign_in = 1
min_free_memory_mb = 512

[telemetry]
traces = true
`

// TestEditLaneCapsKeepsTheRestOfTheLayer is what lets the web UI and the CLI write the file a person also
// edits: only the two caps change, and every other line, comments included, stays as it was.
func TestEditLaneCapsKeepsTheRestOfTheLayer(t *testing.T) {
	tests := []struct {
		name string
		text string
		caps lanes.LaneCapSettings
		want string
	}{
		{"an empty layer gains the table", "", capsOf(lanesCap(6), 3), "[admission]\nmax_lanes = 6\nmax_lanes_per_sign_in = 3\n"},
		{"a layer with no table gains one at its end", "name = \"mine\"\n", capsOf(lanesCap(0), 0), "name = \"mine\"\n\n[admission]\nmax_lanes = 0\n"},
		{"each cap changes in place and keeps its comment", kept, capsOf(lanesCap(9), 2),
			"# my layer\nname = \"mine\"\n\n[agent]\nmodel = \"sonnet\"\n\n[admission]\n# keep this comment\nmax_lanes = 9   # four\nmax_lanes_per_sign_in = 2\nmin_free_memory_mb = 512\n\n[telemetry]\ntraces = true\n"},
		{"a comment with no space before it stays", "[admission]\nmax_lanes = 4# keep me\r\n", capsOf(lanesCap(5), 0), "[admission]\nmax_lanes = 5# keep me\n"},
		{"the recommendation and no sign-in cap leave no keys", kept, capsOf(nil, 0),
			"# my layer\nname = \"mine\"\n\n[agent]\nmodel = \"sonnet\"\n\n[admission]\n# keep this comment\nmin_free_memory_mb = 512\n\n[telemetry]\ntraces = true\n"},
		{"a key the table lacks goes after its last key", "[admission]\nmin_free_memory_mb = 512\n\n[telemetry]\ntraces = true\n", capsOf(lanesCap(5), 0),
			"[admission]\nmin_free_memory_mb = 512\nmax_lanes = 5\n\n[telemetry]\ntraces = true\n"},
		{"a dotted table ends the table", "[admission]\nmin_free_memory_mb = 1\n[context.roles.writer]\nwarn_tokens = 1\n", capsOf(lanesCap(2), 0), "[admission]\nmin_free_memory_mb = 1\nmax_lanes = 2\n[context.roles.writer]\nwarn_tokens = 1\n"},
		{"nothing to write leaves the layer alone", "name = \"mine\"\n", capsOf(nil, 0), "name = \"mine\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EditLaneCaps(tt.text, tt.caps)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
			read, err := laneCapsOf(got)
			if err != nil || !sameCaps(read, tt.caps) {
				t.Errorf("reads back %+v, %v", read, err)
			}
		})
	}
}

func sameCaps(a, b lanes.LaneCapSettings) bool {
	return a.MaxLanesPerSignIn == b.MaxLanesPerSignIn && (a.MaxLanes == nil) == (b.MaxLanes == nil) && (a.MaxLanes == nil || *a.MaxLanes == *b.MaxLanes)
}

// TestEditLaneCapsRefusesWhatItCannotEditSafely leaves a layer it cannot edit line by line as it was, so a
// person edits it by hand.
func TestEditLaneCapsRefusesWhatItCannotEditSafely(t *testing.T) {
	for name, text := range map[string]string{
		"a dotted key":       "admission.max_lanes = 3\n",
		"an inline table":    "admission = { max_lanes = 3 }\n",
		"a layer that fails": "[admission\nmax_lanes = 3\n",
	} {
		if _, err := EditLaneCaps(text, capsOf(lanesCap(4), 0)); err == nil {
			t.Errorf("%s was edited", name)
		}
	}
}

func TestTheFilesystemSetsTheLaneCaps(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), UserFile)
	source := Filesystem{User: path}
	if caps, err := source.LaneCaps(ctx); err != nil || caps.MaxLanes != nil || caps.MaxLanesPerSignIn != 0 {
		t.Fatalf("an absent layer reads %+v, %v", caps, err)
	}
	if err := source.SetLaneCaps(ctx, capsOf(lanesCap(8), 2)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("a new layer is %v: %v", info.Mode(), err)
	}
	if err := os.WriteFile(path, []byte(kept), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := source.SetLaneCaps(ctx, capsOf(lanesCap(12), 0)); err != nil {
		t.Fatal(err)
	}
	if caps, err := source.LaneCaps(ctx); err != nil || !sameCaps(caps, capsOf(lanesCap(12), 0)) {
		t.Errorf("caps %+v, %v", caps, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("an edit changed the layer's mode to %v: %v", info.Mode(), err)
	}
	profile, err := source.Load(ctx, "", "")
	if err != nil || profile.Admission.MinFreeMemoryMB != 512 || profile.Agent.Model != "sonnet" {
		t.Errorf("the rest of the layer: %+v, %v", profile.Admission, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Errorf("an edit left %d files beside the layer: %v", len(entries), err)
	}
}

// TestALinkedLayerStaysALink edits the file a symbolic link names and leaves the link, as a person who keeps
// the layer in a dotfiles repository expects.
func TestALinkedLayerStaysALink(t *testing.T) {
	ctx := context.Background()
	real := filepath.Join(t.TempDir(), "profile.toml")
	if err := os.WriteFile(real, []byte(kept), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), UserFile)
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := (Filesystem{User: link}).SetLaneCaps(ctx, capsOf(lanesCap(3), 0)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link is now %v: %v", info.Mode(), err)
	}
	if caps, err := laneCapsOf(mustRead(t, real)); err != nil || !sameCaps(caps, capsOf(lanesCap(3), 0)) {
		t.Errorf("the linked file holds %+v, %v", caps, err)
	}
	for _, dir := range []string{filepath.Dir(real), filepath.Dir(link)} {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
			t.Errorf("%d files in a directory of the edit: %v", len(entries), err)
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
