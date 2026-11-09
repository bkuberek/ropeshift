package contract

import (
	"errors"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// NoticeChecks are the change table: what reaches a lane when something moves under its pack
// (FR-CTX-3, FR-PLAN-8).
var NoticeChecks = []Check{
	{"N1", "a decision replaced under a pack tells its lanes, and the next generation reads a new version", checkPackedReplaced},
	{"N2", "a decision overturned under a pack does the same", checkPackedOverturned},
	{"N3", "the index finds a lane that nothing else connects to the decision", checkNoticeThroughTheIndex},
	{"N4", "a replacement is carried past a chain of them, whatever put the first in the pack", checkNoticeChain},
	{"N5", "a lane's running generation keeps the version it read", checkNoticeKeepsARunningLane},
	{"N6", "a one-way decision in a lane's scope tells it, and the default policy leaves it running", checkNoticeOneWay},
	{"N7", "a one-way decision parks a lane the policy says to", checkNoticeOneWayParks},
	{"N8", "a reversible decision in a lane's scope asks nothing of it", checkNoticeReversibleAsksNothing},
	{"N9", "a new contract version tells a running lane and leaves its pin", checkNoticeContract},
	{"N10", "a new contract version parks a lane the policy says to", checkNoticeContractParks},
	{"N11", "unstarted work takes a new version with nobody to tell", checkNoticeUnstarted},
	{"N12", "a notice a session missed reaches the next generation's first prompt", checkNoticeMissed},
	{"N13", "a parked lane hears of a change as a fact, and nothing asks it to park", checkNoticeParkedLane},
	{"N14", "an ended lane hears nothing", checkNoticeEndedLane},
	{"N15", "a parked commit answers what a policy asked", checkNoticeParkSettles},
	{"N16", "a lane policy is published like a decision policy", checkLanePolicyPublished},
	{"N17", "a lane policy that names no action is refused", checkLanePolicyRefused},
	{"N18", "a woken lane whose work takes a new version is still told", checkNoticeWokenLane},
	{"N19", "a version that dropped a record pins it no more", checkNoticeDroppedRecord},
	{"N20", "an answer one-way lanes hear of parks them the same way", checkNoticeOneWayAnswer},
	{"N21", "a contract notice takes the window of its kind", checkNoticeWindow},
	{"N22", "a renewed pack keeps to the budget it was given", checkNoticeBudget},
}

// packedLane is a running lane under a contract, whose pack cites what a test decided first.
func packedLane(r *Rig, cited ...string) (*statusRig, *packRig, error) {
	return packedLaneWith(r, func(*packRig) error { return nil }, cited...)
}

// packedLaneWith lets a test add to a work order's pack before its claim builds one.
func packedLaneWith(r *Rig, prepare func(*packRig) error, cited ...string) (*statusRig, *packRig, error) {
	s := r.Store()
	p := &packRig{r: r, s: s, source: fake.NewWorkSource()}
	p.source.Write("requirements.md", []byte(packRequirements))
	p.source.Write("goal.md", []byte(packGoal))
	p.source.Write(packContractFile, []byte(packContract("Pack work", cited...)))
	status, err := newStatusRigOn(r, s, func(store ports.Store) (string, error) {
		if _, err := r.intake(store, p.source); err != nil {
			return "", err
		}
		var err error
		if p.order, err = r.orderByTitle(store, "Pack work"); err != nil {
			return "", err
		}
		if err := prepare(p); err != nil {
			return "", err
		}
		_, err = r.NewOps(store).Pass(r.Ctx, r.Scope, p.order.ID, r.member())
		return p.order.ID, err
	})
	return status, p, err
}

// addedDecision decides something in a scope nothing about a lane touches, and adds it to the pack,
// so only the pack's own items connect the two.
func addedDecision(p *packRig, statement string) (questions.Decision, error) {
	decided, err := p.r.decision(p.s, statement, func(d *questions.Decision) { d.Scope = "elsewhere" })
	if err != nil {
		return questions.Decision{}, err
	}
	addition := contextpacks.Addition{WorkOrder: p.order.ID, Kind: contextpacks.KindDecision, Ref: decided.ID}
	return decided, p.r.NewOps(p.s).AddPackItem(p.r.Ctx, p.r.Scope, addition, p.r.Actor)
}

// decidedFirst records one decision before a rig exists.
func decidedFirst(r *Rig, change func(*questions.Decision)) (questions.Decision, error) {
	return r.decision(r.Store(), "Use two retries", change)
}

// replacing records a decision that supersedes another, as a member does.
func (r *Rig) replacing(s ports.Store, old, statement string) (questions.Decision, error) {
	return r.decision(s, statement, func(d *questions.Decision) { d.Supersedes = old })
}

func noticesOf(s *statusRig, kind messages.Kind) ([]messages.Message, error) {
	rows, err := s.r.NewOps(s.store).LaneMessages(s.r.Ctx, s.r.Scope, s.lane)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(rows, func(m messages.Message) bool { return m.Kind != kind }), nil
}

// expectNotice holds a lane to one notice that says what a test names.
func expectNotice(s *statusRig, kind messages.Kind, text string) error {
	notices, err := noticesOf(s, kind)
	if err != nil {
		return err
	}
	if len(notices) != 1 || !strings.Contains(notices[0].Body, text) {
		return Violatedf("notices %+v, want one saying %q", notices, text)
	}
	return nil
}

func (s *statusRig) newestPack() (contextpacks.Pack, error) {
	return s.r.packOf(s.store, s.grant.ClaimID)
}

// wanted reads what desired state asks of this lane.
func (s *statusRig) wanted() (string, error) {
	state, err := s.desired()
	if err != nil {
		return "", err
	}
	for _, lease := range state.Leases {
		if lease.Lane == s.lane {
			return lease.ParkWanted, nil
		}
	}
	return "", Violatedf("no lease of lane %s in %+v", s.lane, state.Leases)
}

func packHolds(pack contextpacks.Pack, ref string) bool {
	return slices.ContainsFunc(pack.Items, func(item contextpacks.Item) bool { return item.Ref == ref })
}

func publishLanePolicy(s *statusRig, version int64, onContract, onOneWay string) error {
	body := "[lane_policy]\nversion = " + string(rune('0'+version)) + "\non_contract_version = \"" + onContract + "\"\non_one_way_decision = \"" + onOneWay + "\"\n"
	_, err := s.r.NewOps(s.store).PublishLanePolicy(s.r.Ctx, s.r.Scope, authority.LanePolicyDraft{
		Body: body, Policy: authority.LanePolicy{Version: version, OnContractVersion: onContract, OnOneWayDecision: onOneWay},
	}, s.r.Actor)
	return err
}

func checkPackedReplaced(r *Rig) error {
	decided, err := decidedFirst(r, nil)
	if err != nil {
		return err
	}
	status, _, err := packedLane(r, decided.ID)
	if err != nil {
		return err
	}
	first, err := status.newestPack()
	if err != nil {
		return err
	}
	if !packHolds(first, decided.ID) {
		return Violatedf("the pack holds %v", itemRefs(first))
	}
	next, err := r.replacing(status.store, decided.ID, "Use three retries")
	if err != nil {
		return err
	}
	if err := expectNotice(status, messages.KindDecisionNotice, "Use three retries"); err != nil {
		return err
	}
	pack, err := status.newestPack()
	if err != nil {
		return err
	}
	if pack.ID != first.ID || pack.Version != first.Version+1 {
		return Violatedf("pack %s@%d follows %s@%d", pack.ID, pack.Version, first.ID, first.Version)
	}
	if !packHolds(pack, next.ID) || !strings.Contains(pack.Text, "Use three retries") {
		return Violatedf("the new version does not hold the replacement: %v", itemRefs(pack))
	}
	if !strings.Contains(pack.Text, "Status: "+string(questions.DecisionReplaced)) {
		return Violatedf("the contract cites a decision that ended, and the new version does not say so:\n%s", pack.Text)
	}
	return r.CheckPacks(status.store)
}

func checkPackedOverturned(r *Rig) error {
	decided, err := decidedFirst(r, nil)
	if err != nil {
		return err
	}
	status, _, err := packedLane(r, decided.ID)
	if err != nil {
		return err
	}
	result, err := r.overturn(status.store, decided.ID)
	if err != nil {
		return err
	}
	if !slices.Contains(result.Noticed, status.lane) {
		return Violatedf("noticed %v", result.Noticed)
	}
	if err := expectNotice(status, messages.KindDecisionNotice, result.New.ID); err != nil {
		return err
	}
	pack, err := status.newestPack()
	if err != nil {
		return err
	}
	if pack.Version != 2 || !packHolds(pack, result.New.ID) {
		return Violatedf("pack version %d holds %v", pack.Version, itemRefs(pack))
	}
	return r.CheckPacks(status.store)
}

func checkNoticeThroughTheIndex(r *Rig) error {
	var decided questions.Decision
	status, _, err := packedLaneWith(r, func(p *packRig) error {
		var err error
		decided, err = addedDecision(p, "A decision from elsewhere")
		return err
	})
	if err != nil {
		return err
	}
	if _, err := r.replacing(status.store, decided.ID, "A replacement from elsewhere"); err != nil {
		return err
	}
	return expectNotice(status, messages.KindDecisionNotice, "A replacement from elsewhere")
}

// checkNoticeChain replaces a decision twice, in a scope no lane overlaps. A new version holds what
// it replaced in force, so a pack cannot lose a decision to its own history.
func checkNoticeChain(r *Rig) error {
	var first questions.Decision
	status, _, err := packedLaneWith(r, func(p *packRig) error {
		var err error
		first, err = addedDecision(p, "The first")
		return err
	})
	if err != nil {
		return err
	}
	second, err := r.replacing(status.store, first.ID, "The second")
	if err != nil {
		return err
	}
	third, err := r.replacing(status.store, second.ID, "The third")
	if err != nil {
		return err
	}
	pack, err := status.newestPack()
	if err != nil {
		return err
	}
	if !packHolds(pack, third.ID) {
		return Violatedf("the chain ends outside the pack: %v", itemRefs(pack))
	}
	if !strings.Contains(pack.Text, "The third") {
		return Violatedf("the pack does not say the third:\n%s", pack.Text)
	}
	if packHolds(pack, second.ID) {
		return Violatedf("a decision between two others stayed in the pack: %v", itemRefs(pack))
	}
	return r.CheckPacks(status.store)
}

func checkNoticeKeepsARunningLane(r *Rig) error {
	decided, err := decidedFirst(r, nil)
	if err != nil {
		return err
	}
	status, _, err := packedLane(r, decided.ID)
	if err != nil {
		return err
	}
	first, err := status.newestPack()
	if err != nil {
		return err
	}
	if _, err := r.replacing(status.store, decided.ID, "Use three retries"); err != nil {
		return err
	}
	var kept contextpacks.Pack
	err = status.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		kept, err = tx.Packs().Pack(first.ID, first.Version)
		return err
	})
	if err != nil {
		return err
	}
	if kept.Text != first.Text || kept.Digest != first.Digest {
		return Violatedf("a version changed once it was written")
	}
	facts, err := status.plane.LaneFacts(r.Ctx, ledger.LaneFactsRequest{
		Auth: status.auth, Epoch: status.epoch, WorkOrder: status.grant.WorkOrder, Fence: status.grant.Fence, Lane: status.lane,
	})
	if err != nil {
		return err
	}
	if facts.Pack == nil || facts.Pack.Version != first.Version+1 {
		return Violatedf("the next generation reads %+v", facts.Pack)
	}
	return nil
}

