package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Roster holds the use cases of the agent roster (ADR-0025): reading it as a project or the installation
// resolves it, changing one scope's layer, and reading and reverting a scope's history. Changing the roster
// is a consent operation, so only a member's decision runs a change (FR-AGENT-7).
type Roster struct {
	installation ports.Installation
	projects     ports.Projects
}

// NewRoster returns the use cases over the installation's records and its projects, whose open work a
// change to the installation's layer must not leave without its agent.
func NewRoster(installation ports.Installation, projects ports.Projects) *Roster {
	return &Roster{installation: installation, projects: projects}
}

// RosterTarget is the scope a call reads or changes: the installation's layer, or one project's, whose
// Store and scope it names. A change never infers its scope.
type RosterTarget struct {
	Scope   string
	Store   ports.Store
	Project ledger.WorkspaceID
}

// RosterView is a resolved roster with the version of each layer it folded, zero for a layer with none.
type RosterView struct {
	Resolved     roster.Resolved `json:"resolved"`
	Installation int64           `json:"installation_version"`
	Project      int64           `json:"project_version"`
}

// RosterEntry is one version of a scope's history, with what it changed from the version before it.
type RosterEntry struct {
	roster.Version
	Difference []string `json:"difference"`
}

// layerOf reads a scope's newest layer, and an empty one for a scope with no version yet.
func layerOf(reader ports.RosterReader, scope string) (roster.Layer, int64, error) {
	latest, err := reader.Latest(scope)
	if errors.Is(err, ports.ErrNotFound) {
		return roster.Layer{}, 0, nil
	}
	return latest.Body, latest.Version, err
}

// ResolveIn reads the roster a project resolves to, inside one of its transactions.
func ResolveIn(reader ports.RosterReader) (RosterView, error) {
	installation, iv, err := layerOf(reader, roster.ScopeInstallation)
	if err != nil {
		return RosterView{}, err
	}
	project, pv, err := layerOf(reader, roster.ScopeProject)
	if err != nil {
		return RosterView{}, err
	}
	return RosterView{Resolved: roster.Resolve(installation, project), Installation: iv, Project: pv}, nil
}

// View reads the roster a target resolves to: the installation's on its own, or a project's.
func (r *Roster) View(ctx context.Context, target RosterTarget) (RosterView, error) {
	var view RosterView
	if target.Scope == roster.ScopeInstallation {
		err := r.installation.Read(ctx, func(tx ports.SharedReadTx) error {
			layer, version, err := layerOf(tx.Roster(), roster.ScopeInstallation)
			view = RosterView{Resolved: roster.Resolve(layer, roster.Layer{}), Installation: version}
			return err
		})
		return view, err
	}
	err := target.Store.Read(ctx, target.Project, func(tx ports.ReadTx) error {
		var err error
		view, err = ResolveIn(tx.Roster())
		return err
	})
	return view, err
}

// History reads a scope's versions, newest first, each with its difference. A name keeps the versions
// that changed that agent.
func (r *Roster) History(ctx context.Context, target RosterTarget, agent string) ([]RosterEntry, error) {
	var versions []roster.Version
	read := func(reader ports.RosterReader) error {
		var err error
		versions, err = reader.Versions(target.Scope)
		return err
	}
	var err error
	if target.Scope == roster.ScopeInstallation {
		err = r.installation.Read(ctx, func(tx ports.SharedReadTx) error { return read(tx.Roster()) })
	} else {
		err = target.Store.Read(ctx, target.Project, func(tx ports.ReadTx) error { return read(tx.Roster()) })
	}
	if err != nil {
		return nil, err
	}
	out := []RosterEntry{}
	for i, version := range versions {
		var before roster.Layer
		if i+1 < len(versions) {
			before = versions[i+1].Body
		}
		entry := RosterEntry{Version: version, Difference: roster.Difference(before, version.Body)}
		if agent == "" || slices.ContainsFunc(entry.Difference, func(line string) bool { return mentions(line, agent) }) {
			out = append(out, entry)
		}
	}
	return out, nil
}

