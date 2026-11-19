package contract

import (
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// retryWorld is a creationWorld that creates one repository, search, which the plan version's contract gives work, with the
// source its files come from, so a later version can be read.
type retryWorld struct {
	*creationWorld
	files *fake.WorkSource
}

// retryWorld reads a plan version whose manifest lists api, which the project holds, and search, which the version
// creates, and leaves its request waiting.
func (r *Rig) retryWorld(s ports.Store) (*retryWorld, error) {
	files, err := r.manifestProject(s)
	if err != nil {
		return nil, err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return nil, err
	}
	files.Write("repositories.md", []byte(manifestText(creationEntry("search", "../search", ""), "api")))
	files.Write("core.md", []byte(placedContract{ID: "core", Plan: manifestPlan, Orders: []orderIn{{ID: "core-api", Repo: "api"}, {ID: "core-search", Repo: "search"}}}.text()))
	if _, err := r.intake(s, files); err != nil {
		return nil, err
	}
	machine, err := r.planningMachine()
	if err != nil {
		return nil, err
	}
	request, err := r.theOpenPlanApproval(s)
	if err != nil {
		return nil, err
	}
	return &retryWorld{creationWorld: &creationWorld{store: s, machine: machine, creator: newMachineCreator(machine), repositories: r.repositories(s), request: request}, files: files}, nil
}

// decide approves the request and registers what its version lists, as a member's word on a card does.
func (r *Rig) decide(w *retryWorld) ([]control.Added, error) {
	approved, err := r.approve(w.store, w.request, true, r.member())
	if err != nil {
		return nil, err
	}
	w.request = approved
	return r.runCreation(w.creationWorld, false)
}

// retry runs the creation of search again as a member.
func (r *Rig) retry(w *retryWorld, name string) ([]control.Added, error) {
	return w.repositories.RetryCreation(r.Ctx, r.Scope, w.request.ID, name, w.machine, w.creator, "shop", r.member())
}

// uncreated reads what the project's approvals leave uncreated, with no creation running.
func (r *Rig) uncreated(s ports.Store, running func(approval, name string) bool) ([]control.UncreatedRepository, error) {
	var out []control.UncreatedRepository
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = control.UncreatedRepositories(tx, running)
		return err
	})
	return out, err
}

// noCreationRuns says no creation runs now in this process.
func noCreationRuns(string, string) bool { return false }

// approvalEventCount counts the events about an approval.
func (r *Rig) approvalEventCount(s ports.Store, approval string) (int, error) {
	count := 0
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: authority.SubjectApproval, ID: approval})
		count = len(events)
		return err
	})
	return count, err
}

// startedCreation appends the record of a creation that started and records nothing after, as a stop of the daemon leaves it.
func (r *Rig) startedCreation(s ports.Store, approval, name string) error {
	return s.Write(r.Ctx, r.Scope, "test.creation_started", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryCreationStarted, Actor: r.member(), Subject: ledger.Subject{Kind: authority.SubjectApproval, ID: approval},
			Data: mustJSON(ledger.CreationStartedEvent{Name: name, Key: ledger.CreationKey(approval, name), ManifestPath: "../" + name, Path: "/work/" + name, Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: name, Visibility: ledger.VisibilityPrivate}),
		})
		return err
	})
}

// sameKeys says the jobs the creator was asked to create all carry the creation's key.
func sameKeys(jobs []ledger.RepositoryCreation, key string) bool {
	return !slices.ContainsFunc(jobs, func(job ledger.RepositoryCreation) bool { return job.Key != key })
}

