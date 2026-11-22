package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// CloneEditsLimit is how many paths of one clone a reading lists.
const CloneEditsLimit = 500

// FreshLimit is how long a reading a member asks for may take, unless Bounding says another.
const FreshLimit = 2 * time.Minute

// OutsideEdits reads what each registered repository's clone on this machine holds that intake and lanes cannot read,
// with the steps that make it readable, and finds each lane whose branch changed a path a person changed too (FR-UI-14).
// It reads the clones through the ports it is given and changes nothing. What it last found of each project's
// conflicts stays in memory for Needs you, which reads no clone.
type OutsideEdits struct {
	starts *Starts
	edits  ports.CloneEdits
	// prefix is what the name of a lane's branch begins with, before the lane's ID.
	prefix string
	// reuse is how long a reading answers Recent, and now the clock it is timed by.
	reuse time.Duration
	now   func() time.Time
	// limit bounds a reading a member asks for with Fresh.
	limit time.Duration
	// begun numbers the readings in the order they began, so an older one never replaces what a newer one found.
	begun atomic.Int64

	mu     sync.Mutex
	found  map[ledger.WorkspaceID]map[string]Conflict
	latest map[ledger.WorkspaceID]int64
	recent map[ledger.WorkspaceID]recentReading
	// running and waiting are each project's reading a member asked for that is under way, and the one that waits for it.
	running map[ledger.WorkspaceID]*freshTurn
	waiting map[ledger.WorkspaceID]*freshTurn
}

// freshTurn is one reading a member asked for, shared by every call that arrived before it began.
type freshTurn struct {
	ctx     context.Context
	store   ports.Store
	scope   ledger.WorkspaceID
	project string
	done    chan struct{}
	reading OutsideReading
	err     error
}

// recentReading is a project's newest reading, with when it began.
type recentReading struct {
	project string
	began   time.Time
	number  int64
	reading OutsideReading
}

// NewOutsideEdits reads the clone of each repository where starts places its lanes, through edits. A lane's branch is
// the one its work order keeps, or else prefix and the lane's ID.
func NewOutsideEdits(starts *Starts, edits ports.CloneEdits, prefix string) *OutsideEdits {
	return &OutsideEdits{
		starts: starts, edits: edits, prefix: prefix, now: time.Now, limit: FreshLimit,
		found: map[ledger.WorkspaceID]map[string]Conflict{}, latest: map[ledger.WorkspaceID]int64{}, recent: map[ledger.WorkspaceID]recentReading{},
		running: map[ledger.WorkspaceID]*freshTurn{}, waiting: map[ledger.WorkspaceID]*freshTurn{},
	}
}

// Reusing lets Recent answer with a reading that began less than window ago, so a page that asks often runs git no
// more often than a tick does.
func (o *OutsideEdits) Reusing(window time.Duration) *OutsideEdits {
	o.reuse = window
	return o
}

// Bounding sets how long a reading a member asks for with Fresh may take.
func (o *OutsideEdits) Bounding(limit time.Duration) *OutsideEdits {
	o.limit = limit
	return o
}

// CloneReading is one repository's clone as a reading found it.
type CloneReading struct {
	Repository ledger.Repository
	// Planning says the project plans in this repository.
	Planning bool
	// Path is the clone's top on this machine, and empty when it keeps none.
	Path  string
	Edits ledger.CloneEdits
	Steps []ledger.EditStep
	// Problem says why the clone was not read, and is empty when it was.
	Problem string
}

// Conflict is a lane whose branch changed paths that a person changed too, in their clone or in pushes to the base,
// for a member to settle. Ropeshift resolves none of it and overwrites neither side.
type Conflict struct {
	WorkOrder  workorders.WorkOrder
	Lane       string
	Branch     string
	Repository ledger.Repository
	Clone      string
	// Base is the ref the base was read at, such as origin/main.
	Base  string
	Paths []ledger.ConflictPath
	// Cut says git listed more paths of a side than a reading keeps, so Paths may not be every one.
	Cut bool
	// Options are the ways to settle it, each with its exact commands, and Clears says what ends it (FR-UI-13).
	Options []ledger.EditStep
	Clears  string
	// Since is when a reading of this process first found the conflict.
	Since int64
}

// OutsideReading is every registered repository's clone, and every conflict, read at one time.
type OutsideReading struct {
	At        int64
	Clones    []CloneReading
	Conflicts []Conflict
}

