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

export type Sent = { method: string; path: string; body: unknown; csrf: string | null };

type Answer = { status: number; body?: unknown };

// serveApi stands where the daemon stands: it answers each path and keeps what was sent. A path with no
// answer here fails the test that reached it.
export function serveApi(answers: (sent: Sent) => Answer | undefined) {
  const sent: Sent[] = [];
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    const call: Sent = {
      method: init?.method ?? "GET",
      path,
      body: init?.body ? JSON.parse(init.body as string) : undefined,
      csrf: headers.get("Ropeshift-Csrf"),
    };
    sent.push(call);
    const answer = answers(call);
    if (!answer) {
      throw new Error(`nothing answers ${call.method} ${path}`);
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
export function serveViews(answer?: (sent: Sent) => Answer | undefined) {
  return serveApi((call) => {
    const own = answer?.(call);
    if (own) {
      return own;
    }
    const view = /^\/v1\/views\/([a-z-]+)/.exec(call.path)?.[1];
    if (view) {
      return { status: 200, body: golden(view) };
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
  });
}

// FakeEvents stands for a browser's stream, which jsdom does not have.
export class FakeEvents {
  static opened: FakeEvents[] = [];
  static readonly CLOSED = 2;
  url: string;
  closed = false;
  readyState = 1;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private heard: { type: string; listener: (event?: { data: string }) => void }[] = [];

  constructor(url: string) {
    this.url = url;
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
