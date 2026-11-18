package worker_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// What a check says about itself is untrusted: its address is whatever the app that made the check wrote. The tests of
// this file hold the two things that must never follow from it: an action on a run of another head, and a character of
// the address that is not the address the brief is given.

// foreignRun is a run of the repository that belongs to a commit under no review, as a deploy on the base would.
const foreignHead = "0123456789abcdef0123456789abcdef01234567"

// eventsData lists the data of the events of a type about the work order's pull request.
func (f *forgeRig) eventsData(eventType string) []string {
	f.t.Helper()
	var out []string
	err := f.store.Read(f.t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectPullRequest, ID: f.id})
		for _, event := range events {
			if event.Type == eventType {
				out = append(out, string(event.Data))
			}
		}
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// B1: a check whose address names a run of another head is never rerun, and its refusal is recorded and raised.
func TestACheckNamingARunOfAnotherHeadIsNeverRerun(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	check := f.finishedNow("CI", "cancelled", "990")
	f.forge.SetRun("990", foreignHead)
	f.forge.SetChecks(number, check)
	f.settle()
	if f.reruns() != 0 {
		t.Fatalf("a run of another head was rerun: %v", f.forge.Reruns())
	}
	done := f.eventsData(ledger.EventCIRerunDone)
	if len(done) != 1 || !strings.Contains(done[0], "not a run of") {
		t.Fatalf("the refusal was recorded as %v", done)
	}
	if kinds := f.ciKinds(); len(kinds) != 1 || kinds[0] != ledger.CIItemInfrastructure {
		t.Fatalf("a refused rerun raises %v, want %s", kinds, ledger.CIItemInfrastructure)
	}
}

// B1: a check whose address names a run of another head gives the brief no log, and says it refused.
func TestACheckNamingARunOfAnotherHeadGivesNoLog(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	check := f.finishedNow("unit", "failure", "991")
	f.forge.SetRun("991", foreignHead)
	f.forge.SetLog("unit", "DEPLOY SECRET LINE from another run\n")
	f.forge.SetChecks(number, check)
	f.settle()
	if got := f.row().State; got != workorders.Ready {
		t.Fatalf("a test failure left the work at %s", got)
	}
	reason := f.reworkReason()
	if strings.Contains(reason, "DEPLOY SECRET") || strings.Contains(reason, "```") {
		t.Errorf("a log of another head's run reached the brief:\n%s", reason)
	}
	logged := f.eventsData(ledger.EventCILogRead)
	if len(logged) != 1 || !strings.Contains(logged[0], "unit") || !strings.Contains(logged[0], "refused") {
		t.Errorf("the refusal was recorded as %v", logged)
	}
}

// B2: the address of a check reaches the brief only as the canonical address of its run, whatever its query, its
// fragment, or its characters hold.
func TestAHostileCheckAddressNeverReachesTheBrief(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	tags := string([]rune{0xE0049, 0xE0067, 0xE006E, 0xE006F, 0xE0072, 0xE0065})
	bidi := string([]rune{0x202E, 0x2066, 0x200B})
	check := f.finishedNow("unit", "failure", "707")
	check.DetailsURI += "?payload=" + tags + bidi + "#" + tags + bidi
	f.forge.SetRun("707", "fake-head")
	f.forge.SetLog("unit", "FAIL TestWidget\n")
	f.forge.SetChecks(number, check)
	f.settle()
	reason := f.reworkReason()
	for _, bad := range []string{"?", "#", "payload"} {
		if strings.Contains(reason, bad) {
			t.Errorf("the brief holds %q from the address:\n%s", bad, reason)
		}
	}
	for _, r := range reason {
		if r > 0x7e && r != '\n' {
			t.Errorf("the brief holds the character %U outside ASCII", r)
		}
	}
	if !strings.Contains(reason, "(https://github.com/acme/widgets/actions/runs/707/job/7)") {
		t.Errorf("the brief does not give the canonical address of the run:\n%s", reason)
	}
}

