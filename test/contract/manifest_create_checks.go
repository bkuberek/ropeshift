package contract

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ManifestCreateChecks hold what approving a plan version does for an entry of its manifest that asks for its
// repository to be created (FR-REPO-16): the preview shows what approving creates and runs only the checks that come
// before a write, approving runs them again and then creates and registers the repository, and each refusal and failure
// is recorded with its remedy and the commands that remain, with nothing deleted.
var ManifestCreateChecks = []Check{
	{"RM36", "the preview of a version that targets an entry with create lists it as created, with its forge, owner, name, visibility, branch, and URL, records nothing, and asks the creator only to check", checkPreviewListsWhatApprovingCreates},
	{"RM37", "approving a version that targets an entry with create creates the repository, then registers it with the safe settings under the manifest's name, records that it started before it finished, and records the creation on the approval; its work order then passes the tick to ready", checkApprovalCreatesAndRegisters},
	{"RM38", "each refusal of a preview reads as a refusal with its remedy, and approving creates and registers none of them: a name another repository holds, a repository held or removed under its identity, two entries for one forge repository, a path that leads out of the bound, a folder that is not empty, a clone of another repository, a clone with no origin, and each refusal the creator gives", checkEachCreationRefusalIsTold},
	{"RM39", "a creation whose push fails records the failure with its steps and the commands that remain, registers nothing, keeps what it made, and leaves the next entry to run; deciding again replays the record and creates nothing", checkFailedCreationRegistersNothingAndTheNextEntryRuns},
	{"RM40", "a delegate's call and a tick create and check nothing, an entry no work order names is never checked or created, and the creator is asked only by a member's preview and a member's approval", checkOnlyAMemberAsksTheCreator},
	{"RM49", "an error from the creator stops the run as an error of the store does: the creation stays started and is not running, nothing registers, and each entry after it is not attempted", checkCreatorErrorStopsTheRun},
	{"RM50", "an entry that creates a repository where there is no creator, or no clone of the planning repository, is refused with why and nothing is checked or created", checkCreationWithNoCreatorOrNoPlanningCloneIsRefused},
	{"RM51", "a creation left started resumes from the creator's journal and is not registered from the folder it left: a run finds the clone it half made and resumes the creation", checkInterruptedCreationResumesFromTheCreator},
	{"RM52", "a creation the creator refuses after it started, as a folder that changed under it, is recorded as finished and refused right after its start, so each start has its finish: nothing registers, and the entry reads as refused with the reason", checkRefusedCreationPairsItsFinish},
	{"RM56", "the preview of an entry to create shows where its path leads, every link followed, and the creation's start records that beside the path", checkPreviewShowsWhereTheCreationLeads},
	{"RM57", "in a preview, a busy path is a refusal that says to preview again; in an approval, the check waits for the other creation and then creates", checkBusyPathIsATransientRefusal},
	{"RM58", "a worker that refuses an approval's check as busy, though the check was asked to wait, leaves no refusal on the approval: the entry is not attempted and the run stops with that error, so it can be run again", checkBusyRefusalOfAnApprovalIsNeverKept},
	{"RM53", "text a worker's refusal or step carries, with an escape, a control character, or a line break in it, is stored and returned plain and on one line: in the outcome, the creation, the approval's record, and each event", checkWorkerTextIsPlain},
	{"RM54", "a creation holds the registration from its check through its creation: a second decision of the approval waits for the first, finds its record, and asks the creator nothing", checkCreationHoldsTheRegistrationThroughCreate},
	{"RM55", "a repository that exists on the forge, as when an earlier creation timed out after the forge made it, is refused, at the check and at the creation, with the fix: clone it at the entry's path and write remote in place of create in the manifest", checkExistingRepositoryRefusalNamesTheFix},
}

// machineCreator is the fake creator over a worker's machine: a creation that writes leaves the clone it made on the
// machine, with the origin it was given, so the registration that follows reads it as it would read the real one.
type machineCreator struct {
	*fake.RepositoryCreator
	machine *fake.Checkouts
	// approval is the plan approval whose creations the creator answers, which its answers are keyed by.
	approval string
	urls     map[string]string
	checks   map[string]ledger.CreationCheck
	// unread are the entries whose check refuses before it reads where the path leads, as the worker's does, and so says
	// nothing of it.
	unread map[string]bool
	// busy are the entries whose path another creation holds until a run waits for it: a check that does not wait is refused
	// at once as busy, and one that waits is answered as the check set, once the other creation is over.
	busy map[string]bool
	// during hears each job before its creation runs, and checking hears each before its check does.
	during, checking func(ledger.RepositoryCreation)
}

func newMachineCreator(machine *fake.Checkouts, approval string) *machineCreator {
	return &machineCreator{RepositoryCreator: fake.NewRepositoryCreator(), machine: machine, approval: approval, urls: map[string]string{}, checks: map[string]ledger.CreationCheck{}, unread: map[string]bool{}, busy: map[string]bool{}}
}

// leads makes the check of the entry named answer that its path leads to a folder, as a check does that read it.
func (c *machineCreator) leads(name, folder string) {
	check := c.checks[name]
	check.Leads = folder
	c.checks[name] = check
	c.SetCheck(c.key(name), check)
}

// key is the key of the creation of the entry named, which the creator answers by.
func (c *machineCreator) key(name string) string { return ledger.CreationKey(c.approval, name) }

// failsCreate makes the creation of the entry named fail with err.
func (c *machineCreator) failsCreate(name string, err error) { c.FailCreate(c.key(name), err) }

// createdSteps are the steps of a creation that finished.
func createdSteps() []ledger.RemoteStep {
	return []ledger.RemoteStep{
		{Step: ledger.RemoteStepCreate, Outcome: ledger.RemoteStepDone}, {Step: ledger.RemoteStepOrigin, Outcome: ledger.RemoteStepDone},
		{Step: ledger.RemoteStepPush, Outcome: ledger.RemoteStepDone},
	}
}

// passes makes the check of the entry named pass, and its creation finish every step, for the forge repository
// named, with the first commit on the branch.
func (c *machineCreator) passes(name, forgeName, branch string) {
	url := "git@github.com:acme/" + forgeName + ".git"
	c.urls[name] = url
	c.checks[name] = ledger.CreationCheck{URL: url, Login: "octo", Branch: branch}
	c.SetCheck(c.key(name), c.checks[name])
	c.SetReport(c.key(name), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, Commit: "1a2b3c4", Clone: true,
		Repositories: []ledger.RepositoryReport{{Name: name, Path: ".", Commit: "1a2b3c4", Remote: url, Steps: createdSteps()}},
	})
}

// failsPush makes the creation of the entry named stop at its push.
func (c *machineCreator) failsPush(name, forgeName, branch string) {
	c.passes(name, forgeName, branch)
	steps := createdSteps()
	steps[2] = ledger.RemoteStep{Step: ledger.RemoteStepPush, Outcome: ledger.RemoteStepFailed, Detail: "denied by the forge"}
	c.SetReport(c.key(name), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, Commit: "5d6e7f8", Clone: true,
		Repositories: []ledger.RepositoryReport{{Name: name, Path: ".", Commit: "5d6e7f8", Remote: c.urls[name], Steps: steps}},
		Remaining:    []string{"git -C /work/" + name + " push --set-upstream origin " + branch},
	})
}

// refuses makes the check of the entry named refuse with a reason, what it says, and the command that fixes it.
func (c *machineCreator) refuses(name, reason, detail, remedy string) {
	c.SetCheck(c.key(name), ledger.CreationCheck{Refusal: &ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeRefused, Reason: reason, Detail: detail, Remedy: remedy}})
}

