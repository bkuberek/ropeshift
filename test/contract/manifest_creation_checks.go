package contract

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

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
	return &retryWorld{creationWorld: &creationWorld{store: s, machine: machine, creator: newMachineCreator(machine, request.ID), repositories: r.repositories(s), request: request}, files: files}, nil
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

// retried is what a retry answered.
type retried struct {
	added []control.Added
	err   error
}

// retryAsync runs a retry of search in the background.
func (r *Rig) retryAsync(w *retryWorld) chan retried {
	done := make(chan retried, 1)
	go func() {
		added, err := r.retry(w, "search")
		done <- retried{added, err}
	}()
	return done
}

// awaiting waits for a creator to hear a job, and frees it when none comes.
func awaiting(heard, release chan struct{}) error {
	select {
	case <-heard:
		return nil
	case <-time.After(10 * time.Second):
		close(release)
		return Violatedf("the creator was never asked")
	}
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
	// A creator that cannot answer, as a worker that has not started, stops the run, and the approval keeps the error as the
	// problem of its registration, which every later decision replays. The creation waits for a member all the same, and a retry
	// of it runs the whole registration again, under the registration's lock, and keeps no problem once it ran.
	errNotStarted := errors.New("the worker has not started")
	w.creator.FailCheck(w.creator.key("search"), errNotStarted)
	if _, err := r.decide(w); !errors.Is(err, errNotStarted) {
		return Violatedf("approving with a creator that cannot answer ended with %v, want %v", err, errNotStarted)
	}
	approval := w.request.ID
	key := ledger.CreationKey(approval, "search")
	if record, err := r.registrationOf(s, approval); err != nil || record == nil || !strings.Contains(record.Problem, errNotStarted.Error()) {
		return Violatedf("the approval recorded %+v: %v, want the creator's error as its problem", record, err)
	}
	if items, err := r.uncreated(s, noCreationRuns); err != nil || len(items) != 1 || items[0].Name != "search" || items[0].State != control.CreationRefused || !strings.Contains(items[0].Reason, errNotStarted.Error()) {
		return Violatedf("a creation the creator could not answer for waits for a member as %+v: %v", items, err)
	}
	w.creator.FailCheck(w.creator.key("search"), nil)
	w.creator.failsPush("search", "search", "main")
	if _, err := r.retry(w, "search"); err != nil {
		return Violatedf("a retry after the creator could answer: %v", err)
	}
	if record, err := r.registrationOf(s, approval); err != nil || record == nil || record.Problem != "" {
		return Violatedf("the approval recorded %+v after the retry: %v, want no problem", record, err)
	}
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
	// A retry whose creation fails its push again is blocked inside the creation. A creation that runs is answered at once, as
	// an illegal move, and not waited for, and the creator is asked nothing for it.
	w.creator.failsPush("search", "search", "main")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	w.creator.during = func(ledger.RepositoryCreation) {
		once.Do(func() { close(entered) })
		<-release
	}
	running := r.retryAsync(w)
	if err := awaiting(entered, release); err != nil {
		return err
	}
	checked, created := len(w.creator.Checked()), len(w.creator.Created())
	answered := make(chan error, 1)
	go func() { _, err := r.retry(w, "search"); answered <- err }()
	select {
	case err := <-answered:
		if !errors.Is(err, workorders.ErrIllegalMove) || !strings.Contains(err.Error(), "being created now") {
			close(release)
			return Violatedf("a retry of a creation that runs answered %v, want an illegal move that says it is being created now", err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		return Violatedf("a retry of a creation that runs waited for it")
	}
	if len(w.creator.Checked()) != checked || len(w.creator.Created()) != created {
		close(release)
		return Violatedf("a retry of a creation that runs had the creator check %d times and create %d more", len(w.creator.Checked())-checked, len(w.creator.Created())-created)
	}
	close(release)
	if again := <-running; again.err != nil {
		return Violatedf("a retry that failed its push again: %v", again.err)
	} else if search, _ := outcomeNamed(again.added, "search"); search.Outcome != ledger.CreateFailed {
		return Violatedf("a retry that failed its push again reads %+v, want create failed", search)
	}
	w.creator.during = nil
	// The forge takes the push now. The retry goes on from the creation's own journal: the creator is asked to check and to
	// create under the key of the first run, once more, and the clone the first run left is registered. A second retry that
	// comes while the first is between its start and its creation waits for it, and finds the repository registered.
	w.creator.passes("search", "search", "main")
	checked, created = len(w.creator.Checked()), len(w.creator.Created())
	checkedIn, release2 := make(chan struct{}), make(chan struct{})
	var onceCheck sync.Once
	w.creator.checking = func(ledger.RepositoryCreation) {
		onceCheck.Do(func() { close(checkedIn) })
		<-release2
	}
	first := r.retryAsync(w)
	if err := awaiting(checkedIn, release2); err != nil {
		return err
	}
	second := r.retryAsync(w)
	select {
	case got := <-second:
		close(release2)
		return Violatedf("a retry that came while another was checking did not wait for it: %v", got.err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release2)
	one, two := <-first, <-second
	w.creator.checking = nil
	if one.err != nil {
		return Violatedf("a retry after a failed push: %v", one.err)
	}
	if !errors.Is(two.err, workorders.ErrIllegalMove) || !strings.Contains(two.err.Error(), "holds a repository named search") {
		return Violatedf("a retry that waited for one that registered search answered %v, want an illegal move because the project holds it", two.err)
	}
	added := one.added
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
	if types, err := r.creationTypes(s, approval); err != nil || !slices.Equal(types, []string{"started search", "finished search", "started search", "finished search", "started search", "finished search"}) {
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
	if after, err := r.creationEvents(s, approval); err != nil || len(after) != 6 || len(w.creator.Checked()) != checked || len(w.creator.Created()) != created {
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
	w.creator.refuses("search", ledger.TemplateRefusedRemoteExists, "acme/search already exists on GitHub", "")
	if refused, err := r.retry(w, "search"); err != nil {
		return Violatedf("a retry that finds the repository on the forge: %v", err)
	} else if search, _ := outcomeNamed(refused, "search"); search.Outcome != ledger.RegistrationRefused {
		return Violatedf("a retry that finds the repository on the forge reads %+v, want it refused", search)
	}
	if items, err = r.uncreated(s, noCreationRuns); err != nil || len(items) != 1 {
		return Violatedf("a creation refused for an existing repository reads as %+v: %v", items, err)
	}
	const remedy = "clone acme/search to ../search, and write `remote` in place of `create` in the manifest's entry for search, or skip it"
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
	for name, reason := range map[string]string{
		"empty": "", "blank": "  \n", "too long": strings.Repeat("x", 2001), "an escape sequence": "built\x1b[31m by hand", "a line break": "built\nby hand", "a bell": "built\x07",
	} {
		if _, err := w.repositories.SkipCreation(r.Ctx, r.Scope, approval, "search", reason, r.member()); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("a skip with a %s reason answered %v, want an invalid request", name, err)
		}
	}
	if _, err := w.repositories.SkipCreation(r.Ctx, r.Scope, approval, "api", "held already", r.member()); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("a skip of a repository the project holds answered %v, want an illegal move", err)
	}
	// The reason is stored as one plain line, so the spaces inside it are one each.
	added, err := w.repositories.SkipCreation(r.Ctx, r.Scope, approval, "search", "  built   by hand ", r.member())
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
	} // A skipped creation is left as the member left it: a retry of it is refused, and a later approved version creates it.
	if _, err := r.retry(w, "search"); !errors.Is(err, workorders.ErrIllegalMove) || !strings.Contains(err.Error(), "skipped; a later approved version that targets it creates it") {
		return Violatedf("a retry of a skipped creation answered %v, want an illegal move that says a later approved version creates it", err)
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
	w.request = later
	w.creator = newMachineCreator(w.machine, later.ID)
	w.creator.passes("search", "search", "main")
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

func checkARetryOfARunThatStoppedNeverCreatesWhatWasSkipped(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("alpha", "../alpha", "")+creationEntry("beta", "../beta", ""), []string{"alpha", "beta"}, nil)
	if err != nil {
		return err
	}
	// The check of alpha cannot be answered, as a worker that has not started cannot, which stops the run: beta and api are not
	// attempted, and the approval keeps the error as the problem of its registration.
	errNotStarted := errors.New("the worker has not started")
	w.creator.FailCheck(w.creator.key("alpha"), errNotStarted)
	w.creator.passes("beta", "beta", "main")
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	if _, err := r.runCreation(w, false); !errors.Is(err, errNotStarted) {
		return Violatedf("approving with a creator that cannot answer ended with %v, want %v", err, errNotStarted)
	}
	// The member skips alpha, and the creator can answer for it now, so a creation of it would succeed.
	if _, err := w.repositories.SkipCreation(r.Ctx, r.Scope, w.request.ID, "alpha", "built by hand", r.member()); err != nil {
		return err
	}
	w.creator.FailCheck(w.creator.key("alpha"), nil)
	w.creator.passes("alpha", "alpha", "main")
	// Retrying beta registers the whole version again, with alpha left as the member left it.
	added, err := w.repositories.RetryCreation(r.Ctx, r.Scope, w.request.ID, "beta", w.machine, w.creator, "shop", r.member())
	if err != nil {
		return Violatedf("a retry of beta: %v", err)
	}
	if created := asked(w.creator.Created()); !slices.Equal(created, []string{"beta"}) {
		return Violatedf("a retry of beta had the creator create %v, want beta alone: alpha was skipped", created)
	}
	want := map[string]entryOutcome{
		"alpha": {outcome: ledger.Skipped, path: "/work/alpha", reason: "built by hand"},
		"beta":  {outcome: ledger.Created, path: "/work/beta"},
		"api":   {outcome: ledger.AlreadyRegistered, path: "/work/api"},
	}
	if err := outcomesAre(added, want); err != nil {
		return Violatedf("a retry of beta: %v", err)
	}
	if err := r.recordedAre(s, w.request.ID, want); err != nil {
		return Violatedf("a retry of beta: %v", err)
	}
	if found, err := w.repositories.Resolve(r.Ctx, r.Scope, "alpha"); err == nil {
		return Violatedf("a retry of beta registered the skipped alpha as %+v", found)
	}
	if items, err := r.uncreated(s, noCreationRuns); err != nil || len(items) != 0 {
		return Violatedf("Needs you holds %+v after beta was created and alpha skipped: %v", items, err)
	}
	return r.CheckProjection(s)
}

// duplicateWorld is a project whose plan version lists two manifests, each with an entry that creates acme/search: search at
// ../search, and mirror at ../mirror/search. The creation of search stops at its push, so the repository is not held.
func (r *Rig) duplicateWorld(s ports.Store) (*creationWorld, error) {
	files, err := r.manifestProject(s)
	if err != nil {
		return nil, err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return nil, err
	}
	files.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \""+manifestPlan+"\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\", \"repositories:second\"]\n+++\n\nOne plan for every repository.\n"))
	files.Write("repositories.md", []byte(manifestText(creationEntry("search", "../search", ""), "api")))
	files.Write("second.md", []byte("+++\nkind = \"repositories\"\nid = \"second\"\ntitle = \"The second\"\nplan = \""+manifestPlan+"\"\n"+creationEntry("mirror", "../mirror/search", "")+"+++\n\nThe second manifest.\n"))
	files.Write("core.md", []byte(placedContract{ID: "core", Plan: manifestPlan, Orders: []orderIn{{ID: "core-api", Repo: "api"}, {ID: "core-search", Repo: "search"}, {ID: "core-mirror", Repo: "mirror"}}}.text()))
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
	return &creationWorld{store: s, machine: machine, creator: newMachineCreator(machine, request.ID), repositories: r.repositories(s), request: request}, nil
}

func checkARetryRefusesAForgeRepositoryAnotherEntryOfTheVersionCreates(r *Rig) error {
	s := r.Store()
	w, err := r.duplicateWorld(s)
	if err != nil {
		return err
	}
	w.creator.failsPush("search", "search", "main")
	w.creator.passes("mirror", "search", "main")
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	mirror, _ := outcomeNamed(added, "mirror")
	if mirror.Outcome != ledger.RegistrationRefused || !strings.Contains(mirror.Reason, "which the entry search of this version creates too") {
		return Violatedf("approving reads mirror as %+v, want it refused for creating what search creates", mirror)
	}
	// Retrying mirror runs it alone, and it is still refused: the other entry of the version holds the forge repository.
	created := len(w.creator.Created())
	again, err := w.repositories.RetryCreation(r.Ctx, r.Scope, w.request.ID, "mirror", w.machine, w.creator, "shop", r.member())
	if err != nil {
		return Violatedf("a retry of mirror: %v", err)
	}
	mirror, _ = outcomeNamed(again, "mirror")
	if mirror.Outcome != ledger.RegistrationRefused || !strings.Contains(mirror.Reason, "which the entry search of this version creates too") {
		return Violatedf("a retry reads mirror as %+v, want it refused for creating what search creates", mirror)
	}
	if jobs := w.creator.Created()[created:]; len(jobs) != 0 {
		return Violatedf("a retry of mirror had the creator create %v", asked(jobs))
	}
	return nil
}

func checkARunAWorkerStoppedAsBusyIsAnItemAndARetryRegistersTheRest(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", "")+creationEntry("stubborn", "../stubborn", "")+creationEntry("site", "../site", ""), []string{"search", "stubborn", "site"}, nil)
	if err != nil {
		return err
	}
	for _, name := range []string{"search", "site"} {
		w.creator.passes(name, name, "main")
	}
	// The check of stubborn is refused as busy though it waited, so the run stops there and keeps no refusal: stubborn and
	// every entry after it are not attempted, and the approval records the error as the problem of its registration.
	w.creator.refuses("stubborn", ledger.TemplateRefusedBusy, "another creation on this worker writes /work/stubborn", "")
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	if _, err := r.runCreation(w, false); err == nil || !strings.Contains(err.Error(), "stubborn") {
		return Violatedf("a busy refusal of a check that waited stopped the run with %v", err)
	}
	items, err := r.uncreated(s, noCreationRuns)
	if err != nil || len(items) != 2 || items[0].Name != "stubborn" || items[1].Name != "site" {
		return Violatedf("a run the worker stopped as busy waits for a member as %+v: %v", items, err)
	}
	for _, item := range items {
		if item.State != control.CreationFailed || !strings.Contains(item.Reason, "stubborn") {
			return Violatedf("%s waits for a member as %+v, want it not attempted because the run stopped at stubborn", item.Name, item)
		}
	}
	// The member skips site, and the worker answers now. A retry of stubborn registers the version again, under the
	// registration's lock: stubborn is created, search is the repository it was, and site stays as the member left it.
	if _, err := w.repositories.SkipCreation(r.Ctx, r.Scope, w.request.ID, "site", "not this time", r.member()); err != nil {
		return err
	}
	w.creator.passes("stubborn", "stubborn", "main")
	created := len(w.creator.Created())
	added, err := w.repositories.RetryCreation(r.Ctx, r.Scope, w.request.ID, "stubborn", w.machine, w.creator, "shop", r.member())
	if err != nil {
		return Violatedf("a retry of a run the worker stopped as busy: %v", err)
	}
	want := map[string]entryOutcome{
		"search":   {outcome: ledger.AlreadyRegistered, path: "/work/search"},
		"stubborn": {outcome: ledger.Created, path: "/work/stubborn"},
		"api":      {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"site":     {outcome: ledger.Skipped, path: "/work/site", reason: "not this time"},
	}
	if err := outcomesAre(added, want); err != nil {
		return Violatedf("a retry of a run the worker stopped as busy: %v", err)
	}
	if jobs := asked(w.creator.Created()[created:]); !slices.Equal(jobs, []string{"stubborn"}) {
		return Violatedf("the retry had the creator create %v, want stubborn alone: search is held and site was skipped", jobs)
	}
	record, err := r.registrationOf(s, w.request.ID)
	if err != nil || record == nil || record.Problem != "" {
		return Violatedf("the approval recorded %+v after the retry: %v, want no problem", record, err)
	}
	if items, err := r.uncreated(s, noCreationRuns); err != nil || len(items) != 0 {
		return Violatedf("Needs you holds %+v after the retry", items)
	}
	return r.CheckProjection(s)
}

// skippedDuplicatesWorld is a project whose plan version lists two manifests, each with two entries that create one forge
// repository: alpha at ../alpha and alphacopy at ../copy/alpha, and beta at ../beta and betacopy at ../copy/beta.
func (r *Rig) skippedDuplicatesWorld(s ports.Store) (*creationWorld, error) {
	files, err := r.manifestProject(s)
	if err != nil {
		return nil, err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return nil, err
	}
	files.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \""+manifestPlan+"\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\", \"repositories:second\"]\n+++\n\nOne plan for every repository.\n"))
	files.Write("repositories.md", []byte(manifestText(creationEntry("alpha", "../alpha", "")+creationEntry("beta", "../beta", ""), "api")))
	files.Write("second.md", []byte("+++\nkind = \"repositories\"\nid = \"second\"\ntitle = \"The second\"\nplan = \""+manifestPlan+"\"\n"+creationEntry("alphacopy", "../copy/alpha", "")+creationEntry("betacopy", "../copy/beta", "")+"+++\n\nThe second manifest.\n"))
	orders := []orderIn{{ID: "core-api", Repo: "api"}}
	for _, name := range []string{"alpha", "beta", "alphacopy", "betacopy"} {
		orders = append(orders, orderIn{ID: "core-" + name, Repo: name})
	}
	files.Write("core.md", []byte(placedContract{ID: "core", Plan: manifestPlan, Orders: orders}.text()))
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
	return &creationWorld{store: s, machine: machine, creator: newMachineCreator(machine, request.ID), repositories: r.repositories(s), request: request}, nil
}

