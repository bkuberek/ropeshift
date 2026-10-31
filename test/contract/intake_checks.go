package contract

import (
	"errors"
	"slices"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// IntakeChecks hold the ledger's side of a work source: what a poll records, and
// how a recorded work order reaches `ready`.
var IntakeChecks = []Check{
	{"I1", "intake records a version, digest, work orders, dependencies, and keys", checkIntakeRecords},
	{"I2", "a second poll with no change appends nothing", checkIntakeQuiet},
	{"I3", "a changed file makes a new version and a pinned work order keeps its version", checkIntakeNewVersion},
	{"I4", "a change elsewhere in the source appends nothing", checkIntakeUnrelated},
	{"I5", "a person records a passed review and the definition of ready moves the work order", checkReviewPasses},
	{"I6", "only a member records a review", checkReviewMembersOnly},
	{"I7", "a contract with no criterion stays in review", checkReviewNoCriteria},
	{"I8", "a passed review waits for a dependency, and the tick moves it once it is done", checkReviewWaits},
	{"I9", "a dependency nobody knows holds a work order back", checkReviewUnknownDependency},
	{"I10", "planning records replay from their events", checkPlanningReplay},
	{"I11", "a file that cannot be read is reported once", checkIntakeProblems},
	{"I12", "racing polls record one version", checkIntakeRace},
	{"I13", "one work order ID in two contracts is a problem", checkIntakeRepeatedOrder},
	{"I14", "a poll that stopped between items finishes on the next poll", checkIntakeResumes},
	{"I15", "a health change is recorded once", checkIntakeHealth},
}

const intakeFile = "contract.md"

func (r *Rig) intake(s ports.Store, source *fake.WorkSource) (control.IntakeReport, error) {
	return r.NewOps(s).Poll(r.Ctx, r.Scope, source)
}

// bookOf replays the whole log.
func (r *Rig) bookOf(s ports.Store) (planning.Book, error) {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return planning.Book{}, err
	}
	book, err := planning.Replay(events)
	if err != nil {
		return planning.Book{}, Violatedf("the log does not replay: %v", err)
	}
	return book, nil
}

func (r *Rig) readContracts(s ports.Store) ([]planning.Contract, error) {
	var contracts []planning.Contract
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		contracts, err = tx.Planning().Contracts()
		return err
	})
	return contracts, err
}

func (r *Rig) readOrders(s ports.Store) ([]workorders.WorkOrder, error) {
	var rows []workorders.WorkOrder
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.WorkOrders().List()
		return err
	})
	return rows, err
}

func (r *Rig) orderByTitle(s ports.Store, title string) (workorders.WorkOrder, error) {
	rows, err := r.readOrders(s)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	for _, row := range rows {
		if row.Title == title {
			return row, nil
		}
	}
	return workorders.WorkOrder{}, Violatedf("no work order titled %q in %d rows", title, len(rows))
}