func checkNoticeOneWay(r *Rig) error {
	status, p, err := packedLane(r)
	if err != nil {
		return err
	}
	decided, err := r.decision(status.store, "Keep audit data in region", func(d *questions.Decision) {
		d.Scope, d.Reversibility = p.order.RepositoryID, questions.OneWay
	})
	if err != nil {
		return err
	}
	if err := expectNotice(status, messages.KindDecisionNotice, decided.ID); err != nil {
		return err
	}
	wanted, err := status.wanted()
	if err != nil {
		return err
	}
	if wanted != "" {
		return Violatedf("the default policy asked %q", wanted)
	}
	return r.CheckPacks(status.store)
}

func checkNoticeOneWayParks(r *Rig) error {
	status, p, err := packedLane(r)
	if err != nil {
		return err
	}
	if err := publishLanePolicy(status, 2, authority.LaneNotify, authority.LanePark); err != nil {
		return err
	}
	if _, err := r.decision(status.store, "Keep audit data in region", func(d *questions.Decision) {
		d.Scope, d.Reversibility = p.order.RepositoryID, questions.OneWay
	}); err != nil {
		return err
	}
	wanted, err := status.wanted()
	if err != nil {
		return err
	}
	if wanted == "" {
		return Violatedf("the policy parks on a one-way decision, and desired state asks nothing")
	}
	return r.CheckPacks(status.store)
}