// openStates are the states in which a work order's lane holds a branch whose work is not merged yet.
var openStates = []workorders.State{
	workorders.Claimed, workorders.Running, workorders.Parked, workorders.Lapsed, workorders.Acceptance, workorders.Integrating, workorders.Blocked,
}

// ConflictStillOpen says a work order a reading found a conflict of is still in a state whose lane's branch holds
// unmerged work, so its conflict still waits for a member.
func ConflictStillOpen(order workorders.WorkOrder) bool {
	return slices.Contains(openStates, order.State)
}

// endedStates are the states in which a work order's lane has ended and its work waits, so it goes back for another
// attempt rather than taking a message.
var endedStates = []workorders.State{workorders.Acceptance, workorders.Integrating}

// sessionStates are the states in which a work order's lane has a session that takes a message.
var sessionStates = []workorders.State{workorders.Claimed, workorders.Running, workorders.Parked}

// openLane is a work order whose lane holds a branch of unmerged work, with the branch and where it began.
type openLane struct {
	order  workorders.WorkOrder
	lane   string
	branch string
	base   string
}

// ledgerFacts is what a reading takes from the ledger, in one read.
type ledgerFacts struct {
	at           int64
	repositories []ledger.Repository
	planning     string
	lanes        []openLane
	// landed are the commits the ledger records as the forge's merge of a work order, each merged pull request's head, and
	// the ranges a merge recorded as the commits it wrote, which carry no trailer of ropeshift's and are no person's push,
	// and the merge commit of each merged pull request that recorded no range.
	landed ledger.Landed
}

func readFacts(tx ports.ReadTx, prefix string) (ledgerFacts, error) {
	var facts ledgerFacts
	var err error
	if facts.at, err = tx.Now(); err != nil {
		return facts, err
	}
	all, err := tx.Repositories().Repositories()
	if err != nil {
		return facts, err
	}
	for _, record := range all {
		if !record.Removed() {
			facts.repositories = append(facts.repositories, record)
		}
	}
	switch planning, err := PlanningIn(tx.Repositories()); {
	case err == nil:
		facts.planning = planning.ID
	case !errors.Is(err, ports.ErrNotFound):
		return facts, err
	}
	if facts.lanes, err = openLanes(tx, prefix); err != nil {
		return facts, err
	}
	pulls, err := tx.Forge().PullRequests()
	if err != nil {
		return facts, err
	}
	laned := map[string]bool{}
	for _, lane := range facts.lanes {
		laned[lane.order.RepositoryID] = true
	}
	for _, pull := range pulls {
		if pull.MergeCommit == "" {
			continue
		}
		facts.landed.Commits = append(facts.landed.Commits, pull.MergeCommit)
		if pull.HeadCommit != "" {
			facts.landed.Commits = append(facts.landed.Commits, pull.HeadCommit)
		}
		// A range matters only to a repository with a lane open, whose branch a reading compares with the base.
		if !laned[pull.Repository] {
			continue
		}
		spans, err := landedRanges(tx, pull.WorkOrder)
		if err != nil {
			return facts, err
		}
		facts.landed.Ranges = append(facts.landed.Ranges, spans...)
		// A merge made on the forge by a person, or one whose range was not kept, has none.
		if !slices.ContainsFunc(spans, func(span ledger.LandedRange) bool { return span.After == pull.MergeCommit }) {
			facts.landed.Unranged = append(facts.landed.Unranged, pull.MergeCommit)
		}
	}
	return facts, nil
}

// landedRanges reads the ranges of a base's first-parent line that the merges of a work order recorded as written.
func landedRanges(tx ports.ReadTx, workOrder string) ([]ledger.LandedRange, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectIntegration, ID: workOrder})
	if err != nil {
		return nil, err
	}
	var out []ledger.LandedRange
	for _, event := range events {
		if event.Type != ledger.EventIntegrationMerged {
			continue
		}
		var merged mergedWork
		if json.Unmarshal(event.Data, &merged) != nil || merged.Landed == nil {
			continue
		}
		if ledger.CommitHash(merged.Landed.Before) && ledger.CommitHash(merged.Landed.After) {
			out = append(out, *merged.Landed)
		}
	}
	return out, nil
}

