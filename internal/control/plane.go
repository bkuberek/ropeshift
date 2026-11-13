package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Plane is the control plane's side of the plane protocol for an installation that holds several projects
// (ADR-0021). One worker per machine serves every active project: the plane reads its desired state from
// every project's ledger, names the project on every item, and routes each call to the project it names.
// The worker's own records, its session, its autonomy, and its orchestrator, are the installation's.
//
// It has the shape of ports.WorkerLink, as Workers does.
type Plane struct {
	registry     ports.Projects
	installation ports.Installation
	// home is the project a call that names none acts in.
	home     ledger.WorkspaceID
	options  WorkerOptions
	matching func(ports.Store) ports.Matcher

	mu sync.Mutex
	// served holds the plane of each project a call reached, by the project's ID.
	served map[ledger.WorkspaceID]*Workers
}

// NewPlane returns the plane over an installation's registry and its shared records. matching makes the
// question ladder's matcher of a project's Store, and nil leaves an orchestrator to search for itself.
func NewPlane(registry ports.Projects, installation ports.Installation, home ledger.WorkspaceID, options WorkerOptions, matching func(ports.Store) ports.Matcher) *Plane {
	options = withPlaneDefaults(options)
	return &Plane{
		registry: registry, installation: installation, home: home, options: options, matching: matching,
		served: map[ledger.WorkspaceID]*Workers{},
	}
}

func withPlaneDefaults(options WorkerOptions) WorkerOptions {
	if options.Protocol == 0 {
		options.Protocol = ledger.ProtocolVersion
	}
	if options.HeartbeatMs == 0 {
		options.HeartbeatMs = defaultHeartbeatMs
	}
	if options.LeaseTTLMs == 0 {
		options.LeaseTTLMs = defaultLeaseTTLMs
	}
	return options
}

// project reads one project of the registry by its ID.
func (p *Plane) project(ctx context.Context, id ledger.WorkspaceID) (ledger.Project, error) {
	projects, err := p.registry.List(ctx)
	if err != nil {
		return ledger.Project{}, err
	}
	for _, project := range projects {
		if project.ID == id {
			return project, nil
		}
	}
	return ledger.Project{}, fmt.Errorf("project %s: %w", id, ports.ErrNotFound)
}

// serving lists the projects a worker serves, which are the active ones. An archived project takes no
// claims and is offered nothing.
func (p *Plane) serving(ctx context.Context) ([]ledger.Project, error) {
	projects, err := p.registry.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []ledger.Project
	for _, project := range projects {
		if project.State != ledger.ProjectActive {
			continue
		}
		out = append(out, project)
	}
	return out, nil
}

// workers returns the plane of one project, opening its ledger on first use.
func (p *Plane) workers(ctx context.Context, project ledger.Project) (*Workers, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w, ok := p.served[project.ID]; ok {
		return w, nil
	}
	store, err := p.registry.Open(ctx, project)
	if err != nil {
		return nil, err
	}
	options := p.options
	if p.matching != nil {
		options.Matcher = p.matching(store)
	}
	w := NewWorkers(store, project.ID, options)
	p.served[project.ID] = w
	return w, nil
}

// Forget drops the plane of a project the installation removed.
func (p *Plane) Forget(id ledger.WorkspaceID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.served, id)
}

// worker reads a worker by its credential, and its epoch when a session must be current. A session a
// worker opened on a protocol this plane no longer serves is refused on every call, so a worker that
// outlives an upgrade learns it must be upgraded too.
func (p *Plane) worker(ctx context.Context, auth ledger.WorkerAuth, epoch *int64) (ledger.WorkerRecord, error) {
	var record ledger.WorkerRecord
	err := p.installation.Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		if record, err = authenticate(tx.Workers(), auth); err != nil {
			return err
		}
		if record.Protocol != 0 && record.Protocol < ledger.ProtocolOldest {
			return &ledger.ProtocolError{Worker: record.Protocol, Oldest: ledger.ProtocolOldest, Newest: p.options.Protocol}
		}
		if epoch != nil && record.Epoch != *epoch {
			return fmt.Errorf("%w: %d, and this session is %d", ledger.ErrStaleEpoch, record.Epoch, *epoch)
		}
		return nil
	})
	return record, err
}

