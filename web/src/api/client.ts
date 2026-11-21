import { splitProject } from "../app/route";
import type { ApprovalDecided, ApprovalRecord, CandidateList, CIWatch, DocumentRef, DocumentRow, DocumentVersions, EffectOutcome, NeedsYou, Overturned, OverturnDecision, PaneScrollback, PlanOutline, PlanRevision, PlanView, ProjectList, Question, RecordDecision, RecordedDecision, RecordedRead, RecordQuestion, ScheduledStart, Search, WorkAction, WorkOrder } from "./types";

function documentQuery({ path, instance, version }: DocumentRef): URLSearchParams {
  const query = new URLSearchParams({ path });
  if (instance) {
    query.set("instance", instance);
  }
  if (version) {
    query.set("version", version);
  }
  return query;
}

// viewQuery is a view's query, of what narrows it, in the order given; an empty value is left out.
function viewQuery(filter: Record<string, string>): string {
  const query = new URLSearchParams(Object.entries(filter).filter(([, value]) => value !== "")).toString();
  return query ? `?${query}` : "";
}

// acting names the project an item of the combined Needs you acts in, while its change is sent.
let acting = "";

// current is the project an installation view reads, whose address names none: the project the page
// chose for its switch.
let current = "";

export function setCurrentProject(name: string): void {
  current = name;
}

// project is the route of one project's records, as the API names it: the project an item names while it
// acts, or else the project the page shows, which its address names, or else the one the page chose.
export function project(rest: string): string {
  return `/v1/projects/${encodeURIComponent(acting || splitProject(window.location.pathname).project || current)}${rest}`;
}

// inProjectOf sends a change to the project an item belongs to. A path is built when its call starts, so
// the project holds for the whole of one change, and every call after it reaches the page's project again.
export function inProjectOf<T>(name: string, run: () => T): T {
  const was = acting;
  acting = name;
  try {
    return run();
  } finally {
    acting = was;
  }
}

