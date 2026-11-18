import { call, project, request } from "./client";
import type { LaneTelemetryList, MessageList, NoticeList, Orchestrator, Plan, Project, SourceBinding, Standing, WorkOrder, WorkTimeline } from "./types";

// The installation's and one project's records a member manages: projects, repositories, settings, the
// agent roster, members, and the audit. Every call is a route the web listener serves, which names the
// CLI command that does the same.

export type MergeMethod = "merge" | "squash" | "rebase";
export type MergeAuthority = "person_merges" | "person_approves" | "review_approves";
export type ReviewTarget = "branch" | "pull_request";
export type StartFrom = "merged" | "accepted";

export type Repository = {
  id: string;
  name?: string;
  remote?: string;
  default_branch?: string;
  base_branch?: string;
  forge_binding?: string;
  merge_method: MergeMethod;
  merge_authority?: MergeAuthority;
  review_target: ReviewTarget;
  start_from: StartFrom;
  state?: string;
  planning?: boolean;
};

export type Rate = { numerator: number; denominator: number; value: number | null };
export type AgreementPeriod = { decisions: number; agreed: number; disagreed: number; unknown: number; agreement_rate: Rate };

export type ForgeHealth = { worker: string; worker_name?: string; repository: string; state: string; detail?: string; checked_at: number };

export type RepositoryView = Repository & {
  base?: string;
  review_agreement: { last_30_days: AgreementPeriod; all_time: AgreementPeriod };
  plan_of_record?: boolean;
  forge?: ForgeHealth[];
  // Every `[ci]` key with the value in force, so the page repeats no default.
  ci_effective?: Record<string, string | number | boolean>;
};

export type RepositoryList = { repositories: RepositoryView[] };

export type RepositoryChange = Partial<{
  name: string;
  base: string;
  merge_method: MergeMethod;
  start_from: StartFrom;
  review_target: ReviewTarget;
  forge_binding: string;
  merge_authority: MergeAuthority;
  planning: boolean;
  // The `[ci]` keys to change, each by its name and its text.
  ci: Record<string, string>;
}>;

export type RepositoryAdded = { path: string; entered?: string; outcome: "registered" | "known" | "refused"; repository?: Repository; checkout?: string; reason?: string };
export type RepositoryAddReport = { dry_run: boolean; repositories: RepositoryAdded[] };

// The plan of an adoption, as `ropeshift adopt --dry-run` reports it (FR-ADOPT-12). A list the layout has none of may
// come as null.
export type LayoutShape = "single" | "planned" | "consolidate" | "workspace_files";
export type LayoutClone = { name: string; path: string; identity: string; where: "here" | "ignored" | "path"; plan?: boolean; documents: number; instructions?: boolean };
export type PlannedRepository = { name?: string; path: string; id?: string; outcome: "registered" | "known" | "refused" | "not_registered"; reason?: string };
// lines are the lines an append adds to the file, each only when the file holds none like it. template names the
// template a file the layout lacks is written from.
export type PlannedFile = { path: string; action: "add" | "append"; detail?: string; lines?: string[]; template?: string };
export type PlannedCodeTree = { path: string; build_file: string; whole?: boolean; left_out?: boolean };
export type PlannedMove = { name: string; from: string; to: string; commands: string[] };
// A step is what the person does in a code repository that holds no instruction file, because adoption writes to no clone
// but the planning repository's.
export type PlannedStep = { name: string; path: string; commands: string[] };
export type AdoptionPlan = {
  layout: { root: string; folders: string[] | null; clones: LayoutClone[] };
  shape: LayoutShape;
  planning: { name: string; path?: string; why: string; undecided?: boolean };
  design?: { name: string; path: string; root: string; why: string };
  repositories: PlannedRepository[];
  files: PlannedFile[] | null;
  takes: string[] | null;
  collisions: { path: string; kept: "workspace" | "union" }[] | null;
  code_trees: PlannedCodeTree[] | null;
  moves: PlannedMove[] | null;
  steps?: PlannedStep[] | null;
  notes?: string[];
  digest: string;
  // parts are the digest of each part of the plan, by part name, which the route answers beside the plan. They are no
  // part of the plan, and its digest does not hold them: a restructuring sends them back so a worker that reads another
  // layout names the parts that differ.
  parts?: Record<string, string>;
};

