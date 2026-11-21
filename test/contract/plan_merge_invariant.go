package contract

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// holdingPlanRequests runs each check over stores that read, after every write, that no planning branch waits for an
// approval nothing can give: a `plan` work order in acceptance or integrating whose current commit intake took in as
// accepted has its merge approved at that commit by a plan approval, or a plan request for a version the ledger holds,
// naming the work order and that commit, that waits in Needs you, which lists every open plan request as an item, and
// is approvable: it lists nothing missing, its version holds the digest it was bound to, and that version is the plan's
// newest, which is the one a member approves. A request approved with no merge approval on record approved nothing, and
// leaves the branch with no item in Needs you. A commit intake has not taken in yet is read on the next tick, so it is
// held to this from then on. The first write after which it fails fails the check, naming the transaction, whatever the
// check itself went on to say.
func holdingPlanRequests(checks []Check) []Check {
	out := make([]Check, 0, len(checks))
	for _, check := range checks {
		run := check.Run
		check.Run = func(r *Rig) error {
			watch := &planRequestWatch{}
			wrap := r.Wrap
			r.Wrap = func(store ports.Store) ports.Store {
				if wrap != nil {
					store = wrap(store)
				}
				return planRequestStore{Store: store, watch: watch}
			}
			err := run(r)
			if broken := watch.broken(); broken != nil {
				return errors.Join(broken, err)
			}
			return err
		}
		out = append(out, check)
	}
	return out
}

// planRequestWatch keeps the first time the invariant failed.
type planRequestWatch struct {
	mu    sync.Mutex
	first error
}

func (w *planRequestWatch) note(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.first == nil {
		w.first = err
	}
}

func (w *planRequestWatch) broken() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.first
}

// planRequestStore reads the invariant after each write that succeeds.
type planRequestStore struct {
	ports.Store
	watch *planRequestWatch
}

func (s planRequestStore) Write(ctx context.Context, scope ledger.WorkspaceID, op string, fn func(ports.WriteTx) error) error {
	err := s.Store.Write(ctx, scope, op, fn)
	if err == nil {
		if broken := unaskedPlanBranches(ctx, s.Store, scope); broken != nil {
			s.watch.note(Violatedf("after the transaction %q: %v", op, broken))
		}
	}
	return err
}

// unaskedPlanBranches names each planning work order waiting at a commit intake took in, with no approval of its merge
// there, that no request a member can approve names, with the plan requests that name it and why each does not count.
func unaskedPlanBranches(ctx context.Context, store ports.Store, scope ledger.WorkspaceID) error {
	var stuck []string
	err := store.Read(ctx, scope, func(tx ports.ReadTx) error {
		orders, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		requests, err := tx.Approvals().All()
		if err != nil {
			return err
		}
		for _, order := range orders {
			if order.Kind != workorders.KindPlan || (order.State != workorders.Acceptance && order.State != workorders.Integrating) {
				continue
			}
			artifacts, err := tx.Evidence().Artifacts(order.ID)
			if err != nil {
				return err
			}
			commit := branchCommitAt(artifacts, order.Fence)
			if commit == "" || commit != order.PlanCommit {
				continue
			}
			merge, err := tx.Forge().Integration(ledger.IntegrationID(order.ID, commit))
			switch {
			case err == nil && merge.Plan != "":
				continue
			case err != nil && !errors.Is(err, ports.ErrNotFound):
				return err
			}
			var named []string
			asked := false
			for _, request := range requests {
				if request.Kind != authority.KindPlan || request.WorkOrder != order.ID {
					continue
				}
				at := requestedAt(request)
				why, err := uncounted(tx.Planning(), request, commit)
				if err != nil {
					return err
				}
				named = append(named, fmt.Sprintf("%s %s of %q version %d at %s%s", request.ID, request.State, request.Plan, request.PlanVersion, at, why))
				asked = asked || why == ""
			}
			if !asked {
				stuck = append(stuck, fmt.Sprintf("work order %s waits in %s at %s, which intake took in, and its plan requests are [%s]",
					order.ID, order.State, commit, strings.Join(named, "; ")))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(stuck) != 0 {
		return errors.New(strings.Join(stuck, "\n"))
	}
	return nil
}

// uncounted says why a plan request that names a planning work order does not stand for its merge at a commit, and is
// empty when it does: it is open in Needs you, and approving it would be taken.
func uncounted(planned ports.PlanningReader, request authority.Approval, commit string) (string, error) {
	switch {
	case !request.Live():
		return ", which is closed", nil
	case request.Plan == "":
		return ", for a branch no version claimed", nil
	case requestedAt(request) != commit:
		return ", for another commit", nil
	case request.State == authority.ApprovalApproved:
		return ", approved with no approval of the merge on record", nil
	case len(planDetailOf(request).Unfinished) != 0:
		return fmt.Sprintf(", which lists what is missing: %v", planDetailOf(request).Unfinished), nil
	}
	version, err := planned.PlanVersion(request.Plan, request.PlanVersion)
	if errors.Is(err, ports.ErrNotFound) {
		return ", for a version the ledger does not hold", nil
	}
	if err != nil {
		return "", err
	}
	if version.Digest != request.SubjectDigest {
		return fmt.Sprintf(", bound to %s where its version holds %s", request.SubjectDigest, version.Digest), nil
	}
	latest, err := planned.LatestPlanVersion(request.Plan)
	if err != nil {
		return "", err
	}
	if latest.Version != version.Version {
		return fmt.Sprintf(", for a version the plan's version %d replaced, so approving it is refused", latest.Version), nil
	}
	return "", nil
}

// branchCommitAt is the commit the newest branch an attempt reported holds.
func branchCommitAt(artifacts []ledger.Artifact, fence int64) string {
	commit := ""
	for _, artifact := range artifacts {
		if artifact.Kind == ledger.ArtifactBranch && artifact.Fence == fence {
			commit = artifact.Digest
		}
	}
	return commit
}

// requestedAt is the commit a plan request names: its source, or the commit of a file it names as "path@commit".
func requestedAt(request authority.Approval) string {
	source := planDetailOf(request).Source
	if at := strings.LastIndex(source, "@"); at >= 0 {
		return source[at+1:]
	}
	return source
}
