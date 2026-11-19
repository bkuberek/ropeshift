package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Placing names the worker whose machine creates the repositories a plan approval names, which the record of each
// creation's start keeps. Nil names none.
func (r *Repositories) Placing(worker func(context.Context) (string, error)) *Repositories {
	r.worker = worker
	return r
}

// Creating says the creation of an approval's entry runs now in this process, which tells a creation that was
// interrupted from one in flight. A creation holds the mark from just after its start is recorded until its finish
// is.
func (r *Repositories) Creating(approval, name string) bool {
	r.creatingMu.Lock()
	defer r.creatingMu.Unlock()
	return r.creating[ledger.CreationKey(approval, name)]
}

// markCreating marks a creation's key as running now.
func (r *Repositories) markCreating(key string) {
	r.creatingMu.Lock()
	defer r.creatingMu.Unlock()
	if r.creating == nil {
		r.creating = map[string]bool{}
	}
	r.creating[key] = true
}

// clearCreating takes a creation's key out of those running.
func (r *Repositories) clearCreating(key string) {
	r.creatingMu.Lock()
	defer r.creatingMu.Unlock()
	delete(r.creating, key)
}

// creation is what an entry with `create` asks for: where, under whom, how visible, and which repository.
type creation struct {
	create    ledger.RemoteCreate
	forgeName string
	// url is the remote the creation adds as https gives it, and identity is the repository's identity.
	url, identity string
}

// creationOf reads what an entry with `create` asks for. The repository is private unless the entry says public, and
// is named after the last element of the entry's path.
func creationOf(entry planning.ManifestRepository) (creation, error) {
	create, err := ledger.ParseForgeOwner(entry.Create)
	if err != nil {
		return creation{}, err
	}
	create.Visibility = cmp.Or(entry.Visibility, create.Visibility, ledger.VisibilityPrivate)
	name := entry.ForgeName()
	url := ledger.CreatedURL("https", create.Owner, name)
	return creation{create: *create, forgeName: name, url: url, identity: ledger.RepositoryIdentity(url, "")}, nil
}

// sameForge says two remotes name one repository on its forge, which does not tell one case from another.
func sameForge(a, b string) bool {
	return strings.EqualFold(ledger.RepositoryIdentity(a, ""), ledger.RepositoryIdentity(b, ""))
}

// refuses says why a clone is not the repository the entry creates: it has no origin, or its origin is another
// repository. It has nothing to refuse for an entry that creates nothing.
func (c *creation) refuses(clone ports.Clone, real string) string {
	switch {
	case c == nil:
		return ""
	case clone.Remote == "":
		return fmt.Sprintf("the clone at %s has no origin, and the manifest creates %s; add that as its origin, or list the clone with its remote in place of create", real, c.identity)
	case !sameForge(clone.Remote, c.url):
		return fmt.Sprintf("the clone at %s is %s, and the manifest creates %s; list the clone with its remote in place of create, or choose another path",
			real, ledger.RepositoryIdentity(clone.Remote, clone.ID), c.identity)
	}
	return ""
}