// mentions says a line of a difference is about one agent.
func mentions(line, agent string) bool {
	rest := line[min(2, len(line)):]
	return rest == agent || strings.HasPrefix(rest, agent+" ") || strings.HasPrefix(rest, agent+".")
}

// Change makes one change to a scope's layer as a new version (FR-AGENT-4, FR-AGENT-5). It refuses a
// change that would leave the default mapping or a work order that is not done without its agent, and
// names what still uses it.
func (r *Roster) Change(ctx context.Context, target RosterTarget, change roster.Change, actor ledger.Actor) (roster.Version, error) {
	return r.write(ctx, target, roster.Summarize(change), actor, func(layer roster.Layer, below roster.Resolved, _ ports.RosterReader) (roster.Layer, error) {
		return roster.Apply(layer, below, change)
	})
}

// Revert records an earlier version's body as a new version of its scope, checked against the layers
// below it as they stand now.
func (r *Roster) Revert(ctx context.Context, target RosterTarget, version int64, actor ledger.Actor) (roster.Version, error) {
	summary := fmt.Sprintf("revert to version %d", version)
	return r.write(ctx, target, summary, actor, func(_ roster.Layer, below roster.Resolved, reader ports.RosterReader) (roster.Layer, error) {
		found, err := reader.Version(target.Scope, version)
		if err != nil {
			return roster.Layer{}, err
		}
		for _, agent := range roster.ResolveOver(below, found.Body).Agents {
			if err := agent.Validate(); err != nil {
				return roster.Layer{}, fmt.Errorf("version %d no longer fits the layers below it: %w", version, err)
			}
		}
		return found.Body, nil
	})
}

// layerChange makes a scope's next layer from its newest one and the roster below it.
type layerChange func(layer roster.Layer, below roster.Resolved, reader ports.RosterReader) (roster.Layer, error)

// write records a scope's next version from what change makes of its newest layer.
func (r *Roster) write(ctx context.Context, target RosterTarget, summary string, actor ledger.Actor, change layerChange) (roster.Version, error) {
	if !authority.Allowed(actor, authority.ActionChangeRoster) {
		return roster.Version{}, workorders.ErrForbidden
	}
	switch target.Scope {
	case roster.ScopeInstallation:
		return r.writeInstallation(ctx, summary, actor, change)
	case roster.ScopeProject:
		return writeProject(ctx, target, summary, actor, change)
	}
	return roster.Version{}, fmt.Errorf("%w: a change names its scope, %s or %s, and not %q", roster.ErrInvalid, roster.ScopeInstallation, roster.ScopeProject, target.Scope)
}

func writeProject(ctx context.Context, target RosterTarget, summary string, actor ledger.Actor, change layerChange) (roster.Version, error) {
	var out roster.Version
	err := target.Store.Write(ctx, target.Project, "roster.change", func(tx ports.WriteTx) error {
		installation, _, err := layerOf(tx.Roster(), roster.ScopeInstallation)
		if err != nil {
			return err
		}
		layer, version, err := layerOf(tx.Roster(), roster.ScopeProject)
		if err != nil {
			return err
		}
		below := roster.Resolve(installation, roster.Layer{})
		next, err := change(layer, below, tx.Roster())
		if err != nil {
			return err
		}
		uses, err := openUses(tx.WorkOrders())
		if err != nil {
			return err
		}
		if err := stillUsable(roster.Resolve(installation, next), uses, ""); err != nil {
			return err
		}
		if out, err = nextVersion(roster.ScopeProject, version, next, summary, actor, tx.Now()); err != nil {
			return err
		}
		return keepVersion(tx.Roster(), tx.Append, out, actor)
	})
	return out, err
}