// route resolves the project a call names to its plane. A call that names none acts in the default
// project. A change to an archived project is refused, because it is read-only
// until a member restores it.
func (p *Plane) route(ctx context.Context, auth ledger.WorkerAuth, id string, change bool) (*Workers, ledger.Project, error) {
	if _, err := p.worker(ctx, auth, nil); err != nil {
		return nil, ledger.Project{}, err
	}
	return p.routeFor(ctx, id, change)
}

func (p *Plane) routeFor(ctx context.Context, id string, change bool) (*Workers, ledger.Project, error) {
	target := ledger.WorkspaceID(id)
	if id == "" {
		target = p.home
	}
	project, err := p.project(ctx, target)
	if err != nil {
		return nil, ledger.Project{}, err
	}
	if change && project.State == ledger.ProjectArchived {
		return nil, ledger.Project{}, fmt.Errorf("%w: project %s takes nothing until a member restores it", ledger.ErrProjectArchived, project.Name)
	}
	w, err := p.workers(ctx, project)
	return w, project, err
}

// revisions reads where the installation's records and each served project stand. The installation comes
// first: a change after it moves a revision again, and the worker reads once more.
func (p *Plane) revisions(ctx context.Context) (map[string]int64, []ledger.DesiredProject, int64, error) {
	var shared int64
	err := p.installation.Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		shared, err = tx.Revision()
		return err
	})
	if err != nil {
		return nil, nil, 0, err
	}
	projects, err := p.serving(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	revisions := map[string]int64{ledger.RevisionInstallation: shared}
	described := make([]ledger.DesiredProject, 0, len(projects))
	sum := shared
	for _, project := range projects {
		w, err := p.workers(ctx, project)
		if err != nil {
			return nil, nil, 0, err
		}
		workspace, err := w.workspace(ctx)
		if err != nil {
			return nil, nil, 0, err
		}
		revisions[string(project.ID)] = workspace.Rev
		described = append(described, ledger.DesiredProject{ID: string(project.ID), Name: project.Name, FleetMode: workspace.FleetMode})
		sum += workspace.Rev
	}
	return revisions, described, sum, nil
}

// homeMode is the one fleet mode the link's contract has a reply carry beside the mode of each project:
// the only project's, and running when the worker serves several.
func homeMode(projects []ledger.DesiredProject) ledger.FleetMode {
	if len(projects) == 1 {
		return projects[0].FleetMode
	}
	return ledger.FleetRun
}

// CreateJoinToken makes a single-use token for one worker of the installation.
func (p *Plane) CreateJoinToken(ctx context.Context, actor ledger.Actor) (string, error) {
	return createJoinToken(ctx, p.installation, actor)
}

// Register exchanges a join token for a credential.
func (p *Plane) Register(ctx context.Context, registration ledger.Registration) (ledger.WorkerCredential, error) {
	return registerWorker(ctx, p.installation, registration)
}

// OpenSession opens a new epoch. A worker on a protocol this plane no longer serves is refused.
func (p *Plane) OpenSession(ctx context.Context, request ledger.SessionRequest) (ledger.Session, error) {
	session, err := openSession(ctx, p.installation, p.options, request)
	if err != nil {
		return ledger.Session{}, err
	}
	_, _, session.DesiredRev, err = p.revisions(ctx)
	return session, err
}

