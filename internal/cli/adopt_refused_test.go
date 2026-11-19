package cli

import (
	"bytes"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
)

// The reason a registration was refused comes from the API, so a control sequence in it never reaches the terminal.
func TestRefusedPlanningPrintsTheReasonAsPlainText(t *testing.T) {
	var stderr bytes.Buffer
	c := &invocation{name: "adopt", stderr: &stderr}
	plan := planning.AdoptionPlan{Planning: planning.PlanningRepository{Name: "de\x1b[31m\nsign"}}

	code := c.refusedPlanning(plan, "held by \x1b[31mother\x1b[0m\x07\u202e\r\nproject")

	if code != ExitUsage {
		t.Errorf("exit %d, want %d", code, ExitUsage)
	}
	got := stderr.String()
	if strings.ContainsAny(got, "\x1b\x07\u202e\r") || strings.Count(got, "\n") != 1 {
		t.Errorf("the refusal carries a control sequence or a line break of its own: %q", got)
	}
	if !strings.Contains(got, "plan, de sign, was refused: held by other project;") {
		t.Errorf("the refusal reads %q, want its name and reason as plain text on one line", got)
	}
}

// hostile is text a reason or a name can carry that a terminal would act on: an escape sequence, every kind of
// line break, a tab, and a right-to-left override.
const hostile = "a\x1b[31m\nb\r\nc\u2028d\u2029e\x07\u202ef\tg"

// sameShape fails when the text printed for hostile strings has a control character, or a different number of lines
// or fields than the text printed for plain ones.
func sameShape(t *testing.T, got, plain string) {
	t.Helper()
	if strings.ContainsAny(got, "\x1b\x07\r\u2028\u2029\u202e") {
		t.Errorf("the text carries a control character: %q", got)
	}
	for _, sep := range []string{"\n", "\t"} {
		if g, w := strings.Count(got, sep), strings.Count(plain, sep); g != w {
			t.Errorf("the text has %d %q, want %d as for plain strings:\n%q\n%q", g, sep, w, got, plain)
		}
	}
}

// What a registration reports of a refusal, or of a clone, never breaks a line or reaches the terminal as a
// control sequence, whatever the API sent.
func TestAddedTextPrintsReasonsNamesAndPathsAsPlainText(t *testing.T) {
	report := func(text string) api.RepositoryAddReport {
		return api.RepositoryAddReport{Repositories: []api.RepositoryAdded{
			{Path: text, Outcome: ledger.RegistrationRefused, Reason: text},
			{Path: text, Outcome: ledger.Registered, Repository: &ledger.Repository{ID: "5c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5", Name: text}, Reason: text},
			{Path: text, Outcome: ledger.Registered, Checkout: text + "x", Repository: &ledger.Repository{ID: "9a8b7c6d5e4f30211203f4e5d6c7b8a998877665", Name: text}, Reason: text},
		}}
	}
	sameShape(t, addedText(report(hostile)), addedText(report("x")))
}

// The plan --dry-run prints names its repositories, says why, and lists the paths and the commands of the plan, with
// the text of the layout and the API, which never breaks a line or reaches the terminal as a control sequence.
func TestAdoptionPlanTextPrintsEveryFieldAsPlainText(t *testing.T) {
	plan := func(text string) planning.AdoptionPlan {
		return planning.AdoptionPlan{
			Layout:   planning.DetectedLayout{Root: text, Clones: []planning.LayoutClone{{Name: text, Path: text}}},
			Shape:    planning.ShapeWorkspaceFiles,
			Planning: planning.PlanningRepository{Name: text, Why: text},
			Design:   &planning.DesignRepository{Name: text, Why: text},
			Repositories: []planning.PlannedRepository{
				{Name: text, Path: text, Outcome: string(ledger.RegistrationRefused), Reason: text},
				{Name: text, Path: text, Outcome: string(ledger.Registered), Reason: text},
			},
			Files:           []planning.PlannedFile{{Path: text, Action: planning.ActionAdd, Detail: text}, {Path: text, Action: planning.ActionAdd, Detail: text, Template: "t"}},
			Takes:           []string{text},
			Collisions:      []planning.Collision{{Path: text, DesignPath: text, Kept: text}},
			CodeTrees:       []planning.PlannedCodeTree{{Path: text, BuildFile: text}},
			MakefileLeftOut: []planning.Omission{{Name: text, Path: text, Reason: text}},
			Moves:           []planning.Move{{Name: text, From: text, To: text, Commands: []string{text}}},
			Steps:           []planning.Step{{Name: text, Path: text, Commands: []string{text}}},
			Notes:           []string{text},
		}
	}
	sameShape(t, adoptionPlanText(plan(hostile)), adoptionPlanText(plan("x")))
	undecided := func(text string) planning.AdoptionPlan {
		p := plan(text)
		p.Planning.Undecided = true
		return p
	}
	sameShape(t, adoptionPlanText(undecided(hostile)), adoptionPlanText(undecided("x")))
}

// What adopt --restructure prints of the moves, the steps, and the repositories the Makefile leaves out, a person
// copies and runs, and it never breaks a line or reaches the terminal as a control sequence.
func TestAdoptRestructureListsPrintPlainText(t *testing.T) {
	printed := func(text string) string {
		var stdout bytes.Buffer
		c := &invocation{stdout: &stdout}
		c.sayMoves([]planning.Move{{Name: text, Commands: []string{text}}})
		c.saySteps([]planning.Step{{Name: text, Commands: []string{text}}})
		c.sayLeftOut([]planning.Omission{{Name: text, Path: text, Reason: text}})
		return stdout.String()
	}
	sameShape(t, printed(hostile), printed("x"))
}
