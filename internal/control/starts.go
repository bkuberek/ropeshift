package control

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

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
	// Bases answers what git says of a repository.
	Bases ports.Bases
	// Checkout names the directory a repository's lanes branch from, by its project and the repository's
	// identity: the checkout the worker on this machine keeps.
	Checkout func(ctx context.Context, project ledger.WorkspaceID, repository string) (string, error)
}

// dependencyWork is the accepted work of one dependency in the same repository.
type dependencyWork struct {
	WorkOrder string
	// Branch is the branch its lane left, and Commit the commit a member accepted on it.
	Branch string
	Commit string
	// Merged is the commit ropeshift's merge of it made on the base, when ropeshift merged it. A squash or a
	// rebase leaves the base without Commit, so the base holds the work when it holds this one.
	Merged string
}

// startInput is the ledger's side of a decision.
type startInput struct {
	order workorders.WorkOrder
	// base is the branch to start from: the work order's, else the repository's. Empty names neither,
	// and git's default branch stands in.
	base         string
	startFrom    ledger.StartFrom
	dependencies []dependencyWork
	// reads are the other repositories of the project the lane reads, read-only (ADR-0022).
	reads []readWant
}

// readWant is one repository a lane reads, before git says where it stands: a repository of the project,
// with the commits its lane's dependencies there merged as, or a name the project does not hold, with why.
type readWant struct {
	record ledger.Repository
	name   string
	merged []string
	detail string
}

// readsOf names the repositories a work order's lane reads beside its own (ADR-0022). A planning lane reads
// every other active repository of its project, so it plans against the code as it is. A lane that changes
// code reads the repositories its blocking dependencies target and those its contract lists in `reads`. A
// lane never reads a repository of another project, because the ledger it reads from holds only this
// project's, and a name it does not hold is read as one with no repository.
func readsOf(tx ports.ReadTx, order workorders.WorkOrder) ([]readWant, error) {
	var out []readWant
	var err error
	switch order.Kind {
	case workorders.KindPlan:
		out, err = everyOtherRepository(tx.Repositories(), order)
	case workorders.KindChange, workorders.KindRework:
		out, err = changeReads(tx, order)
	}
	slices.SortFunc(out, func(a, b readWant) int {
		return cmp.Or(strings.Compare(a.name, b.name), strings.Compare(a.record.ID, b.record.ID))
	})
	return out, err
}

func everyOtherRepository(repositories ports.RepositoryReader, order workorders.WorkOrder) ([]readWant, error) {
	all, err := repositories.Repositories()
	if err != nil {
		return nil, err
	}
	var out []readWant
	for _, record := range all {
		if record.ID != order.RepositoryID && !record.Removed() {
			out = append(out, readWant{record: record, name: record.Name})
		}
	}
	return out, nil
}

// changeReads are the repositories a lane that changes code reads: each one a blocking dependency targets,
// with the commit that dependency merged as when ropeshift recorded one, and each one its contract lists in
// `reads`. Its own repository is its worktree, and a repository the project removed is read by no lane.
func changeReads(tx ports.ReadTx, order workorders.WorkOrder) ([]readWant, error) {
	wants := map[string]*readWant{}
	want := func(record ledger.Repository) *readWant {
		if wants[record.ID] == nil {
			wants[record.ID] = &readWant{record: record, name: record.Name}
		}
		return wants[record.ID]
	}
	edges, err := tx.Planning().Dependencies(order.ID)
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		if !edge.Kind.Blocks() {
			continue
		}
		dependency, found, err := dependencyOrder(tx.Planning(), tx.WorkOrders(), edge)
		if err != nil {
			return nil, err
		}
		if !found || dependency.RepositoryID == "" || dependency.RepositoryID == order.RepositoryID {
			continue
		}
		record, err := tx.Repositories().Repository(dependency.RepositoryID)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && record.Removed()) {
			continue
		}
		if err != nil {
			return nil, err
		}
		read := want(record)
		merged, err := mergedAs(tx, dependency)
		if err != nil {
			return nil, err
		}
		if merged != "" && !slices.Contains(read.merged, merged) {
			read.merged = append(read.merged, merged)
		}
	}
	if order.ContractVersion != 0 {
		contract, err := tx.Planning().Contract(order.ContractID, order.ContractVersion)
		if err != nil {
			return nil, err
		}
		for _, name := range contract.Reads {
			record, err := tx.Repositories().Named(name)
			switch {
			case errors.Is(err, ports.ErrNotFound) || (err == nil && record.Removed()):
				wants["\x00"+name] = &readWant{name: name, detail: "the project holds no repository named " + name}
			case err != nil:
				return nil, err
			case record.ID != order.RepositoryID:
				want(record)
			}
		}
	}
	out := make([]readWant, 0, len(wants))
	for _, read := range wants {
		out = append(out, *read)
	}
	return out, nil
}

