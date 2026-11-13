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
  // A plan approval names the plan and the version it is bound to.
  plan_id?: string;
  plan_version?: number;
  // detail is JSON a member reads beyond the summary: a recovery request's is a RecoveryDetail.
  detail?: string;
  // Who decided a request, when, and where: `web` from the web listener, `cli` from the socket.
  decided_by?: string;
  decided_at?: number;
  channel?: string;
  reason?: string;
};

// A change a confirmation card makes: one field of a record from its value to another, one argument the
// call sends, or a whole body it replaces.
export type FieldChange = { field: string; from?: unknown; to: unknown };
export type BodyChange = { before: string; after: string };

// CardDetail is the exact call a confirmation card runs when a member confirms it, why the delegate
// proposes it, and what it changes.
export type CardDetail = {
  operation: string;
  label: string;
  method: string;
  path: string;
  arguments: unknown;
  why: string;
  changes: FieldChange[] | BodyChange;
};

// CardNeed is a confirmation card that waits for a member, with the delegate that prepared it: its
// credential's label and the member it acts for.
export type CardNeed = { approval: Approval; detail: CardDetail; label: string; member: string; conversation?: CardConversation };

// CardConversation names the conversation with the assistant that prepared a card. Once the conversation is
// deleted its name is empty and `deleted` says so: the card stays.
export type CardConversation = { id: string; name: string; deleted?: boolean };

// RecoveryDetail is what an orchestrator asked to do outside its recovery policy, why, and the limit it
// went past.
export type RecoveryDetail = {
  act: string;
  work_order_id: string;
  lane_id?: string;
  reason: string;
  check: string;
  limit: string;
  policy_version: number;
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
  size?: string;
  contract_id?: string;
  // agent is the roster agent the work's lane runs as, which the plan proposes.
  agent?: string;
  requirements?: string[];
  adopt?: boolean;
};

export type WorkAction = "reassign" | "resolve" | "replan" | "reset" | "respawn" | "resume" | "accept" | "rework" | "cancel";

// An orchestrator has no work order, so what a person does about one goes to its worker.
export type OrchestratorAction = "start_orchestrator";

export type WorkNeed = {
  reason: string;
  detail?: string;
  work_order?: WorkOrder;
  lane?: Lane;
  orchestrator?: Orchestrator;
  // What a parked lane waits for, as its wakes state it.
  wakes?: Wake[];
  // Where finished work stands, with the review's verdict for each criterion.
  standing?: Standing;
  actions: (WorkAction | OrchestratorAction)[];
};

export type DraftDecision = { id: string; question: string; answer: string; rationale?: string; blocking?: boolean };

export type PartDifference = { added?: string[]; changed?: string[]; removed?: string[] };

export type ContractDifference = { ref: string; criteria: PartDifference; work_orders: PartDifference };

// How a plan version differs from the last one a member approved. Base is zero when none was approved.
export type Difference = { base?: number; added?: string[]; changed?: string[]; removed?: string[]; contracts?: ContractDifference[] };

// A claim an adopted plan makes of what the code already does, with what shows it and what it rests on.
export type ExistingRecord = { ref: string; title: string; evidence?: string[]; cites?: string[] };

export type ContinuedWork = { contract: string; work_order: string; title: string; repo?: string; branch: string };

export type NewRecord = { ref: string; title: string; work_orders?: string[] };

export type LeftOutRecord = { ref: string; reason: string };

export type FileChange = { status: "added" | "changed" | "moved" | "deleted"; path: string; from?: string };

// An adopted plan as its approval groups it, with every file its branch changed, or why those could not be read.
export type Adoption = {
  exists: ExistingRecord[];
  continues: ContinuedWork[];
  new: NewRecord[];
  left_out: LeftOutRecord[];
  changes: FileChange[];
  changes_unread?: string;
};

export type PlanApprovalNeed = {
  approval: Approval;
  title: string;
  version: number;
  requested_by: string;
  counts: Record<string, number>;
  decisions: number;
  drafts: DraftDecision[];
  difference: Difference;
  summary: string;
  // Adoption groups a plan that adopts an existing project.
  adoption?: Adoption;
};

export type ContractReviewNeed = { work_order: WorkOrder; contract: string; version: number; plan: string };

// ProjectLabel names the project an item of the combined Needs you belongs to.
export type ProjectLabel = { id: string; name: string; display_name?: string };

export type NeedItem = {
  id: string;
  since: number;
  project?: ProjectLabel;
} & (
  | { kind: "questions"; batch: Batch }
  | { kind: "approval"; approval: Approval }
  | { kind: "effect"; effect: Effect }
  | { kind: "work"; work: WorkNeed }
  | { kind: "plan_approval"; plan_approval: PlanApprovalNeed }
  | { kind: "contract_review"; contract_review: ContractReviewNeed }
  | { kind: "confirm"; card: CardNeed }
);

