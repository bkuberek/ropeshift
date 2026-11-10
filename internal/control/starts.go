package control

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Starts decides where a lane's branch begins, before the claim that dispatches it (FR-BASE-1 to
// FR-BASE-4). The ledger knows which work depends on which, what each accepted commit is, and what a
// member said of the repository. Git knows where the base branch stands and whether one commit holds
// another, so the decision reads git only through the worktree port, and the same facts and the same
// answers give the same start.
type Starts struct {
	// Worktrees answers what git says of a repository.
	Worktrees ports.Worktrees
	// Checkout names the directory a repository's lanes branch from, by the repository's identity.
	Checkout func(ctx context.Context, repository string) (string, error)
}

// dependencyWork is the accepted work of one dependency in the same repository.
type dependencyWork struct {
	WorkOrder string
	// Branch is the branch its lane left, and Commit the commit a member accepted on it.
	Branch string
	Commit string
}

// startInput is the ledger's side of a decision.
type startInput struct {
	order workorders.WorkOrder
	// base is the branch to start from: the work order's, else the repository's. Empty names neither,
	// and git's default branch stands in.
	base         string
	startFrom    ledger.StartFrom
	dependencies []dependencyWork
}

// startFacts reads what the ledger says of where a work order should start. A dependency counts when it
// is blocking, in the same repository, accepted, and left a commit: any other gates readiness as it
// always did and moves nothing here.
func startFacts(tx ports.ReadTx, order workorders.WorkOrder) (startInput, error) {
	in := startInput{order: order, base: order.Base, startFrom: ledger.StartFromMerged}
	record, err := tx.Repositories().Repository(order.RepositoryID)
	switch {
	case err == nil:
		in.startFrom = record.StartFrom
		if in.base == "" {
			in.base = record.Base()
		}
	case !errors.Is(err, ports.ErrNotFound):
		return in, err
	}
	edges, err := tx.Planning().Dependencies(order.ID)
	if err != nil {
		return in, err
	}
	seen := map[string]bool{}
	for _, edge := range edges {
		if !edge.Kind.Blocks() {
			continue
		}
		dependency, found, err := dependencyOrder(tx.Planning(), tx.WorkOrders(), edge)
		if err != nil {
			return in, err
		}
		if !found || seen[dependency.ID] || dependency.RepositoryID != order.RepositoryID || dependency.State != workorders.Done {
			continue
		}
		seen[dependency.ID] = true
		artifacts, err := tx.Evidence().Artifacts(dependency.ID)
		if err != nil {
			return in, err
		}
		if branch := attemptBranch(artifacts, dependency.Fence); branch.Digest != "" {
			in.dependencies = append(in.dependencies, dependencyWork{WorkOrder: dependency.ID, Branch: branch.URI, Commit: branch.Digest})
		}
	}
	slices.SortFunc(in.dependencies, func(a, b dependencyWork) int { return strings.Compare(a.WorkOrder, b.WorkOrder) })
	return in, nil
}

// tipReads remembers where each base stood in one pass, so a pass over many ready work orders fetches
// a branch once.
type tipReads map[string]tipRead

type tipRead struct {
	tip ports.BaseTip
	err error
}

func (t tipReads) read(ctx context.Context, worktrees ports.Worktrees, path, branch string) (ports.BaseTip, error) {
	key := path + "\x00" + branch
	if known, ok := t[key]; ok {
		return known.tip, known.err
	}
	tip, err := worktrees.BaseTip(ctx, path, branch)
	t[key] = tipRead{tip: tip, err: err}
	return tip, err
}

func unavailable(format string, args ...any) *workorders.StartHold {
	return &workorders.StartHold{Reason: workorders.HoldBaseUnavailable, Detail: fmt.Sprintf(format, args...)}
}

// decide gives the start, or the reason there is none yet. It never answers with the checkout's HEAD.
func (s *Starts) decide(ctx context.Context, in startInput, tips tipReads) (workorders.Start, *workorders.StartHold) {
	path, err := s.Checkout(ctx, in.order.RepositoryID)
	if err != nil {
		return workorders.Start{}, unavailable("no checkout of repository %s: %v", in.order.RepositoryID, err)
	}
	base := in.base
	if base == "" {
		if base, err = s.Worktrees.DefaultBranch(ctx, path); err != nil || base == "" {
			return workorders.Start{}, unavailable("the repository names no default branch: %v", err)
		}
	}
	tip, err := tips.read(ctx, s.Worktrees, path, base)
	if err != nil {
		return workorders.Start{}, unavailable("%v", err)
	}
	if len(in.dependencies) == 0 {
		return workorders.Start{Ref: tip.Ref, Commit: tip.Commit}, nil
	}
	if in.startFrom == ledger.StartFromAccepted {
		return s.acceptedStart(ctx, path, in.dependencies)
	}
	return s.mergedStart(ctx, path, tip, in.dependencies)
}

// mergedStart waits until every dependency's accepted commit is in the base, and then starts from the
// base, which holds them.
func (s *Starts) mergedStart(ctx context.Context, path string, tip ports.BaseTip, dependencies []dependencyWork) (workorders.Start, *workorders.StartHold) {
	for _, dependency := range dependencies {
		merged, err := s.Worktrees.Contains(ctx, path, dependency.Commit, tip.Commit)
		if err != nil {
			return workorders.Start{}, unavailable("reading whether %s is in %s: %v", short(dependency.Commit), tip.Ref, err)
		}
		if !merged {
			return workorders.Start{}, &workorders.StartHold{
				Reason: workorders.HoldWaitingForMerge, Dependency: dependency.WorkOrder,
				Detail: fmt.Sprintf("its work, %s, is accepted and not yet in %s", short(dependency.Commit), tip.Ref),
			}
		}
	}
	return workorders.Start{Ref: tip.Ref, Commit: tip.Commit}, nil
}

