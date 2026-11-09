package contract

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// DecisionChecks hold rungs 3 and 4 inside a versioned policy, and an overturn as one
// transaction.
var DecisionChecks = []Check{
	{"D1", "an orchestrator neither decides nor assumes a one-way category", checkOneWayRefused},
	{"D2", "no second path lets an orchestrator decide a one-way category", checkOneWayNoBypass},
	{"D3", "a member decides a one-way category", checkMemberDecidesOneWay},
	{"D4", "rung 3 closes the question with a decision in force", checkRungDecide},
	{"D5", "rung 4 closes the question with an assumption", checkRungAssume},
	{"D6", "a rung needs the hold and the right rung", checkRungHold},
	{"D7", "a decision under the policy states what a requirement asks of it", checkBoundFields},
	{"D8", "a retry of a ruling finds its first attempt", checkRulingRetry},
	{"D9", "a work order holds no more assumptions than the policy allows", checkAssumptionLimit},
	{"D10", "overlapping scopes come back, and need a statement", checkOverlapStated},
	{"D11", "a superseded decision is replaced, with no rework", checkSuperseded},
	{"D12", "a member sees overlaps on record, and is not refused", checkMemberOverlap},
	{"D13", "a published policy is in force and on every later decision", checkPolicyPublished},
	{"D14", "a policy version is reviewable and stored by digest", checkPolicyDigest},
	{"D15", "only a member publishes a policy", checkPolicyActors},
	{"D16", "an overturn takes its five steps", checkOverturnSteps},
	{"D17", "an overturned decision stays readable", checkOverturnedReadable},
	{"D18", "only a member overturns", checkOverturnActors},
	{"D19", "a decision ends once and an outcome fills once", checkEndsOnce},
	{"D20", "a retried overturn finds what the first attempt wrote", checkOverturnRetry},
	{"D21", "racing overturns have one winner", checkOverturnRace},
	{"D22", "every decision has an experience record", checkExperienceRecorded},
	{"D23", "work not yet completed is told and not reworked", checkOverturnInFlight},
	{"D24", "the ledger and the domain agree on what overlaps", checkOverlapAgrees},
	{"D25", "a person's answer to a one-way question is one-way", checkAnswerOneWay},
}

// RungHolder is who OnRung climbs for.
const RungHolder = "holder"

const (
	checkoutScope = "checkout"
	policyBody    = `[decision_policy]
version = %d
reversible_default = "assume"
one_way_categories = [%s]
max_assumptions_per_work_order = %d
`
)

// publish makes a decision policy with these one-way categories and this limit.
func (r *Rig) publish(s ports.Store, version, limit int64, oneWay ...string) (authority.PolicyVersion, error) {
	return r.NewOps(s).PublishPolicy(r.Ctx, r.Scope, policyDraft(version, limit, oneWay...), r.Actor)
}

func policyDraft(version, limit int64, oneWay ...string) questions.PolicyDraft {
	quoted := make([]string, 0, len(oneWay))
	for _, category := range oneWay {
		quoted = append(quoted, `"`+category+`"`)
	}
	return questions.PolicyDraft{
		Body: fmt.Sprintf(policyBody, version, strings.Join(quoted, ", "), limit),
		Policy: questions.Policy{
			Version: version, ReversibleDefault: questions.ReversibleAssume, OneWayCategories: oneWay,
			MaxAssumptionsPerWorkOrder: limit,
		},
	}
}

// NextPolicy is a decision policy at a version, for a run that publishes one.
func NextPolicy(version int64) questions.PolicyDraft {
	return policyDraft(version, 5, questions.CategorySecurity)
}

// RulingOf is a ruling that meets everything the policy asks of one.
func RulingOf(id string, status questions.DecisionStatus) questions.Ruling {
	return ruling(id, status)
}

func ruling(id string, status questions.DecisionStatus) questions.Ruling {
	return questions.Ruling{
		ID: id, Status: status, Statement: "The retry budget is three calls.", Rationale: "It follows from the contract.",
		Citations: []string{"PAY-12"}, Scope: checkoutScope, Reversibility: questions.Reversible,
	}
}

// OnRung takes a question to a rung for one holder.
func (r *Rig) OnRung(s ports.Store, text string, rung int64, change func(*questions.Ask)) (string, ledger.Actor, error) {
	holder := r.orchestrator(RungHolder)
	asked, err := r.ask(s, text, r.ID("lane"), change)
	if err != nil {
		return "", holder, err
	}
	if err := r.climbed(s, asked.QuestionID, holder); err != nil {
		return "", holder, err
	}
	for step := questions.RungDecide; step < rung; step++ {
		if _, err := r.NewOps(s).Climb(r.Ctx, r.Scope, asked.QuestionID, questions.Step{Rung: step, Outcome: "passed"}, holder); err != nil {
			return "", holder, err
		}
	}
	return asked.QuestionID, holder, nil
}