// mergedAs is the commit ropeshift's merge of a dependency's accepted work made, or empty when it recorded
// none: a person merged it by hand, or it has not merged.
func mergedAs(tx ports.ReadTx, dependency workorders.WorkOrder) (string, error) {
	artifacts, err := tx.Evidence().Artifacts(dependency.ID)
	if err != nil {
		return "", err
	}
	branch := attemptBranch(artifacts, dependency.Fence)
	if branch.Digest == "" {
		return "", nil
	}
	records, err := tx.Forge().Integrations(dependency.ID)
	if err != nil {
		return "", err
	}
	var merged string
	for _, record := range records {
		if record.Merged() && record.ApprovedCommit == branch.Digest && record.MergedCommit != "" {
			merged = record.MergedCommit
		}
	}
	return merged, nil
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
			work := dependencyWork{WorkOrder: dependency.ID, Branch: branch.URI, Commit: branch.Digest}
			if work.Merged, err = mergedAs(tx, dependency); err != nil {
				return in, err
			}
			in.dependencies = append(in.dependencies, work)
		}
	}
	slices.SortFunc(in.dependencies, func(a, b dependencyWork) int { return strings.Compare(a.WorkOrder, b.WorkOrder) })
	in.reads, err = readsOf(tx, order)
	return in, err
}

// pass remembers what git said in one pass, so a pass over many ready work orders fetches a branch once
// and finds a repository's checkout once.
type pass struct {
	// project is the project whose work the pass places, which names its checkouts.
	project   ledger.WorkspaceID
	tips      map[string]tipRead
	checkouts map[string]checkoutRead
}

type tipRead struct {
	tip ports.BaseTip
	err error
}

type checkoutRead struct {
	path string
	err  error
}

func newPass(project ledger.WorkspaceID) *pass {
	return &pass{project: project, tips: map[string]tipRead{}, checkouts: map[string]checkoutRead{}}
}

func (p *pass) tip(ctx context.Context, bases ports.Bases, path, branch string) (ports.BaseTip, error) {
	key := path + "\x00" + branch
	if known, ok := p.tips[key]; ok {
		return known.tip, known.err
	}
	tip, err := bases.BaseTip(ctx, path, branch)
	p.tips[key] = tipRead{tip: tip, err: err}
	return tip, err
}

func (p *pass) checkout(ctx context.Context, find func(context.Context, ledger.WorkspaceID, string) (string, error), repository string) (string, error) {
	if known, ok := p.checkouts[repository]; ok {
		return known.path, known.err
	}
	path, err := find(ctx, p.project, repository)
	p.checkouts[repository] = checkoutRead{path: path, err: err}
	return path, err
}

func unavailable(format string, args ...any) *workorders.StartHold {
	return &workorders.StartHold{Reason: workorders.HoldBaseUnavailable, Detail: fmt.Sprintf(format, args...)}
}

// decide gives the start, with what the lane reads, or the reason there is none yet.
func (s *Starts) decide(ctx context.Context, in startInput, seen *pass) (workorders.Start, *workorders.StartHold) {
	start, hold := s.place(ctx, in, seen)
	if hold != nil || len(in.reads) == 0 {
		return start, hold
	}
	start.Reads = s.readsAt(ctx, in.reads, seen)
	return start, nil
}