// registerCreation decides what approving does for an entry that creates its repository, in the order that names the
// first rule an entry breaks, and runs the creation, then registers the clone, when none is broken. A dry run stops
// after the creator's checks.
func (r *Repositories) registerCreation(ctx context.Context, scope ledger.WorkspaceID, entry planning.ManifestRepository, kept ports.Checkouts, run entryRun, at string, actor ledger.Actor) (Added, error) {
	refused := func(path, reason string) Added {
		return Added{Path: plainPath(path), Outcome: ledger.RegistrationRefused, Reason: plainLine(fmt.Sprintf("%s is not registered: %s", entry.Name, reason))}
	}
	made, err := creationOf(entry)
	if err != nil {
		return refused(at, fmt.Sprintf("its create is no repository to make: %v", err)), nil
	}
	for _, name := range slices.Sorted(maps.Keys(run.held)) {
		if record := run.held[name]; name != entry.Name && record.Remote != "" && sameForge(record.Remote, made.url) {
			return refused(at, fmt.Sprintf("the project already holds %s as %s, which is the repository this entry would create; list it under that name", made.identity, name)), nil
		}
	}
	for identity, record := range run.removedRemotes {
		if strings.EqualFold(identity, made.identity) && !run.restore {
			return refused(at, staysRemoved(record)), nil
		}
	}
	if first, twice := run.forged[strings.ToLower(made.identity)]; twice {
		return refused(at, fmt.Sprintf("it would create %s, which the entry %s of this version creates too", made.identity, first)), nil
	}
	run.forged[strings.ToLower(made.identity)] = entry.Name
	switch {
	case run.creator == nil:
		return refused(at, "this daemon's worker creates no repositories, so approving cannot create it"), nil
	case run.dir.root == "":
		return refused(at, run.why), nil
	case !authority.Allowed(actor, authority.ActionCreateRemotes):
		return refused(at, "only a member creates a repository on a forge"), nil
	}
	resumed, err := r.resuming(ctx, scope, run.cause.Approval, entry.Name)
	if err != nil {
		return refused(at, err.Error()), err
	}
	if !resumed {
		// What is at the path: a clone of the repository is registered as it is, and one that is another repository, or
		// that has no origin, is refused. Nothing there, or a folder that is no clone, is left to the creator's checks.
		real, missing, reason := placeAt(ctx, kept, run.dir, at)
		switch {
		case missing:
		case reason != "":
			return refused(cmp.Or(real, at), reason), nil
		default:
			clone, none, reason := topAt(ctx, kept, real)
			switch {
			case reason != "" && !none:
				return refused(real, reason), nil
			case !none:
				if reason = made.refuses(clone, real); reason != "" {
					return refused(real, reason), nil
				}
				return r.registerClone(ctx, scope, entry, kept, run, at, &made, actor)
			}
		}
	}
	job := ledger.RepositoryCreation{
		Key: ledger.CreationKey(run.cause.Approval, entry.Name), Project: run.project, Name: entry.Name, Path: at, Bound: run.dir.parent,
		PlanningClone: run.dir.clone, Create: made.create, ForgeName: made.forgeName, DefaultBranch: entry.DefaultBranch,
		// An approval's check waits for another creation that holds the folder, as its creation does, and a preview's is
		// refused at once.
		Wait: !run.dryRun,
	}
	check, err := run.creator.Check(ctx, job)
	if err != nil {
		return refused(at, err.Error()), err
	}
	outcome := plainCreation(ledger.CreationOutcome{
		Forge: made.create.Forge, Owner: made.create.Owner, ForgeName: made.forgeName, Visibility: made.create.Visibility,
		DefaultBranch: cmp.Or(check.Branch, entry.DefaultBranch), URL: check.URL, Login: check.Login,
	})
	if check.Refusal != nil && check.Refusal.Reason == ledger.TemplateRefusedBusy && !run.dryRun {
		// A busy path is a refusal for a preview to tell, and an approval's check waits for the folder instead, so a worker that
		// refuses one as busy anyway has not waited. The approval keeps no refusal, which no later word could undo: the entry
		// is not attempted and the run stops with the reason, so it can be run again.
		err := fmt.Errorf("the check of %s found another creation at its path though it was asked to wait for it", entry.Name)
		return Added{Path: plainPath(cmp.Or(check.Leads, at)), Outcome: ledger.NotAttempted, Reason: plainLine(entry.Name + " was not attempted: " + err.Error())}, err
	}
	if check.Refusal != nil {
		outcome.Remedy = remedyFor(*check.Refusal, entry, outcome)
		added := withCreation(refused(cmp.Or(check.Leads, at), refusalText(*check.Refusal, outcome.Remedy)), outcome)
		added.RefusalCode = plainLine(check.Refusal.Reason)
		return added, nil
	}
	if run.dryRun {
		return Added{Path: plainPath(cmp.Or(check.Leads, at)), Outcome: ledger.Created, Creation: &outcome}, nil
	}
	finished, err := r.runCreation(ctx, scope, run, entry, job, check.Leads, outcome, actor)
	if err != nil {
		return refused(at, err.Error()), err
	}
	switch finished.Outcome {
	case ledger.CreationRefused:
		added := withCreation(refused(cmp.Or(check.Leads, at), finished.Reason), finished.CreationOutcome)
		added.RefusalCode = finished.RefusalCode
		return added, nil
	case ledger.CreationFailed:
		return withCreation(Added{
			Path: plainPath(cmp.Or(check.Leads, at)), Outcome: ledger.CreateFailed, Reason: fmt.Sprintf("%s was created only in part, and is kept as it is: %s", entry.Name, finished.Reason),
		}, finished.CreationOutcome), nil
	}
	added, err := r.registerClone(ctx, scope, entry, kept, run, at, &made, actor)
	added.Creation = &finished.CreationOutcome
	if added.Outcome == ledger.Registered {
		added.Outcome = ledger.Created
	}
	return added, err
}

