package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ownerAssistant marks the record of a member's assistant's run, which, like an orchestrator's, has no work
// order, and which reads only the context tools (ADR-0024).
const ownerAssistant = "assistant"

// assistantRoleFile holds an assistant's role, byte-identical across its generations, as an orchestrator's is.
const assistantRoleFile = "assistant-role.md"

// assistantDeadline bounds a call the worker makes for an assistant's run.
const assistantDeadline = 5 * time.Second

// assistantTools are the ropeshift MCP tools an assistant's session holds: the context group.
var assistantTools = []string{lanes.ToolContextGet, lanes.ToolDecisionSearch, lanes.ToolRequirementGet, lanes.ToolQuestionSearch}

// assistantDenial is what the hook tells an assistant's session of a call no rule allows.
const assistantDenial = "The assistant reads, searches, and runs ropeshift commands; it never edits, writes, or runs another command. Use ropeshift for any change."

// leaseless says a record's run has no work order: an orchestrator's, or an assistant's.
func leaseless(owner string) bool { return owner == lanes.OwnerOrchestrator || owner == ownerAssistant }

// assisting says this run is a member's assistant, which desired state names as the run's kind.
func (r *laneRun) assisting() bool { return r.lease.Kind == ledger.RunKindAssistant }

// assistantLease is how an assistant's run reaches the lane code: a lease with no work order, as an
// orchestrator's, whose kind says it is an assistant's, and which carries the messages to it.
func assistantLease(desired ledger.DesiredAssistant) ledger.DesiredLease {
	return ledger.DesiredLease{
		Project: desired.Project, Lane: desired.Lane, State: string(workorders.Claimed), Kind: ledger.RunKindAssistant, Messages: desired.Messages,
	}
}

// assistantState is what a worker holds of one assistant's run in memory: the grant of its generation, and
// the compactions it has acted on. A restart reads desired state again, and an adopted session keeps the
// token it has in its environment.
type assistantState struct {
	mu          sync.Mutex
	grant       ledger.AssistantGrant
	compactions int64
	posted      map[string]bool
	// briefed says the turn of a generation's first prompt ended: what the session wrote in it answers the
	// brief, and no member reads it.
	briefed bool
	// refused says the vendor refused the session a generation resumed, so the next starts over.
	refused bool
}

// noteTurn keeps that the first prompt's turn ended.
func (s *assistantState) noteTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.briefed = true
}

// assistantOf reads the desired state of the assistant a run serves.
func (w *Worker) assistantOf(ctx context.Context, lane string) (ledger.DesiredAssistant, bool) {
	state, err := w.journal.Desired(ctx)
	if err != nil {
		return ledger.DesiredAssistant{}, false
	}
	for _, desired := range state.Assistants {
		if desired.Lane == lane {
			return desired, true
		}
	}
	return ledger.DesiredAssistant{}, false
}

// projectName names an assistant's project as a command reads it: as desired state lists it, else as its
// credential's grant names it, else by its ID.
func (w *Worker) projectName(ctx context.Context, project string, grant ledger.AssistantGrant) string {
	if state, err := w.journal.Desired(ctx); err == nil {
		for _, served := range state.Projects {
			if served.ID == project && served.Name != "" {
				return served.Name
			}
		}
	}
	if grant.Context.ProjectName != "" {
		return grant.Context.ProjectName
	}
	return grant.Context.Project
}

// assistantGrant asks the control plane for this generation's credential, after the worker uploaded what
// it holds, so the ledger knows the generation it asks for has begun.
func (r *laneRun) assistantGrant(ctx context.Context) (ledger.AssistantGrant, error) {
	desired, ok := r.w.assistantOf(ctx, r.lease.Lane)
	if !ok {
		return ledger.AssistantGrant{}, fmt.Errorf("desired state names no conversation for run %s", r.lease.Lane)
	}
	bounded, cancel := context.WithTimeout(ctx, assistantDeadline)
	defer cancel()
	if err := r.w.Upload(bounded); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
	grant, err := r.w.link.AssistantCredential(bounded, ledger.AssistantCredentialRequest{
		Auth: r.w.Auth(), Epoch: r.w.epoch(), Project: r.lease.Project, Conversation: desired.Conversation, Lane: r.lease.Lane,
		Generation: r.currentGeneration(),
	})
	if err != nil {
		return ledger.AssistantGrant{}, err
	}
	r.assistant().mu.Lock()
	r.assistant().grant = grant
	r.assistant().mu.Unlock()
	return grant, nil
}