// retryRefusesWhatTheApprovalDoesNotCreate holds a retry to the entries its approved version creates: a repository the
// project holds, a name the manifest does not list, and an approval nobody holds are refused, with nothing recorded and the
// creator asked nothing.
func (r *Rig) retryRefusesWhatTheApprovalDoesNotCreate(w *retryWorld) error {
	before, err := r.approvalEventCount(w.store, w.request.ID)
	if err != nil {
		return err
	}
	checked, created := len(w.creator.Checked()), len(w.creator.Created())
	for name, why := range map[string]string{"api": "a repository the project holds", "ghost": "a name the manifest does not list"} {
		if _, err := r.retry(w, name); !errors.Is(err, workorders.ErrIllegalMove) {
			return Violatedf("a retry of %s, %s, answered %v, want an illegal move", name, why, err)
		}
	}
	if _, err := w.repositories.RetryCreation(r.Ctx, r.Scope, "no-such-approval", "search", w.machine, w.creator, "shop", r.member()); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a retry under no approval answered %v, want not found", err)
	}
	if after, err := r.approvalEventCount(w.store, w.request.ID); err != nil || after != before {
		return Violatedf("a refused retry recorded events: %d before, %d after: %v", before, after, err)
	}
	if len(w.creator.Checked()) != checked || len(w.creator.Created()) != created {
		return Violatedf("a refused retry had %d more checked and %d more created", len(w.creator.Checked())-checked, len(w.creator.Created())-created)
	}
	return nil
}

func checkRetryAfterAFailedPushResumesAndRegisters(r *Rig) error {
	s := r.Store()
	w, err := r.retryWorld(s)
	if err != nil {
		return err
	}
	w.creator.failsPush("search", "search", "main")
	// An approval nobody has approved creates nothing, so a retry under it is refused and asks the creator nothing.
	if _, err := r.retry(w, "search"); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("a retry under an unapproved plan approval answered %v, want an illegal move", err)
	}
	if checked := w.creator.Checked(); len(checked) != 0 {
		return Violatedf("a retry under an unapproved plan approval had %v checked", asked(checked))
	}
	if _, err := r.decide(w); err != nil {
		return err
	}
	approval := w.request.ID
	key := ledger.CreationKey(approval, "search")
	failed := map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.CreateFailed, path: "/work/search", reason: "denied by the forge"},
	}
	if err := r.recordedAre(s, approval, failed); err != nil {
		return Violatedf("approving: %v", err)
	}
	items, err := r.uncreated(s, noCreationRuns)
	if err != nil || len(items) != 1 || items[0].Name != "search" || items[0].State != control.CreationFailed || items[0].Creation == nil || len(items[0].Creation.Remaining) != 1 || !strings.Contains(items[0].Reason, "denied by the forge") {
		return Violatedf("a creation that failed its push waits for a member as %+v: %v", items, err)
	}
	if err := r.retryRefusesWhatTheApprovalDoesNotCreate(w); err != nil {
		return err
	}
	// The forge takes the push now. The retry goes on from the creation's own journal: the creator is asked to check and to
	// create under the key of the first run, once more, and the clone the first run left is registered.
	w.creator.passes("search", "search", "main")
	checked, created := len(w.creator.Checked()), len(w.creator.Created())
	added, err := r.retry(w, "search")
	if err != nil {
		return Violatedf("a retry after a failed push: %v", err)
	}
	if err := outcomesAre(added, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Created, path: "/work/search"},
	}); err != nil {
		return Violatedf("a retry after a failed push: %v", err)
	}
	if search, _ := outcomeNamed(added, "search"); search.Creation == nil || search.Creation.Makefile == "" || search.Repository.Name != "search" {
		return Violatedf("the retry left search as %+v, want it created and registered", search)
	}
	if err := r.recordedAre(s, approval, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Created, path: "/work/search"},
	}); err != nil {
		return Violatedf("a retry after a failed push: %v", err)
	}
	if got, want := len(w.creator.Checked())-checked, 1; got != want {
		return Violatedf("the retry asked the creator to check %d times, want %d", got, want)
	}
	jobs := w.creator.Created()
	if len(jobs)-created != 1 || !sameKeys(jobs, key) {
		return Violatedf("the retry asked the creator to create %d more times under the keys %v, want once more under %s", len(jobs)-created, jobs, key)
	}
	if types, err := r.creationTypes(s, approval); err != nil || !slices.Equal(types, []string{"started search", "finished search", "started search", "finished search"}) {
		return Violatedf("the creation events are %v: %v", types, err)
	}
	if found, err := w.repositories.Resolve(r.Ctx, r.Scope, "search"); err != nil || found.Name != "search" {
		return Violatedf("the retry registered %+v: %v", found, err)
	}
	if items, err = r.uncreated(s, noCreationRuns); err != nil || len(items) != 0 {
		return Violatedf("a created search still waits for a member as %+v: %v", items, err)
	}
	// The project holds the name now, so another retry is refused, records nothing, and asks the creator nothing.
	checked, created = len(w.creator.Checked()), len(w.creator.Created())
	if _, err := r.retry(w, "search"); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("a retry of a registered repository answered %v, want an illegal move", err)
	}
	if after, err := r.creationEvents(s, approval); err != nil || len(after) != 4 || len(w.creator.Checked()) != checked || len(w.creator.Created()) != created {
		return Violatedf("a refused retry recorded %d events and asked the creator: %v", len(after), err)
	}
	return r.CheckProjection(s)
}

