// Package server holds the handlers of the versioned API. It composes the
// application's use cases and wires no adapter, so one handler serves every
// deployment mode.
package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/version"
	"ropeshift.com/ropeshift/internal/workorders"
)

// maxBody bounds one request. Every body is a small JSON document.
const maxBody = 1 << 20

// Config is what the handlers run on.
type Config struct {
	// Scope and Store are the project the plane protocol serves: the installation's first project, whose
	// worker this control plane runs. The installation's routes read the records every project shares
	// through it.
	Scope ledger.WorkspaceID
	Store ports.Store
	// Projects is the installation's registry, which every project-scoped route resolves its path
	// through. Nil serves Store as the one project, named default.
	Projects ports.Projects
	// Installation holds the records every project shares. Nil reaches them through Store.
	Installation ports.Installation
	// Matching makes the question ladder's matcher of a project's Store. Nil gives a project other than
	// the first no candidates.
	Matching func(ports.Store) ports.Matcher
	// LeaseTTL is the policy's lease in milliseconds, for a claim that names none.
	LeaseTTL int64
	// Protocol is the plane protocol this control plane speaks. Zero means this
	// build's version.
	Protocol int
	// HeartbeatMs is what a worker is told to beat at.
	HeartbeatMs int64
	// Knowledge is where documents are read from.
	Knowledge []ports.KnowledgeSource
	// Matcher produces candidates for the question ladder.
	Matcher ports.Matcher
	// Identity authenticates humans.
	Identity ports.IdentityProvider
	// Batches says how long questions gather for a person.
	Batches questions.BatchPolicy
	// Windows says how long a message stays useful, per kind.
	Windows messages.Windows
	// PackTokens is the budget of the pack a claim pins. Zero takes the default.
	PackTokens int64
	// LongParkedMs is how long a lane stays parked before it reads as stuck. Zero takes the default.
	LongParkedMs int64
	// Telemetry reads the local worker's telemetry store. Nil has none, and its reads answer empty.
	Telemetry ports.TelemetryReader
	// Backup copies the ledger of a control plane with no registry while it runs. Nil has none, as on
	// Postgres, which a team backs up with its own tooling. A registry backs up each of its projects.
	Backup ports.LedgerBackup
	// Starts places the lane of every claim: the base it begins from, fetched, and the wait for the merge
	// of the work it depends on. Nil places none, and a worker begins a branch where its checkout is.
	Starts *control.Starts
	// Branches opens a project's repository at a branch, in the checkout of the machine the daemon runs on,
	// so the outline of a planning session reads what its lane has committed. Nil reads none.
	Branches BranchSource
	// LaneBranchPrefix is what the name of a lane's branch begins with, which the lane's profile sets. The
	// branch a lane works on is the prefix and the lane's ID, unless its work order keeps one.
	LaneBranchPrefix string
	// Panes is the worker's terminal as the web pane reaches it. Nil reaches none, as on a control
	// plane whose worker has no tmux, and the pane's routes refuse.
	Panes Panes
	// WebAddress is the address the web listener bound, which health reports so a command can name a
	// page of the web UI. Empty means the daemon serves no web UI.
	WebAddress string
	// Mode is how the installation runs, as health reports it: api.ModeSolo or api.ModeGroup. Empty means solo.
	Mode string
	// Build is the build this daemon runs, which health reports. The zero value reads it from the running
	// binary.
	Build version.Info
	// Installed reads the build on disk at the daemon's own executable path, which health compares with
	// Build. It answers from what it last read and never reads the disk itself, because health is asked
	// for often. Nil reads none, and health warns of nothing.
	Installed func() version.Installed
	// Checkouts is the machine of the worker this daemon runs: the clones on it and the checkouts it keeps,
	// which registering repositories reads and records (ADR-0022). Nil has no worker's machine, and a
	// registration refuses.
	Checkouts ports.Checkouts
	// Layouts reads how the clones on that machine are laid out, for the plan of an adoption (FR-ADOPT-12). Nil
	// reads none, and the plan refuses.
	Layouts ports.Layouts
	// ParseManifest reads the text of a repository manifest (ADR-0022). Nil reads none, and a registration
	// from a manifest refuses.
	ParseManifest func([]byte) (planning.RepositoriesFile, error)
	// ModelClasses reads the model each driver's profile on the worker this daemon runs maps every
	// capability class to, by driver and then class (ADR-0025). Nil shows no rendered model.
	ModelClasses func(context.Context) (map[string]map[string]string, error)
	// AssistantHosts reads what each worker could run a project's assistant on: whether its driver offers a
	// headless session, and the repositories it holds a checkout of. Nil leaves no worker able to, and no
	// orchestrator serving a project (ADR-0024).
	AssistantHosts control.AssistantHosts
	// Profile is the user's layer on the machine of the worker this daemon runs, where its lane caps live
	// (FR-PROJ-7), and LocalWorker names that worker once it has registered. With either nil, the caps
	// routes refuse.
	Profile     ports.UserProfile
	LocalWorker func(context.Context) (string, error)
	// Profiles reads the profile in force for a clone on this machine, so a registration can say when the
	// worktree root would lie inside the clone (FR-PROF-3). Nil says nothing of it.
	Profiles ports.ProfileSource
	// Attempts limits the wrong guesses at a passphrase or a join token that each client address makes on the
	// network listener, which records the address of every request it serves. Nil limits none. A request on the
	// control socket records no address and is never limited: its file permissions are its credential.
	Attempts *Attempts
	// WebhookAttempts limits the deliveries each client address makes to the webhook route of CI events, apart from
	// Attempts, because a delivery is no guess at a passphrase: GitHub sends several at once from the same few
	// addresses, and does not retry one that is refused. Every refusal counts against the address, as a wrong guess
	// does, only past a much higher threshold (NewWebhookAttempts). Nil limits none.
	WebhookAttempts *Attempts
	// Templates are the project templates this daemon's binary embeds, rendered with the example data, which
	// the templates route serves. Nil has none, and the route refuses.
	Templates []api.Template
	// Reserved are the folders this daemon keeps for itself on its own machine: its state, run, and worktree folders, each
	// as spelled and as it resolves. A template write for the daemon's own worker is refused at or inside any of them.
	Reserved []string
	// Hashes lets one passphrase check run at a time, and no more than its depth wait for it, so a crowd of
	// guessers cannot hold it for as long as they like. The network listener makes one when none is given. Nil
	// checks without one, as the control socket does.
	Hashes *HashGate
	// WorkerURL is the URL a group server's workers reach it at, and CAFingerprint the SHA-256 of its own
	// certificate authority in lowercase hexadecimal, which is empty when it has none. A group server makes a join
	// string of them and the join token. A solo daemon has neither.
	WorkerURL     string
	CAFingerprint string
	// Warn hears what a refusal leaves out of its answer, which is what broke in the server and no business of the
	// client that was refused. Nil hears nothing.
	Warn func(string)
	// Env reads the server's environment variables, where the key each repository's CI webhook deliveries are
	// signed with is kept, under the name the repository's record gives (FR-SPEND-20). Nil has none, and every
	// delivery is refused.
	Env func(string) (string, bool)
}