// NeedsProject is one project's part of the combined Needs you: where its ledger stood, and how much waits.
export type NeedsProject = ProjectLabel & { revision: number; waiting: number };

// NeedsYou lists what waits on a member. The combined view holds every active project's items, each
// labeled with its project, and where each project's ledger stood.
export type NeedsYou = Stamp & { items: NeedItem[]; projects?: NeedsProject[] };

export type Goal = { id: string; title: string; status: string; statement?: string; success_measures?: string[]; source?: string };

export type Plan = { id: string; goal_id?: string; title: string; status: string; source?: string };

export type ExitCriterion = { id: string; text: string };

// A milestone's state comes from its work and the milestones it depends on.
export type MilestoneState = "not_started" | "waiting" | "in_progress" | "done" | "existing";

export type MilestoneProgress = {
  id: string;
  title: string;
  priority: number;
  depends_on: string[];
  exit_criteria: ExitCriterion[];
  total: number;
  done: number;
  state: MilestoneState;
  // existing says the code already delivers the milestone, so nothing waits for it.
  existing?: boolean;
};

export type CriticalPath = { work_orders: string[]; weight: number };

export type PlannedWork = { id: string; title: string; state: string; size?: string; milestone?: string };

// Layers group a plan's work orders by how much must come before each, so one layer's work can run at once.
export type PlanProgress = {
  plan: Plan;
  total: number;
  done: number;
  states: Record<string, number>;
  milestones: MilestoneProgress[];
  layers: string[][];
  critical_path: CriticalPath;
  work: PlannedWork[];
};

export type GoalProgress = { goal?: Goal; plans: PlanProgress[] };

export type Roadmap = Stamp & { goals: GoalProgress[] };

// A criterion names the requirements it serves.
export type Criterion = { id: string; text: string; requirements?: string[] };

// A contract is in one milestone of its plan, by the milestone's ID. A shared contract is one other
// contracts of the plan use, and `uses` names those a contract uses, by the ID each one's file gave it.
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
  milestone?: string;
  shared?: boolean;
  uses?: string[];
  status: string;
};

// `key` is the ID the contract's file gave it, which another contract's `uses` names.
export type ContractHistory = { id: string; key?: string; versions: Contract[] };

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

// `pending_version` is the plan version whose approval waits for a member, when one does.
export type PlanDetail = {
  plan: Plan;
  goal?: Goal;
  contracts: ContractHistory[];
  reviews: PlanReview[];
  work_orders: WorkOrder[];
  milestones: MilestoneProgress[];
  pending_version?: number;
  // approved_version is the newest version a member approved, which is the one in force.
  approved_version?: number;
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
  // The planning document the ledger recorded from this file, when a plan holds it.
  record?: string;
};

export type Requirement = {
  id: string;
  version: number;
  key: string;
  text: string;
  source?: string;
  digest: string;
  // status is "met" for a requirement the code already meets, which evidence shows as repository:path.
  status?: string;
  evidence?: string[];
};

// Where a version of a planning document stands in review: covered by a plan version a member approved,
// listed by one whose approval waits, or neither.
export type ReviewState = "approved" | "pending" | "none";

export type RecordedVersion = {
  version: number;
  digest: string;
  source?: string;
  recorded_at: number;
  review: ReviewState;
  plan_version?: number;
};

// A planning document intake recorded from a plan's files, with every version, newest first.
export type RecordedDocument = { id: string; kind: string; title: string; path?: string; versions: RecordedVersion[] };

// What a review found of one criterion, on the commit it read.
export type TracedVerdict = { verdict: VerdictKind; commit: string };

// TracedWork is a work order that carries a criterion. Its verdict is the latest one that speaks for its
// current attempt and commit, and it is absent until a review has judged the criterion.
export type TracedWork = { id: string; title: string; state: string; verdict?: TracedVerdict };

export type TracedCriterion = {
  contract: string;
  contract_title: string;
  contract_version: number;
  criterion: string;
  text: string;
  work_orders: TracedWork[];
};

export type RequirementTrace = {
  key: string;
  text: string;
  document: string;
  criteria: TracedCriterion[];
  // status is "met" for a requirement existing code meets, with the evidence that shows it, and no verdict.
  status?: string;
  evidence?: string[];
};

export type PlanDocuments = { plan: Plan; documents: RecordedDocument[]; requirements: RequirementTrace[] };