// withCreation gives an outcome the creation it came of.
func withCreation(added Added, creation ledger.CreationOutcome) Added {
	added.Creation = &creation
	return added
}

// plainLine makes text a tool printed, or a path of a disk, safe to keep and to show: the escape sequences and control
// characters ledger.PlainText drops, and each line break and tab made a space, so it is one line.
func plainLine(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(oneLine.Replace(ledger.PlainText(text)), "\t", " "))
}

// plainPath is a path a worker or a disk gave, safe to keep and to show as one field of one line: a line break is a space,
// a control or format character and a tab are dropped, and every other character, a space among them, is kept, so a path
// stays as it is.
func plainPath(path string) string {
	return strings.ReplaceAll(ledger.PlainText(oneLine.Replace(path)), "\t", "")
}

// plainCreation is a creation's outcome with every text the worker gave made plain, in a copy, because what git and the
// forge CLI print, and the paths they print, are not trusted.
func plainCreation(creation ledger.CreationOutcome) ledger.CreationOutcome {
	creation.Forge, creation.Owner, creation.ForgeName = plainLine(creation.Forge), plainLine(creation.Owner), plainLine(creation.ForgeName)
	creation.Visibility, creation.DefaultBranch = plainLine(creation.Visibility), plainLine(creation.DefaultBranch)
	creation.URL, creation.Login, creation.Commit = plainLine(creation.URL), plainLine(creation.Login), plainLine(creation.Commit)
	creation.CommitError, creation.Remedy, creation.Makefile = plainLine(creation.CommitError), plainLine(creation.Remedy), plainLine(creation.Makefile)
	if creation.Steps != nil {
		steps := make([]ledger.RemoteStep, len(creation.Steps))
		for i, step := range creation.Steps {
			steps[i] = ledger.RemoteStep{Step: plainLine(step.Step), Outcome: plainLine(step.Outcome), Detail: plainLine(step.Detail)}
		}
		creation.Steps = steps
	}
	if creation.Remaining != nil {
		remaining := make([]string, len(creation.Remaining))
		for i, command := range creation.Remaining {
			remaining[i] = plainLine(command)
		}
		creation.Remaining = remaining
	}
	return creation
}

// busyRemedy is what a check that finds another creation at the path says to do: the refusal is transient, and the
// entry is neither created nor failed.
const busyRemedy = "another creation at this path is running; preview again when it finishes"

// remedyFor is the fix a refusal of the creator names: the command the worker gave, or, for a repository that exists on
// the forge, which ropeshift never adopts, the clone to make at the entry's path and the manifest change that registers
// it. The worker's detail of that refusal says so already, with the path as the manifest writes it, and then it is the
// remedy, so the fix is said once; otherwise the remedy says it in the same form. A creation that timed out after the
// forge made the repository is refused this way on every retry.
func remedyFor(report ledger.TemplateWriteReport, entry planning.ManifestRepository, outcome ledger.CreationOutcome) string {
	switch {
	case report.Remedy != "":
		return plainLine(report.Remedy)
	case report.Reason == ledger.TemplateRefusedBusy:
		return busyRemedy
	case report.Reason == ledger.TemplateRefusedRemoteExists && strings.Contains(report.Detail, "in place of `create`"):
		return plainLine(report.Detail)
	case report.Reason == ledger.TemplateRefusedRemoteExists:
		return plainLine(fmt.Sprintf("clone %s/%s to %s, and write `remote` in place of `create` in the manifest's entry for %s", outcome.Owner, outcome.ForgeName, entry.Path, entry.Name))
	}
	return ""
}

// refusalText is what a check of the creator refused with, and the fix it names, made plain.
func refusalText(report ledger.TemplateWriteReport, remedy string) string {
	reason, detail := plainLine(report.Reason), plainLine(report.Detail)
	text := cmp.Or(detail, reason)
	if detail != "" && reason != "" {
		text = reason + ": " + detail
	}
	switch {
	case remedy == "" || strings.Contains(text, remedy):
	case report.Remedy != "":
		text += "; run `" + remedy + "`"
	default:
		text += "; " + remedy
	}
	return text
}