func (r *Rig) member() ledger.Actor { return ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"} }

func dependentContract() string {
	return `+++
kind = "contract"
id = "pair"
title = "Two work orders in order"

[[criteria]]
id = "c1"
text = "Both land"

[[work_orders]]
id = "first"
title = "First"

[[work_orders]]
id = "second"
title = "Second"
depends_on = ["first"]
+++

The statement.
`
}

func checkIntakeRecords(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(dependentContract()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 || report.WorkOrders != 2 {
		return Violatedf("report %+v", report)
	}
	contracts, err := r.readContracts(s)
	if err != nil {
		return err
	}
	if len(contracts) != 1 || contracts[0].Version != 1 || contracts[0].Digest == "" || contracts[0].Title != "Two work orders in order" {
		return Violatedf("contracts %+v", contracts)
	}
	if contracts[0].Statement != "The statement.\n" || len(contracts[0].Criteria) != 1 || contracts[0].PlanID == "" {
		return Violatedf("contract %+v", contracts[0])
	}
	second, err := r.orderByTitle(s, "Second")
	if err != nil {
		return err
	}
	if second.State != workorders.ContractReview || second.ContractID != contracts[0].ID || second.ContractVersion != 1 {
		return Violatedf("work order %+v", second)
	}
	if second.Size != planning.SizeUnknown {
		return Violatedf("size %q, want %q", second.Size, planning.SizeUnknown)
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		links, err := tx.Planning().Links()
		if err != nil {
			return err
		}
		var natives []string
		for _, link := range links {
			if link.Key.Adapter == "" || link.Key.Instance == "" {
				return Violatedf("link %+v has no composite key", link)
			}
			natives = append(natives, link.RecordKind+":"+link.Key.NativeID)
		}
		slices.Sort(natives)
		if want := []string{"contract:pair", "work_order:first", "work_order:second"}; !slices.Equal(natives, want) {
			return Violatedf("links %v, want %v", natives, want)
		}
		dependencies, err := tx.Planning().Dependencies(second.ID)
		if err != nil {
			return err
		}
		if len(dependencies) != 1 || dependencies[0].DependsOn != "first" || dependencies[0].Kind != planning.DependencyBlocking {
			return Violatedf("dependencies %+v", dependencies)
		}
		return nil
	})
}

func checkIntakeQuiet(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(dependentContract()))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("a quiet poll moved the revision from %d to %d", before, after)
	}
	return r.CheckRevisions(s, r.Scope)
}

func checkIntakeNewVersion(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "One.")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	pinned, err := r.orderByTitle(s, "The work order of alpha")
	if err != nil {
		return err
	}
	changed := ContractText("alpha", "Alpha", "Two.") + "\n"
	source.Write(intakeFile, []byte(changed))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 || report.WorkOrders != 0 {
		return Violatedf("report %+v", report)
	}
	contracts, err := r.readContracts(s)
	if err != nil {
		return err
	}
	if len(contracts) != 2 || contracts[0].ID != contracts[1].ID || contracts[1].Version != 2 || contracts[0].Digest == contracts[1].Digest {
		return Violatedf("contracts %+v", contracts)
	}
	kept, err := r.orderByTitle(s, "The work order of alpha")
	if err != nil {
		return err
	}
	if kept.ContractVersion != pinned.ContractVersion || kept.ContractVersion != 1 || kept.ID != pinned.ID {
		return Violatedf("the pin moved: %+v", kept)
	}
	return r.CheckProjection(s)
}

func checkIntakeUnrelated(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(dependentContract()))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	source.Write("notes.md", []byte("# Prose\n"))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before || report.Versions != 0 {
		return Violatedf("a change elsewhere moved the revision from %d to %d: %+v", before, after, report)
	}
	return nil
}

func (r *Rig) reviewed(s ports.Store) (workorders.WorkOrder, error) {
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "One.")))
	if _, err := r.intake(s, source); err != nil {
		return workorders.WorkOrder{}, err
	}
	return r.orderByTitle(s, "The work order of alpha")
}

func checkReviewPasses(r *Rig) error {
	s := r.Store()
	order, err := r.reviewed(s)
	if err != nil {
		return err
	}
	result, err := r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, r.member())
	if err != nil {
		return err
	}
	if result.State != workorders.Ready || len(result.Failures) != 0 {
		return Violatedf("review result %+v", result)
	}
	row, err := r.WorkOrder(s, order.ID)
	if err != nil {
		return err
	}
	if row.State != workorders.Ready || row.ReviewedBy != r.member().ID {
		return Violatedf("work order %+v", row)
	}
	if _, err := r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, r.member()); err != nil {
		return Violatedf("a repeated review failed: %v", err)
	}
	if err := r.CheckRevisions(s, r.Scope); err != nil {
		return err
	}
	return r.CheckProjection(s)
}

func checkReviewMembersOnly(r *Rig) error {
	s := r.Store()
	order, err := r.reviewed(s)
	if err != nil {
		return err
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorWorker, ledger.ActorLane, ledger.ActorOrchestrator, ledger.ActorSystem} {
		_, err := r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, ledger.Actor{Kind: kind, ID: "someone"})
		if !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s recorded a review: %v", kind, err)
		}
	}
	return nil
}

