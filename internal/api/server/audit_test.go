package server_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

func auditTypes(page api.AuditPage) []string {
	var types []string
	for _, event := range page.Events {
		types = append(types, event.Type)
	}
	return types
}

// The project's audit reads its log newest first, each event with who did it and its whole record, and
// pages back through it with the revision each page names.
func TestTheAuditReadsTheLogNewestFirst(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	project := d.Client.Project(apitest.ProjectName)
	for i := range 3 {
		createWork(t, project, fmt.Sprintf("wo-%d", i), "Ship the invoice export")
	}
	page, err := project.Audit(t.Context(), api.AuditQuery{Kind: "work_order.created", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Scope != "project" || len(page.Events) != 2 || page.Events[0].SubjectID != "wo-2" || page.Events[1].SubjectID != "wo-1" {
		t.Fatalf("first page %+v", page)
	}
	if page.Events[0].Rev <= page.Events[1].Rev || page.Head < page.Events[0].Rev || page.Next != page.Events[1].Rev {
		t.Errorf("revisions: head %d, next %d, events %d and %d", page.Head, page.Next, page.Events[0].Rev, page.Events[1].Rev)
	}
	if page.Events[0].ActorKind != string(ledger.ActorMember) || len(page.Events[0].Data) == 0 || page.Events[0].ID == "" {
		t.Errorf("an event names its actor and carries its record: %+v", page.Events[0])
	}
	older, err := project.Audit(t.Context(), api.AuditQuery{Kind: "work_order.created", Before: page.Next, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Events) != 1 || older.Events[0].SubjectID != "wo-0" || older.Next != 0 {
		t.Errorf("the second page %+v", older)
	}
}

// Each filter narrows the audit on its own, and one that matches nothing answers an empty page.
func TestTheAuditFilters(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	project := d.Client.Project(apitest.ProjectName)
	createWork(t, project, "wo-1", "Ship the invoice export")
	for _, q := range []api.AuditQuery{{Actor: "member"}, {Subject: "work_order"}, {Kind: "work_order."}} {
		page, err := project.Audit(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) == 0 {
			t.Errorf("%+v found nothing", q)
		}
		for _, event := range page.Events {
			if (q.Actor != "" && event.ActorKind != q.Actor) || (q.Subject != "" && event.SubjectKind != q.Subject) || !strings.HasPrefix(event.Type, q.Kind) {
				t.Errorf("%+v kept %+v", q, event.StreamEvent)
			}
		}
	}
	page, err := project.Audit(t.Context(), api.AuditQuery{Kind: "no.such.kind"})
	if err != nil || len(page.Events) != 0 || page.Next != 0 {
		t.Errorf("a filter with no match: %+v, %v", page, err)
	}
	for _, query := range []string{"?limit=0", "?limit=201", "?before=0", "?before=x"} {
		err := d.Client.Do(t.Context(), http.MethodGet, api.InProject(apitest.ProjectName, api.AuditPath+query), nil, nil)
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
}

// The installation's audit reads its own log, which holds the projects' lifecycle.
func TestTheInstallationAuditReadsItsOwnLog(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t)
	if _, err := in.Client.CreateProject(t.Context(), api.CreateProject{Name: "beta"}); err != nil {
		t.Fatal(err)
	}
	page, err := in.Client.InstallationAudit(t.Context(), api.AuditQuery{Subject: ledger.SubjectProject})
	if err != nil {
		t.Fatal(err)
	}
	if page.Scope != "installation" || !slices.Contains(auditTypes(page), ledger.EventProjectCreated) {
		t.Errorf("installation audit %+v", page)
	}
}

// The members list names who holds a session, never a session's token. The harness signs in with no
// member record, so how many are enrolled is not what this holds.
func TestTheMembersListNamesWhoHoldsASession(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	list, err := d.Client.Members(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Members) != 1 || len(list.Members[0].Sessions) == 0 || list.Members[0].LastSignIn == 0 {
		t.Fatalf("members %+v", list)
	}
	session := list.Members[0].Sessions[0]
	if session.ExpiresAt <= session.CreatedAt {
		t.Errorf("session %+v", session)
	}
}

// A backup a browser downloads is the project's ledger, the same copy a backup to a file writes, and it
// needs a member as that does. Nothing of it stays behind.
func TestABackupDownloadsAsTheLedger(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "acme")
	_, _, acme := in.In(t, "acme")
	createWork(t, acme, "wo-acme", "Ship the invoice export")
	raw, err := acme.DownloadBackup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "acme.db")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if rows := rowsIn(t, file, "work_orders"); !slices.Equal(rows, []string{"wo-acme"}) {
		t.Errorf("the downloaded backup holds work orders %v", rows)
	}
	whole, err := in.Client.DownloadInstallationBackup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(t.TempDir(), "installation.db")
	if err := os.WriteFile(file, whole, 0o600); err != nil {
		t.Fatal(err)
	}
	if rows := rowsIn(t, file, "projects"); !slices.Equal(rows, []string{"acme-id", string(apitest.Scope)}) {
		t.Errorf("the installation's download holds projects %v", rows)
	}
	anonymous := api.NewUnixClient(in.Socket)
	defer anonymous.Close()
	_, err = anonymous.Project("acme").DownloadBackup(t.Context())
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	_, err = anonymous.DownloadInstallationBackup(t.Context())
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}

// A backup a browser uploads registers its project as an import of a file does, under a name the upload
// chooses, and an upload with no backup or no member is refused.
func TestAnUploadedBackupImportsItsProject(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "acme")
	_, _, acme := in.In(t, "acme")
	createWork(t, acme, "wo-acme", "Ship the invoice export")
	raw, err := acme.DownloadBackup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Client.ArchiveProject(t.Context(), "acme"); err != nil {
		t.Fatal(err)
	}
	if err := in.Client.RemoveProject(t.Context(), "acme", api.RemoveProject{Confirm: "acme", NoBackup: true}); err != nil {
		t.Fatal(err)
	}
	imported, err := in.Client.ImportUploaded(t.Context(), api.ImportUpload{Backup: raw, Name: "acme-again"})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Name != "acme-again" || imported.ID != "acme-id" {
		t.Errorf("imported %+v", imported)
	}
	if got := workIDs(t, in.Client.Project("acme-again")); !slices.Equal(got, []string{"wo-acme"}) {
		t.Errorf("the imported project lists %v", got)
	}
	_, err = in.Client.ImportUploaded(t.Context(), api.ImportUpload{})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	anonymous := api.NewUnixClient(in.Socket)
	defer anonymous.Close()
	_, err = anonymous.ImportUploaded(t.Context(), api.ImportUpload{Backup: raw})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}