type handler struct {
	Config
	workOrders *control.WorkOrders
	leases     *control.Leases
	reviews    *control.Reviews
	// planRevisions confirms a plan's revision and starts the session that revises it.
	planRevisions *control.PlanRevisions
	// ci is a member's word on CI that asks for them: waiting longer, and rerunning.
	ci        *control.CIAttention
	accepting *control.Acceptances
	questions *control.Questions
	workers   *control.Workers
	// plane serves the plane protocol across every project. The first project's handler serves its routes,
	// and every project's handler reads the workers and orchestrators every project shares through it.
	plane *control.Plane
	fleet *control.Fleet
	// ciWebhooks takes the signed webhook deliveries of CI events, which only the first project's handler holds.
	ciWebhooks   *control.CIWebhooks
	wakes        *control.Wakes
	messages     *control.Messages
	sessions     *control.Sessions
	approvals    *control.Approvals
	autonomy     *control.Autonomy
	effects      *control.Effects
	notices      *control.Notices
	repositories *control.Repositories
	adoptions    *control.Adoptions
	recoveries   *control.Recoveries
	spend        *control.Spend
	estimates    *control.EstimatePolicies
	delegates    *control.Delegates
	// conversations are the member's conversations with the assistant.
	conversations *control.Conversations
	// root is the whole API as the socket serves it, through which a confirmed card's call runs again.
	root http.Handler
	// lifecycle and router are the installation's, which only the first project's handler holds.
	lifecycle *control.Projects
	router    *router
	// caps reads and sets the lane caps of the worker this daemon runs, which only the first project's
	// handler holds.
	caps *control.LaneCaps
	// templateWrites records a member's requests that a worker write a shape's starting files, which only the
	// first project's handler holds.
	templateWrites *control.TemplateWrites
	// forgeAccountReads records a member's requests that a worker read its forge account, held where templateWrites is.
	forgeAccountReads *control.ForgeAccountReads
}