// Assumed records an assumption, about a work order when one is named.
func (r *Rig) Assumed(s ports.Store, text, workOrder string) (questions.Decision, error) {
	id, holder, err := r.OnRung(s, text, questions.RungAssume, func(a *questions.Ask) {
		if workOrder != "" {
			a.SubjectKind, a.SubjectID = workorders.SubjectKind, workOrder
		}
	})
	if err != nil {
		return questions.Decision{}, err
	}
	draft := ruling(r.ID("d"), questions.DecisionAssumed)
	draft.Scope = "assumption/" + draft.ID
	return r.NewOps(s).Rule(r.Ctx, r.Scope, id, draft, holder)
}

func (r *Rig) readDecision(s ports.Store, id string) (questions.Decision, error) {
	var row questions.Decision
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Questions().Decision(id)
		return err
	})
	return row, err
}

func (r *Rig) absentDecision(s ports.Store, id string) error {
	if _, err := r.readDecision(s, id); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a refused decision reads %v", err)
	}
	return nil
}

func checkOneWayRefused(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator("holder")
	asked, err := r.ask(s, "May audit data leave the region?", "lane-a", func(a *questions.Ask) { a.Categories = []string{questions.CategorySecurity} })
	if err != nil {
		return err
	}
	if err := r.climbed(s, asked.QuestionID, holder); err != nil {
		return err
	}
	for _, status := range []questions.DecisionStatus{questions.DecisionDecided, questions.DecisionAssumed} {
		id := r.ID("d")
		if _, err := ops.Rule(r.Ctx, r.Scope, asked.QuestionID, ruling(id, status), holder); !errors.Is(err, questions.ErrOneWay) {
			return Violatedf("a %s ruling on a one-way category: %v", status, err)
		}
		if err := r.absentDecision(s, id); err != nil {
			return err
		}
	}
	// A policy that changes while a question stands on a rung is the policy that decides.
	id, holder, err := r.OnRung(s, "Which retry header do we send?", questions.RungDecide, nil)
	if err != nil {
		return err
	}
	if _, err := r.publish(s, 2, 5, questions.CategoryProduct); err != nil {
		return err
	}
	if _, err := ops.Rule(r.Ctx, r.Scope, id, ruling(r.ID("d"), questions.DecisionDecided), holder); !errors.Is(err, questions.ErrOneWay) {
		return Violatedf("a category the new policy forces to one-way: %v", err)
	}
	return r.CheckQuestions(s)
}

// checkOneWayNoBypass walks the ways around the rungs: a free-standing decision, a decision
// that names the question, one that calls itself one-way, one that replaces a person's
// decision, and an orchestrator's own decision offered as a rung 1 answer.
func checkOneWayNoBypass(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	orchestrator := r.orchestrator("holder")
	record := func(change func(*questions.Decision)) (string, error) {
		draft := questions.Decision{
			ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Allow it.", Rationale: "It follows.",
			Citations: []string{"PAY-12"}, Scope: r.ID("scope"), Reversibility: questions.Reversible,
			Categories: []string{questions.CategoryProduct},
		}
		change(&draft)
		_, err := ops.RecordDecision(r.Ctx, r.Scope, draft, orchestrator)
		return draft.ID, err
	}
	asked, err := r.ask(s, "May audit data leave the region?", "lane-a", func(a *questions.Ask) { a.Categories = []string{questions.CategorySecurity} })
	if err != nil {
		return err
	}
	human, err := r.decision(s, "Audit data stays in the region.", func(d *questions.Decision) {
		d.Categories, d.Scope = []string{questions.CategorySecurity}, "audit"
	})
	if err != nil {
		return err
	}
	cases := []struct {
		name   string
		change func(*questions.Decision)
	}{
		{"a one-way category", func(d *questions.Decision) { d.Categories = []string{questions.CategoryData} }},
		{"one among others", func(d *questions.Decision) {
			d.Categories = []string{questions.CategoryProduct, questions.CategoryCompliance}
		}},
		{"the question's category", func(d *questions.Decision) { d.QuestionID = asked.QuestionID }},
		{"a one-way choice", func(d *questions.Decision) { d.Reversibility = questions.OneWay }},
		{"replacing a one-way decision", func(d *questions.Decision) { d.Supersedes = human.ID }},
	}
	for _, c := range cases {
		id, err := record(c.change)
		if !errors.Is(err, questions.ErrOneWay) {
			return Violatedf("%s: %v", c.name, err)
		}
		if err := r.absentDecision(s, id); err != nil {
			return err
		}
	}
	own, err := record(func(*questions.Decision) {})
	if err != nil {
		return err
	}
	if err := r.climbed(s, asked.QuestionID, orchestrator); err != nil {
		return err
	}
	if _, err := ops.Resolve(r.Ctx, r.Scope, asked.QuestionID, own, orchestrator); !errors.Is(err, questions.ErrOneWay) {
		return Violatedf("an orchestrator answered a one-way question with its own decision: %v", err)
	}
	if _, err := ops.Resolve(r.Ctx, r.Scope, asked.QuestionID, human.ID, orchestrator); err != nil {
		return Violatedf("an earlier human decision must still answer it: %v", err)
	}
	return r.CheckQuestions(s)
}