// openLanes lists each work order in an open state that has a lane, with the lane's branch and the commit it began at.
func openLanes(tx ports.ReadTx, prefix string) ([]openLane, error) {
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return nil, err
	}
	newest := map[string]ledger.LaneRow{}
	for _, row := range rows {
		if held, ok := newest[row.WorkOrder]; !ok || held.StartedFence <= row.StartedFence {
			newest[row.WorkOrder] = row
		}
	}
	var out []openLane
	for _, order := range orders {
		row, ok := newest[order.ID]
		if !ok || order.RepositoryID == "" || !slices.Contains(openStates, order.State) {
			continue
		}
		lane := openLane{order: order, lane: row.ID, branch: cmp.Or(order.Branch, prefix+row.ID)}
		switch claim, err := tx.Claims().OfLane(row.ID); {
		case err == nil:
			lane.base = claim.BaseCommit
		case !errors.Is(err, ports.ErrNotFound):
			return nil, err
		}
		out = append(out, lane)
	}
	return out, nil
}

// stampOf names the branches a reading's steps make by the time it was read, in UTC.
func stampOf(at int64) string { return time.UnixMilli(at).UTC().Format("20060102-150405") }

// Recent answers a reading of a project that began within the window Reusing set, and reads the clones again when none
// did.
func (o *OutsideEdits) Recent(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, project string) (OutsideReading, error) {
	if o.reuse > 0 {
		o.mu.Lock()
		held, ok := o.recent[scope]
		o.mu.Unlock()
		if ok && held.project == project && o.now().Sub(held.began) < o.reuse {
			return held.reading, nil
		}
	}
	return o.Read(ctx, store, scope, project)
}

// Fresh reads the clones again, however recent the reading Reusing lets Recent answer, for a member who asked to read
// again. A project has at most one such reading under way and one waiting for it: the calls that arrive while one runs
// share the next, which begins after they arrived, so none answers with a reading older than itself and a flood of calls
// runs git twice. A reading ends at the bound Bounding sets, and a call whose context ends stops waiting and leaves the
// reading to the calls that still do.
func (o *OutsideEdits) Fresh(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, project string) (OutsideReading, error) {
	o.mu.Lock()
	turn := o.waiting[scope]
	if turn == nil {
		turn = &freshTurn{ctx: context.WithoutCancel(ctx), store: store, scope: scope, project: project, done: make(chan struct{})}
		if o.running[scope] == nil {
			o.running[scope] = turn
			go o.take(turn)
		} else {
			o.waiting[scope] = turn
		}
	}
	o.mu.Unlock()
	select {
	case <-turn.done:
		return turn.reading, turn.err
	case <-ctx.Done():
		return OutsideReading{}, ctx.Err()
	}
}

// take runs a turn's reading within the bound, answers the calls that wait on it, and begins the turn that waited.
func (o *OutsideEdits) take(turn *freshTurn) {
	bounded, cancel := context.WithTimeout(turn.ctx, o.limit)
	turn.reading, turn.err = o.Read(bounded, turn.store, turn.scope, turn.project)
	cancel()
	if errors.Is(turn.err, context.DeadlineExceeded) {
		turn.err = fmt.Errorf("reading the clones took longer than %s: %w", o.limit, turn.err)
	}
	o.mu.Lock()
	next := o.waiting[turn.scope]
	delete(o.waiting, turn.scope)
	if next != nil {
		o.running[turn.scope] = next
	} else {
		delete(o.running, turn.scope)
	}
	o.mu.Unlock()
	close(turn.done)
	if next != nil {
		go o.take(next)
	}
}

