package worker

import (
	"context"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// writtenLayer is the profile layer a writer committed at the commit under review: a gate, which can only narrow what a
// review may do, a model, and a lane cap, which a layer past the user's may lower.
const writtenLayer = "[agent]\nmodel = \"written\"\ngates = [\"Bash(git tag *)\"]\n[admission]\nmax_lanes = 1\n"

// unbasedReview is a review of a lane that recorded no base, whose profile layer the writer wrote at the commit under
// review, with the run and the lease a worker reads its profile for.
func unbasedReview(t *testing.T) *laneRun {
	t.Helper()
	source := fake.NewProfileSource()
	source.PutAt("/work/app", "tip", writtenLayer)
	runtime := &LaneRuntime{
		Provisioner: Provisioner{Profiles: source},
		Repository:  func(context.Context, string, string) (string, error) { return "/work/app", nil },
	}
	lease := ledger.DesiredLease{
		WorkOrder: "wo", Lane: "r1", Kind: string(workorders.KindReview), Repository: "repo", BaseCommit: "tip",
		Attempt: &ledger.DesiredAttempt{Commit: "tip"},
	}
	return &laneRun{w: &Worker{config: Config{Lanes: runtime, Report: func(error) {}}}, rt: runtime, lease: lease, state: &laneState{}}
}

// A review of a lane that recorded no base, adopted by a restarted worker, runs on the gates of the layer its writer wrote
// at the commit under review and on nothing else the layer sets, as the review's start reads it.
func TestAnAdoptedReviewWithNoBaseTakesOnlyTheGatesOfTheLayerItReads(t *testing.T) {
	t.Parallel()
	run := unbasedReview(t)
	run.loadProfile(t.Context())
	if run.settings.ModelRequested == "written" {
		t.Errorf("an adopted review with no base runs the model its writer's layer sets: %+v", run.settings)
	}
	if !slices.Contains(run.gates, "Bash(git tag *)") {
		t.Errorf("an adopted review with no base runs with the gates %v, want the layer's", run.gates)
	}
}

// The caps a review of a lane that recorded no base is admitted under are those of the layers below the repository's, and
// never the lane cap its writer's layer sets at the commit under review.
func TestTheCapsOfAReviewWithNoBaseIgnoreTheLayerItsWriterWrote(t *testing.T) {
	t.Parallel()
	run := unbasedReview(t)
	if _, layered := run.w.limits(t.Context(), run.lease); layered.MaxLanes != nil {
		t.Errorf("a review with no base is admitted under the lane cap %d its writer's layer sets", *layered.MaxLanes)
	}
}