// The API's own names. A test holds them to the list the web listener serves, which a Go test writes,
// and each row of that list names a CLI command, so the UI reaches nothing the CLI cannot.
export const paths = {
  signIn: "/v1/sessions",
  signOut: "/v1/sessions/end",
  passphrase: "/v1/passphrase",
  projects: "/v1/projects",
  events: (name = "") => (name ? `/v1/projects/${encodeURIComponent(name)}/events` : project("/events")),
  needsYou: () => "/v1/needs-you",
  view: (name: string, category = "", filter: Record<string, string> = {}) => project(`/views/${encodeURIComponent(name)}${viewQuery({ category, ...filter })}`),
  search: (words: string) => project(`/views/search?${new URLSearchParams({ q: words })}`),
  documentRead: (ref: DocumentRef) => project(`/documents/read?${documentQuery(ref)}`),
  documentVersions: (ref: DocumentRef) => project(`/documents/versions?${documentQuery(ref)}`),
  documentRecorded: (id: string, version = "") => project(`/documents/recorded?${new URLSearchParams(version ? { id, version } : { id })}`),
  plan: (id: string, version = "") => project(`/plans/${encodeURIComponent(id)}${version ? `?${new URLSearchParams({ version })}` : ""}`),
  revision: (plan: string, rest = "") => project(`/plans/${encodeURIComponent(plan)}/revision${rest ? `/${rest}` : ""}`),
  approval: (id: string) => project(`/approvals/${encodeURIComponent(id)}`),
  outline: (lane: string) => project(`/lanes/${encodeURIComponent(lane)}/outline`),
  questions: () => project("/questions"),
  answer: (id: string) => project(`/questions/${encodeURIComponent(id)}/answer`),
  batchAnswers: (id: string) => project(`/batches/${encodeURIComponent(id)}/answers`),
  dismiss: (id: string) => project(`/questions/${encodeURIComponent(id)}/dismiss`),
  merge: (id: string) => project(`/questions/${encodeURIComponent(id)}/merge`),
  decideNow: (id: string) => project(`/questions/${encodeURIComponent(id)}/decide-now`),
  decisions: () => project("/decisions"),
  overturn: (id: string) => project(`/decisions/${encodeURIComponent(id)}/overturn`),
  candidates: (text: string, limit: number) => project(`/candidates?${new URLSearchParams({ text, kinds: candidateKinds.join(","), limit: String(limit) })}`),
  decideApproval: (id: string) => project(`/approvals/${encodeURIComponent(id)}/decide`),
  retryRepository: (approval: string, name: string) => project(`/approvals/${encodeURIComponent(approval)}/repositories/${encodeURIComponent(name)}/retry`),
  skipRepository: (approval: string, name: string) => project(`/approvals/${encodeURIComponent(approval)}/repositories/${encodeURIComponent(name)}/skip`),
  decideEffect: (id: string) => project(`/effects/${encodeURIComponent(id)}/decide`),
  work: (id: string, verb: string) => project(`/work-orders/${encodeURIComponent(id)}/${verb}`),
  orchestrator: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/orchestrator`,
  pane: (lane: string) => project(`/panes/${encodeURIComponent(lane)}`),
  paneInput: (lane: string) => project(`/panes/${encodeURIComponent(lane)}/input`),
  paneScrollback: (lane: string, lines: number) => project(`/panes/${encodeURIComponent(lane)}/scrollback?lines=${lines}`),
};

// The named keys a chord in a lane's pane may end with: tmux's names for them, which the server holds the
// same list of. A chord is any of Ctrl, Alt, and Shift, each with a hyphen, then a named key or one
// character, and a sequence is up to maxPaneChords chords separated by spaces, such as Ctrl-X Ctrl-S.
export const paneKeyNames = [
  "Up", "Down", "Left", "Right", "BSpace", "BTab", "DC", "End", "Enter", "Escape",
  "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12",
  "Home", "IC", "PageUp", "PageDown", "Space", "Tab",
] as const;

export type PaneKeyName = (typeof paneKeyNames)[number];

// maxPaneChords is the most chords one key sequence holds.
export const maxPaneChords = 8;

// The kinds of record `ropeshift search` reads when it is given none: what a question may match.
const candidateKinds = ["decision", "requirement", "contract", "decision_document", "question"];

// What each action of a work item calls, and whether it asks a person why.
export const workActions: Record<WorkAction, { verb: string; needsReason: boolean }> = {
  reassign: { verb: "reassign", needsReason: true },
  resolve: { verb: "resolve", needsReason: true },
  replan: { verb: "replan", needsReason: true },
  rework: { verb: "rework", needsReason: true },
  cancel: { verb: "cancel", needsReason: true },
  retry: { verb: "retry", needsReason: true },
  accept: { verb: "accept", needsReason: false },
  reset: { verb: "lane-reset", needsReason: false },
  respawn: { verb: "lane-respawn", needsReason: false },
  resume: { verb: "lane-resume", needsReason: false },
};

const csrfCookie = "ropeshift_csrf";
const csrfHeader = "Ropeshift-Csrf";

// ApiError is a refusal the server explained. The UI decides on the code, never on the text. retryAfter is the
// number of seconds a Retry-After header asked the client to wait, which a group server sends with its limit on
// wrong guesses.
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly retryAfter: number | undefined;

  constructor(status: number, code: string, message: string, retryAfter?: number) {
    super(message);
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
  }
}

// The bounds a Retry-After is held to, in seconds. The server never asks for less than a second, and a refusal for too
// many attempts cannot mean no wait. A day is longer than any limit on sign-in attempts is meant to hold a person off,
// and a number past it is more likely a broken header than a setting; the server answers a try after a day with the
// wait that is left, so a person told a day is never let in early.
export const shortestRetrySeconds = 1;
export const longestRetrySeconds = 24 * 60 * 60;

// retryAfterOf reads a Retry-After header as a number of seconds, the one form the server sends, held to the bounds
// above, so a 0 says a second and a number too long to count says a day. A date, a negative number, or anything else
// is no number of seconds.
export function retryAfterOf(header: string | null): number | undefined {
  const wait = header?.trim() ?? "";
  if (!/^\d+$/.test(wait)) {
    return undefined;
  }
  return Math.min(longestRetrySeconds, Math.max(shortestRetrySeconds, Number(wait)));
}

// refusalOf reads a refusal's code and message, and its Retry-After when the header is a number of seconds.
async function refusalOf(response: Response): Promise<ApiError> {
  const refused = (await response.json().catch(() => null)) as { error?: { code: string; message: string } } | null;
  const retryAfter = retryAfterOf(response.headers.get("Retry-After"));
  return new ApiError(response.status, refused?.error?.code ?? "unavailable", refused?.error?.message ?? response.statusText, retryAfter);
}

function readCookie(name: string): string {
  const prefix = `${name}=`;
  for (const part of document.cookie.split("; ")) {
    if (part.startsWith(prefix)) {
      return decodeURIComponent(part.slice(prefix.length));
    }
  }
  return "";
}

export async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (method !== "GET") {
    headers["Content-Type"] = "application/json";
    headers[csrfHeader] = readCookie(csrfCookie);
  }
  let response: Response;
  try {
    response = await fetch(path, { method, headers, body: method === "GET" ? undefined : JSON.stringify(body ?? {}) });
  } catch {
    throw new ApiError(0, "unreachable", "The daemon did not answer.");
  }
  if (!response.ok) {
    throw await refusalOf(response);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

// A file a route answers, such as a backup: its bytes and the name the server gave it.
export type Download = { blob: Blob; name: string };

// request sends a change whose answer is a file, which a page saves as a download.
export async function request(method: string, path: string, body: unknown): Promise<Download> {
  let response: Response;
  try {
    response = await fetch(path, { method, headers: { "Content-Type": "application/json", [csrfHeader]: readCookie(csrfCookie) }, body: JSON.stringify(body ?? {}) });
  } catch {
    throw new ApiError(0, "unreachable", "The daemon did not answer.");
  }
  if (!response.ok) {
    throw await refusalOf(response);
  }
  const named = /filename="([^"]+)"/.exec(response.headers.get("Content-Disposition") ?? "")?.[1];
  return { blob: await response.blob(), name: named ?? "ropeshift-backup.db" };
}

export function readNeedsYou(): Promise<NeedsYou> {
  return call("GET", paths.needsYou());
}

// readProjects lists the installation's projects, which every view of one project names in its address.
export function readProjects(): Promise<ProjectList> {
  return call("GET", paths.projects);
}

// readView reads one read model. The server filters, so a page never holds what a filter left out.
// readView reads one view. filter holds what else narrows it, such as the questions view's status, lane, and work
// order; an empty value narrows nothing.
export function readView<T>(name: string, category = "", filter: Record<string, string> = {}): Promise<T> {
  return call("GET", paths.view(name, category, filter));
}

export function readDocument(ref: DocumentRef): Promise<DocumentRow> {
  return call("GET", paths.documentRead(ref));
}

export function readDocumentVersions(ref: DocumentRef): Promise<DocumentVersions> {
  return call("GET", paths.documentVersions(ref));
}

// readRecorded reads a version of a planning document as the ledger stored it. No version reads the newest.
export function readRecorded(id: string, version = ""): Promise<RecordedRead> {
  return call("GET", paths.documentRecorded(id, version));
}

// readPlan reads a plan with the request to approve it: the one waiting or the newest, or the one of a
// version asked for by number.
export function readPlan(id: string, version = ""): Promise<PlanView> {
  return call("GET", paths.plan(id, version));
}

// confirmRevision records that the plan's approved version stands with the decisions made since, which releases the
// work that waited for it.
export function confirmRevision(plan: string): Promise<PlanRevision> {
  return call("POST", paths.revision(plan, "confirm"), {});
}

// reviseSession records the planning work order of a session that revises the plan, whose brief lists the revision's
// decisions. The caller goes on as any planning session does.
export function reviseSession(plan: string, request: { id: string; repository?: string }): Promise<WorkOrder> {
  return call("POST", paths.revision(plan, "sessions"), request);
}

// reworkForDecisions makes the rework of one finished work order for the decisions a plan's revision lists for it.
export function reworkForDecisions(id: string, decisions: string[]): Promise<{ work_order: string; rework: string }> {
  return call("POST", paths.work(id, "decision-rework"), { decisions });
}

// waitLongerForCI adds time to the CI timeout of a work order's head, which "CI has not finished" offers: the time
// named, such as "45m", or one more p85 of the repository's CI when none is.
export function waitLongerForCI(id: string, by = ""): Promise<{ work_order: string; watch: CIWatch }> {
  return call("POST", paths.work(id, "ci-wait"), by ? { by } : {});
}

// rerunCI asks the worker to rerun the failed jobs of the checks of a work order's head, which a head that has not
// finished or failed for a reason no agent can fix offers. It starts no agent.
export function rerunCI(id: string): Promise<{ work_order: string; reruns: number }> {
  return call("POST", paths.work(id, "ci-rerun"), {});
}

// readApproval reads one request, which is how a plan version the ledger holds no plan for yet is read.
export function readApproval(id: string): Promise<ApprovalRecord> {
  return call("GET", paths.approval(id));
}

// readOutline reads the plan a planning lane has committed to its branch.
export function readOutline(lane: string): Promise<PlanOutline> {
  return call("GET", paths.outline(lane));
}

export function signIn(passphrase: string): Promise<unknown> {
  return call("POST", paths.signIn, { passphrase });
}

export function signOut(): Promise<void> {
  return call("POST", paths.signOut);
}

// What a change of the passphrase did: it ended the member's other sessions, counted, and left the delegate credentials
// that still act, counted too. The session it came from goes on under a new token, which the answer sets as cookies.
export type PassphraseChanged = { sessions_ended: number; delegates: number };

// changePassphrase replaces the passphrase, as `ropeshift passphrase set` does after the first. The current passphrase
// proves the person at the page knows it, and the daemon ends the member's other sessions with the change. This is the
// only call a page makes with a passphrase besides signing in.
export function changePassphrase(current: string, next: string): Promise<PassphraseChanged> {
  return call("PUT", paths.passphrase, { current, passphrase: next });
}

// Where a decision was made: Needs you, or inline in a conversation. A call that names no place is
// recorded by the listener it came through.
export type Place = "needs_you" | "conversation";

// A plain ID is what the ledger takes for a decision: 1 to 64 letters, digits, '-', and '_'.
export const plainID = /^[A-Za-z0-9_-]{1,64}$/;

// decisionIDOf names the decision an answer becomes after its question, so a second send of one answer finds
// the first. A question whose name would not fit, or holds another character as an older one may, gives a
// digest of its ID instead, under a prefix the plain form never makes.
export async function decisionIDOf(question: string): Promise<string> {
  const named = `answer-${question}`;
  if (plainID.test(named)) {
    return named;
  }
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(question));
  const hex = Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
  return `answer_${hex.slice(0, 40)}`;
}

// answerQuestion answers a question, which records a decision. `supersedes` names the decision in force the answer
// replaces, with no rework: the daemon ends it in the transaction that records the answer.
export async function answerQuestion(id: string, answer: string, place?: Place, supersedes?: string, assumption?: { rationale: string }): Promise<RecordedDecision | undefined> {
  return call<RecordedDecision | undefined>("POST", paths.answer(id), {
    id: await decisionIDOf(id),
    answer,
    ...(place ? { place } : {}),
    ...(supersedes ? { supersedes } : {}),
    ...(assumption ? { assumed: true, ...(assumption.rationale ? { rationale: assumption.rationale } : {}) } : {}),
  });
}

// BatchAnswer is one answer of a batch request. An accepted one records the question's recommendation, with its
// rationale and the item's citations, and states nothing else of its own. Any other is the member's own answer,
// and it says why. An accepted one names the reversibility its item stated when it was read, and the daemon refuses
// the whole request when a policy published since says otherwise, so a one-way decision nobody chose is never recorded.
export type BatchAnswer =
  | { question: string; accepted: true; expect_reversibility?: "reversible" | "one_way"; supersedes?: string }
  | { question: string; accepted?: false; answer: string; rationale: string; supersedes?: string };

// answerBatch answers the questions of one batch in one request, as `ropeshift question answer-batch` does. Every
// answer is recorded or none is, and a refusal names the first one it met. The decision of each answer takes its
// name from its question, so a second send of the same answers finds the first.
export async function answerBatch(batch: string, answers: BatchAnswer[], place?: Place): Promise<RecordedDecision[]> {
  const items = await Promise.all(answers.map(async (answer) => ({ ...answer, id: await decisionIDOf(answer.question) })));
  const recorded = await call<{ decisions: RecordedDecision[] } | undefined>("POST", paths.batchAnswers(batch), { answers: items, ...(place ? { place } : {}) });
  return recorded?.decisions ?? [];
}

// recordQuestion records a question as it is, as `ropeshift question record` does. The ID is the client's, so a
// retry finds the record the first attempt made.
export function recordQuestion(request: RecordQuestion): Promise<Question> {
  return call("POST", paths.questions(), request);
}

// dismissQuestion closes a question with no answer. It stays on record.
export function dismissQuestion(id: string): Promise<void> {
  return call("POST", paths.dismiss(id), {});
}

// mergeQuestion closes a question in favor of the one that stays, which takes its lanes, as
// `ropeshift question merge --into` does.
export function mergeQuestion(id: string, into: string): Promise<void> {
  return call("POST", paths.merge(id), { into });
}

// decideQuestionNow takes a question off the ladder into the member's Needs you, as `ropeshift question decide-now`
// does, and answers with the question as it waits there, in its batch.
export function decideQuestionNow(id: string): Promise<Question> {
  return call<Question>("POST", paths.decideNow(id), {});
}

// recordDecision records a decision, or with the status `assumed` an assumption, as `ropeshift decision record`
// does. The ID is the client's, so a retry finds the record the first attempt made.
export function recordDecision(request: RecordDecision): Promise<RecordedDecision> {
  return call("POST", paths.decisions(), request);
}

// overturnDecision reverses a decision with what takes its place, as `ropeshift decision overturn` does. It
// sends each completed work order that rested on the decision to rework, and the answer names them. The ID of
// the new decision is the client's, so a retry finds what the first attempt made.
export function overturnDecision(id: string, request: OverturnDecision): Promise<Overturned> {
  return call("POST", paths.overturn(id), request);
}

// searchCandidates ranks the project's decisions, requirements, contracts, and questions that match some words,
// best first, as `ropeshift search` does. A member cites what it finds.
export function searchCandidates(text: string, limit = 8): Promise<CandidateList> {
  return call("GET", paths.candidates(text, limit));
}

export function decideApproval(id: string, digest: string, allow: boolean, reason: string, place?: Place): Promise<ApprovalDecided> {
  return call<ApprovalDecided>("POST", paths.decideApproval(id), { digest, allow, reason, ...(place ? { place } : {}) });
}

// retryRepository runs the creation of one repository an approved plan version names again, from its journal, and
// answers with the approval and what its registration holds now.
export function retryRepository(approval: string, name: string): Promise<ApprovalDecided> {
  return call<ApprovalDecided>("POST", paths.retryRepository(approval, name), {});
}

// skipRepository leaves a repository an approved plan version names uncreated, with the member's reason.
export function skipRepository(approval: string, name: string, reason: string): Promise<ApprovalDecided> {
  return call<ApprovalDecided>("POST", paths.skipRepository(approval, name), { reason });
}

export function decideEffect(id: string, outcome: EffectOutcome, reason: string): Promise<unknown> {
  return call("POST", paths.decideEffect(id), { outcome, reason });
}

// A member passes a contract review no plan approval covers, which records them as its reviewer.
export function passReview(id: string): Promise<unknown> {
  return call("POST", paths.work(id, "review"), {});
}

export function sendBack(id: string, reason: string): Promise<unknown> {
  return call("POST", paths.work(id, "send-back"), { reason });
}

export function actOnWork(id: string, action: WorkAction, reason: string): Promise<unknown> {
  const { verb, needsReason } = workActions[action];
  return call("POST", paths.work(id, verb), needsReason ? { reason } : {});
}

// Asking for an orchestrator that runs resets a breaker that stopped it and replaces one that stalled.
export function startOrchestrator(worker: string): Promise<unknown> {
  return call("PUT", paths.orchestrator(worker), { desired_state: "running" });
}

// scheduleOrchestrator starts a worker's orchestrator at its sign-in's usage reset, which is what Start asks while the
// limit holds, as `ropeshift orchestrator schedule` does.
export function scheduleOrchestrator(worker: string): Promise<ScheduledStart> {
  return call("POST", `${paths.orchestrator(worker)}/schedule`, {});
}

// cancelScheduledStart cancels the start scheduled for a worker's orchestrator, as `ropeshift orchestrator unschedule`
// does.
export function cancelScheduledStart(worker: string): Promise<void> {
  return call("DELETE", `${paths.orchestrator(worker)}/schedule`);
}

// searchRecords reads the project's records whose ID or title holds every word, for the command palette.
export function searchRecords(words: string): Promise<Search> {
  return call("GET", paths.search(words));
}

// The stream every open page follows, of the project it shows. A browser's own reconnect names the last
// revision it saw.
export function followEvents(after: number, name = ""): EventSource {
  return new EventSource(`${paths.events(name)}?after=${after}`);
}

// followPane streams a lane's pane: a snapshot of its screen each time it changes, and the lane's state
// while it has no screen to show.
export function followPane(lane: string): EventSource {
  return new EventSource(paths.pane(lane));
}

// paneRefusal asks why a pane's stream closed, because a browser's stream reads no refusal. A stream that
// would open again answers nothing, and the read stops at its first byte.
export async function paneRefusal(lane: string): Promise<ApiError | null> {
  const reading = new AbortController();
  try {
    const response = await fetch(paths.pane(lane), { signal: reading.signal });
    if (response.ok) {
      return null;
    }
    const refused = (await response.json().catch(() => null)) as { error?: { code: string; message: string } } | null;
    return new ApiError(response.status, refused?.error?.code ?? "unavailable", refused?.error?.message ?? response.statusText);
  } catch {
    return new ApiError(0, "unreachable", "The daemon did not answer.");
  } finally {
    reading.abort();
  }
}

// typeIntoPane types text into a lane's pane, and presses Enter after it when asked.
export function typeIntoPane(lane: string, text: string, enter: boolean): Promise<void> {
  return call("POST", paths.paneInput(lane), { text, enter });
}

// sendPaneKeys presses a key sequence in a lane's pane, such as Shift-Tab or Ctrl-X Ctrl-S.
export function sendPaneKeys(lane: string, keys: string): Promise<void> {
  return call("POST", paths.paneInput(lane), { key: keys });
}

// maxPaneScrollback is the most lines one read of a pane's scrollback asks for, as the server bounds it.
export const maxPaneScrollback = 10000;

// readPaneScrollback reads the newest lines of the scrollback of a lane's pane, up to the count asked for.
export function readPaneScrollback(lane: string, lines: number): Promise<PaneScrollback> {
  return call("GET", paths.paneScrollback(lane, lines));
}
