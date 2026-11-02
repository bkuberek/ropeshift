package contract

import (
	"context"
	"errors"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// lineageRig is a repository's default branch on a ledger, and a `plan` work order a member accepted.
// A contract's version names a commit, so what a commit knew decides whether a read is a change.
type lineageRig struct {
	r    *Rig
	s    ports.Store
	main *fake.WorkSource
	plan string
}

func (r *Rig) onMain(files map[string]string) (*lineageRig, error) {
	s := r.Store()
	l := &lineageRig{r: r, s: s, main: fake.NewWorkSource()}
	for name, text := range files {
		l.main.Write(name, []byte(text))
	}
	return l, l.poll()
}

func (l *lineageRig) poll() error {
	_, err := l.r.intake(l.s, l.main)
	return err
}

func (l *lineageRig) commit(name, text string) error {
	l.main.Write(name, []byte(text))
	return l.poll()
}

// takeIn accepts a plan whose lane ended on this branch, and reads it as a tick does.
func (l *lineageRig) takeIn(branch *fake.WorkSource) error {
	id, err := l.r.PlanLaneDone(l.s)
	if err != nil {
		return err
	}
	l.plan = id
	var asked []string
	taken, err := control.NewIntake(l.s).TakeInPlans(l.r.Ctx, l.r.Scope, openFrom(branch, &asked))
	if err != nil {
		return err
	}
	if !slices.Equal(taken, []string{id}) {
		return Violatedf("taken %v", taken)
	}
	return nil
}

// statements reads each version of the one contract these checks hold, oldest first.
func (l *lineageRig) statements() ([]string, error) {
	var out []string
	err := l.s.Read(l.r.Ctx, l.r.Scope, func(tx ports.ReadTx) error {
		versions, err := tx.Planning().Contracts()
		if err != nil {
			return err
		}
		for _, version := range versions {
			if version.Title == "Alpha" {
				out = append(out, strings.TrimSpace(version.Statement))
			}
		}
		return nil
	})
	return out, err
}

func (l *lineageRig) wantStatements(want ...string) error {
	got, err := l.statements()
	if err != nil {
		return err
	}
	if !slices.Equal(got, want) {
		return Violatedf("versions read %q, want %q", got, want)
	}
	return nil
}

func (l *lineageRig) problems() ([]planning.SourceProblem, error) {
	var out []planning.SourceProblem
	err := l.s.Read(l.r.Ctx, l.r.Scope, func(tx ports.ReadTx) error {
		bindings, err := tx.Planning().Bindings()
		if err != nil {
			return err
		}
		for _, binding := range bindings {
			out = append(out, binding.Problems...)
		}
		return nil
	})
	return out, err
}

func (l *lineageRig) wantNoProblem() error {
	problems, err := l.problems()
	if err != nil {
		return err
	}
	if len(problems) != 0 {
		return Violatedf("problems %+v", problems)
	}
	return nil
}

// wantConflict says the source reports this file as a base that moved, and the plan still read.
func (l *lineageRig) wantConflict(path string) error {
	problems, err := l.problems()
	if err != nil {
		return err
	}
	if len(problems) != 1 || problems[0].Path != path || !strings.Contains(problems[0].Detail, planning.ErrBaseMoved.Error()) {
		return Violatedf("problems %+v", problems)
	}
	return nil
}

func alphaOn(statement string) map[string]string {
	return map[string]string{intakeFile: ContractText("alpha", "Alpha", statement)}
}

// answering stands for a source whose lineage says one thing.
type answering struct {
	*fake.WorkSource
	err error
}

func (a *answering) Relate(context.Context, string, string) (ports.Lineage, error) {
	return "", a.err
}

func checkOneLineOfHistory(r *Rig) error {
	l, err := r.onMain(alphaOn("One."))
	if err != nil {
		return err
	}
	l.main.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "Two.")))
	if _, err := r.NewOps(l.s).Poll(r.Ctx, r.Scope, &answering{WorkSource: l.main, err: ports.ErrUnsupported}); err != nil {
		return err
	}
	return l.wantStatements("One.", "Two.")
}

