package control_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// templateRig is the plane rig with the use case that asks its workers to write a shape's starting files.
type templateRig struct {
	*planeRig
	writes *control.TemplateWrites
}

func newTemplateRig(t *testing.T, protocol int) *templateRig {
	t.Helper()
	r := newPlaneRig(t, protocol)
	local := func(context.Context) (string, error) { return "worker-1", nil }
	return &templateRig{planeRig: r, writes: control.NewTemplateWrites(r.state.Installation, 0, local)}
}

// ask makes a member's request of worker-1.
func (r *templateRig) ask(in ledger.TemplateWriteRequest) (ledger.TemplateWriteView, error) {
	return r.writes.Request(r.t.Context(), "worker-1", in, projectMember)
}

func (r *templateRig) mustAsk(in ledger.TemplateWriteRequest) ledger.TemplateWriteView {
	r.t.Helper()
	view, err := r.ask(in)
	if err != nil {
		r.t.Fatal(err)
	}
	return view
}

// events lists the types of the installation's log.
func (r *templateRig) events() []string {
	r.t.Helper()
	var types []string
	err := r.state.Installation.Read(r.t.Context(), func(tx ports.SharedReadTx) error {
		events, err := tx.Events(0, 1000)
		for _, event := range events {
			types = append(types, event.Type)
		}
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return types
}

func (r *templateRig) revision() int64 {
	r.t.Helper()
	var rev int64
	err := r.state.Installation.Read(r.t.Context(), func(tx ports.SharedReadTx) error {
		var err error
		rev, err = tx.Revision()
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return rev
}

// inserted puts a request into the installation as it would stand, whatever its expiry, so a test reads what the
// control plane does with a request of another time.
func (r *templateRig) inserted(record ledger.TemplateWriteRecord) {
	r.t.Helper()
	err := r.state.Installation.Write(r.t.Context(), "test.template_write", func(tx ports.SharedTx) error {
		if err := tx.TemplateWrites().Insert(record); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.inserted", Actor: projectMember, Subject: ledger.Subject{Kind: "template_write", ID: record.ID}})
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// secondWorker registers another worker on a protocol and opens its session.
func (r *planeRig) secondWorker(protocol int) ledger.WorkerAuth {
	r.t.Helper()
	token, err := r.plane.CreateJoinToken(r.t.Context(), projectMember)
	if err != nil {
		r.t.Fatal(err)
	}
	credential, err := r.plane.Register(r.t.Context(), ledger.Registration{JoinToken: token, WorkerID: "worker-2", Name: "second"})
	if err != nil {
		r.t.Fatal(err)
	}
	auth := ledger.WorkerAuth{WorkerID: "worker-2", Credential: credential.Credential}
	if _, err := r.plane.OpenSession(r.t.Context(), ledger.SessionRequest{Auth: auth, Protocol: protocol, Manifest: apiKeyManifest}); err != nil {
		r.t.Fatal(err)
	}
	return auth
}

var monorepoRequest = ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/code/harbor"}

// A request is offered to the worker it names and to no other, with its fields and its expiry two minutes on, and
// the revision the worker reads moves, so its next heartbeat fetches it.
func TestARequestIsOfferedToItsWorkerOnly(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	other := r.secondWorker(ledger.ProtocolVersion)
	before := r.desired()
	revision := r.revision()
	view := r.mustAsk(ledger.TemplateWriteRequest{
		Shape: "workspace", Path: "/srv/code/harbor", FirstCommit: true,
		Repositories: []ledger.TemplateWriteRepository{{Name: "api", Remote: "git@example.com:acme/api.git"}},
	})
	if view.State != ledger.TemplateWriteOffered || view.ExpiresAt-view.RequestedAt != 2*60*1000 || view.RequestedBy != projectMember.ID || view.Worker != "worker-1" {
		t.Fatalf("the request is %+v", view)
	}
	if got := r.revision(); got <= revision {
		t.Errorf("the installation's revision is %d after the request, and was %d", got, revision)
	}
	desired := r.desired()
	if desired.Rev == before.Rev {
		t.Errorf("the desired revision stayed at %d, so the worker's next heartbeat would not fetch the request", desired.Rev)
	}
	if len(desired.TemplateWrites) != 1 {
		t.Fatalf("worker-1 is offered %+v", desired.TemplateWrites)
	}
	offered := desired.TemplateWrites[0]
	if offered.ID != view.ID || offered.Shape != "workspace" || offered.Path != "/srv/code/harbor" || !offered.FirstCommit || offered.ExpiresAt != view.ExpiresAt ||
		len(offered.Repositories) != 1 || offered.Repositories[0].Name != "api" {
		t.Errorf("the worker is offered %+v", offered)
	}
	got, err := r.plane.Desired(t.Context(), ledger.DesiredRequest{Auth: other})
	if err != nil || len(got.TemplateWrites) != 0 {
		t.Errorf("worker-2 is offered %+v (%v)", got.TemplateWrites, err)
	}
	if types := r.events(); !slices.Contains(types, "template_write.requested") {
		t.Errorf("the log holds %v, and no request", types)
	}
}

// A request is offered only until it expires, and a worker that reports it after that is still heard, because the
// files exist.
func TestAnExpiredRequestIsNotOfferedAndAnEarlyReportIsStillKept(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	r.inserted(ledger.TemplateWriteRecord{
		ID: "late", Worker: "worker-1", RequestedBy: "m", Request: monorepoRequest, RequestedAt: 1, ExpiresAt: 2, State: ledger.TemplateWriteOffered,
	})
	if offered := r.desired().TemplateWrites; len(offered) != 0 {
		t.Errorf("an expired request is offered: %+v", offered)
	}
	view, err := r.writes.View(t.Context(), "worker-1", "late")
	if err != nil || view.State != ledger.TemplateWriteExpired {
		t.Fatalf("the request reads %+v (%v), want expired", view, err)
	}
	report := ledger.TemplateWriteReport{Auth: r.auth, Request: "late", Outcome: ledger.TemplateOutcomeWritten, Project: "harbor", Files: []string{"AGENTS.md"}, Clone: true}
	if err := r.plane.ReportTemplateWrite(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	view, err = r.writes.View(t.Context(), "worker-1", "late")
	if err != nil || view.State != ledger.TemplateWriteWritten || view.Report == nil || view.Report.Project != "harbor" {
		t.Fatalf("the request reads %+v (%v), want written", view, err)
	}
	if !view.Registrable {
		t.Error("a clone on the daemon's own worker is not registrable")
	}
}

// The first report of a request is recorded, a later one is accepted and changes nothing, and each report and
// request appends one event naming the member, the worker, the shape, and the folder.
func TestAReportIsRecordedOnceWithOneEventEach(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	view := r.mustAsk(monorepoRequest)
	first := ledger.TemplateWriteReport{Auth: r.auth, Request: view.ID, Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedNotEmpty, Detail: "the folder holds files"}
	if err := r.plane.ReportTemplateWrite(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	revision := r.revision()
	second := ledger.TemplateWriteReport{Auth: r.auth, Request: view.ID, Outcome: ledger.TemplateOutcomeWritten, Project: "harbor"}
	if err := r.plane.ReportTemplateWrite(t.Context(), second); err != nil {
		t.Fatalf("a second report is refused: %v", err)
	}
	if r.revision() != revision {
		t.Error("a second report moved the revision")
	}
	got, err := r.writes.View(t.Context(), "worker-1", view.ID)
	if err != nil || got.State != ledger.TemplateWriteRefused || got.Report.Reason != ledger.TemplateRefusedNotEmpty || got.Registrable {
		t.Errorf("the request reads %+v (%v), want the first report", got, err)
	}
	if offered := r.desired().TemplateWrites; len(offered) != 0 {
		t.Errorf("a reported request is still offered: %+v", offered)
	}
	count := map[string]int{}
	for _, kind := range r.events() {
		count[kind]++
	}
	if count["template_write.requested"] != 1 || count["template_write.reported"] != 1 {
		t.Errorf("the log holds %v, want one request and one report", count)
	}
	var data string
	err = r.state.Installation.Read(t.Context(), func(tx ports.SharedReadTx) error {
		events, err := tx.Events(0, 1000)
		for _, event := range events {
			if event.Type == "template_write.requested" {
				data = string(event.Data)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{projectMember.ID, "worker-1", "monorepo", "/srv/code/harbor"} {
		if !strings.Contains(data, want) {
			t.Errorf("the request's event %s does not name %q", data, want)
		}
	}
}

// A worker reports only its own requests, with its own credential, and one word that is neither outcome is refused.
func TestOnlyTheWorkerAskedReportsARequest(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	other := r.secondWorker(ledger.ProtocolVersion)
	view := r.mustAsk(monorepoRequest)
	err := r.plane.ReportTemplateWrite(t.Context(), ledger.TemplateWriteReport{Auth: other, Request: view.ID, Outcome: ledger.TemplateOutcomeWritten})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("another worker's report: %v, want not found", err)
	}
	err = r.plane.ReportTemplateWrite(t.Context(), ledger.TemplateWriteReport{Auth: ledger.WorkerAuth{WorkerID: "worker-1", Credential: "wrong"}, Request: view.ID, Outcome: ledger.TemplateOutcomeWritten})
	if !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("a report with a wrong credential: %v", err)
	}
	err = r.plane.ReportTemplateWrite(t.Context(), ledger.TemplateWriteReport{Auth: r.auth, Request: view.ID, Outcome: "maybe"})
	if !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("a report with an outcome of maybe: %v", err)
	}
	if got, err := r.writes.View(t.Context(), "worker-1", view.ID); err != nil || got.State != ledger.TemplateWriteOffered {
		t.Errorf("the request reads %+v (%v), and nothing settled it", got, err)
	}
	if _, err := r.writes.View(t.Context(), "worker-2", view.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("another worker's view of the request: %v", err)
	}
}

// A worker on protocol 4, which NFR-COMPAT-2 still accepts, is offered no template write, and the request for it is
// refused with the upgrade it needs.
func TestAVersion4WorkerIsAcceptedAndOfferedNoTemplateWrite(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolTemplateWrites-1)
	_, err := r.ask(monorepoRequest)
	if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "speaks protocol 4; upgrade it to write files from the web UI") {
		t.Errorf("the request for a version 4 worker: %v", err)
	}
	r.inserted(ledger.TemplateWriteRecord{ID: "kept", Worker: "worker-1", RequestedBy: "m", Request: monorepoRequest, RequestedAt: 1, ExpiresAt: 1 << 60, State: ledger.TemplateWriteOffered})
	if offered := r.desired().TemplateWrites; len(offered) != 0 {
		t.Errorf("a worker on protocol 4 is offered %+v", offered)
	}
	if _, err := r.plane.Heartbeat(t.Context(), ledger.Heartbeat{Auth: r.auth, Epoch: r.epoch, Protocol: ledger.ProtocolTemplateWrites - 1, Nonce: "n"}); err != nil {
		t.Errorf("a heartbeat of a worker on protocol 4: %v", err)
	}
}

// A request that no worker could carry out is refused before anything is recorded.
func TestARequestNoWorkerCouldCarryOutIsRefusedBeforeItIsRecorded(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	cases := map[string]ledger.TemplateWriteRequest{
		"another shape":          {Shape: "service", Path: "/srv/harbor"},
		"a relative path":        {Shape: "monorepo", Path: "srv/harbor"},
		"a parent element":       {Shape: "monorepo", Path: "/srv/../harbor"},
		"a trailing slash":       {Shape: "monorepo", Path: "/srv/harbor/"},
		"a double slash":         {Shape: "monorepo", Path: "/srv//harbor"},
		"a leading double slash": {Shape: "monorepo", Path: "//srv/harbor"},
		"a dot element":          {Shape: "monorepo", Path: "/srv/./harbor"},
		"repositories on a repo": {Shape: "repository", Path: "/srv/harbor", Repositories: []ledger.TemplateWriteRepository{{Name: "api", Remote: "git@example.com:a/b.git"}}},
		"a name refused":         {Shape: "workspace", Path: "/srv/harbor", Repositories: []ledger.TemplateWriteRepository{{Name: "a/b", Remote: "git@example.com:a/b.git"}}},
		"a name twice": {Shape: "workspace", Path: "/srv/harbor", Repositories: []ledger.TemplateWriteRepository{
			{Name: "api", Remote: "git@example.com:a/b.git"}, {Name: "api", Remote: "git@example.com:a/c.git"}}},
		"an empty remote":         {Shape: "workspace", Path: "/srv/harbor", Repositories: []ledger.TemplateWriteRepository{{Name: "api"}}},
		"a remote that runs":      {Shape: "workspace", Path: "/srv/harbor", Repositories: []ledger.TemplateWriteRepository{{Name: "api", Remote: "ext::sh -c id"}}},
		"a project on a monorepo": {Shape: "monorepo", Path: "/srv/harbor", Project: "harbor"},
		"a project that is none":  {Shape: "repository", Path: "/srv/harbor", Project: "Not A Project"},
		"a folder with no name":   {Shape: "monorepo", Path: "/srv/___"},
		"a name that is none":     {Shape: "monorepo", Path: "/srv/harbor", Name: "Not A Name"},
		"a name with a slash":     {Shape: "workspace", Path: "/srv/harbor", Name: "a/b"},
		"a name that is too long": {Shape: "repository", Path: "/srv/harbor", Name: strings.Repeat("a", 64)},
	}
	for name, in := range cases {
		if _, err := r.ask(in); !errors.Is(err, workorders.ErrInvalid) {
			t.Errorf("%s: %v, want an invalid request", name, err)
		}
	}
	for _, kind := range r.events() {
		if strings.HasPrefix(kind, "template_write.") {
			t.Errorf("a refused request left an event of %s", kind)
		}
	}
	if offered := r.desired().TemplateWrites; len(offered) != 0 {
		t.Errorf("a refused request is offered: %+v", offered)
	}
}

// A folder at or inside a folder ropeshift keeps for itself on the daemon's own machine is refused before it is recorded.
// Another worker's machine has folders of its own, so the daemon's paths mean nothing to it.
func TestAFolderInsideRopeshiftsOwnFoldersIsRefusedForTheDaemonsOwnWorker(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	other := r.secondWorker(ledger.ProtocolVersion)
	reserving := control.NewTemplateWrites(r.state.Installation, 0, func(context.Context) (string, error) { return "worker-1", nil }).
		Reserving("/home/u/.local/state/ropeshift", "/home/u/.ropeshift/run")
	for _, path := range []string{"/home/u/.local/state/ropeshift", "/home/u/.local/state/ropeshift/harbor", "/home/u/.ropeshift/run/a/b"} {
		_, err := reserving.Request(t.Context(), "worker-1", ledger.TemplateWriteRequest{Shape: "monorepo", Path: path}, projectMember)
		if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "keeps for itself") {
			t.Errorf("%s: %v, want a refusal that the folder is ropeshift's own", path, err)
		}
	}
	for _, path := range []string{"/home/u/.local/state/ropeshift-notes", "/home/u/.local/state"} {
		if _, err := reserving.Request(t.Context(), "worker-1", ledger.TemplateWriteRequest{Shape: "monorepo", Path: path}, projectMember); err != nil {
			t.Errorf("%s is no folder of ropeshift's, and is refused: %v", path, err)
		}
	}
	if _, err := reserving.Request(t.Context(), other.WorkerID, ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/home/u/.local/state/ropeshift/harbor"}, projectMember); err != nil {
		t.Errorf("another machine's folder is refused for the daemon's paths: %v", err)
	}
}

// A worker that has not beaten lately is not connected, and a worker that does not exist is not found.
func TestARequestNeedsAConnectedWorkerThatExists(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	if _, err := r.writes.Request(t.Context(), "nobody", monorepoRequest, projectMember); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("an unknown worker: %v", err)
	}
	// A heartbeat of a millisecond leaves a session three milliseconds of freshness, which the wait outlasts.
	quiet := control.NewTemplateWrites(r.state.Installation, 1, nil)
	time.Sleep(50 * time.Millisecond)
	_, err := quiet.Request(t.Context(), "worker-1", monorepoRequest, projectMember)
	if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "is not connected") {
		t.Errorf("a worker that beat long ago: %v, want it refused as not connected", err)
	}
}

// Only a member asks. A delegate, and every other actor, is refused as a member's alone.
func TestOnlyAMemberAsksForATemplateWrite(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	for _, kind := range []ledger.ActorKind{ledger.ActorDelegate, ledger.ActorOrchestrator, ledger.ActorLane} {
		_, err := r.writes.Request(t.Context(), "worker-1", monorepoRequest, ledger.Actor{Kind: kind, ID: "x"})
		if !errors.Is(err, workorders.ErrForbidden) || !errors.Is(err, authority.ErrMemberOnly) {
			t.Errorf("%s: %v, want a refusal as the member's alone", kind, err)
		}
	}
}

// A request may name the project, on any shape, and the name stands in for the folder's: a folder that makes none is
// enough when the request names one.
func TestARequestMayNameTheProject(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	for _, shape := range []string{"workspace", "monorepo", "repository"} {
		view, err := r.ask(ledger.TemplateWriteRequest{Shape: shape, Path: "/srv/Orchid Shop", Name: "harbor"})
		if err != nil || view.Name != "harbor" {
			t.Errorf("%s: view %+v (%v), want the name harbor", shape, view, err)
		}
	}
	if view, err := r.ask(ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/___", Name: "harbor"}); err != nil || view.Name != "harbor" {
		t.Errorf("a folder that makes no name with a name given: %+v (%v)", view, err)
	}
	if view, err := r.ask(ledger.TemplateWriteRequest{Shape: "repository", Path: "/srv/code", Name: "harbor-api", Project: "harbor"}); err != nil || view.Name != "harbor-api" || view.Project != "harbor" {
		t.Errorf("a repository's name and project: %+v (%v)", view, err)
	}
}

// A worker on the protocol before ProtocolTemplateName ignores a template write's name and would write the files
// for the folder's name, so a request that names the project is refused for it, and one that does not is served.
func TestAWorkerThatCannotReadANameIsAskedForNone(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolTemplateName-1)
	_, err := r.ask(ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/harbor", Name: "harbor"})
	if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("a name for a worker on protocol %d: %v", ledger.ProtocolTemplateName-1, err)
	}
	if _, err := r.ask(ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/harbor"}); err != nil {
		t.Errorf("a request with no name: %v", err)
	}
}

// The name a request carries is kept with it: the worker is offered it, and a member reads it back.
func TestARequestsNameIsKeptAndOfferedToItsWorker(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	view := r.mustAsk(ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/Orchid Shop", Name: "harbor"})
	offered := r.desired().TemplateWrites
	if len(offered) != 1 || offered[0].Name != "harbor" {
		t.Errorf("the worker is offered %+v", offered)
	}
	read, err := r.writes.View(t.Context(), "worker-1", view.ID)
	if err != nil || read.Name != "harbor" {
		t.Errorf("the member reads %+v (%v)", read, err)
	}
}

// A request that names the project, held from before its worker went back to an older protocol, is not offered to it: the
// worker would drop the name and write the files for the folder's name.
func TestAWorkerBeforeTheNameProtocolIsOfferedNoRequestThatNamesAProject(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolTemplateName-1)
	r.inserted(ledger.TemplateWriteRecord{ID: "named", Worker: "worker-1", RequestedBy: "m", Request: ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/harbor", Name: "harbor"}, RequestedAt: 1, ExpiresAt: 1 << 60, State: ledger.TemplateWriteOffered})
	r.inserted(ledger.TemplateWriteRecord{ID: "plain", Worker: "worker-1", RequestedBy: "m", Request: ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/other"}, RequestedAt: 1, ExpiresAt: 1 << 60, State: ledger.TemplateWriteOffered})
	var ids []string
	for _, offered := range r.desired().TemplateWrites {
		ids = append(ids, offered.ID)
	}
	if !slices.Equal(ids, []string{"plain"}) {
		t.Errorf("the worker is offered %v, want only the request that names no project", ids)
	}
}
