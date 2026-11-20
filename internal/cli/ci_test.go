package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// ciPullRequestHead is the commit the pull requests of these tests are at, as git names one.
const ciPullRequestHead = "abc123abc123abc123abc123abc123abc123abc1"

// ciPullRequestOn keeps an open pull request for a work order whose one required run is in progress and was first
// held an age ago.
func ciPullRequestOn(t *testing.T, s *session, id string, age time.Duration) {
	t.Helper()
	err := s.daemon.Store.Write(t.Context(), apitest.Scope, "test.pull_request", func(tx ports.WriteTx) error {
		err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{
			PullRequest: ledger.PullRequest{
				Number: 7, URL: "https://forge.example/acme/app/pull/7", State: ledger.PullRequestOpen, Base: "main", Branch: "lane/" + id, HeadCommit: ciPullRequestHead,
				Checks: []ledger.PullRequestCheck{{Name: "CI", State: ledger.CheckPending, Status: ledger.CIRunInProgress, Required: true}},
			},
			WorkOrder: id, Repository: aRepositoryID, Worker: "worker-a", ReadAt: time.Now().Add(-age).UnixMilli(),
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventPullRequestUpdated, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: ledger.SubjectPullRequest, ID: id},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// `measures ci` says how long each repository's CI takes, and that a repository with too few heads has no history, with
// how many it has and what the keys expect meanwhile.
func TestMeasuresCIPrintsEachRepositorysDuration(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	name := strings.TrimSpace(s.must("repository", "show", aRepositoryID, "--json"))
	var repository ledger.Repository
	if err := json.Unmarshal([]byte(name), &repository); err != nil {
		t.Fatal(err)
	}
	if text := s.must("measures", "ci"); !strings.Contains(text, repository.Name+"\tno history\t0 of 5 heads\texpects p50 10m0s p85 20m0s\n") {
		t.Errorf("a repository with no records prints:\n%s", text)
	}
	err := s.daemon.Store.Write(t.Context(), apitest.Scope, "test.ci_records", func(tx ports.WriteTx) error {
		for i := range 5 {
			done := time.Now().Add(-time.Duration(i) * time.Hour).UnixMilli()
			record := ledger.CIRecord{
				Repository: aRepositoryID, Head: "head-" + string(rune('a'+i)), Attempt: 1, PushedAt: done - 240_000, FirstStart: done - 180_000, LastCompleted: done,
				QueueMs: 60_000, DurationMs: 240_000, Result: ledger.CIPass,
			}
			if _, err := tx.Forge().InsertCIRecord(record); err != nil {
				return err
			}
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.ci_records", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: aRepositoryID}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if text := s.must("measures", "ci"); !strings.Contains(text, repository.Name+"\t5 heads\tp50 4m0s\tp85 4m0s\tqueue p50 1m0s\tqueue p85 1m0s\n") {
		t.Errorf("a repository with five records prints:\n%s", text)
	}
}

// `work ci-wait` and `work ci-rerun` answer the item "CI has not finished" with the result the web UI's buttons have, a
// refusal names why, and `needs` lists the item with the commands.
func TestWorkCIWaitAndRerunAnswerTheItem(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	s.completed("wo")
	ciPullRequestOn(t, s, "wo", 4*time.Hour)
	needs := s.must("needs")
	if !strings.Contains(needs, "ci\two:ci_not_finished") {
		t.Fatalf("needs does not list the item:\n%s", needs)
	}
	if code, _, stderr := s.run("work", "ci-wait", "wo", "--by", "-5m"); code != cli.ExitUsage {
		t.Errorf("a negative time exited %d: %s", code, stderr)
	}
	if code, _, stderr := s.run("work", "ci-wait", "wo", "--by", "500us"); code == cli.ExitOK || !strings.Contains(stderr, "from 1s to") {
		t.Errorf("a time under a second exited %d: %s", code, stderr)
	}
	text := s.must("work", "ci-wait", "wo", "--by", "2h")
	if !strings.Contains(text, "wo\twaits until 5h0m0s of CI") {
		t.Errorf("Wait longer by 2 hours printed %q", text)
	}
	if strings.Contains(s.must("needs"), "wo:ci_not_finished") {
		t.Error("the item stayed after Wait longer")
	}
	if code, _, stderr := s.run("work", "ci-wait", "wo"); code == cli.ExitOK || !strings.Contains(stderr, "raises none") {
		t.Errorf("Wait longer for a head that raises nothing exited %d: %s", code, stderr)
	}
	if code, _, stderr := s.run("work", "ci-rerun", "wo"); code == cli.ExitOK || !strings.Contains(stderr, "raises none") {
		t.Errorf("a rerun of a head that raises nothing exited %d: %s", code, stderr)
	}
	s.completed("wo-2")
	ciPullRequestOn(t, s, "wo-2", 4*time.Hour)
	var rerun api.CIRerun
	if err := json.Unmarshal([]byte(s.must("work", "ci-rerun", "wo-2", "--json")), &rerun); err != nil || rerun.Reruns != 1 || rerun.WorkOrder != "wo-2" {
		t.Errorf("a rerun answered %+v: %v", rerun, err)
	}
	if strings.Contains(s.must("needs"), "wo-2:ci_not_finished") {
		t.Error("the item stayed after a rerun")
	}
}
