package worker

import (
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
)

// rosterSession is the kind of session this lane is, as the roster reads it (ADR-0025).
func (r *laneRun) rosterSession() roster.Session {
	switch {
	case r.assisting():
		return roster.SessionAssistant
	case r.orchestrating():
		return roster.SessionOrchestrator
	case r.reviewing():
		return roster.SessionReview
	}
	return roster.SessionWriting
}

// sessionAgents are the roster agents this session may start, from the roster the lane's claim pinned,
// each fitted inside the session's rules and given the model this driver's profile maps its class to. A
// lane whose claim pinned no roster gets none.
func (r *laneRun) sessionAgents(profile lanes.Profile, support roster.Support) []ports.SessionAgent {
	if r.lease.Roster == nil {
		return nil
	}
	var out []ports.SessionAgent
	for _, fitted := range r.lease.Roster.ForSession(r.rosterSession(), support) {
		out = append(out, ports.SessionAgent{Fitted: fitted, Model: fitted.ModelFor(profile.Driver, profile.Agent.Classes)})
	}
	return out
}

// laneAgent is the roster agent this lane runs as, and false when it runs as none.
func (r *laneRun) laneAgent() (roster.ResolvedAgent, bool) {
	if r.lease.Roster == nil || r.lease.Agent == "" || r.orchestrating() {
		return roster.ResolvedAgent{}, false
	}
	return r.lease.Roster.Agent(r.lease.Agent)
}

// asAgent is the model, the effort, and the denied categories of the session, from the agent the lane runs
// as: its class through the profile's `[agent.classes]`, or its own model for this driver, in place of the
// profile's model, and its effort. A lane's own profile layer still wins, and the agent's denials only
// narrow the lane's rules (ADR-0025).
func (r *laneRun) asAgent(prepared PreparedLane) (string, string, []string) {
	agent, ok := r.laneAgent()
	if !ok {
		return prepared.Profile.Agent.Model, "", nil
	}
	model, effort := agentRuns(prepared.Profile, agent.Agent, prepared.LaneModel, prepared.LaneEffort)
	return model, effort, append([]string(nil), agent.Tools.Deny...)
}

// runsAs is the definition of the roster agent this lane runs as, and nil when it runs as none.
func (r *laneRun) runsAs() *roster.Agent {
	agent, ok := r.laneAgent()
	if !ok {
		return nil
	}
	return &agent.Agent
}

// agentRuns is the model and the effort a session runs as a roster agent: the model the profile's
// `[agent.classes]` maps the agent's class to, or the agent's own model for the profile's driver, at the
// agent's effort. The lane's own profile layer wins where laneModel or laneEffort says it set either
// (ADR-0025). The session and the check of a delegate's layer both read it, so they never disagree on what
// a lane runs.
func agentRuns(profile lanes.Profile, agent roster.Agent, laneModel, laneEffort bool) (string, string) {
	model, effort := profile.Agent.Model, agent.Effort
	if mapped := agent.ModelFor(profile.Driver, profile.Agent.Classes); mapped != "" && !laneModel {
		model = mapped
	}
	if laneEffort {
		effort = profile.Agent.Effort
	}
	return model, effort
}

// rosterText is the roster's item of this lane's context pack: the routing guide and the catalog of the
// agents it may start, and the instructions and report format of the agent it runs as. A driver with no
// subagents gets the catalog it would have, and is told to do the work itself.
func (r *laneRun) rosterText(support roster.Support) string {
	if r.lease.Roster == nil || r.orchestrating() {
		return ""
	}
	delegates := support.Subagents == roster.SubagentsNative
	if !delegates {
		support = roster.Support{Subagents: roster.SubagentsNative, SubagentTools: roster.ToolsEnforced}
	}
	var lane *roster.Agent
	if agent, ok := r.laneAgent(); ok {
		lane = &agent.Agent
	}
	return roster.Text(r.lease.Roster.Guide, r.lease.Roster.ForSession(r.rosterSession(), support), delegates, lane)
}