func checkNoticeReversibleAsksNothing(r *Rig) error {
	status, p, err := packedLane(r)
	if err != nil {
		return err
	}
	if err := publishLanePolicy(status, 2, authority.LaneNotify, authority.LanePark); err != nil {
		return err
	}
	if _, err := r.decision(status.store, "A decision anyone can take back", func(d *questions.Decision) { d.Scope = p.order.RepositoryID }); err != nil {
		return err
	}
	notices, err := noticesOf(status, messages.KindDecisionNotice)
	if err != nil {
		return err
	}
	wanted, err := status.wanted()
	if err != nil {
		return err
	}
	if len(notices) != 0 || wanted != "" {
		return Violatedf("notices %+v, wanted %q", notices, wanted)
	}
	return nil
}

// changeContract records a new version of the rig's contract.
func (p *packRig) changeContract() error {
	p.source.Write(packContractFile, []byte(strings.Replace(packContract("Pack work"), "The statement of Pack work.", "A later statement.", 1)))
	_, err := p.r.intake(p.s, p.source)
	return err
}

func checkNoticeContract(r *Rig) error {
	status, p, err := packedLane(r)
	if err != nil {
		return err
	}
	if err := p.changeContract(); err != nil {
		return err
	}
	if err := expectNotice(status, messages.KindContractNotice, p.order.ContractID+"@2"); err != nil {
		return err
	}
	row, err := r.WorkOrder(p.s, p.order.ID)
	if err != nil {
		return err
	}
	if row.ContractVersion != 1 {
		return Violatedf("a running lane moved to version %d", row.ContractVersion)
	}
	wanted, err := status.wanted()
	if err != nil {
		return err
	}
	if wanted != "" {
		return Violatedf("the default policy asked %q", wanted)
	}
	pack, err := status.newestPack()
	if err != nil {
		return err
	}
	if pack.Version != 1 {
		return Violatedf("a pinned contract took pack version %d", pack.Version)
	}
	if err := p.changeContract(); err != nil {
		return err
	}
	return expectNotice(status, messages.KindContractNotice, p.order.ContractID+"@2")
}