func checkMemberDecidesOneWay(r *Rig) error {
	s := r.Store()
	if _, err := r.decision(s, "Audit data stays in the region.", func(d *questions.Decision) {
		d.Categories, d.Reversibility = []string{questions.CategorySecurity}, questions.OneWay
	}); err != nil {
		return Violatedf("a member decided nothing: %v", err)
	}
	return nil
}

func checkRungDecide(r *Rig) error {
	s := r.Store()
	id, holder, err := r.OnRung(s, "What is the retry budget?", questions.RungDecide, nil)
	if err != nil {
		return err
	}
	draft := ruling(r.ID("d"), questions.DecisionDecided)
	decided, err := r.NewOps(s).Rule(r.Ctx, r.Scope, id, draft, holder)
	if err != nil {
		return err
	}
	if decided.Status != questions.DecisionDecided || decided.DecidedBy != holder.ID || decided.QuestionID != id ||
		decided.PolicyVersion != questions.DefaultPolicy().Version || !slices.Equal(decided.Citations, draft.Citations) ||
		decided.Reversibility != questions.Reversible || decided.Rationale == "" || decided.Scope != checkoutScope {
		return Violatedf("decision %+v", decided)
	}
	row, err := r.readQuestion(s, id)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusAnswered || row.DecisionID != decided.ID || row.ClaimedBy != "" ||
		!row.Ran(questions.RungDecide) {
		return Violatedf("question after a ruling %+v", row)
	}
	again, err := r.ask(s, "What is the retry budget?", "lane-z", nil)
	if err != nil {
		return err
	}
	if again.Status != questions.AskAnswered || again.Answer != draft.Statement {
		return Violatedf("the next asker reads %+v", again)
	}
	return r.CheckQuestions(s)
}

func checkRungAssume(r *Rig) error {
	s := r.Store()
	decision, err := r.Assumed(s, "Which log level do retries use?", "")
	if err != nil {
		return err
	}
	if decision.Status != questions.DecisionAssumed {
		return Violatedf("assumption %+v", decision)
	}
	row, err := r.readQuestion(s, decision.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusAnswered || row.DecisionID != decision.ID {
		return Violatedf("question after an assumption %+v", row)
	}
	return r.CheckQuestions(s)
}

func checkRungHold(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, holder, err := r.OnRung(s, "What is the retry budget?", questions.RungDecide, nil)
	if err != nil {
		return err
	}
	if _, err := ops.Rule(r.Ctx, r.Scope, id, ruling(r.ID("d"), questions.DecisionDecided), r.orchestrator("other")); !errors.Is(err, questions.ErrNotHeld) {
		return Violatedf("a ruling from no holder: %v", err)
	}
	if _, err := ops.Rule(r.Ctx, r.Scope, id, ruling(r.ID("d"), questions.DecisionAssumed), holder); !errors.Is(err, questions.ErrWrongRung) {
		return Violatedf("an assumption on rung 3: %v", err)
	}
	if _, err := ops.Rule(r.Ctx, r.Scope, id, ruling(r.ID("d"), questions.DecisionOverturned), holder); !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("a ruling of a status no ruling has: %v", err)
	}
	if _, err := ops.Rule(r.Ctx, r.Scope, id, ruling(r.ID("d"), questions.DecisionDecided), r.laneActor("lane-a")); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a lane's ruling: %v", err)
	}
	return nil
}

