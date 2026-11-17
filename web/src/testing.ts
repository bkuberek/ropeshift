import { readFileSync } from "node:fs";

import { waitFor } from "@testing-library/react";
import { expect, onTestFinished, vi } from "vitest";

import type { Health } from "./api/admin";
import type { Approval, MergeDetail, NeedsYou } from "./api/types";
import needsYou from "../testdata/needs-you.json";

// What a daemon says of itself when the build installed at its path is the one it runs.
export const runningHealth: Health = {
  status: "ok",
  revision: 1,
  mode: "solo",
  version: "v0.1.0-alpha.16",
  commit: "0123456789abcdef0123456789abcdef01234567",
  date: "2026-11-08T10:00:00Z",
  installed: "v0.1.0-alpha.16",
};

// What the daemon of an installation in group mode says of itself, which a test of what only a group offers serves.
export const groupHealth: Health = { ...runningHealth, mode: "group" };

// The view a Go test wrote from a real ledger, so these tests run on what the API serves.
export function goldenNeedsYou(): NeedsYou {
  return structuredClone(needsYou) as unknown as NeedsYou;
}

// The commit a merge request's review passed, which the request is bound to.
export const reviewedCommit = "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432";

// mergeRequest is a request a member decides about the merge of a work order's reviewed commit, as the ledger
// holds it: to approve the merge at person_approves, or to decide a merge that is held. It names no lane and
// runs no command, and its detail says where the commit lands.
export function mergeRequest(subject: "approve" | "held" = "approve", over: Partial<MergeDetail> = {}): Approval {
  const detail: MergeDetail = {
    work_order_id: "wo-merge",
    repository: "harbor-web",
    commit: reviewedCommit,
    branch: "ropeshift/wo-merge",
    base: "main",
    method: "squash",
    merge_authority: "person_approves",
    ...(subject === "held" ? { reason: "the base branch requires 2 approving reviews", request: "merge:wo-merge:1" } : {}),
    ...over,
  };
  const into = detail.pull_request ? `pull request #${detail.pull_request}` : `${detail.branch} into ${detail.base}`;
  return {
    id: `merge-${subject}`,
    lane_id: "",
    work_order_id: detail.work_order_id,
    generation: 0,
    kind: "merge",
    subject,
    subject_digest: reviewedCommit,
    summary:
      subject === "held"
        ? `The merge of ${into}, commit 9f8e7d6 of work order wo-merge (Add the retry budget), cannot go on: ${detail.reason ?? ""}. Approve to try it again, or deny to send the work back for rework.`
        : `Approve the merge of ${into}, commit 9f8e7d6 of work order wo-merge (Add the retry budget), at person_approves. The review's verdicts are on the work order.`,
    requested_by: "system:integration",
    state: "pending",
    raised_at: 1790000100000,
    detail: JSON.stringify(detail),
  };
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

// The stylesheets, in the order main.tsx imports them.
const sheets = ["tokens", "base", "shell", "components", "views", "admin", "talk", "responsive"];

// withStyles puts the application's stylesheets in the page for one test, so the test can read what the
// cascade gives an element. The test's page lays nothing out, so this holds the rules, not the sizes.
export function withStyles(): void {
  const style = document.createElement("style");
  style.textContent = sheets.map((name) => readFileSync(`src/styles/${name}.css`, "utf8")).join("\n");
  document.head.append(style);
  onTestFinished(() => style.remove());
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
// stored among them, a plan, the outline of a planning lane, and the project's repositories. A test answers
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
    // The daemon's own build, which the sidebar names and Settings, About gives.
    if (call.method === "GET" && call.path === "/v1/health") {
      return { status: 200, body: runningHealth };
    }
    // The templates the daemon's binary embeds, which the Templates view lists.
    if (call.method === "GET" && call.path === "/v1/templates") {
      return { status: 200, body: golden("templates") };
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
    // A plan's forecast, which the plan page and the Roadmap read apart from the plan.
    if (call.method === "GET" && /^\/v1\/plans\/[^/?]+\/forecast/.test(call.path)) {
      return { status: 200, body: golden("forecast") };
    }
    // A work order's estimate beside its baseline, as its page and its Activity tab read them.
    if (call.method === "GET" && /^\/v1\/work-orders\/[^/?]+\/estimate$/.test(call.path)) {
      return { status: 200, body: golden("work-estimate") };
    }
    if (call.method === "GET" && /^\/v1\/plans\/[^/?]+/.test(call.path)) {
      return { status: 200, body: golden("plan") };
    }
    // Delivery look back, grouped as the page asks.
    if (call.method === "GET" && call.path.startsWith("/v1/measures/delivery")) {
      return { status: 200, body: golden(call.path.includes("by=size") ? "delivery-size" : "delivery") };
    }
    // The delivery history a planning session sees, as the planning pane's History tab reads it.
    if (call.method === "GET" && call.path === "/v1/measures/history") {
      return { status: 200, body: golden("history") };
    }
    // Estimate calibration, as the Estimates tab reads it.
    if (call.method === "GET" && call.path.startsWith("/v1/measures/estimates")) {
      return { status: 200, body: golden("calibration") };
    }
    if (call.method === "GET" && /^\/v1\/lanes\/[^/]+\/outline$/.test(call.path)) {
      return { status: 200, body: golden("outline") };
    }
    // What was sent to a lane, which the Lane view lists.
    if (call.method === "GET" && /^\/v1\/lanes\/[^/]+\/messages$/.test(call.path)) {
      return { status: 200, body: golden("lane-messages") };
    }
    // What each lane reported of its tokens and cost, which Usage lists.
    if (call.method === "GET" && call.path === "/v1/telemetry/lanes") {
      return { status: 200, body: golden("telemetry-lanes") };
    }
    if (call.method === "GET" && call.path === "/v1/repositories") {
      return { status: 200, body: golden("repositories") };
    }
    // The categories the questions view lists are the project's every category, built-in and added.
    if (call.method === "GET" && call.path === "/v1/categories") {
      return { status: 200, body: { categories: golden<{ categories: string[] }>("questions").categories } };
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

// openLedgerStream waits for the stream the page follows the ledger on, and returns it. A page opens that
// stream in an effect, after the render a test finds its content in, so an event sent before it opens reaches
// nobody. Waiting here, and sending on what it returns, fails loudly where an optional call would skip the
// event and leave the test to wait out its timeout.
export function openLedgerStream(): Promise<FakeEvents> {
  return waitFor(() => {
    const open = FakeEvents.opened.filter((stream) => !stream.closed && stream.path.startsWith("/v1/events"));
    expect(open).toHaveLength(1);
    return open[0] as FakeEvents;
  });
}

export function stubEvents() {
  FakeEvents.opened = [];
  vi.stubGlobal("EventSource", FakeEvents);
}
