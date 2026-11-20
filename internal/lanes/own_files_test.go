package lanes

import (
	"slices"
	"testing"
)

func TestALaneOwnsWhatChangedAfterItsSessionBegan(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		files   LaneFiles
		changes []string
		own     []string
		left    []string
	}{
		{
			name:    "no reading of the start owns every change",
			files:   LaneFiles{Written: []string{"a.go"}, AllWritten: true},
			changes: []string{".atl/", "a.go"},
			own:     []string{".atl/", "a.go"},
		},
		{
			name:    "a change there before the first turn is left out",
			files:   LaneFiles{Before: []string{".atl/"}, Began: true, AllWritten: true},
			changes: []string{".atl/", "gen/out.go"},
			own:     []string{"gen/out.go"},
			left:    []string{".atl/"},
		},
		{
			name:    "a file under a folder there before is left out",
			files:   LaneFiles{Before: []string{".cache/"}, Began: true, AllWritten: true},
			changes: []string{".cache/index", "main.go"},
			own:     []string{"main.go"},
			left:    []string{".cache/index"},
		},
		{
			name:    "a file there before that the session edited is its own",
			files:   LaneFiles{Before: []string{".env.example"}, Began: true, Written: []string{".env.example"}, AllWritten: true},
			changes: []string{".env.example"},
			own:     []string{".env.example"},
		},
		{
			name:    "what the session wrote in a folder there before is its own, and the rest is left out",
			files:   LaneFiles{Before: []string{".atl/"}, Began: true, Written: []string{".atl/notes.md"}, AllWritten: true},
			changes: []string{".atl/"},
			own:     []string{".atl/notes.md"},
			left:    []string{".atl/"},
		},
		{
			name:    "writes not all known leave nothing out",
			files:   LaneFiles{Before: []string{".atl/"}, Began: true},
			changes: []string{".atl/", "a.go"},
			own:     []string{".atl/", "a.go"},
		},
		{
			name:    "a clean start owns everything after it",
			files:   LaneFiles{Before: []string{}, Began: true, AllWritten: true},
			changes: []string{"b.go", "a.go"},
			own:     []string{"a.go", "b.go"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			own, left := tt.files.Split(tt.changes)
			if !slices.Equal(own, tt.own) || !slices.Equal(left, tt.left) {
				t.Errorf("own %q, left %q; want %q, %q", own, left, tt.own, tt.left)
			}
		})
	}
}

func TestADigestKeepsWhatTheWorktreeHeldBeforeTheFirstTurnOnce(t *testing.T) {
	t.Parallel()
	builder := NewDigest(DigestHooks, "/lanes/lane-1", 1)
	if _, began := builder.Before(); began {
		t.Fatal("a new digest says what the worktree held before it began")
	}
	if !builder.Begin([]string{".atl/"}) {
		t.Fatal("the first reading was not kept")
	}
	if builder.Begin([]string{".atl/", "a.go"}) {
		t.Error("a second reading replaced the first")
	}
	raw, err := builder.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	files := LaneFilesOf([]DigestRecord{{Lane: "lane-1", Generation: 1, State: raw}})
	if !files.Began || !slices.Equal(files.Before, []string{".atl/"}) || !files.AllWritten {
		t.Errorf("files %+v", files)
	}
}

func TestLaneFilesReadEveryGenerationsWritesAndOnlyTheFirstsStart(t *testing.T) {
	t.Parallel()
	first := NewDigest(DigestHooks, "/lanes/lane-1", 1)
	first.Begin([]string{})
	first.Hook(HookEvent{Event: EventPreToolUse, Tool: "Write", Path: "/lanes/lane-1/a.go"})
	second := NewDigest(DigestHooks, "/lanes/lane-1", 2)
	second.Begin([]string{"a.go"})
	second.Hook(HookEvent{Event: EventPreToolUse, Tool: "Edit", Path: "/lanes/lane-1/b.go"})
	second.Missed()
	files := LaneFilesOf([]DigestRecord{record(t, 2, second), record(t, 1, first)})
	if !files.Began || len(files.Before) != 0 || !slices.Equal(files.Written, []string{"a.go", "b.go"}) || files.AllWritten {
		t.Errorf("files %+v", files)
	}
}

func record(t *testing.T, generation int64, builder *DigestBuilder) DigestRecord {
	t.Helper()
	raw, err := builder.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return DigestRecord{Lane: "lane-1", Generation: generation, State: raw}
}