func checkReviewNoCriteria(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(`+++
kind = "contract"
id = "bare"
title = "Nothing to accept against"

[[work_orders]]
id = "bare-order"
title = "Bare"
+++
`))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	order, err := r.orderByTitle(s, "Bare")
	if err != nil {
		return err
	}
	result, err := r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, r.member())
	if err != nil {
		return err
	}
	if result.State != workorders.ContractReview || len(result.Failures) != 1 || result.Failures[0].Check != workorders.CheckCriteria {
		return Violatedf("review result %+v", result)
	}
	return r.CheckProjection(s)
}

func checkReviewWaits(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(dependentContract()))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	first, err := r.orderByTitle(s, "First")
	if err != nil {
		return err
	}
	second, err := r.orderByTitle(s, "Second")
	if err != nil {
		return err
	}
	reviews := r.NewOps(s)
	result, err := reviews.Pass(r.Ctx, r.Scope, second.ID, r.member())
	if err != nil {
		return err
	}
	if result.State != workorders.ContractReview || len(result.Failures) != 1 || result.Failures[0].Check != workorders.CheckDependencies {
		return Violatedf("review result %+v", result)
	}
	moved, err := reviews.Promote(r.Ctx, r.Scope, r.Actor)
	if err != nil || len(moved) != 0 {
		return Violatedf("promoted %v with a dependency open: %v", moved, err)
	}
	if _, err := reviews.Pass(r.Ctx, r.Scope, first.ID, r.member()); err != nil {
		return err
	}
	if err := r.finish(s, first.ID); err != nil {
		return err
	}
	moved, err = reviews.Promote(r.Ctx, r.Scope, r.Actor)
	if err != nil {
		return err
	}
	if !slices.Equal(moved, []string{second.ID}) {
		return Violatedf("promoted %v, want %s", moved, second.ID)
	}
	return r.CheckProjection(s)
}

// finish walks a ready work order through a lane to `done`.
func (r *Rig) finish(s ports.Store, id string) error {
	holder, err := r.Running(s, id, "worker-a")
	if err != nil {
		return err
	}
	result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}}
	if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, result, r.Actor); err != nil {
		return err
	}
	return r.NewOps(s).Move(r.Ctx, r.Scope, id, workorders.TriggerAcceptancePassed, r.Actor)
}

func checkReviewUnknownDependency(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(`+++
kind = "contract"
id = "ahead"
title = "Ahead of its dependency"

[[criteria]]
id = "c1"
text = "It lands"

[[work_orders]]
id = "ahead-order"
title = "Ahead"
depends_on = ["not-yet"]
+++
`))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	order, err := r.orderByTitle(s, "Ahead")
	if err != nil {
		return err
	}
	result, err := r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, r.member())
	if err != nil {
		return err
	}
	if result.State != workorders.ContractReview || len(result.Failures) != 1 || result.Failures[0].Check != workorders.CheckDependencies {
		return Violatedf("review result %+v", result)
	}
	return nil
}

func checkPlanningReplay(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(dependentContract()))
	source.Write("other.md", []byte(ContractText("alpha", "Alpha", "One.")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	source.Write(intakeFile, []byte(dependentContract()+"\nChanged.\n"))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

// CheckPlanning compares the planning tables with what the log replays to.
func (r *Rig) CheckPlanning(s ports.Store) error {
	book, err := r.bookOf(s)
	if err != nil {
		return err
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		contracts, err := tx.Planning().Contracts()
		if err != nil {
			return err
		}
		if len(contracts) != len(book.Contracts) {
			return Violatedf("%d contract versions, the log replays to %d", len(contracts), len(book.Contracts))
		}
		for _, c := range contracts {
			want, ok := book.Contracts[planning.ContractKey(c.ID, c.Version)]
			if !ok || want.Digest != c.Digest || want.Source != c.Source || want.Statement != c.Statement {
				return Violatedf("contract %s version %d disagrees with its log", c.ID, c.Version)
			}
		}
		plans, err := tx.Planning().Plans()
		if err != nil {
			return err
		}
		if len(plans) != len(book.Plans) {
			return Violatedf("%d plans, the log replays to %d", len(plans), len(book.Plans))
		}
		links, err := tx.Planning().Links()
		if err != nil {
			return err
		}
		if len(links) != len(book.Links) {
			return Violatedf("%d links, the log replays to %d", len(links), len(book.Links))
		}
		for _, link := range links {
			if want, ok := book.Links[planning.LinkKey(link)]; !ok || want.RecordID != link.RecordID {
				return Violatedf("link %+v disagrees with its log", link)
			}
		}
		dependencies, err := tx.Planning().AllDependencies()
		if err != nil {
			return err
		}
		if len(dependencies) != len(book.Dependencies) {
			return Violatedf("%d dependencies, the log replays to %d", len(dependencies), len(book.Dependencies))
		}
		return nil
	})
}

func (r *Rig) binding(s ports.Store, id string) (planning.SourceBinding, error) {
	var binding planning.SourceBinding
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		binding, err = tx.Planning().Binding(id)
		return err
	})
	return binding, err
}