func checkBoundFields(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	cases := []struct {
		name   string
		status questions.DecisionStatus
		change func(*questions.Ruling)
		want   error
	}{
		{"no rationale", questions.DecisionDecided, func(d *questions.Ruling) { d.Rationale = "" }, questions.ErrInvalid},
		{"no citation", questions.DecisionDecided, func(d *questions.Ruling) { d.Citations = nil }, questions.ErrInvalid},
		{"no scope", questions.DecisionDecided, func(d *questions.Ruling) { d.Scope = "" }, questions.ErrInvalid},
		{"no reversibility", questions.DecisionDecided, func(d *questions.Ruling) { d.Reversibility = "" }, questions.ErrInvalid},
		{"a one-way choice", questions.DecisionDecided, func(d *questions.Ruling) { d.Reversibility = questions.OneWay }, questions.ErrOneWay},
		{"an assumption with no rationale", questions.DecisionAssumed, func(d *questions.Ruling) { d.Rationale = "" }, questions.ErrInvalid},
		{"a one-way assumption", questions.DecisionAssumed, func(d *questions.Ruling) { d.Reversibility = questions.OneWay }, questions.ErrOneWay},
	}
	for _, c := range cases {
		rung := questions.RungDecide
		if c.status == questions.DecisionAssumed {
			rung = questions.RungAssume
		}
		id, holder, err := r.OnRung(s, "Question "+c.name+"?", rung, nil)
		if err != nil {
			return err
		}
		draft := ruling(r.ID("d"), c.status)
		c.change(&draft)
		if _, err := ops.Rule(r.Ctx, r.Scope, id, draft, holder); !errors.Is(err, c.want) {
			return Violatedf("%s: %v", c.name, err)
		}
		if err := r.absentDecision(s, draft.ID); err != nil {
			return err
		}
	}
	// A citation is not required of an assumption, which rests on no answer by definition.
	id, holder, err := r.OnRung(s, "Which log level do retries use?", questions.RungAssume, nil)
	if err != nil {
		return err
	}
	draft := ruling(r.ID("d"), questions.DecisionAssumed)
	draft.Citations = nil
	if _, err := ops.Rule(r.Ctx, r.Scope, id, draft, holder); err != nil {
		return Violatedf("an assumption with no citation: %v", err)
	}
	return nil
}

func checkRulingRetry(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, holder, err := r.OnRung(s, "What is the retry budget?", questions.RungDecide, nil)
	if err != nil {
		return err
	}
	draft := ruling(r.ID("d"), questions.DecisionDecided)
	first, err := ops.Rule(r.Ctx, r.Scope, id, draft, holder)
	if err != nil {
		return err
	}
	before, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	second, err := ops.Rule(r.Ctx, r.Scope, id, draft, holder)
	if err != nil {
		return err
	}
	after, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	if second.ID != first.ID || len(after) != len(before) {
		return Violatedf("a retry returned %+v and wrote %d events", second, len(after)-len(before))
	}
	return nil
}

func checkAssumptionLimit(r *Rig) error {
	s := r.Store()
	if _, err := r.publish(s, 2, 1, questions.CategorySecurity); err != nil {
		return err
	}
	order, err := r.Ready(s)
	if err != nil {
		return err
	}
	first, err := r.Assumed(s, "Which log level do retries use?", order)
	if err != nil {
		return err
	}
	if _, err := r.Assumed(s, "Which metric do retries emit?", order); !errors.Is(err, questions.ErrAssumptionLimit) {
		return Violatedf("an assumption past the limit: %v", err)
	}
	if _, err := r.Assumed(s, "Which trace do retries emit?", ""); err != nil {
		return Violatedf("a question about no work order counts toward none: %v", err)
	}
	if _, err := r.overturn(s, first.ID); err != nil {
		return err
	}
	if _, err := r.Assumed(s, "Which event do retries emit?", order); err != nil {
		return Violatedf("an overturned assumption still counts: %v", err)
	}
	return nil
}

func checkOverlapStated(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	broad, err := r.decision(s, "Payments retry.", func(d *questions.Decision) { d.Scope = "payments" })
	if err != nil {
		return err
	}
	narrow, err := r.decision(s, "Refunds retry once.", func(d *questions.Decision) { d.Scope = "payments/refunds" })
	if err != nil {
		return err
	}
	if _, err := r.decision(s, "Paymentsplus is apart.", func(d *questions.Decision) { d.Scope = "paymentsplus" }); err != nil {
		return err
	}
	id, holder, err := r.OnRung(s, "How many refund retries?", questions.RungDecide, nil)
	if err != nil {
		return err
	}
	draft := ruling(r.ID("d"), questions.DecisionDecided)
	draft.Scope = "payments/refunds/partial"
	_, err = ops.Rule(r.Ctx, r.Scope, id, draft, holder)
	var overlap *questions.OverlapError
	if !errors.As(err, &overlap) || !slices.Equal(overlap.Overlapping, sorted(broad.ID, narrow.ID)) {
		return Violatedf("an unstated overlap: %v", err)
	}
	draft.NoConflict = []string{broad.ID}
	if _, err = ops.Rule(r.Ctx, r.Scope, id, draft, holder); !errors.As(err, &overlap) || !slices.Equal(overlap.Overlapping, []string{narrow.ID}) {
		return Violatedf("half stated: %v", err)
	}
	if err := r.absentDecision(s, draft.ID); err != nil {
		return err
	}
	draft.NoConflict = []string{broad.ID, narrow.ID}
	decided, err := ops.Rule(r.Ctx, r.Scope, id, draft, holder)
	if err != nil {
		return err
	}
	read, err := r.readDecision(s, decided.ID)
	if err != nil {
		return err
	}
	if !slices.Equal(read.Overlaps, sorted(broad.ID, narrow.ID)) || !slices.Equal(read.NoConflict, draft.NoConflict) {
		return Violatedf("overlaps on record: %+v", read)
	}
	for _, id := range []string{broad.ID, narrow.ID} {
		kept, err := r.readDecision(s, id)
		if err != nil {
			return err
		}
		if kept.Status != questions.DecisionDecided {
			return Violatedf("a decision that does not conflict became %s", kept.Status)
		}
	}
	return r.CheckQuestions(s)
}

