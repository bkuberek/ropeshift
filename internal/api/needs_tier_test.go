package api_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	authz "ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/questions"
)

// Needs you sorts by importance first: what holds a running session, then what stopped work, then what waits for a
// decision, then what can wait. The tier comes from an item's kind and state alone.
func TestATierSaysHowMuchAnItemHolds(t *testing.T) {
	batch := func(blocking bool) *api.QuestionBatch {
		return &api.QuestionBatch{Items: []api.QuestionItem{{Item: questions.Item{ID: "q1"}}, {Item: questions.Item{ID: "q2", Blocking: blocking}}}}
	}
	approval := func(kind string) *authz.Approval { return &authz.Approval{Kind: kind} }
	work := func(reason string) *api.WorkNeed { return &api.WorkNeed{Reason: reason} }
	for name, tc := range map[string]struct {
		item api.NeedItem
		want int
	}{
		"a call a lane waits to run":           {api.NeedItem{Kind: api.NeedApproval, Approval: approval(authz.KindToolCall)}, api.TierRunningSession},
		"an artifact a lane waits to write":    {api.NeedItem{Kind: api.NeedApproval, Approval: approval(authz.KindArtifact)}, api.TierRunningSession},
		"a question that blocks its lane":      {api.NeedItem{Kind: api.NeedQuestions, Batch: batch(true)}, api.TierRunningSession},
		"questions that block nothing":         {api.NeedItem{Kind: api.NeedQuestions, Batch: batch(false)}, api.TierDecision},
		"a recovery act":                       {api.NeedItem{Kind: api.NeedApproval, Approval: approval(authz.KindRecovery)}, api.TierStopped},
		"a merge":                              {api.NeedItem{Kind: api.NeedApproval, Approval: approval(authz.KindMerge)}, api.TierDecision},
		"a contract":                           {api.NeedItem{Kind: api.NeedApproval, Approval: approval(authz.KindContract)}, api.TierDecision},
		"failed work":                          {api.NeedItem{Kind: api.NeedWork, Work: work(api.ReasonFailed)}, api.TierStopped},
		"tripped work":                         {api.NeedItem{Kind: api.NeedWork, Work: work(api.ReasonTripped)}, api.TierStopped},
		"a parked lane that waits on a person": {api.NeedItem{Kind: api.NeedWork, Work: work(api.ReasonResume)}, api.TierStopped},
		"work that waits for acceptance":       {api.NeedItem{Kind: api.NeedWork, Work: work(api.ReasonAcceptance)}, api.TierDecision},
		"a design history check":               {api.NeedItem{Kind: api.NeedWork, Work: work(api.ReasonHistoryUnread)}, api.TierLater},
		"a design history that went missing":   {api.NeedItem{Kind: api.NeedWork, Work: work(api.ReasonHistoryMissing)}, api.TierLater},
		"an uncertain effect":                  {api.NeedItem{Kind: api.NeedEffect}, api.TierStopped},
		"a conflict that holds a merge":        {api.NeedItem{Kind: api.NeedConflict}, api.TierStopped},
		"a spend stop":                         {api.NeedItem{Kind: api.NeedSpend}, api.TierStopped},
		"CI that asks a person":                {api.NeedItem{Kind: api.NeedCI}, api.TierStopped},
		"a repository a plan did not create":   {api.NeedItem{Kind: api.NeedRepositoryCreation}, api.TierStopped},
		"a plan approval":                      {api.NeedItem{Kind: api.NeedPlanApproval}, api.TierDecision},
		"a plan revision":                      {api.NeedItem{Kind: api.NeedPlanRevision}, api.TierDecision},
		"a contract review":                    {api.NeedItem{Kind: api.NeedContractReview}, api.TierDecision},
		"a confirmation card":                  {api.NeedItem{Kind: api.NeedConfirm}, api.TierDecision},
		"a kind this build does not know":      {api.NeedItem{Kind: "something_new"}, api.TierDecision},
	} {
		t.Run(name, func(t *testing.T) {
			if got := api.TierOf(tc.item); got != tc.want {
				t.Errorf("tier %d, want %d", got, tc.want)
			}
		})
	}
}