// New returns the versioned API. The installation's routes sit at the top, and each project's routes
// under /v1/projects/{project}/, on that project's ledger (ADR-0021).
func New(config Config) http.Handler {
	if config.Build.Version == "" {
		config.Build = version.Read("ropeshiftd")
	}
	if config.Projects == nil {
		config.Projects = oneProject{project: ledger.Project{
			ID: config.Scope, Name: ledger.DefaultProjectName, State: ledger.ProjectActive, Database: "ledger",
		}, store: config.Store, backup: config.Backup}
	}
	if config.Installation == nil {
		config.Installation = config.Store.Installation(config.Scope)
	}
	home := newHandler(config)
	home.plane = control.NewPlane(config.Projects, config.Installation, config.Scope, control.WorkerOptions{
		Protocol: config.Protocol, HeartbeatMs: config.HeartbeatMs, LeaseTTLMs: config.LeaseTTL, Batches: config.Batches, Windows: config.Windows,
		PackTokens: config.PackTokens, Starts: config.Starts,
	}, matching(config))
	home.ciWebhooks = control.NewCIWebhooks(home.plane, home.plane.CIHints(), config.Env, time.Now, config.Warn)
	projects := newRouter(config, home)
	home.lifecycle, home.router = control.NewProjects(config.Projects), projects
	home.caps = control.NewLaneCaps(config.Installation, config.Profile, config.LocalWorker)
	home.templateWrites = control.NewTemplateWrites(config.Installation, config.HeartbeatMs, config.LocalWorker).Reserving(config.Reserved...)
	home.forgeAccountReads = control.NewForgeAccountReads(config.Installation, config.HeartbeatMs)
	mux := http.NewServeMux()
	home.installationRoutes(mux)
	projects.registryRoutes(mux)
	mux.Handle(projectPrefix+"{project}/", projects)
	root := &gate{mux: mux, home: home, router: projects}
	home.root, projects.root = root, root
	return root
}

// installationRoutes are the routes of the installation's own records: members and their sessions,
// workers with their autonomy and orchestrators, the plane protocol, and the worker's telemetry.
func (h *handler) installationRoutes(mux *http.ServeMux) {
	h.lifecycleRoutes(mux)
	h.sessionRoutes(mux)
	h.workerRoutes(mux)
	h.orchestratorRoutes(mux)
	h.autonomyRoutes(mux)
	h.capsRoutes(mux)
	h.templateWriteRoutes(mux)
	h.telemetryRoutes(mux)
	h.installationRosterRoutes(mux)
	h.templateRoutes(mux)
	h.newAdoptionRoutes(mux)
	mux.HandleFunc("GET /v1/health", h.health)
	mux.HandleFunc("POST "+api.GitHubWebhookPath, h.githubWebhook)
	mux.HandleFunc("GET "+api.AuditPath, h.installationAudit)
	mux.HandleFunc("GET "+api.MembersPath, h.listMembers)
	mux.HandleFunc("POST "+api.InstallationBackupDownloadPath, h.member(h.downloadInstallationBackup))
}