// AdoptionPlanRequest asks for the plan of adopting the clone at root, which is the clone `ropeshift adopt` runs in,
// with the clones the paths name beside it.
export type AdoptionPlanRequest = { root: string; paths?: string[]; depth?: number; planning?: string; design?: string; leave_out?: string[]; new_project?: boolean };

// The restructuring a member chose from a plan, as an adopt work order keeps it: the shape and the plan's digest, for a
// consolidation the design repository and the trees to leave out, and the request the plan was read with (root, paths,
// depth, and the names planning and design) with the digest of each of the plan's parts, so a worker reads the layout
// again the way the plan did and names what differs.
export type Restructure = {
  shape: LayoutShape;
  digest: string;
  design_path?: string;
  design_root?: string;
  leave_out?: string[];
  root?: string;
  paths?: string[];
  depth?: number;
  planning?: string;
  design?: string;
  parts?: Record<string, string>;
};

export type Fleet = { fleet_mode: "run" | "pause" | "drain"; wip_limit: number; lanes_per_repository: number; lanes_per_worker: number };

export type PolicyVersion = { id: string; kind: string; version: number; digest: string; body: string; settings: string; authored_by: string; created_at: number };
export type PolicyList = { versions: PolicyVersion[] };
// A published version, and a notice of what is still to follow from it, such as an estimate policy that stops
// sharing history while the project's records are still in the installation's pool.
export type PublishedPolicy = PolicyVersion & { notice?: string };

// Where a project's spend stands (ADR-0037), as `ropeshift spend show` reads it: the charged spend of the day, the week,
// and each open work order against its limits. Every amount is US dollars at list price, and a limit of 0 is none.
export type Charged = { recorded_usd: number; estimated_usd: number; estimated_turns: number; unpriced?: number; charged_usd: number };
export type WorkOrderSpend = {
  work_order: string;
  kind: string;
  charged: Charged;
  label: string;
  soft_limit: number;
  hard_limit: number;
  raised_to?: number;
  basis?: string;
  remaining: number;
  conditions?: string[];
  policy_version: number;
};
export type ProjectWindowSpend = {
  window: "day" | "week";
  spent_usd: number;
  soft_limit: number;
  hard_limit: number;
  raised_to?: number;
  remaining: number;
  starts_at: number;
  ends_at: number;
};
export type SpendView = {
  project: { version: number; authored_by: string; created_at: number; keys: { key: string; value: string }[] };
  installation: { version: number; authored_by: string; created_at: number; keys: { key: string; value: string }[] };
  work_orders: WorkOrderSpend[] | null;
  project_spend: { day: ProjectWindowSpend; week: ProjectWindowSpend };
};

export type Tools = { allow: string[]; deny: string[] };

export type AgentFields = {
  name: string;
  purpose: string;
  kind: string;
  access: string;
  tools: Tools;
  class: string;
  models?: Record<string, string>;
  effort: string;
  approval: string;
  instructions: string;
  report: string;
  enabled: boolean;
};

export type RosterAgent = AgentFields & { sources: Record<string, string> };

export type RosterView = {
  scope: "installation" | "project";
  installation_version: number;
  project_version: number;
  roster: { agents?: RosterAgent[]; guide: string; guide_source: string; defaults: Record<string, unknown>; digest: string };
  models?: Record<string, Record<string, string>>;
};

export type RosterVersion = { id: string; scope: string; version: number; digest: string; summary: string; authored_by: string; prepared_by?: string; created_at: number };
export type RosterEntry = RosterVersion & { difference: string[] };
export type RosterHistory = { scope: string; versions: RosterEntry[] };

export type RosterChange =
  | { op: "set"; agent: string; fields: Record<string, string> }
  | { op: "add"; agent: string; from: string }
  // definition_from says where the definition was read, which the roster's history names.
  | { op: "add"; agent: string; definition: AgentFields; definition_from: "file" | "pasted" }
  | { op: "disable" | "enable" | "reset"; agent: string }
  | { op: "guide_set"; guide: string }
  | { op: "guide_reset" };

export type MemberSession = { created_at: number; expires_at: number };
export type MemberView = { id: string; sessions: MemberSession[]; last_sign_in?: number };
export type MemberList = { as_of: number; enrolled: number; members: MemberView[] };