func (r *Roster) writeInstallation(ctx context.Context, summary string, actor ledger.Actor, change layerChange) (roster.Version, error) {
	projects, err := r.projectLayers(ctx)
	if err != nil {
		return roster.Version{}, err
	}
	var out roster.Version
	err = r.installation.Write(ctx, "roster.change", func(tx ports.SharedTx) error {
		layer, version, err := layerOf(tx.Roster(), roster.ScopeInstallation)
		if err != nil {
			return err
		}
		next, err := change(layer, roster.Resolve(roster.Layer{}, roster.Layer{}), tx.Roster())
		if err != nil {
			return err
		}
		for _, project := range projects {
			if err := stillUsable(roster.Resolve(next, project.layer), project.uses, project.name); err != nil {
				return err
			}
		}
		if len(projects) == 0 {
			if err := stillUsable(roster.Resolve(next, roster.Layer{}), nil, ""); err != nil {
				return err
			}
		}
		if out, err = nextVersion(roster.ScopeInstallation, version, next, summary, actor, tx.Now()); err != nil {
			return err
		}
		return keepVersion(tx.Roster(), tx.Append, out, actor)
	})
	return out, err
}

// projectRoster is what one project holds of the roster: its layer and what names an agent.
type projectRoster struct {
	name  string
	layer roster.Layer
	uses  []roster.Use
}

// projectLayers reads every active project's layer and open work, which a change to the installation's
// layer must leave usable.
func (r *Roster) projectLayers(ctx context.Context) ([]projectRoster, error) {
	if r.projects == nil {
		return nil, nil
	}
	list, err := r.projects.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []projectRoster
	for _, project := range list {
		store, err := r.projects.Open(ctx, project)
		if err != nil {
			return nil, err
		}
		one := projectRoster{name: project.Name}
		err = store.Read(ctx, project.ID, func(tx ports.ReadTx) error {
			var err error
			if one.layer, _, err = layerOf(tx.Roster(), roster.ScopeProject); err != nil {
				return err
			}
			one.uses, err = openUses(tx.WorkOrders())
			return err
		})
		if err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, nil
}

// openUses lists each work order that is not done and names an agent.
func openUses(orders ports.WorkOrderReader) ([]roster.Use, error) {
	list, err := orders.List()
	if err != nil {
		return nil, err
	}
	var out []roster.Use
	for _, order := range list {
		if order.Agent != "" && !workorders.Terminal(order.State) {
			out = append(out, roster.Use{Agent: order.Agent, By: "work order " + order.ID})
		}
	}
	return out, nil
}

// stillUsable refuses a roster that leaves the default mapping or open work without its agent.
func stillUsable(after roster.Resolved, uses []roster.Use, project string) error {
	missing := after.Missing(append(after.DefaultUses(), uses...))
	if len(missing) == 0 {
		return nil
	}
	var named []string
	for _, use := range missing {
		named = append(named, fmt.Sprintf("%s names %s", use.By, use.Agent))
	}
	where := ""
	if project != "" {
		where = " in project " + project
	}
	return fmt.Errorf("%w: the change would leave an agent unusable that something still names%s: %s", roster.ErrInUse, where, strings.Join(named, "; "))
}

func nextVersion(scope string, current int64, layer roster.Layer, summary string, actor ledger.Actor, now int64) (roster.Version, error) {
	id, err := newID(now)
	if err != nil {
		return roster.Version{}, err
	}
	_, digest, err := roster.Encode(layer)
	if err != nil {
		return roster.Version{}, err
	}
	return roster.Version{
		ID: id, Scope: scope, Version: current + 1, Digest: digest, Body: layer, Summary: summary,
		AuthoredBy: string(actor.Kind) + ":" + actor.ID, CreatedAt: now,
	}, nil
}

// keepVersion writes a version and its event, which holds the whole version.
func keepVersion(writer ports.RosterWriter, appendEvent func(ledger.EventDraft) (ledger.Event, error), version roster.Version, actor ledger.Actor) error {
	if _, err := writer.Insert(version); err != nil {
		return err
	}
	data, err := json.Marshal(version)
	if err != nil {
		return err
	}
	_, err = appendEvent(ledger.EventDraft{
		Type: roster.EventChanged, Actor: actor, Subject: ledger.Subject{Kind: roster.SubjectKind, ID: version.Scope}, Data: data,
	})
	return err
}

// pinAgent chooses the roster agent a claim's lane runs as and pins the roster it starts with (ADR-0025):
// the agent the work order names, or the default mapping's, one implementation tier above the agent that
// ran the work this attempt reworks. A work order whose agent the roster cannot give is not eligible.
func pinAgent(tx ports.WriteTx, order workorders.WorkOrder, priorClaim string, claim *workorders.Claim) error {
	view, err := ResolveIn(tx.Roster())
	if err != nil {
		return err
	}
	reworks, err := reworkedAgent(tx, order, priorClaim)
	if err != nil {
		return err
	}
	agent, err := view.Resolved.LaneAgent(roster.LaneWork{Kind: string(order.Kind), Size: order.Size, Named: order.Agent, Reworks: reworks})
	if err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrIneligible, err)
	}
	claim.Agent = agent.Name
	claim.Roster = workorders.RosterPin{Installation: view.Installation, Project: view.Project, Digest: view.Resolved.Digest}
	return nil
}