// readsAt records where each repository a lane reads stands, as the same pass reads every tip: at the merge
// of its dependency there, or at its base branch tip. A name the project does not hold, a repository with no
// checkout here, or a base that cannot be read is recorded with the reason and no commit: the lane still
// starts, and its brief says what it could not read.
func (s *Starts) readsAt(ctx context.Context, wants []readWant, seen *pass) []ledger.RepositoryRead {
	out := make([]ledger.RepositoryRead, 0, len(wants))
	for _, want := range wants {
		out = append(out, s.readAt(ctx, want, seen))
	}
	return out
}

func (s *Starts) readAt(ctx context.Context, want readWant, seen *pass) ledger.RepositoryRead {
	read := ledger.RepositoryRead{Repository: want.record.ID, Name: want.name, Detail: want.detail}
	if want.detail != "" {
		return read
	}
	path, err := seen.checkout(ctx, s.Checkout, want.record.ID)
	if err != nil {
		read.Detail = fmt.Sprintf("no checkout of it on this machine: %v", err)
		return read
	}
	base := want.record.Base()
	if base == "" {
		if base, err = s.Bases.DefaultBranch(ctx, path); err != nil || base == "" {
			read.Detail = fmt.Sprintf("it names no default branch: %v", err)
			return read
		}
	}
	// Reading the tip fetches the base, which brings the merges on it.
	tip, err := seen.tip(ctx, s.Bases, path, base)
	if err != nil {
		read.Detail = err.Error()
		return read
	}
	merge, err := s.latestMerge(ctx, path, want.merged)
	if err != nil {
		read.Detail = err.Error()
		return read
	}
	read.Ref, read.Commit = tip.Ref, cmp.Or(merge, tip.Commit)
	return read
}

// latestMerge is the merge that holds every other merge, or none when no merge holds them all, as for merges
// onto different branches.
func (s *Starts) latestMerge(ctx context.Context, path string, merged []string) (string, error) {
	for _, candidate := range merged {
		holdsAll := true
		for _, other := range merged {
			if other == candidate {
				continue
			}
			holds, err := s.Bases.Contains(ctx, path, other, candidate)
			if err != nil {
				return "", fmt.Errorf("reading whether %s holds %s: %w", short(candidate), short(other), err)
			}
			if !holds {
				holdsAll = false
				break
			}
		}
		if holdsAll {
			return candidate, nil
		}
	}
	return "", nil
}

// place gives where the lane's branch begins, or the reason there is none yet. It never answers with the
// checkout's HEAD.
func (s *Starts) place(ctx context.Context, in startInput, seen *pass) (workorders.Start, *workorders.StartHold) {
	path, err := seen.checkout(ctx, s.Checkout, in.order.RepositoryID)
	if err != nil {
		return workorders.Start{}, unavailable("no checkout of repository %s: %v", in.order.RepositoryID, err)
	}
	base := in.base
	if base == "" {
		if base, err = s.Bases.DefaultBranch(ctx, path); err != nil || base == "" {
			return workorders.Start{}, unavailable("the repository names no default branch: %v", err)
		}
	}
	tip, err := seen.tip(ctx, s.Bases, path, base)
	if err != nil {
		return workorders.Start{}, unavailable("%v", err)
	}
	if in.order.Continues != "" {
		return s.continuedStart(ctx, path, tip, in)
	}
	if len(in.dependencies) == 0 {
		return workorders.Start{Ref: tip.Ref, Commit: tip.Commit}, nil
	}
	if in.startFrom == ledger.StartFromAccepted {
		return s.acceptedStart(ctx, path, in.dependencies)
	}
	return s.mergedStart(ctx, path, tip, in.dependencies)
}