func checkNoticeContractParks(r *Rig) error {
	status, p, err := packedLane(r)
	if err != nil {
		return err
	}
	if err := publishLanePolicy(status, 2, authority.LanePark, authority.LaneNotify); err != nil {
		return err
	}
	if err := p.changeContract(); err != nil {
		return err
	}
	wanted, err := status.wanted()
	if err != nil {
		return err
	}
	if wanted == "" {
		return Violatedf("the policy parks on a new version, and desired state asks nothing")
	}
	return r.CheckPacks(status.store)
}

func checkNoticeUnstarted(r *Rig) error {
	p, err := newPackRig(r)
	if err != nil {
		return err
	}
	if err := p.changeContract(); err != nil {
		return err
	}
	rows, err := r.NewOps(p.s).LaneMessages(r.Ctx, r.Scope, "lane-1")
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		return Violatedf("unstarted work told %+v", rows)
	}
	row, err := r.WorkOrder(p.s, p.order.ID)
	if err != nil {
		return err
	}
	if row.ContractVersion != 2 {
		return Violatedf("unstarted work stayed on version %d", row.ContractVersion)
	}
	return nil
}

func checkNoticeMissed(r *Rig) error {
	decided, err := decidedFirst(r, nil)
	if err != nil {
		return err
	}
	status, p, err := packedLane(r, decided.ID)
	if err != nil {
		return err
	}
	if _, err := r.replacing(status.store, decided.ID, "Use three retries"); err != nil {
		return err
	}
	if err := p.changeContract(); err != nil {
		return err
	}
	if err := status.nextGeneration(2, ledger.StartHandoff); err != nil {
		return err
	}
	facts, err := status.laneFacts(status.grant.Fence)
	if err != nil {
		return err
	}
	kinds := project(facts, func(fact ledger.MessageFact) string { return fact.Kind })
	slices.Sort(kinds)
	want := []string{string(messages.KindContractNotice), string(messages.KindDecisionNotice)}
	if !slices.Equal(kinds, want) {
		return Violatedf("the next generation owes %v, want %v", kinds, want)
	}
	return nil
}

func checkNoticeParkedLane(r *Rig) error {
	s, _, err := parkedOn(r, true)
	if err != nil {
		return err
	}
	if err := publishLanePolicy(s, 2, authority.LaneNotify, authority.LanePark); err != nil {
		return err
	}
	if _, err := r.decision(s.store, "A one-way decision", func(d *questions.Decision) {
		d.Scope, d.Reversibility = s.grant.WorkOrder, questions.OneWay
	}); err != nil {
		return err
	}
	notices, err := noticesOf(s, messages.KindDecisionNotice)
	if err != nil {
		return err
	}
	if len(notices) != 1 || notices[0].State != messages.Expired || notices[0].Reason != messages.ReasonLedgerFact {
		return Violatedf("a parked lane's notice reads %+v", notices)
	}
	var row ledger.LaneRow
	err = s.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Lanes().Row(s.lane)
		return err
	})
	if err != nil {
		return err
	}
	if row.ParkWanted != "" {
		return Violatedf("a parked lane was asked to park: %q", row.ParkWanted)
	}
	return r.CheckPacks(s.store)
}

