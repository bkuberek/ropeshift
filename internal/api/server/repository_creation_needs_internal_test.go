package server

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A member can retry or skip a creation that waits for them, and neither one that runs now: its item offers no action, and
// the item carries what the creation did before it stopped.
func TestACreationThatRunsOffersNoAction(t *testing.T) {
	creation := &ledger.CreationOutcome{Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: ledger.VisibilityPrivate, Remaining: []string{"git -C /work/api push"}}
	approval := authority.Approval{ID: "ap1", Plan: "tt", PlanVersion: 2}
	for state, want := range map[control.UncreatedState][]string{
		control.CreationRefused:     {api.ActRepositoryRetry, api.ActRepositorySkip},
		control.CreationFailed:      {api.ActRepositoryRetry, api.ActRepositorySkip},
		control.CreationInterrupted: {api.ActRepositoryRetry, api.ActRepositorySkip},
		control.CreationRunning:     {},
	} {
		item := creationNeedItem(control.UncreatedRepository{
			Approval: approval, Name: "api", ManifestPath: "repos/api", Path: "/work/repos/api", State: state, Reason: "push failed", Creation: creation, Since: 7,
		})
		need := item.RepositoryCreation
		if item.Kind != api.NeedRepositoryCreation || item.ID != "ap1/api" || item.Since != 7 || need == nil {
			t.Fatalf("%s: the item reads %+v", state, item)
		}
		if need.State != string(state) || need.Approval != "ap1" || need.Plan != "tt" || need.PlanVersion != 2 || need.Name != "api" ||
			need.ManifestPath != "repos/api" || need.Path != "/work/repos/api" || need.Reason != "push failed" || need.Creation != creation {
			t.Errorf("%s: the need reads %+v", state, need)
		}
		if need.Actions == nil || !slices.Equal(need.Actions, want) {
			t.Errorf("%s: the need offers %#v, want %#v", state, need.Actions, want)
		}
	}
}
