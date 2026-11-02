package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/contextpacks"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// PackChecks are what the ledger promises a context pack (FR-CTX-1, FR-CTX-4). What a worker does
// with one is in the worker's tests, because this suite holds no session.
var PackChecks = []Check{
	{"PK1", "a claim pins a pack built from the ledger, in the design's order", checkPackAtClaim},
	{"PK2", "a retried claim builds no second pack", checkPackRetry},
	{"PK3", "a pack replays from its event, whole", checkPackReplays},
	{"PK4", "a claim that brings a lane back keeps the lane's pack", checkPackFollowsLane},
	{"PK5", "desired state carries the newest version, byte for byte", checkPackDesired},
	{"PK6", "work with no contract still gets a pack", checkPackNoContract},
	{"PK7", "a scope past one page of decisions reads every decision", checkPackReadsEveryPage},
	{"PK8", "a cited decision that stopped standing says so, and one that never stood says that", checkPackCitedDecisions},
	{"PK9", "what was added reaches the next pack, and knowledge reads as external", checkPackAdditions},
	{"PK10", "an addition needs authority, a work order, and the shape of one", checkPackAdditionRefusals},
	{"PK11", "a budget set for the claim is the budget of its pack", checkPackBudget},
	{"PK12", "a goal that is not active stays out", checkPackGoals},
	{"PK13", "a new claim on the same work order pins a pack of its own", checkPackNewClaim},
	{"PK14", "a lane reads any kind of record by name", checkContextLookups},
	{"PK15", "a search reads decisions and questions, as a pack would say them", checkContextSearches},
	{"PK16", "a read that finds nothing says so, and a tool nobody named is refused", checkContextMisses},
	{"PK17", "a lane reads only what its own worker holds, and knowledge of its own work order", checkContextReach},
	{"PK18", "a read past a lane's lease reads nothing", checkContextFence},
	{"PK19", "a baseline stays on its generation, and only the current one's", checkBaselineUpload},
	{"PK20", "an estimate follows what fresh generations measured", checkPackRatio},
	{"PK21", "a baseline stays on the generation that measured it", checkBaselineOneGeneration},
}