func checkASkippedEntryTakesNoForgeRepository(r *Rig) error {
	s := r.Store()
	w, err := r.skippedDuplicatesWorld(s)
	if err != nil {
		return err
	}
	// The creation of alpha and of beta stops at its push, so neither repository is held, and the entry after each that would
	// create the same forge repository is refused for it.
	w.creator.failsPush("alpha", "alpha", "main")
	w.creator.failsPush("beta", "beta", "main")
	w.creator.passes("alphacopy", "alpha", "main")
	w.creator.passes("betacopy", "beta", "main")
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	for _, name := range []string{"alphacopy", "betacopy"} {
		if copied, _ := outcomeNamed(added, name); copied.Outcome != ledger.RegistrationRefused || !strings.Contains(copied.Reason, "which the entry") {
			return Violatedf("approving reads %s as %+v, want it refused for creating what its original creates", name, copied)
		}
	}
	for _, name := range []string{"alpha", "beta"} {
		if _, err := w.repositories.SkipCreation(r.Ctx, r.Scope, w.request.ID, name, "built by hand", r.member()); err != nil {
			return err
		}
	}
	// A retry of alphacopy runs it alone, and a skipped alpha takes no forge repository: alphacopy is created.
	created := len(w.creator.Created())
	again, err := w.repositories.RetryCreation(r.Ctx, r.Scope, w.request.ID, "alphacopy", w.machine, w.creator, "shop", r.member())
	if err != nil {
		return Violatedf("a retry of alphacopy: %v", err)
	}
	if copied, _ := outcomeNamed(again, "alphacopy"); copied.Outcome != ledger.Created {
		return Violatedf("a retry of alphacopy, whose original alpha was skipped, reads it as %+v, want it created", copied)
	}
	if jobs := asked(w.creator.Created()[created:]); !slices.Equal(jobs, []string{"alphacopy"}) {
		return Violatedf("a retry of alphacopy had the creator create %v", jobs)
	}
	// A creator that cannot answer stops a retry of betacopy, which leaves the approval a problem, so the next retry registers
	// the whole version again, with alpha and beta left as the member left them, and creates betacopy.
	errNotStarted := errors.New("the worker has not started")
	w.creator.FailCheck(w.creator.key("betacopy"), errNotStarted)
	if _, err := w.repositories.RetryCreation(r.Ctx, r.Scope, w.request.ID, "betacopy", w.machine, w.creator, "shop", r.member()); !errors.Is(err, errNotStarted) {
		return Violatedf("a retry of betacopy with a creator that cannot answer ended with %v, want %v", err, errNotStarted)
	}
	w.creator.FailCheck(w.creator.key("betacopy"), nil)
	created = len(w.creator.Created())
	again, err = w.repositories.RetryCreation(r.Ctx, r.Scope, w.request.ID, "betacopy", w.machine, w.creator, "shop", r.member())
	if err != nil {
		return Violatedf("a second retry of betacopy: %v", err)
	}
	if copied, _ := outcomeNamed(again, "betacopy"); copied.Outcome != ledger.Created {
		return Violatedf("a registration of the whole version again reads betacopy, whose original beta was skipped, as %+v, want it created", copied)
	}
	if jobs := asked(w.creator.Created()[created:]); !slices.Equal(jobs, []string{"betacopy"}) {
		return Violatedf("a registration of the whole version again had the creator create %v, want betacopy alone", jobs)
	}
	for _, name := range []string{"alpha", "beta"} {
		if skipped, _ := outcomeNamed(again, name); skipped.Outcome != ledger.Skipped {
			return Violatedf("a registration of the whole version again reads %s as %+v, want it as the member left it", name, skipped)
		}
	}
	return r.CheckProjection(s)
}