export type AuditEvent = {
  rev: number;
  id: string;
  type: string;
  subject_kind: string;
  subject_id: string;
  actor_kind: string;
  actor_id: string;
  // A delegate's event names the member it acted for and its credential's label.
  actor_for?: string;
  actor_label?: string;
  delegate_kind?: string;
  occurred_at?: number;
  recorded_at: number;
  data?: unknown;
};

export type AuditPage = { scope: "installation" | "project"; as_of: number; head: number; next: number; scanned?: boolean; events: AuditEvent[] };
export type AuditQuery = { actor?: string; kind?: string; subject?: string; before?: number; limit?: number };

// A project template as the daemon's binary renders it with the example data: the shapes of `ropeshift new` that
// write it, the path it has in a new folder, and the text `ropeshift template show` prints for it.
export type Template = { name: string; shapes: string[]; writes_to: string; content: string };
export type TemplateList = { version: string; templates: Template[] };

export type WorkerRecord = { id: string; name?: string; epoch?: number; protocol?: number; manifest?: { capacity?: { max_lanes?: number } }; last_handshake_at?: number };
export type WorkerList = { workers: WorkerRecord[] };

// The oldest plane protocol a worker is offered a template write on. A worker on an older one, which the daemon still
// accepts, drops the request, so the dialog lists it as disabled and the daemon refuses a request for it.
export const protocolTemplateWrites = 5;

// A request that a worker write a shape's starting files into a folder on its machine, as `ropeshift new` writes them
// (FR-TPL-5): the shape, the absolute folder, the code repositories of a workspace, the project a repository's
// commands name, and whether the worker makes the folder's first commit.
export type TemplateShape = "workspace" | "monorepo" | "repository";
export type TemplateRepository = { name: string; remote: string };
export type TemplateWriteRequest = { shape: TemplateShape; path: string; repositories?: TemplateRepository[]; project?: string; first_commit?: boolean };

// What the worker did with a request. A refusal has its reason and detail; a write has the files it made, the first
// commit or why it failed, whether the folder is now a clone, and the commands that register it.
export type TemplateWriteReport = {
  request: string;
  outcome: "written" | "refused";
  reason?: string;
  detail?: string;
  project?: string;
  files?: string[];
  commit?: string;
  commit_error?: string;
  clone?: boolean;
  commands?: string[];
};

// A request as the daemon reads it: offered to its worker for two minutes, then written, refused, or expired. A
// folder is registrable from the web UI when its report says it is a clone and the worker is the daemon's own.
export type TemplateWriteView = {
  id: string;
  worker: string;
  requested_by: string;
  requested_at: number;
  expires_at: number;
  shape: TemplateShape;
  path: string;
  project?: string;
  repositories?: TemplateRepository[];
  first_commit?: boolean;
  state: "offered" | "written" | "refused" | "expired";
  report?: TemplateWriteReport;
  reported_at?: number;
  registrable: boolean;
};

// A worker's lane caps (FR-PROJ-7): max_lanes is null while it follows the worker's recommendation, and 0 caps
// nothing. The recommendation is the worker's last report, with the numbers behind it and why, and the warning
// says a cap above it stands all the same.
export type LaneRecommendation = {
  max_lanes: number;
  basis: string;
  observed_lanes: number;
  cores: number;
  memory_total_bytes?: number;
  reserve_cpu: number;
  reserve_memory_bytes: number;
  lane_cpu: number;
  lane_memory_bytes: number;
  by_cpu: number;
  by_memory?: number;
  reason: string;
};
export type CapWarning = { recommended: number; reason: string; message: string };
export type WorkerCaps = {
  worker: string;
  max_lanes: number | null;
  max_lanes_per_sign_in: number;
  effective_max_lanes: number;
  recommendation?: LaneRecommendation;
  reported_at?: number;
  warning?: CapWarning;
};
export type LaneCapSettings = { max_lanes: number | null; max_lanes_per_sign_in: number };

export type Level = "manual" | "attended" | "unattended";
export type RunWindow = { id: string; worker_id: string; opened_by: string; opened_at: number; closes_at: number };
export type Autonomy = {
  as_of: number;
  worker_id: string;
  sign_in: string;
  level: Level;
  chosen_level?: Level;
  risk_acknowledged: boolean;
  window?: RunWindow;
  may_claim: boolean;
  max_lanes?: number;
  max_respawns_per_day?: number;
};
export type Risk = { statement: string; digest: string };

