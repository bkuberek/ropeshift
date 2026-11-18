package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// spendPolicyInForce reads the project's spend policy in force with its version. A project nobody configured runs on
// the built-in version, which reading writes nothing for.
func spendPolicyInForce(policies ports.PolicyReader) (authority.SpendPolicy, error) {
	version, err := policies.InForce(authority.PolicySpend)
	if errors.Is(err, ports.ErrNotFound) {
		return authority.DefaultSpendPolicy(), nil
	}
	if err != nil {
		return authority.SpendPolicy{}, err
	}
	return authority.ReadSpendPolicy(version)
}

// spendReads is the part of a transaction the spend fold reads. A read transaction and a write transaction both give
// it, through the readers their writers contain.
type spendReads struct {
	lanes     ports.LaneReader
	orders    ports.WorkOrderReader
	estimates ports.EstimateReader
	policies  ports.PolicyReader
	// events reads what the ledger says about one subject, which is where a member's raises of a limit are kept.
	events func(ledger.Subject) ([]ledger.Event, error)
	// now reads the ledger's clock, which is the one clock a project's day and week are read by.
	now func() (int64, error)
}

func spendReadsOf(tx ports.ReadTx) spendReads {
	return spendReads{lanes: tx.Lanes(), orders: tx.WorkOrders(), estimates: tx.Estimates(), policies: tx.Policies(), events: tx.EventsAbout, now: tx.Now}
}

func spendWritesOf(tx ports.WriteTx) spendReads {
	return spendReads{
		lanes: tx.Lanes(), orders: tx.WorkOrders(), estimates: tx.Estimates(), policies: tx.Policies(), events: tx.EventsAbout,
		now: func() (int64, error) { return tx.Now(), nil },
	}
}

// usageOf reads the generations of every lane that served a work order, a lane at a time, in order. A lane that
// is named is left out, for a worker that holds that lane's own spend and asks only what the others cost.
func (r spendReads) usageOf(workOrder, except string) ([][]ledger.GenerationUsage, error) {
	rows, err := r.lanes.Rows()
	if err != nil {
		return nil, err
	}
	var lanes [][]ledger.GenerationUsage
	for _, row := range rows {
		if row.WorkOrder != workOrder || row.ID == except {
			continue
		}
		generations, err := r.lanes.Generations(row.ID)
		if err != nil {
			return nil, err
		}
		usage := make([]ledger.GenerationUsage, 0, len(generations))
		for _, g := range generations {
			usage = append(usage, g.Usage)
		}
		lanes = append(lanes, usage)
	}
	return lanes, nil
}

// charged folds the generations of every lane that served a work order (FR-SPEND-3). A retried work order is one
// work order, so its earlier lanes' spend stays.
func (r spendReads) charged(workOrder string) (ledger.Charged, error) {
	lanes, err := r.usageOf(workOrder, "")
	if err != nil {
		return ledger.Charged{}, err
	}
	return ledger.Charge(lanes), nil
}

// raisedTo is the hard limit a member's raises and resumes set on a work order, or 0 when none did.
func (r spendReads) raisedTo(workOrder string) (float64, error) {
	events, err := r.events(ledger.Subject{Kind: ledger.SubjectSpend, ID: workOrder})
	if err != nil {
		return 0, err
	}
	return ledger.RaisedLimit(events), nil
}

// bandOf is the cost band a work order's limit reads: the band recorded at plan approval, or its current band when it
// has no baseline, and nil when it has no estimate.
func (r spendReads) bandOf(id string) (*delivery.SpendBand, error) {
	_, estimate, err := r.estimates.BaselineOf(id)
	baselined := err == nil
	if errors.Is(err, ports.ErrNotFound) {
		estimate, err = r.estimates.Current(id)
	}
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if estimate.Status != delivery.Estimated {
		return nil, nil
	}
	return &delivery.SpendBand{
		P50: estimate.CostP50USD, P85: estimate.CostP85USD, Outcomes: estimate.SampleCount, Open: estimate.P85Open, Baselined: baselined,
	}, nil
}

// limitsOf derives a work order's limits. A review's hard limit is a share of the hard limit of the work order it
// judges, which is read the same way.
func (r spendReads) limitsOf(policy authority.SpendPolicy, order workorders.WorkOrder) (delivery.SpendLimits, error) {
	input := delivery.SpendInput{Kind: string(order.Kind), Size: order.Size}
	switch order.Kind {
	case workorders.KindChange, workorders.KindRework:
		band, err := r.bandOf(order.ID)
		if err != nil {
			return delivery.SpendLimits{}, err
		}
		input.Band = band
	case workorders.KindReview:
		reviewed, err := r.orders.Get(order.Reviews)
		if errors.Is(err, ports.ErrNotFound) {
			break
		}
		if err != nil {
			return delivery.SpendLimits{}, err
		}
		limits, err := r.limitsOf(policy, reviewed)
		if err != nil {
			return delivery.SpendLimits{}, err
		}
		input.ReviewedHard = limits.Hard
	}
	limits := delivery.SpendLimitsOf(policy, input)
	// A raise lifts the limit the band derives and never lowers it, and it lifts a limit there is: a work order the
	// policy leaves with no hard limit has none to raise.
	raised, err := r.raisedTo(order.ID)
	if err != nil {
		return delivery.SpendLimits{}, err
	}
	if limits.Hard > 0 && raised > limits.Hard {
		limits.Hard = raised
	}
	return limits, nil
}

