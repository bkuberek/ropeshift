package cli_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A member's own `work retry`, `lane reset`, and `lane respawn` of work at its hard spend limit exit as a conflict, and the
// message that reaches the terminal names the work order, its limit, what it is charged, and the command that raises it
// (FR-SPEND-8).
func TestAMembersCommandAtTheHardLimitSaysToRaiseIt(t *testing.T) {
	s := newSession(t)
	s.stopped("wo")
	for name, args := range map[string][]string{
		"work retry":   {"work", "retry", "wo", "--reason", "try once more"},
		"lane reset":   {"lane", "reset", "wo"},
		"lane respawn": {"lane", "respawn", "wo"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := s.run(args...)
			if code != cli.ExitConflict || stdout != "" {
				t.Errorf("%v: exit %d with %q on stdout, want %d and nothing", args, code, stdout, cli.ExitConflict)
			}
			for _, want := range []string{"wo", "hard spend limit of $25.00", "$30.00 charged", "raise it with ropeshift spend raise"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("%v: the message %q leaves out %q", args, stderr, want)
				}
			}
		})
	}
	s.must("spend", "raise", "wo", "--to", "100")
	if code, _, stderr := s.run("lane", "respawn", "wo"); strings.Contains(stderr, "hard spend limit") {
		t.Errorf("a respawn once the limit was raised was still refused for it: exit %d, %q", code, stderr)
	}
}

// A raise that does not clear what the work order is charged is refused as a usage error, with the figure it fell short of.
func TestSpendRaiseBelowChargedSpendSaysWhatItFellShortOf(t *testing.T) {
	s := newSession(t)
	s.stopped("wo")
	code, _, stderr := s.run("spend", "raise", "wo", "--to", "28")
	if code != cli.ExitUsage || !strings.Contains(stderr, "would stay below charged spend of $30.00") {
		t.Errorf("a raise to 28 over a charge of 30: exit %d, %q", code, stderr)
	}
	if shown := s.must("spend", "--work-order", "wo"); !strings.Contains(shown, "hard limit\t$25.00") {
		t.Errorf("a refused raise moved the limit:\n%s", shown)
	}
}

// A resume says what it let one more generation spend and what it took that from, beside the limit before and after, so a
// member reads the new limit and the one it replaced; a raise has no such figure and prints none.
func TestAResumeSaysWhatItLetOneMoreGenerationSpend(t *testing.T) {
	resume := ledger.SpendLimitChange{
		WorkOrder: "wo", Act: ledger.SpendActResume, BeforeUSD: 25, AfterUSD: 40.5, ChargedUSD: 30,
		AllowanceUSD: 10.5, AllowanceBasis: ledger.AllowanceMeanGeneration,
	}
	text := cli.LimitChangeText(resume)
	for _, want := range []string{"work order wo", "resume\thard limit $25.00 -> $40.50, charged $30.00", "one more generation: $10.50, the mean charge of the generations that recorded spend"} {
		if !strings.Contains(text, want) {
			t.Errorf("a resume prints %q, which leaves out %q", text, want)
		}
	}
	resume.AllowanceBasis = ledger.AllowanceEstimate
	if text = cli.LimitChangeText(resume); !strings.Contains(text, "$10.50, the work order's estimate") {
		t.Errorf("a resume sized by the estimate prints %q", text)
	}
	raise := ledger.SpendLimitChange{WorkOrder: "wo", Act: ledger.SpendActRaise, BeforeUSD: 25, AfterUSD: 50, ChargedUSD: 30}
	if text = cli.LimitChangeText(raise); strings.Contains(text, "one more generation") {
		t.Errorf("a raise prints %q", text)
	}
}