// ClaimsPinned holds every claim to a pack the ledger holds, and every version to its log.
func ClaimsPinned(r *Rig, s ports.Store) error {
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		claims, err := tx.Claims().List()
		if err != nil {
			return err
		}
		for _, claim := range claims {
			if claim.PackID == "" {
				return Violatedf("claim %s pinned no pack", claim.ID)
			}
			if _, err := tx.Packs().Latest(claim.PackID); err != nil {
				return Violatedf("claim %s pinned a pack the ledger does not hold: %v", claim.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return r.CheckPacks(s)
}

func checkBaselineOneGeneration(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := s.nextGeneration(2, ledger.StartHandoff); err != nil {
		return err
	}
	raw, err := json.Marshal(ledger.GenerationBaseline{Tokens: 31000})
	if err != nil {
		return err
	}
	if err := accepted(s.uploadAt(2, ledger.UploadGenerationBaseline, raw)); err != nil {
		return err
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 2 || generations[0].BaselineTokens != 0 || generations[1].BaselineTokens != 31000 {
		return Violatedf("generations %+v", generations)
	}
	return nil
}

const packContractFile = "pack-contract.md"

func packContract(title string, decisions ...string) string {
	cited := ""
	if len(decisions) != 0 {
		cited = `decisions = ["` + strings.Join(decisions, `", "`) + `"]` + "\n"
	}
	return `+++
kind = "contract"
id = "` + strings.ToLower(strings.ReplaceAll(title, " ", "-")) + `"
title = "` + title + `"
requirements = ["PK-2", "PK-1", "PK-GONE"]
` + cited + `
[[criteria]]
id = "c1"
text = "It lands"

[[work_orders]]
id = "` + strings.ToLower(strings.ReplaceAll(title, " ", "-")) + `-order"
title = "` + title + `"
+++

The statement of ` + title + `.
`
}

const packRequirements = `+++
kind = "requirements"
id = "pack-reqs"
title = "Requirements"

[[requirement]]
id = "PK-1"
text = "The first requirement."

[[requirement]]
id = "PK-2"
text = "The second requirement."
+++
`

const packGoal = `+++
kind = "goal"
id = "pack-goal"
title = "Keep lanes grounded"
success_measures = ["Fewer repeated questions"]
+++

A goal's statement.
`

// packRig is one work order under a contract, on its way to a claim.
type packRig struct {
	r      *Rig
	s      ports.Store
	source *fake.WorkSource
	order  workorders.WorkOrder
}

// newPackRig records the planning files and a contract, with what a test decided first.
func newPackRig(r *Rig, cited ...string) (*packRig, error) {
	s := r.Store()
	p := &packRig{r: r, s: s, source: fake.NewWorkSource()}
	p.source.Write("requirements.md", []byte(packRequirements))
	p.source.Write("goal.md", []byte(packGoal))
	p.source.Write(packContractFile, []byte(packContract("Pack work", cited...)))
	if _, err := r.intake(s, p.source); err != nil {
		return nil, err
	}
	var err error
	if p.order, err = r.orderByTitle(s, "Pack work"); err != nil {
		return nil, err
	}
	result, err := r.NewOps(s).Pass(r.Ctx, r.Scope, p.order.ID, r.member())
	if err != nil {
		return nil, err
	}
	if result.State != workorders.Ready {
		return nil, fmt.Errorf("the contract did not pass review: %+v", result)
	}
	return p, nil
}

func (p *packRig) claim() (workorders.Grant, error) {
	return p.r.NewOps(p.s).Claim(p.r.Ctx, p.r.Scope, p.r.Request(p.order.ID, "worker-1"), p.r.Actor)
}

// packOf reads the pack a claim pinned.
func (r *Rig) packOf(s ports.Store, claim string) (contextpacks.Pack, error) {
	var pack contextpacks.Pack
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		row, err := tx.Claims().Find(claim)
		if err != nil {
			return err
		}
		if row.PackID == "" {
			return Violatedf("claim %s pinned no pack", claim)
		}
		pack, err = tx.Packs().Latest(row.PackID)
		return err
	})
	return pack, err
}

func itemRefs(pack contextpacks.Pack) []string {
	return project(pack.Items, func(item contextpacks.Item) string { return string(item.Kind) + " " + item.Ref })
}

// CheckPacks compares every version with what the log replays to.
func (r *Rig) CheckPacks(s ports.Store) error {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	book, err := contextpacks.Replay(events)
	if err != nil {
		return Violatedf("the log does not replay: %v", err)
	}
	var rows []contextpacks.Pack
	var additions []contextpacks.Addition
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		if rows, err = tx.Packs().All(); err != nil {
			return err
		}
		additions, err = tx.Packs().AllAdditions()
		return err
	})
	if err != nil {
		return err
	}
	if len(rows) != len(book.Packs) {
		return Violatedf("%d pack versions and %d in the log", len(rows), len(book.Packs))
	}
	for _, row := range rows {
		want, ok := book.Packs[contextpacks.VersionKey(row.ID, row.Version)]
		if !ok {
			return Violatedf("pack %s version %d has no history", row.ID, row.Version)
		}
		if !reflect.DeepEqual(row, want) {
			return Violatedf("pack %s disagrees with its log:\nrow %+v\nlog %+v", row.ID, row, want)
		}
	}
	if len(additions) != len(book.Additions) {
		return Violatedf("%d additions and %d in the log", len(additions), len(book.Additions))
	}
	for _, addition := range additions {
		if want := book.Additions[contextpacks.AdditionKey(addition)]; !reflect.DeepEqual(addition, want) {
			return Violatedf("an addition disagrees with its log:\nrow %+v\nlog %+v", addition, want)
		}
	}
	return nil
}

