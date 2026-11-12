package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/roster"
)

// RosterView is the agent roster as a scope resolves it (ADR-0025): the installation's on its own, or a
// project's, with the version of each layer it folded, zero for a layer with none, and each agent's
// rendered model per driver.
type RosterView struct {
	Scope               string          `json:"scope"`
	InstallationVersion int64           `json:"installation_version"`
	ProjectVersion      int64           `json:"project_version"`
	Roster              roster.Resolved `json:"roster"`
	// Models is each agent's model on each driver whose class mapping this control plane read, by agent
	// and then driver.
	Models map[string]map[string]string `json:"models,omitempty"`
}

// RosterChange is one change to a scope's layer. Scope names it, and is never inferred.
type RosterChange struct {
	Scope  string        `json:"scope"`
	Change roster.Change `json:"change"`
}

// RosterRevert records an earlier version of a scope's layer as its newest.
type RosterRevert struct {
	Scope   string `json:"scope"`
	Version int64  `json:"version"`
}

// RosterEntry is one version of a scope's layer, with what it changed from the version before it.
type RosterEntry struct {
	roster.Version
	Difference []string `json:"difference"`
}

// RosterHistory is a scope's versions, newest first.
type RosterHistory struct {
	Scope    string        `json:"scope"`
	Versions []RosterEntry `json:"versions"`
}

func historyQuery(scope, agent string) string {
	query := url.Values{"scope": {scope}}
	if agent != "" {
		query.Set("agent", agent)
	}
	return "?" + query.Encode()
}

// InstallationRoster reads the roster the installation resolves to with no project.
func (c *Client) InstallationRoster(ctx context.Context) (RosterView, error) {
	var out RosterView
	err := c.Do(ctx, http.MethodGet, "/v1/roster", nil, &out)
	return out, err
}

// ChangeInstallationRoster changes the installation's layer, which only a member can do.
func (c *Client) ChangeInstallationRoster(ctx context.Context, change roster.Change) (roster.Version, error) {
	var out roster.Version
	err := c.Do(ctx, http.MethodPost, "/v1/roster/changes", RosterChange{Scope: roster.ScopeInstallation, Change: change}, &out)
	return out, err
}

// RevertInstallationRoster records an earlier version of the installation's layer as its newest.
func (c *Client) RevertInstallationRoster(ctx context.Context, version int64) (roster.Version, error) {
	var out roster.Version
	err := c.Do(ctx, http.MethodPost, "/v1/roster/revert", RosterRevert{Scope: roster.ScopeInstallation, Version: version}, &out)
	return out, err
}

// InstallationRosterHistory reads the installation layer's versions, of one agent when named.
func (c *Client) InstallationRosterHistory(ctx context.Context, agent string) (RosterHistory, error) {
	var out RosterHistory
	err := c.Do(ctx, http.MethodGet, "/v1/roster/history"+historyQuery(roster.ScopeInstallation, agent), nil, &out)
	return out, err
}

// Roster reads the roster this project resolves to.
func (c *ProjectClient) Roster(ctx context.Context) (RosterView, error) {
	var out RosterView
	err := c.do(ctx, http.MethodGet, "/v1/roster", nil, &out)
	return out, err
}

// ChangeRoster changes this project's layer, which only a member can do.
func (c *ProjectClient) ChangeRoster(ctx context.Context, change roster.Change) (roster.Version, error) {
	var out roster.Version
	err := c.do(ctx, http.MethodPost, "/v1/roster/changes", RosterChange{Scope: roster.ScopeProject, Change: change}, &out)
	return out, err
}

// RevertRoster records an earlier version of this project's layer as its newest.
func (c *ProjectClient) RevertRoster(ctx context.Context, version int64) (roster.Version, error) {
	var out roster.Version
	err := c.do(ctx, http.MethodPost, "/v1/roster/revert", RosterRevert{Scope: roster.ScopeProject, Version: version}, &out)
	return out, err
}

// RosterHistory reads this project layer's versions, of one agent when named.
func (c *ProjectClient) RosterHistory(ctx context.Context, agent string) (RosterHistory, error) {
	var out RosterHistory
	err := c.do(ctx, http.MethodGet, "/v1/roster/history"+historyQuery(roster.ScopeProject, agent), nil, &out)
	return out, err
}