// projectRoutes are one project's routes, as they read under /v1. The router serves them under
// /v1/projects/{project}/.
func (h *handler) projectRoutes(mux *http.ServeMux) {
	h.laneRoutes(mux)
	h.planningRoutes(mux)
	h.revisionRoutes(mux)
	h.ciRoutes(mux)
	h.questionRoutes(mux)
	h.messageRoutes(mux)
	h.approvalRoutes(mux)
	h.repositoryRoutes(mux)
	h.adoptionRoutes(mux)
	h.effectRoutes(mux)
	h.viewRoutes(mux)
	h.eventRoutes(mux)
	h.noticeRoutes(mux)
	h.measureRoutes(mux)
	h.spendRoutes(mux)
	h.paneRoutes(mux)
	h.projectRosterRoutes(mux)
	h.conversationRoutes(mux)
	mux.HandleFunc("POST /v1/backup", h.member(h.backup))
	mux.HandleFunc("POST "+api.BackupDownloadPath, h.member(h.downloadBackup))
	mux.HandleFunc("GET "+api.AuditPath, h.projectAudit)
	mux.HandleFunc("GET /v1/sources", h.sources)
	mux.HandleFunc("POST /v1/work-orders", h.member(h.createWorkOrder))
	mux.HandleFunc("GET /v1/work-orders", h.listWorkOrders)
	mux.HandleFunc("GET /v1/work-orders/{id}", h.showWorkOrder)
	mux.HandleFunc("POST /v1/work-orders/{id}/review", h.member(h.review))
	mux.HandleFunc("GET /v1/work-orders/{id}/acceptance", h.acceptance)
	mux.HandleFunc("POST /v1/work-orders/{id}/accept", h.member(h.accept))
	mux.HandleFunc("POST /v1/work-orders/{id}/rework", h.member(h.rework))
	mux.HandleFunc("POST /v1/work-orders/{id}/claims", h.claim)
	mux.HandleFunc("POST /v1/work-orders/{id}/claims/{claim}/release", h.member(h.release))
	mux.HandleFunc("POST /v1/work-orders/{id}/reassign", h.member(h.reassign))
	mux.HandleFunc("POST /v1/work-orders/{id}/cancel", h.member(h.cancel))
	mux.HandleFunc("POST /v1/work-orders/{id}/lanes", h.member(h.startLane))
	mux.HandleFunc("POST /v1/work-orders/{id}/writes", h.member(h.write))
}

// matching makes the question ladder's matcher of a project's Store: the registry's, or the one Matcher a
// control plane of one ledger was given.
func matching(config Config) func(ports.Store) ports.Matcher {
	if config.Matching != nil {
		return config.Matching
	}
	return func(store ports.Store) ports.Matcher {
		if store == config.Store {
			return config.Matcher
		}
		return nil
	}
}

func newHandler(config Config) *handler {
	practices := control.NewPractices(config.Store).Placing(config.AssistantHosts, config.HeartbeatMs, config.LocalWorker)
	repositories := control.NewRepositories(config.Store).Rooted(config.Profiles).Practicing(practices)
	return &handler{
		Config:        config,
		adoptions:     control.NewAdoptions(repositories),
		workOrders:    control.NewWorkOrders(config.Store),
		leases:        control.NewLeases(config.Store).Budgeted(config.PackTokens).Starting(config.Starts),
		reviews:       control.NewReviews(config.Store),
		planRevisions: control.NewPlanRevisions(config.Store),
		ci:            control.NewCIAttention(config.Store),
		recoveries:    control.NewRecoveries(config.Store),
		spend:         control.NewSpend(config.Store),
		estimates:     control.NewEstimatePolicies(config.Store),
		accepting:     control.NewAcceptances(config.Store),
		questions:     control.NewQuestions(config.Store, config.Batches).Telling(config.Windows).Budgeted(config.PackTokens),
		fleet:         control.NewFleet(config.Store),
		wakes:         control.NewWakes(config.Store),
		messages:      control.NewMessages(config.Store, config.Windows).Hosting(config.AssistantHosts),
		sessions:      control.NewSessions(config.Store, config.Identity),
		approvals:     control.NewApprovals(config.Store),
		autonomy:      control.NewAutonomy(config.Store),
		effects:       control.NewEffects(config.Store),
		repositories:  repositories,
		delegates:     control.NewDelegates(config.Store),
		conversations: control.NewConversations(config.Store, config.Windows).Hosting(config.AssistantHosts, config.HeartbeatMs),
		workers: control.NewWorkers(config.Store, config.Scope, control.WorkerOptions{
			Protocol: config.Protocol, HeartbeatMs: config.HeartbeatMs, LeaseTTLMs: config.LeaseTTL, Batches: config.Batches, Windows: config.Windows,
			PackTokens: config.PackTokens, Matcher: config.Matcher, Starts: config.Starts,
		}),
		notices: control.NewNotices(config.Store, func(authority.NotifyPolicy) []ports.Notifier { return nil }),
	}
}

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
		return false
	}
	return true
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func refuse(w http.ResponseWriter, status int, detail api.ErrorDetail) {
	reply(w, status, api.ErrorBody{Error: detail})
}