func sorted(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

func checkSuperseded(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	old, err := r.Assumed(s, "Which log level do retries use?", "")
	if err != nil {
		return err
	}
	if _, err := r.ask(s, "Which log level do retries use today?", "lane-b", nil); err != nil {
		return err
	}
	draft := questions.Decision{
		ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Retries log at info.", Rationale: "It follows.",
		Citations: []string{"PAY-12"}, Scope: old.Scope, Reversibility: questions.Reversible, Supersedes: old.ID,
	}
	holder := r.orchestrator("holder")
	if _, err := ops.RecordDecision(r.Ctx, r.Scope, draft, holder); err != nil {
		return err
	}
	read, err := r.readDecision(s, old.ID)
	if err != nil {
		return err
	}
	if read.Status != questions.DecisionReplaced {
		return Violatedf("a superseded decision is %s", read.Status)
	}
	draft.ID, draft.Supersedes = r.ID("d"), old.ID
	if _, err := ops.RecordDecision(r.Ctx, r.Scope, draft, holder); !errors.Is(err, questions.ErrDecisionEnded) {
		return Violatedf("superseding a replaced decision: %v", err)
	}
	return r.CheckQuestions(s)
}

func checkMemberOverlap(r *Rig) error {
	s := r.Store()
	first, err := r.decision(s, "Retry twice.", nil)
	if err != nil {
		return err
	}
	second, err := r.decision(s, "Retry on timeouts.", nil)
	if err != nil {
		return err
	}
	if !slices.Equal(second.Overlaps, []string{first.ID}) {
		return Violatedf("a member's decision came back with %v", second.Overlaps)
	}
	return nil
}

func checkPolicyPublished(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	before, err := r.decision(s, "Retry twice.", func(d *questions.Decision) { d.Scope = "first" })
	if err != nil {
		return err
	}
	if before.PolicyVersion != questions.DefaultPolicy().Version {
		return Violatedf("before any publish a decision records version %d", before.PolicyVersion)
	}
	published, err := r.publish(s, 7, 5, questions.CategorySecurity, "pricing-model")
	if err == nil || !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("a policy naming an unknown category: %v", err)
	}
	if err := ops.AddCategory(r.Ctx, r.Scope, "pricing-model", r.Actor); err != nil {
		return err
	}
	if published, err = r.publish(s, 7, 5, questions.CategorySecurity, "pricing-model"); err != nil {
		return err
	}
	if published.Version != 7 || published.AuthoredBy != r.Actor.ID || published.Kind != authority.PolicyDecision {
		return Violatedf("published %+v", published)
	}
	after, err := r.decision(s, "Retry on timeouts.", func(d *questions.Decision) { d.Scope = "second" })
	if err != nil {
		return err
	}
	if after.PolicyVersion != 7 {
		return Violatedf("a later decision records version %d", after.PolicyVersion)
	}
	kept, err := r.readDecision(s, before.ID)
	if err != nil {
		return err
	}
	if kept.PolicyVersion != questions.DefaultPolicy().Version {
		return Violatedf("an earlier decision moved to version %d", kept.PolicyVersion)
	}
	if _, err := r.publish(s, 7, 4, questions.CategorySecurity); !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("a different body at the same version: %v", err)
	}
	if _, err := r.publish(s, 3, 5, questions.CategorySecurity); !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("an older version: %v", err)
	}
	// A body names its version, so publishing the same body again is a retry.
	revs, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	again, err := r.publish(s, 7, 5, questions.CategorySecurity, "pricing-model")
	if err != nil {
		return err
	}
	revsAfter, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	if again.ID != published.ID || len(revsAfter) != len(revs) {
		return Violatedf("a retried publish returned %+v and wrote %d events", again, len(revsAfter)-len(revs))
	}
	return r.CheckQuestions(s)
}

