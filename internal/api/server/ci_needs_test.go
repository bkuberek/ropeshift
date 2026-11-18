package server_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

const ciRunPage = "https://github.com/acme/app/actions/runs/9"

// aCIWorld is a work order with an open pull request whose one required check is at a point of its run, first held by
// the ledger an age ago, which a session that lives a few hours could not wait for.
func aCIWorld(t *testing.T, status ledger.CIRunStatus, age time.Duration) (*apitest.Daemon, *apitest.Clock) {
	t.Helper()
	d, clock, _ := aCIWorldOf(t, status, age)
	return d, clock
}

func aCIWorldOf(t *testing.T, status ledger.CIRunStatus, age time.Duration) (*apitest.Daemon, *apitest.Clock, *apitest.World) {
	t.Helper()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	w.Repository("repository-1", ledger.PersonApproves)
	aCIPullRequest(t, d, w, clock, "wo-ci", "Add the retry budget", 14, status, age)
	return d, clock, w
}

// aCIPullRequest records a work order and its open pull request, whose one required check is at a point of its run.
func aCIPullRequest(t *testing.T, d *apitest.Daemon, w *apitest.World, clock *apitest.Clock, id, title string, number int64, status ledger.CIRunStatus, age time.Duration) {
	t.Helper()
	w.Created(id, title)
	err := d.Store.Write(t.Context(), apitest.Scope, "test.pull_request", func(tx ports.WriteTx) error {
		err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{
			PullRequest: ledger.PullRequest{
				Number: number, URL: "https://github.com/acme/app/pull/" + id, State: ledger.PullRequestOpen, Base: "main", Branch: "lane/" + id, HeadCommit: "abc123-" + id,
				Checks: []ledger.PullRequestCheck{{Name: "CI", State: ledger.CheckPending, Status: status, Required: true, DetailsURI: ciRunPage}},
			},
			WorkOrder: id, Repository: "repository-1", Worker: apitest.WorldWorker, ReadAt: clock.Now() - age.Milliseconds(),
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventPullRequestUpdated, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}, Subject: ledger.Subject{Kind: ledger.SubjectPullRequest, ID: id},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestGoldenCI writes what Needs you and a work order's page read of CI that asks a person: a head past its CI
// timeout, a run no runner picked up, and each finding of the classification, from a real ledger.
func TestGoldenCI(t *testing.T) {
	t.Parallel()
	d, clock, w := aCIWorldOf(t, ledger.CIRunInProgress, 4*time.Hour)
	aCIPullRequest(t, d, w, clock, "wo-queued", "Trim the retry log", 15, ledger.CIRunQueued, 2*time.Hour)
	attention := control.NewCIAttention(d.Store)
	for _, kind := range ledger.CIItemKinds[2:] {
		finding := ledger.CIFinding{WorkOrder: "wo-ci", Head: "abc123-wo-ci", Kind: kind, Check: "CI", Conclusion: "timed_out", URL: ciRunPage}
		if _, err := attention.Raise(t.Context(), apitest.Scope, finding); err != nil {
			t.Fatal(err)
		}
	}
	holdsGolden(t, "ci-needs-you", steady(pretty(t, readAny(t, d, api.ViewNeedsYou))))
	view, err := d.Client.Acceptance(t.Context(), "wo-ci")
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "acceptance-ci", steady(pretty(t, view)))
}

func ciItems(t *testing.T, d *apitest.Daemon) []api.NeedItem {
	t.Helper()
	var out []api.NeedItem
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedCI {
			out = append(out, item)
		}
	}
	return out
}

// waitingNotices is what a tick would tell the channels of one project's Needs you.
func waitingNotices(t *testing.T, d *apitest.Daemon) []ledger.Notice {
	t.Helper()
	notices, err := server.Waiting(d.Config, "Retail")(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return notices
}

// TestGoldenCIDurations writes what Measures reads of each repository's CI: the p50 and p85 of a repository with
// history, and the no-history figures of one with too few heads.
func TestGoldenCIDurations(t *testing.T) {
	t.Parallel()
	d, clock, _ := aCIWorldOf(t, ledger.CIRunInProgress, time.Hour)
	other := control.DetectedRepository{ID: "repository-2", Remote: "https://example.invalid/acme/other.git", DefaultBranch: "main"}
	if _, err := control.NewRepositories(d.Store).Detect(t.Context(), apitest.Scope, other, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}); err != nil {
		t.Fatal(err)
	}
	err := d.Store.Write(t.Context(), apitest.Scope, "test.ci_records", func(tx ports.WriteTx) error {
		for i := range 6 {
			done := clock.Now() - int64(i)*time.Hour.Milliseconds()
			record := ledger.CIRecord{
				Repository: "repository-1", Head: "head-" + string(rune('a'+i)), Attempt: 1, PushedAt: done - (6+int64(i))*time.Minute.Milliseconds(),
				FirstStart: done - (5+int64(i))*time.Minute.Milliseconds(), LastCompleted: done, QueueMs: time.Minute.Milliseconds(),
				DurationMs: (6 + int64(i)) * time.Minute.Milliseconds(), Result: ledger.CIPass,
			}
			if _, err := tx.Forge().InsertCIRecord(record); err != nil {
				return err
			}
		}
		_, err := tx.Append(ledger.EventDraft{Type: ledger.EventPullRequestUpdated, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}, Subject: ledger.Subject{Kind: ledger.SubjectPullRequest, ID: "wo-ci"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := d.Client.CIDurations(t.Context())
	if err != nil || len(view.Repositories) != 2 {
		t.Fatalf("Measures reads %+v: %v", view, err)
	}
	with, without := view.Repositories[0], view.Repositories[1]
	if with.Repository != "repository-1" || !with.History || with.Records != 6 || with.P50Ms != (8*time.Minute).Milliseconds() || with.P85Ms != (11*time.Minute).Milliseconds() {
		t.Errorf("the repository with history reads %+v", with)
	}
	if without.History || without.Records != 0 || without.P85Ms != 0 || without.NoHistoryP85Ms != (20*time.Minute).Milliseconds() || without.MinRecords != 5 {
		t.Errorf("the repository with none reads %+v", without)
	}
	holdsGolden(t, "ci-durations", steady(pretty(t, view)))
}

// A head that passed its CI timeout is one Needs you item that offers Wait longer, Rerun, and the run's page, and a
// notice of its own kind that names no check, run, or branch.
func TestCIThatHasNotFinishedIsOneItemWithItsActionsAndANotice(t *testing.T) {
	t.Parallel()
	d, clock := aCIWorld(t, ledger.CIRunInProgress, 3*time.Hour-time.Second)
	if items := ciItems(t, d); len(items) != 0 {
		t.Fatalf("a run a second short of its timeout raises %+v", items)
	}
	clock.Advance(time.Second)
	items := ciItems(t, d)
	if len(items) != 1 {
		t.Fatalf("a run at its timeout raises %d items", len(items))
	}
	item := items[0]
	if item.ID != "wo-ci:ci_not_finished" || item.CI == nil || item.CI.Kind != ledger.CIItemNotFinished || item.CI.WorkOrder.ID != "wo-ci" || item.CI.Number != 14 {
		t.Errorf("the item reads %+v", item)
	}
	if want := []string{api.ActCIWait, api.ActCIRerun, api.ActCIOpen}; !slices.Equal(item.CI.Actions, want) || item.CI.RunURL != ciRunPage {
		t.Errorf("the item offers %v at %q", item.CI.Actions, item.CI.RunURL)
	}
	var found *ledger.Notice
	for _, notice := range waitingNotices(t, d) {
		if notice.Kind == ledger.CIItemNotFinished {
			found = &notice
		}
	}
	if found == nil || found.Text != "Retail: CI has not finished." || strings.Contains(found.Text, "abc123") || strings.Contains(found.Text, "wo-ci") {
		t.Errorf("the notice reads %+v", found)
	}
}

// Wait longer and Rerun are the member's acts through the API: each adds what it says, is recorded, and starts no
// agent, and an answer that is no time is refused.
func TestWaitLongerAndRerunAnswerTheItemThroughTheAPI(t *testing.T) {
	t.Parallel()
	d, clock := aCIWorld(t, ledger.CIRunInProgress, 3*time.Hour)
	for _, by := range []string{"soon", "500us", "-5m", "0s", "73h"} {
		if _, err := d.Client.CIWait(t.Context(), "wo-ci", api.CIWait{By: by}); err == nil {
			t.Errorf("a time of %q was taken", by)
		}
	}
	if items := ciItems(t, d); len(items) != 1 {
		t.Errorf("a refused wait changed the item: %+v", items)
	}
	waited, err := d.Client.CIWait(t.Context(), "wo-ci", api.CIWait{By: "30m"})
	if err != nil || waited.Watch.ExtendedMs != (30*time.Minute).Milliseconds() {
		t.Fatalf("Wait longer by 30 minutes: %+v, %v", waited, err)
	}
	if items := ciItems(t, d); len(items) != 0 {
		t.Errorf("the item stayed after Wait longer: %+v", items)
	}
	clock.Advance(31 * time.Minute)
	if items := ciItems(t, d); len(items) != 1 {
		t.Fatalf("the item did not return past the extended timeout: %+v", items)
	}
	rerun, err := d.Client.CIRerun(t.Context(), "wo-ci")
	if err != nil || rerun.Reruns != 1 {
		t.Fatalf("a rerun: %+v, %v", rerun, err)
	}
	if items := ciItems(t, d); len(items) != 0 {
		t.Errorf("the item stayed after a rerun: %+v", items)
	}
	view, err := d.Client.Acceptance(t.Context(), "wo-ci")
	if err != nil || view.CI == nil || view.CI.Reruns != 1 || view.CI.ExpectedMs != 0 || view.CI.History {
		t.Errorf("the work order's page reads %+v: %v", view.CI, err)
	}
}

// A run still queued past its limit is the one item "No runner picked it up", which a rerun cannot mend, so it offers
// only the run's page.
func TestAStalledQueueOffersOnlyTheRun(t *testing.T) {
	t.Parallel()
	d, _ := aCIWorld(t, ledger.CIRunQueued, 4*time.Hour)
	items := ciItems(t, d)
	if len(items) != 1 || items[0].CI.Kind != ledger.CIItemQueueStalled || !slices.Equal(items[0].CI.Actions, []string{api.ActCIOpen}) {
		t.Fatalf("a run queued for 4 hours raises %+v", items)
	}
	if _, err := d.Client.CIRerun(t.Context(), "wo-ci"); err == nil {
		t.Error("a rerun was taken for a run no runner picked up")
	}
	if _, err := d.Client.CIWait(t.Context(), "wo-ci", api.CIWait{}); err == nil {
		t.Error("Wait longer was taken for a run no runner picked up")
	}
}

// Each CI kind reaches a channel as a notice of its own kind, once, and a channel that is down leaves the item in
// Needs you.
func TestEachCIKindReachesAChannelAsItsOwnNotice(t *testing.T) {
	t.Parallel()
	d, clock := aCIWorld(t, ledger.CIRunInProgress, 4*time.Hour)
	attention := control.NewCIAttention(d.Store)
	for _, kind := range ledger.CIItemKinds[2:] {
		if _, err := attention.Raise(t.Context(), apitest.Scope, ledger.CIFinding{WorkOrder: "wo-ci", Head: "abc123-wo-ci", Kind: kind, Check: "CI"}); err != nil {
			t.Fatal(err)
		}
	}
	queued := waitingNotices(t, d)
	if _, err := d.Client.CIWait(t.Context(), "wo-ci", api.CIWait{By: "5h"}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	channel := fake.NewNotifier("desktop")
	notices := control.NewNotices(d.Store, func(authority.NotifyPolicy) []ports.Notifier { return []ports.Notifier{channel} }).RetryingAfter(0)
	if err := notices.Deliver(t.Context(), apitest.Scope, waitingNotices(t, d), ledger.Actor{Kind: ledger.ActorSystem, ID: "notify"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, notice := range channel.Notices() {
		got = append(got, notice.Kind)
	}
	slices.Sort(got)
	want := []string{ledger.CIItemActionRequired, ledger.CIItemBaseRed, ledger.CIItemBaseUnread, ledger.CIItemInfrastructure}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the channel heard %v, want %v; before Wait longer the tick held %d notices", got, want, len(queued))
	}
	if err := notices.Deliver(t.Context(), apitest.Scope, waitingNotices(t, d), ledger.Actor{Kind: ledger.ActorSystem, ID: "notify"}); err != nil {
		t.Fatal(err)
	}
	if again := len(channel.Notices()); again != len(want) {
		t.Errorf("a second tick told the channel %d notices, want %d", again, len(want))
	}
	if items := ciItems(t, d); len(items) != len(want) {
		t.Errorf("Needs you holds %d items, want %d", len(items), len(want))
	}
}

// The address of a finding's run is the forge's word, so what JSON consumers read is a link only when it is https with a
// host and no user: nothing else is emitted, in the finding or in the item's run address, and no Open the run is offered.
func TestAFindingsUnsafeAddressIsNotEmitted(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"javascript:alert(1)", "http://forge.example/runs/9", "https://user:secret@forge.example/runs/9", "https://", "ftp://forge.example/9", "https://forge.example/a b"} {
		d, _ := aCIWorld(t, ledger.CIRunInProgress, time.Hour)
		finding := ledger.CIFinding{WorkOrder: "wo-ci", Head: "abc123-wo-ci", Kind: ledger.CIItemBaseRed, Check: "CI", URL: address}
		if _, err := control.NewCIAttention(d.Store).Raise(t.Context(), apitest.Scope, finding); err != nil {
			t.Fatal(err)
		}
		items := ciItems(t, d)
		if len(items) != 1 || items[0].CI.Finding == nil {
			t.Fatalf("%q raised %+v", address, items)
		}
		if got := items[0].CI.Finding.URL; got != "" || items[0].CI.RunURL != "" || slices.Contains(items[0].CI.Actions, api.ActCIOpen) {
			t.Errorf("%q is emitted as finding url %q, run url %q, actions %v", address, got, items[0].CI.RunURL, items[0].CI.Actions)
		}
	}
	d, _ := aCIWorld(t, ledger.CIRunInProgress, time.Hour)
	finding := ledger.CIFinding{WorkOrder: "wo-ci", Head: "abc123-wo-ci", Kind: ledger.CIItemBaseRed, Check: "CI", URL: ciRunPage}
	if _, err := control.NewCIAttention(d.Store).Raise(t.Context(), apitest.Scope, finding); err != nil {
		t.Fatal(err)
	}
	if items := ciItems(t, d); len(items) != 1 || items[0].CI.Finding.URL != ciRunPage || items[0].CI.RunURL != ciRunPage {
		t.Errorf("an https address reads %+v", items)
	}
}

// Each finding of the classification is an item and a notice of its kind, with the actions its kind offers.
func TestEachFindingOfTheClassificationIsAnItemAndANotice(t *testing.T) {
	t.Parallel()
	d, _ := aCIWorld(t, ledger.CIRunInProgress, time.Hour)
	attention := control.NewCIAttention(d.Store)
	for _, kind := range ledger.CIItemKinds[2:] {
		finding := ledger.CIFinding{WorkOrder: "wo-ci", Head: "abc123-wo-ci", Kind: kind, Check: "CI", Conclusion: "timed_out", URL: ciRunPage}
		if raised, err := attention.Raise(t.Context(), apitest.Scope, finding); err != nil || !raised {
			t.Fatalf("%s raised %v: %v", kind, raised, err)
		}
	}
	want := map[string][]string{
		ledger.CIItemInfrastructure: {api.ActCIRerun, api.ActCIOpen},
		ledger.CIItemBaseRed:        {api.ActCIOpen},
		ledger.CIItemBaseUnread:     {api.ActCIOpen},
		ledger.CIItemActionRequired: {api.ActCIOpen},
	}
	items := ciItems(t, d)
	if len(items) != len(want) {
		t.Fatalf("four findings raise %+v", items)
	}
	for _, item := range items {
		if !slices.Equal(item.CI.Actions, want[item.CI.Kind]) || item.CI.Finding == nil || item.CI.Finding.Conclusion != "timed_out" {
			t.Errorf("%s reads %+v", item.CI.Kind, item.CI)
		}
	}
	texts := map[string]string{}
	for _, notice := range waitingNotices(t, d) {
		texts[notice.Kind] = notice.Text
	}
	words := map[string]string{
		ledger.CIItemInfrastructure: "Retail: CI failed for a reason an agent cannot fix.",
		ledger.CIItemBaseRed:        "Retail: A check fails on the base branch too.",
		ledger.CIItemBaseUnread:     "Retail: The base branch's checks cannot be read.",
		ledger.CIItemActionRequired: "Retail: A check waits for a person on the forge.",
	}
	for kind, text := range words {
		if texts[kind] != text {
			t.Errorf("%s has the notice %q, want %q", kind, texts[kind], text)
		}
	}
}