func checkLineageFailure(r *Rig) error {
	l, err := r.onMain(alphaOn("One."))
	if err != nil {
		return err
	}
	l.main.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "Two.")))
	gone := errors.New("the repository went away")
	if _, err := r.NewOps(l.s).Poll(r.Ctx, r.Scope, &answering{WorkSource: l.main, err: gone}); !errors.Is(err, gone) {
		return Violatedf("a lineage that could not be read: %v", err)
	}
	if err := l.wantStatements("One."); err != nil {
		return err
	}
	if err := l.poll(); err != nil {
		return err
	}
	return l.wantStatements("One.", "Two.")
}

func checkPlanOnTheNewestVersion(r *Rig) error {
	l, err := r.onMain(alphaOn("One."))
	if err != nil {
		return err
	}
	branch := l.main.Fork()
	branch.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "From the plan.")))
	if err := l.takeIn(branch); err != nil {
		return err
	}
	if err := l.wantStatements("One.", "From the plan."); err != nil {
		return err
	}
	return l.wantNoProblem()
}

// The lane never touched this file. Its branch only stands where main stood when it was cut, and
// reading that as a version is what brought old text back.
func checkStalePlanBringsNothingBack(r *Rig) error {
	l, err := r.onMain(alphaOn("One."))
	if err != nil {
		return err
	}
	branch := l.main.Fork()
	if err := l.commit(intakeFile, ContractText("alpha", "Alpha", "Two.")); err != nil {
		return err
	}
	branch.Write("notes.md", []byte("# Ordinary prose\n"))
	if err := l.takeIn(branch); err != nil {
		return err
	}
	if err := l.wantStatements("One.", "Two."); err != nil {
		return err
	}
	return l.wantNoProblem()
}

func checkPlanConflict(r *Rig) error {
	l, err := r.onMain(alphaOn("One."))
	if err != nil {
		return err
	}
	branch := l.main.Fork()
	if err := l.commit(intakeFile, ContractText("alpha", "Alpha", "On main.")); err != nil {
		return err
	}
	branch.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "From the plan.")))
	if err := l.takeIn(branch); err != nil {
		return err
	}
	if err := l.wantStatements("One.", "On main."); err != nil {
		return err
	}
	if err := l.wantConflict(intakeFile); err != nil {
		return err
	}
	row, err := r.WorkOrder(l.s, l.plan)
	if err != nil {
		return err
	}
	if row.PlanCommit != acceptedHead {
		return Violatedf("a conflict left the plan unread: %+v", row)
	}
	events, err := r.Events(l.s, r.Scope)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Type == workorders.EventPlanTakenIn && strings.Contains(string(event.Data), "1 conflicts") {
			return r.CheckProjection(l.s)
		}
	}
	return Violatedf("the plan's event does not say what it left out")
}

func checkMergeSettlesAConflict(r *Rig) error {
	l, err := r.onMain(alphaOn("One."))
	if err != nil {
		return err
	}
	branch := l.main.Fork()
	if err := l.commit(intakeFile, ContractText("alpha", "Alpha", "On main.")); err != nil {
		return err
	}
	branch.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "From the plan.")))
	if err := l.takeIn(branch); err != nil {
		return err
	}
	l.main.Merge(branch, intakeFile, []byte(ContractText("alpha", "Alpha", "Both.")))
	if err := l.poll(); err != nil {
		return err
	}
	if err := l.wantStatements("One.", "On main.", "Both."); err != nil {
		return err
	}
	return l.wantNoProblem()
}