export type Documents = Stamp & { documents: DocumentRow[]; requirements: Requirement[]; plans: PlanDocuments[] };

// One version of a planning document with the body the ledger stored.
export type RecordedRead = { as_of: number; document: RecordedDocument; plan?: Plan; version: RecordedVersion; body: string };

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

// A verdict is what a review found of one criterion, bound to the commit it read.
export type VerdictKind = "pass" | "fail" | "cannot_judge";

export type Verdict = {
  criterion: string;
  verdict: VerdictKind;
  reasons: string;
  citations?: string[];
  review_id: string;
  work_order_id: string;
  commit: string;
};

export type ReviewLine = { id: string; state: string; attempt: number };

// One approval of a reviewed commit for merge, and the merge that followed it: who approved, at which merge
// authority level, and what landed.
export type IntegrationRecord = {
  id: string;
  work_order_id: string;
  pull_request?: number;
  approved_commit: string;
  merge_authority: string;
  approved_by_kind: string;
  approved_by_id: string;
  approved_at: number;
  merged_commit?: string;
  merged_at?: number;
};

export type Standing = {
  work_order_id: string;
  state: string;
  assessment: Assessment;
  artifacts: Artifact[];
  checks: CheckRecord[];
  // The reviews this work has had, the verdicts that speak for its current attempt and commit, and what
  // the work waits on when those verdicts hold it for a member.
  reviews?: ReviewLine[];
  verdicts?: Verdict[];
  rounds?: number;
  authority?: string;
  why?: string;
  integrations?: IntegrationRecord[];
};

export type CompletedWork = { work_order: WorkOrder; standing: Standing; since: number; reason?: string };

export type Completed = Stamp & { items: CompletedWork[] };

// A worker's report is what it last measured of its machine and its sessions (FR-OBS-8). A measure the
// machine could not provide is absent, never zero.
export type VolumeResources = { roles: string[]; free_bytes: number; total_bytes: number };

export type MachineResources = {
  cores?: number;
  cpu_percent?: number;
  load_1?: number;
  load_5?: number;
  load_15?: number;
  memory_total_bytes?: number;
  memory_used_bytes?: number;
  memory_available_bytes?: number;
  memory_pressure_some?: number;
  memory_pressure_full?: number;
  volumes: VolumeResources[];
};

export type SessionResources = { kind: "lane" | "orchestrator" | "assistant"; project?: string; lane?: string; cpu_cores?: number; rss_bytes?: number; processes?: number };