func (c *machineCreator) Check(ctx context.Context, job ledger.RepositoryCreation) (ledger.CreationCheck, error) {
	if c.checking != nil {
		c.checking(job)
	}
	check, err := c.RepositoryCreator.Check(ctx, job)
	if c.unread[job.Name] {
		check.Leads = ""
	}
	if c.busy[job.Name] && !job.Wait {
		return ledger.CreationCheck{Leads: check.Leads, Refusal: &ledger.TemplateWriteReport{
			Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedBusy, Detail: "another creation on this worker writes " + job.Path,
		}}, err
	}
	return check, err
}

func (c *machineCreator) Create(ctx context.Context, job ledger.RepositoryCreation) (ledger.TemplateWriteReport, error) {
	if c.during != nil {
		c.during(job)
	}
	report, err := c.RepositoryCreator.Create(ctx, job)
	if err == nil && report.Outcome == ledger.TemplateOutcomeWritten {
		c.machine.Place(ports.Clone{Path: job.Path, ID: "id-" + job.Name, Remote: c.urls[job.Name], DefaultBranch: "main", Directory: path.Base(job.Path)})
	}
	return report, err
}

// asked lists the names of the jobs a list holds, in order.
func asked(jobs []ledger.RepositoryCreation) []string {
	out := make([]string, len(jobs))
	for i, job := range jobs {
		out[i] = job.Name
	}
	return out
}

// creationEntry is a manifest entry that creates its repository under acme, at a path beside the planning
// repository's clone, with the lines given after it.
func creationEntry(name, at, more string) string {
	return "\n[[repository]]\nname = \"" + name + "\"\npath = \"" + at + "\"\ncreate = \"github:acme\"\n" + more
}

// creationWorld is a project whose plan version lists a manifest of api, which the project holds, and the entries
// given, and whose contract gives a work order to api and one to each repository named in targets.
type creationWorld struct {
	store        ports.Store
	machine      *fake.Checkouts
	creator      *machineCreator
	repositories *control.Repositories
	request      authority.Approval
}

func (r *Rig) creationWorld(s ports.Store, entries string, targets []string, more map[string]string) (*creationWorld, error) {
	files, err := r.manifestProject(s)
	if err != nil {
		return nil, err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return nil, err
	}
	orders := []orderIn{{ID: "core-api", Repo: "api"}}
	for _, name := range targets {
		orders = append(orders, orderIn{ID: "core-" + name, Repo: name})
	}
	files.Write("repositories.md", []byte(manifestText(entries, "api")))
	files.Write("core.md", []byte(placedContract{ID: "core", Plan: manifestPlan, Orders: orders}.text()))
	for name, text := range more {
		files.Write(name, []byte(text))
	}
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

// run reads what approving the version would register, or registers it, as the member, with the world's creator.
func (r *Rig) runCreation(w *creationWorld, dryRun bool) ([]control.Added, error) {
	return w.repositories.RegisterApproved(r.Ctx, r.Scope, w.request.ID, w.machine, w.creator, "shop", dryRun, r.member())
}

// creationEvents lists the events about an approval that record a creation, in the order they were recorded.
func (r *Rig) creationEvents(s ports.Store, approval string) ([]ledger.Event, error) {
	var out []ledger.Event
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: authority.SubjectApproval, ID: approval})
		for _, event := range events {
			if strings.HasPrefix(event.Type, "approval.repository_creation_") {
				out = append(out, event)
			}
		}
		return err
	})
	return out, err
}

// creationTypes lists the types of the creation events about an approval, each with the entry it is about.
func (r *Rig) creationTypes(s ports.Store, approval string) ([]string, error) {
	events, err := r.creationEvents(s, approval)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, event := range events {
		var data struct {
			Name string `json:"name"`
		}
		if err := jsonInto(event.Data, &data); err != nil {
			return nil, err
		}
		out = append(out, strings.TrimPrefix(event.Type, "approval.repository_creation_")+" "+data.Name)
	}
	return out, nil
}

func checkPreviewListsWhatApprovingCreates(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", "")+creationEntry("site", "../site", "visibility = \"public\"\ndefault_branch = \"trunk\"\n"), []string{"search", "site"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("search", "search", "main")
	w.creator.passes("site", "site", "trunk")
	preview, err := r.runCreation(w, true)
	if err != nil {
		return err
	}
	if err := outcomesAre(preview, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Created, path: "/work/search"},
		"site":   {outcome: ledger.Created, path: "/work/site"},
	}); err != nil {
		return Violatedf("the preview: %v", err)
	}
	search, _ := outcomeNamed(preview, "search")
	site, _ := outcomeNamed(preview, "site")
	wantSearch := ledger.CreationOutcome{Forge: "github", Owner: "acme", ForgeName: "search", Visibility: "private", DefaultBranch: "main", URL: "git@github.com:acme/search.git", Login: "octo"}
	wantSite := ledger.CreationOutcome{Forge: "github", Owner: "acme", ForgeName: "site", Visibility: "public", DefaultBranch: "trunk", URL: "git@github.com:acme/site.git", Login: "octo"}
	if search.Creation == nil || !reflect.DeepEqual(*search.Creation, wantSearch) || search.Remote != wantSearch.URL {
		return Violatedf("search would be created as %+v, remote %q, want %+v", search.Creation, search.Remote, wantSearch)
	}
	if site.Creation == nil || !reflect.DeepEqual(*site.Creation, wantSite) || site.Remote != wantSite.URL {
		return Violatedf("site would be created as %+v, remote %q, want %+v", site.Creation, site.Remote, wantSite)
	}
	if created := w.creator.Created(); len(created) != 0 {
		return Violatedf("a preview created %v", asked(created))
	}
	checked := w.creator.Checked()
	if !slices.Equal(asked(checked), []string{"search", "site"}) {
		return Violatedf("a preview asked the creator to check %v, want search and site", asked(checked))
	}
	wantJob := ledger.RepositoryCreation{
		Key: ledger.CreationKey(w.request.ID, "search"), Project: "shop", Name: "search", Path: "/work/search", Bound: "/work", PlanningClone: "/work/plan",
		Create: ledger.RemoteCreate{Forge: "github", Owner: "acme", Visibility: "private"}, ForgeName: "search",
	}
	if !reflect.DeepEqual(checked[0], wantJob) {
		return Violatedf("the job the creator checked is %+v, want %+v", checked[0], wantJob)
	}
	if public := checked[1]; public.Create.Visibility != "public" || public.DefaultBranch != "trunk" || public.ForgeName != "site" {
		return Violatedf("the job of the public entry is %+v", public)
	}
	if record, err := r.registrationOf(s, w.request.ID); err != nil || record != nil {
		return Violatedf("a preview recorded %+v: %v", record, err)
	}
	if types, err := r.creationTypes(s, w.request.ID); err != nil || len(types) != 0 {
		return Violatedf("a preview recorded the creation events %v: %v", types, err)
	}
	if found, err := w.repositories.Resolve(r.Ctx, r.Scope, "search"); err == nil {
		return Violatedf("a preview registered %+v", found)
	}
	if held, _ := w.machine.Of(r.Ctx, r.Scope); slices.Contains(slices.Collect(maps.Values(held)), "/work/search") {
		return Violatedf("a preview kept a checkout of search: %v", held)
	}
	return r.CheckProjection(s)
}

