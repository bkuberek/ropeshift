package control

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/contextpacks"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// opPackAdd is the transaction that adds an item to a work order's next pack.
const opPackAdd = "pack.add"

// missingRecord is what a pack says of a record a contract cites and the ledger does not hold.
const missingRecord = "cited, and not on record"

// Packs holds the use cases of context packs. A pack itself is built by the claim that pins it, so
// what is left here is what a member or an orchestrator adds by ID.
type Packs struct {
	store ports.Store
}

// NewPacks returns the use cases over a Store.
func NewPacks(store ports.Store) *Packs { return &Packs{store: store} }

// AddPackItem adds a record to a work order's next pack. Knowledge brings its text, because a
// claim's transaction reaches no outside system, and that text is always labeled external. Adding
// the same record twice keeps the first.
func (p *Packs) AddPackItem(ctx context.Context, scope ledger.WorkspaceID, addition contextpacks.Addition, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionAddPackItem) {
		return workorders.ErrForbidden
	}
	if err := addition.Check(); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	return p.store.Write(ctx, scope, opPackAdd, func(tx ports.WriteTx) error {
		if _, err := tx.WorkOrders().Get(addition.WorkOrder); err != nil {
			return err
		}
		addition.AddedBy, addition.AddedAt = actor.ID, tx.Now()
		added, err := tx.Packs().Add(addition)
		if err != nil {
			return err
		}
		if !added {
			return nil
		}
		event, err := contextpacks.RecordEvent(contextpacks.EventItemAdded, contextpacks.SubjectAdded, contextpacks.AdditionKey(addition), addition, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// packRecords are what a pack draws from, in either kind of transaction.
type packRecords struct {
	planning  ports.PlanningReader
	questions ports.QuestionReader
	packs     ports.PackReader
}

func writeRecords(tx ports.WriteTx) packRecords {
	return packRecords{planning: tx.Planning(), questions: tx.Questions(), packs: tx.Packs()}
}

func readRecords(tx ports.ReadTx) packRecords {
	return packRecords{planning: tx.Planning(), questions: tx.Questions(), packs: tx.Packs()}
}

func contractSource(contract planning.Contract) contextpacks.Source {
	var text strings.Builder
	text.WriteString(contract.Statement)
	if len(contract.Criteria) != 0 {
		text.WriteString("\n\nAcceptance criteria:\n")
		for _, criterion := range contract.Criteria {
			text.WriteString("- " + criterion.ID + ": " + criterion.Text + "\n")
		}
	}
	return contextpacks.Source{
		Kind: contextpacks.KindContract, Ref: planning.ContractKey(contract.ID, contract.Version), Digest: contract.Digest,
		Trust: contextpacks.TrustWorkspace, Title: contract.Title, Text: text.String(),
	}
}

func requirementSource(requirement planning.Requirement) contextpacks.Source {
	return contextpacks.Source{
		Kind: contextpacks.KindRequirement, Ref: requirement.Key, Digest: requirement.Digest, Trust: contextpacks.TrustWorkspace, Text: requirement.Text,
	}
}

func decisionSource(decision questions.Decision) contextpacks.Source {
	text := questions.DecisionText(decision)
	return contextpacks.Source{
		Kind: contextpacks.KindDecision, Ref: decision.ID, Digest: contextpacks.TextDigest(text), Trust: contextpacks.TrustWorkspace, Text: text,
	}
}

func goalSource(goal planning.Goal) contextpacks.Source {
	text := goal.Statement
	if len(goal.SuccessMeasures) != 0 {
		text += "\n\nSuccess measures:\n- " + strings.Join(goal.SuccessMeasures, "\n- ") + "\n"
	}
	return contextpacks.Source{
		Kind: contextpacks.KindGoal, Ref: goal.ID, Digest: contextpacks.TextDigest(goal.Title, text), Trust: contextpacks.TrustWorkspace,
		Title: goal.Title, Text: text,
	}
}

// missing names a record a pack cannot hold, so an agent learns the citation leads nowhere.
func missing(kind contextpacks.Kind, ref string) contextpacks.Source {
	return contextpacks.Source{Kind: kind, Ref: ref, Trust: contextpacks.TrustWorkspace, Title: missingRecord}
}

func (r packRecords) requirement(key string) (contextpacks.Source, error) {
	found, err := r.planning.Requirement(key)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return missing(contextpacks.KindRequirement, key), nil
	case err != nil:
		return contextpacks.Source{}, err
	}
	return requirementSource(found), nil
}

func (r packRecords) decision(id string) (contextpacks.Source, error) {
	found, err := r.questions.Decision(id)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return missing(contextpacks.KindDecision, id), nil
	case err != nil:
		return contextpacks.Source{}, err
	}
	return decisionSource(found), nil
}

func (r packRecords) goal(id string) (contextpacks.Source, error) {
	found, err := r.planning.Goal(id)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return missing(contextpacks.KindGoal, id), nil
	case err != nil:
		return contextpacks.Source{}, err
	}
	return goalSource(found), nil
}