// Read reads the clone of each of a project's repositories, and says of each clone it could not read why. project is
// the project's name, which the commands of the command line give. A reading whose context ended keeps nothing it
// found, and one that began before another kept its conflicts replaces none of them.
func (o *OutsideEdits) Read(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, project string) (OutsideReading, error) {
	number, began := o.begun.Add(1), o.now()
	var facts ledgerFacts
	if err := store.Read(ctx, scope, func(tx ports.ReadTx) (err error) {
		facts, err = readFacts(tx, o.prefix)
		return err
	}); err != nil {
		return OutsideReading{}, err
	}
	out := OutsideReading{At: facts.at, Clones: []CloneReading{}, Conflicts: []Conflict{}}
	unread := map[string]bool{}
	for _, record := range facts.repositories {
		reading, failed, err := o.readClone(ctx, scope, project, record, facts)
		if err != nil {
			return OutsideReading{}, err
		}
		out.Clones = append(out.Clones, reading)
		if failed {
			for _, lane := range facts.lanes {
				if lane.order.RepositoryID == record.ID {
					unread[lane.order.ID] = true
				}
			}
			continue
		}
		if reading.Problem != "" {
			continue
		}
		conflicts, err := o.conflictsIn(ctx, project, reading, facts, unread)
		if err != nil {
			return OutsideReading{}, err
		}
		out.Conflicts = append(out.Conflicts, conflicts...)
	}
	if err := ctx.Err(); err != nil {
		return OutsideReading{}, err
	}
	out.Conflicts = o.remember(scope, number, out.Conflicts, unread, facts.at)
	o.mu.Lock()
	if held, ok := o.recent[scope]; !ok || held.number < number {
		o.recent[scope] = recentReading{project: project, began: began, number: number, reading: out}
	}
	o.mu.Unlock()
	return out, nil
}

// conflictsIn finds each open lane of a repository whose branch in the clone changed a path the person changed too.
// The base's side leaves out the commits that carry a trailer of ropeshift's, those the ledger names as landed, and
// those in a range it records as landed, so the work that ropeshift or the forge merged is no person's. A rebase on the
// forge that a person merged records no range, and the commits it wrote carry no trailer, so in a repository whose merge
// method is rebase the base's side is not shown while the merge commit of such a pull request lies between the lane's
// branch and the base.
// The clone's side is settled once the lane's branch holds the branch the person's files are committed on, which
// EditsBranch names. A lane whose branch the clone really does not hold, as one on another machine's worker, has none.
// A lane git could not read is marked unread, so the conflict an earlier reading found stays. Only a reading the control
// plane cannot make at all fails.
func (o *OutsideEdits) conflictsIn(ctx context.Context, project string, clone CloneReading, facts ledgerFacts, unread map[string]bool) ([]Conflict, error) {
	var out []Conflict
	base := cmp.Or(clone.Repository.BaseBranch, clone.Repository.DefaultBranch)
	for _, lane := range facts.lanes {
		if lane.order.RepositoryID != clone.Repository.ID || base == "" {
			continue
		}
		conflict, found, err := o.conflictOf(ctx, project, clone, lane, base, facts)
		switch {
		case errors.Is(err, ports.ErrRepositoryReadsUnavailable):
			return nil, err
		case errors.Is(err, ports.ErrNotFound):
		case err != nil:
			unread[lane.order.ID] = true
		case found:
			out = append(out, conflict)
		}
	}
	return out, nil
}

// conflictOf reads one lane's conflict with the person's edits, and says whether it has one.
func (o *OutsideEdits) conflictOf(ctx context.Context, project string, clone CloneReading, lane openLane, base string, facts ledgerFacts) (Conflict, bool, error) {
	divergence, err := o.edits.Diverged(ctx, clone.Path, lane.branch, base, facts.landed, CloneEditsLimit)
	if err != nil {
		return Conflict{}, false, err
	}
	pushed := divergence.Pushed
	if clone.Repository.MergeMethod == ledger.MergeRebase && divergence.Unranged {
		pushed = nil
	}
	paths := ledger.ConflictPaths(divergence.Lane, clone.Edits.Edits, pushed)
	var local []string
	for _, path := range paths {
		if path.Person != ledger.PersonPushed {
			local = append(local, path.Path)
		}
	}
	branch := ""
	if len(local) > 0 {
		contents, err := o.edits.Contents(ctx, clone.Path, local)
		if err != nil {
			return Conflict{}, false, err
		}
		branch = ledger.EditsBranch(lane.order.ID, contents)
		held, err := o.edits.Holds(ctx, clone.Path, lane.branch, branch)
		if err != nil {
			return Conflict{}, false, err
		}
		if held {
			paths = slices.DeleteFunc(paths, func(path ledger.ConflictPath) bool { return path.Person != ledger.PersonPushed })
		}
	}
	if len(paths) == 0 {
		return Conflict{}, false, nil
	}
	place := ledger.ConflictPlace{
		Project: project, Clone: clone.Path, WorkOrder: lane.order.ID, Lane: lane.lane, Base: divergence.Base,
		Branch: branch, Ended: slices.Contains(endedStates, lane.order.State),
	}
	return Conflict{
		WorkOrder: lane.order, Lane: lane.lane, Branch: lane.branch, Repository: clone.Repository, Clone: clone.Path, Base: divergence.Base,
		Paths: paths, Cut: divergence.Cut || clone.Edits.Cut, Options: ledger.ConflictOptions(paths, place), Clears: ledger.ConflictClears(paths, place),
	}, true, nil
}

