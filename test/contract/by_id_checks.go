package contract

import (
	"fmt"
	"reflect"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ByIDChecks hold the reads that name several records by their IDs, so a read of a few records never lists every
// record of their kind: work orders, plans, and the newest version of contracts. Each returns one record for each ID
// that names one, by ID, as a read of that one ID returns it, and nothing for an ID the ledger does not hold, however
// many IDs the read names and however often it names one.
var ByIDChecks = []Check{
	{"BI1", "a read of work orders by ID returns each one an ID names, once and by ID, as a read of it alone does", checkWorkOrdersByID},
	{"BI2", "a read of plans by ID returns each one an ID names, once and by ID, as a read of it alone does", checkPlansByID},
	{"BI3", "a read of contracts by ID returns the newest version of each one an ID names, once and by ID, as a read of it alone does", checkLatestContractsByID},
}

// byIDCount is how many records of one kind a check holds: more than one statement of a read by ID names, so a
// read of most of them runs that statement more than once.
const byIDCount = 70

// askedIDs names most of the held IDs, newest first and then again oldest first, among IDs the ledger does not
// hold, and says the held IDs the read names, in the order of their IDs.
func askedIDs(held []string) (asked, want []string) {
	for i, id := range held {
		if i%5 != 0 {
			want = append(want, id)
		}
	}
	asked = []string{"gone-1"}
	for i := len(want) - 1; i >= 0; i-- {
		asked = append(asked, want[i], "gone-2")
	}
	asked = append(asked, want...)
	slices.Sort(want)
	return asked, want
}

// checkByID reads some held IDs and no IDs with one read by ID, and compares each record it returns with what a
// read of that one ID returns.
func checkByID[T any](r *Rig, s ports.Store, held []string, read func(ports.ReadTx, []string) ([]T, error), one func(ports.ReadTx, string) (T, error), idOf func(T) string) error {
	asked, want := askedIDs(held)
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		found, err := read(tx, asked)
		if err != nil {
			return err
		}
		got := make([]string, len(found))
		for i, record := range found {
			got[i] = idOf(record)
		}
		if !slices.Equal(got, want) {
			return Violatedf("a read of %d IDs found %v, want %v", len(asked), got, want)
		}
		for _, record := range found {
			alone, err := one(tx, idOf(record))
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(record, alone) {
				return Violatedf("%s reads %+v by ID and %+v alone", idOf(record), record, alone)
			}
		}
		none, err := read(tx, nil)
		if err != nil || len(none) != 0 {
			return Violatedf("a read of no IDs found %v: %v", none, err)
		}
		return nil
	})
}

func checkWorkOrdersByID(r *Rig) error {
	s := r.Store()
	held := make([]string, 0, byIDCount)
	for range byIDCount {
		order, err := r.NewWorkOrder(s)
		if err != nil {
			return err
		}
		held = append(held, order.ID)
	}
	return checkByID(r, s, held,
		func(tx ports.ReadTx, ids []string) ([]workorders.WorkOrder, error) { return tx.WorkOrders().ByIDs(ids) },
		func(tx ports.ReadTx, id string) (workorders.WorkOrder, error) { return tx.WorkOrders().Get(id) },
		func(order workorders.WorkOrder) string { return order.ID })
}

func checkPlansByID(r *Rig) error {
	s := r.Store()
	held := make([]string, 0, byIDCount)
	err := s.Write(r.Ctx, r.Scope, "test.plans", func(tx ports.WriteTx) error {
		for i := range byIDCount {
			id := r.ID("plan")
			if err := tx.Planning().InsertPlan(planning.Plan{ID: id, Title: fmt.Sprintf("Plan %d", i), Status: planning.StatusActive, Source: "plans/" + id + ".md"}); err != nil {
				return err
			}
			held = append(held, id)
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: r.Actor, Subject: ledger.Subject{Kind: "note", ID: "note"}})
		return err
	})
	if err != nil {
		return err
	}
	return checkByID(r, s, held,
		func(tx ports.ReadTx, ids []string) ([]planning.Plan, error) { return tx.Planning().PlansByIDs(ids) },
		func(tx ports.ReadTx, id string) (planning.Plan, error) { return tx.Planning().Plan(id) },
		func(plan planning.Plan) string { return plan.ID })
}

// checkLatestContractsByID holds contracts of one to three versions, each version with its own title, so a read
// that returned an older version would read another title.
func checkLatestContractsByID(r *Rig) error {
	s := r.Store()
	plan := r.ID("plan")
	held := make([]string, 0, byIDCount)
	err := s.Write(r.Ctx, r.Scope, "test.contracts", func(tx ports.WriteTx) error {
		if err := tx.Planning().InsertPlan(planning.Plan{ID: plan, Title: "Contracts", Status: planning.StatusActive}); err != nil {
			return err
		}
		for i := range byIDCount {
			id := r.ID("contract")
			for version := int64(1); version <= int64(1+i%3); version++ {
				if err := tx.Planning().InsertContract(planning.Contract{
					ID: id, Version: version, PlanID: plan, Title: fmt.Sprintf("Contract %d at version %d", i, version), Digest: fmt.Sprintf("sha256:%d-%d", i, version),
					Statement: "Do the work.", Status: planning.StatusActive, Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
				}); err != nil {
					return err
				}
			}
			held = append(held, id)
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: r.Actor, Subject: ledger.Subject{Kind: "note", ID: "note"}})
		return err
	})
	if err != nil {
		return err
	}
	return checkByID(r, s, held,
		func(tx ports.ReadTx, ids []string) ([]planning.Contract, error) {
			return tx.Planning().LatestContracts(ids)
		},
		func(tx ports.ReadTx, id string) (planning.Contract, error) { return tx.Planning().LatestContract(id) },
		func(contract planning.Contract) string { return contract.ID })
}