func checkApprovalCreatesAndRegisters(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", ""), []string{"search"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("search", "search", "main")
	var whileRunning struct {
		running bool
		events  []string
	}
	w.creator.during = func(job ledger.RepositoryCreation) {
		whileRunning.running = w.repositories.Creating(w.request.ID, job.Name)
		whileRunning.events, _ = r.creationTypes(s, w.request.ID)
	}
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	if order, err := r.orderByTitle(s, "The work order core-search"); err != nil || order.State != workorders.ContractReview {
		return Violatedf("before the repository is registered the work order is %+v: %v", order, err)
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	if err := outcomesAre(added, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Created, path: "/work/search"},
	}); err != nil {
		return Violatedf("approving it: %v", err)
	}
	search, _ := outcomeNamed(added, "search")
	if search.Repository.Name != "search" || search.Checkout != "/work/search" || search.Repository.Remote != "git@github.com:acme/search.git" {
		return Violatedf("the created repository is registered as %+v", search)
	}
	if record := search.Repository; record.Target() != ledger.ReviewOnBranch || record.Level() != ledger.PersonMerges || record.StartFrom != ledger.StartFromMerged || record.MergeMethod != ledger.MergeCommit {
		return Violatedf("the created repository is registered with settings %+v", record)
	}
	creation := search.Creation
	if creation == nil || creation.Makefile != "search=git@github.com:acme/search.git=../search" || creation.Commit != "1a2b3c4" || len(creation.Steps) != 3 || creation.URL != "git@github.com:acme/search.git" || creation.Login != "octo" {
		return Violatedf("the creation reads %+v", creation)
	}
	if !whileRunning.running || !slices.Equal(whileRunning.events, []string{"started search"}) {
		return Violatedf("while the creator ran, the creation read as running %v after the events %v", whileRunning.running, whileRunning.events)
	}
	if w.repositories.Creating(w.request.ID, "search") {
		return Violatedf("a creation that finished still reads as running")
	}
	events, err := r.creationEvents(s, w.request.ID)
	if err != nil || len(events) != 2 || events[0].Type != ledger.EventRepositoryCreationStarted || events[1].Type != ledger.EventRepositoryCreationFinished || events[0].Rev >= events[1].Rev {
		return Violatedf("the creation events are %+v: %v", events, err)
	}
	if actor := events[1].Actor; actor != r.member() {
		return Violatedf("the creation finished as %+v, not the member who approved", actor)
	}
	var started ledger.CreationStartedEvent
	var finished ledger.CreationFinishedEvent
	if err := jsonInto(events[0].Data, &started); err != nil {
		return err
	}
	if err := jsonInto(events[1].Data, &finished); err != nil {
		return err
	}
	if started.Name != "search" || started.Key != ledger.CreationKey(w.request.ID, "search") || started.ManifestPath != "../search" || started.Path != "/work/search" ||
		started.Forge != "github" || started.Owner != "acme" || started.ForgeName != "search" || started.Visibility != "private" {
		return Violatedf("the creation started as %+v", started)
	}
	if finished.Outcome != ledger.CreationCreated || finished.Key != started.Key || finished.URL != "git@github.com:acme/search.git" || len(finished.Steps) != 3 || finished.Makefile == "" {
		return Violatedf("the creation finished as %+v", finished)
	}
	if err := r.recordedAre(s, w.request.ID, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Created, path: "/work/search"},
	}); err != nil {
		return err
	}
	record, err := r.registrationOf(s, w.request.ID)
	if err != nil || record == nil {
		return Violatedf("the approval recorded %+v: %v", record, err)
	}
	at := slices.IndexFunc(record.Repositories, func(o ledger.ManifestOutcome) bool { return o.Name == "search" })
	if at < 0 || record.Repositories[at].Creation == nil || record.Repositories[at].Creation.Makefile != creation.Makefile {
		return Violatedf("the approval recorded %+v", record)
	}
	if _, err := r.NewOps(s).Promote(r.Ctx, r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}); err != nil {
		return err
	}
	if order, err := r.orderByTitle(s, "The work order core-search"); err != nil || order.State != workorders.Ready || order.ReviewedBy != r.member().ID {
		return Violatedf("after the repository is registered the work order is %+v: %v", order, err)
	}
	return r.CheckProjection(s)
}

func checkEachCreationRefusalIsTold(r *Rig) error {
	s := r.Store()
	names := []string{"taken", "dup", "revived", "twin-a", "nonempty", "leaky", "foreign", "bare", "signedout", "exists", "owner", "outside", "slow"}
	entries := ""
	for _, name := range names {
		path := "../" + name
		if name == "twin-a" {
			path = "../twin"
		}
		entries += creationEntry(name, path, "")
	}
	more := map[string]string{
		"plan.md": "+++\nkind = \"plan\"\nid = \"" + manifestPlan + "\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\", \"repositories:second\"]\n+++\n\nOne plan for every repository.\n",
		"more.md": "+++\nkind = \"repositories\"\nid = \"second\"\nplan = \"" + manifestPlan + "\"\n" + creationEntry("twin-b", "../alt/twin", "") + "+++\n\nMore.\n",
	}
	// The project holds taken as another repository, dup under another name, and removed revived's remote.
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}
	repositories := r.repositories(s)
	for _, held := range []control.DetectedRepository{
		{ID: "repo-taken", Remote: "git@github.com:other/taken.git", DefaultBranch: "main", Directory: "taken"},
		{ID: "repo-old", Remote: "git@github.com:acme/dup.git", DefaultBranch: "main", Directory: "old-name"},
		{ID: "repo-legacy", Remote: "https://github.com/acme/revived.git", DefaultBranch: "main", Directory: "legacy"},
	} {
		if _, err := repositories.Detect(r.Ctx, r.Scope, held, system); err != nil {
			return err
		}
	}
	w, err := r.creationWorld(s, entries, append(slices.Clone(names), "twin-b"), more)
	if err != nil {
		return err
	}
	if _, err := w.repositories.Remove(r.Ctx, r.Scope, "repo-legacy", w.machine, r.member()); err != nil {
		return err
	}
	w.machine.Link("/work/leaky", "/elsewhere/leaky")
	w.machine.Place(ports.Clone{Path: "/work/foreign", ID: "id-foreign", Remote: "git@github.com:other/foreign.git", DefaultBranch: "main"})
	w.machine.Place(ports.Clone{Path: "/work/bare", ID: "id-bare", DefaultBranch: "main"})
	w.creator.passes("twin-a", "twin", "main")
	w.creator.refuses("nonempty", ledger.TemplateRefusedNotEmpty, "/work/nonempty holds files of its own", "")
	w.creator.refuses("signedout", ledger.TemplateRefusedGHSignedOut, "Run `gh auth login --hostname github.com` on this machine", "gh auth login --hostname github.com")
	w.creator.refuses("exists", ledger.TemplateRefusedRemoteExists, "acme/exists already exists on GitHub; clone it to ../exists and write `remote` in place of `create`", "")
	w.creator.refuses("owner", ledger.TemplateRefusedOwner, "acme is neither the signed-in account nor one of its organizations; the owners available are octo", "")
	w.creator.refuses("outside", ledger.TemplateRefusedOutsideBound, "/work/outside leads, through a link of a folder above it, to /elsewhere/outside, outside /work", "")
	w.creator.refuses("slow", ledger.TemplateRefusedGHTimeout, "GitHub did not answer within 30 seconds; read the preview again", "")
	want := map[string]entryOutcome{
		"api":       {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"taken":     {outcome: ledger.RegistrationRefused, path: "/work/taken", reason: "its name is held by another repository"},
		"dup":       {outcome: ledger.RegistrationRefused, path: "/work/dup", reason: "old-name"},
		"revived":   {outcome: ledger.RegistrationRefused, path: "/work/revived", reason: "was removed from this project"},
		"twin-a":    {outcome: ledger.Created, path: "/work/twin"},
		"twin-b":    {outcome: ledger.RegistrationRefused, path: "/work/alt/twin", reason: "twin-a"},
		"nonempty":  {outcome: ledger.RegistrationRefused, path: "/work/nonempty", reason: "holds files of its own", code: ledger.TemplateRefusedNotEmpty},
		"leaky":     {outcome: ledger.RegistrationRefused, path: "/elsewhere/leaky", reason: "outside /work"},
		"foreign":   {outcome: ledger.RegistrationRefused, path: "/work/foreign", reason: "github.com/other/foreign"},
		"bare":      {outcome: ledger.RegistrationRefused, path: "/work/bare", reason: "no origin"},
		"signedout": {outcome: ledger.RegistrationRefused, path: "/work/signedout", reason: "gh auth login --hostname github.com", code: ledger.TemplateRefusedGHSignedOut},
		"exists":    {outcome: ledger.RegistrationRefused, path: "/work/exists", reason: "already exists on GitHub", code: ledger.TemplateRefusedRemoteExists},
		"owner":     {outcome: ledger.RegistrationRefused, path: "/work/owner", reason: "neither the signed-in account", code: ledger.TemplateRefusedOwner},
		"outside":   {outcome: ledger.RegistrationRefused, path: "/work/outside", reason: ledger.TemplateRefusedOutsideBound + ": /work/outside leads", code: ledger.TemplateRefusedOutsideBound},
		"slow":      {outcome: ledger.RegistrationRefused, path: "/work/slow", reason: ledger.TemplateRefusedGHTimeout + ": GitHub did not answer within 30 seconds", code: ledger.TemplateRefusedGHTimeout},
	}
	preview, err := r.runCreation(w, true)
	if err != nil {
		return err
	}
	if err := outcomesAre(preview, want); err != nil {
		return Violatedf("the preview: %v", err)
	}
	if signedOut, _ := outcomeNamed(preview, "signedout"); signedOut.Creation == nil || signedOut.Creation.Remedy != "gh auth login --hostname github.com" || signedOut.Creation.Owner != "acme" || signedOut.Creation.ForgeName != "signedout" {
		return Violatedf("the refusal of the signed-out entry carries %+v", signedOut.Creation)
	}
	if checked := asked(w.creator.Checked()); !slices.Equal(checked, []string{"twin-a", "nonempty", "signedout", "exists", "owner", "outside", "slow"}) {
		return Violatedf("the creator was asked to check %v, want only the entries no earlier rule refused", checked)
	}
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	if err := outcomesAre(added, want); err != nil {
		return Violatedf("approving it: %v", err)
	}
	if created := asked(w.creator.Created()); !slices.Equal(created, []string{"twin-a"}) {
		return Violatedf("approving created %v, want only twin-a", created)
	}
	if types, err := r.creationTypes(s, w.request.ID); err != nil || !slices.Equal(types, []string{"started twin-a", "finished twin-a"}) {
		return Violatedf("the creation events are %v: %v", types, err)
	}
	for _, name := range []string{"taken", "dup", "revived", "twin-b", "nonempty", "leaky", "foreign", "bare", "signedout", "exists", "owner", "outside", "slow"} {
		if found, err := w.repositories.Resolve(r.Ctx, r.Scope, name); err == nil && found.Remote != "git@github.com:other/taken.git" && found.Remote != "git@github.com:acme/dup.git" {
			return Violatedf("approving registered the refused entry %s as %+v", name, found)
		}
	}
	if err := r.recordedAre(s, w.request.ID, want); err != nil {
		return err
	}
	return r.CheckProjection(s)
}