// assistant is this run's assistant state, made on first use.
func (r *laneRun) assistant() *assistantState {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	if r.assisted == nil {
		r.assisted = &assistantState{posted: map[string]bool{}}
	}
	return r.assisted
}

// assistantEnvironment adds the credential and the project to a session's environment, after the lane
// environment's filter, which drops any credential a worker inherits. Nothing here reaches the journal.
func assistantEnvironment(environment []string, grant ledger.AssistantGrant, project string) []string {
	out := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		if strings.HasPrefix(entry, authority.EnvDelegateToken+"=") || strings.HasPrefix(entry, envProject+"=") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, authority.EnvDelegateToken+"="+grant.Token, envProject+"="+project)
}

// envProject names the project a `ropeshift` command acts in.
const envProject = "ROPESHIFT_PROJECT"

// assistantReads makes a read-only worktree of every active repository of the project, detached at its base
// branch's tip, under the run's private directory, for each generation. One that cannot be made does not
// stop the session: the context says what it could not read.
func (r *laneRun) assistantReads(ctx context.Context, private string, repositories []ledger.AssistantRepository) []lanes.ReadTree {
	trees, offered := r.rt.Provisioner.Worktrees.(ports.ReviewTrees)
	out := make([]lanes.ReadTree, 0, len(repositories))
	for _, repository := range repositories {
		tree := lanes.ReadTree{Name: repository.Name, Ref: repository.Base}
		checkout, err := r.rt.Repository(ctx, r.lease.Project, repository.ID)
		switch {
		case !offered:
			tree.Detail = "this worker's worktrees cannot check a commit out with no branch"
		case err != nil:
			tree.Detail = fmt.Sprintf("this machine keeps no checkout of it: %v", err)
		default:
			place := filepath.Join(private, "repositories", readName(repository))
			_ = r.rt.Provisioner.Worktrees.Discard(ctx, checkout, place)
			_ = r.rt.Provisioner.Worktrees.Fetch(ctx, checkout, []string{repository.Base})
			if _, err := trees.Create(ctx, ports.WorktreeSpec{Repository: checkout, Path: place, Base: repository.Base, Detached: true}); err != nil {
				tree.Detail = fmt.Sprintf("checking it out at %s failed: %v", repository.Base, err)
			} else {
				tree.Path = place
			}
		}
		out = append(out, tree)
	}
	return out
}

func readName(repository ledger.AssistantRepository) string {
	name := repository.Name
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return repository.ID
	}
	return name
}

