package contract

import (
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
)

// RosterChecks are what the ledger promises about the agent roster (ADR-0025): the installation's layer
// and a project's resolve field by field, every change is a version with its event, only a member makes
// one, and no change leaves open work without its agent.
var RosterChecks = []Check{
	{"RO1", "the installation's layer and a project's resolve field by field, each with its source", checkRosterLayers},
	{"RO2", "only a member changes the roster, at either scope", checkOnlyMemberChangesRoster},
	{"RO3", "each change is a version with its event, and a revert records a new version", checkRosterHistory},
	{"RO4", "an agent the default mapping or open work names cannot be disabled or removed", checkRosterInUse},
	{"RO5", "a project transaction never writes the installation's layer", checkProjectWritesNoInstallationRoster},
}

type rosterRig struct {
	r     *Rig
	store ports.Store
	ops   *control.Roster
}

func newRosterRig(r *Rig) *rosterRig {
	store := r.Store()
	return &rosterRig{r: r, store: store, ops: control.NewRoster(store.Installation(r.Scope), nil)}
}

func (g *rosterRig) installation() control.RosterTarget {
	return control.RosterTarget{Scope: roster.ScopeInstallation}
}

func (g *rosterRig) project() control.RosterTarget {
	return control.RosterTarget{Scope: roster.ScopeProject, Store: g.store, Project: g.r.Scope}
}

func (g *rosterRig) set(target control.RosterTarget, agent string, fields map[string]string) (roster.Version, error) {
	return g.ops.Change(g.r.Ctx, target, roster.Change{Op: roster.OpSet, Agent: agent, Fields: fields}, theMember)
}

func checkRosterLayers(r *Rig) error {
	g := newRosterRig(r)
	if _, err := g.set(g.installation(), "explore", map[string]string{"purpose": "Search the whole tree.", "class": roster.ClassStandard}); err != nil {
		return err
	}
	if _, err := g.set(g.project(), "explore", map[string]string{"class": roster.ClassLarge}); err != nil {
		return err
	}
	view, err := g.ops.View(r.Ctx, g.project())
	if err != nil {
		return err
	}
	explore, _ := view.Resolved.Agent("explore")
	if explore.Purpose != "Search the whole tree." || explore.Class != roster.ClassLarge ||
		explore.Sources["purpose"] != roster.SourceInstallation || explore.Sources["class"] != roster.SourceProject || explore.Sources["effort"] != roster.SourceBuiltin {
		return Violatedf("the project resolved explore as %+v", explore)
	}
	if view.Installation != 1 || view.Project != 1 {
		return Violatedf("the project read installation version %d and project version %d", view.Installation, view.Project)
	}
	alone, err := g.ops.View(r.Ctx, g.installation())
	if err != nil {
		return err
	}
	if explore, _ := alone.Resolved.Agent("explore"); explore.Class != roster.ClassStandard || alone.Project != 0 {
		return Violatedf("the installation alone resolved explore as %+v", explore)
	}
	return nil
}

func checkOnlyMemberChangesRoster(r *Rig) error {
	g := newRosterRig(r)
	for _, actor := range theAgents {
		for _, target := range []control.RosterTarget{g.installation(), g.project()} {
			if _, err := g.ops.Change(r.Ctx, target, roster.Change{Op: roster.OpDisable, Agent: "lookup"}, actor); !errors.Is(err, workorders.ErrForbidden) {
				return Violatedf("%s changed the %s roster: %v", actor.Kind, target.Scope, err)
			}
		}
	}
	for _, target := range []control.RosterTarget{g.installation(), g.project()} {
		history, err := g.ops.History(r.Ctx, target, "")
		if err != nil {
			return err
		}
		if len(history) != 0 {
			return Violatedf("a refused change left %d versions of the %s roster", len(history), target.Scope)
		}
	}
	if _, err := g.ops.Change(r.Ctx, g.project(), roster.Change{Op: roster.OpDisable, Agent: "lookup"}, theMember); err != nil {
		return Violatedf("the same change from a member failed, so nothing above proved a refusal: %v", err)
	}
	return nil
}