// Ropeshift does not merge, so an accepted plan stands in the ledger before it stands on main. A
// commit to main in that gap reads the old file, which is no change to it.
func checkMainKeepsAnAcceptedPlan(r *Rig) error {
	l, err := r.onMain(alphaOn("One."))
	if err != nil {
		return err
	}
	branch := l.main.Fork()
	branch.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "From the plan.")))
	if err := l.takeIn(branch); err != nil {
		return err
	}
	if err := l.commit("notes.md", "# Ordinary prose\n"); err != nil {
		return err
	}
	if err := l.wantStatements("One.", "From the plan."); err != nil {
		return err
	}
	if err := l.wantNoProblem(); err != nil {
		return err
	}
	l.main.Merge(branch, intakeFile, []byte(ContractText("alpha", "Alpha", "From the plan.")))
	if err := l.poll(); err != nil {
		return err
	}
	if err := l.commit(intakeFile, ContractText("alpha", "Alpha", "Later.")); err != nil {
		return err
	}
	if err := l.wantStatements("One.", "From the plan.", "Later."); err != nil {
		return err
	}
	return l.wantNoProblem()
}

func checkMainEditBesideAPlan(r *Rig) error {
	l, err := r.onMain(alphaOn("One."))
	if err != nil {
		return err
	}
	branch := l.main.Fork()
	branch.Write(intakeFile, []byte(ContractText("alpha", "Alpha", "From the plan.")))
	if err := l.takeIn(branch); err != nil {
		return err
	}
	if err := l.commit(intakeFile, ContractText("alpha", "Alpha", "On main.")); err != nil {
		return err
	}
	if err := l.wantStatements("One.", "From the plan."); err != nil {
		return err
	}
	return l.wantConflict(intakeFile)
}

// The other files of a repository go through the same read, so a stale branch holds their old text
// too.
func recordFiles(statement string) map[string]string {
	return map[string]string{
		"goal.md":         GoalText("a-goal", "Goal "+statement),
		"plan.md":         PlanText("a-plan", "Plan "+statement, "a-goal"),
		"requirements.md": RequirementsText("reqs", "LN-1", "Text "+statement),
	}
}

func (l *lineageRig) recordsRead() (goal, plan, requirement string, err error) {
	err = l.s.Read(l.r.Ctx, l.r.Scope, func(tx ports.ReadTx) error {
		found, err := tx.Planning().Requirement("LN-1")
		if err != nil {
			return err
		}
		requirement = found.Text
		goals, err := tx.Planning().Goals()
		if err != nil {
			return err
		}
		plans, err := tx.Planning().Plans()
		if err != nil {
			return err
		}
		if len(goals) != 1 {
			return Violatedf("%d goals", len(goals))
		}
		goal = goals[0].Title
		// The plan work order has a plan of its own, which holds no file.
		for _, row := range plans {
			if row.Source != "" {
				plan = row.Title
			}
		}
		return nil
	})
	return goal, plan, requirement, err
}

func checkStalePlanBringsNoRecordBack(r *Rig) error {
	l, err := r.onMain(recordFiles("one"))
	if err != nil {
		return err
	}
	branch := l.main.Fork()
	for name, text := range recordFiles("two") {
		l.main.Write(name, []byte(text))
	}
	if err := l.poll(); err != nil {
		return err
	}
	if err := l.takeIn(branch); err != nil {
		return err
	}
	goal, plan, requirement, err := l.recordsRead()
	if err != nil {
		return err
	}
	if goal != "Goal two" || plan != "Plan two" || requirement != "Text two" {
		return Violatedf("a stale plan left %q, %q, %q", goal, plan, requirement)
	}
	return l.wantNoProblem()
}

func checkRequirementConflict(r *Rig) error {
	l, err := r.onMain(map[string]string{"requirements.md": RequirementsText("reqs", "LN-1", "One")})
	if err != nil {
		return err
	}
	branch := l.main.Fork()
	if err := l.commit("requirements.md", RequirementsText("reqs", "LN-1", "On main")); err != nil {
		return err
	}
	branch.Write("requirements.md", []byte(RequirementsText("reqs", "LN-1", "From the plan")))
	if err := l.takeIn(branch); err != nil {
		return err
	}
	var found planning.Requirement
	err = l.s.Read(l.r.Ctx, l.r.Scope, func(tx ports.ReadTx) error {
		var err error
		found, err = tx.Planning().Requirement("LN-1")
		return err
	})
	if err != nil {
		return err
	}
	if found.Text != "On main" || found.Version != 2 {
		return Violatedf("the requirement reads %+v", found)
	}
	return l.wantConflict("requirements.md")
}