// resuming says the creation of an approval's entry was started and has no outcome that leaves nothing to go on
// with: it was interrupted, or it failed partway and kept what it made. Its folder is the creation's own, and the
// creator goes on from its journal.
func (r *Repositories) resuming(ctx context.Context, scope ledger.WorkspaceID, approval, name string) (bool, error) {
	var latest ledger.CreationRecord
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: authority.SubjectApproval, ID: approval})
		if err != nil {
			return err
		}
		latest, err = ledger.LatestCreation(events, name)
		return err
	})
	return latest.State == ledger.CreationStarted || latest.State == ledger.CreationFailed, err
}

// runCreation records that a creation starts, runs it on the worker's machine, and records what it came to. The
// creation reads as running from just after its start is recorded until its finish is.
func (r *Repositories) runCreation(ctx context.Context, scope ledger.WorkspaceID, run entryRun, entry planning.ManifestRepository, job ledger.RepositoryCreation, leads string, outcome ledger.CreationOutcome, actor ledger.Actor) (ledger.CreationFinishedEvent, error) {
	started := ledger.CreationStartedEvent{
		Name: entry.Name, Key: job.Key, ManifestPath: entry.Path, Path: job.Path, Leads: plainPath(leads), Forge: outcome.Forge, Owner: outcome.Owner, ForgeName: outcome.ForgeName,
		Visibility: outcome.Visibility, DefaultBranch: outcome.DefaultBranch,
	}
	if r.worker != nil {
		started.Worker, _ = r.worker(ctx)
	}
	if err := r.appendCreation(ctx, scope, ledger.EventRepositoryCreationStarted, run.cause.Approval, started, actor); err != nil {
		return ledger.CreationFinishedEvent{}, err
	}
	r.markCreating(job.Key)
	defer r.clearCreating(job.Key)
	report, err := run.creator.Create(ctx, job)
	if err != nil {
		return ledger.CreationFinishedEvent{}, err
	}
	finished := finishedCreation(entry, job, outcome, report)
	return finished, r.appendCreation(ctx, scope, ledger.EventRepositoryCreationFinished, run.cause.Approval, finished, actor)
}

// finishedCreation reads what a report of the creator says a creation came to. It is created when every step is done
// and no command remains, refused when a check refused with nothing written, and failed otherwise.
func finishedCreation(entry planning.ManifestRepository, job ledger.RepositoryCreation, outcome ledger.CreationOutcome, report ledger.TemplateWriteReport) ledger.CreationFinishedEvent {
	finished := ledger.CreationFinishedEvent{Name: entry.Name, Key: job.Key, CreationOutcome: outcome}
	if report.Outcome == ledger.TemplateOutcomeRefused {
		finished.Remedy = remedyFor(report, entry, outcome)
		finished.Outcome, finished.Reason, finished.RefusalCode = ledger.CreationRefused, refusalText(report, finished.Remedy), plainLine(report.Reason)
		return finished
	}
	finished.Commit, finished.CommitError, finished.Remaining = report.Commit, report.CommitError, report.Remaining
	for _, repository := range report.Repositories {
		finished.Steps = append(finished.Steps, repository.Steps...)
		finished.Commit = cmp.Or(finished.Commit, repository.Commit)
		finished.CommitError = cmp.Or(finished.CommitError, repository.CommitError)
		finished.URL = cmp.Or(repository.Remote, finished.URL)
	}
	step := slices.IndexFunc(finished.Steps, func(step ledger.RemoteStep) bool { return step.Outcome != ledger.RemoteStepDone })
	switch {
	case report.Outcome != ledger.TemplateOutcomeWritten:
		finished.Outcome, finished.Reason = ledger.CreationFailed, fmt.Sprintf("the worker reported the outcome %q", report.Outcome)
	case finished.CommitError != "":
		finished.Outcome, finished.Reason = ledger.CreationFailed, "the first commit failed: "+finished.CommitError
	case step >= 0:
		finished.Outcome, finished.Reason = ledger.CreationFailed, fmt.Sprintf("the %s step %s", finished.Steps[step].Step, finished.Steps[step].Outcome)
		if detail := finished.Steps[step].Detail; detail != "" {
			finished.Reason += ": " + detail
		}
	case len(finished.Remaining) != 0:
		finished.Outcome, finished.Reason = ledger.CreationFailed, "commands remain to finish it"
	default:
		finished.Outcome = ledger.CreationCreated
		finished.Makefile = entry.Name + "=" + finished.URL + "=" + entry.Path
	}
	finished.CreationOutcome, finished.Reason = plainCreation(finished.CreationOutcome), plainLine(finished.Reason)
	return finished
}