// spendOf is a work order's charged spend beside its limits.
func (r spendReads) spendOf(id string) (ledger.WorkOrderSpend, error) {
	order, err := r.orders.Get(id)
	if err != nil {
		return ledger.WorkOrderSpend{}, err
	}
	policy, err := spendPolicyInForce(r.policies)
	if err != nil {
		return ledger.WorkOrderSpend{}, err
	}
	charged, err := r.charged(id)
	if err != nil {
		return ledger.WorkOrderSpend{}, err
	}
	limits, err := r.limitsOf(policy, order)
	if err != nil {
		return ledger.WorkOrderSpend{}, err
	}
	out := ledger.WorkOrderSpend{
		WorkOrder: id, Kind: string(order.Kind), Charged: charged, Label: charged.Label(), SoftLimit: limits.Soft, HardLimit: limits.Hard,
		Basis: string(limits.Basis), PolicyVersion: policy.Version,
	}
	if limits.Hard > 0 {
		out.Remaining = max(limits.Hard-charged.ChargedUSD, 0)
		raised, err := r.raisedTo(id)
		if err != nil {
			return ledger.WorkOrderSpend{}, err
		}
		if raised == limits.Hard {
			out.RaisedTo = raised
		}
	}
	if limits.PastSoft(charged.ChargedUSD) {
		out.Conditions = append(out.Conditions, ledger.ConditionOverEstimate)
	}
	if limits.PastHard(charged.ChargedUSD) {
		out.Conditions = append(out.Conditions, ledger.ConditionSpendLimit)
	}
	return out, nil
}

// WorkOrderSpend reads one work order's charged spend and limits for a member (FR-SPEND-3, FR-SPEND-4).
func (s *Spend) WorkOrderSpend(ctx context.Context, scope ledger.WorkspaceID, id string) (ledger.WorkOrderSpend, error) {
	var out ledger.WorkOrderSpend
	err := s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		out, err = spendReadsOf(tx).spendOf(id)
		return err
	})
	return out, err
}

// SpendState is what a member reads to see where spend stands: the project's spend policy in force and the
// installation's, each with the version record that holds it, and the spend of each open work order that a limit
// covers, against its limits.
type SpendState struct {
	Project             authority.SpendPolicy
	ProjectVersion      authority.PolicyVersion
	Installation        authority.InstallationSpendPolicy
	InstallationVersion authority.PolicyVersion
	WorkOrders          []ledger.WorkOrderSpend
}

// State reads the spend state of a project (FR-SPEND-18).
func (s *Spend) State(ctx context.Context, scope ledger.WorkspaceID) (SpendState, error) {
	var out SpendState
	versions, err := s.SpendPolicyVersions(ctx, scope)
	if err != nil {
		return out, err
	}
	out.ProjectVersion = versions[0]
	if out.Project, err = authority.ReadSpendPolicy(out.ProjectVersion); err != nil {
		return out, err
	}
	installed, err := s.InstallationSpendPolicyVersions(ctx, scope)
	if err != nil {
		return out, err
	}
	out.InstallationVersion = installed[0]
	if out.Installation, err = authority.ReadInstallationSpendPolicy(out.InstallationVersion); err != nil {
		return out, err
	}
	err = s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		orders, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		reads := spendReadsOf(tx)
		for _, order := range orders {
			if workorders.Terminal(order.State) {
				continue
			}
			spend, err := reads.spendOf(order.ID)
			if err != nil {
				return err
			}
			if spend.HardLimit > 0 || spend.SoftLimit > 0 || spend.Charged.ChargedUSD > 0 {
				out.WorkOrders = append(out.WorkOrders, spend)
			}
		}
		return nil
	})
	return out, err
}

// limitNotice is the data of EventSpendSoftLimit and EventSpendLimited: which work order, the limit it reached, and how
// far it had come.
type limitNotice struct {
	WorkOrder     string  `json:"work_order"`
	Limit         float64 `json:"limit"`
	ChargedUSD    float64 `json:"charged_usd"`
	EstimatedTurn int64   `json:"estimated_turns"`
	PolicyVersion int64   `json:"policy_version"`
}