func checkAnInterruptedCreationIsAnItemAndRetryFinishesIt(r *Rig) error {
	s := r.Store()
	w, err := r.retryWorld(s)
	if err != nil {
		return err
	}
	w.creator.passes("search", "search", "main")
	approved, err := r.approve(s, w.request, true, r.member())
	if err != nil {
		return err
	}
	w.request = approved
	approval := approved.ID
	// A creation the daemon's stop left started: its folder is a clone with the origin it added, and nothing pushed.
	w.machine.Place(ports.Clone{Path: "/work/search", ID: "id-search", Remote: "git@github.com:acme/search.git", DefaultBranch: "main"})
	if err := r.startedCreation(s, approval, "search"); err != nil {
		return err
	}
	items, err := r.uncreated(s, noCreationRuns)
	if err != nil || len(items) != 1 {
		return Violatedf("an interrupted creation reads as %+v: %v", items, err)
	}
	if items[0].Approval.ID != approval || items[0].Name != "search" || items[0].ManifestPath != "../search" || items[0].Path != "/work/search" || items[0].State != control.CreationInterrupted {
		return Violatedf("an interrupted creation reads as %+v, want search of %s at /work/search as interrupted", items[0], approval)
	}
	running, err := r.uncreated(s, func(a, n string) bool { return a == approval && n == "search" })
	if err != nil || len(running) != 1 || running[0].State != control.CreationRunning {
		return Violatedf("a creation that runs now reads as %+v: %v", running, err)
	}
	// A create that timed out after the forge made the repository finds it there on every retry, which refuses with the repository
	// it found: the item says so, and says what a member does about it, which is to clone it and name it as the remote, or skip it.
	w.creator.SetCheck("search", ledger.CreationCheck{Refusal: &ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedRemoteExists, Detail: "acme/search already exists on GitHub",
	}})
	if refused, err := r.retry(w, "search"); err != nil {
		return Violatedf("a retry that finds the repository on the forge: %v", err)
	} else if search, _ := outcomeNamed(refused, "search"); search.Outcome != ledger.RegistrationRefused {
		return Violatedf("a retry that finds the repository on the forge reads %+v, want it refused", search)
	}
	if items, err = r.uncreated(s, noCreationRuns); err != nil || len(items) != 1 {
		return Violatedf("a creation refused for an existing repository reads as %+v: %v", items, err)
	}
	const remedy = "clone acme/search at /work/search and write `remote` in place of `create` in the manifest entry for search, or skip it"
	if items[0].State != control.CreationRefused || !strings.Contains(items[0].Reason, "acme/search already exists on GitHub") || items[0].Remedy != remedy || items[0].Path != "/work/search" {
		return Violatedf("a creation refused for an existing repository reads as %+v, want it refused with the remedy %q", items[0], remedy)
	}
	w.creator.passes("search", "search", "main")
	added, err := r.retry(w, "search")
	if err != nil {
		return Violatedf("a retry of an interrupted creation: %v", err)
	}
	if search, _ := outcomeNamed(added, "search"); search.Outcome != ledger.Created || search.Creation == nil || search.Creation.Makefile == "" || search.Repository.Name != "search" {
		return Violatedf("a retry of an interrupted creation reads %+v, want it finished and registered", search)
	}
	// The creator was asked once, under the key of the creation that started, and made no other: the retry goes on from the
	// creation's journal and repeats no step of it, so the clone the stop left is not made again.
	jobs := w.creator.Created()
	if len(jobs) != 1 || jobs[0].Name != "search" || jobs[0].Key != ledger.CreationKey(approval, "search") || jobs[0].Path != "/work/search" {
		return Violatedf("the creator was asked to create %+v, want search once, as the creation that started", jobs)
	}
	if checked := w.creator.Checked(); len(checked) != 0 && !sameKeys(checked, ledger.CreationKey(approval, "search")) {
		return Violatedf("the creator was asked to check %+v", checked)
	}
	if types, err := r.creationTypes(s, approval); err != nil || !slices.Equal(types, []string{"started search", "started search", "finished search"}) {
		return Violatedf("the creation events are %v: %v", types, err)
	}
	if items, err = r.uncreated(s, noCreationRuns); err != nil || len(items) != 0 {
		return Violatedf("a retried creation still waits as %+v: %v", items, err)
	}
	return r.CheckProjection(s)
}

