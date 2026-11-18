package worker_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/worker"
)

// gitRefuses is a machine on which git cannot say whether a workspace ignores a path, as it cannot when the
// workspace's .ropeshift is a link, or the clone is not one the user owns.
type gitRefuses struct{ *fake.Checkouts }

func (gitRefuses) Ignored(context.Context, string, string) (bool, error) {
	return false, errors.New("fatal: pathspec is beyond a symbolic link (exit status 128)")
}

// A workspace that cannot be checked stops no lane that never uses it. A lane with a root a person set starts as
// it would with no workspace, and a lane on the built-in root starts outside every clone with the reason in its
// start notice, where it would otherwise have failed on git's answer for a workspace it never asked about.
func TestAWorkspaceThatCannotBeCheckedStopsNoLane(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		user     string
		want     string
		fallback string
	}{
		{"a root a person set", "[worktree]\nroot = \"/mine/{project}/{repo}\"\n", "/mine/prj-1/api/l1", ""},
		{"the built-in root", "", "/data/ropeshift/worktrees/prj-1/api/l1", lanes.FallbackUnreadable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, source, _ := provisioner(nil)
			source.SetDefaultRoot("/data/ropeshift/worktrees/{project}/{repo}")
			source.PutUser(tt.user + "[projects.prj-1]\nworkspace = \"/work/ws\"\n")
			p.Checkouts = gitRefuses{Checkouts: workspaceMachine(t)}
			lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/ws/repos/api", Project: "prj-1", PlanningRepository: "plan"})
			if err != nil {
				t.Fatalf("the lane is stopped: %v", err)
			}
			if lane.Worktree.Path != tt.want || lane.Fallback.Reason != tt.fallback {
				t.Errorf("the worktree is at %q with the fallback %+v, want %q and %q", lane.Worktree.Path, lane.Fallback, tt.want, tt.fallback)
			}
			if tt.fallback != "" && !strings.Contains(lane.Fallback.Notice(), "symbolic link") {
				t.Errorf("the notice %q does not carry what git said", lane.Fallback.Notice())
			}
		})
	}
}

// A root a person set inside the workspace's clone needs the workspace to say its folder for worktrees is ignored.
// When the workspace cannot be checked, the root is refused as a root inside a clone, with the lane's own reason,
// and not with git's error.
func TestAUserRootUnderAWorkspaceThatCannotBeCheckedIsRefusedAsInsideTheClone(t *testing.T) {
	t.Parallel()
	p, source, _ := provisioner(nil)
	source.PutUser("[worktree]\nroot = \"/work/ws/.ropeshift/worktrees/{repo}\"\n[projects.prj-1]\nworkspace = \"/work/ws\"\n")
	p.Checkouts = gitRefuses{Checkouts: workspaceMachine(t)}
	_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/ws/repos/api", Project: "prj-1", PlanningRepository: "plan"})
	var inside *worker.WorktreeInsideClone
	if !errors.As(err, &inside) || inside.Clone != "/work/ws" {
		t.Errorf("got %v, want a root inside the clone /work/ws", err)
	}
}