func checkPackAtClaim(r *Rig) error {
	s := r.Store()
	cited, err := r.decision(s, "A cited decision", func(d *questions.Decision) { d.Scope = "elsewhere" })
	if err != nil {
		return err
	}
	p, err := newPackRig(r, cited.ID)
	if err != nil {
		return err
	}
	scoped, err := r.decision(p.s, "A decision on this repository", func(d *questions.Decision) { d.Scope = p.order.RepositoryID })
	if err != nil {
		return err
	}
	if _, err := r.decision(p.s, "A decision on something else", func(d *questions.Decision) { d.Scope = "other" }); err != nil {
		return err
	}
	grant, err := p.claim()
	if err != nil {
		return err
	}
	pack, err := r.packOf(p.s, grant.ClaimID)
	if err != nil {
		return err
	}
	var goal planning.Goal
	err = p.s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		goals, err := tx.Planning().Goals()
		if err != nil || len(goals) != 1 {
			return fmt.Errorf("%d goals: %v", len(goals), err)
		}
		goal = goals[0]
		return nil
	})
	if err != nil {
		return err
	}
	want := []string{
		"contract " + p.order.ContractID + "@1", "requirement PK-2", "requirement PK-1", "requirement PK-GONE",
		"decision " + cited.ID, "decision " + scoped.ID, "goal " + goal.ID,
	}
	if got := itemRefs(pack); !slices.Equal(got, want) {
		return Violatedf("items %v, want %v", got, want)
	}
	for _, item := range pack.Items {
		if item.Trust != contextpacks.TrustWorkspace {
			return Violatedf("item %s reads %s", item.Ref, item.Trust)
		}
	}
	if pack.Version != 1 || pack.WorkOrder != p.order.ID || pack.TokensEstimate <= 0 || pack.Digest == "" {
		return Violatedf("pack %+v", pack)
	}
	for _, text := range []string{"The statement of Pack work.", "It lands", "The second requirement.", "A cited decision", "A decision on this repository", "Fewer repeated questions", "PK-GONE"} {
		if !strings.Contains(pack.Text, text) {
			return Violatedf("the pack does not say %q:\n%s", text, pack.Text)
		}
	}
	if strings.Contains(pack.Text, "A decision on something else") {
		return Violatedf("a decision out of scope reached the pack")
	}
	return r.CheckPacks(p.s)
}

func checkPackRetry(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	request := r.Request(p.order.ID, "worker-1")
	ops := r.NewOps(p.s)
	if _, err := ops.Claim(r.Ctx, r.Scope, request, r.Actor); err != nil {
		return err
	}
	again, err := ops.Claim(r.Ctx, r.Scope, request, r.Actor)
	if err != nil {
		return err
	}
	if !again.Replayed {
		return Violatedf("a retry read %+v", again)
	}
	events, err := r.Events(p.s, r.Scope)
	if err != nil {
		return err
	}
	built := 0
	for _, e := range events {
		if e.Type == contextpacks.EventBuilt {
			built++
		}
	}
	if built != 1 {
		return Violatedf("%d packs built, want 1", built)
	}
	return r.CheckPacks(p.s)
}

func checkPackReplays(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	if err := r.NewOps(p.s).AddPackItem(r.Ctx, r.Scope, contextpacks.Addition{WorkOrder: p.order.ID, Kind: contextpacks.KindKnowledge, Ref: "wiki:1@2", Text: "A page."}, r.Actor); err != nil {
		return err
	}
	if _, err := p.claim(); err != nil {
		return err
	}
	return r.CheckPacks(p.s)
}

func checkPackFollowsLane(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	first, err := r.packOf(s.store, s.grant.ClaimID)
	if err != nil {
		return err
	}
	again, err := s.claim()
	if err != nil {
		return err
	}
	second, err := r.packOf(s.store, again.ClaimID)
	if err != nil {
		return err
	}
	if second.ID != first.ID || second.Version != first.Version || second.Text != first.Text {
		return Violatedf("a resumed claim pinned %s@%d, and the lane holds %s@%d", second.ID, second.Version, first.ID, first.Version)
	}
	return r.CheckPacks(s.store)
}

func checkPackDesired(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	pack, err := r.packOf(s.store, s.grant.ClaimID)
	if err != nil {
		return err
	}
	state, err := s.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: s.auth})
	if err != nil {
		return err
	}
	if len(state.Leases) != 1 || state.Leases[0].Pack == nil {
		return Violatedf("desired state %+v", state.Leases)
	}
	desired := state.Leases[0].Pack
	if desired.ID != pack.ID || desired.Version != pack.Version || desired.Digest != pack.Digest || desired.Text != pack.Text ||
		desired.TokensEstimate != pack.TokensEstimate {
		return Violatedf("desired %+v, pack %+v", desired, pack)
	}
	return nil
}

func checkPackNoContract(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	pack, err := r.packOf(s.store, s.grant.ClaimID)
	if err != nil {
		return err
	}
	if pack.Text == "" || pack.Digest == "" || len(pack.Items) != 0 {
		return Violatedf("pack %+v", pack)
	}
	return nil
}