func checkSkipRecordsTheReasonAndALaterVersionCreates(r *Rig) error {
	s := r.Store()
	w, err := r.retryWorld(s)
	if err != nil {
		return err
	}
	w.creator.refuses("search", "gh_signed_out", "gh is signed out", "gh auth login --hostname github.com")
	if _, err := r.decide(w); err != nil {
		return err
	}
	approval := w.request.ID
	for name, reason := range map[string]string{"empty": "", "blank": "  \n", "too long": strings.Repeat("x", 2001)} {
		if _, err := w.repositories.SkipCreation(r.Ctx, r.Scope, approval, "search", reason, r.member()); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("a skip with a %s reason answered %v, want an invalid request", name, err)
		}
	}
	if _, err := w.repositories.SkipCreation(r.Ctx, r.Scope, approval, "api", "held already", r.member()); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("a skip of a repository the project holds answered %v, want an illegal move", err)
	}
	added, err := w.repositories.SkipCreation(r.Ctx, r.Scope, approval, "search", "built by hand", r.member())
	if err != nil {
		return err
	}
	want := map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Skipped, path: "/work/search", reason: "built by hand"},
	}
	if err := outcomesAre(added, want); err != nil {
		return Violatedf("a skip: %v", err)
	}
	if err := r.recordedAre(s, approval, want); err != nil {
		return Violatedf("a skip: %v", err)
	}
	var skipped ledger.CreationRecord
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: authority.SubjectApproval, ID: approval})
		if err != nil {
			return err
		}
		skipped, err = ledger.LatestCreation(events, "search")
		return err
	})
	if err != nil || skipped.State != ledger.CreationSkipped || skipped.Reason != "built by hand" {
		return Violatedf("the approval's latest word on search is %+v: %v", skipped, err)
	}
	if items, err := r.uncreated(s, noCreationRuns); err != nil || len(items) != 0 {
		return Violatedf("a skipped creation still waits for a member as %+v: %v", items, err)
	}
	// The work that names search stays in contract review, which no skip moves.
	named := 0
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		orders, err := tx.WorkOrders().List()
		for _, order := range orders {
			if order.UnknownRepository != "search" {
				continue
			}
			named++
			if order.State != workorders.ContractReview {
				return Violatedf("work %s that names search is %s after the skip, want contract_review", order.ID, order.State)
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	if named != 1 {
		return Violatedf("%d work orders name search, want the one that gives it work", named)
	}
	// A later version whose work orders target search creates it: the skip belongs to the approval that was skipped.
	w.files.Write("core.md", []byte(placedContract{ID: "core", Plan: manifestPlan, Orders: []orderIn{{ID: "core-api", Repo: "api"}, {ID: "core-search", Repo: "search"}, {ID: "core-search-2", Repo: "search"}}}.text()))
	if _, err := r.intake(s, w.files); err != nil {
		return err
	}
	later, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if later.ID == approval {
		return Violatedf("a changed contract raised no later version")
	}
	w.creator.passes("search", "search", "main")
	w.request = later
	added, err = r.decide(w)
	if err != nil {
		return err
	}
	if err := outcomesAre(added, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Created, path: "/work/search"},
	}); err != nil {
		return Violatedf("approving the later version: %v", err)
	}
	if jobs := w.creator.Created(); len(jobs) != 1 || jobs[0].Key != ledger.CreationKey(later.ID, "search") {
		return Violatedf("the later version asked the creator to create %+v, want search once under its own approval", jobs)
	}
	if err := r.recordedAre(s, approval, want); err != nil {
		return Violatedf("the skipped approval after the later version: %v", err)
	}
	if items, err := r.uncreated(s, noCreationRuns); err != nil || len(items) != 0 {
		return Violatedf("Needs you holds %+v after the later version created search", items)
	}
	return r.CheckProjection(s)
}

