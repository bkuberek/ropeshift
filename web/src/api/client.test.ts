import { describe, expect, it } from "vitest";

import keys from "../../testdata/pane-keys.json";
import routes from "../../testdata/routes.json";
import { goldenNeedsYou, refusal, serveApi } from "../testing";
import {
  ApiError,
  actOnWork,
  answerQuestion,
  changePassphrase,
  decideApproval,
  decideEffect,
  followEvents,
  passReview,
  paneKeys,
  paths,
  pressPaneKey,
  readDocument,
  readDocumentVersions,
  readApproval,
  readNeedsYou,
  readOutline,
  readPlan,
  readProjects,
  readRecorded,
  readView,
  retryAfterOf,
  sendBack,
  signIn,
  signOut,
  startOrchestrator,
  typeIntoPane,
  workActions,
} from "./client";
import type { Route, WorkAction } from "./types";

const served = (routes as Route[]).map((route) => ({
  method: route.method,
  path: new RegExp(`^${route.pattern.replaceAll("{id}", "[^/]+").replaceAll("{project}", "[^/]+")}(\\?.*)?$`),
}));

const reachable = (method: string, path: string) => served.some((route) => route.method === method && route.path.test(path));

// The list the web listener serves comes from a Go test, and each row of it names a CLI command, so a
// call this client makes that the list lacks is a thing the UI could do and the CLI could not.
describe("the client", () => {
  it("calls nothing the web listener does not serve", async () => {
    const sent = serveApi((call) => (call.method === "GET" ? { status: 200, body: goldenNeedsYou() } : { status: 200 }));
    await readNeedsYou();
    await signIn("a passphrase");
    await signOut();
    await answerQuestion("q1", "Yes");
    await decideApproval("a1", "digest", true, "");
    await decideEffect("worker-a:1:8", "retry", "");
    for (const action of Object.keys(workActions) as WorkAction[]) {
      await actOnWork("wo", action, "because");
    }
    await startOrchestrator("worker-a");
    await passReview("wo");
    await sendBack("wo", "because");
    for (const route of routes as Route[]) {
      const view = /^\/v1\/projects\/\{project\}\/views\/(.+)$/.exec(route.pattern)?.[1];
      if (view) {
        await readView(view);
      }
    }
    await readView("questions", "security");
    await readDocument({ path: "docs/a b.md", instance: "repository", version: "1" });
    await readDocumentVersions({ path: "docs/a b.md" });
    await readRecorded("spec:a b", "2");
    await readPlan("plan a", "3");
    await readApproval("approval a");
    await readOutline("lane:a b");
    await typeIntoPane("lane:a b", "A planning tool", true);
    await pressPaneKey("lane:a b", "Ctrl-C");
    expect(sent.length).toBeGreaterThan(20);
    await readProjects();
    for (const call of sent) {
      expect(reachable(call.method, call.url), `${call.method} ${call.url}`).toBe(true);
    }
    expect(reachable("GET", paths.events())).toBe(true);
    expect(reachable("GET", paths.pane("lane:a b"))).toBe(true);
  });

  it("changes the passphrase with the current one and the new one, by the route the listener serves", async () => {
    const sent = serveApi(() => ({ status: 200, body: { sessions_ended: 3 } }));
    await expect(changePassphrase("the old one", "the new one")).resolves.toEqual({ sessions_ended: 3 });
    expect(sent).toHaveLength(1);
    expect(sent[0]).toMatchObject({ method: "PUT", path: "/v1/passphrase", body: { current: "the old one", passphrase: "the new one" } });
    expect(reachable("PUT", sent[0]?.url ?? "")).toBe(true);
    expect(reachable("GET", "/v1/passphrase")).toBe(false);
    expect(reachable("POST", "/v1/passphrase")).toBe(false);
  });

  it("offers the keys the server takes in a pane, and no other", () => {
    expect([...paneKeys]).toEqual(keys);
  });

  it("escapes an ID into its path", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    await decideEffect("worker/a:1:8", "happened", "");
    expect(sent[0]?.path).toBe("/v1/effects/worker%2Fa%3A1%3A8/decide");
  });

  it("escapes what a read's address holds", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await readView("questions", "a&b=c");
    await readDocument({ path: "../a&b.md", instance: "x y" });
    expect(sent[0]?.path).toBe("/v1/views/questions?category=a%26b%3Dc");
    expect(sent[1]?.path).toBe("/v1/documents/read?path=..%2Fa%26b.md&instance=x+y");
  });

  it("names a recorded document by its reference, and a version only when given one", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await readRecorded("spec:a&b");
    await readRecorded("spec:a&b", "2");
    expect(sent.map((call) => call.path)).toEqual([
      "/v1/documents/recorded?id=spec%3Aa%26b",
      "/v1/documents/recorded?id=spec%3Aa%26b&version=2",
    ]);
  });

  it("names no filter it was not given", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await readView("questions");
    await readView("questions", "");
    await readDocument({ path: "a.md" });
    expect(sent.map((call) => call.path)).toEqual(["/v1/views/questions", "/v1/views/questions", "/v1/documents/read?path=a.md"]);
  });

  it("carries a refusal's code, which is what the UI decides on", async () => {
    serveApi(() => refusal(409, "stale_digest", "It changed."));
    await expect(decideApproval("a1", "d", true, "")).rejects.toMatchObject({ code: "stale_digest", status: 409, message: "It changed." });
  });

  it("names a reply that is not an error body", async () => {
    serveApi(() => ({ status: 502 }));
    const refused = await readNeedsYou().catch((error: unknown) => error);
    expect(refused).toBeInstanceOf(ApiError);
    expect((refused as ApiError).status).toBe(502);
  });

  it("reads a Retry-After as a number of seconds between a second and a day, and anything else as no number", async () => {
    const cases: [string | null, number | undefined][] = [
      ["900", 900],
      [" 45 ", 45],
      ["0", 1],
      ["86400", 86400],
      ["86401", 86400],
      ["9".repeat(400), 86400],
      ["-30", undefined],
      ["1.5", undefined],
      ["soon", undefined],
      ["Wed, 21 Oct 2026 07:28:00 GMT", undefined],
      ["", undefined],
      [null, undefined],
    ];
    for (const [header, seconds] of cases) {
      expect(retryAfterOf(header), `Retry-After ${String(header).slice(0, 12)}`).toBe(seconds);
    }
    serveApi(() => ({ ...refusal(429, "too_many_attempts"), headers: { "Retry-After": "9".repeat(400) } }));
    await expect(readNeedsYou()).rejects.toMatchObject({ status: 429, retryAfter: 86400 });
  });

  it("follows from a revision", () => {
    let opened = "";
    class Recording {
      constructor(url: string) {
        opened = url;
      }
    }
    globalThis.EventSource = Recording as unknown as typeof EventSource;
    followEvents(12);
    expect(opened).toBe("/v1/projects/default/events?after=12");
  });
});