func checkFailedCreationRegistersNothingAndTheNextEntryRuns(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", "")+creationEntry("site", "../site", ""), []string{"search", "site"}, nil)
	if err != nil {
		return err
	}
	w.creator.failsPush("search", "search", "main")
	w.creator.passes("site", "site", "main")
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	if err := outcomesAre(added, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.CreateFailed, path: "/work/search", reason: "denied by the forge"},
		"site":   {outcome: ledger.Created, path: "/work/site"},
	}); err != nil {
		return Violatedf("approving it: %v", err)
	}
	search, _ := outcomeNamed(added, "search")
	if creation := search.Creation; creation == nil || creation.Commit != "5d6e7f8" || len(creation.Steps) != 3 || creation.Steps[2].Outcome != ledger.RemoteStepFailed ||
		!slices.Equal(creation.Remaining, []string{"git -C /work/search push --set-upstream origin main"}) || creation.Makefile != "" || creation.URL != "git@github.com:acme/search.git" {
		return Violatedf("the failed creation reads %+v", search.Creation)
	}
	if search.Repository.ID != "" || search.Checkout != "" {
		return Violatedf("a creation that failed registered %+v", search)
	}
	if found, err := w.repositories.Resolve(r.Ctx, r.Scope, "search"); err == nil {
		return Violatedf("a creation that failed registered %+v", found)
	}
	if site, _ := outcomeNamed(added, "site"); site.Repository.Name != "site" || site.Creation == nil || site.Creation.Makefile == "" {
		return Violatedf("the entry after the failed one reads %+v", site)
	}
	events, err := r.creationEvents(s, w.request.ID)
	if err != nil || len(events) != 4 {
		return Violatedf("the creation events are %+v: %v", events, err)
	}
	var failed ledger.CreationFinishedEvent
	if err := jsonInto(events[1].Data, &failed); err != nil {
		return err
	}
	if failed.Name != "search" || failed.Outcome != ledger.CreationFailed || !strings.Contains(failed.Reason, "denied by the forge") || len(failed.Remaining) != 1 {
		return Violatedf("the failed creation finished as %+v", failed)
	}
	if w.repositories.Creating(w.request.ID, "search") || w.repositories.Creating(w.request.ID, "site") {
		return Violatedf("a creation that finished reads as running")
	}
	if err := r.recordedAre(s, w.request.ID, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.CreateFailed, path: "/work/search", reason: "denied by the forge"},
		"site":   {outcome: ledger.Created, path: "/work/site"},
	}); err != nil {
		return err
	}
	// Deciding again reads the record, and checks and creates nothing.
	checked, created, types := len(w.creator.Checked()), len(w.creator.Created()), len(events)
	again, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	if err := outcomesAre(again, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.CreateFailed, path: "/work/search", reason: "denied by the forge"},
		"site":   {outcome: ledger.Created, path: "/work/site"},
	}); err != nil {
		return Violatedf("deciding again: %v", err)
	}
	after, err := r.creationEvents(s, w.request.ID)
	if err != nil || len(w.creator.Checked()) != checked || len(w.creator.Created()) != created || len(after) != types {
		return Violatedf("deciding again checked %d, created %d, and recorded %d events more: %v", len(w.creator.Checked())-checked, len(w.creator.Created())-created, len(after)-types, err)
	}
	if recorded, err := r.registrationEvents(s, w.request.ID); err != nil || recorded != 1 {
		return Violatedf("the approval recorded what it registered %d times: %v", recorded, err)
	}
	return r.CheckProjection(s)
}