func checkADelegateRetriesAndSkipsNothing(r *Rig) error {
	s := r.Store()
	w, err := r.retryWorld(s)
	if err != nil {
		return err
	}
	w.creator.refuses("search", "gh_signed_out", "gh is signed out", "gh auth login --hostname github.com")
	if _, err := r.decide(w); err != nil {
		return err
	}
	approval := w.request.ID
	before, err := r.approvalEventCount(s, approval)
	if err != nil {
		return err
	}
	recorded, err := r.registrationOf(s, approval)
	if err != nil {
		return err
	}
	checked := len(w.creator.Checked())
	for _, actor := range []ledger.Actor{r.delegate(), {Kind: ledger.ActorLane, ID: "lane-1"}, {Kind: ledger.ActorOrchestrator, ID: "worker-1"}, {Kind: ledger.ActorSystem, ID: "tick"}} {
		if _, err := w.repositories.RetryCreation(r.Ctx, r.Scope, approval, "search", w.machine, w.creator, "shop", actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s's retry answered %v, want forbidden", actor.Kind, err)
		}
		if _, err := w.repositories.SkipCreation(r.Ctx, r.Scope, approval, "search", "not mine to skip", actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s's skip answered %v, want forbidden", actor.Kind, err)
		}
	}
	if after, err := r.approvalEventCount(s, approval); err != nil || after != before {
		return Violatedf("a refused word recorded events: %d before, %d after: %v", before, after, err)
	}
	if now, err := r.registrationOf(s, approval); err != nil || !slices.Equal(outcomesOfRecord(now), outcomesOfRecord(recorded)) {
		return Violatedf("a refused word changed the record: %+v, was %+v: %v", now, recorded, err)
	}
	if len(w.creator.Checked()) != checked || len(w.creator.Created()) != 0 {
		return Violatedf("a refused word asked the creator to check %d times and create %v", len(w.creator.Checked())-checked, asked(w.creator.Created()))
	}
	return nil
}

// outcomesOfRecord reads each recorded outcome as a name, an outcome, and its reason.
func outcomesOfRecord(record *ledger.ManifestRegistration) []string {
	var out []string
	if record != nil {
		for _, one := range record.Repositories {
			out = append(out, one.Name+" "+string(one.Outcome)+" "+one.Reason)
		}
	}
	return out
}