// Health is what the daemon says of itself: the build it runs, the release installed at its own path when it could
// read it, and a warning when that release is later than the one running.
export type Health = {
  status: string;
  revision: number;
  // How the installation runs: "solo" for one person's, with its worker in the daemon, or "group".
  mode: string;
  web?: string;
  version: string;
  commit?: string;
  date?: string;
  installed?: string;
  warning?: string;
};

export type Approval = { id: string; state: string; kind?: string };

// Scope names whose layer of the roster a call reads or changes. It is never inferred: the page states it.
export type Scope = "installation" | "project";

const installation = (path: string) => `/v1${path}`;
const scoped = (scope: Scope, path: string) => (scope === "installation" ? installation(path) : project(path));
const query = (values: Record<string, string | number | undefined>) => {
  const search = new URLSearchParams();
  for (const [name, value] of Object.entries(values)) {
    if (value !== undefined && value !== "" && value !== 0) {
      search.set(name, String(value));
    }
  }
  const text = search.toString();
  return text ? `?${text}` : "";
};

export const adminPaths = {
  createProject: () => "/v1/projects",
  projectStep: (name: string, step: "rename" | "archive" | "restore" | "remove") => `/v1/projects/${encodeURIComponent(name)}/${step}`,
  importUpload: () => "/v1/projects/import/upload",
  backupDownload: (name: string) => `/v1/projects/${encodeURIComponent(name)}/backup/download`,
  installationBackupDownload: () => "/v1/installation/backup/download",
  repositories: () => project("/repositories"),
  repository: (id: string) => project(`/repositories/${encodeURIComponent(id)}`),
  removeRepository: (id: string) => project(`/repositories/${encodeURIComponent(id)}/remove`),
  adoptionPlan: (scope: Scope) => scoped(scope, "/adoption/plan"),
  fleet: () => project("/fleet"),
  fleetMode: () => project("/fleet/mode"),
  fleetCaps: () => project("/fleet/caps"),
  policies: (kind: string) => project(`/policies${query({ kind })}`),
  publishPolicy: () => project("/policies"),
  spend: () => project("/spend"),
  categories: () => project("/categories"),
  roster: (scope: Scope) => scoped(scope, "/roster"),
  rosterHistory: (scope: Scope, agent = "") => scoped(scope, `/roster/history${query({ scope, agent })}`),
  rosterChange: (scope: Scope) => scoped(scope, "/roster/changes"),
  rosterRevert: (scope: Scope) => scoped(scope, "/roster/revert"),
  members: () => "/v1/members",
  audit: (scope: Scope, q: AuditQuery) => scoped(scope, `/audit${query({ actor: q.actor, kind: q.kind, subject: q.subject, before: q.before, limit: q.limit })}`),
  templates: () => "/v1/templates",
  workers: () => "/v1/workers",
  joinToken: () => "/v1/workers/tokens",
  joinTokenRevoke: (id: string) => `/v1/workers/tokens/${encodeURIComponent(id)}/revoke`,
  autonomy: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/autonomy`,
  caps: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/caps`,
  templateWrites: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/template-writes`,
  templateWrite: (worker: string, request: string) => `/v1/workers/${encodeURIComponent(worker)}/template-writes/${encodeURIComponent(request)}`,
  acknowledge: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/autonomy/acknowledgement`,
  windows: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/windows`,
  risk: () => "/v1/autonomy/risk",
  health: () => "/v1/health",
  orchestrator: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/orchestrator`,
  approvals: (state: string) => project(`/approvals${query({ state })}`),
  workOrders: () => project("/work-orders"),
  plans: () => project("/plans"),
  reviewPlan: (plan: string) => project(`/plans/${encodeURIComponent(plan)}/reviews`),
  sources: () => project("/sources"),
  claim: (id: string) => project(`/work-orders/${encodeURIComponent(id)}/claims`),
  startLane: (id: string) => project(`/work-orders/${encodeURIComponent(id)}/lanes`),
  release: (id: string, claim: string) => project(`/work-orders/${encodeURIComponent(id)}/claims/${encodeURIComponent(claim)}/release`),
  archiveLane: (id: string) => project(`/work-orders/${encodeURIComponent(id)}/lane-archive`),
  review: (id: string) => project(`/work-orders/${encodeURIComponent(id)}/review`),
  acceptance: (id: string) => project(`/work-orders/${encodeURIComponent(id)}/acceptance`),
  timeline: (id: string) => project(`/work-orders/${encodeURIComponent(id)}/timeline`),
  notices: () => project("/notices"),
  laneMessages: (lane: string) => project(`/lanes/${encodeURIComponent(lane)}/messages`),
  laneTelemetry: () => "/v1/telemetry/lanes",
};

