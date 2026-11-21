package lanes

import (
	"fmt"
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
			changes: []string{".atl/a.md", "a.go"},
			own:     []string{".atl/a.md", "a.go"},
		},
		{
			name:    "a file there before the first turn is left out",
			files:   LaneFiles{Before: []string{".atl/a.md"}, Began: true, AllWritten: true},
			changes: []string{".atl/a.md", "gen/out.go"},
			own:     []string{"gen/out.go"},
			left:    []string{".atl/a.md"},
		},
		{
			name:    "a new file in a folder that was there before is the lane's own",
			files:   LaneFiles{Before: []string{".cache/index"}, Began: true, AllWritten: true},
			changes: []string{".cache/index", ".cache/new", "main.go"},
			own:     []string{".cache/new", "main.go"},
			left:    []string{".cache/index"},
		},
		{
			name:    "a folder in the reading makes no new file in it old",
			files:   LaneFiles{Before: []string{".cache/"}, Began: true, AllWritten: true},
			changes: []string{".cache/new"},
			own:     []string{".cache/new"},
		},
		{
			name:    "a file there before that the session edited is its own",
			files:   LaneFiles{Before: []string{".env.example"}, Began: true, Written: []string{".env.example"}, AllWritten: true},
			changes: []string{".env.example"},
			own:     []string{".env.example"},
		},
		{
			name:    "what the session wrote beside files there before is its own, and the rest is left out",
			files:   LaneFiles{Before: []string{".atl/a.md", ".atl/notes.md"}, Began: true, Written: []string{".atl/notes.md"}, AllWritten: true},
			changes: []string{".atl/a.md", ".atl/notes.md"},
			own:     []string{".atl/notes.md"},
			left:    []string{".atl/a.md"},
		},
		{
			name:    "writes not all known leave nothing out",
			files:   LaneFiles{Before: []string{".atl/a.md"}, Began: true},
			changes: []string{".atl/a.md", "a.go"},
			own:     []string{".atl/a.md", "a.go"},
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
	if !builder.Begin([]string{".atl/a.md"}) {
		t.Fatal("the first reading was not kept")
	}
	if builder.Begin([]string{".atl/a.md", "a.go"}) {
		t.Error("a second reading replaced the first")
	}
	raw, err := builder.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	files := LaneFilesOf([]DigestRecord{{Lane: "lane-1", Generation: 1, State: raw}})
	if !files.Began || !slices.Equal(files.Before, []string{".atl/a.md"}) || !files.AllWritten {
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

// TestAReadingAfterTheSessionBeganIsNeverTaken is the reading's one moment: a digest that has seen a turn or a call,
// or that was told the reading failed, keeps no reading and owns every change from then on.
func TestAReadingAfterTheSessionBeganIsNeverTaken(t *testing.T) {
	t.Parallel()
	called := NewDigest(DigestHooks, "/lanes/lane-1", 1)
	called.Hook(HookEvent{Event: EventPreToolUse, Tool: "Bash"})
	prompted := NewDigest(DigestHooks, "/lanes/lane-1", 1)
	prompted.Hook(HookEvent{Event: EventUserPromptSubmit})
	failed := NewDigest(DigestHooks, "/lanes/lane-1", 1)
	failed.LeaveUnread()
	many := NewDigest(DigestHooks, "/lanes/lane-1", 1)
	for name, builder := range map[string]*DigestBuilder{"a call": called, "a turn": prompted, "a failed reading": failed, "too many changes": many} {
		changes := []string{".atl/a.md"}
		if builder == many {
			changes = make([]string, MaxBefore+1)
			for i := range changes {
				changes[i] = fmt.Sprintf("cache/%d", i)
			}
		}
		if builder.Begin(changes) {
			t.Errorf("after %s, a reading was kept", name)
		}
		if builder.Begin(nil) {
			t.Errorf("after %s, a second reading was kept", name)
		}
		if files := LaneFilesOf([]DigestRecord{record(t, 1, builder)}); files.Began || len(files.Before) != 0 {
			t.Errorf("after %s, the lane's files read as begun with %d files", name, len(files.Before))
		}
	}
}

// TestAnAwaitedReadingIsKeptAfterThePromptItBeganAt is a reading whose survey comes back after the digest has seen the
// first prompt it began at: it is kept once. A reading given up while it was awaited, a second moment, and a moment
// after the prompt keep none.
func TestAnAwaitedReadingIsKeptAfterThePromptItBeganAt(t *testing.T) {
	t.Parallel()
	prompt := HookEvent{Event: EventUserPromptSubmit}
	kept := NewDigest(DigestHooks, "/lanes/lane-1", 1)
	if !kept.Await() {
		t.Fatal("a fresh digest has no moment for a reading")
	}
	kept.Hook(prompt)
	if !kept.Awaiting() || !kept.Begin([]string{".atl/a.md"}) {
		t.Fatal("the awaited reading was not kept")
	}
	if kept.Awaiting() || kept.Begin([]string{".atl/a.md", "a.go"}) {
		t.Error("a kept reading still awaits one, or took a second")
	}
	if files := LaneFilesOf([]DigestRecord{record(t, 1, kept)}); !files.Began || !slices.Equal(files.Before, []string{".atl/a.md"}) {
		t.Errorf("files %+v", files)
	}
	givenUp := NewDigest(DigestHooks, "/lanes/lane-1", 1)
	givenUp.Await()
	givenUp.Hook(prompt)
	givenUp.LeaveUnread()
	twice := NewDigest(DigestStream, "/lanes/lane-1", 1)
	twice.Await()
	if twice.Await() {
		t.Error("a digest that awaits a reading had a second moment for one")
	}
	late := NewDigest(DigestHooks, "/lanes/lane-1", 1)
	late.Hook(prompt)
	if late.Await() {
		t.Error("a digest that saw a prompt had the moment for a reading")
	}
	for name, builder := range map[string]*DigestBuilder{"given up": givenUp, "a second moment": twice, "after the prompt": late} {
		if builder.Awaiting() || builder.Begin([]string{".atl/a.md"}) {
			t.Errorf("%s, a reading was kept", name)
		}
		if files := LaneFilesOf([]DigestRecord{record(t, 1, builder)}); files.Began {
			t.Errorf("%s, the lane's files read as begun", name)
		}
	}
}

// TestALeftOutReportNamesAWhollyUntrackedFolderOnce collapses the files of a folder git does not track, and holds
// nothing of the lane's own, into that folder, which is what a .gitignore entry would name.
func TestALeftOutReportNamesAWhollyUntrackedFolderOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		left, own, folders []string
		want               []string
	}{
		{"a cache folder", []string{".atl/a.md", ".atl/b.md", "notes.txt"}, []string{"gen/out.go"}, []string{".atl/"}, []string{".atl/", "notes.txt"}},
		{"a folder that holds a file of the lane's", []string{".atl/a.md"}, []string{".atl/new.md"}, []string{".atl/"}, []string{".atl/a.md"}},
		{"a deeper file", []string{"build/x/y.o"}, nil, []string{"build/"}, []string{"build/"}},
		{"no folder", []string{"a.log"}, nil, nil, []string{"a.log"}},
	}
	for _, tt := range tests {
		if got := CollapseLeft(tt.left, tt.own, tt.folders); !slices.Equal(got, tt.want) {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}