// fail turns an error from a use case into its response. Each domain error is a
// different instruction to the caller. What is left came from the store, and a
// store that cannot answer is 503, so a client retries instead of giving up.
func fail(w http.ResponseWriter, err error) {
	var fenced *workorders.FencedError
	var protocol *ledger.ProtocolError
	var overlap *questions.OverlapError
	var limited *ledger.SpendLimitError
	switch {
	case errors.As(err, &limited):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeSpendLimit, Message: err.Error()})
	case errors.As(err, &fenced):
		refuse(w, http.StatusConflict, api.ErrorDetail{
			Code: api.CodeFenced, Message: err.Error(), CurrentFence: fenced.Current, State: string(fenced.State),
		})
	case errors.As(err, &protocol):
		refuse(w, http.StatusUpgradeRequired, api.ErrorDetail{
			Code: api.CodeProtocol, Message: err.Error(), Worker: protocol.Worker, Oldest: protocol.Oldest, Newest: protocol.Newest,
		})
	case errors.Is(err, ledger.ErrBadCredential):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeBadCredential, Message: err.Error()})
	case errors.Is(err, ledger.ErrJoinTokenExpired):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeJoinTokenExpired, Message: err.Error()})
	case errors.Is(err, ledger.ErrJoinTokenRevoked):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeJoinTokenRevoked, Message: err.Error()})
	case errors.Is(err, ledger.ErrBadJoinToken):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeBadJoinToken, Message: err.Error()})
	case errors.Is(err, ledger.ErrJoinTokenUsed):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeJoinTokenUsed, Message: err.Error()})
	case errors.Is(err, authority.ErrNoSession):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeSessionRequired, Message: err.Error()})
	case errors.Is(err, authority.ErrBadProof):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeBadPassphrase, Message: err.Error()})
	case errors.Is(err, authority.ErrPassphraseChanged):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodePassphraseChanged, Message: err.Error()})
	case errors.Is(err, authority.ErrNotEnrolled):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeNotEnrolled, Message: err.Error()})
	case errors.Is(err, authority.ErrBadDelegate):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeBadDelegate, Message: err.Error()})
	case errors.Is(err, authority.ErrOutsideProject):
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeOutsideProject, Message: err.Error()})
	case errors.Is(err, authority.ErrMemberOnly):
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeMemberOnly, Message: err.Error()})
	case errors.Is(err, authority.ErrDelegateActive):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeDelegateActive, Message: err.Error()})
	case errors.Is(err, workorders.ErrAlreadyPlanned):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeAlreadyPlanned, Message: err.Error()})
	case errors.Is(err, ledger.ErrProjectExists):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeProjectExists, Message: err.Error()})
	case errors.Is(err, ledger.ErrProjectState):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeProjectState, Message: err.Error()})
	case errors.Is(err, ledger.ErrProjectArchived):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeProjectArchived, Message: err.Error()})
	case errors.Is(err, ledger.ErrRepositoryHeld):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeRepositoryHeld, Message: err.Error()})
	case errors.Is(err, ledger.ErrRepositoryState):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeRepositoryState, Message: err.Error()})
	case errors.Is(err, ledger.ErrStaleEpoch):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeStaleEpoch, Message: err.Error()})
	case errors.Is(err, ports.ErrNotFound):
		refuse(w, http.StatusNotFound, api.ErrorDetail{Code: api.CodeNotFound, Message: err.Error()})
	case errors.Is(err, conversations.ErrNoWorker):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeNoAssistantWorker, Message: err.Error()})
	case errors.Is(err, conversations.ErrNotRunning), errors.Is(err, messages.ErrNotRunning):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeTargetNotRunning, Message: err.Error()})
	case errors.Is(err, conversations.ErrInvalid):
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
	case errors.Is(err, workorders.ErrInvalid), errors.Is(err, questions.ErrInvalid), errors.Is(err, authority.ErrInvalid), errors.Is(err, authority.ErrBadLanePolicy), errors.Is(err, authority.ErrBadRecoveryPolicy), errors.Is(err, authority.ErrBadSpendPolicy), errors.Is(err, delivery.ErrBadEstimatePolicy), errors.Is(err, authority.ErrBadNotifyPolicy), errors.Is(err, messages.ErrInvalid), errors.Is(err, authority.ErrBadWindow), errors.Is(err, ledger.ErrInvalidRepository), errors.Is(err, ledger.ErrInvalidProject):
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
	case errors.Is(err, authority.ErrRiskNotAcknowledged):
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeRiskNotAcknowledged, Message: err.Error()})
	case errors.Is(err, authority.ErrStaleStatement):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeStaleStatement, Message: err.Error()})
	case errors.Is(err, authority.ErrWindowOpen):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeWindowOpen, Message: err.Error()})
	case errors.Is(err, questions.ErrReversibilityChanged):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeReversibilityChanged, Message: err.Error()})
	case errors.Is(err, questions.ErrResolved):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeResolved, Message: err.Error()})
	case errors.Is(err, roster.ErrInvalid), errors.Is(err, roster.ErrUnknownAgent), errors.Is(err, roster.ErrDisabledAgent):
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
	case errors.Is(err, roster.ErrExists), errors.Is(err, roster.ErrNothingToReset), errors.Is(err, roster.ErrInUse):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeRosterConflict, Message: err.Error()})
	case errors.As(err, &overlap):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeOverlap, Message: err.Error(), Decisions: overlap.Overlapping})
	case errors.Is(err, questions.ErrOneWay):
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeOneWay, Message: err.Error()})
	case errors.Is(err, questions.ErrNotHeld), errors.Is(err, questions.ErrTaken):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeNotHeld, Message: err.Error()})
	case errors.Is(err, questions.ErrWrongRung):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeWrongRung, Message: err.Error()})
	case errors.Is(err, questions.ErrAssumptionLimit):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeAssumptionLimit, Message: err.Error()})
	case errors.Is(err, questions.ErrDecisionEnded):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeDecisionEnded, Message: err.Error()})
	case errors.Is(err, questions.ErrDecisionClash):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeDecisionClash, Message: err.Error()})
	case errors.Is(err, authority.ErrSelfApproval):
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeSelfApproval, Message: err.Error()})
	case errors.Is(err, authority.ErrDeciderKind):
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeNotADecider, Message: err.Error()})
	case errors.Is(err, authority.ErrNotDelegated):
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeNotDelegated, Message: err.Error()})
	case errors.Is(err, authority.ErrStaleDigest):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeStaleDigest, Message: err.Error()})
	case errors.Is(err, authority.ErrApprovalClosed):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeRequestClosed, Message: err.Error()})
	case errors.Is(err, ledger.ErrEffectOutcome):
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
	case errors.Is(err, ledger.ErrEffectDecided):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeEffectDecided, Message: err.Error()})
	case errors.Is(err, ledger.ErrEffectCannotRepeat):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeCannotRepeat, Message: err.Error()})
	case errors.Is(err, authority.ErrBadApproval):
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
	case errors.Is(err, workorders.ErrForbidden):
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeForbidden, Message: err.Error()})
	case errors.Is(err, workorders.ErrNotAcceptable):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeNotAcceptable, Message: err.Error()})
	case errors.Is(err, workorders.ErrLost):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeLost, Message: err.Error()})
	case errors.Is(err, workorders.ErrIneligible):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeIneligible, Message: err.Error()})
	case errors.Is(err, workorders.ErrLapsed):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeLapsed, Message: err.Error()})
	case errors.Is(err, workorders.ErrNotLapsed):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeNotLapsed, Message: err.Error()})
	case errors.Is(err, workorders.ErrUnpushedWork):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeUnpushedWork, Message: err.Error()})
	case errors.Is(err, authority.ErrRecoveryMoved):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeMoved, Message: err.Error()})
	case errors.Is(err, ports.ErrRepositoryReadsUnavailable):
		refuse(w, http.StatusNotImplemented, api.ErrorDetail{Code: api.CodeRepositoryReadsUnavailable, Message: err.Error()})
	case errors.Is(err, ports.ErrPaneRelayUnavailable):
		refuse(w, http.StatusNotImplemented, api.ErrorDetail{Code: api.CodePaneRelayUnavailable, Message: err.Error()})
	case errors.Is(err, workorders.ErrIllegalMove), errors.Is(err, workorders.ErrLeasedEdge):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeIllegalMove, Message: err.Error()})
	default:
		refuse(w, http.StatusServiceUnavailable, api.ErrorDetail{Code: api.CodeUnavailable, Message: err.Error()})
	}
}