// acceptedStart starts from the accepted commit that holds every other dependency's. Dependencies on
// separate lines of work have no such commit, and the work waits for a person to join them.
func (s *Starts) acceptedStart(ctx context.Context, path string, dependencies []dependencyWork) (workorders.Start, *workorders.StartHold) {
	for _, candidate := range dependencies {
		holdsAll := true
		for _, other := range dependencies {
			holds, err := s.Worktrees.Contains(ctx, path, other.Commit, candidate.Commit)
			if err != nil {
				return workorders.Start{}, unavailable("reading whether %s holds %s: %v", short(candidate.Commit), short(other.Commit), err)
			}
			if !holds {
				holdsAll = false
				break
			}
		}
		if holdsAll {
			return workorders.Start{Ref: candidate.Branch, Commit: candidate.Commit}, nil
		}
	}
	names := make([]string, len(dependencies))
	for i, dependency := range dependencies {
		names[i] = dependency.WorkOrder
	}
	return workorders.Start{}, &workorders.StartHold{
		Reason: workorders.HoldDependenciesDiverge,
		Detail: "the accepted work of " + strings.Join(names, ", ") + " is on separate lines, and none of it holds the others",
	}
}

func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}

// Starting makes claims in repositories resolve where their lanes begin. Without it a claim records no
// start, and a worker begins the branch where its checkout is.
func (l *Leases) Starting(starts *Starts) *Leases {
	l.starts = starts
	return l
}

// startOf resolves the start of a claim before the claim's transaction, because git can take as long as
// a network does and a transaction holds the workspace. Work that is not ready, has no repository, or
// brings a parked lane back resolves nothing here: the claim itself refuses the first, and the others
// have no new branch to place. A start that must wait is recorded and refused.
func (l *Leases) startOf(ctx context.Context, scope ledger.WorkspaceID, request workorders.ClaimRequest, actor ledger.Actor) (workorders.Start, error) {
	if l.starts == nil {
		return workorders.Start{}, nil
	}
	var in startInput
	var needed bool
	err := l.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		if _, err := tx.Claims().Find(request.ClaimID); err == nil || !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		order, err := tx.WorkOrders().Get(request.WorkOrder)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if order.State != workorders.Ready || order.RepositoryID == "" || order.ResumeLaneID != "" {
			return nil
		}
		needed = true
		in, err = startFacts(tx, order)
		return err
	})
	if err != nil || !needed {
		return workorders.Start{}, err
	}
	start, hold := l.starts.decide(ctx, in, tipReads{})
	crashpoint.Hit("claim", "resolved")
	if hold != nil {
		if _, err := l.setHold(ctx, scope, in.order.ID, hold, actor); err != nil {
			return workorders.Start{}, err
		}
		return workorders.Start{}, *hold
	}
	return start, nil
}

func sameHold(a, b *workorders.StartHold) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// setHold records the reason ready work is not claimed, or takes it back when want is nil. It records a
// reason once: the same reason again appends nothing, so a claim retried every few seconds leaves one
// event.
func (l *Leases) setHold(ctx context.Context, scope ledger.WorkspaceID, id string, want *workorders.StartHold, actor ledger.Actor) (bool, error) {
	var changed bool
	err := l.store.Write(ctx, scope, "claim.hold", func(tx ports.WriteTx) error {
		changed = false
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		if current.State != workorders.Ready || sameHold(current.StartHeld, want) {
			return nil
		}
		if err := tx.WorkOrders().Hold(id, want); err != nil {
			return err
		}
		eventType := workorders.EventStartResolved
		if want != nil {
			eventType = workorders.EventStartHeld
		}
		event, err := workorders.ChangeEvent(eventType, id, workorders.Change{Hold: want}, actor)
		if err != nil {
			return err
		}
		if _, err = tx.Append(event); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

// RefreshStarts reads again what ready work waits for, so a person sees a reason before anyone claims
// and sees it go once the merge is in. It looks at the ready work that has accepted work to wait for in
// its repository, and at the work already held, and it fetches each base once for the whole pass. It
// returns the work orders whose reason changed. It is a tick: the list is read with no lock, and each
// change is one transaction that reads again.
func (l *Leases) RefreshStarts(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	if l.starts == nil {
		return nil, nil
	}
	var inputs []startInput
	err := l.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State != workorders.Ready || row.RepositoryID == "" || row.ResumeLaneID != "" {
				continue
			}
			in, err := startFacts(tx, row)
			if err != nil {
				return err
			}
			if row.StartHeld != nil || len(in.dependencies) > 0 {
				inputs = append(inputs, in)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	tips := tipReads{}
	var changed []string
	for _, in := range inputs {
		_, hold := l.starts.decide(ctx, in, tips)
		moved, err := l.setHold(ctx, scope, in.order.ID, hold, actor)
		if err != nil {
			return changed, err
		}
		if moved {
			changed = append(changed, in.order.ID)
		}
	}
	return changed, nil
}