func checkNoticeEndedLane(r *Rig) error {
	decided, err := decidedFirst(r, nil)
	if err != nil {
		return err
	}
	status, p, err := packedLane(r, decided.ID)
	if err != nil {
		return err
	}
	ops := r.NewOps(status.store)
	holder := workorders.Holder{Lease: status.grant.Lease, Lane: status.lane, Generation: 1}
	if err := ops.EndLane(r.Ctx, r.Scope, holder, workorders.LaneResult{Outcome: workorders.OutcomeCompleted}, r.Actor); err != nil {
		return err
	}
	if _, err := r.replacing(status.store, decided.ID, "Use three retries"); err != nil {
		return err
	}
	if err := p.changeContract(); err != nil {
		return err
	}
	rows, err := ops.LaneMessages(r.Ctx, r.Scope, status.lane)
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		return Violatedf("an ended lane was told %+v", rows)
	}
	pack, err := r.packOf(status.store, status.grant.ClaimID)
	if err != nil {
		return err
	}
	if pack.Version != 1 {
		return Violatedf("a pack no generation can read took version %d", pack.Version)
	}
	return nil
}

func checkNoticeWindow(r *Rig) error {
	status, p, err := packedLane(r)
	if err != nil {
		return err
	}
	const window = 7 * time.Minute
	p.source.Write(packContractFile, []byte(strings.Replace(packContract("Pack work"), "The statement of Pack work.", "A later statement.", 1)))
	telling := control.NewIntake(status.store).Telling(messages.Windows{messages.KindContractNotice: {StaleAfter: time.Minute, ExpireAfter: window}})
	if _, err := telling.Poll(r.Ctx, r.Scope, p.source); err != nil {
		return err
	}
	notices, err := noticesOf(status, messages.KindContractNotice)
	if err != nil {
		return err
	}
	if len(notices) != 1 || notices[0].ExpiresAt-notices[0].CreatedAt != window.Milliseconds() {
		return Violatedf("a notice reads %+v", notices)
	}
	return nil
}

func checkNoticeBudget(r *Rig) error {
	decided, err := decidedFirst(r, nil)
	if err != nil {
		return err
	}
	status, _, err := packedLane(r, decided.ID)
	if err != nil {
		return err
	}
	draft := questions.Decision{ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Use three retries", Supersedes: decided.ID}
	if _, err := control.NewQuestions(status.store, questions.BatchPolicy{}).Budgeted(40).RecordDecision(r.Ctx, r.Scope, draft, r.Actor); err != nil {
		return err
	}
	pack, err := status.newestPack()
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(pack.Items, func(item contextpacks.Item) bool { return item.Indexed }) {
		return Violatedf("a budget of 40 tokens held all of %v", itemRefs(pack))
	}
	return nil
}

func checkNoticeParkSettles(r *Rig) error {
	status, p, err := packedLane(r)
	if err != nil {
		return err
	}
	if err := publishLanePolicy(status, 2, authority.LanePark, authority.LaneNotify); err != nil {
		return err
	}
	if err := p.changeContract(); err != nil {
		return err
	}
	wake := r.ID("wake")
	if err := accepted(status.parkingUpload(wake, `{"human_resume":true}`)); err != nil {
		return err
	}
	if err := accepted(status.parkedUpload(wake, true)); err != nil {
		return err
	}
	if err := status.woken(); err != nil {
		return err
	}
	grant, err := status.claim()
	if err != nil {
		return err
	}
	status.grant = grant
	wanted, err := status.wanted()
	if err != nil {
		return err
	}
	if wanted != "" {
		return Violatedf("a lane that came back was asked to park again: %q", wanted)
	}
	return r.CheckPacks(status.store)
}

// checkNoticeWokenLane moves a parked lane's work back to ready, where a new version takes it. The
// lane has held a pack through all of that, so it must hear what changed under it.
func checkNoticeWokenLane(r *Rig) error {
	status, p, err := packedLane(r)
	if err != nil {
		return err
	}
	wake := r.ID("wake")
	if err := accepted(status.parkingUpload(wake, `{"human_resume":true}`)); err != nil {
		return err
	}
	if err := accepted(status.parkedUpload(wake, true)); err != nil {
		return err
	}
	if err := status.woken(); err != nil {
		return err
	}
	if err := p.changeContract(); err != nil {
		return err
	}
	notices, err := noticesOf(status, messages.KindContractNotice)
	if err != nil {
		return err
	}
	if len(notices) != 1 {
		return Violatedf("a woken lane read %+v", notices)
	}
	return nil
}

func checkNoticeDroppedRecord(r *Rig) error {
	status, p, err := packedLaneWith(r, func(p *packRig) error {
		_, err := r.decision(p.s, "A decision in scope", func(d *questions.Decision) { d.Scope = p.order.RepositoryID })
		return err
	})
	if err != nil {
		return err
	}
	first, err := status.newestPack()
	if err != nil {
		return err
	}
	var old string
	for _, item := range first.Items {
		if item.Kind == contextpacks.KindDecision {
			old = item.Ref
		}
	}
	if old == "" {
		return Violatedf("no decision in %v", itemRefs(first))
	}
	if _, err := r.decision(p.s, "A replacement elsewhere", func(d *questions.Decision) { d.Scope, d.Supersedes = "elsewhere", old }); err != nil {
		return err
	}
	return status.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		packs, err := tx.Packs().Pinning(contextpacks.KindDecision, old)
		if err != nil {
			return err
		}
		if len(packs) != 0 {
			return Violatedf("a record the newest version dropped still pins %v", packs)
		}
		return nil
	})
}

