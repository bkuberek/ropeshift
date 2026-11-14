import { splitProject } from "../app/route";
import type { ApprovalRecord, DocumentRef, DocumentRow, DocumentVersions, EffectOutcome, NeedsYou, PlanOutline, PlanView, ProjectList, RecordedRead, Search, WorkAction } from "./types";

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
  projects: "/v1/projects",
  events: (name = "") => (name ? `/v1/projects/${encodeURIComponent(name)}/events` : project("/events")),
  needsYou: () => "/v1/needs-you",
  view: (name: string, category = "") => project(`/views/${encodeURIComponent(name)}${category ? `?${new URLSearchParams({ category })}` : ""}`),
  search: (words: string) => project(`/views/search?${new URLSearchParams({ q: words })}`),
  documentRead: (ref: DocumentRef) => project(`/documents/read?${documentQuery(ref)}`),
  documentVersions: (ref: DocumentRef) => project(`/documents/versions?${documentQuery(ref)}`),
  documentRecorded: (id: string, version = "") => project(`/documents/recorded?${new URLSearchParams(version ? { id, version } : { id })}`),
  plan: (id: string, version = "") => project(`/plans/${encodeURIComponent(id)}${version ? `?${new URLSearchParams({ version })}` : ""}`),
  approval: (id: string) => project(`/approvals/${encodeURIComponent(id)}`),
  outline: (lane: string) => project(`/lanes/${encodeURIComponent(lane)}/outline`),
  answer: (id: string) => project(`/questions/${encodeURIComponent(id)}/answer`),
  decideApproval: (id: string) => project(`/approvals/${encodeURIComponent(id)}/decide`),
  decideEffect: (id: string) => project(`/effects/${encodeURIComponent(id)}/decide`),
  work: (id: string, verb: string) => project(`/work-orders/${encodeURIComponent(id)}/${verb}`),
  orchestrator: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/orchestrator`,
  pane: (lane: string) => project(`/panes/${encodeURIComponent(lane)}`),
  paneInput: (lane: string) => project(`/panes/${encodeURIComponent(lane)}/input`),
};

// The keys a member may press in a lane's pane. The list is closed, and the server holds the same one.
export const paneKeys = ["Enter", "Escape", "Tab", "Backspace", "Up", "Down", "Left", "Right", "Ctrl-C", "Ctrl-D"] as const;

export type PaneKey = (typeof paneKeys)[number];

// What each action of a work item calls, and whether it asks a person why.
export const workActions: Record<WorkAction, { verb: string; needsReason: boolean }> = {
  reassign: { verb: "reassign", needsReason: true },
  resolve: { verb: "resolve", needsReason: true },
  replan: { verb: "replan", needsReason: true },
  rework: { verb: "rework", needsReason: true },
  cancel: { verb: "cancel", needsReason: true },
  accept: { verb: "accept", needsReason: false },
  reset: { verb: "lane-reset", needsReason: false },
  respawn: { verb: "lane-respawn", needsReason: false },
  resume: { verb: "lane-resume", needsReason: false },
};

const csrfCookie = "ropeshift_csrf";
const csrfHeader = "Ropeshift-Csrf";

// ApiError is a refusal the server explained. The UI decides on the code, never on the text.
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
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
    const refused = (await response.json().catch(() => null)) as { error?: { code: string; message: string } } | null;
    throw new ApiError(response.status, refused?.error?.code ?? "unavailable", refused?.error?.message ?? response.statusText);
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
    const refused = (await response.json().catch(() => null)) as { error?: { code: string; message: string } } | null;
    throw new ApiError(response.status, refused?.error?.code ?? "unavailable", refused?.error?.message ?? response.statusText);
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
export function readView<T>(name: string, category = ""): Promise<T> {
  return call("GET", paths.view(name, category));
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

// Where a decision was made: Needs you, or inline in a conversation. A call that names no place is
// recorded by the listener it came through.
export type Place = "needs_you" | "conversation";

// The decision's ID comes from the question, so a second send of one answer finds the first.
export function answerQuestion(id: string, answer: string, place?: Place): Promise<unknown> {
  return call("POST", paths.answer(id), { id: `answer-${id}`, answer, ...(place ? { place } : {}) });
}

export function decideApproval(id: string, digest: string, allow: boolean, reason: string, place?: Place): Promise<unknown> {
  return call("POST", paths.decideApproval(id), { digest, allow, reason, ...(place ? { place } : {}) });
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

export function pressPaneKey(lane: string, key: PaneKey): Promise<void> {
  return call("POST", paths.paneInput(lane), { key });
}
