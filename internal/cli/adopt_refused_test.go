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
func TestAddedTextPrintsReasonsAndNamesAsPlainText(t *testing.T) {
	report := func(text string) api.RepositoryAddReport {
		return api.RepositoryAddReport{Repositories: []api.RepositoryAdded{
			{Path: "/work/a", Outcome: ledger.RegistrationRefused, Reason: text},
			{Path: "/work/b", Outcome: ledger.Registered, Repository: &ledger.Repository{ID: "5c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5", Name: text}, Reason: text},
			{Path: "/work/c", Outcome: ledger.Registered, Checkout: "/work/d", Repository: &ledger.Repository{ID: "9a8b7c6d5e4f30211203f4e5d6c7b8a998877665", Name: text}, Reason: text},
		}}
	}
	sameShape(t, addedText(report(hostile)), addedText(report("x")))
}

// The plan --dry-run prints names its repositories, and says why, with the text of the layout and the API, which never
// breaks a line or reaches the terminal as a control sequence.
func TestAdoptionPlanTextPrintsReasonsAndNamesAsPlainText(t *testing.T) {
	plan := func(text string) planning.AdoptionPlan {
		return planning.AdoptionPlan{
			Layout:   planning.DetectedLayout{Root: "/work", Clones: []planning.LayoutClone{{Name: text, Path: "/work/a"}}},
			Shape:    planning.ShapeWorkspaceFiles,
			Planning: planning.PlanningRepository{Name: text, Why: text},
			Design:   &planning.DesignRepository{Name: text, Why: text},
			Repositories: []planning.PlannedRepository{
				{Name: text, Path: "/work/a", Outcome: string(ledger.RegistrationRefused), Reason: text},
				{Name: text, Path: "/work/b", Outcome: string(ledger.Registered), Reason: text},
			},
			Files:           []planning.PlannedFile{{Path: "a", Action: planning.ActionAdd, Detail: text}, {Path: "b", Action: planning.ActionAdd, Detail: text, Template: "t"}},
			Collisions:      []planning.Collision{{Path: "c", Kept: text}},
			MakefileLeftOut: []planning.Omission{{Name: text, Path: "/work/c", Reason: text}},
			Moves:           []planning.Move{{Name: text, From: "/work/a", To: "/work/b"}},
			Steps:           []planning.Step{{Name: text, Path: "/work/s"}},
			Notes:           []string{text},
		}
	}
	sameShape(t, adoptionPlanText(plan(hostile)), adoptionPlanText(plan("x")))
	undecided := plan(hostile)
	undecided.Planning.Undecided = true
	sameShape(t, adoptionPlanText(undecided), adoptionPlanText(func() planning.AdoptionPlan { p := plan("x"); p.Planning.Undecided = true; return p }()))
}