func checkOnlyAMemberAsksTheCreator(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", "")+creationEntry("docs", "../docs", ""), []string{"search"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("search", "search", "main")
	w.creator.passes("docs", "docs", "main")
	delegate := ledger.Actor{Kind: ledger.ActorDelegate, ID: "member-1/assistant"}
	tick := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}
	for _, actor := range []ledger.Actor{delegate, tick} {
		for _, dryRun := range []bool{true, false} {
			if _, err := w.repositories.RegisterApproved(r.Ctx, r.Scope, w.request.ID, w.machine, w.creator, "shop", dryRun, actor); !errors.Is(err, workorders.ErrForbidden) {
				return Violatedf("%s %v registering with a creator (dry run %v): %v", actor.Kind, actor.ID, dryRun, err)
			}
		}
	}
	if checked, created := w.creator.Checked(), w.creator.Created(); len(checked) != 0 || len(created) != 0 {
		return Violatedf("a delegate and a tick had %v checked and %v created", asked(checked), asked(created))
	}
	preview, err := r.runCreation(w, true)
	if err != nil {
		return err
	}
	if checked := asked(w.creator.Checked()); !slices.Equal(checked, []string{"search"}) {
		return Violatedf("the member's preview checked %v, want only search, which a work order names", checked)
	}
	if docs, _ := outcomeNamed(preview, "docs"); docs.Outcome != ledger.Planned || docs.Creation != nil {
		return Violatedf("docs, which no work order names, reads %+v", docs)
	}
	// A delegate's call after the member's approval creates nothing either.
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	if _, err := w.repositories.RegisterApproved(r.Ctx, r.Scope, w.request.ID, w.machine, w.creator, "shop", false, delegate); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate registering an approved version: %v", err)
	}
	if created := w.creator.Created(); len(created) != 0 {
		return Violatedf("a delegate had %v created", asked(created))
	}
	if _, err := r.runCreation(w, false); err != nil {
		return err
	}
	if created := asked(w.creator.Created()); !slices.Equal(created, []string{"search"}) {
		return Violatedf("the member's approval created %v, want only search", created)
	}
	if checked := asked(w.creator.Checked()); !slices.Equal(checked, []string{"search", "search"}) {
		return Violatedf("the creator was asked to check %v, want search for the preview and for the approval", checked)
	}
	// What the approval recorded is read without the creator, by a delegate's read of the record too.
	if record, err := r.registrationOf(s, w.request.ID); err != nil || record == nil {
		return Violatedf("the approval recorded %+v: %v", record, err)
	}
	if checked := len(w.creator.Checked()); checked != 2 {
		return Violatedf("reading the record asked the creator to check: %d checks", checked)
	}
	return r.CheckProjection(s)
}

func checkCreatorErrorStopsTheRun(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", "")+creationEntry("site", "../site", ""), []string{"search", "site"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("search", "search", "main")
	w.creator.passes("site", "site", "main")
	w.creator.failsCreate("search", errors.New("the worker's machine is gone"))
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err == nil || !strings.Contains(err.Error(), "the worker's machine is gone") {
		return Violatedf("a creator that failed stopped the run with %v", err)
	}
	if search, _ := outcomeNamed(added, "search"); search.Outcome != ledger.RegistrationRefused || search.Repository.ID != "" {
		return Violatedf("the entry the creator failed on reads %+v", search)
	}
	if site, _ := outcomeNamed(added, "site"); site.Outcome != ledger.NotAttempted {
		return Violatedf("the entry after the failure reads %+v", site)
	}
	if types, err := r.creationTypes(s, w.request.ID); err != nil || !slices.Equal(types, []string{"started search"}) {
		return Violatedf("the creation events are %v: %v", types, err)
	}
	if w.repositories.Creating(w.request.ID, "search") {
		return Violatedf("a creation the creator failed on still reads as running")
	}
	if len(w.creator.Created()) != 1 {
		return Violatedf("the creator was asked to create %v", asked(w.creator.Created()))
	}
	if record, err := r.registrationOf(s, w.request.ID); err != nil || record == nil || !strings.Contains(record.Problem, "the worker's machine is gone") {
		return Violatedf("the approval recorded %+v: %v", record, err)
	}
	return r.CheckProjection(s)
}

func checkCreationWithNoCreatorOrNoPlanningCloneIsRefused(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", ""), []string{"search"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("search", "search", "main")
	preview, err := w.repositories.RegisterApproved(r.Ctx, r.Scope, w.request.ID, w.machine, nil, "shop", true, r.member())
	if err != nil {
		return err
	}
	if search, _ := outcomeNamed(preview, "search"); search.Outcome != ledger.RegistrationRefused || search.Path != "/work/search" || !strings.Contains(search.Reason, "creates no repositories") || search.Creation != nil {
		return Violatedf("an entry to create, with no creator, reads %+v", search)
	}
	bare := fake.NewCheckouts()
	preview, err = w.repositories.RegisterApproved(r.Ctx, r.Scope, w.request.ID, bare, w.creator, "shop", true, r.member())
	if err != nil {
		return err
	}
	if search, _ := outcomeNamed(preview, "search"); search.Outcome != ledger.RegistrationRefused || !strings.Contains(search.Reason, "planning repository") {
		return Violatedf("an entry to create, on a machine with no planning clone, reads %+v", search)
	}
	if checked, created := w.creator.Checked(), w.creator.Created(); len(checked) != 0 || len(created) != 0 {
		return Violatedf("a run with no creator or no planning clone had %v checked and %v created", asked(checked), asked(created))
	}
	return nil
}

func checkInterruptedCreationResumesFromTheCreator(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", ""), []string{"search"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("search", "search", "main")
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	// A creation the daemon's stop left started: its folder is a clone with the origin it had added, and no push.
	w.machine.Place(ports.Clone{Path: "/work/search", ID: "id-search", Remote: "git@github.com:acme/search.git", DefaultBranch: "main"})
	err = s.Write(r.Ctx, r.Scope, "test.creation_started", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryCreationStarted, Actor: r.member(), Subject: ledger.Subject{Kind: authority.SubjectApproval, ID: w.request.ID},
			Data: mustJSON(ledger.CreationStartedEvent{Name: "search", Key: ledger.CreationKey(w.request.ID, "search"), ManifestPath: "../search", Path: "/work/search", Forge: "github", Owner: "acme", ForgeName: "search"}),
		})
		return err
	})
	if err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	if search, _ := outcomeNamed(added, "search"); search.Outcome != ledger.Created || search.Creation == nil || search.Creation.Makefile == "" {
		return Violatedf("the creation the stop left reads %+v, want it created from the creator", search)
	}
	if created := asked(w.creator.Created()); !slices.Equal(created, []string{"search"}) {
		return Violatedf("the creator was asked to create %v, want the creation left started to go on", created)
	}
	if types, err := r.creationTypes(s, w.request.ID); err != nil || !slices.Equal(types, []string{"started search", "started search", "finished search"}) {
		return Violatedf("the creation events are %v: %v", types, err)
	}
	return r.CheckProjection(s)
}

// jsonInto reads the data of an event.
func jsonInto(raw json.RawMessage, into any) error { return json.Unmarshal(raw, into) }

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func checkRefusedCreationPairsItsFinish(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", "")+creationEntry("site", "../site", ""), []string{"search", "site"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("search", "search", "main")
	w.creator.passes("site", "site", "main")
	// The creator's checks pass, and the creation then finds the folder is no longer the one it checked.
	w.creator.SetReport(w.creator.key("search"), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedChanged, Detail: "the folder was swapped for a link after it was checked",
	})
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	if err := outcomesAre(added, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.RegistrationRefused, path: "/work/search", reason: ledger.TemplateRefusedChanged + ": the folder was swapped for a link", code: ledger.TemplateRefusedChanged},
		"site":   {outcome: ledger.Created, path: "/work/site"},
	}); err != nil {
		return Violatedf("approving it: %v", err)
	}
	if found, err := w.repositories.Resolve(r.Ctx, r.Scope, "search"); err == nil {
		return Violatedf("a creation the creator refused registered %+v", found)
	}
	events, err := r.creationEvents(s, w.request.ID)
	if err != nil || len(events) != 4 {
		return Violatedf("the creation events are %+v: %v", events, err)
	}
	var started ledger.CreationStartedEvent
	var finished ledger.CreationFinishedEvent
	if err := jsonInto(events[0].Data, &started); err != nil {
		return err
	}
	if err := jsonInto(events[1].Data, &finished); err != nil {
		return err
	}
	if events[0].Type != ledger.EventRepositoryCreationStarted || events[1].Type != ledger.EventRepositoryCreationFinished || events[0].Rev >= events[1].Rev ||
		started.Name != "search" || finished.Name != "search" || finished.Key != started.Key {
		return Violatedf("the refused creation did not pair its start and its finish: %+v then %+v", events[0].Type, events[1].Type)
	}
	if finished.Outcome != ledger.CreationRefused || !strings.Contains(finished.Reason, ledger.TemplateRefusedChanged) || finished.RefusalCode != ledger.TemplateRefusedChanged || len(finished.Steps) != 0 {
		return Violatedf("the refused creation finished as %+v", finished)
	}
	if latest, err := r.latestCreation(s, w.request.ID, "search"); err != nil || latest.State != ledger.CreationRefused {
		return Violatedf("the events leave the creation %+v: %v", latest, err)
	}
	if w.repositories.Creating(w.request.ID, "search") {
		return Violatedf("a creation the creator refused reads as running")
	}
	return r.CheckProjection(s)
}

