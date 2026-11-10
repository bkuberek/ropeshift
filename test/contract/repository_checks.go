package contract

import (
	"encoding/json"
	"errors"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// RepositoryChecks hold what the ledger promises about its record of a repository: git gives the remote
// and the default branch, only a member gives the rest, and a setting that cannot hold with another is
// refused whichever one changes second (FR-REPO-1, FR-BASE-1, FR-BASE-3).
var RepositoryChecks = []Check{
	{"RB1", "reading a repository makes its record with the safest settings, and reading it again appends nothing", checkRepositoryIsMadeOnce},
	{"RB2", "a later read refreshes the remote and the default branch and keeps what a member set", checkDetectionKeepsMemberSettings},
	{"RB3", "a member's change is one audited event, and a change that changes nothing appends nothing", checkRepositoryChangeIsAudited},
	{"RB4", "only a member changes a repository's settings", checkOnlyAMemberChangesARepository},
	{"RB5", "accepted work cannot start from a repository whose merge method rewrites commits, whichever setting changes second", checkAcceptedNeedsCommitsThatSurvive},
	{"RB6", "a setting nobody offers is refused and changes nothing", checkRepositoryRefusesBadSettings},
	{"RB7", "a repository record belongs to one workspace", checkRepositoryIsScoped},
	{"RB9", "intake makes a repository's record from the facts its source gives, and a source with none makes none", checkIntakeRecordsTheRepository},
	{"RB8", "intake carries a work order's base to the work order, and a rebuild from events keeps it", checkIntakeCarriesAWorkOrdersBase},
}

func (r *Rig) repositories(s ports.Store) *control.Repositories { return control.NewRepositories(s) }

func (r *Rig) theRepository(s ports.Store, id string) (ledger.Repository, error) {
	var record ledger.Repository
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Repositories().Repository(id)
		return err
	})
	return record, err
}

func (r *Rig) detect(s ports.Store, id, remote, branch string) (ledger.Repository, error) {
	return r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: id, Remote: remote, DefaultBranch: branch}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
}

func (r *Rig) change(s ports.Store, id string, change control.RepositoryChange, actor ledger.Actor) (ledger.Repository, error) {
	return r.repositories(s).Change(r.Ctx, r.Scope, id, change, actor)
}

func (r *Rig) repositoryEvents(s ports.Store, id string) ([]ledger.Event, error) {
	var events []ledger.Event
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		events, err = tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectRepository, ID: id})
		return err
	})
	return events, err
}

func text(value string) *string { return &value }

