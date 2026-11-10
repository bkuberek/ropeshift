import type { DocumentRef, DocumentRow, DocumentVersions, EffectOutcome, NeedsYou, WorkAction } from "./types";

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

// The API's own names. A test holds them to the list the web listener serves, which a Go test writes,
// and each row of that list names a CLI command, so the UI reaches nothing the CLI cannot.
export const paths = {
  signIn: "/v1/sessions",
  signOut: "/v1/sessions/end",
  events: "/v1/events",
  needsYou: "/v1/views/needs-you",
  view: (name: string, category = "") => `/v1/views/${encodeURIComponent(name)}${category ? `?${new URLSearchParams({ category })}` : ""}`,
  documentRead: (ref: DocumentRef) => `/v1/documents/read?${documentQuery(ref)}`,
  documentVersions: (ref: DocumentRef) => `/v1/documents/versions?${documentQuery(ref)}`,
  answer: (id: string) => `/v1/questions/${encodeURIComponent(id)}/answer`,
  decideApproval: (id: string) => `/v1/approvals/${encodeURIComponent(id)}/decide`,
  decideEffect: (id: string) => `/v1/effects/${encodeURIComponent(id)}/decide`,
  work: (id: string, verb: string) => `/v1/work-orders/${encodeURIComponent(id)}/${verb}`,
  orchestrator: (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/orchestrator`,
};

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

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
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

export function readNeedsYou(): Promise<NeedsYou> {
  return call("GET", paths.needsYou);
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

export function signIn(passphrase: string): Promise<unknown> {
  return call("POST", paths.signIn, { passphrase });
}

export function signOut(): Promise<void> {
  return call("POST", paths.signOut);
}

// The decision's ID comes from the question, so a second send of one answer finds the first.
export function answerQuestion(id: string, answer: string): Promise<unknown> {
  return call("POST", paths.answer(id), { id: `answer-${id}`, answer });
}

export function decideApproval(id: string, digest: string, allow: boolean, reason: string): Promise<unknown> {
  return call("POST", paths.decideApproval(id), { digest, allow, reason });
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

// The stream every open page follows. A browser's own reconnect names the last revision it saw.
export function followEvents(after: number): EventSource {
  return new EventSource(`${paths.events}?after=${after}`);
}