func checkPackReadsEveryPage(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	last := ""
	for i := range ports.MaxRecords + 3 {
		decided, err := r.decision(p.s, fmt.Sprintf("Decision %d", i), func(d *questions.Decision) {
			d.Scope = p.order.RepositoryID + "/" + fmt.Sprint(i)
			d.Reversibility, d.NoConflict = questions.Reversible, nil
		})
		if err != nil {
			return err
		}
		last = decided.ID
	}
	var overlapping []questions.Decision
	err = p.s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		overlapping, err = tx.Questions().Overlapping(p.order.RepositoryID)
		return err
	})
	if err != nil {
		return err
	}
	if len(overlapping) != ports.MaxRecords+3 {
		return Violatedf("%d decisions overlap, and %d were made", len(overlapping), ports.MaxRecords+3)
	}
	grant, err := p.claim()
	if err != nil {
		return err
	}
	pack, err := r.packOf(p.s, grant.ClaimID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(pack.Items, func(item contextpacks.Item) bool { return item.Ref == last }) {
		return Violatedf("the last decision in scope is not in the pack, which holds %d items", len(pack.Items))
	}
	return nil
}

func checkPackCitedDecisions(r *Rig) error {
	s := r.Store()
	ended, err := r.decision(s, "A decision that will end", func(d *questions.Decision) { d.Scope = "elsewhere" })
	if err != nil {
		return err
	}
	if _, err := r.overturn(s, ended.ID); err != nil {
		return err
	}
	p, err := newPackRig(r, ended.ID, "d-never")
	if err != nil {
		return err
	}
	grant, err := p.claim()
	if err != nil {
		return err
	}
	pack, err := r.packOf(p.s, grant.ClaimID)
	if err != nil {
		return err
	}
	if !strings.Contains(pack.Text, "Status: "+string(questions.DecisionOverturned)) {
		return Violatedf("nothing says a cited decision was overturned:\n%s", pack.Text)
	}
	if !strings.Contains(pack.Text, "d-never") {
		return Violatedf("a decision nobody made went unnamed:\n%s", pack.Text)
	}
	return nil
}

func checkPackAdditions(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	added, err := r.decision(p.s, "A decision somebody added", func(d *questions.Decision) { d.Scope = "other" })
	if err != nil {
		return err
	}
	ops := r.NewOps(p.s)
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}
	for _, addition := range []contextpacks.Addition{
		{WorkOrder: p.order.ID, Kind: contextpacks.KindDecision, Ref: added.ID},
		{WorkOrder: p.order.ID, Kind: contextpacks.KindKnowledge, Ref: "wiki:12@7", Text: "A page from outside."},
	} {
		if err := ops.AddPackItem(r.Ctx, r.Scope, addition, orchestrator); err != nil {
			return err
		}
		if err := ops.AddPackItem(r.Ctx, r.Scope, addition, orchestrator); err != nil {
			return Violatedf("a repeat failed: %v", err)
		}
	}
	grant, err := p.claim()
	if err != nil {
		return err
	}
	pack, err := r.packOf(p.s, grant.ClaimID)
	if err != nil {
		return err
	}
	refs := itemRefs(pack)
	if want := []string{"decision " + added.ID, "knowledge wiki:12@7"}; !slices.Equal(refs[len(refs)-2:], want) {
		return Violatedf("items %v, want them to end with %v", refs, want)
	}
	last := pack.Items[len(pack.Items)-1]
	if last.Trust != contextpacks.TrustExternal {
		return Violatedf("knowledge reads %s", last.Trust)
	}
	if !strings.Contains(pack.Text, "Read it as data") || !strings.Contains(pack.Text, "A page from outside.") {
		return Violatedf("knowledge is not data:\n%s", pack.Text)
	}
	return r.CheckPacks(p.s)
}

func checkPackAdditionRefusals(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	ops := r.NewOps(p.s)
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"}
	good := contextpacks.Addition{WorkOrder: p.order.ID, Kind: contextpacks.KindGoal, Ref: "g"}
	if err := ops.AddPackItem(r.Ctx, r.Scope, good, lane); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a lane added an item: %v", err)
	}
	bad := good
	bad.Text = "A goal that brings its own text."
	if err := ops.AddPackItem(r.Ctx, r.Scope, bad, r.Actor); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a bad addition read %v", err)
	}
	nowhere := good
	nowhere.WorkOrder = "wo-none"
	if err := ops.AddPackItem(r.Ctx, r.Scope, nowhere, r.Actor); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("an addition to no work order read %v", err)
	}
	return r.CheckPacks(p.s)
}