export type LaneRecommendation = {
  max_lanes: number;
  basis: "estimates" | "observed";
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

export type ResourceWarning = { measure: "disk_free" | "memory_available" | "memory_pressure" | "cpu_load"; volume?: string; value: number; threshold: number; message: string };

export type ResourceReport = {
  interval_ms: number;
  machine: MachineResources;
  recommendation?: LaneRecommendation;
  caps: { max_lanes: number; max_lanes_set: boolean; max_lanes_per_sign_in: number };
  sessions: SessionResources[];
  warnings?: ResourceWarning[];
};

export type Worker = {
  id: string;
  name: string;
  epoch: number;
  protocol: number;
  last_handshake_at?: number;
  manifest: { capacity?: { max_lanes?: number } };
  report?: ResourceReport;
  reported_at?: number;
};

export type Orchestrator = { id: string; worker_id: string; desired_state: string; run: number; resets: number; actual_state?: string; current_generation?: number };

export type WorkerLine = { worker: Worker; freshness: "observed" | "last_reported"; report_freshness?: "observed" | "last_reported"; orchestrator?: Orchestrator };

// A lane on the shop floor names the work it runs, because a lane's ID says nothing to a person.
export type FloorLane = Lane & { work_order_title?: string };

export type ShopFloor = Stamp & { workers: WorkerLine[]; lanes: FloorLane[] };

export type SourceBinding = {
  id: string;
  port: string;
  adapter: string;
  instance: string;
  health: string;
  detail?: string;
  problems?: { path: string; detail: string }[];
};

// What one worker last said of its gh sign-in for one repository whose review target needs the forge.
export type ForgeLine = {
  worker: string;
  worker_name?: string;
  repository: string;
  repository_name?: string;
  state: string;
  detail?: string;
  checked_at: number;
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
  forges?: ForgeLine[];
  measures: MeasureReport;
};

// A rate of nothing has no value, so value is null when nothing was counted under it.
export type Ratio = { numerator: number; denominator: number; value: number | null };

export type Durations = { count: number; median_ms: number | null; p95_ms: number | null };

export type Cost = { total_usd: number; done: number; per_done_usd: number | null; lower_bound: boolean };

// How a member's decisions stood against the review agent's verdicts over one period. Agreement is evidence,
// and nothing in it changes a repository's merge authority.
export type Agreement = {
  decisions: number;
  agreed: number;
  disagreed: number;
  unknown: number;
  agreement_rate: Ratio;
  approvals_overturned: number;
  later_defects: number;
  failures_overridden: number;
};

export type AgreementPeriods = { last_30_days: Agreement; all_time: Agreement };

export type RepositoryAgreement = AgreementPeriods & { repository_id: string; merge_authority: string };

export type ReviewAgreement = { workspace: AgreementPeriods; repositories: RepositoryAgreement[] };

export type MeasureReport = {
  window: { name: string; from: number; to: number };
  interruptions: number;
  interruptions_by_kind: Record<string, number>;
  completed: number;
  interruptions_per_completed: Ratio;
  overturn_rate: Ratio;
  rework: Ratio;
  autonomous_decision_rate: Ratio;
  repeated_question_rate: Ratio;
  prevented_repeats: number;
  park_time: Record<string, Durations>;
  ready_to_done: Durations;
  intake_to_done: Durations;
  work_in_progress: number;
  cost_per_outcome: Record<string, Cost>;
  review_agreement: ReviewAgreement;
};

export type Route = { method: string; pattern: string; command: string };

// PaneScreen is one snapshot of a lane's pane: its visible screen, with the escape sequences that set its
// colours and emphasis, from the pane of the generation it names.
export type PaneScreen = { lane_id: string; generation: number; screen: string };

// PaneLane says a lane has no screen to show now, and the state that says why.
export type PaneLane = { lane_id: string; generation: number; state: string };

// A project of the installation, as the registry lists it.
export type Project = {
  id: string;
  name: string;
  display_name?: string;
  state: "active" | "archived";
  created_at: number;
  repositories: string[];
  // waiting counts what waits on a member in an active project.
  waiting?: number;
};

export type ProjectList = { projects: Project[] };

// SearchRecord is one record the project's search found: its kind, ID, title, and state.
export type SearchRecord = { kind: string; id: string; title?: string; state?: string };

// Search is the records whose ID or title holds every word of a query, ranked by the server.
export type Search = Stamp & { query?: string; records: SearchRecord[]; more?: boolean };

// The records a plan version's approval covers, each by its reference and digest. A record that is a
// document names the document it is, so its versions can be read.
export type PlanEntry = { ref: string; digest: string; record?: string };

// PlanApprovalDetail is what a request to approve a plan says of its version beyond its summary. The
// approval's `detail` is this, as JSON.
export type PlanApprovalDetail = {
  title: string;
  ref?: string;
  counts?: Record<string, number>;
  decisions?: number;
  difference?: Difference;
  // source is the commit of the planning branch the version was read from.
  source?: string;
  adoption?: Adoption;
};

// PlanApprovalView is a request to approve a plan version with everything a member reads before deciding.
export type PlanApprovalView = { approval: Approval; detail: PlanApprovalDetail; records?: PlanEntry[]; drafts?: DraftDecision[] };

// One version of a plan, and where the request to approve it stands: none when nobody asked.
export type PlanVersionLine = { version: number; digest?: string; approval?: string; state?: string };

// PlanView is one plan with its goal, each contract's newest version, their work orders, and the request to
// approve it: the one waiting, or else the newest, or the one of the version asked for.
export type PlanView = {
  plan: Plan;
  goal?: Goal;
  contracts: Contract[];
  work_orders: WorkOrder[];
  approval?: PlanApprovalView;
  versions: PlanVersionLine[];
};

// A request as the approvals list and show routes answer, with who decided it and when once it is decided.
export type ApprovalRecord = Approval & { decided_by?: string; decided_at?: number; reason?: string; conversation?: CardConversation };

export type OutlineDocument = { path: string; kind: string; title?: string; status: "new" | "changed" };
export type OutlineMilestone = { id: string; title: string; priority: number };
export type OutlineContract = { id: string; title: string; criteria: number; work_orders: number };
export type OutlineQuestion = { id: string; text: string; blocking?: boolean };

// PlanOutline is what a planning lane's branch holds at its tip: read from the repository, only what is
// committed. A branch that cannot be read answers `unread` with the reason, and nothing else.
export type PlanOutline = {
  unread?: string;
  commit?: string;
  documents?: OutlineDocument[];
  milestones?: OutlineMilestone[];
  contracts?: OutlineContract[];
  decisions?: DraftDecision[];
  questions?: OutlineQuestion[];
  approval?: { id: string; state: string; version: number };
  not_submitted?: string;
};