// checkNoticeOneWayAnswer takes the other road to a one-way decision: a person answering a question
// that climbed the ladder.
func checkNoticeOneWayAnswer(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := publishLanePolicy(s, 2, authority.LaneNotify, authority.LanePark); err != nil {
		return err
	}
	asked, err := r.ask(s.store, "May audit data leave the region?", "lane-a", func(a *questions.Ask) {
		a.Categories, a.SubjectKind, a.SubjectID = []string{questions.CategorySecurity}, workorders.SubjectKind, s.grant.WorkOrder
	})
	if err != nil {
		return err
	}
	if _, err := r.answer(s.store, asked.QuestionID, "It may not."); err != nil {
		return err
	}
	if err := expectNotice(s, messages.KindDecisionNotice, "It may not."); err != nil {
		return err
	}
	wanted, err := s.wanted()
	if err != nil {
		return err
	}
	if wanted == "" {
		return Violatedf("a one-way answer asked nothing of the lane")
	}
	return r.CheckPacks(s.store)
}

func checkLanePolicyPublished(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	ops := r.NewOps(s.store)
	versions, err := ops.LanePolicyVersions(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(versions) != 1 || versions[0].Version != 1 || versions[0].Kind != authority.PolicyLane {
		return Violatedf("a workspace starts on %+v", versions)
	}
	if err := publishLanePolicy(s, 2, authority.LanePark, authority.LaneNotify); err != nil {
		return err
	}
	if err := publishLanePolicy(s, 2, authority.LanePark, authority.LaneNotify); err != nil {
		return Violatedf("a retry failed: %v", err)
	}
	if err := publishLanePolicy(s, 2, authority.LaneNotify, authority.LaneNotify); !errors.Is(err, authority.ErrBadLanePolicy) {
		return Violatedf("a version that is not newer read %v", err)
	}
	versions, err = ops.LanePolicyVersions(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(versions) != 2 || versions[0].Version != 2 {
		return Violatedf("versions %+v", versions)
	}
	decisions, err := ops.PolicyVersions(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(decisions) != 1 || decisions[0].Kind != authority.PolicyDecision {
		return Violatedf("the decision policy reads %+v", decisions)
	}
	_, err = ops.PublishLanePolicy(r.Ctx, r.Scope, authority.LanePolicyDraft{}, ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "o"})
	if !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("an orchestrator published a policy: %v", err)
	}
	return r.CheckQuestions(s.store)
}

func checkLanePolicyRefused(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := publishLanePolicy(s, 2, "ignore", authority.LaneNotify); !errors.Is(err, authority.ErrBadLanePolicy) {
		return Violatedf("an action nobody named read %v", err)
	}
	if err := publishLanePolicy(s, 2, authority.LaneNotify, ""); !errors.Is(err, authority.ErrBadLanePolicy) {
		return Violatedf("no action read %v", err)
	}
	return nil
}