// latestCreation reads where the events about an approval leave the creation of an entry.
func (r *Rig) latestCreation(s ports.Store, approval, name string) (ledger.CreationRecord, error) {
	var latest ledger.CreationRecord
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: authority.SubjectApproval, ID: approval})
		if err != nil {
			return err
		}
		latest, err = ledger.LatestCreation(events, name)
		return err
	})
	return latest, err
}

// hostile is text a tool printed that a terminal or a page must not take as its own: an escape sequence that sets a title
// and one that colors, a line break, a bell, a NUL, and a tab.
const hostile = "Run \x1b[31mgh auth login\x1b[0m\non this\x07 machine\x00 now\x1b]0;title\x07\t!"

// hostilePath is a path a worker read from a disk that carries an escape, a line break, a bell, and a tab, and
// cleanPath is it as it is kept: the line break a space, the rest dropped, and nothing else changed.
const (
	hostilePath = "/mnt/disk/evil\x1b[31m\n/dir\x07\tend"
	cleanPath   = "/mnt/disk/evil /dirend"
)

// hostileLine is hostile as it is kept: on one line, with no escape or control character.
const hostileLine = "Run gh auth login on this machine now !"

// plainEverywhere checks that no string a value holds, as it is written, carries a control character or an escape.
func plainEverywhere(label string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var walk func(any) error
	walk = func(v any) error {
		switch v := v.(type) {
		case string:
			if strings.ContainsFunc(v, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }) {
				return Violatedf("%s carries %q", label, v)
			}
		case []any:
			for _, item := range v {
				if err := walk(item); err != nil {
					return err
				}
			}
		case map[string]any:
			for _, item := range v {
				if err := walk(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(decoded)
}

func checkWorkerTextIsPlain(r *Rig) error {
	s := r.Store()
	names := []string{"dirty", "late", "broken", "nocommit"}
	entries := ""
	for _, name := range names {
		entries += creationEntry(name, "../"+name, "")
	}
	w, err := r.creationWorld(s, entries, names, nil)
	if err != nil {
		return err
	}
	for _, name := range names {
		w.creator.passes(name, name, "main")
		w.creator.leads(name, hostilePath)
	}
	// The check of dirty refuses with text from a disk, and the creation of late refuses after it started.
	w.creator.SetCheck(w.creator.key("dirty"), ledger.CreationCheck{Leads: hostilePath, Refusal: &ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedGHSignedOut, Detail: hostile, Remedy: "gh auth login\n--hostname\x1b[2J github.com\x07",
	}})
	w.creator.SetReport(w.creator.key("late"), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedChanged, Detail: hostile, Remedy: "git -C /work/late\nstatus\x1b[1m",
	})
	w.creator.SetReport(w.creator.key("broken"), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, Commit: "5d6e7f8", Clone: true,
		Repositories: []ledger.RepositoryReport{{Name: "broken", Path: ".", Commit: "5d6e7f8", Remote: w.creator.urls["broken"], Steps: []ledger.RemoteStep{
			{Step: ledger.RemoteStepCreate, Outcome: ledger.RemoteStepDone}, {Step: ledger.RemoteStepOrigin, Outcome: ledger.RemoteStepDone},
			{Step: ledger.RemoteStepPush, Outcome: ledger.RemoteStepFailed, Detail: hostile},
		}}},
		Remaining: []string{"git -C /work/broken push\x1b[1m\n--set-upstream origin main"},
	})
	w.creator.SetReport(w.creator.key("nocommit"), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, CommitError: hostile,
		Repositories: []ledger.RepositoryReport{{Name: "nocommit", Path: ".", CommitError: hostile, Steps: []ledger.RemoteStep{
			{Step: ledger.RemoteStepCreate, Outcome: ledger.RemoteStepSkipped, Detail: "the repository has no first commit to push\x07"},
		}}},
	})
	preview, err := r.runCreation(w, true)
	if err != nil {
		return err
	}
	dirty, _ := outcomeNamed(preview, "dirty")
	if dirty.Outcome != ledger.RegistrationRefused || !strings.Contains(dirty.Reason, hostileLine) || dirty.Creation == nil || dirty.Creation.Remedy != "gh auth login --hostname github.com" || dirty.Path != cleanPath {
		return Violatedf("the refused check reads %+v with creation %+v", dirty, dirty.Creation)
	}
	if err := plainEverywhere("the preview", preview); err != nil {
		return err
	}
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	for name, want := range map[string]ledger.RepositoryOutcome{"dirty": ledger.RegistrationRefused, "late": ledger.RegistrationRefused, "broken": ledger.CreateFailed, "nocommit": ledger.CreateFailed} {
		if got, _ := outcomeNamed(added, name); got.Outcome != want || got.Path != cleanPath {
			return Violatedf("%s reads %+v, want %s at %q", name, got, want, cleanPath)
		}
	}
	if late, _ := outcomeNamed(added, "late"); !strings.Contains(late.Reason, hostileLine) || late.Creation == nil || late.Creation.Remedy != "git -C /work/late status" {
		return Violatedf("the creation refused after it started reads %+v with creation %+v", late, late.Creation)
	}
	broken, _ := outcomeNamed(added, "broken")
	if broken.Creation == nil || broken.Creation.Steps[2].Detail != hostileLine || !slices.Equal(broken.Creation.Remaining, []string{"git -C /work/broken push --set-upstream origin main"}) || !strings.Contains(broken.Reason, hostileLine) {
		return Violatedf("the creation that failed reads %+v with creation %+v", broken, broken.Creation)
	}
	if nocommit, _ := outcomeNamed(added, "nocommit"); nocommit.Creation == nil || nocommit.Creation.CommitError != hostileLine || !strings.Contains(nocommit.Reason, hostileLine) {
		return Violatedf("the creation with no first commit reads %+v with creation %+v", nocommit, nocommit.Creation)
	}
	if err := plainEverywhere("the outcomes of the run", added); err != nil {
		return err
	}
	record, err := r.registrationOf(s, w.request.ID)
	if err != nil || record == nil {
		return Violatedf("the approval recorded %+v: %v", record, err)
	}
	if err := plainEverywhere("the approval's record", record); err != nil {
		return err
	}
	events, err := r.creationEvents(s, w.request.ID)
	if err != nil || len(events) == 0 {
		return Violatedf("the creation events are %+v: %v", events, err)
	}
	var started ledger.CreationStartedEvent
	if err := jsonInto(events[0].Data, &started); err != nil || started.Leads != cleanPath {
		return Violatedf("the creation started as %+v, want where its path leads as %q: %v", started, cleanPath, err)
	}
	for _, event := range events {
		var data any
		if err := jsonInto(event.Data, &data); err != nil {
			return err
		}
		if err := plainEverywhere("the event "+event.Type, data); err != nil {
			return err
		}
	}
	return r.CheckProjection(s)
}

