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

// A path prints as it is, with its spaces, and a command a person copies and runs prints exactly: the plan, the moves,
// and the steps all show a path with two spaces in it, quoted as the shell needs, unchanged.
func TestAdoptionPrintsSpacesInPathsAndCommandsAsTheyAre(t *testing.T) {
	const path = "/w/My  Repo"
	const command = "mv '/w/My  Repo' '/w/Plans  Dir/My  Repo'"
	plan := planning.AdoptionPlan{
		Layout:       planning.DetectedLayout{Root: path, Clones: []planning.LayoutClone{{Name: "a", Path: path}}},
		Shape:        planning.ShapeWorkspaceFiles,
		Planning:     planning.PlanningRepository{Name: "a", Why: "w"},
		Repositories: []planning.PlannedRepository{{Name: "a", Path: path, Outcome: string(ledger.Registered)}},
		Moves:        []planning.Move{{Name: "a", From: path, To: path, Commands: []string{command}}},
		Steps:        []planning.Step{{Name: "a", Path: path, Commands: []string{command}}},
	}
	text := adoptionPlanText(plan)
	for _, want := range []string{"Layout\t" + path + "\n", "\t" + path + "\n", "run\t" + command + "\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the plan prints no %q:\n%s", want, text)
		}
	}
	var stdout bytes.Buffer
	c := &invocation{stdout: &stdout}
	c.sayMoves(plan.Moves)
	c.saySteps(plan.Steps)
	c.sayLeftOut([]planning.Omission{{Name: "a", Path: path, Reason: "r"}})
	if got := strings.Count(stdout.String(), "  "+command+"\n"); got != 2 {
		t.Errorf("the moves and steps print the command %d times exactly, want 2:\n%s", got, stdout.String())
	}
	if !strings.Contains(stdout.String(), "a ("+path+"): r") {
		t.Errorf("the repositories left out print no %q:\n%s", path, stdout.String())
	}
	added := addedText(api.RepositoryAddReport{Repositories: []api.RepositoryAdded{{Path: path, Outcome: ledger.RegistrationRefused, Reason: "r"}}})
	if !strings.Contains(added, "refused\t"+path+"\t") {
		t.Errorf("the registration prints no %q:\n%s", path, added)
	}
}

// A command with a control or format character, a tab or a line break among them, is not printed: a line says it holds
// characters that cannot be shown, and where the command is exact.
func TestAdoptionPrintsNoCommandThatHoldsACharacterThatCannotBeShown(t *testing.T) {
	for _, hidden := range []string{"\x1b[31m", "\t", "\n", "\r", "\u2028", "\u202e", "\u200b", "\x07"} {
		command := "mv 'a" + hidden + "b' c"
		plan := planning.AdoptionPlan{
			Shape: planning.ShapeWorkspaceFiles,
			Moves: []planning.Move{{Name: "a", Commands: []string{command}}},
			Steps: []planning.Step{{Name: "a", Commands: []string{command}}},
		}
		var stdout bytes.Buffer
		c := &invocation{stdout: &stdout}
		c.sayMoves(plan.Moves)
		c.saySteps(plan.Steps)
		for name, text := range map[string]string{"the plan": adoptionPlanText(plan), "the lists": stdout.String()} {
			if strings.Contains(text, "mv ") {
				t.Errorf("%s print the command that holds %q:\n%s", name, hidden, text)
			}
			if !strings.Contains(text, "cannot be shown") || !strings.Contains(text, "adopt --dry-run --json") {
				t.Errorf("%s print no notice for the command that holds %q:\n%s", name, hidden, text)
			}
		}
	}
}
