import { describe, expect, it } from "vitest";

import routes from "../../testdata/routes.json";
import { goldenNeedsYou, refusal, serveApi } from "../testing";
import {
  ApiError,
  actOnWork,
  answerQuestion,
  decideApproval,
  decideEffect,
  followEvents,
  passReview,
  paths,
  readDocument,
  readDocumentVersions,
  readNeedsYou,
  readRecorded,
  readView,
  sendBack,
  signIn,
  signOut,
  startOrchestrator,
  workActions,
} from "./client";
import type { Route, WorkAction } from "./types";

const served = (routes as Route[]).map((route) => ({
  method: route.method,
  path: new RegExp(`^${route.pattern.replaceAll("{id}", "[^/]+")}(\\?.*)?$`),
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
      const view = /^\/v1\/views\/(.+)$/.exec(route.pattern)?.[1];
      if (view) {
        await readView(view);
      }
    }
    await readView("questions", "security");
    await readDocument({ path: "docs/a b.md", instance: "repository", version: "1" });
    await readDocumentVersions({ path: "docs/a b.md" });
    await readRecorded("spec:a b", "2");
    expect(sent.length).toBeGreaterThan(20);
    for (const call of sent) {
      expect(reachable(call.method, call.path), `${call.method} ${call.path}`).toBe(true);
    }
    expect(reachable("GET", paths.events)).toBe(true);
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

  it("follows from a revision", () => {
    let opened = "";
    class Recording {
      constructor(url: string) {
        opened = url;
      }
    }
    globalThis.EventSource = Recording as unknown as typeof EventSource;
    followEvents(12);
    expect(opened).toBe("/v1/events?after=12");
  });
});
