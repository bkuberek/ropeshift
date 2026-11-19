import { call, project } from "./client";

// The practices check, as the API reads it (FR-PRAC-6, FR-PRAC-10): each repository's latest result with a status per
// practice and a list of findings, the request a worker is carrying out, and the acknowledgements members made. Every
// call is a route the web listener serves, which names the CLI command that does the same.

export type Severity = "high" | "medium" | "low";
export type PracticeStatus = "met" | "finding" | "unknown" | "not_applicable";

// The words the practice table gives a practice, which every view of a result prints as they are.
export type PracticeGuide = { why: string; fix: string; template?: string; command?: string };

export type PracticeRow = PracticeGuide & { id: string; severity: Severity; status: PracticeStatus; reason?: string };

export type PracticesAcknowledgement = { finding: string; reason: string; member: string; acknowledged_at: string };

// Rule, path, line, and commit describe a `secrets` finding. A finding never holds the text a rule matched.
export type PracticeFinding = PracticeGuide & {
  id: string;
  practice: string;
  subject?: string;
  severity: Severity;
  detail: string;
  rule?: string;
  path?: string;
  line?: number;
  commit?: string;
  acknowledged?: boolean;
  acknowledgement?: PracticesAcknowledgement;
};

export type PracticesResult = {
  request: string;
  commit: string;
  base_branch: string;
  worker: string;
  checked_at: string;
  table_version: number;
  rules_version: number;
  scanned_from?: string;
  skipped?: number;
  truncated?: boolean;
  practices: PracticeRow[];
  findings: PracticeFinding[];
};

export type RequestState = "offered" | "reported" | "expired";

export type PracticesRequest = {
  id: string;
  repository: string;
  worker: string;
  worker_name?: string;
  cause: string;
  requested_by: string;
  requested_at: number;
  expires_at: number;
  state: RequestState;
  reported_at?: number;
};

export type PracticesView = {
  repository: string;
  name: string;
  result?: PracticesResult;
  request?: PracticesRequest;
  acknowledgement?: PracticesAcknowledgement[];
};

export type PracticesList = { repositories: PracticesView[] };

// What became of one repository in a request for a check: the request made or found open, or why none was.
export type PracticesCheckEntry = { repository: string; name: string; request?: PracticesRequest; existing?: boolean; reason?: string };
export type PracticesChecked = { checks: PracticesCheckEntry[] };

export const practicesPaths = {
  list: (repository: string) => project(`/practices${repository ? `?repository=${encodeURIComponent(repository)}` : ""}`),
  checks: () => project("/practices/checks"),
  check: (request: string) => project(`/practices/checks/${encodeURIComponent(request)}`),
  acknowledgements: () => project("/practices/acknowledgements"),
  withdraw: () => project("/practices/acknowledgements/withdraw"),
};

// listPractices reads the latest result of each repository, or of the one named.
export const listPractices = (repository = "") => call<PracticesList>("GET", practicesPaths.list(repository));

// checkPractices asks that the check run on the repositories named, or on every active repository when none is.
export const checkPractices = (repositories: string[]) => call<PracticesChecked>("POST", practicesPaths.checks(), { repositories });

// readPracticesCheck reads one request and the state it stands in.
export const readPracticesCheck = (request: string) => call<PracticesRequest>("GET", practicesPaths.check(request));

// acknowledgeFinding leaves a finding open with the member's reason, and it stays in every view of the result.
export const acknowledgeFinding = (repository: string, finding: string, reason: string) =>
  call<PracticesAcknowledgement>("POST", practicesPaths.acknowledgements(), { repository, finding, reason });

// withdrawAcknowledgement takes the acknowledgement back, so the finding reads plain again.
export const withdrawAcknowledgement = (repository: string, finding: string) => call<PracticesAcknowledgement>("POST", practicesPaths.withdraw(), { repository, finding });