func checkPolicyDigest(r *Rig) error {
	s := r.Store()
	draft := policyDraft(2, 5, questions.CategorySecurity)
	published, err := r.NewOps(s).PublishPolicy(r.Ctx, r.Scope, draft, r.Actor)
	if err != nil {
		return err
	}
	if published.Digest != authority.PolicyDigest(draft.Body) || published.Body != draft.Body {
		return Violatedf("published %+v", published)
	}
	versions, err := r.NewOps(s).PolicyVersions(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		return Violatedf("versions %+v", versions)
	}
	err = s.Write(r.Ctx, r.Scope, "policy.repeat", func(tx ports.WriteTx) error {
		added, err := tx.Policies().Insert(published)
		if err != nil {
			return err
		}
		if added {
			return Violatedf("a version was written twice")
		}
		_, err = tx.Append(ledger.EventDraft{Type: "note.added", Actor: r.Actor, Subject: ledger.Subject{Kind: "note", ID: "note"}})
		return err
	})
	if err != nil {
		return err
	}
	return r.damagedPolicy(s)
}

// damagedPolicy changes a body after its review. Every later decision must stop.
func (r *Rig) damagedPolicy(s ports.Store) error {
	if err := r.DB.Damage(r.T, "UPDATE policy_versions SET body = 'one_way_categories = []' WHERE version = 2"); err != nil {
		return err
	}
	_, err := r.decision(s, "Retry twice.", nil)
	if !errors.Is(err, authority.ErrPolicyDigest) {
		return Violatedf("a decision under a changed body: %v", err)
	}
	return nil
}

func checkPolicyActors(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	for _, actor := range []ledger.Actor{r.orchestrator("holder"), r.laneActor("lane-a"), r.worker("a")} {
		if _, err := ops.PublishPolicy(r.Ctx, r.Scope, policyDraft(2, 5), actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s published a policy: %v", actor.Kind, err)
		}
	}
	return nil
}

// overturn reverses a decision as a member.
func (r *Rig) overturn(s ports.Store, id string) (questions.Overturned, error) {
	return r.NewOps(s).Overturn(r.Ctx, r.Scope, id, questions.Overturn{
		DecisionID: r.ID("d"), Statement: "Retries log at warn.", Rationale: "A person read the logs.", Citations: []string{"PAY-12"},
	}, r.Actor)
}

// Completed returns a work order that a lane finished, in `done` when accepted is true.
func (r *Rig) Completed(s ports.Store, accepted bool) (string, error) {
	id, err := r.Ready(s)
	if err != nil {
		return "", err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return "", err
	}
	ops := r.NewOps(s)
	if err := ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a")); err != nil {
		return "", err
	}
	if !accepted {
		return id, nil
	}
	_, err = ops.Accept(r.Ctx, r.Scope, id, r.Actor)
	return id, err
}

func checkOverturnSteps(r *Rig) error {
	s := r.Store()
	done, err := r.Completed(s, true)
	if err != nil {
		return err
	}
	waiting, err := r.Completed(s, false)
	if err != nil {
		return err
	}
	old, err := r.Assumed(s, "Which log level do retries use?", done)
	if err != nil {
		return err
	}
	lane := r.ID("lane")
	if _, err := r.ask(s, "Which log level do retries use?", lane, nil); err != nil {
		return err
	}
	joined, err := r.ask(s, "Which log level do retries use now?", lane, func(a *questions.Ask) { a.SubjectKind, a.SubjectID = workorders.SubjectKind, waiting })
	if err != nil {
		return err
	}
	// The second work order rests on the same assumption through its own question.
	if err := r.resolvedBy(s, joined.QuestionID, old.ID); err != nil {
		return err
	}
	result, err := r.overturn(s, old.ID)
	if err != nil {
		return err
	}
	if result.Old.Status != questions.DecisionOverturned {
		return Violatedf("step 1: the old decision is %s", result.Old.Status)
	}
	if result.New.Supersedes != old.ID || result.New.DecidedBy != r.Actor.ID || result.New.Status != questions.DecisionDecided {
		return Violatedf("step 2: the new decision %+v", result.New)
	}
	if len(result.Rework) != 2 {
		return Violatedf("step 3: rework %v", result.Rework)
	}
	reworkOf := []string{}
	for _, id := range result.Rework {
		rework, err := r.WorkOrder(s, id)
		if err != nil {
			return err
		}
		if rework.Kind != workorders.KindRework || rework.State != workorders.ContractReview || rework.Title == "" {
			return Violatedf("step 3: rework %+v", rework)
		}
		reworkOf = append(reworkOf, rework.ReworkOf)
	}
	slices.Sort(reworkOf)
	if !slices.Equal(reworkOf, sorted(done, waiting)) {
		return Violatedf("step 3: rework of %v", reworkOf)
	}
	if !slices.Contains(result.Noticed, r.attachedLane(s, old.QuestionID)) {
		return Violatedf("step 4: noticed %v", result.Noticed)
	}
	var experience measures.Experience
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		experience, err = tx.Measures().Experience(measures.SubjectDecision, old.ID)
		return err
	})
	if err != nil {
		return err
	}
	if experience.Outcome == nil || experience.Outcome.Status != measures.OutcomeOverturned ||
		!slices.Equal(experience.Outcome.ReworkWorkOrders, result.Rework) || experience.OutcomeObservedAt == 0 {
		return Violatedf("step 5: experience %+v", experience)
	}
	again, err := r.ask(s, "Which log level do retries use?", "lane-z", nil)
	if err != nil {
		return err
	}
	if again.Status != questions.AskAnswered || again.DecisionID != result.New.ID {
		return Violatedf("the next asker reads %+v", again)
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	return r.CheckQuestions(s)
}

