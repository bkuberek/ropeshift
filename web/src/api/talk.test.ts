import { describe, expect, it } from "vitest";

import routes from "../../testdata/routes.json";
import { golden, serveApi } from "../testing";
import { answerQuestion, decideApproval } from "./client";
import {
  clearConversation,
  compactConversation,
  createConversation,
  deleteConversation,
  openConversation,
  readConversationEntries,
  readConversations,
  readConversationTargets,
  readLaneStream,
  readOrchestratorStream,
  renameConversation,
  sendMessage,
  sendToAssistant,
  stopConversation,
} from "./talk";
import type { ConversationList, ConversationTargets, TalkPage } from "./talk";
import type { Route } from "./types";

const served = (routes as Route[]).map((route) => ({
  method: route.method,
  path: new RegExp(`^${route.pattern.replaceAll("{id}", "[^/]+").replaceAll("{worker}", "[^/]+").replaceAll("{project}", "[^/]+")}(\\?.*)?$`),
}));

const reachable = (method: string, path: string) => served.some((route) => route.method === method && route.path.test(path));

describe("the conversation calls", () => {
  it("call nothing the web listener does not serve", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await readConversations();
    await readConversationTargets();
    await readConversationEntries("conv/1", "cursor 1");
    await readLaneStream("lane:a b");
    await readOrchestratorStream("worker a", "x&y");
    await createConversation("conv-1", "Merges", "atlas");
    await openConversation("conv-1", "atlas");
    await stopConversation("conv-1");
    await renameConversation("conv-1", "Merge queue");
    await deleteConversation("conv-1");
    await compactConversation("conv-1");
    await clearConversation("conv-1");
    await sendToAssistant("conv-1", "Which merges wait?", "atlas");
    await sendMessage({ lane: "lane-1" }, "Use two retries");
    await sendMessage({ kind: "orchestrator", worker: "worker-atlas" }, "Claim the harbor work first");
    expect(sent).toHaveLength(15);
    for (const call of sent) {
      expect(reachable(call.method, call.url), `${call.method} ${call.url}`).toBe(true);
    }
  });

  it("escape what an address holds into its path and query", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await readConversationEntries("conv/1", "a&b=c");
    await readLaneStream("lane:a b");
    await readOrchestratorStream("worker a", "x y");
    expect(sent.map((call) => call.path)).toEqual([
      "/v1/conversations/conv%2F1/entries?before=a%26b%3Dc",
      "/v1/lanes/lane%3Aa%20b/stream",
      "/v1/orchestrators/worker%20a/stream?before=x+y",
    ]);
  });

  it("send a lane's message and an orchestrator's as instructions, each to its own target", async () => {
    const sent = serveApi(() => ({ status: 201, body: {} }));
    await sendMessage({ lane: "lane-1" }, "Use two retries", "m-1");
    await sendMessage({ kind: "orchestrator", worker: "worker-atlas" }, "Claim first", "m-2");
    expect(sent.map((call) => [call.method, call.path, call.body])).toEqual([
      ["POST", "/v1/messages", { id: "m-1", to: { lane: "lane-1" }, kind: "instruction", body: "Use two retries" }],
      ["POST", "/v1/messages", { id: "m-2", to: { kind: "orchestrator", worker: "worker-atlas" }, kind: "instruction", body: "Claim first" }],
    ]);
  });

  it("name a worker only when the member chose one", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await createConversation("conv-1");
    await openConversation("conv-1");
    await sendToAssistant("conv-1", "Hello", "", "m-1");
    expect(sent.map((call) => call.body)).toEqual([{ id: "conv-1" }, {}, { id: "m-1", body: "Hello" }]);
  });

  it("send the place a decision was made in, and no place when none is given", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await decideApproval("a1", "digest", true, "");
    await decideApproval("a1", "digest", true, "", "conversation");
    await answerQuestion("q1", "Yes");
    await answerQuestion("q1", "Yes", "conversation");
    expect(sent.map((call) => (call.body as { place?: string }).place)).toEqual([undefined, "conversation", undefined, "conversation"]);
  });
});

// What the API serves is what these types read: the goldens are written by a Go test from a real ledger.
describe("the goldens", () => {
  it("hold a conversation, its entries, and what the member can talk to", () => {
    const list = golden<ConversationList>("conversations");
    expect(list.conversations[0]?.actual_state).toBe("running");
    const entries = golden<TalkPage>("conversation-entries");
    expect(new Set(entries.entries.map((entry) => entry.kind))).toEqual(new Set(["marker", "member", "reply", "card", "action"]));
    expect(entries.entries.some((entry) => entry.assent)).toBe(true);
    const targets = golden<ConversationTargets>("conversation-targets");
    expect(targets.assistant.warm_idle_stop_ms).toBe(120_000);
  });
});