func checkCreationHoldsTheRegistrationThroughCreate(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", ""), []string{"search"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("search", "search", "main")
	var once sync.Once
	entered, release := make(chan struct{}), make(chan struct{})
	w.creator.during = func(ledger.RepositoryCreation) {
		once.Do(func() { close(entered) })
		<-release
	}
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	type result struct {
		added []control.Added
		err   error
	}
	run := func() chan result {
		done := make(chan result, 1)
		go func() {
			added, err := r.runCreation(w, false)
			done <- result{added, err}
		}()
		return done
	}
	first := run()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		return Violatedf("the creator was never asked to create")
	}
	// The first decision has checked, recorded its start, and is inside the creation. A second decision of the same
	// approval must wait for it, so it asks the creator nothing while the first is between its check and its end.
	second := run()
	var held error
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline) && held == nil; time.Sleep(10 * time.Millisecond) {
		if checked, created := w.creator.Checked(), w.creator.Created(); len(checked) != 1 || len(created) != 0 {
			held = Violatedf("a second decision, while the first was creating, had the creator check %v and create %v", asked(checked), asked(created))
		}
	}
	close(release)
	a, b := <-first, <-second
	if held != nil {
		return held
	}
	if a.err != nil || b.err != nil {
		return Violatedf("the decisions ended with %v and %v", a.err, b.err)
	}
	for which, got := range map[string]result{"first": a, "second": b} {
		if search, _ := outcomeNamed(got.added, "search"); search.Outcome != ledger.Created || search.Repository.Name != "search" {
			return Violatedf("the %s decision reads search as %+v", which, search)
		}
	}
	if checked, created := asked(w.creator.Checked()), asked(w.creator.Created()); !slices.Equal(checked, []string{"search"}) || !slices.Equal(created, []string{"search"}) {
		return Violatedf("the creator was asked to check %v and create %v, want search once each", checked, created)
	}
	if types, err := r.creationTypes(s, w.request.ID); err != nil || !slices.Equal(types, []string{"started search", "finished search"}) {
		return Violatedf("the creation events are %v: %v", types, err)
	}
	if recorded, err := r.registrationEvents(s, w.request.ID); err != nil || recorded != 1 {
		return Violatedf("the approval recorded what it registered %d times: %v", recorded, err)
	}
	return r.CheckProjection(s)
}

func checkExistingRepositoryRefusalNamesTheFix(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("exists", "../exists", "")+creationEntry("late", "../late", "")+creationEntry("signedout", "../signedout", ""), []string{"exists", "late", "signedout"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("late", "late", "main")
	// The worker's detail of the check that found the repository already says how to clone it and what to write, with the
	// path as the manifest writes it. The creation of late finds the repository the forge made for an earlier attempt that
	// timed out, and its detail says nothing of the fix.
	w.creator.refuses("exists", ledger.TemplateRefusedRemoteExists, "acme/exists already exists on GitHub; clone it to ../exists and write `remote` in place of `create`", "")
	w.creator.SetReport(w.creator.key("late"), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedRemoteExists, Detail: "acme/late already exists on GitHub",
	})
	w.creator.refuses("signedout", ledger.TemplateRefusedGHSignedOut, "Run `gh auth login --hostname github.com` on this machine", "gh auth login --hostname github.com")
	const fix = "`remote` in place of `create`"
	once := func(name string, got control.Added, clone string) error {
		if got.Outcome != ledger.RegistrationRefused || got.RefusalCode != ledger.TemplateRefusedRemoteExists || !strings.Contains(got.Reason, "already exists on GitHub") {
			return Violatedf("%s reads %+v", name, got)
		}
		if n := strings.Count(got.Reason, fix); n != 1 {
			return Violatedf("the reason of %s names the fix %d times, want once: %q", name, n, got.Reason)
		}
		if n := strings.Count(got.Reason, "clone"); n != 1 {
			return Violatedf("the reason of %s says how to clone it %d times, want once: %q", name, n, got.Reason)
		}
		if got.Creation == nil || !strings.Contains(got.Creation.Remedy, fix) || !strings.Contains(got.Creation.Remedy, clone) {
			return Violatedf("the remedy of %s reads %+v, want it to say %q and %s", name, got.Creation, clone, fix)
		}
		return nil
	}
	preview, err := r.runCreation(w, true)
	if err != nil {
		return err
	}
	exists, _ := outcomeNamed(preview, "exists")
	if err := once("exists", exists, "clone it to ../exists"); err != nil {
		return err
	}
	// A fix the worker names itself is not replaced.
	if signedOut, _ := outcomeNamed(preview, "signedout"); signedOut.Creation == nil || signedOut.Creation.Remedy != "gh auth login --hostname github.com" {
		return Violatedf("the remedy the worker gave reads %+v", signedOut.Creation)
	}
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	late, _ := outcomeNamed(added, "late")
	if err := once("late", late, "clone acme/late to ../late"); err != nil {
		return err
	}
	latest, err := r.latestCreation(s, w.request.ID, "late")
	if err != nil || latest.State != ledger.CreationRefused || latest.Finished == nil || !strings.Contains(latest.Finished.Remedy, fix) || strings.Count(latest.Finished.Reason, fix) != 1 {
		return Violatedf("the events leave the creation of late as %+v: %v", latest, err)
	}
	return r.CheckProjection(s)
}

func checkPreviewShowsWhereTheCreationLeads(r *Rig) error {
	s := r.Store()
	names := []string{"search", "dirty", "late", "broken", "unread"}
	entries := ""
	for _, name := range names {
		entries += creationEntry(name, "../"+name, "")
	}
	w, err := r.creationWorld(s, entries, names, nil)
	if err != nil {
		return err
	}
	for _, name := range names {
		w.creator.passes(name, name, "main")
	}
	w.creator.failsPush("broken", "broken", "main")
	// The paths of search, late, and broken lead through links to folders on another disk, which the checks read. The check
	// of dirty refused after it read where its path leads, the creation of late is refused after it started, and the push
	// of broken failed. The check of unread refused before it read the path, so it says nothing of where it leads and the manifest's path stands.
	for _, name := range []string{"search", "dirty", "late", "broken"} {
		w.creator.leads(name, "/mnt/disk/"+name)
	}
	w.creator.SetCheck(w.creator.key("dirty"), ledger.CreationCheck{Leads: "/mnt/disk/dirty", Refusal: &ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedNotEmpty, Detail: "/mnt/disk/dirty holds files of its own",
	}})
	w.creator.SetReport(w.creator.key("late"), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedChanged, Detail: "the folder was swapped for a link after it was checked",
	})
	w.creator.refuses("unread", ledger.TemplateRefusedGHSignedOut, "Run `gh auth login --hostname github.com` on this machine", "gh auth login --hostname github.com")
	w.creator.unread["unread"] = true
	preview, err := r.runCreation(w, true)
	if err != nil {
		return err
	}
	if err := outcomesAre(preview, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Created, path: "/mnt/disk/search"},
		"dirty":  {outcome: ledger.RegistrationRefused, path: "/mnt/disk/dirty", reason: "holds files of its own", code: ledger.TemplateRefusedNotEmpty},
		"late":   {outcome: ledger.Created, path: "/mnt/disk/late"},
		"broken": {outcome: ledger.Created, path: "/mnt/disk/broken"},
		"unread": {outcome: ledger.RegistrationRefused, path: "/work/unread", reason: "gh auth login --hostname github.com", code: ledger.TemplateRefusedGHSignedOut},
	}); err != nil {
		return Violatedf("the preview: %v", err)
	}
	if search, _ := outcomeNamed(preview, "search"); search.Manifest != "../search" {
		return Violatedf("the preview names the entry's path %q, want it as the manifest wrote it", search.Manifest)
	}
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	// A creation that was refused after it started and one that failed partway report where the path leads too.
	if err := outcomesAre(added, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Created, path: "/work/search"},
		"dirty":  {outcome: ledger.RegistrationRefused, path: "/mnt/disk/dirty", reason: "holds files of its own", code: ledger.TemplateRefusedNotEmpty},
		"late":   {outcome: ledger.RegistrationRefused, path: "/mnt/disk/late", reason: ledger.TemplateRefusedChanged, code: ledger.TemplateRefusedChanged},
		"broken": {outcome: ledger.CreateFailed, path: "/mnt/disk/broken", reason: "denied by the forge"},
		"unread": {outcome: ledger.RegistrationRefused, path: "/work/unread", reason: "gh auth login --hostname github.com", code: ledger.TemplateRefusedGHSignedOut},
	}); err != nil {
		return Violatedf("approving it: %v", err)
	}
	if err := r.recordedAre(s, w.request.ID, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"search": {outcome: ledger.Created, path: "/work/search"},
		"dirty":  {outcome: ledger.RegistrationRefused, path: "/mnt/disk/dirty", reason: "holds files of its own", code: ledger.TemplateRefusedNotEmpty},
		"late":   {outcome: ledger.RegistrationRefused, path: "/mnt/disk/late", reason: ledger.TemplateRefusedChanged, code: ledger.TemplateRefusedChanged},
		"broken": {outcome: ledger.CreateFailed, path: "/mnt/disk/broken", reason: "denied by the forge"},
		"unread": {outcome: ledger.RegistrationRefused, path: "/work/unread", reason: "gh auth login --hostname github.com", code: ledger.TemplateRefusedGHSignedOut},
	}); err != nil {
		return err
	}
	events, err := r.creationEvents(s, w.request.ID)
	if err != nil || len(events) == 0 {
		return Violatedf("the creation events are %+v: %v", events, err)
	}
	var started ledger.CreationStartedEvent
	if err := jsonInto(events[0].Data, &started); err != nil {
		return err
	}
	if started.Name != "search" || started.Path != "/work/search" || started.Leads != "/mnt/disk/search" || started.ManifestPath != "../search" {
		return Violatedf("the creation started as %+v, want the path /work/search and where it leads, /mnt/disk/search", started)
	}
	return r.CheckProjection(s)
}