// Projects

export const createProject = (name: string, displayName: string) => call<Project>("POST", adminPaths.createProject(), { name, display_name: displayName || undefined });
// renameProject gives a project a new name, and the display name it is given or else the one it has.
export const renameProject = (name: string, next: string, displayName = "") =>
  call<Project>("POST", adminPaths.projectStep(name, "rename"), { name: next, display_name: displayName || undefined });
export const archiveProject = (name: string) => call<Project>("POST", adminPaths.projectStep(name, "archive"), {});
export const restoreProject = (name: string) => call<Project>("POST", adminPaths.projectStep(name, "restore"), {});
// A removal confirms with the project's name. The page downloads a backup first when a member asks for one,
// so the daemon writes none of its own.
export const removeProject = (name: string, confirm: string) => call<void>("POST", adminPaths.projectStep(name, "remove"), { confirm, no_backup: true });
export const importUpload = (backup: string, name: string) => call<Project>("POST", adminPaths.importUpload(), { backup, name: name || undefined });
export const downloadBackup = (name: string) => request("POST", adminPaths.backupDownload(name), {});
export const downloadInstallationBackup = () => request("POST", adminPaths.installationBackupDownload(), {});

// Repositories

export const readRepositories = () => call<RepositoryList>("GET", adminPaths.repositories());
export const changeRepository = (id: string, change: RepositoryChange) => call<RepositoryView>("PATCH", adminPaths.repository(id), change);
export const addRepositories = (paths: string[], depth: number, dryRun: boolean) => call<RepositoryAddReport>("POST", adminPaths.repositories(), { paths, depth, dry_run: dryRun });
export const removeRepository = (id: string) => call<RepositoryView>("POST", adminPaths.removeRepository(id), {});

// Fleet and policies

export const readFleet = () => call<Fleet>("GET", adminPaths.fleet());
export const setFleetMode = (mode: Fleet["fleet_mode"]) => call<Fleet>("PUT", adminPaths.fleetMode(), { fleet_mode: mode });
export const setFleetCaps = (caps: { wip_limit: number; lanes_per_repository: number; lanes_per_worker: number }) => call<Fleet>("PUT", adminPaths.fleetCaps(), caps);
export const readPolicies = (kind: string) => call<PolicyList>("GET", adminPaths.policies(kind));
export const publishPolicy = (body: string) => call<PublishedPolicy>("POST", adminPaths.publishPolicy(), { body });
// readSpend reads where the project's spend stands, as `ropeshift spend show` does.
export const readSpend = () => call<SpendView>("GET", adminPaths.spend());
export const readCategories = () => call<{ categories: string[] }>("GET", adminPaths.categories());
// A category is added once for the workspace, as `ropeshift category add` does, and a repeat is done.
export const addCategory = (name: string) => call<void>("POST", adminPaths.categories(), { name });

// The roster

export const readRoster = (scope: Scope) => call<RosterView>("GET", adminPaths.roster(scope));
export const readRosterHistory = (scope: Scope, agent = "") => call<RosterHistory>("GET", adminPaths.rosterHistory(scope, agent));
export const changeRoster = (scope: Scope, change: RosterChange) => call<RosterVersion>("POST", adminPaths.rosterChange(scope), { scope, change });
export const revertRoster = (scope: Scope, version: number) => call<RosterVersion>("POST", adminPaths.rosterRevert(scope), { scope, version });

// Members and the audit

