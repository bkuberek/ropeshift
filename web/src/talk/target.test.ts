import { describe, expect, it } from "vitest";

import type { Conversation, ConversationTargets } from "../api/talk";
import { golden } from "../testing";
import { announcement, assistantOf, describeTarget, drafting, empty, kinds, rememberDraft, selectTarget, targetOf } from "./target";

const targets = golden<ConversationTargets>("conversation-targets");
const facts = (extra: Partial<Parameters<typeof describeTarget>[1]> = {}) => ({ projectLabel: "Harbor", you: "Sam Rivera", targets, ...extra });

const withLane = (run_mode: string): ConversationTargets => ({
  ...targets,
  planning: [{ lane: "L-91", work_order: "WO-233", title: "Plan alerts", kind: "plan", worker: "worker-atlas", worker_name: "atlas", generation: 1, run_mode: "tmux", state: "working" }],
  lanes: [{ lane: "L-88", work_order: "WO-219", title: "Highlight spans", kind: "change", worker: "worker-atlas", worker_name: "atlas", generation: 2, run_mode, state: "working" }],
});

describe("the target a project shows", () => {
  it("is the assistant until something switches it, in every project", () => {
    expect(targetOf(empty, "harbor")).toEqual({ project: "harbor", kind: "assistant", id: "" });
    expect(targetOf(empty, "kestrel")).toEqual(assistantOf("kestrel"));
  });

  it("changes only when it is switched, and each project keeps its own", () => {
    let state = selectTarget(empty, { project: "harbor", kind: "lane", id: "L-88" });
    state = selectTarget(state, { project: "kestrel", kind: "orchestrator", id: "worker-atlas" });
    expect(targetOf(state, "harbor")).toEqual({ project: "harbor", kind: "lane", id: "L-88" });
    expect(targetOf(state, "kestrel")).toEqual({ project: "kestrel", kind: "orchestrator", id: "worker-atlas" });
    expect(targetOf(state, "quarry")).toEqual(assistantOf("quarry"));
  });

  it("keeps one draft for each target, so moving between them loses none", () => {
    const lane = { project: "harbor", kind: "lane", id: "L-88" } as const;
    const assistant = assistantOf("harbor");
    let state = rememberDraft(empty, assistant, "Which merges wait?");
    state = rememberDraft(state, lane, "Use two retries");
    state = selectTarget(state, lane);
    expect(drafting(state, assistant)).toBe("Which merges wait?");
    expect(drafting(state, lane)).toBe("Use two retries");
    expect(drafting(state, { project: "harbor", kind: "lane", id: "L-90" })).toBe("");
    expect(drafting(rememberDraft(state, lane, ""), lane)).toBe("");
  });
});

describe("the assistant's draft", () => {
  it("is one for the assistant, whichever of its conversations the member is in", () => {
    const state = rememberDraft(empty, { project: "harbor", kind: "assistant", id: "" }, "Which merges wait?");
    expect(drafting(state, { project: "harbor", kind: "assistant", id: "conv-1" })).toBe("Which merges wait?");
    expect(drafting(state, { project: "kestrel", kind: "assistant", id: "" })).toBe("");
  });
});