// busyRemedy is what a check that finds another creation at the path says to do.
const busyRemedy = "another creation at this path is running; preview again when it finishes"

func checkBusyPathIsATransientRefusal(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("busy", "../busy", "")+creationEntry("search", "../search", ""), []string{"busy", "search"}, nil)
	if err != nil {
		return err
	}
	w.creator.passes("busy", "busy", "main")
	w.creator.passes("search", "search", "main")
	w.creator.busy["busy"] = true
	// A preview does not wait for the folder, so it is refused at once, and the refusal passes: it says to preview again.
	preview, err := r.runCreation(w, true)
	if err != nil {
		return err
	}
	if err := outcomesAre(preview, map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"busy":   {outcome: ledger.RegistrationRefused, path: "/work/busy", reason: "another creation", code: ledger.TemplateRefusedBusy},
		"search": {outcome: ledger.Created, path: "/work/search"},
	}); err != nil {
		return Violatedf("the preview: %v", err)
	}
	if busy, _ := outcomeNamed(preview, "busy"); busy.Outcome == ledger.CreateFailed || busy.Creation == nil || busy.Creation.Remedy != busyRemedy || !strings.Contains(busy.Reason, busyRemedy) {
		return Violatedf("the preview reads the busy entry as %+v with creation %+v, want a refusal that says %q", busy, busy.Creation, busyRemedy)
	}
	if checked := w.creator.Checked(); len(checked) != 2 || checked[0].Wait || checked[1].Wait {
		return Violatedf("the preview asked the creator to check %+v, want two checks that do not wait", checked)
	}
	// An approval's check waits for the other creation, and then the entry is created and registered as any other.
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err != nil {
		return err
	}
	want := map[string]entryOutcome{
		"api":    {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"busy":   {outcome: ledger.Created, path: "/work/busy"},
		"search": {outcome: ledger.Created, path: "/work/search"},
	}
	if err := outcomesAre(added, want); err != nil {
		return Violatedf("approving it: %v", err)
	}
	checked := w.creator.Checked()
	if len(checked) != 4 || !checked[2].Wait || !checked[3].Wait {
		return Violatedf("approving asked the creator to check %+v after the preview, want two checks that wait", checked)
	}
	created := w.creator.Created()
	if !slices.Equal(asked(created), []string{"busy", "search"}) || !created[0].Wait || !created[1].Wait {
		return Violatedf("approving created %+v, want busy and search with the wait the checks had", created)
	}
	if types, err := r.creationTypes(s, w.request.ID); err != nil || !slices.Equal(types, []string{"started busy", "finished busy", "started search", "finished search"}) {
		return Violatedf("the creation events are %v: %v", types, err)
	}
	if err := r.recordedAre(s, w.request.ID, want); err != nil {
		return err
	}
	if found, err := w.repositories.Resolve(r.Ctx, r.Scope, "busy"); err != nil || found.Name != "busy" {
		return Violatedf("the entry another creation held reads %+v: %v", found, err)
	}
	return r.CheckProjection(s)
}

func checkBusyRefusalOfAnApprovalIsNeverKept(r *Rig) error {
	s := r.Store()
	w, err := r.creationWorld(s, creationEntry("search", "../search", "")+creationEntry("stubborn", "../stubborn", "")+creationEntry("site", "../site", ""), []string{"search", "stubborn", "site"}, nil)
	if err != nil {
		return err
	}
	for _, name := range []string{"search", "site"} {
		w.creator.passes(name, name, "main")
	}
	// The check of stubborn is refused as busy whether it waits or not, as a worker that does not wait answers.
	w.creator.refuses("stubborn", ledger.TemplateRefusedBusy, "another creation on this worker writes /work/stubborn", "")
	if _, err := r.approve(s, w.request, true, r.member()); err != nil {
		return err
	}
	added, err := r.runCreation(w, false)
	if err == nil || !strings.Contains(err.Error(), "stubborn") || !strings.Contains(err.Error(), "wait") {
		return Violatedf("a busy refusal of a check that waited stopped the run with %v", err)
	}
	if err := outcomesAre(added, map[string]entryOutcome{
		"search":   {outcome: ledger.Created, path: "/work/search"},
		"stubborn": {outcome: ledger.NotAttempted, path: "/work/stubborn", reason: "though it was asked to wait"},
		"api":      {outcome: ledger.NotAttempted, path: "/work/api", reason: "stopped at stubborn"},
		"site":     {outcome: ledger.NotAttempted, path: "/work/site", reason: "stopped at stubborn"},
	}); err != nil {
		return Violatedf("approving it: %v", err)
	}
	for _, one := range added {
		if one.RefusalCode != "" || one.Outcome == ledger.RegistrationRefused {
			return Violatedf("the approval kept %s as %+v", one.Name, one)
		}
	}
	record, err := r.registrationOf(s, w.request.ID)
	if err != nil || record == nil || record.Problem == "" {
		return Violatedf("the approval recorded %+v: %v", record, err)
	}
	for _, one := range record.Repositories {
		if one.RefusalCode == ledger.TemplateRefusedBusy {
			return Violatedf("the approval recorded a busy refusal: %+v", one)
		}
	}
	if types, err := r.creationTypes(s, w.request.ID); err != nil || !slices.Equal(types, []string{"started search", "finished search"}) {
		return Violatedf("the creation events are %v: %v", types, err)
	}
	return r.CheckProjection(s)
}
