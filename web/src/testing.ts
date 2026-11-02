import { vi } from "vitest";

import type { NeedsYou } from "./api/types";
import needsYou from "../testdata/needs-you.json";

// The view a Go test wrote from a real ledger, so these tests run on what the API serves.
export function goldenNeedsYou(): NeedsYou {
  return structuredClone(needsYou) as unknown as NeedsYou;
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

// FakeEvents stands for a browser's stream, which jsdom does not have.
export class FakeEvents {
  static opened: FakeEvents[] = [];
  static readonly CLOSED = 2;
  url: string;
  closed = false;
  readyState = 1;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private heard: (() => void)[] = [];

  constructor(url: string) {
    this.url = url;
    FakeEvents.opened.push(this);
  }

  addEventListener(_type: string, listener: () => void) {
    this.heard.push(listener);
  }

  close() {
    this.closed = true;
  }

  emit() {
    for (const listener of this.heard) {
      listener();
    }
  }
}

export function stubEvents() {
  FakeEvents.opened = [];
  vi.stubGlobal("EventSource", FakeEvents);
}