// continuedStart places a lane that finishes an in-flight branch (ADR-0023). Its branch begins at the
// continued branch's tip, and its base commit is where that branch left the base, so its review reads the
// whole change and not only the lane's commits. Dependencies wait for their merge into the base, as with
// `merged`, because the lane builds on the continued branch and not on accepted work. Nothing here moves
// the continued branch.
func (s *Starts) continuedStart(ctx context.Context, path string, tip ports.BaseTip, in startInput) (workorders.Start, *workorders.StartHold) {
	if len(in.dependencies) > 0 {
		if _, hold := s.mergedStart(ctx, path, tip, in.dependencies); hold != nil {
			return workorders.Start{}, hold
		}
	}
	branch, err := s.Bases.Branch(ctx, path, in.order.Continues)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return workorders.Start{}, &workorders.StartHold{
			Reason: workorders.HoldContinuedBranchMissing, Detail: fmt.Sprintf("the work continues branch %s: %v", in.order.Continues, err),
		}
	case err != nil:
		return workorders.Start{}, unavailable("reading branch %s: %v", in.order.Continues, err)
	}
	base, err := s.Bases.MergeBase(ctx, path, branch.Commit, tip.Commit)
	if err != nil {
		return workorders.Start{}, unavailable("finding where %s left %s: %v", branch.Ref, tip.Ref, err)
	}
	return workorders.Start{Ref: tip.Ref, Commit: base, ContinuesRef: branch.Ref, ContinuesCommit: branch.Commit}, nil
}

// mergedStart waits until every dependency's accepted commit is in the base, and then starts from the
// base, which holds them.
func (s *Starts) mergedStart(ctx context.Context, path string, tip ports.BaseTip, dependencies []dependencyWork) (workorders.Start, *workorders.StartHold) {
	for _, dependency := range dependencies {
		landed := cmp.Or(dependency.Merged, dependency.Commit)
		merged, err := s.Bases.Contains(ctx, path, landed, tip.Commit)
		if err != nil {
			return workorders.Start{}, unavailable("reading whether %s is in %s: %v", short(landed), tip.Ref, err)
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
			holds, err := s.Bases.Contains(ctx, path, other.Commit, candidate.Commit)
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
// have no new branch to place. A start that must wait is recorded and refused. A review starts at the
// commit it judges, which the ledger holds, so it needs no git and no Starts.
func (l *Leases) startOf(ctx context.Context, scope ledger.WorkspaceID, request workorders.ClaimRequest, actor ledger.Actor) (workorders.Start, error) {
	var in startInput
	var needed, fromLedger bool
	var start workorders.Start
	var hold *workorders.StartHold
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
		in.order = order
		if order.Kind == workorders.KindReview {
			fromLedger = true
			start, hold, err = reviewStart(tx, order)
			return err
		}
		if l.starts == nil {
			return nil
		}
		needed = true
		in, err = startFacts(tx, order)
		return err
	})
	if err != nil {
		return workorders.Start{}, err
	}
	if needed {
		start, hold = l.starts.decide(ctx, in, newPass(scope))
	} else if !fromLedger {
		return workorders.Start{}, nil
	}
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
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		changed, err = holdIn(tx, current, want, actor)
		return err
	})
	return changed, err
}

// RefreshStarts reads again what ready work waits for, so a person sees a reason before anyone claims
// and sees it go once the merge is in. It looks at the ready work that has accepted work to wait for in
// its repository, and at the work already held, and it fetches each base once for the whole pass. It
// returns the work orders whose reason changed. A held review reads again from the ledger alone, because
// a review starts at the commit it judges. It is a tick: the list is read with no lock, and each change is
// one transaction that reads again.
func (l *Leases) RefreshStarts(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	var inputs []startInput
	type reviewWait struct {
		id   string
		hold *workorders.StartHold
	}
	var reviews []reviewWait
	err := l.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State != workorders.Ready || row.RepositoryID == "" || row.ResumeLaneID != "" {
				continue
			}
			if row.Kind == workorders.KindReview {
				if row.StartHeld != nil {
					_, hold, err := reviewStart(tx, row)
					if err != nil {
						return err
					}
					reviews = append(reviews, reviewWait{id: row.ID, hold: hold})
				}
				continue
			}
			if l.starts == nil {
				continue
			}
			in, err := startFacts(tx, row)
			if err != nil {
				return err
			}
			if row.StartHeld != nil || len(in.dependencies) > 0 || row.Continues != "" {
				inputs = append(inputs, in)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, review := range reviews {
		moved, err := l.setHold(ctx, scope, review.id, review.hold, actor)
		if err != nil {
			return changed, err
		}
		if moved {
			changed = append(changed, review.id)
		}
	}
	seen := newPass(scope)
	for _, in := range inputs {
		// What a lane reads never holds it, so the tick places the start alone.
		_, hold := l.starts.place(ctx, in, seen)
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