// added reads one addition. A workspace record is read as it stands now, and knowledge as it
// came, under a label that says so.
func (r packRecords) added(addition contextpacks.Addition) (contextpacks.Source, error) {
	switch addition.Kind {
	case contextpacks.KindRequirement:
		return r.requirement(addition.Ref)
	case contextpacks.KindDecision:
		return r.decision(addition.Ref)
	case contextpacks.KindGoal:
		return r.goal(addition.Ref)
	}
	return contextpacks.Source{
		Kind: addition.Kind, Ref: addition.Ref, Digest: contextpacks.TextDigest(addition.Text), Trust: contextpacks.TrustExternal, Text: addition.Text,
	}, nil
}

// inScope reads every decision in force that overlaps what a work order touches, each once, by ID.
func (r packRecords) inScope(order workorders.WorkOrder) ([]contextpacks.Source, error) {
	found := map[string]questions.Decision{}
	for _, scope := range []string{order.ID, order.ContractID, order.RepositoryID} {
		overlapping, err := r.questions.Overlapping(scope)
		if err != nil {
			return nil, err
		}
		for _, decision := range overlapping {
			found[decision.ID] = decision
		}
	}
	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]contextpacks.Source, 0, len(ids))
	for _, id := range ids {
		out = append(out, decisionSource(found[id]))
	}
	return out, nil
}

// selection reads what a work order's pack draws from, at the contract version it is pinned to.
func (r packRecords) selection(order workorders.WorkOrder) (contextpacks.Selection, error) {
	var selection contextpacks.Selection
	var err error
	if order.ContractVersion != 0 {
		contract, err := r.planning.Contract(order.ContractID, order.ContractVersion)
		if err != nil {
			return selection, err
		}
		source := contractSource(contract)
		selection.Contract = &source
		for _, key := range contract.Requirements {
			requirement, err := r.requirement(key)
			if err != nil {
				return selection, err
			}
			selection.Requirements = append(selection.Requirements, requirement)
		}
		for _, id := range contract.Decisions {
			decision, err := r.decision(id)
			if err != nil {
				return selection, err
			}
			selection.Decisions = append(selection.Decisions, decision)
		}
	}
	if selection.Overlapping, err = r.inScope(order); err != nil {
		return selection, err
	}
	goals, err := r.planning.Goals()
	if err != nil {
		return selection, err
	}
	for _, goal := range goals {
		if goal.Status == planning.StatusActive {
			selection.Goals = append(selection.Goals, goalSource(goal))
		}
	}
	additions, err := r.packs.Additions(order.ID)
	if err != nil {
		return selection, err
	}
	for _, addition := range additions {
		source, err := r.added(addition)
		if err != nil {
			return selection, err
		}
		selection.Added = append(selection.Added, source)
	}
	return selection, nil
}

// buildPack fits what a work order selects to a budget, at the ratio this workspace has measured.
func buildPack(tx ports.WriteTx, order workorders.WorkOrder, tokens int64) (contextpacks.Pack, error) {
	records := writeRecords(tx)
	selection, err := records.selection(order)
	if err != nil {
		return contextpacks.Pack{}, err
	}
	samples, err := tx.Packs().Samples()
	if err != nil {
		return contextpacks.Pack{}, err
	}
	return contextpacks.Build(order.ID, selection, contextpacks.Budget{Tokens: tokens, Ratio: contextpacks.Ratio(samples)})
}

// keepPack stores one version with its event.
func keepPack(tx ports.WriteTx, pack contextpacks.Pack, actor ledger.Actor) error {
	if err := tx.Packs().Insert(pack); err != nil {
		return err
	}
	crashpoint.Hit("claim", "packed")
	if dropsPackEvent() {
		return nil
	}
	event, err := contextpacks.RecordEvent(contextpacks.EventBuilt, contextpacks.SubjectPack, pack.ID, pack, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// pinPack gives a claim its pack, in the claim's transaction, so a kill leaves both or neither. A
// claim that brings a lane back keeps the lane's, because a lane keeps its pack.
func (l *Leases) pinPack(tx ports.WriteTx, order workorders.WorkOrder, claim *workorders.Claim, actor ledger.Actor) error {
	if claim.PackID != "" && !rebuildsPack() {
		return nil
	}
	pack, err := buildPack(tx, order, l.packTokens)
	if err != nil {
		return err
	}
	if pack.ID, err = newID(tx.Now()); err != nil {
		return err
	}
	pack.Version = 1
	claim.PackID = pack.ID
	return keepPack(tx, pack, actor)
}

// LaneContext reads what a workspace holds, so a pack is where a lane starts and not where it stops
// (FR-CTX-2). It holds to the same test as a question, because a lane reaches the ledger only
// through the worker that holds it, and it reads the record as it stands now.
func (w *Workers) LaneContext(ctx context.Context, request ledger.LaneContextRequest) (ledger.LaneContext, error) {
	found := ledger.LaneContext{Records: []ledger.ContextRecord{}}
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		if err := w.checkSession(tx, request.Auth, request.Epoch); err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(request.WorkOrder)
		if err != nil {
			return err
		}
		if order.LeaseWorker != request.Auth.WorkerID || order.Fence != request.Fence {
			return &workorders.FencedError{Current: order.Fence, State: order.State}
		}
		sources, err := readRecords(tx).reach(request)
		if err != nil {
			return err
		}
		for _, source := range sources {
			found.Records = append(found.Records, ledger.ContextRecord{
				Kind: string(source.Kind), Ref: source.Ref, Trust: string(source.Trust), Text: contextpacks.Render(source),
			})
		}
		return nil
	})
	if err != nil {
		return ledger.LaneContext{}, err
	}
	return found, nil
}