// Heartbeat renews each lease in the project it names, and answers with the revision of every project the
// worker serves. A lease of a project the installation no longer holds is fenced.
func (p *Plane) Heartbeat(ctx context.Context, beat ledger.Heartbeat) (ledger.HeartbeatReply, error) {
	if beat.Nonce == "" {
		return ledger.HeartbeatReply{}, fmt.Errorf("%w: a heartbeat needs a nonce", workorders.ErrInvalid)
	}
	_, err := p.worker(ctx, beat.Auth, &beat.Epoch)
	if err != nil {
		return ledger.HeartbeatReply{}, err
	}
	reply := ledger.HeartbeatReply{Nonce: beat.Nonce, Leases: []ledger.RenewedLease{}, Fenced: []ledger.FencedLease{}}
	actor := ledger.Actor{Kind: ledger.ActorWorker, ID: beat.Auth.WorkerID}
	for _, held := range beat.Leases {
		w, project, err := p.routeFor(ctx, held.Project, false)
		if errors.Is(err, ports.ErrNotFound) {
			reply.Fenced = append(reply.Fenced, ledger.FencedLease{Project: held.Project, WorkOrder: held.WorkOrder, Fence: held.Fence})
			continue
		}
		if err != nil {
			return ledger.HeartbeatReply{}, err
		}
		lease := workorders.Lease{WorkOrder: held.WorkOrder, ClaimID: held.Claim, Fence: held.Fence}
		err = w.renewOrReturn(ctx, lease, actor)
		var fenced *workorders.FencedError
		name := string(project.ID)
		switch {
		case err == nil:
			reply.Leases = append(reply.Leases, ledger.RenewedLease{Project: name, WorkOrder: held.WorkOrder, Fence: held.Fence, TTLMs: p.options.LeaseTTLMs})
		case errors.As(err, &fenced):
			reply.Fenced = append(reply.Fenced, ledger.FencedLease{Project: name, WorkOrder: held.WorkOrder, Fence: held.Fence, CurrentFence: fenced.Current})
		case errors.Is(err, ports.ErrNotFound):
			reply.Fenced = append(reply.Fenced, ledger.FencedLease{Project: name, WorkOrder: held.WorkOrder, Fence: held.Fence})
		case errors.Is(err, workorders.ErrExpired):
		default:
			return ledger.HeartbeatReply{}, err
		}
	}
	revisions, projects, sum, err := p.revisions(ctx)
	if err != nil {
		return ledger.HeartbeatReply{}, err
	}
	reply.DesiredRev, reply.FleetMode, reply.Revisions = sum, homeMode(projects), revisions
	err = p.installation.Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		reply.ServerTimeMs, err = tx.Now()
		return err
	})
	if err != nil {
		return ledger.HeartbeatReply{}, err
	}
	return reply, nil
}

// Desired reads what this worker should hold in every project it serves, each item naming its project. Its
// orchestrator and the bound its autonomy holds a lane to are the installation's, and read once.
func (p *Plane) Desired(ctx context.Context, request ledger.DesiredRequest) (ledger.DesiredState, error) {
	record, err := p.worker(ctx, request.Auth, nil)
	if err != nil {
		return ledger.DesiredState{}, err
	}
	revisions, projects, sum, err := p.revisions(ctx)
	if err != nil {
		return ledger.DesiredState{}, err
	}
	state := ledger.DesiredState{Rev: sum, FleetMode: homeMode(projects), Revisions: revisions, Projects: projects, Leases: []ledger.DesiredLease{}}
	state.Unchanged = request.Since == sum
	if request.Revisions != nil {
		state.Unchanged = maps.Equal(request.Revisions, revisions)
	}
	if state.Unchanged {
		return state, nil
	}
	err = p.installation.Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		if state.Orchestrator, err = desiredOrchestrator(tx.Workers(), record.ID); err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		facts, err := readAutonomy(tx.Policies(), tx.Workers(), record, now)
		state.MaxRespawnsPerDay = facts.respawns()
		return err
	})
	if err != nil {
		return ledger.DesiredState{}, err
	}
	served, err := p.serving(ctx)
	if err != nil {
		return ledger.DesiredState{}, err
	}
	for _, project := range served {
		w, err := p.workers(ctx, project)
		if err != nil {
			return ledger.DesiredState{}, err
		}
		var part ledger.DesiredState
		err = w.store.Read(ctx, project.ID, func(tx ports.ReadTx) error { return desiredIn(tx, record.ID, &part) })
		if err != nil {
			return ledger.DesiredState{}, err
		}
		name := string(project.ID)
		for _, lease := range part.Leases {
			lease.Project = name
			state.Leases = append(state.Leases, lease)
		}
		for _, archive := range part.Archives {
			archive.Project = name
			state.Archives = append(state.Archives, archive)
		}
		for _, effect := range part.Effects {
			effect.Project = name
			state.Effects = append(state.Effects, effect)
		}
		for _, woken := range part.Woken {
			woken.Project = name
			state.Woken = append(state.Woken, woken)
		}
		for _, request := range part.Forge {
			request.Project = name
			state.Forge = append(state.Forge, request)
		}
		for _, repository := range part.ForgeRepositories {
			repository.Project = name
			state.ForgeRepositories = append(state.ForgeRepositories, repository)
		}
		for _, watched := range part.PullRequests {
			watched.Project = name
			state.PullRequests = append(state.PullRequests, watched)
		}
		for _, assistant := range part.Assistants {
			if record.Protocol != 0 && record.Protocol < ledger.ProtocolAssistants {
				break
			}
			assistant.Project = name
			state.Assistants = append(state.Assistants, assistant)
		}
	}
	return state, nil
}