export const readMembers = () => call<MemberList>("GET", adminPaths.members());
export const readAudit = (scope: Scope, q: AuditQuery) => call<AuditPage>("GET", adminPaths.audit(scope, q));
// readNotices reads what each channel was told of a project's, and how many times, as `ropeshift notice list` does.
export const readNotices = () => call<NoticeList>("GET", adminPaths.notices());
// readLaneMessages reads what was sent to a lane, oldest first, as `ropeshift message list` does.
export const readLaneMessages = (lane: string) => call<MessageList>("GET", adminPaths.laneMessages(lane));
// readLaneTelemetry reads each lane's context, tokens, and cost from the worker's local store, as `ropeshift
// telemetry lanes` does.
export const readLaneTelemetry = () => call<LaneTelemetryList>("GET", adminPaths.laneTelemetry());

// Templates

// listTemplates reads every template the daemon's binary embeds, as `ropeshift template show` lists and prints them.
export const listTemplates = () => call<TemplateList>("GET", adminPaths.templates());
// writeTemplates asks a worker to write a shape's starting files into a folder on its machine, as `ropeshift new` does,
// and answers with the request, which the worker settles within two minutes. readTemplateWrite reads how it stands.
export const writeTemplates = (worker: string, request: TemplateWriteRequest) => call<TemplateWriteView>("POST", adminPaths.templateWrites(worker), request);
export const readTemplateWrite = (worker: string, request: string) => call<TemplateWriteView>("GET", adminPaths.templateWrite(worker, request));

// Workers

export const readWorkers = () => call<WorkerList>("GET", adminPaths.workers());
// A join token lives a day. The answer carries the token's ID, which a revocation names, and when it expires in
// milliseconds. A group server answers a join string too, which holds the token with the URL its workers reach it at
// and the fingerprint of its authority, and which is what a worker is given in place of the token.
export type JoinTokenMade = { id?: string; token: string; expires_at?: number; join_string?: string };
export const createJoinToken = () => call<JoinTokenMade>("POST", adminPaths.joinToken(), {});
// A token no worker used is revoked, and revoking one again is no error.
export const revokeJoinToken = (id: string) => call<{ id: string }>("POST", adminPaths.joinTokenRevoke(id), {});
export const readAutonomy = (worker: string) => call<Autonomy>("GET", adminPaths.autonomy(worker));
export const setLevel = (worker: string, level: Level) => call<Autonomy>("PUT", adminPaths.autonomy(worker), { level });
// A worker's caps live in the user's profile on its own machine, so a worker on another machine is refused in the
// API's words, for a read and for a change alike.
export const readCaps = (worker: string) => call<WorkerCaps>("GET", adminPaths.caps(worker));
export const setCaps = (worker: string, caps: LaneCapSettings) => call<WorkerCaps>("PUT", adminPaths.caps(worker), caps);
export const readRisk = () => call<Risk>("GET", adminPaths.risk());
export const readHealth = () => call<Health>("GET", adminPaths.health());
export const acknowledgeRisk = (worker: string, digest: string) => call<Autonomy>("POST", adminPaths.acknowledge(worker), { digest });
// A window's ID makes a second press of the same open find the first.
export const openWindow = (worker: string, id: string, lengthMs: number) => call<RunWindow>("POST", adminPaths.windows(worker), { id, length_ms: lengthMs });
export const closeWindow = (worker: string) => call<void>("DELETE", adminPaths.windows(worker), {});
export const stopOrchestrator = (worker: string) => call<unknown>("PUT", adminPaths.orchestrator(worker), { desired_state: "stopped" });
// readOrchestrator reads one worker's orchestrator as the ledger holds it, as `ropeshift orchestrator show` does.
export const readOrchestrator = (worker: string) => call<Orchestrator>("GET", adminPaths.orchestrator(worker));

// The adopt flow's own steps, each one a member's call, as `ropeshift adopt` makes them.

export const readSources = () => call<{ sources: SourceBinding[] }>("GET", adminPaths.sources());
export const readApprovals = (state: string) => call<{ approvals: Approval[] }>("GET", adminPaths.approvals(state));

export type ReadyFailure = { check: string; detail: string };
export type ReviewResult = { state: string; failures?: ReadyFailure[] };

// readAdoptionPlan reads the plan of an adoption, as `ropeshift adopt --dry-run` does, and records nothing. A project
// that does not exist yet has no route of its own, so its plan is read at the installation's, with no record of any
// project read.
export const readAdoptionPlan = (scope: Scope, request: AdoptionPlanRequest) =>
  call<AdoptionPlan>("POST", adminPaths.adoptionPlan(scope), scope === "installation" ? { ...request, new_project: true } : request);

