package lanes_test

import (
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
)

func TestPlanGuard(t *testing.T) {
	tests := []struct {
		name   string
		found  lanes.WorktreeFindings
		fenced bool
		want   lanes.GuardPlan
	}{
		{name: "clean, everything pushed", found: lanes.WorktreeFindings{Remote: "origin", Branch: "lane/one"}},
		{
			name:  "commits not pushed",
			found: lanes.WorktreeFindings{Remote: "origin", Branch: "lane/one", Ahead: 2},
			want:  lanes.GuardPlan{Push: true},
		},
		{
			name:  "uncommitted changes",
			found: lanes.WorktreeFindings{Remote: "origin", Branch: "lane/one", Dirty: true},
			want:  lanes.GuardPlan{Snapshot: true, PushSnapshot: true},
		},
		{
			name:  "both",
			found: lanes.WorktreeFindings{Remote: "origin", Branch: "lane/one", Dirty: true, Ahead: 1},
			want:  lanes.GuardPlan{Push: true, Snapshot: true, PushSnapshot: true},
		},
		{name: "no remote and nothing of its own", found: lanes.WorktreeFindings{Branch: "lane/one"}},
		{name: "no remote and commits of its own", found: lanes.WorktreeFindings{Branch: "lane/one", Ahead: 1}},
		{
			name:  "no remote and uncommitted changes",
			found: lanes.WorktreeFindings{Branch: "lane/one", Dirty: true},
			want:  lanes.GuardPlan{Snapshot: true},
		},
		{
			name:  "a detached head has no branch to push, and only a ref keeps its commits",
			found: lanes.WorktreeFindings{Remote: "origin", Ahead: 1},
			want:  lanes.GuardPlan{Snapshot: true, PushSnapshot: true},
		},
		{
			name:   "a fenced lane leaves the branch alone and snapshots its commits",
			found:  lanes.WorktreeFindings{Remote: "origin", Branch: "lane/one", Ahead: 1},
			fenced: true,
			want:   lanes.GuardPlan{Snapshot: true, PushSnapshot: true},
		},
		{
			name:   "a fenced lane with nothing of its own has nothing to save",
			found:  lanes.WorktreeFindings{Remote: "origin", Branch: "lane/one"},
			fenced: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.PlanGuard(tt.found, tt.fenced); got != tt.want {
				t.Errorf("plan %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSettleGuard(t *testing.T) {
	tests := []struct {
		name  string
		after lanes.WorktreeFindings
		acts  lanes.GuardActs
		want  bool
	}{
		{name: "nothing was there", after: lanes.WorktreeFindings{Remote: "origin"}},
		{name: "the push took", after: lanes.WorktreeFindings{Remote: "origin"}, acts: lanes.GuardActs{Plan: lanes.GuardPlan{Push: true}}},
		{name: "the push failed", after: lanes.WorktreeFindings{Remote: "origin", Ahead: 1}, want: true},
		{
			name:  "a snapshot on the remote saves uncommitted work",
			after: lanes.WorktreeFindings{Remote: "origin", Dirty: true},
			acts:  lanes.GuardActs{Ref: "r", SnapshotPushed: true},
		},
		{
			name:  "a snapshot that stayed here is a local copy",
			after: lanes.WorktreeFindings{Remote: "origin", Dirty: true},
			acts:  lanes.GuardActs{Ref: "r"},
			want:  true,
		},
		{name: "no remote holds commits", after: lanes.WorktreeFindings{Ahead: 1}, want: true},
		{name: "no remote holds changes", after: lanes.WorktreeFindings{Dirty: true}, acts: lanes.GuardActs{Ref: "r"}, want: true},
		{
			name:  "a fenced lane's commits are saved by its pushed snapshot",
			after: lanes.WorktreeFindings{Remote: "origin", Ahead: 1},
			acts:  lanes.GuardActs{Fenced: true, Ref: "r", SnapshotPushed: true},
		},
		{
			name:  "a fenced lane's snapshot that stayed here",
			after: lanes.WorktreeFindings{Remote: "origin", Ahead: 1},
			acts:  lanes.GuardActs{Fenced: true, Ref: "r"},
			want:  true,
		},
		{
			name:  "a detached head's commits are saved by its pushed snapshot",
			after: lanes.WorktreeFindings{Remote: "origin", Ahead: 1},
			acts:  lanes.GuardActs{Plan: lanes.GuardPlan{Snapshot: true}, Ref: "r", SnapshotPushed: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.SettleGuard(tt.after, tt.acts); got.Unpushed != tt.want {
				t.Errorf("unpushed %v, want %v", got.Unpushed, tt.want)
			}
		})
	}
}

func TestSnapshotRefs(t *testing.T) {
	if got := lanes.SalvageRef("lane-1", 7); got != "refs/ropeshift/salvage/lane-1/f7" {
		t.Errorf("salvage ref %s", got)
	}
	if got := lanes.CheckpointRef("lane-1", 3); got != "refs/ropeshift/checkpoints/lane-1/3" {
		t.Errorf("checkpoint ref %s", got)
	}
}

func TestNextPortBase(t *testing.T) {
	pool := lanes.PortPool{Base: 20000, Size: 30}
	tests := []struct {
		name  string
		held  []lanes.Allocation
		ports int64
		want  int64
		fits  bool
	}{
		{name: "an empty pool starts at its base", ports: 10, want: 20000, fits: true},
		{
			name: "a block follows the one before it", ports: 10, want: 20010, fits: true,
			held: []lanes.Allocation{{PortBase: 20000, Ports: 10}},
		},
		{
			name: "a released block is used again", ports: 10, want: 20000, fits: true,
			held: []lanes.Allocation{{PortBase: 20010, Ports: 10}},
		},
		{
			name: "a wider block finds no gap", ports: 20,
			held: []lanes.Allocation{{PortBase: 20010, Ports: 10}},
		},
		{
			name: "a full pool has no block", ports: 10,
			held: []lanes.Allocation{{PortBase: 20000, Ports: 30}},
		},
		{name: "a lane that asks for no ports takes none", ports: 0, want: 0, fits: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fits := lanes.NextPortBase(tt.held, pool, tt.ports)
			if fits != tt.fits || (fits && got != tt.want) {
				t.Errorf("base %d, fits %v, want %d, %v", got, fits, tt.want, tt.fits)
			}
		})
	}
}

func TestComposeProject(t *testing.T) {
	tests := []struct {
		name, prefix, lane, want string
	}{
		{name: "a plain lane", prefix: "rs", lane: "lane-1", want: "rs-lane-1"},
		{name: "upper case is folded, and two spellings stay apart", prefix: "rs", lane: "Lane-1", want: "rs-lane-1-e6f3a6"},
		{name: "a name compose refuses", prefix: "rs", lane: "lane.1 two", want: "rs-lane-1-two-34a43b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lanes.ComposeProject(tt.prefix, tt.lane)
			if got[:len("rs-lane-1")] != tt.want[:len("rs-lane-1")] {
				t.Errorf("project %s, want %s", got, tt.want)
			}
			if tt.lane != "lane-1" && got == lanes.ComposeProject(tt.prefix, "lane-1") {
				t.Errorf("%q and lane-1 share a project", tt.lane)
			}
			for _, c := range got {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
					t.Errorf("project %q holds %q", got, c)
				}
			}
		})
	}
}