func checkRosterHistory(r *Rig) error {
	g := newRosterRig(r)
	for _, target := range []control.RosterTarget{g.installation(), g.project()} {
		first, err := g.set(target, "review", map[string]string{"effort": roster.EffortMax})
		if err != nil {
			return err
		}
		if _, err := g.ops.Change(r.Ctx, target, roster.Change{Op: roster.OpAdd, Agent: "porter", From: "mechanical"}, theMember); err != nil {
			return err
		}
		reverted, err := g.ops.Revert(r.Ctx, target, first.Version, theMember)
		if err != nil {
			return err
		}
		if reverted.Version != 3 || reverted.Digest != first.Digest || reverted.AuthoredBy != "member:"+theMember.ID {
			return Violatedf("the %s revert recorded %+v from %+v", target.Scope, reverted, first)
		}
		history, err := g.ops.History(r.Ctx, target, "")
		if err != nil {
			return err
		}
		if len(history) != 3 || history[0].Version.Version != 3 || !slices.Contains(history[0].Difference, "- porter removed") ||
			!slices.Contains(history[1].Difference, "+ porter added") {
			return Violatedf("the %s history reads %+v", target.Scope, history)
		}
		named, err := g.ops.History(r.Ctx, target, "porter")
		if err != nil {
			return err
		}
		if len(named) != 2 {
			return Violatedf("the %s history of porter holds %d versions", target.Scope, len(named))
		}
	}
	installation, err := r.SharedEvents(g.store)
	if err != nil {
		return err
	}
	project, err := r.Events(g.store, r.Scope)
	if err != nil {
		return err
	}
	// A store with no installation database keeps the installation's events in its own log, which both
	// reads then hold, so each event counts once.
	count := func(events []ledger.Event, scope string) int {
		seen := map[string]bool{}
		for _, e := range events {
			if e.Type == roster.EventChanged && e.Subject.ID == scope {
				seen[e.ID] = true
			}
		}
		return len(seen)
	}
	both := append(installation, project...)
	if count(both, roster.ScopeInstallation) != 3 || count(project, roster.ScopeProject) != 3 {
		return Violatedf("the logs hold %d installation and %d project roster events", count(both, roster.ScopeInstallation), count(project, roster.ScopeProject))
	}
	return nil
}

func checkRosterInUse(r *Rig) error {
	g := newRosterRig(r)
	if _, err := g.ops.Change(r.Ctx, g.project(), roster.Change{Op: roster.OpDisable, Agent: "implementation"}, theMember); !errors.Is(err, roster.ErrInUse) {
		return Violatedf("the default mapping's agent was disabled: %v", err)
	}
	if _, err := g.ops.Change(r.Ctx, g.installation(), roster.Change{Op: roster.OpAdd, Agent: "porter", From: "mechanical"}, theMember); err != nil {
		return err
	}
	order, err := r.NewOps(g.store).Create(r.Ctx, r.Scope, workorders.WorkOrder{
		ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Port the module", Agent: "porter", MaxAttempts: defaultMaxAttempts,
	}, r.Actor)
	if err != nil {
		return err
	}
	if order.Agent != "porter" {
		return Violatedf("the work order kept agent %q", order.Agent)
	}
	refused := []struct {
		target control.RosterTarget
		change roster.Change
	}{
		{g.project(), roster.Change{Op: roster.OpDisable, Agent: "porter"}},
		{g.installation(), roster.Change{Op: roster.OpDisable, Agent: "porter"}},
		{g.installation(), roster.Change{Op: roster.OpReset, Agent: "porter"}},
	}
	g.ops = control.NewRoster(g.store.Installation(r.Scope), oneProjectOf(r, g.store))
	for _, tt := range refused {
		_, err := g.ops.Change(r.Ctx, tt.target, tt.change, theMember)
		if !errors.Is(err, roster.ErrInUse) || !containsText(err.Error(), "work order "+order.ID) {
			return Violatedf("%s at %s, with %s naming porter: %v", tt.change.Op, tt.target.Scope, order.ID, err)
		}
	}
	if _, err := g.ops.Change(r.Ctx, g.project(), roster.Change{Op: roster.OpDisable, Agent: "lookup"}, theMember); err != nil {
		return Violatedf("an agent nothing names could not be disabled: %v", err)
	}
	return nil
}

func checkProjectWritesNoInstallationRoster(r *Rig) error {
	store := r.Store()
	err := store.Write(r.Ctx, r.Scope, "roster.forged", func(tx ports.WriteTx) error {
		if _, err := tx.Roster().Insert(roster.Version{ID: r.ID("rv"), Scope: roster.ScopeInstallation, Version: 1, AuthoredBy: "member:m"}); err != nil {
			return err
		}
		_, err := tx.Append(r.noteDraft(r.ID("note")))
		return err
	})
	if !errors.Is(err, ports.ErrNotShared) {
		return Violatedf("a project transaction wrote the installation's layer: %v", err)
	}
	return nil
}

// oneProjectOf is a registry of the rig's one project, so a change to the installation's layer reads the
// work that project holds.
func oneProjectOf(r *Rig, store ports.Store) ports.Projects {
	projects := fake.NewProjects()
	projects.Add(ledger.Project{ID: r.Scope, Name: "rig", State: ledger.ProjectActive, Database: "ledger"}, store)
	return projects
}

func containsText(text, part string) bool { return strings.Contains(text, part) }