// appendCreation records an event about a creation on the plan approval that creates it, as the member who approved.
func (r *Repositories) appendCreation(ctx context.Context, scope ledger.WorkspaceID, kind, approval string, data any, actor ledger.Actor) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return r.store.Write(ctx, scope, kind, func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{Type: kind, Actor: actor, Subject: ledger.Subject{Kind: authority.SubjectApproval, ID: approval}, Data: raw})
		return err
	})
}

// maxSkipReason is the longest reason a skip takes, in characters.
const maxSkipReason = 2000

// UncreatedState is where a repository an approved plan version creates stands when its creation waits for a member.
type UncreatedState string

// The states of an uncreated repository.
const (
	// CreationRefused is a creation a check refused before it wrote anything.
	CreationRefused UncreatedState = "refused"
	// CreationFailed is a creation that stopped at a step that failed, keeping what it made.
	CreationFailed UncreatedState = "failed"
	// CreationInterrupted is a creation that started and has no later record, with no run of it in this process.
	CreationInterrupted UncreatedState = "interrupted"
	// CreationRunning is a creation that runs in this process now.
	CreationRunning UncreatedState = "running"
)

// UncreatedRepository is a repository an approved plan version creates that its approval has not created, and a member
// has not skipped: the approval, the entry's name, its path as the manifest writes it and where that leads, where the
// creation stands and why, what it did before it stopped, and when its latest record was made.
type UncreatedRepository struct {
	Approval     authority.Approval
	Name         string
	ManifestPath string
	Path         string
	State        UncreatedState
	Reason       string
	Creation     *ledger.CreationOutcome
	Since        int64
}

// UncreatedRepositories lists, for each approved plan approval, the entries its version creates whose latest creation
// is refused, failed, or interrupted, or runs now, and whose name the project does not hold. A skipped or a created
// entry is not listed. creating says whether the creation of an approval's entry runs now in this process, and an entry
// whose creation does is running. An entry whose creation left no record is read from the registration the approval
// recorded, when that says it was refused, not attempted, or failed to create.
func UncreatedRepositories(tx ports.ReadTx, creating func(approval, name string) bool) ([]UncreatedRepository, error) {
	approvals, err := tx.Approvals().All()
	if err != nil {
		return nil, err
	}
	held, err := heldNames(tx)
	if err != nil {
		return nil, err
	}
	var out []UncreatedRepository
	for _, row := range approvals {
		if row.Kind != authority.KindPlan || row.State != authority.ApprovalApproved {
			continue
		}
		entries, err := VersionManifest(tx, row)
		if err != nil {
			return nil, err
		}
		targets, err := VersionTargets(tx, row)
		if err != nil {
			return nil, err
		}
		events, err := tx.EventsAbout(ledger.Subject{Kind: authority.SubjectApproval, ID: row.ID})
		if err != nil {
			return nil, err
		}
		recorded, err := RegistrationOf(tx, row.ID)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.Create == "" || !slices.Contains(targets, entry.Name) || held[entry.Name] {
				continue
			}
			item, listed, err := uncreated(row, entry, events, recorded, creating(row.ID, entry.Name))
			if err != nil {
				return nil, err
			}
			if listed {
				out = append(out, item)
			}
		}
	}
	return out, nil
}

// heldNames are the names of the repositories the project holds and has not removed.
func heldNames(tx ports.ReadTx) (map[string]bool, error) {
	all, err := tx.Repositories().Repositories()
	held := map[string]bool{}
	for _, record := range all {
		if record.Name != "" && !record.Removed() {
			held[record.Name] = true
		}
	}
	return held, err
}