// remember keeps a reading's conflicts as the project's, each with the time a reading first found it. A conflict found
// before whose work order the reading could not read stays as it was, and one the reading read and no longer found goes.
// A reading that began before the one whose conflicts the project holds replaces none of them.
func (o *OutsideEdits) remember(scope ledger.WorkspaceID, number int64, conflicts []Conflict, unread map[string]bool, at int64) []Conflict {
	o.mu.Lock()
	defer o.mu.Unlock()
	held := o.found[scope]
	if number < o.latest[scope] {
		for i := range conflicts {
			conflicts[i].Since = cmp.Or(held[conflicts[i].WorkOrder.ID].Since, at)
		}
		return conflicts
	}
	o.latest[scope] = number
	next := make(map[string]Conflict, len(conflicts))
	for i := range conflicts {
		conflicts[i].Since = at
		if was, ok := held[conflicts[i].WorkOrder.ID]; ok {
			conflicts[i].Since = was.Since
		}
		next[conflicts[i].WorkOrder.ID] = conflicts[i]
	}
	for id, was := range held {
		if _, found := next[id]; !found && unread[id] {
			next[id] = was
			conflicts = append(conflicts, was)
		}
	}
	o.found[scope] = next
	return conflicts
}

// Conflicts are the conflicts the last reading of a project found, oldest first.
func (o *OutsideEdits) Conflicts(scope ledger.WorkspaceID) []Conflict {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Conflict, 0, len(o.found[scope]))
	for _, conflict := range o.found[scope] {
		out = append(out, conflict)
	}
	slices.SortFunc(out, func(a, b Conflict) int {
		return cmp.Or(cmp.Compare(a.Since, b.Since), cmp.Compare(a.WorkOrder.ID, b.WorkOrder.ID))
	})
	return out
}

// readClone reads one repository's clone. A clone this machine keeps none of, or git cannot read, is a problem of that
// clone, and failed says the read failed, so what was found of the clone before is kept, rather than that the clone is
// not there. A read the control plane cannot make at all, as on a group server, fails the whole reading.
func (o *OutsideEdits) readClone(ctx context.Context, scope ledger.WorkspaceID, project string, record ledger.Repository, facts ledgerFacts) (CloneReading, bool, error) {
	reading := CloneReading{Repository: record, Planning: record.ID == facts.planning, Edits: ledger.CloneEdits{Edits: []ledger.CloneEdit{}}, Steps: []ledger.EditStep{}}
	path, err := o.starts.Checkout(ctx, scope, record.ID)
	switch {
	case errors.Is(err, ports.ErrRepositoryReadsUnavailable):
		return reading, false, err
	case err != nil:
		reading.Problem = fmt.Sprintf("This machine keeps no clone of it: %v", err)
		return reading, !errors.Is(err, ports.ErrNotFound), nil
	}
	reading.Path = path
	edits, err := o.edits.Edits(ctx, path, CloneEditsLimit)
	switch {
	case errors.Is(err, ports.ErrRepositoryReadsUnavailable):
		return reading, false, err
	case err != nil:
		reading.Problem = fmt.Sprintf("Git could not read the clone: %v", err)
		return reading, !errors.Is(err, ports.ErrNotFound), nil
	}
	reading.Edits = edits
	place := ledger.EditPlace{Project: project, Clone: path, Stamp: stampOf(facts.at)}
	if reading.Planning {
		for _, lane := range facts.lanes {
			if lane.order.Kind == workorders.KindPlan && lane.order.RepositoryID == record.ID && slices.Contains(sessionStates, lane.order.State) {
				place.Planning = append(place.Planning, ledger.PlanningBranch{WorkOrder: lane.order.ID, Lane: lane.lane, Branch: lane.branch})
			}
		}
	}
	reading.Steps = ledger.EditSteps(edits, place)
	return reading, false, nil
}
