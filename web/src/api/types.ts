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

export type Lane = {
  id: string;
  work_order: string;
  worker_id?: string;
  generation: number;
  actual_state: string;
  conditions?: string[];
  liveness: string;
  freshness: "observed" | "last_reported";
  as_of: number;
};

export type WorkOrder = { id: string; title?: string; state: string; kind: string; repository_id?: string };

export type WorkAction = "reassign" | "resolve" | "replan" | "reset" | "respawn" | "resume" | "accept" | "rework";

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

export type Route = { method: string; pattern: string; command: string };