// uncreated reads where one entry's creation stands, and says whether it waits for a member or runs.
func uncreated(row authority.Approval, entry planning.ManifestRepository, events []ledger.Event, recorded *ledger.ManifestRegistration, running bool) (UncreatedRepository, bool, error) {
	latest, err := ledger.LatestCreation(events, entry.Name)
	if err != nil {
		return UncreatedRepository{}, false, err
	}
	item := UncreatedRepository{Approval: row, Name: entry.Name, ManifestPath: entry.Path, Reason: latest.Reason, Since: creationSince(events, entry.Name)}
	if latest.Finished != nil {
		creation := latest.Finished.CreationOutcome
		item.Creation = &creation
	}
	item.Path = creationPath(events, recorded, entry.Name)
	switch {
	case running:
		item.State = CreationRunning
	case latest.State == ledger.CreationStarted:
		item.State = CreationInterrupted
	case latest.State == ledger.CreationRefused:
		item.State = CreationRefused
	case latest.State == ledger.CreationFailed:
		item.State = CreationFailed
	case latest.State != ledger.CreationUntried:
		return UncreatedRepository{}, false, nil
	default:
		// No creation record: the registration the approval recorded is what says the entry was not created.
		outcome, ok := recordedOutcome(recorded, entry.Name)
		switch {
		case !ok:
			return UncreatedRepository{}, false, nil
		case outcome.Outcome == ledger.RegistrationRefused:
			item.State = CreationRefused
		case outcome.Outcome == ledger.NotAttempted, outcome.Outcome == ledger.CreateFailed:
			item.State = CreationFailed
		default:
			return UncreatedRepository{}, false, nil
		}
		item.Reason, item.Creation = outcome.Reason, outcome.Creation
	}
	if item.Since == 0 && len(events) != 0 {
		item.Since = events[len(events)-1].RecordedAt
	}
	return item, true, nil
}

// recordedOutcome reads what the approval recorded of one entry, when it recorded it.
func recordedOutcome(recorded *ledger.ManifestRegistration, name string) (ledger.ManifestOutcome, bool) {
	if recorded == nil {
		return ledger.ManifestOutcome{}, false
	}
	at := slices.IndexFunc(recorded.Repositories, func(one ledger.ManifestOutcome) bool { return one.Name == name })
	if at < 0 {
		return ledger.ManifestOutcome{}, false
	}
	return recorded.Repositories[at], true
}

// creationPath is where an entry's path led on the worker's machine: where its creation started, or else where the
// approval's registration recorded it.
func creationPath(events []ledger.Event, recorded *ledger.ManifestRegistration, name string) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != ledger.EventRepositoryCreationStarted {
			continue
		}
		var started ledger.CreationStartedEvent
		if json.Unmarshal(events[i].Data, &started) == nil && started.Name == name && started.Path != "" {
			return started.Path
		}
	}
	if one, ok := recordedOutcome(recorded, name); ok {
		return one.Resolved
	}
	return ""
}

// creationSince is when the latest event about one entry's creation was recorded, or zero when there is none.
func creationSince(events []ledger.Event, name string) int64 {
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case ledger.EventRepositoryCreationStarted, ledger.EventRepositoryCreationFinished, ledger.EventRepositoryCreationSkipped:
			var named struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(events[i].Data, &named) == nil && named.Name == name {
				return events[i].RecordedAt
			}
		}
	}
	return 0
}

// creationTarget is an approved plan approval and the entry of its version that a retry or a skip acts on.
type creationTarget struct {
	row      authority.Approval
	entry    planning.ManifestRepository
	entries  []planning.ManifestRepository
	targets  map[string]bool
	recorded *ledger.ManifestRegistration
	// started is where the entry's path led when its creation last started, which a record of it keeps when the
	// approval recorded none.
	started string
}