// Watch hints that a revision of a project this worker serves, or the installation's, moved. A hint
// carries no state: the worker reads desired state, and a hint it drops the next heartbeat makes up for.
func (p *Plane) Watch(ctx context.Context, request ledger.WatchRequest) (<-chan int64, error) {
	_, err := p.worker(ctx, request.Auth, nil)
	if err != nil {
		return nil, err
	}
	hints := make(chan int64, 1)
	go func() {
		defer close(hints)
		var last map[string]int64
		ticker := time.NewTicker(watchEvery)
		defer ticker.Stop()
		for {
			revisions, _, sum, err := p.revisions(ctx)
			moved := err == nil && ((last == nil && sum != request.After) || (last != nil && !maps.Equal(last, revisions)))
			if err == nil {
				last = revisions
			}
			if moved {
				select {
				case <-hints:
				default:
				}
				hints <- sum
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return hints, nil
}

// Upload stores a batch in order, each event in the project it names. What belongs to no lane is the
// worker's: its orchestrator's word and a probe's result go to the installation's log, and a word that
// its sign-in is back reaches every project it serves, because a lane of any of them may wait on it.
func (p *Plane) Upload(ctx context.Context, batch ledger.UploadBatch) (ledger.UploadReply, error) {
	record, err := p.worker(ctx, batch.Auth, &batch.Epoch)
	if err != nil {
		return ledger.UploadReply{}, err
	}
	var reply ledger.UploadReply
	for _, event := range batch.Events {
		rejected, err := p.storeEvent(ctx, record, event)
		if errors.Is(err, workorders.ErrLapsed) {
			return reply, nil
		}
		if err != nil {
			return reply, err
		}
		if rejected != nil {
			reply.Rejected = append(reply.Rejected, *rejected)
		}
		reply.HighWater = event.Seq
	}
	return reply, nil
}

func (p *Plane) storeEvent(ctx context.Context, record ledger.WorkerRecord, e ledger.UploadEvent) (*ledger.UploadResult, error) {
	worker := record.ID
	id := ledger.UploadedEventID(worker, e.Epoch, e.Seq)
	switch {
	case orchestratorReport(e):
		return uploadResult(e, storeOrchestratorEvent(ctx, p.installation, worker, id, e))
	case e.WorkOrder == "" && e.Lane == "" && e.Type == ledger.UploadDriverQualified:
		return uploadResult(e, storeQualification(ctx, p.installation, worker, id, e))
	case e.WorkOrder == "" && e.Lane == "" && e.Type == ledger.UploadWakeSignal:
		return p.storeEverywhere(ctx, record, e)
	}
	w, _, err := p.routeFor(ctx, e.Project, true)
	if err != nil {
		return uploadResult(e, err)
	}
	return w.storeEvent(ctx, worker, e)
}

// storeEverywhere stores one event in every project the worker serves, once in each.
func (p *Plane) storeEverywhere(ctx context.Context, record ledger.WorkerRecord, e ledger.UploadEvent) (*ledger.UploadResult, error) {
	projects, err := p.serving(ctx)
	if err != nil {
		return nil, err
	}
	var first *ledger.UploadResult
	for _, project := range projects {
		w, err := p.workers(ctx, project)
		if err != nil {
			return nil, err
		}
		rejected, err := w.storeEvent(ctx, record.ID, e)
		if err != nil {
			return nil, err
		}
		if first == nil {
			first = rejected
		}
	}
	return first, nil
}

// Claim takes a work order of the project the claim names. An archived project takes no claims.
func (p *Plane) Claim(ctx context.Context, auth ledger.WorkerAuth, request workorders.ClaimRequest) (workorders.Grant, error) {
	w, _, err := p.route(ctx, auth, request.Project, true)
	if err != nil {
		return workorders.Grant{}, err
	}
	return w.Claim(ctx, auth, request)
}

// Ask records a question for a lane this worker holds, in the lane's project.
func (p *Plane) Ask(ctx context.Context, ask questions.LaneAsk) (questions.AskResult, error) {
	w, _, err := p.route(ctx, ask.Auth, ask.Project, true)
	if err != nil {
		return questions.AskResult{}, err
	}
	return w.Ask(ctx, ask)
}

// LaneFacts reads what a handoff states, in the lane's project.
func (p *Plane) LaneFacts(ctx context.Context, request ledger.LaneFactsRequest) (ledger.LaneFacts, error) {
	w, _, err := p.route(ctx, request.Auth, request.Project, false)
	if err != nil {
		return ledger.LaneFacts{}, err
	}
	return w.LaneFacts(ctx, request)
}

// LaneContext reads what a project holds, for a lane of that project. An ID from another project does not
// resolve, because the read never leaves the project's ledger.
func (p *Plane) LaneContext(ctx context.Context, request ledger.LaneContextRequest) (ledger.LaneContext, error) {
	w, _, err := p.route(ctx, request.Auth, request.Project, false)
	if err != nil {
		return ledger.LaneContext{}, err
	}
	return w.LaneContext(ctx, request)
}

// UseApproval spends a decision on a call of a lane, in the lane's project.
func (p *Plane) UseApproval(ctx context.Context, use authority.ApprovalUse) (authority.ApprovalAnswer, error) {
	w, _, err := p.route(ctx, use.Auth, use.Project, true)
	if err != nil {
		return authority.ApprovalAnswer{}, err
	}
	return w.UseApproval(ctx, use)
}

// DecideApproval carries what this worker's orchestrator decided, in the project the request is in.
func (p *Plane) DecideApproval(ctx context.Context, decision authority.WorkerDecision) (authority.Approval, error) {
	w, _, err := p.route(ctx, decision.Auth, decision.Project, true)
	if err != nil {
		return authority.Approval{}, err
	}
	return w.DecideApproval(ctx, decision)
}

// Rule records what this worker's orchestrator decided, in the question's project.
func (p *Plane) Rule(ctx context.Context, ruling questions.WorkerRuling) (questions.Decision, error) {
	w, _, err := p.route(ctx, ruling.Auth, ruling.Project, true)
	if err != nil {
		return questions.Decision{}, err
	}
	return w.Rule(ctx, ruling)
}

// WakeDigest reads what this worker's orchestrator could judge now in every project it serves, each item
// labeled with its project.
func (p *Plane) WakeDigest(ctx context.Context, request ledger.WakeRequest) (ledger.WakeDigest, error) {
	_, err := p.worker(ctx, request.Auth, &request.Epoch)
	if err != nil {
		return ledger.WakeDigest{}, err
	}
	projects, err := p.serving(ctx)
	if err != nil {
		return ledger.WakeDigest{}, err
	}
	digest := ledger.WakeDigest{Items: []ledger.WakeItem{}}
	for _, project := range projects {
		w, err := p.workers(ctx, project)
		if err != nil {
			return ledger.WakeDigest{}, err
		}
		part, err := w.WakeDigest(ctx, request)
		if err != nil {
			return ledger.WakeDigest{}, err
		}
		for _, item := range part.Items {
			item.Project = string(project.ID)
			digest.Items = append(digest.Items, item)
		}
	}
	return digest, nil
}

// Orchestrate carries one act of this worker's orchestrator to the project the act names, through that
// project's own ledger, so a record of another project does not resolve. Its two lists name no project to
// read every one the worker serves, and each item they give names its project: the work it may claim, and
// the next question, which comes from the first project that has one free. A turn's end lets go of what
// it held in every project.
func (p *Plane) Orchestrate(ctx context.Context, verb string, call ledger.OrchestratorCall) (ledger.OrchestratorReply, error) {
	spans := verb == ledger.VerbCandidates || verb == ledger.VerbNext || verb == ledger.VerbLetGo
	if !spans || call.Project != "" {
		w, project, err := p.route(ctx, call.Auth, call.Project, verb != ledger.VerbLetGo && verb != ledger.VerbCandidates)
		if err != nil {
			return ledger.OrchestratorReply{}, err
		}
		reply, err := w.Orchestrate(ctx, verb, call)
		if err != nil || !spans {
			return reply, err
		}
		return p.labeled(verb, project, reply)
	}
	_, err := p.worker(ctx, call.Auth, &call.Epoch)
	if err != nil {
		return ledger.OrchestratorReply{}, err
	}
	projects, err := p.serving(ctx)
	if err != nil {
		return ledger.OrchestratorReply{}, err
	}
	candidates := []json.RawMessage{}
	for _, project := range projects {
		w, err := p.workers(ctx, project)
		if err != nil {
			return ledger.OrchestratorReply{}, err
		}
		reply, err := w.Orchestrate(ctx, verb, call)
		if err != nil {
			return ledger.OrchestratorReply{}, err
		}
		labeled, err := p.labeled(verb, project, reply)
		if err != nil {
			return ledger.OrchestratorReply{}, err
		}
		switch verb {
		case ledger.VerbNext:
			if len(reply.Result) != 0 && string(reply.Result) != "null" {
				return labeled, nil
			}
		case ledger.VerbCandidates:
			var items []json.RawMessage
			if err := json.Unmarshal(labeled.Result, &items); err != nil {
				return ledger.OrchestratorReply{}, err
			}
			candidates = append(candidates, items...)
		}
	}
	if verb != ledger.VerbCandidates {
		return ledger.OrchestratorReply{}, nil
	}
	raw, err := json.Marshal(candidates)
	return ledger.OrchestratorReply{Result: raw}, err
}

// labeled names the project of each item a list gave: each work order a worker may claim, or the question
// it took next, by the project's ID, which every act after it names, and its name, which a person reads.
func (p *Plane) labeled(verb string, project ledger.Project, reply ledger.OrchestratorReply) (ledger.OrchestratorReply, error) {
	if len(reply.Result) == 0 || string(reply.Result) == "null" {
		return reply, nil
	}
	label := func(raw json.RawMessage) (json.RawMessage, error) {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		id, _ := json.Marshal(string(project.ID))
		name, _ := json.Marshal(project.Name)
		item["project"], item["project_name"] = id, name
		return json.Marshal(item)
	}
	switch verb {
	case ledger.VerbCandidates:
		var items []json.RawMessage
		if err := json.Unmarshal(reply.Result, &items); err != nil {
			return ledger.OrchestratorReply{}, err
		}
		for i, item := range items {
			labeled, err := label(item)
			if err != nil {
				return ledger.OrchestratorReply{}, err
			}
			items[i] = labeled
		}
		raw, err := json.Marshal(items)
		return ledger.OrchestratorReply{Result: raw}, err
	case ledger.VerbNext:
		raw, err := label(reply.Result)
		return ledger.OrchestratorReply{Result: raw}, err
	}
	return reply, nil
}

// AssistantCredential makes a generation's credential in the project the run is in.
func (p *Plane) AssistantCredential(ctx context.Context, request ledger.AssistantCredentialRequest) (ledger.AssistantGrant, error) {
	w, project, err := p.route(ctx, request.Auth, request.Project, true)
	if err != nil {
		return ledger.AssistantGrant{}, err
	}
	grant, err := w.AssistantCredential(ctx, request)
	// A command names a project as the registry does.
	grant.Context.ProjectName = project.Name
	return grant, err
}

// AssistantEntry records a reply in the project the run is in.
func (p *Plane) AssistantEntry(ctx context.Context, request ledger.AssistantEntryRequest) error {
	w, _, err := p.route(ctx, request.Auth, request.Project, true)
	if err != nil {
		return err
	}
	return w.AssistantEntry(ctx, request)
}

// List returns every registered worker of the installation.
func (p *Plane) List(ctx context.Context) ([]ledger.Worker, error) {
	return listWorkers(ctx, p.installation)
}

// Orchestrator reads a worker's orchestrator.
func (p *Plane) Orchestrator(ctx context.Context, worker string) (ledger.Orchestrator, error) {
	return orchestratorOf(ctx, p.installation, worker)
}

// SetOrchestrator records what a member asks of a worker's orchestrator.
func (p *Plane) SetOrchestrator(ctx context.Context, worker, desired string, actor ledger.Actor) (ledger.Orchestrator, error) {
	return setOrchestrator(ctx, p.installation, worker, desired, actor)
}

// Projects lists the projects a worker serves, in the registry's order, for a surface that names them.
func (p *Plane) Projects(ctx context.Context) ([]ledger.Project, error) {
	return p.serving(ctx)
}

var _ ports.WorkerLink = (*Plane)(nil)
