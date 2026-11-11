import { vi } from "vitest";

import type { NeedsYou } from "./api/types";
import needsYou from "../testdata/needs-you.json";

// The view a Go test wrote from a real ledger, so these tests run on what the API serves.
export function goldenNeedsYou(): NeedsYou {
  return structuredClone(needsYou) as unknown as NeedsYou;
}

const goldens = import.meta.glob("../testdata/*.json", { eager: true, import: "default" });

// golden reads what a Go test wrote, by the name of its file, in a copy a test may change.
export function golden<T>(name: string): T {
  const found = goldens[`../testdata/${name}.json`];
  if (!found) {
    throw new Error(`no golden named ${name}`);
  }
  return structuredClone(found) as T;
}

// Sent is one call a page made. url is the path it called, and path the same with its project taken out,
// as one project's routes read under /v1, with project the name it took out.
export type Sent = { method: string; url: string; path: string; project: string; body: unknown; csrf: string | null };

type Answer = { status: number; body?: unknown };

// The project every page opens on in a test, and the registry that lists it.
export const testProject = "default";

export const oneProject = {
  status: 200,
  body: { projects: [{ id: "default", name: testProject, state: "active", created_at: 1, repositories: [] }] },
};

// here is the address of a view in the project a test serves.
export const here = (view: string) => `/projects/${testProject}${view}`;

const projectRoute = /^\/v1\/projects\/([^/?]*)(\/.*)$/;

// serveApi stands where the daemon stands: it answers each path and keeps what was sent. A path with no
// answer here fails the test that reached it. It lists one project unless a test answers the registry.
export function serveApi(answers: (sent: Sent) => Answer | undefined, { projects = oneProject }: { projects?: Answer } = {}) {
  const sent: Sent[] = [];
  const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    const scoped = projectRoute.exec(url);
    const call: Sent = {
      method: init?.method ?? "GET",
      url,
      path: scoped ? `/v1${scoped[2]}` : url,
      project: scoped ? decodeURIComponent(scoped[1] ?? "") : "",
      body: init?.body ? JSON.parse(init.body as string) : undefined,
      csrf: headers.get("Ropeshift-Csrf"),
    };
    sent.push(call);
    if (call.method === "GET" && url === "/v1/projects") {
      return new Response(JSON.stringify(projects.body ?? {}), { status: projects.status });
    }
    const answer = answers(call);
    if (!answer) {
      throw new Error(`nothing answers ${call.method} ${url}`);
    }
    return new Response(answer.status === 204 ? null : JSON.stringify(answer.body ?? {}), { status: answer.status });
  });
  vi.stubGlobal("fetch", fetcher);
  return sent;
}

export const refusal = (status: number, code: string, message = code) => ({ status, body: { error: { code, message } } });

// serveViews answers every view from its golden, and a document's reads from theirs, a version the ledger
// stored among them. A test answers
// anything else, or answers differently, by returning something from `answer`.
export function serveViews(answer?: (sent: Sent) => Answer | undefined, options: { projects?: Answer } = {}) {
  return serveApi((call) => {
    const own = answer?.(call);
    if (own) {
      return own;
    }
    const view = /^\/v1\/views\/([a-z-]+)/.exec(call.path)?.[1];
    if (view) {
      return { status: 200, body: golden(view) };
    }
    // The page reads the combined Needs you, of every active project, which the golden view stands for.
    if (call.path === "/v1/needs-you") {
      return { status: 200, body: golden("needs-you") };
    }
    if (call.path.startsWith("/v1/documents/read")) {
      return { status: 200, body: golden("document") };
    }
    if (call.path.startsWith("/v1/documents/versions")) {
      return { status: 200, body: golden("document-versions") };
    }
    if (call.path.startsWith("/v1/documents/recorded")) {
      return { status: 200, body: golden("recorded-document") };
    }
    return undefined;
  }, options);
}

// FakeEvents stands for a browser's stream, which jsdom does not have.
export class FakeEvents {
  static opened: FakeEvents[] = [];
  static readonly CLOSED = 2;
  // url is what the page opened, and path the same as one project's routes read under /v1.
  url: string;
  path: string;
  closed = false;
  readyState = 1;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private heard: { type: string; listener: (event?: { data: string }) => void }[] = [];

  constructor(url: string) {
    this.url = url;
    const scoped = projectRoute.exec(url);
    this.path = scoped ? `/v1${scoped[2]}` : url;
    FakeEvents.opened.push(this);
  }

  addEventListener(type: string, listener: (event?: { data: string }) => void) {
    this.heard.push({ type, listener });
  }

  close() {
    this.closed = true;
    this.readyState = FakeEvents.CLOSED;
  }

  // emit tells every listener that something happened, with nothing more.
  emit() {
    for (const { listener } of this.heard) {
      listener();
    }
  }

  // send delivers one named event with a JSON body, as a server's stream does.
  send(type: string, body: unknown) {
    for (const heard of this.heard) {
      if (heard.type === type) {
        heard.listener({ data: JSON.stringify(body) });
      }
    }
  }

  // drop fails the stream for good, as a browser does when the server refuses it.
  drop() {
    this.readyState = FakeEvents.CLOSED;
    this.onerror?.();
  }
}

export function stubEvents() {
  FakeEvents.opened = [];
  vi.stubGlobal("EventSource", FakeEvents);
}
