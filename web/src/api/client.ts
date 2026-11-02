import type { EffectOutcome, NeedsYou, WorkAction } from "./types";

// The API's own names, which a Go test holds this file to. The UI reaches nothing the CLI cannot.
export const paths = {
  signIn: "/v1/sessions",
  signOut: "/v1/sessions/end",
  events: "/v1/events",
  needsYou: "/v1/views/needs-you",
  answer: (id: string) => `/v1/questions/${encodeURIComponent(id)}/answer`,
  decideApproval: (id: string) => `/v1/approvals/${encodeURIComponent(id)}/decide`,
  decideEffect: (id: string) => `/v1/effects/${encodeURIComponent(id)}/decide`,
  work: (id: string, verb: string) => `/v1/work-orders/${encodeURIComponent(id)}/${verb}`,
};

// What each action of a work item calls, and whether it asks a person why.
export const workActions: Record<WorkAction, { verb: string; needsReason: boolean }> = {
  reassign: { verb: "reassign", needsReason: true },
  resolve: { verb: "resolve", needsReason: true },
  replan: { verb: "replan", needsReason: true },
  rework: { verb: "rework", needsReason: true },
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

export function actOnWork(id: string, action: WorkAction, reason: string): Promise<unknown> {
  const { verb, needsReason } = workActions[action];
  return call("POST", paths.work(id, verb), needsReason ? { reason } : {});
}

// The stream every open page follows. A browser's own reconnect names the last revision it saw.
export function followEvents(after: number): EventSource {
  return new EventSource(`${paths.events}?after=${after}`);
}