func (r *Rig) attachedLane(s ports.Store, question string) string {
	lane := ""
	_ = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		attached, err := tx.Questions().Attachments(question)
		if err == nil && len(attached) != 0 {
			lane = attached[0].Lane
		}
		return err
	})
	return lane
}

// resolvedBy closes a question from a decision the ledger holds, as rung 1 does.
func (r *Rig) resolvedBy(s ports.Store, question, decision string) error {
	ops := r.NewOps(s)
	holder := r.orchestrator("resolver")
	if _, err := ops.Take(r.Ctx, r.Scope, question, TTLMs, holder); err != nil {
		return err
	}
	_, err := ops.Resolve(r.Ctx, r.Scope, question, decision, holder)
	return err
}

func checkOverturnedReadable(r *Rig) error {
	s := r.Store()
	old, err := r.Assumed(s, "Which log level do retries use?", "")
	if err != nil {
		return err
	}
	result, err := r.overturn(s, old.ID)
	if err != nil {
		return err
	}
	read, err := r.readDecision(s, old.ID)
	if err != nil {
		return err
	}
	if read.Status != questions.DecisionOverturned || read.Statement != old.Statement || read.Rationale != old.Rationale ||
		read.DecidedBy != old.DecidedBy || read.PolicyVersion != old.PolicyVersion {
		return Violatedf("the overturned decision reads %+v", read)
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Questions().Decisions(ports.RecordFilter{Status: string(questions.DecisionOverturned)})
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].ID != old.ID {
			return Violatedf("the overturned list holds %+v", rows)
		}
		successor, err := tx.Questions().Decision(result.New.ID)
		if err != nil {
			return err
		}
		if successor.Supersedes != old.ID {
			return Violatedf("nothing leads from the new decision to the old: %+v", successor)
		}
		return nil
	})
}

func checkOverturnActors(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	old, err := r.Assumed(s, "Which log level do retries use?", "")
	if err != nil {
		return err
	}
	for _, actor := range []ledger.Actor{r.orchestrator("holder"), r.laneActor("lane-a")} {
		_, err := ops.Overturn(r.Ctx, r.Scope, old.ID, questions.Overturn{DecisionID: r.ID("d"), Statement: "No."}, actor)
		if !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s overturned a decision: %v", actor.Kind, err)
		}
	}
	read, err := r.readDecision(s, old.ID)
	if err != nil {
		return err
	}
	if read.Status != questions.DecisionAssumed {
		return Violatedf("a refused overturn left %s", read.Status)
	}
	return nil
}

// checkEndsOnce calls both guarded statements a second time directly, because a race does
// not reliably catch a removed compare-and-set.
func checkEndsOnce(r *Rig) error {
	s := r.Store()
	old, err := r.Assumed(s, "Which log level do retries use?", "")
	if err != nil {
		return err
	}
	if _, err := r.overturn(s, old.ID); err != nil {
		return err
	}
	return s.Write(r.Ctx, r.Scope, "decision.repeat", func(tx ports.WriteTx) error {
		ended, err := tx.Questions().EndDecision(old.ID, questions.DecisionOverturned)
		if err != nil {
			return err
		}
		if ended {
			return Violatedf("a decision ended twice")
		}
		filled, err := tx.Measures().FillOutcome(measures.SubjectDecision, old.ID, measures.Outcome{Status: "later"}, tx.Now())
		if err != nil {
			return err
		}
		if filled {
			return Violatedf("an outcome filled twice")
		}
		_, err = tx.Append(ledger.EventDraft{Type: "note.added", Actor: r.Actor, Subject: ledger.Subject{Kind: "note", ID: "note"}})
		return err
	})
}

