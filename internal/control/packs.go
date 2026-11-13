package control

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
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
	evidence  ports.EvidenceReader
}

func writeRecords(tx ports.WriteTx) packRecords {
	return packRecords{planning: tx.Planning(), questions: tx.Questions(), packs: tx.Packs(), evidence: tx.Evidence()}
}

func readRecords(tx ports.ReadTx) packRecords {
	return packRecords{planning: tx.Planning(), questions: tx.Questions(), packs: tx.Packs(), evidence: tx.Evidence()}
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

// documentSource is a document at one version: its Markdown, and what its kind says beyond that, so
// that a pack holds the version a contract read after the file has moved on.
func documentSource(version planning.DocumentVersion) contextpacks.Source {
	return contextpacks.Source{
		Kind: contextpacks.KindDocument, Ref: planning.ContractKey(version.DocumentID, version.Version), Digest: version.Digest,
		Trust: contextpacks.TrustWorkspace, Title: version.Title, Text: documentText(version),
	}
}

// documentText reads the body of a document version, then the part of its front matter a reader of the
// pack needs: the requirements it addresses, the requirements it holds, the assets it shows, or what
// a milestone depends on and exits on.
func documentText(version planning.DocumentVersion) string {
	text := strings.TrimSpace(version.Body)
	kind, _, _ := planning.ParseDocumentRef(version.DocumentID)
	for _, list := range documentLists(kind, version) {
		if len(list.items) != 0 {
			text += "\n\n" + list.title + ":\n- " + strings.Join(list.items, "\n- ") + "\n"
		}
	}
	return text
}

// textList is a titled list in the text of a document.
type textList struct {
	title string
	items []string
}

func documentLists(kind planning.FileKind, version planning.DocumentVersion) []textList {
	switch kind {
	case planning.FileRequirements:
		var file planning.RequirementsFile
		if version.Decode(&file) != nil {
			return nil
		}
		held := make([]string, len(file.Requirements))
		for i, spec := range file.Requirements {
			held[i] = spec.ID + ": " + spec.Text
		}
		return []textList{{"Requirements", held}}
	case planning.FileSpec, planning.FileArchitecture, planning.FileExperienceDesign:
		// An experience design is the others with assets, so one struct reads all three.
		var file planning.ExperienceDesignFile
		if version.Decode(&file) != nil {
			return nil
		}
		return []textList{{"Addresses requirements", file.Requirements}, {"Assets", file.Assets}}
	case planning.FileMilestone:
		var file planning.MilestoneFile
		if version.Decode(&file) != nil {
			return nil
		}
		exits := make([]string, len(file.ExitCriteria))
		for i, criterion := range file.ExitCriteria {
			exits[i] = criterion.ID + ": " + criterion.Text
		}
		return []textList{{"Depends on milestones", file.DependsOn}, {"Exit criteria", exits}}
	}
	return nil
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

// document reads one version of a document, or says the pack's citation leads nowhere.
func (r packRecords) document(id string, version int64) (contextpacks.Source, error) {
	found, err := r.planning.DocumentVersion(id, version)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return missing(contextpacks.KindDocument, planning.ContractKey(id, version)), nil
	case err != nil:
		return contextpacks.Source{}, err
	}
	return documentSource(found), nil
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

// reported is what the lane whose work a review judges said of it when it ended, with what a forge
// recorded about the same commit. It is information a reviewer weighs, never evidence it rests on, and
// it is what the lane wrote, so the pack fences it.
func (r packRecords) reported(review workorders.WorkOrder) (contextpacks.Source, error) {
	rows, err := r.evidence.Attempt(review.Reviews, review.ReviewsFence)
	if err != nil {
		return contextpacks.Source{}, err
	}
	var text strings.Builder
	text.WriteString("These are the checks the lane reported when it ended its work, and what a forge recorded of the same commit. " +
		"They are information and not evidence: they are what that lane says of its own work.\n")
	if len(rows) == 0 {
		text.WriteString("\nThe lane reported no checks.\n")
	}
	for _, row := range rows {
		fmt.Fprintf(&text, "\n- %s: %s", row.Name, row.Status)
		if row.Criterion != "" {
			fmt.Fprintf(&text, " (criterion %s)", row.Criterion)
		}
		fmt.Fprintf(&text, ", from the %s", row.Source)
	}
	if len(rows) != 0 {
		text.WriteString("\n")
	}
	ref := review.Reviews + "@" + strconv.FormatInt(review.ReviewsFence, 10)
	return contextpacks.Source{
		Kind: contextpacks.KindChecks, Ref: ref, Digest: contextpacks.TextDigest(text.String()), Trust: contextpacks.TrustLane, Text: text.String(),
	}, nil
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
		for _, pin := range contract.Documents {
			document, err := r.document(pin.Document, pin.Version)
			if err != nil {
				return selection, err
			}
			selection.Documents = append(selection.Documents, document)
		}
		for _, key := range contract.RequirementKeys() {
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
	if order.Kind == workorders.KindReview {
		reported, err := r.reported(order)
		if err != nil {
			return selection, err
		}
		selection.Reported = []contextpacks.Source{reported}
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
// Carried is what an earlier version held that the selection would no longer reach.
func buildPack(tx ports.WriteTx, order workorders.WorkOrder, tokens int64, carried ...contextpacks.Source) (contextpacks.Pack, error) {
	records := writeRecords(tx)
	selection, err := records.selection(order)
	if err != nil {
		return contextpacks.Pack{}, err
	}
	selection.Added = append(selection.Added, carried...)
	samples, err := tx.Packs().Samples()
	if err != nil {
		return contextpacks.Pack{}, err
	}
	return contextpacks.Build(order.ID, selection, contextpacks.Budget{Tokens: tokens, Ratio: contextpacks.Ratio(samples)})
}

// carried reads what a pack's decisions stand for now. A decision that ended leaves its replacement
// in the pack, whatever put it there, because a selection reads scopes and a replacement can name
// another. Each version is renewed as a decision ends, so one step reaches the one in force.
func (r packRecords) carried(pack contextpacks.Pack) ([]contextpacks.Source, error) {
	var out []contextpacks.Source
	for _, item := range pack.Items {
		if item.Kind != contextpacks.KindDecision {
			continue
		}
		decision, err := r.questions.Decision(item.Ref)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !decision.Status.InForce() {
			decision, err = r.questions.ReplacedBy(decision.ID)
			if errors.Is(err, ports.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
		}
		if decision.Status.InForce() {
			out = append(out, decisionSource(decision))
		}
	}
	return out, nil
}

// renewPacks gives each pack a new version for its next generation, in the transaction that ended
// what it held. A running generation keeps the version it read, and the notice is what it hears.
func (q *Questions) renewPacks(tx ports.WriteTx, packs []string, actor ledger.Actor) error {
	for _, id := range packs {
		live, err := packInUse(tx, id)
		if err != nil {
			return err
		}
		if !live {
			continue
		}
		latest, err := tx.Packs().Latest(id)
		if err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(latest.WorkOrder)
		if err != nil {
			return err
		}
		carried, err := writeRecords(tx).carried(latest)
		if err != nil {
			return err
		}
		next, err := buildPack(tx, order, q.packTokens, carried...)
		if err != nil {
			return err
		}
		next.ID, next.Version = latest.ID, latest.Version+1
		if err := keepPack(tx, next, actor); err != nil {
			return err
		}
	}
	return nil
}

// packInUse says a generation can still read a pack: a lane that has not ended holds it, or no
// lane has started under it yet.
func packInUse(tx ports.WriteTx, pack string) (bool, error) {
	lanes, err := tx.Packs().Lanes(pack)
	if err != nil || len(lanes) == 0 {
		return true, err
	}
	for _, lane := range lanes {
		row, err := tx.Lanes().Row(lane)
		if errors.Is(err, ports.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if row.Status.ActualState != ledger.StateEnded {
			return true, nil
		}
	}
	return false, nil
}

// addPinnedLanes finds every lane whose pack holds a record, through the item index, so a notice
// reaches them whatever else does or does not connect the two.
func addPinnedLanes(tx ports.WriteTx, touched *reach, kind contextpacks.Kind, record string) error {
	if ignoresPins() {
		return nil
	}
	packs, err := tx.Packs().Pinning(kind, record)
	if err != nil {
		return err
	}
	touched.packs = packs
	for _, pack := range packs {
		lanes, err := tx.Packs().Lanes(pack)
		if err != nil {
			return err
		}
		for _, lane := range lanes {
			row, err := tx.Lanes().Row(lane)
			switch {
			case errors.Is(err, ports.ErrNotFound):
				continue
			case err != nil:
				return err
			}
			touched.lanes[lane] = row.Generation
		}
	}
	return nil
}

// askToPark records a policy's word on a lane a session still runs. A lane no generation runs has
// nothing to stop, and it reads the change as a fact when it comes back.
func askToPark(tx ports.WriteTx, lane string, generation int64, reason string, actor ledger.Actor) error {
	open, err := generationOpen(tx, messages.Target{Lane: lane, Generation: generation})
	if err != nil || !open {
		return err
	}
	return settlePark(tx, lane, reason, actor)
}

// settlePark keeps what a policy asks of a lane with its event. An empty reason takes it back.
func settlePark(tx ports.WriteTx, lane, reason string, actor ledger.Actor) error {
	if err := tx.Lanes().WantPark(lane, reason); err != nil {
		return err
	}
	event, err := contextpacks.RecordEvent(contextpacks.EventParkWanted, contextpacks.SubjectLane, lane, contextpacks.ParkWanted{Lane: lane, Reason: reason}, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// parkWanted reads what a policy asks of a lane, and a lane that has not started has no answer yet.
func parkWanted(lanes ports.LaneReader, lane string) (string, error) {
	row, err := lanes.Row(lane)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	return row.ParkWanted, err
}

// answerPark clears what a policy asked of a lane that has now parked, so it does not park again
// when a person brings it back.
func answerPark(tx ports.WriteTx, lane string, actor ledger.Actor) error {
	row, err := tx.Lanes().Row(lane)
	if err != nil || row.ParkWanted == "" || keepsParkWanted() {
		return err
	}
	return settlePark(tx, lane, "", actor)
}

// keepPack stores one version with its event.
func keepPack(tx ports.WriteTx, pack contextpacks.Pack, actor ledger.Actor) error {
	if err := tx.Packs().Insert(pack); err != nil {
		return err
	}
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
	crashpoint.Hit("claim", "packed")
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
		if err := readsAsHolder(tx, request); err != nil {
			return err
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

// readsAsHolder holds a read to the session it speaks for: a lane that this worker holds at this
// fence, or this worker's own orchestrator, which has no work order to hold.
func readsAsHolder(tx ports.ReadTx, request ledger.LaneContextRequest) error {
	if request.WorkOrder == "" {
		// An assistant's run reads its own project, while its conversation runs on this worker.
		if record, err := tx.Conversations().ByRunLane(request.Lane); err == nil {
			if record.Worker != request.Auth.WorkerID || !record.Running() {
				return fmt.Errorf("%w: run %s is not this worker's", workorders.ErrInvalid, request.Lane)
			}
			return nil
		}
		row, err := tx.Workers().Orchestrator(request.Auth.WorkerID)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && !row.Belongs(request.Lane)) {
			return fmt.Errorf("%w: %s is not this worker's orchestrator", workorders.ErrInvalid, request.Lane)
		}
		return err
	}
	order, err := tx.WorkOrders().Get(request.WorkOrder)
	if err != nil {
		return err
	}
	if order.LeaseWorker != request.Auth.WorkerID || order.Fence != request.Fence {
		return &workorders.FencedError{Current: order.Fence, State: order.State}
	}
	return nil
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
	case contextpacks.KindDocument:
		return r.documentNamed(ref)
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

// documentNamed reads a version a pack named, or the newest version of a bare reference.
func (r packRecords) documentNamed(ref string) ([]contextpacks.Source, error) {
	var found planning.DocumentVersion
	var err error
	if id, version, versioned := planning.ParseContractKey(ref); versioned {
		found, err = r.planning.DocumentVersion(id, version)
	} else {
		found, err = r.planning.LatestDocumentVersion(ref)
	}
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return []contextpacks.Source{documentSource(found)}, nil
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