// creationTargetOf reads the entry a retry or a skip of an approval names, and refuses it as an illegal move when the
// approval is not an approved plan approval, the entry is not one its version creates, or the project holds its name.
func creationTargetOf(tx ports.ReadTx, approval, name string) (creationTarget, error) {
	row, err := tx.Approvals().Approval(approval)
	if err != nil {
		return creationTarget{}, err
	}
	if row.Kind != authority.KindPlan || row.State != authority.ApprovalApproved {
		return creationTarget{}, fmt.Errorf("%w: approval %s is not an approved plan approval", workorders.ErrIllegalMove, approval)
	}
	target := creationTarget{row: row, targets: map[string]bool{}}
	if target.entries, err = VersionManifest(tx, row); err != nil {
		return creationTarget{}, err
	}
	targeted, err := VersionTargets(tx, row)
	if err != nil {
		return creationTarget{}, err
	}
	for _, repository := range targeted {
		target.targets[repository] = true
	}
	at := slices.IndexFunc(target.entries, func(entry planning.ManifestRepository) bool { return entry.Name == name })
	if at < 0 || target.entries[at].Create == "" || !target.targets[name] {
		return creationTarget{}, fmt.Errorf("%w: %s is not a repository version %d of plan %s creates", workorders.ErrIllegalMove, name, row.PlanVersion, row.Plan)
	}
	target.entry = target.entries[at]
	held, err := heldNames(tx)
	if err != nil {
		return creationTarget{}, err
	}
	if held[name] {
		return creationTarget{}, fmt.Errorf("%w: the project holds a repository named %s", workorders.ErrIllegalMove, name)
	}
	if target.recorded, err = RegistrationOf(tx, approval); err != nil {
		return creationTarget{}, err
	}
	events, err := tx.EventsAbout(ledger.Subject{Kind: authority.SubjectApproval, ID: approval})
	if err != nil {
		return creationTarget{}, err
	}
	target.started = creationPath(events, nil, name)
	return target, nil
}

// refuseRunning refuses a retry or a skip of a creation that runs now in this process.
func (r *Repositories) refuseRunning(approval, name string) error {
	if r.Creating(approval, name) {
		return fmt.Errorf("%w: %s is being created now", workorders.ErrIllegalMove, name)
	}
	return nil
}

// RetryCreation runs the creation of one entry of an approved plan version again, as one entry of a real run under
// the approval, with the key of its first run, and records the approval's registration as the last one with that entry
// replaced. An approval that recorded no registration runs the whole registration. It answers with the registration as
// recorded. Retrying is a member's act, as approving the plan was. An entry that is not one the approval's version
// creates, a name the project holds, an approval that is not an approved plan approval, and a creation that runs now are
// refused as an illegal move, and record nothing; an error that comes with a registration is one the run met after it
// began.
func (r *Repositories) RetryCreation(ctx context.Context, scope ledger.WorkspaceID, approval, name string, kept ports.Checkouts, creator ports.RepositoryCreator, project string, actor ledger.Actor) ([]Added, error) {
	if !authority.Allowed(actor, authority.ActionApprovePlan) || !authority.Allowed(actor, authority.ActionRegisterManifest) || !authority.Allowed(actor, authority.ActionCreateRemotes) {
		return nil, workorders.ErrForbidden
	}
	if err := r.refuseRunning(approval, name); err != nil {
		return nil, err
	}
	r.registering.Lock()
	defer r.registering.Unlock()
	var target creationTarget
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		target, err = creationTargetOf(tx, approval, name)
		return err
	})
	if err != nil {
		return nil, err
	}
	if target.recorded == nil {
		return r.registerApproved(ctx, scope, approval, kept, creator, project, false, actor)
	}
	run := manifestRun{cause: registrationCause{Approval: target.row.ID, Plan: target.row.Plan, PlanVersion: target.row.PlanVersion}, targets: target.targets, creator: creator, project: project}
	added, runErr := r.registerEntries(ctx, scope, []planning.ManifestRepository{target.entry}, kept, run, actor)
	if len(added) == 0 {
		return nil, runErr
	}
	record := replacedOutcome(*target.recorded, outcomesOf(added[:1])[0], runErr)
	if failed := r.recordRegistration(ctx, scope, approval, record, actor); failed != nil {
		return nil, errors.Join(runErr, failed)
	}
	out, err := r.recordedAs(ctx, scope, record)
	return out, errors.Join(runErr, err)
}