// usageReported keeps what a generation has spent, in place of what it last reported, and records once when the work
// order it belongs to reaches its soft limit (FR-SPEND-4) and once when it reaches its hard one (FR-SPEND-5). The soft
// limit stops nothing. The hard limit is the worker's to hold a lane to, in the lane's own session, and this is the
// ledger's word that it was reached.
func usageReported(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var usage ledger.GenerationUsage
	if err := json.Unmarshal(e.Payload, &usage); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if usage.CostUSD < 0 || usage.KnownTurns < 0 || usage.UnknownTurns < 0 {
		return fmt.Errorf("%w: a generation's usage is negative", workorders.ErrInvalid)
	}
	e.Payload = nil
	draft, err := uploadedDraft(tx, worker, id, e)
	if err != nil {
		return err
	}
	reads := spendWritesOf(tx)
	before, err := reads.charged(e.WorkOrder)
	if err != nil {
		return err
	}
	if _, err := tx.Lanes().ReportUsage(e.Lane, e.Generation, usage); err != nil {
		return err
	}
	if _, err := tx.Append(draft); err != nil {
		return err
	}
	spend, err := reads.spendOf(e.WorkOrder)
	if err != nil {
		return err
	}
	// What the work order was charged since its last report is what the project spent today: the tally only grows,
	// because a charge never falls (the generation's floor), and the project's daily and weekly limits read it.
	if err := tx.Lanes().AddSpend(ledger.SpendScopeWork, spend.Charged.ChargedUSD-before.ChargedUSD); err != nil {
		return err
	}
	if err := noteLimit(tx, spend, ledger.ConditionOverEstimate, ledger.EventSpendSoftLimit, spend.SoftLimit); err != nil {
		return err
	}
	return noteLimit(tx, spend, ledger.ConditionSpendLimit, ledger.EventSpendLimited, spend.HardLimit)
}

// noteLimit records, once per work order and limit, that charged spend reached a limit. The notice is a ledger event
// that names its scope, so a member sees it and a later delivery channel can send it. A raised limit is a new limit,
// so passing it is noted again.
func noteLimit(tx ports.WriteTx, spend ledger.WorkOrderSpend, condition, eventType string, limit float64) error {
	if !slices.Contains(spend.Conditions, condition) {
		return nil
	}
	subject := ledger.Subject{Kind: ledger.SubjectSpend, ID: spend.WorkOrder}
	events, err := tx.EventsAbout(subject)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		var earlier limitNotice
		if json.Unmarshal(event.Data, &earlier) == nil && earlier.Limit == limit {
			return nil
		}
	}
	data, err := json.Marshal(limitNotice{
		WorkOrder: spend.WorkOrder, Limit: limit, ChargedUSD: spend.Charged.ChargedUSD, EstimatedTurn: spend.Charged.EstimatedTurns,
		PolicyVersion: spend.PolicyVersion,
	})
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{
		Type: eventType, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "spend"}, Subject: subject, Data: data,
	})
	return err
}

// spendAllowance reads what a work order allows a lane to spend, for the worker that holds the lane to its hard limit:
// the limit in force, and what every other lane of the work order is charged. It is nil for work with no hard limit.
//
// The lane's own earlier generations are in it too, apart from the generation the worker says it is starting and any
// after it: a worker that resumes a lane it never ran has no journal of what they spent.
func spendAllowance(tx ports.ReadTx, order workorders.WorkOrder, lane string, generation int64) (*ledger.SpendAllowance, error) {
	reads := spendReadsOf(tx)
	policy, err := spendPolicyInForce(reads.policies)
	if err != nil {
		return nil, err
	}
	limits, err := reads.limitsOf(policy, order)
	if err != nil {
		return nil, err
	}
	// A project with a daily or weekly limit holds every lane of it, with or without a limit of its own on the work order,
	// so the worker reads whether the project is held, and keeps the ledger current with what the lane spends.
	project, err := reads.projectSpendUnder(policy, false)
	if err != nil {
		return nil, err
	}
	if limits.Hard <= 0 {
		if !project.Limited() {
			return nil, nil
		}
		return &ledger.SpendAllowance{HeadroomShare: policy.BudgetFlagHeadroomShare, ProjectHold: project.Hold(), ProjectLimited: true}, nil
	}
	others, err := reads.usageOf(order.ID, lane)
	if err != nil {
		return nil, err
	}
	var earlier []ledger.GenerationUsage
	generations, err := tx.Lanes().Generations(lane)
	if err != nil {
		return nil, err
	}
	if generation == 0 {
		// A worker that names no generation is one from before the allowance held the lane's own, and the one the ledger
		// has running is the one it means.
		if generation, err = tx.Lanes().Generation(lane); err != nil && !errors.Is(err, ports.ErrNotFound) {
			return nil, err
		}
	}
	for _, g := range generations {
		if g.Number < generation {
			earlier = append(earlier, g.Usage)
		}
	}
	return &ledger.SpendAllowance{
		HardUSD: limits.Hard, OthersUSD: ledger.Charge(others).ChargedUSD, LaneUSD: ledger.Charge([][]ledger.GenerationUsage{earlier}).ChargedUSD,
		HeadroomShare: policy.BudgetFlagHeadroomShare, ProjectHold: project.Hold(), ProjectLimited: project.Limited(),
	}, nil
}