func invalid(w http.ResponseWriter, format string, args ...any) {
	refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: fmt.Sprintf(format, args...)})
}

// health reads the default project's revision, which the routes with no project serve. An installation
// that removed that project still answers, at revision zero. It reports the build the daemon runs, and
// warns when the build installed at the daemon's path is newer.
func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	health := api.Health{Status: "ok", Mode: cmp.Or(h.Mode, api.ModeSolo), Web: h.WebAddress, Version: h.Build.Version, Commit: h.Build.Commit, Date: h.Build.Date}
	if h.Installed != nil {
		if installed := h.Installed(); version.IsRelease(installed.Version) {
			health.Installed = installed.Version
			health.Warning = version.Warning(h.Build.Version, installed)
		}
	}
	if _, err := h.self(r.Context()); errors.Is(err, ports.ErrNotFound) {
		reply(w, http.StatusOK, health)
		return
	}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		workspace, err := tx.Workspace()
		health.Revision = workspace.Rev
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, health)
}

func (h *handler) sources(w http.ResponseWriter, r *http.Request) {
	list := api.SourceList{Sources: []planning.SourceBinding{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		bindings, err := tx.Planning().Bindings()
		if err == nil && bindings != nil {
			list.Sources = bindings
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

func (h *handler) createWorkOrder(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.CreateWorkOrder
	if !decode(w, r, &in) {
		return
	}
	kind := in.Kind
	if kind == "" {
		kind = workorders.KindChange
	}
	record, err := h.workOrders.Record(r.Context(), h.Scope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: in.ID, Kind: kind, Title: in.Title, RepositoryID: in.RepositoryID, Priority: in.Priority,
			Size: in.Size, Requirements: in.Requirements, Needs: in.Needs, Adopt: in.Adopt, Restructure: in.Restructure, Agent: in.Agent,
		},
		Statement: in.Statement,
		Criteria:  in.Criteria,
		DependsOn: in.DependsOn,
	}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

func (h *handler) listWorkOrders(w http.ResponseWriter, r *http.Request) {
	state := workorders.State(r.URL.Query().Get("state"))
	list := api.WorkOrderList{WorkOrders: []workorders.WorkOrder{}}
	if worker := r.URL.Query().Get("offered_to"); worker != "" {
		offered, err := h.workers.Offered(r.Context(), worker)
		if err != nil {
			fail(w, err)
			return
		}
		list.WorkOrders = offered
		reply(w, http.StatusOK, list)
		return
	}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if state == "" || row.State == state {
				list.WorkOrders = append(list.WorkOrders, row)
			}
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

func (h *handler) showWorkOrder(w http.ResponseWriter, r *http.Request) {
	var row workorders.WorkOrder
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.WorkOrders().Get(r.PathValue("id"))
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, row)
}

func (h *handler) review(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	result, err := h.reviews.Pass(r.Context(), h.Scope, r.PathValue("id"), actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.ReviewResult{State: result.State, Failures: result.Failures})
}

// acceptance reads where a work order stands, with what stays linked to it.
func (h *handler) acceptance(w http.ResponseWriter, r *http.Request) {
	view, err := h.accepting.Standing(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

func (h *handler) accept(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	state, err := h.accepting.Accept(r.Context(), h.Scope, r.PathValue("id"), actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.Accepted{State: state})
}

func (h *handler) rework(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.ReworkRequest
	if !decode(w, r, &in) {
		return
	}
	state, err := h.accepting.Rework(r.Context(), h.Scope, r.PathValue("id"), in.Reason, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.Accepted{State: state})
}

// claim serves both claimants: a worker names itself with its credential, and a
// member with a session chooses the worker (FR-WO-9).
func (h *handler) claim(w http.ResponseWriter, r *http.Request) {
	var in api.ClaimRequest
	if !decode(w, r, &in) {
		return
	}
	if in.ClaimID == "" || in.WorkerID == "" {
		invalid(w, "a claim needs a claim_id and a worker_id")
		return
	}
	ttl := in.TTLMs
	if ttl == 0 {
		ttl = h.LeaseTTL
	}
	request := workorders.ClaimRequest{WorkOrder: r.PathValue("id"), ClaimID: in.ClaimID, Worker: in.WorkerID, TTLMs: ttl}
	var grant workorders.Grant
	var err error
	if credential := bearer(r); credential != "" {
		grant, err = h.workers.Claim(r.Context(), ledger.WorkerAuth{WorkerID: in.WorkerID, Credential: credential}, request)
	} else {
		actor, _, ok := h.admit(w, r)
		if !ok {
			return
		}
		grant, err = h.leases.Claim(r.Context(), h.Scope, request, actor)
	}
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, grantBody(grant))
}

// workerClaim is a worker's claim on the plane protocol, which names the work in its body. The worker's
// credential says who claims, so a member's session counts for nothing here.
func (h *handler) workerClaim(w http.ResponseWriter, r *http.Request) {
	var in api.ClaimRequest
	if !decode(w, r, &in) {
		return
	}
	if in.ClaimID == "" || in.WorkOrder == "" {
		invalid(w, "a claim needs a claim_id and a work_order")
		return
	}
	worker := r.PathValue("id")
	if in.WorkerID != "" && in.WorkerID != worker {
		invalid(w, "the claim names worker %s on worker %s's route", in.WorkerID, worker)
		return
	}
	ttl := in.TTLMs
	if ttl == 0 {
		ttl = h.LeaseTTL
	}
	request := workorders.ClaimRequest{WorkOrder: in.WorkOrder, ClaimID: in.ClaimID, Worker: worker, TTLMs: ttl, Project: in.Project}
	grant, err := h.plane.Claim(r.Context(), ledger.WorkerAuth{WorkerID: worker, Credential: bearer(r)}, request)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, grantBody(grant))
}

func grantBody(grant workorders.Grant) api.Grant {
	body := api.Grant{
		WorkOrder: grant.WorkOrder, ClaimID: grant.ClaimID, Fence: grant.Fence, TTLMs: grant.TTLMs,
		ResumeLaneID: grant.ResumeLaneID, Branch: grant.Branch, Replayed: grant.Replayed,
	}
	if grant.ContractVersion != 0 {
		body.Contract = &api.ContractPin{ID: grant.ContractID, Version: grant.ContractVersion, Digest: grant.ContractDigest}
	}
	return body
}

func (h *handler) release(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.ReleaseRequest
	if !decode(w, r, &in) {
		return
	}
	lease := workorders.Lease{WorkOrder: r.PathValue("id"), ClaimID: r.PathValue("claim"), Fence: in.Fence}
	if err := h.leases.Release(r.Context(), h.Scope, lease, actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) reassign(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.ReassignRequest
	if !decode(w, r, &in) {
		return
	}
	if err := h.leases.Reassign(r.Context(), h.Scope, r.PathValue("id"), in.Reason, actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cancel ends work for good. The answer comes before any lane has stopped: a lane with a process hears
// from desired state, and reads as last reported until its worker says it ended.
func (h *handler) cancel(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.CancelWork
	if !decode(w, r, &in) {
		return
	}
	if err := h.leases.Cancel(r.Context(), h.Scope, r.PathValue("id"), in.Reason, actor); err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.Accepted{State: workorders.Canceled})
}

// startLane records a member's request to run a lane (FR-WO-9). The worker holding the
// claim reads it as desired state, so this answers before anything has started.
func (h *handler) startLane(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.StartLane
	if !decode(w, r, &in) {
		return
	}
	if in.LaneID == "" {
		invalid(w, "a lane needs a lane_id")
		return
	}
	if err := checkOverrides(in.Overrides); err != nil {
		invalid(w, "%s", err)
		return
	}
	lease, err := h.leases.DispatchWith(r.Context(), h.Scope, r.PathValue("id"), in.LaneID, in.Overrides, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.LaneDispatched{LaneID: in.LaneID, WorkOrder: lease.WorkOrder, ClaimID: lease.ClaimID, Fence: lease.Fence})
}

func (h *handler) write(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.WriteRequest
	if !decode(w, r, &in) {
		return
	}
	if in.WriteID == "" || in.ClaimID == "" {
		invalid(w, "a write needs a write_id and a claim_id")
		return
	}
	holder := workorders.Holder{
		Lease: workorders.Lease{WorkOrder: r.PathValue("id"), ClaimID: in.ClaimID, Fence: in.Fence},
		Lane:  in.Lane, Generation: in.Generation,
	}
	event, err := h.leases.Write(r.Context(), h.Scope, holder, in.WriteID, in.Payload, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.WriteResponse{ID: event.ID, Rev: event.Rev})
}