func checkIntakeProblems(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("bad.md", []byte("+++\nkind = \"contract\"\nid = unquoted\n+++\n"))
	source.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "One.")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 1 || report.Versions != 1 {
		return Violatedf("report %+v", report)
	}
	binding, err := r.binding(s, control.BindingID("fake", "memory"))
	if err != nil {
		return err
	}
	if binding.Health != planning.HealthBadFiles || len(binding.Problems) != 1 {
		return Violatedf("binding %+v", binding)
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	source.Write("notes.md", []byte("# Prose\n"))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("the same problem moved the revision from %d to %d", before, after)
	}
	return nil
}

func checkIntakeRace(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(dependentContract()))
	const racers = 4
	failures := make(chan error, racers)
	for range racers {
		go func() {
			_, err := r.intake(s, source)
			failures <- err
		}()
	}
	for range racers {
		if err := <-failures; err != nil {
			return err
		}
	}
	contracts, err := r.readContracts(s)
	if err != nil {
		return err
	}
	rows, err := r.readOrders(s)
	if err != nil {
		return err
	}
	if len(contracts) != 1 || len(rows) != 2 {
		return Violatedf("%d versions and %d work orders", len(contracts), len(rows))
	}
	if err := r.CheckRevisions(s, r.Scope); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkIntakeRepeatedOrder(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	shared := `
[[criteria]]
id = "c1"
text = "It lands"

[[work_orders]]
id = "shared-order"
title = "Shared"
+++
`
	source.Write("a.md", []byte("+++\nkind = \"contract\"\nid = \"one\"\ntitle = \"One\"\n"+shared))
	source.Write("b.md", []byte("+++\nkind = \"contract\"\nid = \"two\"\ntitle = \"Two\"\n"+shared))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 || len(report.Problems) != 1 || report.Problems[0].Path != "b.md" {
		return Violatedf("report %+v", report)
	}
	return nil
}

func checkIntakeResumes(r *Rig) error {
	var writes atomicCount
	s := StopAfter(r.Store(), "intake.item", 1, &writes)
	source := fake.NewWorkSource()
	source.Write("a.md", []byte(ContractText("alpha", "Alpha", "One.")))
	source.Write("b.md", []byte(ContractText("beta", "Beta", "One.")))
	if _, err := r.intake(s, source); !errors.Is(err, ErrStopped) {
		return Violatedf("the poll ended with %v", err)
	}
	contracts, err := r.readContracts(s)
	if err != nil {
		return err
	}
	if len(contracts) != 1 {
		return Violatedf("%d contracts after one item", len(contracts))
	}
	writes.disarm()
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 {
		return Violatedf("report %+v", report)
	}
	return r.CheckPlanning(s)
}

func checkIntakeHealth(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "One.")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	source.SetHealth(ports.SourceHealth{State: planning.HealthFetchFailed, Detail: "no route"})
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	binding, err := r.binding(s, control.BindingID("fake", "memory"))
	if err != nil {
		return err
	}
	if binding.Health != planning.HealthFetchFailed || binding.Detail != "no route" {
		return Violatedf("binding %+v", binding)
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("the same health moved the revision from %d to %d", before, after)
	}
	return nil
}
