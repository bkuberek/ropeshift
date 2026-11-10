package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// planFileRead is a plan file as a batch gave it, once the ledger took it. A plan version is made at
// the end of a batch, from this and from the records the plan has by then, so a commit that holds a
// plan, its documents, and its contracts makes one version and not one for each file.
type planFileRead struct {
	digest    string
	source    string
	documents []string
	decisions []planning.DraftDecision
}

// planOrigin says where a batch came from. A batch read from an accepted `plan` work order's commit
// came from that work order's lane.
type planOrigin struct{ workOrder string }

// nativeIDs gives the ID a file gave a record, which a ref is made of, for a record of the ledger.
type nativeIDs map[string]string

func nativeKey(kind, record string) string { return kind + "\x00" + record }

func readNativeIDs(planned ports.PlanningReader) (nativeIDs, error) {
	links, err := planned.Links()
	if err != nil {
		return nil, err
	}
	out := nativeIDs{}
	for _, link := range links {
		out[nativeKey(link.RecordKind, link.RecordID)] = link.Key.NativeID
	}
	return out, nil
}

// planEntries gathers every record that names a plan: the plan's own file at the digest given, each
// document whose file names it, and the newest version of each contract that does. Refs come from the
// files, so the same records have the same refs before the ledger holds them.
func planEntries(planned ports.PlanningReader, native nativeIDs, plan, fileDigest string) ([]planning.Entry, error) {
	file, held := native[nativeKey(planning.SubjectPlan, plan)]
	if !held {
		return nil, errNoPlanFile
	}
	entries := []planning.Entry{{Ref: planning.PlanRef(file), Digest: fileDigest}}
	documents, err := planned.PlanDocuments(plan)
	if err != nil {
		return nil, err
	}
	for _, document := range documents {
		latest, err := planned.LatestDocumentVersion(document.ID)
		if err != nil {
			return nil, err
		}
		entries = append(entries, planning.Entry{Ref: document.ID, Digest: latest.Digest, Record: document.ID})
	}
	contracts, err := newestContracts(planned, plan)
	if err != nil {
		return nil, err
	}
	for _, contract := range contracts {
		entry := planning.Entry{Ref: planning.ContractRef(native[nativeKey(planning.SubjectContract, contract.ID)]), Digest: contract.Digest, Record: contract.ID}
		for _, criterion := range contract.Criteria {
			entry.Parts = append(entry.Parts, planning.CriterionPart(criterion))
		}
		entry.Parts = append(entry.Parts, contract.Orders...)
		entries = append(entries, entry)
	}
	return entries, nil
}

// orderParts are the work orders a contract file lists, as the parts a plan's difference reads.
func orderParts(file planning.ContractFile) []planning.Part {
	var parts []planning.Part
	for _, spec := range file.WorkOrders {
		parts = append(parts, planning.WorkOrderPart(spec))
	}
	return parts
}

