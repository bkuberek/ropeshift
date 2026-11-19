package control

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
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
		return Added{Path: path, Outcome: ledger.RegistrationRefused, Reason: plainLine(fmt.Sprintf("%s is not registered: %s", entry.Name, reason))}
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
	}
	check, err := run.creator.Check(ctx, job)
	if err != nil {
		return refused(at, err.Error()), err
	}
	outcome := plainCreation(ledger.CreationOutcome{
		Forge: made.create.Forge, Owner: made.create.Owner, ForgeName: made.forgeName, Visibility: made.create.Visibility,
		DefaultBranch: cmp.Or(check.Branch, entry.DefaultBranch), URL: check.URL, Login: check.Login,
	})
	if check.Refusal != nil {
		outcome.Remedy = remedyFor(*check.Refusal, entry, outcome, at)
		added := withCreation(refused(cmp.Or(check.Leads, at), refusalText(*check.Refusal, outcome.Remedy)), outcome)
		added.RefusalCode = plainLine(check.Refusal.Reason)
		return added, nil
	}
	if run.dryRun {
		return Added{Path: cmp.Or(check.Leads, at), Outcome: ledger.Created, Creation: &outcome}, nil
	}
	finished, err := r.runCreation(ctx, scope, run, entry, job, check.Leads, outcome, actor)
	if err != nil {
		return refused(at, err.Error()), err
	}
	switch finished.Outcome {
	case ledger.CreationRefused:
		added := withCreation(refused(at, finished.Reason), finished.CreationOutcome)
		added.RefusalCode = finished.RefusalCode
		return added, nil
	case ledger.CreationFailed:
		return withCreation(Added{
			Path: at, Outcome: ledger.CreateFailed, Reason: fmt.Sprintf("%s was created only in part, and is kept as it is: %s", entry.Name, finished.Reason),
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
// it. A creation that timed out after the forge made the repository is refused this way on every retry.
func remedyFor(report ledger.TemplateWriteReport, entry planning.ManifestRepository, outcome ledger.CreationOutcome, path string) string {
	switch {
	case report.Remedy != "":
		return plainLine(report.Remedy)
	case report.Reason == ledger.TemplateRefusedBusy:
		return busyRemedy
	case report.Reason == ledger.TemplateRefusedRemoteExists:
		return plainLine(fmt.Sprintf("clone %s/%s to %s, and write `remote` in place of `create` in the manifest's entry for %s", outcome.Owner, outcome.ForgeName, path, entry.Name))
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
		Name: entry.Name, Key: job.Key, ManifestPath: entry.Path, Path: job.Path, Leads: plainLine(leads), Forge: outcome.Forge, Owner: outcome.Owner, ForgeName: outcome.ForgeName,
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
		finished.Remedy = remedyFor(report, entry, outcome, job.Path)
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
