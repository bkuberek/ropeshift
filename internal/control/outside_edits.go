package control

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// CloneEditsLimit is how many paths of one clone a reading lists.
const CloneEditsLimit = 500

// OutsideEdits reads what each registered repository's clone on this machine holds that intake and lanes cannot read,
// with the steps that make it readable (FR-UI-14). It reads the clones through the ports it is given and changes
// nothing.
type OutsideEdits struct {
	starts *Starts
	edits  ports.CloneEdits
	// prefix is what the name of a lane's branch begins with, before the lane's ID.
	prefix string
}

// NewOutsideEdits reads the clone of each repository where starts places its lanes, through edits. A lane's branch is
// the one its work order keeps, or else prefix and the lane's ID.
func NewOutsideEdits(starts *Starts, edits ports.CloneEdits, prefix string) *OutsideEdits {
	return &OutsideEdits{starts: starts, edits: edits, prefix: prefix}
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

// OutsideReading is every registered repository's clone, read at one time.
type OutsideReading struct {
	At     int64
	Clones []CloneReading
}

// openStates are the states in which a work order's lane holds a branch whose work is not merged yet.
var openStates = []workorders.State{
	workorders.Claimed, workorders.Running, workorders.Parked, workorders.Lapsed, workorders.Acceptance, workorders.Integrating, workorders.Blocked,
}

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
	facts.lanes, err = openLanes(tx, prefix)
	return facts, err
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

// Read reads the clone of each of a project's repositories, and says of each clone it could not read why. project is
// the project's name, which the commands of the command line give.
func (o *OutsideEdits) Read(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, project string) (OutsideReading, error) {
	var facts ledgerFacts
	if err := store.Read(ctx, scope, func(tx ports.ReadTx) (err error) {
		facts, err = readFacts(tx, o.prefix)
		return err
	}); err != nil {
		return OutsideReading{}, err
	}
	out := OutsideReading{At: facts.at, Clones: []CloneReading{}}
	for _, record := range facts.repositories {
		reading, err := o.readClone(ctx, scope, project, record, facts)
		if err != nil {
			return OutsideReading{}, err
		}
		out.Clones = append(out.Clones, reading)
	}
	return out, nil
}

// readClone reads one repository's clone. A clone this machine keeps none of, or git cannot read, is a problem of that
// clone. A read the control plane cannot make at all, as on a group server, fails the whole reading.
func (o *OutsideEdits) readClone(ctx context.Context, scope ledger.WorkspaceID, project string, record ledger.Repository, facts ledgerFacts) (CloneReading, error) {
	reading := CloneReading{Repository: record, Planning: record.ID == facts.planning, Edits: ledger.CloneEdits{Edits: []ledger.CloneEdit{}}, Steps: []ledger.EditStep{}}
	path, err := o.starts.Checkout(ctx, scope, record.ID)
	switch {
	case errors.Is(err, ports.ErrRepositoryReadsUnavailable):
		return reading, err
	case err != nil:
		reading.Problem = fmt.Sprintf("This machine keeps no clone of it: %v", err)
		return reading, nil
	}
	reading.Path = path
	edits, err := o.edits.Edits(ctx, path, CloneEditsLimit)
	switch {
	case errors.Is(err, ports.ErrRepositoryReadsUnavailable):
		return reading, err
	case err != nil:
		reading.Problem = fmt.Sprintf("Git could not read the clone: %v", err)
		return reading, nil
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
	return reading, nil
}
