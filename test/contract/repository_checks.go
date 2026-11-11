package contract

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
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
	{"RB10", "a merge authority starts at person_merges, is a member's setting, and is one audited event", checkMergeAuthorityIsAMembersSetting},
	{"RB8", "intake carries a work order's base to the work order, and a rebuild from events keeps it", checkIntakeCarriesAWorkOrdersBase},
	{"RB11", "a repository is named by its clone's directory, or by its remote when that name is taken, and a member renames it with one audited event to a name no other repository holds", checkRepositoryNames},
	{"RB12", "a work order's repo names a repository by its name, and an unknown name keeps the work from ready until a repository takes it", checkWorkNamesItsRepository},
	{"RB13", "a repository leaves its project only once no open work targets it, and its record stays as history with its name, which no work can take", checkRepositoryRemoval},
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
	want.Name, want.Remote, want.DefaultBranch = "app", "https://example.invalid/acme/app.git", "main"
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
	pullRequest := ledger.ReviewOnPullRequest
	if _, err := r.change(s, "repo-1", control.RepositoryChange{Base: text("release/1.2"), MergeMethod: &squash, ReviewTarget: &pullRequest}, r.Actor); err != nil {
		return err
	}
	refreshed, err := r.detect(s, "repo-1", "git@example.invalid:acme/app.git", "trunk")
	if err != nil {
		return err
	}
	if refreshed.Remote != "git@example.invalid:acme/app.git" || refreshed.DefaultBranch != "trunk" {
		return Violatedf("a later read left the facts at %+v", refreshed)
	}
	if refreshed.BaseBranch != "release/1.2" || refreshed.MergeMethod != ledger.MergeSquash || refreshed.Target() != ledger.ReviewOnPullRequest || refreshed.Base() != "release/1.2" {
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
	pullRequest := ledger.ReviewOnPullRequest
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "worker-1"}, {Kind: ledger.ActorLane, ID: "lane-1"},
		{Kind: ledger.ActorWorker, ID: "worker-1"}, {Kind: ledger.ActorSystem, ID: "intake"},
	} {
		if _, err := r.change(s, "repo-1", control.RepositoryChange{ReviewTarget: &pullRequest}, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s changing a repository: %v", actor.Kind, err)
		}
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a refused caller moved the ledger from %d to %d: %v", before, after, err)
	}
	if record, err := r.theRepository(s, "repo-1"); err != nil || record.Target() != ledger.ReviewOnBranch {
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
	method, start, level := ledger.MergeMethod("fast-forward"), ledger.StartFrom("whenever"), ledger.MergeAuthority("whenever")
	target := ledger.ReviewTarget("merge_queue")
	for name, change := range map[string]control.RepositoryChange{
		"a merge method": {MergeMethod: &method}, "a start": {StartFrom: &start}, "a merge authority": {MergeAuthority: &level},
		"a review target":    {ReviewTarget: &target},
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
	// A store whose installation keeps the repository index refuses the same repository in a second
	// workspace (FR-PROJ-6). One with no installation keeps a record in each.
	in, err := control.NewRepositories(s).Detect(r.Ctx, other, control.DetectedRepository{ID: "repo-1", DefaultBranch: "trunk"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	if !errors.Is(err, ledger.ErrRepositoryHeld) && (err != nil || in.DefaultBranch != "trunk") {
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
	if record.Remote != "https://example.invalid/acme/app.git" || record.DefaultBranch != "main" || record.MergeMethod != ledger.MergeCommit || record.ReviewTarget != ledger.ReviewOnBranch {
		return Violatedf("the record reads %+v", record)
	}
	pullRequest := ledger.ReviewOnPullRequest
	if _, err := r.change(s, "memory", control.RepositoryChange{ReviewTarget: &pullRequest}, r.Actor); err != nil {
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
	if err != nil || moved.DefaultBranch != "trunk" || moved.Remote != "https://example.invalid/acme/renamed.git" || moved.Target() != ledger.ReviewOnPullRequest {
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

func checkMergeAuthorityIsAMembersSetting(r *Rig) error {
	s := r.Store()
	made, err := r.detect(s, "repo-1", "", "main")
	if err != nil {
		return err
	}
	if made.MergeAuthority != ledger.PersonMerges {
		return Violatedf("a new record reads merge authority %q, want %q", made.MergeAuthority, ledger.PersonMerges)
	}
	level := ledger.ReviewApproves
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "worker-1"}, {Kind: ledger.ActorLane, ID: "lane-1"},
		{Kind: ledger.ActorWorker, ID: "worker-1"}, {Kind: ledger.ActorSystem, ID: "review"},
	} {
		if _, err := r.change(s, "repo-1", control.RepositoryChange{MergeAuthority: &level}, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s raising the merge authority: %v", actor.Kind, err)
		}
	}
	if record, err := r.theRepository(s, "repo-1"); err != nil || record.MergeAuthority != ledger.PersonMerges {
		return Violatedf("a refused caller left the record at %+v: %v", record, err)
	}
	changed, err := r.change(s, "repo-1", control.RepositoryChange{MergeAuthority: &level}, r.Actor)
	if err != nil || changed.MergeAuthority != ledger.ReviewApproves {
		return Violatedf("a member's change left %+v: %v", changed, err)
	}
	events, err := r.repositoryEvents(s, "repo-1")
	if err != nil {
		return err
	}
	last := events[len(events)-1]
	var data struct {
		Before ledger.Repository `json:"before"`
		After  ledger.Repository `json:"after"`
	}
	if err := json.Unmarshal(last.Data, &data); err != nil {
		return err
	}
	if last.Type != ledger.EventRepositoryChanged || last.Actor != r.Actor || data.Before.MergeAuthority != ledger.PersonMerges || data.After.MergeAuthority != ledger.ReviewApproves {
		return Violatedf("the last event is %s from %+v, saying %q then %q", last.Type, last.Actor, data.Before.MergeAuthority, data.After.MergeAuthority)
	}
	refreshed, err := r.detect(s, "repo-1", "", "trunk")
	if err != nil || refreshed.MergeAuthority != ledger.ReviewApproves {
		return Violatedf("a later read left the merge authority at %q: %v", refreshed.MergeAuthority, err)
	}
	return nil
}

func checkRepositoryNames(r *Rig) error {
	s := r.Store()
	intake := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}
	detect := func(id, remote, directory string) (ledger.Repository, error) {
		return r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: id, Remote: remote, DefaultBranch: "main", Directory: directory}, intake)
	}
	first, err := detect("repo-1", "https://example.invalid/acme/api.git", "api")
	if err != nil || first.Name != "api" {
		return Violatedf("the first clone is named %q: %v", first.Name, err)
	}
	second, err := detect("repo-2", "git@example.invalid:acme/payments-api.git", "api")
	if err != nil || second.Name != "payments-api" {
		return Violatedf("a clone whose directory name is taken is named %q: %v", second.Name, err)
	}
	events, err := r.repositoryEvents(s, "repo-2")
	if err != nil || len(events) != 1 || !strings.Contains(string(events[0].Data), `"name":"payments-api"`) {
		return Violatedf("the record's first event: %+v: %v", events, err)
	}
	if again, err := detect("repo-1", "https://example.invalid/acme/api.git", "elsewhere"); err != nil || again.Name != "api" {
		return Violatedf("a later read renamed the record to %q: %v", again.Name, err)
	}
	payments := "payments"
	renamed, err := r.change(s, "repo-2", control.RepositoryChange{Name: &payments}, r.Actor)
	if err != nil || renamed.Name != "payments" {
		return Violatedf("a member's rename left %q: %v", renamed.Name, err)
	}
	events, err = r.repositoryEvents(s, "repo-2")
	if err != nil || len(events) != 2 || events[1].Type != ledger.EventRepositoryChanged || events[1].Actor != r.Actor {
		return Violatedf("the rename's events: %+v: %v", events, err)
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	taken, empty, dash := "api", "", "-x"
	if _, err := r.change(s, "repo-2", control.RepositoryChange{Name: &taken}, r.Actor); !errors.Is(err, ledger.ErrRepositoryState) {
		return Violatedf("a name another repository holds: %v", err)
	}
	for _, name := range []*string{&empty, &dash} {
		if _, err := r.change(s, "repo-2", control.RepositoryChange{Name: name}, r.Actor); !errors.Is(err, ledger.ErrInvalidRepository) {
			return Violatedf("the name %q: %v", *name, err)
		}
	}
	if _, err := r.change(s, "repo-2", control.RepositoryChange{Name: &taken}, ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "worker-1"}); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("an orchestrator renaming a repository: %v", err)
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a refused rename moved the ledger from %d to %d: %v", before, after, err)
	}
	for name, want := range map[string]string{"payments": "repo-2", "api": "repo-1", "repo-2": "repo-2"} {
		if found, err := r.repositories(s).Resolve(r.Ctx, r.Scope, name); err != nil || found.ID != want {
			return Violatedf("%q resolves to %q: %v", name, found.ID, err)
		}
	}
	return nil
}

func checkWorkNamesItsRepository(r *Rig) error {
	s := r.Store()
	if _, err := r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: "repo-api", DefaultBranch: "main", Directory: "api"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}); err != nil {
		return err
	}
	files := fake.NewWorkSource()
	named := func(id, repo string) string {
		return strings.Replace(ContractText(id, id, "Change it."), `size = "small"`, "size = \"small\"\nrepo = \""+repo+"\"", 1)
	}
	files.Write("here.md", []byte(named("here", ".")))
	files.Write("api.md", []byte(named("api", "api")))
	files.Write("web.md", []byte(named("web", "web")))
	report, err := r.intake(s, files)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(report.Problems, func(p planning.SourceProblem) bool { return p.Path == "web.md" && strings.Contains(p.Detail, `"web"`) }) {
		return Violatedf("an unknown name is no intake problem: %+v", report.Problems)
	}
	// The fake names the repository holding its files "memory".
	for title, want := range map[string]string{"The work order of here": "memory", "The work order of api": "repo-api", "The work order of web": ""} {
		if order, err := r.orderByTitle(s, title); err != nil || order.RepositoryID != want {
			return Violatedf("%s targets %q, want %q: %v", title, order.RepositoryID, want, err)
		}
	}
	web, err := r.orderByTitle(s, "The work order of web")
	if err != nil {
		return err
	}
	result, err := r.NewOps(s).Pass(r.Ctx, r.Scope, web.ID, r.member())
	if err != nil {
		return err
	}
	if result.State == workorders.Ready || !slices.ContainsFunc(result.Failures, func(f workorders.ReadyFailure) bool { return f.Check == workorders.CheckRepository }) {
		return Violatedf("work naming a repository the project does not hold reads %+v", result)
	}
	if _, err := r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: "repo-web", DefaultBranch: "main", Directory: "web"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}); err != nil {
		return err
	}
	if _, err := r.NewOps(s).Promote(r.Ctx, r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}); err != nil {
		return err
	}
	web, err = r.orderByTitle(s, "The work order of web")
	if err != nil || web.State != workorders.Ready || web.RepositoryID != "repo-web" || web.UnknownRepository != "" {
		return Violatedf("once a repository took the name the work reads %+v: %v", web, err)
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if rebuilt, err := r.orderByTitle(s, "The work order of web"); err != nil || rebuilt.RepositoryID != "repo-web" {
		return Violatedf("a rebuild left the work targeting %q: %v", rebuilt.RepositoryID, err)
	}
	return nil
}