// defaultSearch is how many records one search reads when a lane names no limit.
const defaultSearch = 20

// reach answers one read.
func (r packRecords) reach(request ledger.LaneContextRequest) ([]contextpacks.Source, error) {
	switch request.Tool {
	case lanes.ToolContextGet:
		return r.lookup(contextpacks.Kind(request.Kind), request.Ref, request.WorkOrder)
	case lanes.ToolRequirementGet:
		return r.lookup(contextpacks.KindRequirement, request.Ref, request.WorkOrder)
	case lanes.ToolDecisionSearch:
		return r.searchDecisions(request.Query, request.Limit)
	case lanes.ToolQuestionSearch:
		return r.searchQuestions(request.Query, request.Limit)
	}
	return nil, fmt.Errorf("%w: no tool %q", workorders.ErrInvalid, request.Tool)
}

// lookup reads one record by the name a pack gave it. A name the ledger does not hold finds
// nothing, and knowledge is reachable only from the work order it was added to.
func (r packRecords) lookup(kind contextpacks.Kind, ref, workOrder string) ([]contextpacks.Source, error) {
	var source contextpacks.Source
	var err error
	switch kind {
	case contextpacks.KindContract:
		return r.contract(ref)
	case contextpacks.KindRequirement:
		source, err = r.requirement(ref)
	case contextpacks.KindDecision:
		source, err = r.decision(ref)
	case contextpacks.KindGoal:
		source, err = r.goal(ref)
	case contextpacks.KindKnowledge:
		return r.knowledge(ref, workOrder)
	default:
		return nil, fmt.Errorf("%w: no kind %q", workorders.ErrInvalid, kind)
	}
	if err != nil || source.Title == missingRecord {
		return nil, err
	}
	return []contextpacks.Source{source}, nil
}

// contract reads a version a pack named, or the newest version of a bare ID.
func (r packRecords) contract(ref string) ([]contextpacks.Source, error) {
	var found planning.Contract
	var err error
	id, version, versioned := strings.Cut(ref, "@")
	if number, parsed := strconv.ParseInt(version, 10, 64); versioned && parsed == nil {
		found, err = r.planning.Contract(id, number)
	} else {
		found, err = r.planning.LatestContract(ref)
	}
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return []contextpacks.Source{contractSource(found)}, nil
}

func (r packRecords) knowledge(ref, workOrder string) ([]contextpacks.Source, error) {
	additions, err := r.packs.Additions(workOrder)
	if err != nil {
		return nil, err
	}
	for _, addition := range additions {
		if addition.Kind == contextpacks.KindKnowledge && addition.Ref == ref {
			source, err := r.added(addition)
			return []contextpacks.Source{source}, err
		}
	}
	return nil, nil
}

func searchLimit(limit int) int {
	if limit <= 0 {
		return defaultSearch
	}
	return limit
}

func (r packRecords) searchDecisions(query string, limit int) ([]contextpacks.Source, error) {
	rows, err := r.questions.Decisions(ports.RecordFilter{Text: query, Limit: searchLimit(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]contextpacks.Source, 0, len(rows))
	for _, row := range rows {
		out = append(out, decisionSource(row))
	}
	return out, nil
}

func (r packRecords) searchQuestions(query string, limit int) ([]contextpacks.Source, error) {
	rows, err := r.questions.Questions(ports.RecordFilter{Text: query, Limit: searchLimit(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]contextpacks.Source, 0, len(rows))
	for _, row := range rows {
		text := "Status: " + string(row.Status) + "\nQuestion: " + row.Text + "\n"
		if row.DecisionID != "" {
			text += "Decision: " + row.DecisionID + "\n"
		}
		out = append(out, contextpacks.Source{Kind: contextpacks.KindQuestion, Ref: row.ID, Trust: contextpacks.TrustWorkspace, Text: text})
	}
	return out, nil
}

// desiredPack reads the newest version of a pack for a lane's next generation.
func desiredPack(packs ports.PackReader, id string) (*ledger.DesiredPack, error) {
	if id == "" {
		return nil, nil
	}
	pack, err := packs.Latest(id)
	if err != nil {
		return nil, err
	}
	return &ledger.DesiredPack{ID: pack.ID, Version: pack.Version, Digest: pack.Digest, TokensEstimate: pack.TokensEstimate, Text: pack.Text}, nil
}