func checkOverturnRetry(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	done, err := r.Completed(s, true)
	if err != nil {
		return err
	}
	old, err := r.Assumed(s, "Which log level do retries use?", done)
	if err != nil {
		return err
	}
	overturn := questions.Overturn{DecisionID: r.ID("d"), Statement: "Retries log at warn."}
	first, err := ops.Overturn(r.Ctx, r.Scope, old.ID, overturn, r.Actor)
	if err != nil {
		return err
	}
	second, err := ops.Overturn(r.Ctx, r.Scope, old.ID, overturn, r.Actor)
	if err != nil {
		return err
	}
	if second.New.ID != first.New.ID || second.Old.Status != questions.DecisionOverturned ||
		!slices.Equal(second.Rework, first.Rework) || !slices.Equal(second.Noticed, first.Noticed) {
		return Violatedf("a retry returned %+v, the first %+v", second, first)
	}
	if _, err := r.overturn(s, old.ID); !errors.Is(err, questions.ErrDecisionEnded) {
		return Violatedf("a second overturn: %v", err)
	}
	return r.CheckQuestions(s)
}

func checkOverturnRace(r *Rig) error {
	s := r.Store()
	done, err := r.Completed(s, true)
	if err != nil {
		return err
	}
	old, err := r.Assumed(s, "Which log level do retries use?", done)
	if err != nil {
		return err
	}
	const racers = 6
	results := make([]error, racers)
	var group sync.WaitGroup
	for i := range racers {
		group.Go(func() {
			_, results[i] = r.overturn(s, old.ID)
		})
	}
	group.Wait()
	won := 0
	for _, err := range results {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, questions.ErrDecisionEnded):
			return err
		}
	}
	if won != 1 {
		return Violatedf("%d overturns won", won)
	}
	return r.CheckQuestions(s)
}

func checkExperienceRecorded(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.escalated(s, "May we retry on a 500?", func(a *questions.Ask) { a.Blocking = false })
	if err != nil {
		return err
	}
	answer, err := ops.Answer(r.Ctx, r.Scope, id, questions.Answer{DecisionID: r.ID("d"), Statement: "Keep"}, r.Actor)
	if err != nil {
		return err
	}
	if answer.PolicyVersion != questions.DefaultPolicy().Version {
		return Violatedf("an answer records version %d", answer.PolicyVersion)
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		record, err := tx.Measures().Experience(measures.SubjectDecision, answer.ID)
		if err != nil {
			return err
		}
		if record.Chosen != "Keep" || record.PolicyVersion != answer.PolicyVersion || record.Outcome != nil ||
			!slices.Equal(record.OptionsConsidered, []string{"Keep", "Change"}) {
			return Violatedf("experience %+v", record)
		}
		return nil
	})
}

func checkOverturnInFlight(r *Rig) error {
	s := r.Store()
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	old, err := r.Assumed(s, "Which log level do retries use?", id)
	if err != nil {
		return err
	}
	result, err := r.overturn(s, old.ID)
	if err != nil {
		return err
	}
	if len(result.Rework) != 0 {
		return Violatedf("running work made rework %v", result.Rework)
	}
	if !slices.Contains(result.Noticed, holder.Lane) {
		return Violatedf("the running lane was not told: %v", result.Noticed)
	}
	return r.CheckQuestions(s)
}

// checkOverlapAgrees holds the statement to the pure rule, because a lane's work is matched
// by one and a decision's overlaps by the other. The marks a LIKE pattern reads mean nothing.
func checkOverlapAgrees(r *Rig) error {
	s := r.Store()
	recorded := []string{"pay", "pay/refunds", "pay%", "p_y", "payments"}
	ids := map[string]string{}
	for _, scope := range recorded {
		decided, err := r.decision(s, "Scope "+scope, func(d *questions.Decision) { d.Scope = scope })
		if err != nil {
			return err
		}
		ids[decided.ID] = scope
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		for _, probe := range []string{"pay", "pay/refunds/partial", "pay%", "p_y/x", "pa", "payments/x", "", "other"} {
			rows, err := tx.Questions().Overlapping(probe)
			if err != nil {
				return err
			}
			found := map[string]bool{}
			for _, row := range rows {
				found[row.Scope] = true
			}
			for _, scope := range recorded {
				if found[scope] != questions.ScopesOverlap(scope, probe) {
					return Violatedf("scope %q against %q: the ledger says %v", scope, probe, found[scope])
				}
			}
		}
		return nil
	})
}

func checkAnswerOneWay(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, "May audit data leave the region?", "lane-a", func(a *questions.Ask) { a.Categories = []string{questions.CategorySecurity} })
	if err != nil {
		return err
	}
	answer, err := r.NewOps(s).Answer(r.Ctx, r.Scope, asked.QuestionID, questions.Answer{DecisionID: r.ID("d"), Statement: "It stays."}, r.Actor)
	if err != nil {
		return err
	}
	if answer.Reversibility != questions.OneWay {
		return Violatedf("a person's one-way answer is %q", answer.Reversibility)
	}
	return nil
}