// writeAssistantRole leaves the assistant's role where the driver reads it, the same bytes for every
// generation of the run's conversation.
func (r *laneRun) writeAssistantRole(ctx context.Context) (string, error) {
	grant := r.assistant().grantNow()
	member := grant.Context.MemberName
	if member == "" {
		member = grant.Context.Member
	}
	path := filepath.Join(r.rt.configDir(r.lease.Lane), assistantRoleFile)
	if err := os.WriteFile(path, []byte(contextpacks.AssistantRole(member, grant.Context.ProjectName)), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *assistantState) grantNow() ledger.AssistantGrant {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grant
}

// briefAssistant is a generation's first prompt: the project as it stands, with the read-only trees this
// generation reads, and, for a session that starts with no context of its own, the conversation so far.
func (r *laneRun) briefAssistant(resumed bool) string {
	grant := r.assistant().grantNow()
	facts := contextpacks.AssistantFacts{
		ProjectName: grant.Context.ProjectName, Plans: make([]string, 0, len(grant.Context.Plans)), Work: grant.Context.Work,
	}
	trees := r.readTrees()
	for i, repository := range grant.Context.Repositories {
		line := contextpacks.AssistantRepository{Name: repository.Name, Base: repository.Base}
		if i < len(trees) {
			line.Path, line.Detail = trees[i].Path, trees[i].Detail
		}
		facts.Repositories = append(facts.Repositories, line)
	}
	for _, plan := range grant.Context.Plans {
		facts.Plans = append(facts.Plans, plan.ID+" "+plan.Title)
	}
	if !resumed {
		for _, entry := range grant.History {
			facts.History = append(facts.History, contextpacks.AssistantHistory{Kind: entry.Kind, Body: entry.Body})
		}
	}
	return contextpacks.AssistantBrief(facts)
}

// assistantHome holds what this machine keeps of one conversation: the directory its session runs in at
// every start, and the vendor session it last had. The vendor keeps a session by the directory it ran in,
// so a run that started anywhere else could not resume it.
func (rt *LaneRuntime) assistantHome(conversation string) string {
	return filepath.Join(rt.StateDir, "assistants", conversation)
}

// assistantSession is where the vendor session an assistant's next start resumes is kept.
func (rt *LaneRuntime) assistantSession(conversation string) string {
	return filepath.Join(rt.assistantHome(conversation), "kept-session.json")
}

// assistantDir is the directory an assistant's session runs in, the same for every run of its conversation.
func (r *laneRun) assistantDir(ctx context.Context) (string, error) {
	desired, ok := r.w.assistantOf(ctx, r.lease.Lane)
	if !ok {
		return "", fmt.Errorf("desired state names no conversation for run %s", r.lease.Lane)
	}
	dir := filepath.Join(r.rt.assistantHome(desired.Conversation), "session")
	return dir, os.MkdirAll(dir, 0o700)
}

type keptSession struct {
	SessionID string `json:"session_id"`
}

// rememberSession keeps the vendor's session of this run's conversation, so a later start resumes it.
func (r *laneRun) rememberSession(ctx context.Context, session string) {
	desired, ok := r.w.assistantOf(ctx, r.lease.Lane)
	if !ok || session == "" {
		return
	}
	path := r.rt.assistantSession(desired.Conversation)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		r.w.config.Report(err)
		return
	}
	raw, err := json.Marshal(keptSession{SessionID: session})
	if err == nil {
		err = os.WriteFile(path, raw, 0o600)
	}
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: keeping its session: %w", r.lease.Lane, err))
	}
}