// reworkedAgent is the agent that ran the work this attempt reworks: the attempt before it, when a review
// or a member sent that attempt back, or the work a `rework` work order reworks. Empty means this attempt
// reworks nothing, because a failed review is an actual failure and a crash is not.
func reworkedAgent(tx ports.WriteTx, order workorders.WorkOrder, priorClaim string) (string, error) {
	if priorClaim != "" {
		prior, err := tx.Claims().Find(priorClaim)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return "", err
		}
		note, err := tx.Evidence().Reworked(order.ID)
		switch {
		case err == nil && prior.Agent != "" && note.Attempt == prior.Fence:
			return prior.Agent, nil
		case err != nil && !errors.Is(err, ports.ErrNotFound):
			return "", err
		}
	}
	if order.Kind != workorders.KindRework || order.ReworkOf == "" {
		return "", nil
	}
	of, err := tx.WorkOrders().Get(order.ReworkOf)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && of.ClaimID == "") {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	ran, err := tx.Claims().Find(of.ClaimID)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	return ran.Agent, err
}

// pinnedRoster resolves the roster a claim pinned, which its lane renders on every start and resume. A
// claim made before the roster pinned none, and its lane receives no agents.
func pinnedRoster(reader ports.RosterReader, pin workorders.RosterPin) (*roster.Resolved, error) {
	if pin.Digest == "" {
		return nil, nil
	}
	layer := func(scope string, version int64) (roster.Layer, error) {
		if version == 0 {
			return roster.Layer{}, nil
		}
		found, err := reader.Version(scope, version)
		return found.Body, err
	}
	installation, err := layer(roster.ScopeInstallation, pin.Installation)
	if err != nil {
		return nil, err
	}
	project, err := layer(roster.ScopeProject, pin.Project)
	if err != nil {
		return nil, err
	}
	resolved := roster.Resolve(installation, project)
	return &resolved, nil
}

// agentProblem is why the definition of ready refuses a work order's agent, or empty when the roster gives
// it: the agent it names, or the default mapping's for its kind and size, must be known and enabled.
func agentProblem(reader ports.RosterReader, order workorders.WorkOrder) (string, error) {
	view, err := ResolveIn(reader)
	if err != nil {
		return "", err
	}
	_, err = view.Resolved.DefaultAgent(string(order.Kind), order.Size, order.Agent)
	if errors.Is(err, roster.ErrUnknownAgent) || errors.Is(err, roster.ErrDisabledAgent) {
		return err.Error(), nil
	}
	return "", err
}