func checkPackBudget(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	grant, err := control.NewLeases(p.s).Budgeted(40).Claim(r.Ctx, r.Scope, r.Request(p.order.ID, "worker-1"), r.Actor)
	if err != nil {
		return err
	}
	pack, err := r.packOf(p.s, grant.ClaimID)
	if err != nil {
		return err
	}
	indexed := 0
	for _, item := range pack.Items {
		if item.Indexed {
			indexed++
		}
	}
	if indexed == 0 || !strings.Contains(pack.Text, contextpacks.ToolGet) {
		return Violatedf("a budget of 40 tokens held all of it: %+v", pack.Items)
	}
	return r.CheckPacks(p.s)
}

func checkPackGoals(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	err = p.s.Write(r.Ctx, r.Scope, "note", func(tx ports.WriteTx) error {
		if err := tx.Planning().SaveGoal(planning.Goal{ID: "g-old", Title: "An old goal", Status: "closed", Statement: "No longer pursued."}); err != nil {
			return err
		}
		_, err := tx.Append(r.noteDraft("goal"))
		return err
	})
	if err != nil {
		return err
	}
	grant, err := p.claim()
	if err != nil {
		return err
	}
	pack, err := r.packOf(p.s, grant.ClaimID)
	if err != nil {
		return err
	}
	if strings.Contains(pack.Text, "No longer pursued.") {
		return Violatedf("a closed goal reached the pack")
	}
	return nil
}

// laneRead reads through the plane, as a lane's worker does.
func (s *statusRig) laneRead(change func(*ledger.LaneContextRequest)) (ledger.LaneContext, error) {
	request := ledger.LaneContextRequest{Auth: s.auth, Epoch: s.epoch, WorkOrder: s.grant.WorkOrder, Fence: s.grant.Fence, Lane: s.lane}
	change(&request)
	return s.plane.LaneContext(s.r.Ctx, request)
}

func (s *statusRig) lookup(kind contextpacks.Kind, ref string) (ledger.LaneContext, error) {
	return s.laneRead(func(request *ledger.LaneContextRequest) {
		request.Tool, request.Kind, request.Ref = lanes.ToolContextGet, string(kind), ref
	})
}

func oneRecord(found ledger.LaneContext, kind contextpacks.Kind, ref, text string) error {
	if len(found.Records) != 1 {
		return Violatedf("found %+v", found)
	}
	record := found.Records[0]
	if record.Kind != string(kind) || record.Ref != ref || !strings.Contains(record.Text, text) {
		return Violatedf("a read of %s %s found %+v", kind, ref, record)
	}
	return nil
}

func checkContextLookups(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	source := fake.NewWorkSource()
	source.Write("requirements.md", []byte(packRequirements))
	source.Write("goal.md", []byte(packGoal))
	source.Write(packContractFile, []byte(packContract("Pack work")))
	if _, err := r.intake(s.store, source); err != nil {
		return err
	}
	order, err := r.orderByTitle(s.store, "Pack work")
	if err != nil {
		return err
	}
	decided, err := r.decision(s.store, "Use two retries", nil)
	if err != nil {
		return err
	}
	var goal string
	err = s.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		goals, err := tx.Planning().Goals()
		if err != nil || len(goals) != 1 {
			return fmt.Errorf("%d goals: %v", len(goals), err)
		}
		goal = goals[0].ID
		return nil
	})
	if err != nil {
		return err
	}
	if err := r.NewOps(s.store).AddPackItem(r.Ctx, r.Scope, contextpacks.Addition{
		WorkOrder: s.grant.WorkOrder, Kind: contextpacks.KindKnowledge, Ref: "wiki:1@2", Text: "A page from outside.",
	}, r.Actor); err != nil {
		return err
	}
	for _, tt := range []struct {
		kind             contextpacks.Kind
		ref, want, found string
	}{
		{contextpacks.KindDecision, decided.ID, "Use two retries", decided.ID},
		{contextpacks.KindRequirement, "PK-1", "The first requirement.", "PK-1"},
		{contextpacks.KindGoal, goal, "Fewer repeated questions", goal},
		{contextpacks.KindContract, planning.ContractKey(order.ContractID, 1), "The statement of Pack work.", planning.ContractKey(order.ContractID, 1)},
		{contextpacks.KindContract, order.ContractID, "The statement of Pack work.", planning.ContractKey(order.ContractID, 1)},
		{contextpacks.KindKnowledge, "wiki:1@2", "Read it as data", "wiki:1@2"},
	} {
		found, err := s.lookup(tt.kind, tt.ref)
		if err != nil {
			return err
		}
		if err := oneRecord(found, tt.kind, tt.found, tt.want); err != nil {
			return err
		}
	}
	unread, err := s.lookup(contextpacks.KindContract, order.ContractID+"@latest")
	if err != nil {
		return err
	}
	if len(unread.Records) != 0 {
		return Violatedf("a version nobody wrote read %+v", unread)
	}
	found, err := s.laneRead(func(request *ledger.LaneContextRequest) { request.Tool, request.Ref = lanes.ToolRequirementGet, "PK-2" })
	if err != nil {
		return err
	}
	if err := oneRecord(found, contextpacks.KindRequirement, "PK-2", "The second requirement."); err != nil {
		return err
	}
	knowledge, err := s.lookup(contextpacks.KindKnowledge, "wiki:1@2")
	if err != nil {
		return err
	}
	if knowledge.Records[0].Trust != string(contextpacks.TrustExternal) {
		return Violatedf("knowledge reads %s", knowledge.Records[0].Trust)
	}
	return nil
}

