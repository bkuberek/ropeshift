package control

import (
	"cmp"
	"errors"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
)

// estimateReads are what an estimate is made from within one transaction (ADR-0026): the work orders, the
// roster, the repositories, the contracts, the estimate policy, and the delivery records the fold keeps.
type estimateReads struct {
	orders       ports.WorkOrderReader
	roster       ports.RosterReader
	repositories ports.RepositoryReader
	planning     ports.PlanningReader
	policies     ports.PolicyReader
	deliveries   ports.DeliveryReader
}

func writeEstimateReads(tx ports.WriteTx) estimateReads {
	return estimateReads{
		orders: tx.WorkOrders(), roster: tx.Roster(), repositories: tx.Repositories(), planning: tx.Planning(),
		policies: tx.Policies(), deliveries: tx.Deliveries(),
	}
}

// estimable says a work order takes an estimate: a `change` or a `rework` that has not ended.
func estimable(order workorders.WorkOrder) bool {
	return (order.Kind == workorders.KindChange || order.Kind == workorders.KindRework) && !workorders.Terminal(order.State)
}

// estimatePolicyIn reads the estimate policy in force: the built-in one until a member publishes one.
func estimatePolicyIn(policies ports.PolicyReader) (delivery.EstimatePolicy, error) {
	version, err := policies.InForce(delivery.PolicyEstimate)
	if errors.Is(err, ports.ErrNotFound) {
		return delivery.DefaultEstimatePolicy(), nil
	}
	if err != nil {
		return delivery.EstimatePolicy{}, err
	}
	return delivery.ReadEstimatePolicy(version)
}

// keysOf reads the keys of work that has not started, which are known before it starts (ADR-0026): its kind;
// its declared size, `unknown` when it declares none; the agent it names or the default mapping gives it before
// any rework, and that agent's capability class; its repository and that repository's merge authority level
// now; and the criteria of the contract version it pins. An agent the roster cannot give leaves the class empty.
func (r estimateReads) keysOf(view roster.Resolved, order workorders.WorkOrder) (delivery.Keys, error) {
	keys := delivery.Keys{Kind: string(order.Kind), Size: cmp.Or(order.Size, measures.SizeUnknown), Repository: order.RepositoryID}
	if agent, err := view.DefaultAgent(string(order.Kind), order.Size, order.Agent); err == nil {
		keys.Agent, keys.CapabilityClass = agent.Name, agent.Class
	}
	if order.RepositoryID != "" {
		repository, err := r.repositories.Repository(order.RepositoryID)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return delivery.Keys{}, err
		}
		keys.MergeAuthority = string(cmp.Or(repository.MergeAuthority, ledger.PersonMerges))
	}
	if order.ContractID != "" {
		contract, err := r.planning.Contract(order.ContractID, order.ContractVersion)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return delivery.Keys{}, err
		}
		keys.Criteria = int64(len(contract.Criteria))
	}
	return keys, nil
}

// estimateSet estimates the open work orders among those given, from the delivery records the fold keeps and
// under the estimate policy in force, as read at a revision, and seals them as one set under a new ID. It
// reports false when none of them takes an estimate.
func (r estimateReads) estimateSet(set delivery.EstimateSet, orders []workorders.WorkOrder, now int64) (delivery.EstimateSet, bool, error) {
	var open []workorders.WorkOrder
	for _, order := range orders {
		if estimable(order) {
			open = append(open, order)
		}
	}
	if len(open) == 0 {
		return delivery.EstimateSet{}, false, nil
	}
	policy, err := estimatePolicyIn(r.policies)
	if err != nil {
		return delivery.EstimateSet{}, false, err
	}
	records, err := r.deliveries.Records()
	if err != nil {
		return delivery.EstimateSet{}, false, err
	}
	view, err := ResolveIn(r.roster)
	if err != nil {
		return delivery.EstimateSet{}, false, err
	}
	if set.ID, err = newID(now); err != nil {
		return delivery.EstimateSet{}, false, err
	}
	set.EstimatorVersion, set.PolicyVersion, set.CreatedAt = delivery.EstimatorVersion, policy.Version, now
	for _, order := range open {
		keys, err := r.keysOf(view.Resolved, order)
		if err != nil {
			return delivery.EstimateSet{}, false, err
		}
		e := delivery.EstimateOf(delivery.EstimateInput{WorkOrder: order.ID, Keys: keys, Records: records, Policy: policy, Rev: set.Rev})
		if e.ID, err = newID(now); err != nil {
			return delivery.EstimateSet{}, false, err
		}
		set.Estimates = append(set.Estimates, e)
	}
	return set.Seal(), true, nil
}

// keepEstimates estimates work orders in the transaction that made or planned them, at the revision it has
// reached, and keeps the set. plan and version name the plan version it estimates, and are empty for work made
// outside a plan (FR-EST-10).
func keepEstimates(tx ports.WriteTx, plan string, version int64, orders []workorders.WorkOrder) error {
	workspace, err := tx.Workspace()
	if err != nil {
		return err
	}
	set, made, err := writeEstimateReads(tx).estimateSet(delivery.EstimateSet{Plan: plan, PlanVersion: version, Rev: workspace.Rev}, orders, tx.Now())
	if err != nil || !made {
		return err
	}
	return tx.Estimates().Add(set)
}

// estimateMade estimates one work order made outside a plan, in the transaction that made it.
func estimateMade(tx ports.WriteTx, order workorders.WorkOrder) error {
	return keepEstimates(tx, "", 0, []workorders.WorkOrder{order})
}

// estimatePlanVersion estimates the open work of every contract a plan version covers, in the transaction that
// recorded the version. The set stays outside the plan's digest, so new history never voids an approval.
func estimatePlanVersion(tx ports.WriteTx, version planning.PlanVersion) error {
	var orders []workorders.WorkOrder
	for _, entry := range version.Records {
		if planning.EntryKind(entry.Ref) != "contract" || entry.Record == "" {
			continue
		}
		rows, err := tx.WorkOrders().ByContract(entry.Record)
		if err != nil {
			return err
		}
		orders = append(orders, rows...)
	}
	return keepEstimates(tx, version.PlanID, version.Version, orders)
}
