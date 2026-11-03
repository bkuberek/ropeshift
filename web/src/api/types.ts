// What the API serves. Every value here is data, and text from an agent or a document stays text.

export type Stamp = { as_of: number; revision: number };

export type QuestionItem = {
  id: string;
  text: string;
  categories?: string[];
  blocking?: boolean;
  options?: string[];
  recommendation?: string;
  escalation_reason?: string;
  citations?: string[];
};

export type Batch = {
  batch: { id: string; status: string; opened_at: number; closes_at: number; delivered_at?: number };
  items: QuestionItem[];
};

export type Approval = {
  id: string;
  lane_id: string;
  work_order_id: string;
  generation: number;
  kind: string;
  subject: string;
  subject_digest: string;
  input?: string;
  command?: string;
  path?: string;
  summary: string;
  requested_by: string;
  state: string;
  raised_at?: number;
  expires_at?: number;
};

export type EffectOutcome = "happened" | "not_happened" | "retry";

export type Effect = {
  id: string;
  worker_id: string;
  key: string;
  kind: string;
  lane_id: string;
  work_order_id?: string;
  generation: number;
  target?: string;
  attempt: number;
  raised_at: number;
  offers: EffectOutcome[];
};

export type Artifact = { id: string; lane_id: string; kind: string; uri: string; digest?: string; fence?: number; created_at: number };

export type Generation = {
  lane_id: string;
  number: number;
  start_reason: string;
  end_reason?: string;
  started_at: number;
  ended_at?: number;
};

export type Lane = {
  id: string;
  work_order: string;
  worker_id?: string;
  generation: number;
  actual_state: string;
  conditions?: string[];
  liveness: string;
  model_reach?: string;
  ledger_reach?: string;
  freshness: "observed" | "last_reported";
  as_of: number;
  reported_by_epoch?: number;
  archived?: boolean;
  artifacts?: Artifact[];
  generations?: Generation[];
};

export type WorkOrder = {
  id: string;
  title?: string;
  state: string;
  kind: string;
  repository_id?: string;
  branch?: string;
  claim_id?: string;
  lease_worker?: string;
  lease_until?: number;
  attempts?: number;
  max_attempts?: number;
  review_pins?: string[];
};

export type WorkAction = "reassign" | "resolve" | "replan" | "reset" | "respawn" | "resume" | "accept" | "rework" | "cancel";

export type WorkNeed = {
  reason: string;
  detail?: string;
  work_order?: WorkOrder;
  lane?: Lane;
  actions: WorkAction[];
};

export type NeedItem = {
  id: string;
  since: number;
} & (
  | { kind: "questions"; batch: Batch }
  | { kind: "approval"; approval: Approval }
  | { kind: "effect"; effect: Effect }
  | { kind: "work"; work: WorkNeed }
);

export type NeedsYou = Stamp & { items: NeedItem[] };

export type Goal = { id: string; title: string; status: string; statement?: string; success_measures?: string[]; source?: string };

export type Plan = { id: string; goal_id?: string; title: string; status: string; source?: string };

export type PlanProgress = { plan: Plan; total: number; done: number; states: Record<string, number> };

export type GoalProgress = { goal?: Goal; plans: PlanProgress[] };

export type Roadmap = Stamp & { goals: GoalProgress[] };

export type Criterion = { id: string; text: string };

export type Contract = {
  id: string;
  version: number;
  plan_id: string;
  title: string;
  digest: string;
  source?: string;
  statement?: string;
  criteria?: Criterion[];
  requirements?: string[];
  decisions?: string[];
  status: string;
};

export type ContractHistory = { id: string; versions: Contract[] };

export type LadderStep = { rung: number; candidates?: string[]; outcome?: string };

export type Question = {
  id: string;
  text: string;
  categories?: string[];
  status: string;
  rung: number;
  blocking?: boolean;
  subject_kind?: string;
  subject_id?: string;
  asked_by?: string;
  options?: string[];
  recommendation?: string;
  escalation_reason?: string;
  ladder_trace?: LadderStep[];
  claimed_by?: string;
};

export type PlanReview = { work_order: WorkOrder; open_questions: Question[] };

export type PlanDetail = {
  plan: Plan;
  goal?: Goal;
  contracts: ContractHistory[];
  reviews: PlanReview[];
  work_orders: WorkOrder[];
};

export type Plans = Stamp & { plans: PlanDetail[]; unplanned: WorkOrder[] };

export type DocumentKind = "requirements" | "spec" | "design" | "other";

export type DocumentRow = {
  adapter: string;
  instance: string;
  path: string;
  title: string;
  kind: DocumentKind;
  version: string;
  digest: string;
  source: string;
  body?: string;
};

export type Requirement = { id: string; version: number; key: string; text: string; source?: string; digest: string };

export type Documents = Stamp & { documents: DocumentRow[]; requirements: Requirement[] };

export type DocumentRef = { path: string; instance?: string; version?: string };

export type DocumentVersion = { version: string; digest: string };

export type DocumentVersions = { versions: DocumentVersion[] };

export type Decision = {
  id: string;
  status: string;
  statement: string;
  rationale?: string;
  citations?: string[];
  scope?: string;
  reversibility?: string;
  categories?: string[];
  decided_by?: string;
  supersedes?: string;
  superseded_by?: string;
  question_id?: string;
  policy_version?: number;
  overlaps?: string[];
  no_conflict?: string[];
  lineage: string[];
};

export type Decisions = Stamp & { decisions: Decision[] };

export type Questions = Stamp & { categories: string[]; questions: Question[] };

export type WorkLine = { work_order: WorkOrder; lane?: Lane; since: number };

export type InProgress = Stamp & { items: WorkLine[] };

export type Wake = { id: string; expression: string; next_eval_at?: number };

export type HeldLane = { work_order: WorkOrder; lane?: Lane; wakes: Wake[]; since: number };

export type OnHold = Stamp & { items: HeldLane[] };

export type StuckItem = WorkNeed & { id: string; since: number };

export type Stuck = Stamp & { items: StuckItem[] };

export type Assessment = { criteria: { criterion_id: string; standing: string }[]; failed?: string[]; needs: string };

export type CheckRecord = { id: string; name: string; status: string; source: string; criterion_id?: string; details_uri?: string };

export type Standing = { work_order_id: string; state: string; assessment: Assessment; artifacts: Artifact[]; checks: CheckRecord[] };

export type CompletedWork = { work_order: WorkOrder; standing: Standing; since: number; reason?: string };

export type Completed = Stamp & { items: CompletedWork[] };

export type Worker = { id: string; name: string; epoch: number; protocol: number; last_handshake_at?: number; manifest: { capacity?: { max_lanes?: number } } };

export type Orchestrator = { id: string; worker_id: string; desired_state: string; run: number; resets: number; actual_state?: string; current_generation?: number };

export type WorkerLine = { worker: Worker; freshness: "observed" | "last_reported"; orchestrator?: Orchestrator };

export type ShopFloor = Stamp & { workers: WorkerLine[]; lanes: Lane[] };

export type SourceBinding = {
  id: string;
  port: string;
  adapter: string;
  instance: string;
  health: string;
  detail?: string;
  problems?: { path: string; detail: string }[];
};

export type Fleet = { fleet_mode: string; wip_limit: number; lanes_per_repository: number; lanes_per_worker: number };

export type Operations = Stamp & {
  fleet: Fleet;
  work_orders: Record<string, number>;
  lanes: Record<string, number>;
  workers: number;
  workers_fresh: number;
  waiting_for_a_person: number;
  sources: SourceBinding[];
};

export type Route = { method: string; pattern: string; command: string };