// resumable reads the vendor session this run's conversation last had on this machine, unless the run
// begins after a clear, which starts with an empty context.
func (r *laneRun) resumable(ctx context.Context) string {
	desired, ok := r.w.assistantOf(ctx, r.lease.Lane)
	if !ok {
		return ""
	}
	path := r.rt.assistantSession(desired.Conversation)
	if desired.Fresh {
		_ = os.Remove(path)
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var kept keptSession
	if json.Unmarshal(raw, &kept) != nil {
		return ""
	}
	return kept.SessionID
}

// noteRefusedResume keeps that the vendor refused the session this generation resumed: it ended before it
// bound, and nobody stopped it.
func (r *laneRun) noteRefusedResume(ctx context.Context) {
	if !r.assisting() || r.resumeFrom == "" || r.bound || !r.orchestratorLost(ctx) {
		return
	}
	r.w.config.Report(fmt.Errorf("lane %s: the vendor refused to resume session %s, so the next generation starts over", r.lease.Lane, r.resumeFrom))
	state := r.assistant()
	state.mu.Lock()
	state.refused = true
	state.mu.Unlock()
}

// resumeRefused says the last generation's resume was refused, and the next has not begun.
func (r *laneRun) resumeRefused() bool {
	if !r.assisting() {
		return false
	}
	state := r.assistant()
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.refused
}

// startOver begins the generation after a refused resume: a fresh session, whose first prompt holds the
// conversation so far, and the refused session is forgotten, so no later start tries it again. It counts
// toward the day as any planned generation does, and not toward the breaker.
func (r *laneRun) startOver(ctx context.Context) bool {
	state := r.assistant()
	state.mu.Lock()
	state.refused = false
	state.mu.Unlock()
	if desired, ok := r.w.assistantOf(ctx, r.lease.Lane); ok {
		if err := os.Remove(r.rt.assistantSession(desired.Conversation)); err != nil && !errors.Is(err, os.ErrNotExist) {
			r.w.config.Report(fmt.Errorf("lane %s: forgetting its session: %w", r.lease.Lane, err))
		}
	}
	next := r.currentGeneration() + 1
	if !r.chargePlanned(ctx, next) {
		return false
	}
	// The session's end left the lane in backoff, and a refused resume waits for no delay.
	r.signal(ctx, lanes.OnDelayElapsed)
	return r.nextGeneration(ctx, next, ledger.StartReset)
}

// postReply records one message the session wrote as a reply in its conversation, apart from the stream,
// which must keep reading. The vendor's message ID makes a retry record it once.
func (r *laneRun) postReply(ctx context.Context, event ports.DriverEvent) {
	state := r.assistant()
	state.mu.Lock()
	if !state.briefed || state.posted[event.MessageID] {
		state.mu.Unlock()
		return
	}
	state.posted[event.MessageID] = true
	state.mu.Unlock()
	desired, ok := r.w.assistantOf(ctx, r.lease.Lane)
	if !ok {
		return
	}
	request := ledger.AssistantEntryRequest{
		Auth: r.w.Auth(), Epoch: r.w.epoch(), Project: r.lease.Project, Conversation: desired.Conversation, Lane: r.lease.Lane,
		ID: event.MessageID, Body: event.Text,
	}
	r.stopped.Go(func() {
		ctx := context.WithoutCancel(ctx)
		for attempt := range 3 {
			bounded, cancel := context.WithTimeout(ctx, assistantDeadline)
			err := r.w.link.AssistantEntry(bounded, request)
			cancel()
			if err == nil || errors.Is(err, workorders.ErrInvalid) {
				if err != nil {
					r.w.config.Report(fmt.Errorf("lane %s: a reply: %w", r.lease.Lane, err))
				}
				return
			}
			if attempt == 2 {
				r.w.config.Report(fmt.Errorf("lane %s: a reply: %w", r.lease.Lane, err))
				return
			}
			time.Sleep(time.Duration(attempt+1) * retryEvery)
		}
	})
}

// assistantRun finds the run of one assistant's lane, if this process holds one.
func (w *Worker) assistantRuns() []*laneRun {
	var out []*laneRun
	for _, run := range w.runs() {
		if run.assisting() {
			out = append(out, run)
		}
	}
	return out
}

// tendAssistants stops each assistant's run that desired state no longer names, which is how a stop, a
// clear, a delete, or an idle stop reaches a session, and asks a running one for each compaction its member
// asked for since its start. It never fails a pass.
func (w *Worker) tendAssistants(ctx context.Context) {
	runs := w.assistantRuns()
	if len(runs) == 0 {
		return
	}
	state, err := w.desired(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading the assistants asked for: %w", err))
		return
	}
	named := map[string]ledger.DesiredAssistant{}
	for _, desired := range state.Assistants {
		named[desired.Lane] = desired
	}
	for _, run := range runs {
		desired, ok := named[run.lease.Lane]
		if !ok {
			run.stopOrchestrator(ctx)
			continue
		}
		run.compactWhenAsked(ctx, desired.Compactions)
	}
}

// compactWhenAsked asks a session at rest to compact once for each compaction its member asked for. A
// session in a turn hears on a later pass.
func (r *laneRun) compactWhenAsked(ctx context.Context, asked int64) {
	state := r.assistant()
	state.mu.Lock()
	due := asked > state.compactions
	state.mu.Unlock()
	if !due || r.sessionHandle().ID == "" || r.sessionOver.Load() || r.state.current() != lanes.Idle {
		return
	}
	if r.requestCompact(ctx) {
		state.mu.Lock()
		state.compactions = asked
		state.mu.Unlock()
	}
}

// assistantApproval answers a call of an assistant's session that no rule allowed: denied, with a reason,
// and never raised as a request, because an assistant changes things only through ropeshift.
func assistantApproval() lanes.Reply {
	return answer(lanes.ApprovalDeny, assistantDenial, "")
}

// assistantMode is the one permission mode an assistant's session runs in: the mode that asks, so a call no
// rule allows reaches the hook, which denies it, whatever mode the member's profile gives lanes.
const assistantMode = "default"

// permissionMode is the mode a session runs in: its profile's, except for an assistant's.
func (r *laneRun) permissionMode(profile lanes.Profile) string {
	if r.assisting() {
		return assistantMode
	}
	return profile.Agent.PermissionMode
}