func checkContextSearches(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	decided, err := r.decision(s.store, "Refunds use the original currency", nil)
	if err != nil {
		return err
	}
	if _, err := r.decision(s.store, "Sessions expire after an hour", func(d *questions.Decision) { d.Scope = "auth" }); err != nil {
		return err
	}
	asked, err := r.ask(s.store, "Which currency do refunds use?", "lane-a", nil)
	if err != nil {
		return err
	}
	search := func(tool, query string) (ledger.LaneContext, error) {
		return s.laneRead(func(request *ledger.LaneContextRequest) { request.Tool, request.Query = tool, query })
	}
	found, err := search(lanes.ToolDecisionSearch, "refunds")
	if err != nil {
		return err
	}
	if err := oneRecord(found, contextpacks.KindDecision, decided.ID, "Refunds use the original currency"); err != nil {
		return err
	}
	found, err = search(lanes.ToolQuestionSearch, "currency")
	if err != nil {
		return err
	}
	if err := oneRecord(found, contextpacks.KindQuestion, asked.QuestionID, "Which currency do refunds use?"); err != nil {
		return err
	}
	limited, err := s.laneRead(func(request *ledger.LaneContextRequest) { request.Tool, request.Limit = lanes.ToolDecisionSearch, 1 })
	if err != nil {
		return err
	}
	if len(limited.Records) != 1 {
		return Violatedf("a limit of one found %d", len(limited.Records))
	}
	return nil
}

func checkContextMisses(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	for _, tt := range []struct {
		kind contextpacks.Kind
		ref  string
	}{
		{contextpacks.KindDecision, "d-none"}, {contextpacks.KindRequirement, "PK-NONE"}, {contextpacks.KindGoal, "g-none"},
		{contextpacks.KindContract, "c-none"}, {contextpacks.KindContract, "c-none@9"}, {contextpacks.KindKnowledge, "wiki:none"},
	} {
		found, err := s.lookup(tt.kind, tt.ref)
		if err != nil {
			return err
		}
		if len(found.Records) != 0 {
			return Violatedf("%s %s found %+v", tt.kind, tt.ref, found)
		}
	}
	if _, err := s.laneRead(func(request *ledger.LaneContextRequest) { request.Tool = "lane_delete" }); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a tool nobody named read %v", err)
	}
	if _, err := s.laneRead(func(request *ledger.LaneContextRequest) { request.Tool, request.Kind = lanes.ToolContextGet, "note" }); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a kind nobody named read %v", err)
	}
	return nil
}

func checkContextReach(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	other, err := r.NewWorkOrder(s.store)
	if err != nil {
		return err
	}
	if err := r.NewOps(s.store).AddPackItem(r.Ctx, r.Scope, contextpacks.Addition{
		WorkOrder: other.ID, Kind: contextpacks.KindKnowledge, Ref: "wiki:other", Text: "Another work order's page.",
	}, r.Actor); err != nil {
		return err
	}
	found, err := s.lookup(contextpacks.KindKnowledge, "wiki:other")
	if err != nil {
		return err
	}
	if len(found.Records) != 0 {
		return Violatedf("a lane read another work order's knowledge: %+v", found)
	}
	stranger := *s
	stranger.auth.Credential = "wrong"
	if _, err := stranger.lookup(contextpacks.KindDecision, "d-1"); err == nil {
		return Violatedf("a worker with no credential read the ledger")
	}
	return nil
}