// reconcilePlanVersions gives a plan a new version for each digest it has not had, and returns how
// many it made. It runs after a batch is recorded, and it is idempotent: a plan whose digest is the
// newest version's is only read, so a poll that changes nothing appends nothing. A poll that stops
// before it runs leaves the cursor where it was, and the next poll runs it (FR-PLAN-9, FR-PLAN-11).
func (i *Intake) reconcilePlanVersions(ctx context.Context, scope ledger.WorkspaceID, files map[string]planFileRead, origin planOrigin) (int, error) {
	var drifted []string
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		plans, err := tx.Planning().Plans()
		if err != nil {
			return err
		}
		native, err := readNativeIDs(tx.Planning())
		if err != nil {
			return err
		}
		for _, plan := range plans {
			digest, _, err := planDigestOf(tx.Planning(), native, plan.ID, files)
			if errors.Is(err, errNoPlanFile) {
				continue
			}
			if err != nil {
				return err
			}
			latest, err := tx.Planning().LatestPlanVersion(plan.ID)
			if errors.Is(err, ports.ErrNotFound) || (err == nil && latest.Digest != digest) {
				drifted = append(drifted, plan.ID)
				continue
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	made := 0
	for _, plan := range drifted {
		err := i.store.Write(ctx, scope, "intake.plan_version", func(tx ports.WriteTx) error {
			// The plan is read again under the revision, because another writer may have moved it since.
			recorded, err := recordPlanVersion(tx, plan, files, origin)
			if recorded {
				made++
			}
			return err
		})
		if err != nil {
			return made, err
		}
	}
	return made, nil
}

// errNoPlanFile means a plan has no plan file the ledger read, as a plan that a contract with no plan
// made for itself, which is approved as a contract and not as a plan.
var errNoPlanFile = errors.New("the plan has no file")

// planDigestOf reads a plan's digest from the ledger, with the file a batch gave when it gave one that
// is newer than the ledger's.
func planDigestOf(planned ports.PlanningReader, native nativeIDs, plan string, files map[string]planFileRead) (string, []planning.Entry, error) {
	fileDigest, err := planFileDigest(planned, plan, files)
	if err != nil {
		return "", nil, err
	}
	entries, err := planEntries(planned, native, plan, fileDigest)
	if err != nil {
		return "", nil, err
	}
	return planning.PlanDigest(entries), entries, nil
}

// planFileDigest is the digest of the plan's file: the batch's when it read one, and otherwise the
// newest version's. A plan the ledger has no version of and no file for has no plan file.
func planFileDigest(planned ports.PlanningReader, plan string, files map[string]planFileRead) (string, error) {
	if read, held := files[plan]; held {
		return read.digest, nil
	}
	latest, err := planned.LatestPlanVersion(plan)
	switch {
	case err == nil:
		return latest.FileDigest, nil
	case errors.Is(err, ports.ErrNotFound):
		return "", errNoPlanFile
	}
	return "", err
}

// recordPlanVersion makes the next version of a plan when its digest moved, and raises its approval in
// the same transaction. The older version's open approval is superseded in that transaction too, so
// a member never sees two requests for one plan.
func recordPlanVersion(tx ports.WriteTx, plan string, files map[string]planFileRead, origin planOrigin) (bool, error) {
	native, err := readNativeIDs(tx.Planning())
	if err != nil {
		return false, err
	}
	digest, entries, err := planDigestOf(tx.Planning(), native, plan, files)
	if errors.Is(err, errNoPlanFile) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	next := planning.PlanVersion{PlanID: plan, Version: 1, Digest: digest, Records: entries, WorkOrder: origin.workOrder}
	latest, err := tx.Planning().LatestPlanVersion(plan)
	switch {
	case err == nil && latest.Digest == digest:
		return false, nil
	case err == nil:
		next.Version = latest.Version + 1
		next.FileDigest, next.Source, next.Documents, next.Decisions = latest.FileDigest, latest.Source, latest.Documents, latest.Decisions
	case !errors.Is(err, ports.ErrNotFound):
		return false, err
	}
	if read, held := files[plan]; held {
		next.FileDigest, next.Source, next.Documents, next.Decisions = read.digest, read.source, read.documents, read.decisions
	}
	if err := (planningWriter{tx: tx, actor: intakeActor}).planVersion(next); err != nil {
		return false, err
	}
	record, err := tx.Planning().Plan(plan)
	if err != nil {
		return false, err
	}
	ref := planFileRef(entries)
	approved, found, err := approvedBeforeRecorded(tx, next, ref)
	if err != nil {
		return false, err
	}
	if found {
		return true, finishApproved(tx, next, approved)
	}
	return true, askForPlanApproval(tx, planAsk{
		plan: plan, ref: ref, title: record.Title, version: next.Version, digest: next.Digest, entries: entries,
		decisions: len(next.Decisions), source: next.Source, workOrder: next.WorkOrder,
	})
}

// planFileRef is the ID the plan's own file gives it, from the entries of a version.
func planFileRef(entries []planning.Entry) string {
	for _, entry := range entries {
		if planning.EntryKind(entry.Ref) == "plan" {
			return strings.TrimPrefix(entry.Ref, "plan:")
		}
	}
	return ""
}

// lastApproved reads the newest version of a plan a member approved, which a new version is compared
// with. It is false for a plan no member has approved.
func lastApproved(tx ports.WriteTx, plan string) (planning.PlanVersion, bool, error) {
	rows, err := tx.Approvals().All()
	if err != nil {
		return planning.PlanVersion{}, false, err
	}
	var base int64
	for _, row := range rows {
		if row.Kind == authority.KindPlan && row.Plan == plan && row.State == authority.ApprovalApproved && row.PlanVersion > base {
			base = row.PlanVersion
		}
	}
	if base == 0 {
		return planning.PlanVersion{}, false, nil
	}
	version, err := tx.Planning().PlanVersion(plan, base)
	return version, err == nil, err
}

// planRequester is the principal that asks for a plan to be approved: the lane whose branch held the
// plan, or else intake on behalf of the source the plan was read from.
func planRequester(tx ports.WriteTx, workOrder string) (ledger.Actor, error) {
	if workOrder == "" {
		return intakeActor, nil
	}
	order, err := tx.WorkOrders().Get(workOrder)
	if err != nil {
		return ledger.Actor{}, err
	}
	artifacts, err := tx.Evidence().Artifacts(workOrder)
	if err != nil {
		return ledger.Actor{}, err
	}
	if lane := attemptBranch(artifacts, order.Fence).Lane; lane != "" {
		return ledger.Actor{Kind: ledger.ActorLane, ID: lane}, nil
	}
	return intakeActor, nil
}

// planAsk is a plan version a member is asked to approve. It is the same whether the ledger holds the
// version or only a branch that nothing has read: plan is empty until the ledger holds the plan.
type planAsk struct {
	plan      string
	ref       string
	title     string
	version   int64
	digest    string
	entries   []planning.Entry
	decisions int
	source    string
	workOrder string
}

// planDetailOf reads what a plan approval says of its version.
func planDetailOf(a authority.Approval) planning.PlanDetail {
	var detail planning.PlanDetail
	if a.Detail != "" {
		_ = json.Unmarshal([]byte(a.Detail), &detail)
	}
	return detail
}

// askForPlanApproval asks for one plan version to be approved, bound to its digest, and closes the
// request for an older version that nobody decided. Approval is never delegated, so the request has no
// time to expire in: it waits for a member (FR-PLAN-12).
func askForPlanApproval(tx ports.WriteTx, ask planAsk) error {
	rows, err := tx.Approvals().All()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Kind != authority.KindPlan || !row.Open() {
			continue
		}
		samePlan := ask.plan != "" && row.Plan == ask.plan
		sameBranch := ask.workOrder != "" && row.WorkOrder == ask.workOrder && planDetailOf(row).Ref == ask.ref
		if !samePlan && !sameBranch {
			continue
		}
		next, err := row.Supersede()
		if err != nil {
			return err
		}
		if err := noteApproval(tx, next, row.State, intakeActor); err != nil {
			return err
		}
	}
	difference := planning.Compare(0, nil, ask.entries)
	if ask.plan != "" {
		if base, approved, err := lastApproved(tx, ask.plan); err != nil {
			return err
		} else if approved {
			difference = planning.Compare(base.Version, base.Records, ask.entries)
		}
	}
	detail, err := json.Marshal(planning.PlanDetail{
		Title: ask.title, Ref: ask.ref, Counts: planning.CountEntries(ask.entries), Decisions: ask.decisions, Difference: difference, Source: ask.source,
	})
	if err != nil {
		return err
	}
	requester, err := planRequester(tx, ask.workOrder)
	if err != nil {
		return err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return err
	}
	request := authority.Approval{
		ID: id, Kind: authority.KindPlan, WorkOrder: ask.workOrder, Subject: ask.ref + "@" + strconv.FormatInt(ask.version, 10),
		SubjectDigest: ask.digest, Summary: planSummary(ask), RequestedBy: authority.Principal(requester),
		State: authority.ApprovalPending, RaisedAt: tx.Now(), Plan: ask.plan, PlanVersion: ask.version, Detail: string(detail),
	}
	if ask.plan != "" {
		request.Subject = planning.ContractKey(ask.plan, ask.version)
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if _, err := tx.Approvals().Insert(request); err != nil {
		return err
	}
	event, err := authority.ApprovalEvent(request, intakeActor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// planSummary is what a member reads first of a plan version: what it is and what it holds.
func planSummary(ask planAsk) string {
	counts := planning.CountEntries(ask.entries)
	var parts []string
	for _, kind := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%d %s", counts[kind], kind))
	}
	if ask.decisions != 0 {
		parts = append(parts, fmt.Sprintf("%d draft decisions", ask.decisions))
	}
	held := "no other records"
	if len(parts) != 0 {
		held = strings.Join(parts, ", ")
	}
	return fmt.Sprintf("Plan %s, version %d: %s", ask.title, ask.version, held)
}