// SkipCreation records that a member chose to leave one entry of an approved plan version uncreated, with why: it appends
// the skip to the approval and the approval's registration as the last one with that entry skipped, in one write, so the
// repository leaves Needs you and the work that names it stays in contract review. A later approved version that creates
// the entry creates it. The reason takes 1 to 2000 characters. It answers with the registration as recorded, and refuses
// what RetryCreation refuses. Skipping is a member's act.
func (r *Repositories) SkipCreation(ctx context.Context, scope ledger.WorkspaceID, approval, name, reason string, actor ledger.Actor) ([]Added, error) {
	if !authority.Allowed(actor, authority.ActionApprovePlan) {
		return nil, workorders.ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if length := utf8.RuneCountInString(reason); length == 0 || length > maxSkipReason {
		return nil, fmt.Errorf("%w: a reason for skipping takes 1 to %d characters", workorders.ErrInvalid, maxSkipReason)
	}
	if err := r.refuseRunning(approval, name); err != nil {
		return nil, err
	}
	r.registering.Lock()
	defer r.registering.Unlock()
	var target creationTarget
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		target, err = creationTargetOf(tx, approval, name)
		return err
	})
	if err != nil {
		return nil, err
	}
	skipped := ledger.ManifestOutcome{Name: name, Path: target.entry.Path, Remote: target.entry.Remote, Resolved: target.started, Outcome: ledger.Skipped, Reason: reason}
	var record ledger.ManifestRegistration
	if target.recorded != nil {
		if before, ok := recordedOutcome(target.recorded, name); ok {
			skipped.Resolved = before.Resolved
		}
		record = replacedOutcome(*target.recorded, skipped, nil)
	} else {
		record = ledger.ManifestRegistration{Repositories: outcomesNotAttempted(target.entries, skipped)}
	}
	skip, err := json.Marshal(ledger.CreationSkippedEvent{Name: name, Reason: reason})
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	subject := ledger.Subject{Kind: authority.SubjectApproval, ID: approval}
	err = r.store.Write(ctx, scope, "approval.repository_creation_skipped", func(tx ports.WriteTx) error {
		if _, err := tx.Append(ledger.EventDraft{Type: ledger.EventRepositoryCreationSkipped, Actor: actor, Subject: subject, Data: skip}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: ledger.EventManifestRegistered, Actor: actor, Subject: subject, Data: data})
		return err
	})
	if err != nil {
		return nil, err
	}
	return r.recordedAs(ctx, scope, record)
}

// replacedOutcome is a registration with one entry's outcome in place of what it recorded of that entry, or after the
// others when it recorded none. The registration's problem is the run's error when it has one, and else stays while an
// entry is still not attempted.
func replacedOutcome(record ledger.ManifestRegistration, outcome ledger.ManifestOutcome, runErr error) ledger.ManifestRegistration {
	out := ledger.ManifestRegistration{Repositories: slices.Clone(record.Repositories), Problem: record.Problem}
	if at := slices.IndexFunc(out.Repositories, func(one ledger.ManifestOutcome) bool { return one.Name == outcome.Name }); at >= 0 {
		out.Repositories[at] = outcome
	} else {
		out.Repositories = append(out.Repositories, outcome)
	}
	switch {
	case runErr != nil:
		out.Problem = runErr.Error()
	case !slices.ContainsFunc(out.Repositories, func(one ledger.ManifestOutcome) bool { return one.Outcome == ledger.NotAttempted }):
		out.Problem = ""
	}
	return out
}

// outcomesNotAttempted is the outcome of each entry of a manifest for an approval that recorded none when a member skips
// one of them: the skipped entry as given, and each other as not attempted, which a retry of it runs.
func outcomesNotAttempted(entries []planning.ManifestRepository, skipped ledger.ManifestOutcome) []ledger.ManifestOutcome {
	out := make([]ledger.ManifestOutcome, 0, len(entries))
	for _, entry := range entries {
		if entry.Name == skipped.Name {
			out = append(out, skipped)
			continue
		}
		out = append(out, ledger.ManifestOutcome{
			Name: entry.Name, Path: entry.Path, Remote: entry.Remote, Outcome: ledger.NotAttempted,
			Reason: fmt.Sprintf("%s was not attempted: %s was skipped before the registration ran", entry.Name, skipped.Name),
		})
	}
	return out
}

// recordedAs reads a registration as the outcomes of a run, each with the record it names as it stands now.
func (r *Repositories) recordedAs(ctx context.Context, scope ledger.WorkspaceID, record ledger.ManifestRegistration) ([]Added, error) {
	var out []Added
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		out, err = recordedAdded(tx.Repositories(), &record)
		return err
	})
	return out, err
}