func checkContextFence(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	var fenced *workorders.FencedError
	if _, err := s.laneRead(func(request *ledger.LaneContextRequest) {
		request.Tool, request.Kind, request.Ref, request.Fence = lanes.ToolContextGet, string(contextpacks.KindDecision), "d-1", s.grant.Fence+1
	}); !errors.As(err, &fenced) {
		return Violatedf("a stale fence read %v", err)
	}
	return nil
}

func checkBaselineUpload(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(ledger.GenerationBaseline{Tokens: 31000})
	if err != nil {
		return err
	}
	if err := accepted(s.uploadAt(1, ledger.UploadGenerationBaseline, raw)); err != nil {
		return err
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 1 || generations[0].BaselineTokens != 31000 {
		return Violatedf("generations %+v", generations)
	}
	stale, err := s.uploadAt(9, ledger.UploadGenerationBaseline, raw)
	if err != nil {
		return err
	}
	if len(stale.Rejected) != 1 {
		return Violatedf("a generation that is not current recorded a baseline: %+v", stale)
	}
	return r.CheckProjection(s.store)
}

// checkPackRatio holds a measured baseline to what it means: the slope across two sizes of pack,
// with what every session starts with left out of it.
func checkPackRatio(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	measure := func(tx ports.WriteTx, id, text string, baseline int64) error {
		pack := contextpacks.Pack{ID: id, Version: 1, Digest: "sha256:0", WorkOrder: "wo", Text: text}
		if err := tx.Packs().Insert(pack); err != nil {
			return err
		}
		if err := tx.Lanes().StartGeneration(ledger.Generation{Lane: id, Number: 1, StartReason: ledger.StartInitial}); err != nil {
			return err
		}
		if err := tx.Lanes().ReadPack(id, 1, id, 1); err != nil {
			return err
		}
		return tx.Lanes().Baseline(id, 1, baseline)
	}
	err = p.s.Write(r.Ctx, r.Scope, "note", func(tx ports.WriteTx) error {
		if err := measure(tx, "small", strings.Repeat("x", 4000), 21000); err != nil {
			return err
		}
		if err := measure(tx, "large", strings.Repeat("x", 44000), 31000); err != nil {
			return err
		}
		if err := tx.Lanes().StartGeneration(ledger.Generation{Lane: "resumed", Number: 1, StartReason: ledger.StartResume}); err != nil {
			return err
		}
		if err := tx.Lanes().ReadPack("resumed", 1, "large", 1); err != nil {
			return err
		}
		if err := tx.Lanes().Baseline("resumed", 1, 900000); err != nil {
			return err
		}
		_, err := tx.Append(r.noteDraft("measured"))
		return err
	})
	if err != nil {
		return err
	}
	grant, err := p.claim()
	if err != nil {
		return err
	}
	pack, err := r.packOf(p.s, grant.ClaimID)
	if err != nil {
		return err
	}
	const measured = 4.0
	want := int64(math.Ceil(float64(utf8.RuneCountInString(pack.Text)) / measured))
	if pack.TokensEstimate != want {
		return Violatedf("an estimate of %d, and a measured ratio of %v gives %d", pack.TokensEstimate, measured, want)
	}
	return nil
}

func checkPackNewClaim(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	first, err := p.claim()
	if err != nil {
		return err
	}
	ops := r.NewOps(p.s)
	if err := ops.Release(r.Ctx, r.Scope, first.Lease, r.Actor); err != nil {
		return err
	}
	second, err := p.claim()
	if err != nil {
		return err
	}
	a, err := r.packOf(p.s, first.ClaimID)
	if err != nil {
		return err
	}
	b, err := r.packOf(p.s, second.ClaimID)
	if err != nil {
		return err
	}
	if a.ID == b.ID {
		return Violatedf("two claims with no lane share pack %s", a.ID)
	}
	if a.Digest != b.Digest {
		return Violatedf("one content gave two digests")
	}
	return r.CheckPacks(p.s)
}