func checkRepositoryIsMadeOnce(r *Rig) error {
	s := r.Store()
	made, err := r.detect(s, "repo-1", "https://example.invalid/acme/app.git", "main")
	if err != nil {
		return err
	}
	want := ledger.NewRepository("repo-1")
	want.Remote, want.DefaultBranch = "https://example.invalid/acme/app.git", "main"
	if made != want {
		return Violatedf("a new record reads %+v, not %+v", made, want)
	}
	if made.Base() != "main" {
		return Violatedf("the base of a record with none named is %q", made.Base())
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if _, err := r.detect(s, "repo-1", "https://example.invalid/acme/app.git", "main"); err != nil {
		return err
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("reading the same facts again moved the ledger from %d to %d: %v", before, after, err)
	}
	events, err := r.repositoryEvents(s, "repo-1")
	if err != nil || len(events) != 1 || events[0].Type != ledger.EventRepositoryDetected {
		return Violatedf("the record's history is %+v: %v", events, err)
	}
	return nil
}

func checkDetectionKeepsMemberSettings(r *Rig) error {
	s := r.Store()
	if _, err := r.detect(s, "repo-1", "", "main"); err != nil {
		return err
	}
	squash := ledger.MergeSquash
	on := true
	if _, err := r.change(s, "repo-1", control.RepositoryChange{Base: text("release/1.2"), MergeMethod: &squash, Integration: &on}, r.Actor); err != nil {
		return err
	}
	refreshed, err := r.detect(s, "repo-1", "git@example.invalid:acme/app.git", "trunk")
	if err != nil {
		return err
	}
	if refreshed.Remote != "git@example.invalid:acme/app.git" || refreshed.DefaultBranch != "trunk" {
		return Violatedf("a later read left the facts at %+v", refreshed)
	}
	if refreshed.BaseBranch != "release/1.2" || refreshed.MergeMethod != ledger.MergeSquash || !refreshed.Integration || refreshed.Base() != "release/1.2" {
		return Violatedf("a later read changed what a member set: %+v", refreshed)
	}
	// A source that lost its remote says so, and the record follows it.
	lost, err := r.detect(s, "repo-1", "", "trunk")
	if err != nil || lost.Remote != "" {
		return Violatedf("a repository with no remote reads %+v: %v", lost, err)
	}
	return nil
}

func checkRepositoryChangeIsAudited(r *Rig) error {
	s := r.Store()
	if _, err := r.detect(s, "repo-1", "", "main"); err != nil {
		return err
	}
	method := ledger.MergeRebase
	changed, err := r.change(s, "repo-1", control.RepositoryChange{MergeMethod: &method}, r.Actor)
	if err != nil {
		return err
	}
	if changed.MergeMethod != ledger.MergeRebase {
		return Violatedf("the change left %+v", changed)
	}
	events, err := r.repositoryEvents(s, "repo-1")
	if err != nil {
		return err
	}
	last := events[len(events)-1]
	if last.Type != ledger.EventRepositoryChanged || last.Actor != r.Actor {
		return Violatedf("the last event is %s from %+v", last.Type, last.Actor)
	}
	var data struct {
		Before ledger.Repository `json:"before"`
		After  ledger.Repository `json:"after"`
	}
	if err := json.Unmarshal(last.Data, &data); err != nil {
		return err
	}
	if data.Before.MergeMethod != ledger.MergeCommit || data.After.MergeMethod != ledger.MergeRebase {
		return Violatedf("the event says %s then %s", data.Before.MergeMethod, data.After.MergeMethod)
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if _, err := r.change(s, "repo-1", control.RepositoryChange{MergeMethod: &method}, r.Actor); err != nil {
		return err
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a change that changes nothing moved the ledger from %d to %d: %v", before, after, err)
	}
	if _, err := r.change(s, "missing", control.RepositoryChange{MergeMethod: &method}, r.Actor); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("changing a repository nothing read: %v", err)
	}
	return nil
}

func checkOnlyAMemberChangesARepository(r *Rig) error {
	s := r.Store()
	if _, err := r.detect(s, "repo-1", "", "main"); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	on := true
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "worker-1"}, {Kind: ledger.ActorLane, ID: "lane-1"},
		{Kind: ledger.ActorWorker, ID: "worker-1"}, {Kind: ledger.ActorSystem, ID: "intake"},
	} {
		if _, err := r.change(s, "repo-1", control.RepositoryChange{Integration: &on}, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s changing a repository: %v", actor.Kind, err)
		}
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a refused caller moved the ledger from %d to %d: %v", before, after, err)
	}
	if record, err := r.theRepository(s, "repo-1"); err != nil || record.Integration {
		return Violatedf("a refused caller changed the record: %+v: %v", record, err)
	}
	return nil
}

func checkAcceptedNeedsCommitsThatSurvive(r *Rig) error {
	s := r.Store()
	if _, err := r.detect(s, "repo-1", "", "main"); err != nil {
		return err
	}
	accepted, squash, rebase, merge := ledger.StartFromAccepted, ledger.MergeSquash, ledger.MergeRebase, ledger.MergeCommit
	for _, method := range []ledger.MergeMethod{squash, rebase} {
		if _, err := r.change(s, "repo-1", control.RepositoryChange{StartFrom: &accepted, MergeMethod: &method}, r.Actor); !errors.Is(err, ledger.ErrRewritesCommits) {
			return Violatedf("%s with accepted work set together: %v", method, err)
		}
	}
	if _, err := r.change(s, "repo-1", control.RepositoryChange{MergeMethod: &squash}, r.Actor); err != nil {
		return err
	}
	if _, err := r.change(s, "repo-1", control.RepositoryChange{StartFrom: &accepted}, r.Actor); !errors.Is(err, ledger.ErrRewritesCommits) {
		return Violatedf("accepted work on a squash repository: %v", err)
	}
	if _, err := r.change(s, "repo-1", control.RepositoryChange{MergeMethod: &merge}, r.Actor); err != nil {
		return err
	}
	if _, err := r.change(s, "repo-1", control.RepositoryChange{StartFrom: &accepted}, r.Actor); err != nil {
		return err
	}
	if _, err := r.change(s, "repo-1", control.RepositoryChange{MergeMethod: &rebase}, r.Actor); !errors.Is(err, ledger.ErrRewritesCommits) {
		return Violatedf("a rebase repository that starts from accepted work: %v", err)
	}
	record, err := r.theRepository(s, "repo-1")
	if err != nil || record.MergeMethod != ledger.MergeCommit || record.StartFrom != ledger.StartFromAccepted {
		return Violatedf("a refused change left %+v: %v", record, err)
	}
	return nil
}