func checkRepositoryRemoval(r *Rig) error {
	s := r.Store()
	if _, err := r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: "repo-api", DefaultBranch: "main", Directory: "api"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}); err != nil {
		return err
	}
	work, err := r.NewOps(s).Create(r.Ctx, r.Scope, workorders.WorkOrder{ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Change the api", RepositoryID: "repo-api", MaxAttempts: 1}, r.Actor)
	if err != nil {
		return err
	}
	remove := func(actor ledger.Actor) (ledger.Repository, error) {
		return r.repositories(s).Remove(r.Ctx, r.Scope, "repo-api", nil, actor)
	}
	if _, err := remove(ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "worker-1"}); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("an orchestrator removing a repository: %v", err)
	}
	if _, err := remove(r.Actor); !errors.Is(err, ledger.ErrRepositoryState) || !strings.Contains(err.Error(), work.ID) {
		return Violatedf("removing a repository open work targets: %v", err)
	}
	if err := r.NewOps(s).Cancel(r.Ctx, r.Scope, work.ID, "not needed", r.Actor); err != nil {
		return err
	}
	removed, err := remove(r.Actor)
	if err != nil || removed.State != ledger.RepositoryRemoved || removed.Name != "api" {
		return Violatedf("the removal left %+v: %v", removed, err)
	}
	if record, err := r.theRepository(s, "repo-api"); err != nil || record.State != ledger.RepositoryRemoved {
		return Violatedf("the record as history reads %+v: %v", record, err)
	}
	events, err := r.repositoryEvents(s, "repo-api")
	if err != nil || events[len(events)-1].Type != ledger.EventRepositoryRemoved || events[len(events)-1].Actor != r.Actor {
		return Violatedf("the removal's event: %+v: %v", events, err)
	}
	if _, err := remove(r.Actor); !errors.Is(err, ledger.ErrRepositoryState) {
		return Violatedf("removing a removed repository: %v", err)
	}
	// A contract that names the removed repository names none the project holds.
	files := fake.NewWorkSource()
	files.Write("api.md", []byte(strings.Replace(ContractText("api", "api", "Change it."), `size = "small"`, "size = \"small\"\nrepo = \"api\"", 1)))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	if order, err := r.orderByTitle(s, "The work order of api"); err != nil || order.RepositoryID != "" || order.UnknownRepository != "api" {
		return Violatedf("work naming a removed repository reads %+v: %v", order, err)
	}
	// A source still reading it is told, and records nothing about it.
	if _, err := r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: "repo-api", Remote: "https://example.invalid/acme/api.git", DefaultBranch: "main"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}); !errors.Is(err, ledger.ErrRepositoryState) {
		return Violatedf("reading a removed repository: %v", err)
	}
	return nil
}
