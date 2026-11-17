//go:build chaos

package chaos

import (
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/contract"
)

// checkClaimsPinned holds every claim to a pack that exists, whichever side of a kill it stands on.
func checkClaimsPinned(w *world) error {
	return contract.ClaimsPinned(w.rig, w.store)
}

// oneWayCase kills a one-way decision on its way to the lanes it covers. It must leave nothing, or
// the decision and everything it told.
func oneWayCase() crashCase {
	return crashCase{
		name:  "a one-way decision",
		op:    "decision.record",
		steps: []string{"held", "noticed", "parked", "reworked", "revised", "bound"},
		prepare: func(w *world) (childSpec, error) {
			key := w.rig.ID("d")
			return childSpec{Role: "one-way", Key: key}, w.rig.HoldRepository(w.store, key)
		},
		settled: func(w *world, spec childSpec) error {
			return countEvents(w, questions.EventDecisionRecorded, spec.Key, 1)
		},
	}
}

// packVariants are the broken packs the pack checks must catch.
func packVariants() []variant {
	return []variant{
		{name: "a parked lane that stays asked to park", control: control.Sabotage{KeepsParkWanted: true}, check: "N15"},
		{name: "a notice that does not read the index", control: control.Sabotage{IgnoresPins: true}, check: "N3"},
		{name: "a plan scope that binds no work", control: control.Sabotage{BindsNoPlan: true}, check: "MP10"},
		{name: "a decision recorded with any scope at all", control: control.Sabotage{AcceptsAnyScope: true}, check: "BD11"},
		{name: "a wake on a decision of any key at all", control: control.Sabotage{AcceptsAnyScope: true}, check: "BD12"},
		{name: "a plan scope that binds no work to overturn", control: control.Sabotage{BindsNoPlan: true}, check: "MP13"},
		{name: "ready work that stays ready under a decision", control: control.Sabotage{KeepsReadyWork: true}, check: "MP11"},
		{name: "ready work that stays ready under a person's decision", control: control.Sabotage{KeepsReadyWork: true}, check: "BD1"},
		{name: "work returned to review that nobody holds there", control: control.Sabotage{HoldsNothing: true}, check: "BD1"},
		{name: "work held for another reason that no decision holds", control: control.Sabotage{KeepsStandingHold: true}, check: "BD16"},
		{name: "work returned to review that nobody holds, for the plan's scenario", control: control.Sabotage{HoldsNothing: true}, check: "MP11"},
		{name: "work held in review for a decision that can be taken back", control: control.Sabotage{HoldsReversible: true}, check: "BD2"},
		{name: "a contract scope that binds every contract", control: control.Sabotage{BindsEveryContract: true}, check: "BD3"},
		{name: "a decision that tells no lane of a reversible decision", control: control.Sabotage{TellsNobody: true}, check: "N8"},
		{name: "a decision that tells no lane of the plan", control: control.Sabotage{TellsNobody: true}, check: "BD4"},
		{name: "a lane whose pack a decision does not renew", control: control.Sabotage{RenewsNoPack: true}, check: "BD4"},
		{name: "a lane that parks for no policy", control: control.Sabotage{ParksNothing: true}, check: "BD5"},
		{name: "a lane that parks for a reversible decision", control: control.Sabotage{ParksAnyDecision: true}, check: "N8"},
		{name: "a lane that parks for a reversible decision under on_one_way_decision", control: control.Sabotage{ParksAnyDecision: true}, check: "BD5"},
		{name: "finished work a one-way decision does not rework", control: control.Sabotage{ReworksNothing: true}, check: "BD6"},
		{name: "finished work a one-way decision of the batch does not rework", control: control.Sabotage{ReworksNothing: true}, check: "BD8"},
		{name: "finished work a reversible decision reworks", control: control.Sabotage{ReworksReversible: true}, check: "BD7"},
		{name: "a lane parked by the answer to its own question", control: control.Sabotage{ParksAskingLanes: true}, check: "BD15"},
		{name: "a rework beside the rework a lane runs", control: control.Sabotage{BranchesTheChain: true}, check: "BD17"},
		{name: "a rework beside the rework of a parked lane", control: control.Sabotage{BranchesTheChain: true}, check: "BD18"},
		{name: "a rework beside the rework of a lapsed lane", control: control.Sabotage{BranchesTheChain: true}, check: "BD19"},
		{name: "an overturn that starts a rework beside the rework a lane runs", control: control.Sabotage{BranchesTheChain: true}, check: "BD21"},
		{name: "a lane that asked, left running by the overturn of its answer", control: control.Sabotage{SparesAskersOnOverturn: true}, check: "BD20"},
		{name: "a rework of every order of a chain of rework", control: control.Sabotage{ReworksWholeChain: true}, check: "BD14"},
		{name: "a rework for each decision of a batch", control: control.Sabotage{ReworksEachDecision: true}, check: "BD8"},
		{name: "a contract review a decision moves and reworks", control: control.Sabotage{ReworksAnyKind: true}, check: "MP15"},
		{name: "a contract review's lane that nothing tells", control: control.Sabotage{TellsNobody: true}, check: "BD9"},
		{name: "a plan no decision of a member marks as needing revision", control: control.Sabotage{MarksNoRevision: true}, check: "PRV1"},
		{name: "a plan no answer of a batch marks as needing revision", control: control.Sabotage{MarksNoRevision: true}, check: "MP12"},
		{name: "a plan no answer of a batch marks, for the batch's one revision", control: control.Sabotage{MarksNoRevision: true}, check: "PRV11"},
		{name: "a revision that lists no later decision", control: control.Sabotage{ExtendsNothing: true}, check: "PRV1"},
		{name: "a revision an orchestrator's decision opens", control: control.Sabotage{MarksAnyActor: true}, check: "PRV2"},
		{name: "a revision a member's overturn does not open", control: control.Sabotage{MarksNoOverturn: true}, check: "PRV8"},
		{name: "a revision that lists no finished work", control: control.Sabotage{ListsNoFinishedWork: true}, check: "PRV3"},
		{name: "a revision that lists the work a one-way decision reworked", control: control.Sabotage{ListsOneWayFinishedWork: true}, check: "PRV3"},
		{name: "a revision that lists no finished work to rework", control: control.Sabotage{ListsNoFinishedWork: true}, check: "PRV10"},
		{name: "work that passes review while its plan's revision is open", control: control.Sabotage{WaitsOnNoRevision: true}, check: "PRV4"},
		{name: "work of a plan that no decision of its revision binds, which waits for it", control: control.Sabotage{WaitsPlanWide: true}, check: "PRV12"},
		{name: "work that waits on a revision after Confirm", control: control.Sabotage{KeepsWaitingWork: true}, check: "PRV4"},
		{name: "work a one-way decision held, which Confirm leaves held", control: control.Sabotage{KeepsDecisionHolds: true}, check: "PRV5"},
		{name: "a revision any actor confirms", control: control.Sabotage{ConfirmsAnyActor: true}, check: "PRV6"},
		{name: "a revision that stays open when a later version is approved", control: control.Sabotage{KeepsRevisionOnApproval: true}, check: "PRV7"},
		{name: "a revision that stays open when a branch is approved", control: control.Sabotage{KeepsRevisionOnApproval: true}, check: "PRV13"},
		{name: "a revision that an approval closes for decisions recorded after it", control: control.Sabotage{ClosesLaterDecisions: true}, check: "PRV14"},
		{name: "a revision an approval rewrites for the decisions after it, leaving the approval none", control: control.Sabotage{NarrowsRevisionInPlace: true}, check: "PRV14"},
		{name: "work a later decision binds, released when a branch's approval is recorded", control: control.Sabotage{ReleasesBoundWork: true}, check: "PRV17"},
		{name: "a revision closed for a decision that no approval showed, when none was", control: control.Sabotage{ClosesLaterDecisions: true}, check: "PRV18"},
		{name: "a member's assumption that opens no revision", control: control.Sabotage{MarksNoAssumption: true}, check: "PRV15"},
		{name: "rework for a decision of finished work its revision does not list", control: control.Sabotage{ReworksUnlistedWork: true}, check: "PRV16"},
		{name: "a revising session that returns work of another kind", control: control.Sabotage{ReusesAnyWorkOrder: true}, check: "PRV9"},
		{name: "a revising session whose brief lists no decision", control: control.Sabotage{BriefsNoDecisions: true}, check: "PRV9"},
		{name: "a rework for each decision a member names", control: control.Sabotage{ReworksEachDecision: true}, check: "PRV10"},
		{
			name:     "a plan whose closed revision still reads as open",
			sabotage: sqlstore.Sabotage{Rewrite: replace("plan_revision.open", " AND closed_at IS NULL", "")},
			check:    "PRV4",
		},
		{
			name: "an end notice a lane's earlier notice swallows",
			sabotage: sqlstore.Sabotage{Rewrite: replace("decision_notice.insert",
				"DO UPDATE\n   SET generation = excluded.generation, replaced_by = excluded.replaced_by, noticed_at = excluded.noticed_at\n WHERE decision_notices.replaced_by = '' AND excluded.replaced_by <> ''",
				"DO NOTHING")},
			check: "BD10",
		},
		{
			name:     "a record that pins a pack past the version that held it",
			sabotage: sqlstore.Sabotage{Rewrite: replace("pack.pinning", " AND i.version = (SELECT max(p.version) FROM context_packs p WHERE p.workspace_id = i.workspace_id AND p.id = i.pack_id)", "")}, check: "N19",
		},
		{
			name:     "a new version that loses a decision's replacement",
			sabotage: sqlstore.Sabotage{Rewrite: replace("decision.replaced_by", "AND supersedes = :supersedes", "AND supersedes = :supersedes AND id = ''")}, check: "N4",
		},
		{name: "a pack the log does not hold", control: control.Sabotage{DropsPackEvent: true}, check: "PK3"},
		{name: "a woken lane that takes a new pack", control: control.Sabotage{RebuildsPack: true}, check: "PK4"},
		{
			name:    "a scope read that stops at a page",
			control: control.Sabotage{StopsAtOnePage: true}, check: "PK7",
		},
		{
			name:     "a generation that resumed, read as a fresh baseline",
			sabotage: sqlstore.Sabotage{Rewrite: replace("pack.samples", " AND g.start_reason IN ('initial', 'handoff')", "")}, check: "PK20",
		},
		{
			name:     "knowledge of another work order",
			sabotage: sqlstore.Sabotage{Rewrite: replace("pack.additions", " AND work_order_id = :work_order_id", " AND :work_order_id = :work_order_id")}, check: "PK17",
		},
		{
			name:     "a generation that reads any lane's baseline",
			sabotage: sqlstore.Sabotage{Rewrite: replace("generation.baseline", " AND generation = :generation", " AND :generation = :generation")}, check: "PK21",
		},
	}
}