// F3: a base the forge will not let the worker read does not fail the read of the pull request. The failed check is
// held, where nothing is sent back and nothing is rerun, the repository is told once, and a read that can tell decides.
func TestAnUnreadableBaseHoldsAFailedCheckAndIsToldOnce(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	sessions := len(f.driver.Specs())
	f.forge.FailBase(errors.New("the forge refused the base"))
	f.forge.SetLog("unit", "FAIL TestWidget\n")
	f.forge.SetChecks(number, f.finishedNow("unit", "failure", "611"))
	f.settle()
	f.settle()
	if got := f.row().State; got != workorders.Integrating {
		t.Fatalf("a failure nobody can place sent the work to %s", got)
	}
	if countPrefix(f.forge.Calls(), "log ") != 0 || f.reruns() != 0 {
		t.Fatalf("a held check read a log or asked a rerun: %v", f.forge.Calls())
	}
	f.noSession(sessions)
	pr, err := f.pullRequest()
	if err != nil || len(pr.Checks) != 1 || !pr.Checks[0].BaseUnread {
		t.Fatalf("the ledger holds %+v: %v", pr.Checks, err)
	}
	told := 0
	for _, notice := range f.notices() {
		if strings.Contains(notice, "base branch's checks cannot be read") && strings.Contains(notice, "repo-1") {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("the repository was told %d times, want once: %q", told, f.notices())
	}
	// The base can be read again, and the failure is the code's.
	f.forge.FailBase(nil)
	f.settle()
	if got := f.row().State; got != workorders.Ready {
		t.Fatalf("once the base was read, the failure left the work at %s", got)
	}
}

// F3: and one the base shares, once it can be read, is raised and starts nothing.
func TestAnUnreadableBaseThatIsRedIsRaisedOnceItCanBeRead(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	f.forge.FailBase(errors.New("the forge refused the base"))
	f.forge.SetBase(widgets, "main", "base-tip-9", "unit")
	f.forge.SetChecks(number, f.finishedNow("unit", "failure", "612"))
	f.settle()
	if kinds := f.ciKinds(); len(kinds) != 0 {
		t.Fatalf("a held check raises %v", kinds)
	}
	f.forge.FailBase(nil)
	f.settle()
	if kinds := f.ciKinds(); len(kinds) != 1 || kinds[0] != ledger.CIItemBaseRed {
		t.Fatalf("a base that can be read and is red raises %v", kinds)
	}
}

// F4: a forge that fails after it started some runs again leaves those on record as done, and the rest are finished as
// the same rerun: asked for once, never counted again and never held to the cap, which the rerun passed when it was
// asked for. The head is never over `ci_auto_rerun`, and a member's own rerun is unaffected.
func TestAFailureInTheMiddleOfARerunAsksOnlyForTheRest(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	ci := f.finishedNow("CI", "timed_out", "11")
	ci.DetailsURI = "https://github.com/acme/widgets/actions/runs/11/job/1"
	lint := f.finishedNow("lint", "cancelled", "12")
	f.forge.FailRerunOf("12", 1)
	f.forge.SetChecks(number, ci, lint)
	f.settle()
	f.settle()
	if got := f.forge.Reruns(); len(got) != 2 || !strings.HasSuffix(got[0], " 11") || !strings.HasSuffix(got[1], " 12") {
		t.Fatalf("the forge took the reruns %v, want run 11 and then run 12 once each", got)
	}
	if held := f.waiting(); held.Reruns != 1 || held.AutoReruns != 1 {
		t.Fatalf("the head holds %+v, want the one rerun counted once", held)
	}
	done := f.eventsData(ledger.EventCIRerunDone)
	if len(done) != 2 || !strings.Contains(done[0], `"pending":["12"]`) || !strings.Contains(done[0], `"runs":["11"]`) || !strings.Contains(done[1], `"runs":["12"]`) {
		t.Fatalf("the reruns were recorded as %v", done)
	}
	if continued := f.eventsData(ledger.EventCIRerunContinued); len(continued) != 1 || !strings.Contains(continued[0], `"continues":"rerun:auto:`) || !strings.Contains(continued[0], `"pending":["12"]`) {
		t.Fatalf("the continuation was recorded as %v", continued)
	}
	f.assertEvents(ledger.EventCIRerunRequested, 1)
	// The rerun ended the same way, past the cap: a person decides, and the cap is not passed.
	f.forge.SetChecks(number, f.finishedNow("CI", "timed_out", "11"), f.finishedNow("lint", "cancelled", "12"))
	f.settle()
	if kinds := f.ciKinds(); len(kinds) != 1 || kinds[0] != ledger.CIItemInfrastructure {
		t.Fatalf("a failure past the cap raises %v", kinds)
	}
	if len(f.forge.Reruns()) != 2 {
		t.Fatalf("the cap was passed: %v", f.forge.Reruns())
	}
	// A member's own rerun is unaffected: it is made and counted apart.
	if _, err := control.NewCIAttention(f.store).Rerun(t.Context(), scope, f.id, member); err != nil {
		t.Fatal(err)
	}
	f.settle()
	if held := f.waiting(); held.Reruns != 2 || held.AutoReruns != 1 {
		t.Fatalf("after a member's rerun the head holds %+v, want 2 of which 1 automatic", held)
	}
	if len(f.forge.Reruns()) != 3 {
		t.Fatalf("a member's rerun made %d forge reruns in all, want 3", len(f.forge.Reruns()))
	}
}

// F5: two checks that share a name each get the log of their own run and job, and never the other's.
func TestTwoChecksOfOneNameEachGetTheirOwnLog(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	one := f.finishedNow("unit", "failure", "801")
	two := f.finishedNow("unit", "failure", "802")
	f.forge.SetLogOf("801/7", "LOG OF THE FIRST RUN\n")
	f.forge.SetLogOf("802/7", "LOG OF THE SECOND RUN\n")
	f.forge.SetChecks(number, one, two)
	f.settle()
	reason := f.reworkReason()
	for _, want := range []string{"LOG OF THE FIRST RUN", "LOG OF THE SECOND RUN"} {
		if strings.Count(reason, want) != 1 {
			t.Errorf("the brief holds %q %d times:\n%s", want, strings.Count(reason, want), reason)
		}
	}
}