export type AdoptDraft = { id: string; title: string; statement: string; criterion: string; repository: string; restructure?: Restructure };

export const createAdoptWork = (draft: AdoptDraft) =>
  call<{ id: string; title: string }>("POST", adminPaths.workOrders(), {
    id: draft.id,
    kind: "plan",
    title: draft.title,
    statement: draft.statement,
    repository_id: draft.repository || undefined,
    adopt: true,
    restructure: draft.restructure,
    criteria: [{ id: "c1", text: draft.criterion }],
  });
// What a planning session's work order is called when its goal says nothing, how long a title taken from a
// goal runs, and the one criterion the session holds, because the definition of ready wants at least one and
// a plan has no test to run: as `ropeshift plan start` records them.
export const defaultPlanTitle = "Planning session";
export const maxPlanTitle = 80;
export const planCriterion = "The plan is written to the repository as files, and the person has gone through it in this session.";

// planTitle is a goal's first line that says anything, which is what a person scanning a list would call it.
export function planTitle(goal: string): string {
  for (const line of goal.split("\n")) {
    const text = line.trim();
    if (text !== "") {
      return [...text].slice(0, maxPlanTitle).join("");
    }
  }
  return defaultPlanTitle;
}

export type PlanDraft = { id: string; goal: string; repository: string };

// createPlanWork records the work order of a planning session: a `plan` work order whose statement is the goal.
export const createPlanWork = (draft: PlanDraft) =>
  call<{ id: string; title: string }>("POST", adminPaths.workOrders(), {
    id: draft.id,
    kind: "plan",
    title: planTitle(draft.goal),
    statement: draft.goal,
    repository_id: draft.repository || undefined,
    criteria: [{ id: "c1", text: planCriterion }],
  });
// What the New work order form sends, as `ropeshift work create` sends it: what a member left empty is left out.
// The criteria carry the IDs the command gives them, c1 and on.
export type NewWork = {
  id: string;
  kind?: "plan";
  title: string;
  statement?: string;
  criteria?: { id: string; text: string }[];
  repository_id?: string;
  depends_on?: string[];
  requirements?: string[];
  agent?: string;
  size?: string;
  priority?: number;
};

// createWork records a work order and the contract it pins, as `ropeshift work create` does. The ID is the
// client's, so a retry finds the work order the first attempt made.
export const createWork = (request: NewWork) => call<WorkOrder>("POST", adminPaths.workOrders(), request);
// readPlans lists the plans the project holds, as `ropeshift plan list` does.
export const readPlans = () => call<{ plans: Plan[] }>("GET", adminPaths.plans());

// startContractReview records a work order that reads a plan and its contracts, as `ropeshift plan review`
// does. The ID is the client's, so a retry finds the review the first attempt made.
export const startContractReview = (plan: string, id: string) => call<WorkOrder>("POST", adminPaths.reviewPlan(plan), { id });

// readAcceptance reads where a work order stands with what stays linked to it, as `ropeshift work acceptance` does.
export const readAcceptance = (id: string) => call<Standing>("GET", adminPaths.acceptance(id));

// readTimeline reads where a work order's time went, and its delivery record once it ended, as
// `ropeshift work timeline` does.
export const readTimeline = (id: string) => call<WorkTimeline>("GET", adminPaths.timeline(id));
export const reviewWork = (id: string) => call<ReviewResult>("POST", adminPaths.review(id), {});
export const claimWork = (id: string, claim: string, worker: string) => call<unknown>("POST", adminPaths.claim(id), { claim_id: claim, worker_id: worker });
export const startLane = (id: string, lane: string) => call<unknown>("POST", adminPaths.startLane(id), { lane_id: lane });

// Recovering work, each one a member's call as the command line makes it. Giving a claim back names the claim
// and the fence the work order holds it under, which the work order's record says. Archiving a lane asks the
// worker that ran it to remove its worktree, and a request that is not forced is refused while the worktree
// holds work no remote has.
export const releaseClaim = (id: string, claim: string, fence: number) => call<void>("POST", adminPaths.release(id, claim), { fence });
export const archiveLane = (id: string, lane: string, force: boolean) => call<unknown>("POST", adminPaths.archiveLane(id), { lane_id: lane, ...(force ? { force: true } : {}) });