func checkRepositoryRefusesBadSettings(r *Rig) error {
	s := r.Store()
	if _, err := r.detect(s, "repo-1", "", "main"); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	method, start := ledger.MergeMethod("fast-forward"), ledger.StartFrom("whenever")
	for name, change := range map[string]control.RepositoryChange{
		"a merge method": {MergeMethod: &method}, "a start": {StartFrom: &start},
		"a base with a dash": {Base: text("-rf")}, "a base with a space": {Base: text("a b")},
	} {
		if _, err := r.change(s, "repo-1", change, r.Actor); !errors.Is(err, ledger.ErrInvalidRepository) {
			return Violatedf("%s that nobody offers: %v", name, err)
		}
	}
	if _, err := r.detect(s, "", "", "main"); err == nil {
		return Violatedf("a record with no identity was accepted")
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a refused change moved the ledger from %d to %d: %v", before, after, err)
	}
	return nil
}

func checkRepositoryIsScoped(r *Rig) error {
	s := r.Store()
	other := ledger.WorkspaceID("workspace-b")
	if _, err := s.CreateWorkspace(r.Ctx, ledger.Workspace{ID: other, Name: "B"}, r.Actor); err != nil {
		return err
	}
	if _, err := r.detect(s, "repo-1", "", "main"); err != nil {
		return err
	}
	err := s.Read(r.Ctx, other, func(tx ports.ReadTx) error {
		if _, err := tx.Repositories().Repository("repo-1"); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("workspace b read workspace a's repository: %v", err)
		}
		all, err := tx.Repositories().Repositories()
		if err != nil || len(all) != 0 {
			return Violatedf("workspace b lists %+v: %v", all, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	in, err := control.NewRepositories(s).Detect(r.Ctx, other, control.DetectedRepository{ID: "repo-1", DefaultBranch: "trunk"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	if err != nil || in.DefaultBranch != "trunk" {
		return Violatedf("workspace b made %+v: %v", in, err)
	}
	if kept, err := r.theRepository(s, "repo-1"); err != nil || kept.DefaultBranch != "main" {
		return Violatedf("workspace b changed workspace a's record to %+v: %v", kept, err)
	}
	return nil
}

func checkIntakeCarriesAWorkOrdersBase(r *Rig) error {
	s := r.Store()
	files := fake.NewWorkSource()
	files.Write("plain.md", []byte(ContractText("plain", "Plain", "Follow the repository's base.")))
	files.Write("fix.md", []byte(strings.Replace(ContractText("fix", "Fix", "Build on the release."), `size = "small"`, "size = \"small\"\nbase = \"release/1.2\"", 1)))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	fix, err := r.orderByTitle(s, "The work order of fix")
	if err != nil {
		return err
	}
	if fix.Base != "release/1.2" {
		return Violatedf("a work order that named its base reads %q", fix.Base)
	}
	if plain, err := r.orderByTitle(s, "The work order of plain"); err != nil || plain.Base != "" {
		return Violatedf("a work order that named none reads %q: %v", plain.Base, err)
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	rebuilt, err := r.orderByTitle(s, "The work order of fix")
	if err != nil || rebuilt.Base != "release/1.2" {
		return Violatedf("a rebuild left base %q: %v", rebuilt.Base, err)
	}
	return nil
}

func checkIntakeRecordsTheRepository(r *Rig) error {
	s := r.Store()
	files := fake.NewWorkSource()
	files.Write("a.md", []byte(ContractText("a", "A", "First.")))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	if all, err := r.listRepositories(s); err != nil || len(all) != 0 {
		return Violatedf("a source with no repository made %+v: %v", all, err)
	}
	files.SetRepository("https://example.invalid/acme/app.git", "main")
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	// The fake names its repository "memory", as a source does by its history.
	record, err := r.theRepository(s, "memory")
	if err != nil {
		return Violatedf("a source with facts made no record: %v", err)
	}
	if record.Remote != "https://example.invalid/acme/app.git" || record.DefaultBranch != "main" || record.MergeMethod != ledger.MergeCommit || record.Integration {
		return Violatedf("the record reads %+v", record)
	}
	on := true
	if _, err := r.change(s, "memory", control.RepositoryChange{Integration: &on}, r.Actor); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("reading a quiet source again moved the ledger from %d to %d: %v", before, after, err)
	}
	files.SetRepository("https://example.invalid/acme/renamed.git", "trunk")
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	moved, err := r.theRepository(s, "memory")
	if err != nil || moved.DefaultBranch != "trunk" || moved.Remote != "https://example.invalid/acme/renamed.git" || !moved.Integration {
		return Violatedf("after the facts moved the record reads %+v: %v", moved, err)
	}
	return nil
}

func (r *Rig) listRepositories(s ports.Store) ([]ledger.Repository, error) {
	var all []ledger.Repository
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		all, err = tx.Repositories().Repositories()
		return err
	})
	return all, err
}