describe("what a target says of itself", () => {
  it("gives each kind its own label, icon, and color, and no two share one", () => {
    const rows = Object.values(kinds);
    expect(new Set(rows.map((row) => row.label)).size).toBe(4);
    expect(new Set(rows.map((row) => row.icon)).size).toBe(4);
    expect(kinds.assistant.can).toBe("Acts for you through the API. Operational actions run at once; decisions become cards you confirm.");
    expect(kinds.orchestrator.can).toContain("A message wakes it once and grants it nothing.");
    expect(kinds.planning.can).toBe("Writes the plan and its draft decisions on its own branch. Approving the plan stays yours.");
    expect(kinds.lane.can).toBe("Works within its work order's generated rules. A message reaches it once and grants it nothing.");
  });

  it("names the assistant by its conversation and its member", () => {
    const conversation = targets.assistant.conversation as Conversation;
    const said = describeTarget(assistantOf("harbor"), facts({ conversation }));
    expect(said.which).toBe("Harbor · your assistant, Sam Rivera");
    expect(said.name).toBe(conversation.name);
    expect(said.placeholder).toBe("Message the assistant…");
    expect(said.send).toBe("Send to the assistant");
    expect(said.streamLabel).toBe("Messages with the assistant");
    expect(said.mode).toBe("stream");
    expect(said.to).toBe("in Harbor");
  });

  it("names the assistant before it has a conversation", () => {
    const said = describeTarget(assistantOf("harbor"), facts({ conversation: null }));
    expect(said.name).toBe("New conversation");
    expect(said.ended).toBeUndefined();
  });

  it("names an orchestrator by its worker and what it serves, and says when it is not running", () => {
    const said = describeTarget({ project: "harbor", kind: "orchestrator", id: "worker-atlas" }, facts({ targets: { ...targets, orchestrators: [{ ...(targets.orchestrators[0] as object), projects: ["Harbor", "Kestrel", "Quarry"] } as never] } }));
    expect(said.which).toBe("atlas · serves Harbor, Kestrel, Quarry");
    expect(said.send).toBe("Send to the orchestrator on atlas");
    expect(said.streamLabel).toBe("Event stream of the orchestrator on atlas");
    expect(said.ended).toBe("The orchestrator on atlas is not running. Start it from Workers.");
    const running = describeTarget({ project: "harbor", kind: "orchestrator", id: "worker-atlas" }, facts({ targets: { ...targets, orchestrators: [{ ...(targets.orchestrators[0] as object), state: "running" } as never] } }));
    expect(running.ended).toBeUndefined();
  });

  // A running orchestrator's state is what its session reports, so idle and working are both running.
  it("takes a message for an orchestrator a member started, in whatever state its session reports, until it ends", () => {
    const orchestrator = (state: string, desired_state = "running") =>
      describeTarget({ project: "harbor", kind: "orchestrator", id: "worker-atlas" }, facts({ targets: { ...targets, orchestrators: [{ ...(targets.orchestrators[0] as object), state, desired_state } as never] } }));
    for (const state of ["idle", "working", "starting", "needs_decision"]) {
      expect(orchestrator(state).ended, state).toBeUndefined();
    }
    for (const state of ["ended", "tripped"]) {
      expect(orchestrator(state).ended, state).toBe("The orchestrator on atlas is not running. Start it from Workers.");
    }
    expect(orchestrator("stopped", "stopped").ended).toBe("The orchestrator on atlas is not running. Start it from Workers.");
  });

  it("names a headless lane, with a stream and a composer", () => {
    const said = describeTarget({ project: "harbor", kind: "lane", id: "L-88" }, facts({ targets: withLane("headless") }));
    expect(said.which).toBe("L-88 · WO-219 · atlas · headless");
    expect(said.mode).toBe("stream");
    expect(said.placeholder).toBe("Message lane L-88…");
    expect(said.send).toBe("Send to lane L-88");
    expect(said.streamLabel).toBe("Event stream of lane L-88");
    expect(said.to).toBe("L-88 · WO-219 · headless");
  });

  it("shows an interactive lane and a planning session as a pane, with no composer", () => {
    expect(describeTarget({ project: "harbor", kind: "lane", id: "L-88" }, facts({ targets: withLane("tmux") })).mode).toBe("pane");
    const planning = describeTarget({ project: "harbor", kind: "planning", id: "L-91" }, facts({ targets: withLane("headless") }));
    expect(planning.mode).toBe("pane");
    expect(planning.which).toBe("WO-233 · L-91 · atlas");
    expect(planning.name).toBe("Plan alerts");
  });

  it("finds a session in either list, so a control that asks for a lane reaches a planning session too", () => {
    const asLane = describeTarget({ project: "harbor", kind: "lane", id: "L-91" }, facts({ targets: withLane("headless") }));
    expect(asLane.kind).toBe("planning");
    expect(asLane.mode).toBe("pane");
    const asPlanning = describeTarget({ project: "harbor", kind: "planning", id: "L-88" }, facts({ targets: withLane("headless") }));
    expect(asPlanning.kind).toBe("lane");
    expect(asPlanning.mode).toBe("stream");
  });

  it("says a lane that is no longer listed has ended, and why when it is known", () => {
    const gone = { project: "harbor", kind: "lane", id: "L-77" } as const;
    expect(describeTarget(gone, facts({ targets: withLane("headless") })).ended).toBe("Lane L-77 ended.");
    expect(describeTarget(gone, facts({ targets: withLane("headless"), laneEnd: "done" })).ended).toBe("Lane L-77 ended: done.");
  });

  it("waits for the list before it says anything about a lane", () => {
    const said = describeTarget({ project: "harbor", kind: "lane", id: "L-88" }, facts({ targets: null }));
    expect(said.ended).toBeUndefined();
    expect(said.mode).toBe("stream");
  });

  it("announces a switch in words", () => {
    expect(announcement({ project: "harbor", kind: "lane", id: "L-88" })).toBe("Talking to lane L-88");
    expect(announcement(assistantOf("harbor"))).toBe("Talking to the assistant");
    expect(announcement({ project: "harbor", kind: "orchestrator", id: "worker-atlas" }, "atlas")).toBe("Talking to the orchestrator on atlas");
    expect(announcement({ project: "harbor", kind: "planning", id: "L-91" })).toBe("Talking to planning session L-91");
  });
});