// The ledger takes a decision's ID of 1 to 64 letters, digits, '-', and '_'. An answer's decision is named
// from its question, so a second send of one answer finds the first, whatever the question's ID holds.
describe("the decision an answer becomes", () => {
  const plain = /^[A-Za-z0-9_-]{1,64}$/;
  const sentID = async (question: string): Promise<string> => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await answerQuestion(question, "Two retries.");
    return (sent[0]?.body as { id: string }).id;
  };

  it("is named after a plain question", async () => {
    expect(await sentID("q1")).toBe("answer-q1");
    expect(await sentID("01JD5Z8Q4M3N2P1R0S9T8V7W6X")).toBe("answer-01JD5Z8Q4M3N2P1R0S9T8V7W6X");
  });

  it("is a plain digest of a question whose name would not fit or holds another character", async () => {
    const long = "q".repeat(60);
    for (const question of [long, "q.1", "q:1", "older/question"]) {
      const id = await sentID(question);
      expect(id).toMatch(plain);
      expect(id.startsWith("answer_")).toBe(true);
      expect(await sentID(question)).toBe(id);
    }
    expect(await sentID(long)).not.toBe(await sentID("q".repeat(61)));
    expect(await sentID("q.1")).not.toBe(await sentID("q:1"));
  });
});
