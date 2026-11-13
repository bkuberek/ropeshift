import { call, project, request } from "./client";
import type { Project, SourceBinding } from "./types";

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
}>;

export type RepositoryAdded = { path: string; outcome: "registered" | "known" | "refused"; repository?: Repository; checkout?: string; reason?: string };
export type RepositoryAddReport = { dry_run: boolean; repositories: RepositoryAdded[] };

export type Fleet = { fleet_mode: "run" | "pause" | "drain"; wip_limit: number; lanes_per_repository: number; lanes_per_worker: number };

export type PolicyVersion = { id: string; kind: string; version: number; digest: string; body: string; settings: string; authored_by: string; created_at: number };
export type PolicyList = { versions: PolicyVersion[] };

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
  occurred_at?: number;
  recorded_at: number;
  data?: unknown;
};

export type AuditPage = { scope: "installation" | "project"; as_of: number; head: number; next: number; scanned?: boolean; events: AuditEvent[] };
export type AuditQuery = { actor?: string; kind?: string; subject?: string; before?: number; limit?: number };

export type WorkerRecord = { id: string; name?: string; epoch?: number; protocol?: number; manifest?: { capacity?: { max_lanes?: number } }; last_handshake_at?: number };
export type WorkerList = { workers: WorkerRecord[] };

export type Level = "manual" | "attended" | "unattended";
export type RunWindow = { id: string; worker_id: string; opened_by: string; opened_at: number; closes_at: number };
export type Autonomy = {
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
  fleet: () => project("/fleet"),
  fleetMode: () => project("/fleet/mode"),
  fleetCaps: () => project("/fleet/caps"),
  policies: (kind: string) => project(`/policies${query({ kind })}`),
  publishPolicy: () => project("/policies"),
  categories: () => project("/categories"),
  roster: (scope: Scope) => scoped(scope, "/roster"),
  rosterHistory: (scope: Scope, agent = "") => scoped(scope, `/roster/history${query({ scope, agent })}`),
  rosterChange: (scope: Scope) => scoped(scope, "/roster/changes"),
  rosterRevert: (scope: Scope) => scoped(scope, "/roster/revert"),
  members: () => "/v1/members",
  audit: (scope: Scope, q: AuditQuery) => scoped(scope, `/audit${query({ actor: q.actor, kind: q.kind, subject: q.subject, before: q.before, limit: q.limit })}`),
  workers: () => "/v1/workers",
  joinToken: () => "/v1/workers/tokens",
  autonomy: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/autonomy`,
  acknowledge: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/autonomy/acknowledgement`,
  windows: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/windows`,
  risk: () => "/v1/autonomy/risk",
  orchestrator: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/orchestrator`,
  approvals: (state: string) => project(`/approvals${query({ state })}`),
  workOrders: () => project("/work-orders"),
  sources: () => project("/sources"),
  claim: (id: string) => project(`/work-orders/${encodeURIComponent(id)}/claims`),
  startLane: (id: string) => project(`/work-orders/${encodeURIComponent(id)}/lanes`),
  review: (id: string) => project(`/work-orders/${encodeURIComponent(id)}/review`),
};

// Projects

export const createProject = (name: string, displayName: string) => call<Project>("POST", adminPaths.createProject(), { name, display_name: displayName || undefined });
export const renameProject = (name: string, next: string) => call<Project>("POST", adminPaths.projectStep(name, "rename"), { name: next });
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
export const publishPolicy = (body: string) => call<PolicyVersion>("POST", adminPaths.publishPolicy(), { body });
export const readCategories = () => call<{ categories: string[] }>("GET", adminPaths.categories());

// The roster

export const readRoster = (scope: Scope) => call<RosterView>("GET", adminPaths.roster(scope));
export const readRosterHistory = (scope: Scope, agent = "") => call<RosterHistory>("GET", adminPaths.rosterHistory(scope, agent));
export const changeRoster = (scope: Scope, change: RosterChange) => call<RosterVersion>("POST", adminPaths.rosterChange(scope), { scope, change });
export const revertRoster = (scope: Scope, version: number) => call<RosterVersion>("POST", adminPaths.rosterRevert(scope), { scope, version });

// Members and the audit

export const readMembers = () => call<MemberList>("GET", adminPaths.members());
export const readAudit = (scope: Scope, q: AuditQuery) => call<AuditPage>("GET", adminPaths.audit(scope, q));

// Workers

export const readWorkers = () => call<WorkerList>("GET", adminPaths.workers());
export const createJoinToken = () => call<{ token: string }>("POST", adminPaths.joinToken(), {});
export const readAutonomy = (worker: string) => call<Autonomy>("GET", adminPaths.autonomy(worker));
export const setLevel = (worker: string, level: Level) => call<Autonomy>("PUT", adminPaths.autonomy(worker), { level });
export const readRisk = () => call<Risk>("GET", adminPaths.risk());
export const acknowledgeRisk = (worker: string, digest: string) => call<Autonomy>("POST", adminPaths.acknowledge(worker), { digest });
// A window's ID makes a second press of the same open find the first.
export const openWindow = (worker: string, id: string, lengthMs: number) => call<RunWindow>("POST", adminPaths.windows(worker), { id, length_ms: lengthMs });
export const closeWindow = (worker: string) => call<void>("DELETE", adminPaths.windows(worker), {});
export const stopOrchestrator = (worker: string) => call<unknown>("PUT", adminPaths.orchestrator(worker), { desired_state: "stopped" });

// The adopt flow's own steps, each one a member's call, as `ropeshift adopt` makes them.

export const readSources = () => call<{ sources: SourceBinding[] }>("GET", adminPaths.sources());
export const readApprovals = (state: string) => call<{ approvals: Approval[] }>("GET", adminPaths.approvals(state));

export type ReadyFailure = { check: string; detail: string };
export type ReviewResult = { state: string; failures?: ReadyFailure[] };

export type AdoptDraft = { id: string; title: string; statement: string; criterion: string; repository: string };

export const createAdoptWork = (draft: AdoptDraft) =>
  call<{ id: string; title: string }>("POST", adminPaths.workOrders(), {
    id: draft.id,
    kind: "plan",
    title: draft.title,
    statement: draft.statement,
    repository_id: draft.repository || undefined,
    adopt: true,
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
export const reviewWork = (id: string) => call<ReviewResult>("POST", adminPaths.review(id), {});
export const claimWork = (id: string, claim: string, worker: string) => call<unknown>("POST", adminPaths.claim(id), { claim_id: claim, worker_id: worker });
export const startLane = (id: string, lane: string) => call<unknown>("POST", adminPaths.startLane(id), { lane_id: lane });
