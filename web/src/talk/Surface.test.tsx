import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import type { SessionTarget } from "../api/talk";
import { openPage } from "../opening";
import { here, openLedgerStream, refusal } from "../testing";
import type { Sent } from "../testing";
import { aConversation, serveTalk, talking } from "./serve";
import type { Talking } from "./serve";

// The dock on a view that holds nothing the tests read, so what is read and sent here is the conversation's.
const open = async (state: Talking, path = here("/roadmap")) => {
  const sent = serveTalk(state);
  openPage(path);
  await screen.findByRole("heading", { level: 1 });
  await userEvent.keyboard(".");
  const dock = await screen.findByRole("complementary", { name: "Conversation" });
  return { sent, dock };
};

const changes = (sent: Sent[]) => sent.filter((call) => call.method !== "GET");

const lane = (over: Partial<SessionTarget> = {}): SessionTarget => ({
  lane: "L-88",
  work_order: "WO-219",
  title: "Highlight spans",
  kind: "change",
  worker: "worker-atlas",
  worker_name: "atlas",
  generation: 2,
  run_mode: "headless",
  state: "working",
  ...over,
});

let state: Talking;
beforeEach(() => {
  state = talking();
});

describe("opening the dock", () => {
  it("reads the newest conversation's history from the ledger and starts nothing", async () => {
    state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped" })];
    const { sent, dock } = await open(state);
    const log = await within(dock).findByRole("log", { name: "Messages with the assistant" });
    expect(within(log).getByText("Cancel the retry work, then plan the harbor work.")).toBeTruthy();
    expect(within(dock).getByText("Stopped · type to resume")).toBeTruthy();
    expect(changes(sent)).toEqual([]);
  });

  it("names the assistant, the project, the member, and what it can do", async () => {
    const { dock } = await open(state);
    await within(dock).findByRole("log", { name: "Messages with the assistant" });
    expect(within(dock).getByRole("heading", { level: 2, name: state.conversations[0]?.name })).toBeTruthy();
    expect(within(dock).getByText(/· your assistant, member-1$/)).toBeTruthy();
    expect(within(dock).getByText("Acts for you through the API. Operational actions run at once; decisions become cards you confirm.")).toBeTruthy();
    expect(within(dock).getByText("generation 1")).toBeTruthy();
    expect(within(dock).getByText("stops after 15 min idle")).toBeTruthy();
  });

  it("says the shorter stop of a session started ahead of a message, until a message reaches it", async () => {
    state.entries = state.entries.filter((entry) => entry.kind === "marker");
    const { dock } = await open(state);
    await within(dock).findByRole("log", { name: "Messages with the assistant" });
    expect(within(dock).getByText("stops after 2 min if no message is sent")).toBeTruthy();
    expect(within(dock).queryByText("stops after 15 min idle")).toBeNull();
  });

  it("closing it stops nothing", async () => {
    const { sent, dock } = await open(state);
    await within(dock).findByRole("log", { name: "Messages with the assistant" });
    await userEvent.click(within(dock).getByRole("button", { name: "Close the conversation panel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Conversation panel" }).getAttribute("aria-pressed")).toBe("false"));
    expect(changes(sent)).toEqual([]);
  });
});

describe("starting the session", () => {
  it("starts on the first keystroke, once, and sends nothing", async () => {
    state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped" })];
    const { sent, dock } = await open(state);
    const box = await within(dock).findByRole("textbox", { name: "Message to the assistant" });
    await userEvent.type(box, "Which merges wait?");
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/conversations/conv-1/open", body: {} });
    expect((box as HTMLTextAreaElement).value).toBe("Which merges wait?");
  });

  it("starts nothing on a key after a session failed to start, and starts it on Resume", async () => {
    state.conversations = [aConversation({ actual_state: "failed", desired_state: "stopped", stopped_reason: "could_not_start" })];
    const { sent, dock } = await open(state);
    const box = await within(dock).findByRole("textbox", { name: "Message to the assistant" });
    expect(within(dock).getByText("Could not start")).toBeTruthy();
    await userEvent.type(box, "Hi");
    await new Promise((done) => setTimeout(done, 300));
    expect(changes(sent)).toEqual([]);
    await userEvent.click(within(dock).getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe("/v1/conversations/conv-1/open");
  });

  it("starts nothing for a conversation whose session already runs", async () => {
    const { sent, dock } = await open(state);
    await userEvent.type(await within(dock).findByRole("textbox", { name: "Message to the assistant" }), "hi");
    expect(changes(sent)).toEqual([]);
  });

  it("makes the first conversation on the first keystroke, with an ID of its own", async () => {
    state.conversations = [];
    state.entries = [];
    const { sent, dock } = await open(state);
    await userEvent.type(await within(dock).findByRole("textbox", { name: "Message to the assistant" }), "Hello");
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    const made = changes(sent)[0];
    expect(made?.path).toBe("/v1/conversations");
    expect((made?.body as { id: string }).id).toMatch(/^[0-9A-Z]{26}$/);
  });

  it("asks which worker when several can run it, and starts on the one chosen", async () => {
    state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped", worker_id: undefined })];
    state.workers = [
      { id: "worker-atlas", name: "atlas", available: true },
      { id: "worker-basalt", name: "basalt", available: true },
    ];
    const { sent, dock } = await open(state);
    await userEvent.type(await within(dock).findByRole("textbox", { name: "Message to the assistant" }), "Hi");
    const dialog = await screen.findByRole("dialog", { name: "Choose a worker" });
    expect(changes(sent)).toEqual([]);
    await userEvent.click(within(dialog).getByRole("radio", { name: "basalt" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Start the session" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/conversations/conv-1/open", body: { worker: "worker-basalt" } });
  });

  it("names why no worker can run it, and the conversation stays readable", async () => {
    state.workers = [{ id: "worker-a", name: "solo", available: false, reason: "runs no headless session" }];
    const { dock } = await open(state);
    expect(await within(dock).findByText(/No worker can run the assistant now\. solo runs no headless session\./)).toBeTruthy();
    expect(within(dock).getByRole("log", { name: "Messages with the assistant" })).toBeTruthy();
  });
});

describe("sending a message to the assistant", () => {
  it("sends with Mod+Enter and keeps Enter for a new line", async () => {
    const { sent, dock } = await open(state);
    const box = await within(dock).findByRole("textbox", { name: "Message to the assistant" });
    await userEvent.type(box, "Line one{Enter}line two");
    expect((box as HTMLTextAreaElement).value).toBe("Line one\nline two");
    expect(changes(sent)).toEqual([]);
    await userEvent.keyboard("{Control>}{Enter}{/Control}");
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/conversations/conv-1/messages", body: { body: "Line one\nline two" } });
    await waitFor(() => expect((box as HTMLTextAreaElement).value).toBe(""));
  });

  it("names the send button for its target", async () => {
    const { dock } = await open(state);
    expect(await within(dock).findByRole("button", { name: "Send to the assistant" })).toBeTruthy();
  });

  it("shows the API's refusal at the composer and keeps the draft", async () => {
    state.answers["POST /v1/conversations/conv-1/messages"] = () => refusal(409, "no_assistant_worker", "No worker can run the assistant: solo runs no headless session.");
    const { dock } = await open(state);
    const box = await within(dock).findByRole("textbox", { name: "Message to the assistant" });
    await userEvent.type(box, "Hello");
    await userEvent.click(within(dock).getByRole("button", { name: "Send to the assistant" }));
    expect((await within(dock).findByRole("alert")).textContent).toBe("No worker can run the assistant: solo runs no headless session.");
    expect((box as HTMLTextAreaElement).value).toBe("Hello");
  });
});

describe("the stream", () => {
  it("shows a message that reads as a yes with the line that a message never confirms a card", async () => {
    const { dock } = await open(state);
    await within(dock).findByRole("log", { name: "Messages with the assistant" });
    expect(within(dock).getByText("A message never confirms a card. The card above waits for your click on Confirm.")).toBeTruthy();
  });

  it("shows a message's delivery state as a badge", async () => {
    const { dock } = await open(state);
    await within(dock).findByRole("log", { name: "Messages with the assistant" });
    expect(within(dock).getAllByText("Queued").length).toBeGreaterThan(0);
  });

  it("shows an operation the assistant ran, with its actor and an Audit link", async () => {
    const { dock } = await open(state);
    const log = await within(dock).findByRole("log", { name: "Messages with the assistant" });
    expect(within(log).getByText("Create work")).toBeTruthy();
    expect(within(log).getAllByText(/as the assistant for member-1/).length).toBeGreaterThan(0);
    expect(within(log).getAllByRole("link", { name: "Audit" })[0]?.getAttribute("href")).toBe("/projects/default/audit?actor=delegate");
  });

  it("shows a citation as a chip that opens the record's view", async () => {
    const { dock } = await open(state);
    const log = await within(dock).findByRole("log", { name: "Messages with the assistant" });
    expect(within(log).getAllByRole("link", { name: "Work order wo-a" })[0]?.getAttribute("href")).toBe("/projects/default/work-order?id=wo-a");
    expect(within(log).getByRole("button", { name: "Copy harbor-api/src/retry.go:42 @a1b2c3d" })).toBeTruthy();
  });

  it("shows a card inline as the card Needs you shows, and confirms it in the conversation", async () => {
    const { sent, dock } = await open(state);
    const log = await within(dock).findByRole("log", { name: "Messages with the assistant" });
    const card = within(log).getByRole("article", { name: "Cancel work: wo-a" });
    expect(card.classList.contains("decision-compact")).toBe(true);
    expect(card.classList.contains("card-confirm")).toBe(true);
    expect(within(card).getByText("Exact operation")).toBeTruthy();
    await userEvent.click(within(card).getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    const decide = changes(sent)[0];
    expect(decide?.path).toMatch(/^\/v1\/approvals\/[^/]+\/decide$/);
    expect(decide?.body).toMatchObject({ allow: true, place: "conversation" });
    // The message that read as a yes decided nothing.
    expect(changes(sent).every((call) => call.path.endsWith("/decide"))).toBe(true);
  });

  it("shows a card decided elsewhere with its outcome, who, where, and when", async () => {
    const { dock } = await open(state);
    const log = await within(dock).findByRole("log", { name: "Messages with the assistant" });
    const card = within(log).getByRole("article", { name: "Cancel work: wo-b" });
    expect(within(card).getByRole("status").textContent).toMatch(/Confirmedby you from the CLI at \d{2}:\d{2}/);
    expect(within(card).queryByRole("button", { name: "Confirm" })).toBeNull();
  });

  it("offers Ask the assistant again on a void card, which fills the composer and sends nothing", async () => {
    const card = state.entries.find((entry) => entry.kind === "card" && entry.approval?.state === "pending");
    if (card?.approval) {
      card.approval = { ...card.approval, state: "void" };
    }
    const { sent, dock } = await open(state);
    const log = await within(dock).findByRole("log", { name: "Messages with the assistant" });
    await userEvent.click(within(within(log).getByRole("article", { name: "Cancel work: wo-a" })).getByRole("button", { name: "Ask the assistant again" }));
    const box = within(dock).getByRole("textbox", { name: "Message to the assistant" }) as HTMLTextAreaElement;
    expect(box.value).toBe('The card "Cancel work: wo-a" went void because its subject changed. Prepare it again if it still applies.');
    await waitFor(() => expect(document.activeElement).toBe(box));
    expect(changes(sent)).toEqual([]);
  });

  it("says the assistant is working, once", async () => {
    state.conversations = [aConversation({ actual_state: "working" })];
    const { dock } = await open(state);
    expect((await within(dock).findByText("The assistant is working…")).closest("[role=status]")).not.toBeNull();
    expect(within(dock).getAllByText("The assistant is working…")).toHaveLength(1);
  });
});

describe("switching the target", () => {
  const withLanes = () => {
    state.targets = {
      ...state.targets,
      planning: [lane({ lane: "L-91", work_order: "WO-233", title: "Plan alerts", kind: "plan", run_mode: "tmux" })],
      lanes: [lane(), lane({ lane: "L-90", work_order: "WO-221", title: "Rate limits" })],
    };
  };

  it("opens Switch target with T, grouped by kind, each row checked or not", async () => {
    withLanes();
    const { dock } = await open(state);
    await within(dock).findByRole("log", { name: "Messages with the assistant" });
    (document.activeElement as HTMLElement | null)?.blur();
    await userEvent.keyboard("t");
    const menu = await screen.findByRole("menu", { name: "Switch conversation target" });
    for (const group of ["Assistant", "Orchestrator", "Planning session", "Lane"]) {
      expect(within(menu).getByRole("group", { name: group })).toBeTruthy();
    }
    const rows = within(menu).getAllByRole("menuitemradio");
    expect(rows.map((row) => row.getAttribute("aria-checked"))).toEqual(["true", "false", "false", "false", "false"]);
    expect(within(menu).getByRole("menuitemradio", { name: /Orchestrator.*atlas.*stopped/ })).toBeTruthy();
  });

  it("switches to a lane: its header, its stream, its composer, announced politely", async () => {
    withLanes();
    const { dock } = await open(state);
    await within(dock).findByRole("log", { name: "Messages with the assistant" });
    await userEvent.click(within(dock).getByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    expect(await within(dock).findByRole("log", { name: "Event stream of lane L-88" })).toBeTruthy();
    expect(within(dock).getByText("L-88 · WO-219 · atlas · headless")).toBeTruthy();
    expect(within(dock).getByText("Works within its work order's generated rules. A message reaches it once and grants it nothing.")).toBeTruthy();
    expect(within(dock).getByRole("textbox", { name: "Message to lane L-88" })).toBeTruthy();
    expect(within(dock).getByRole("button", { name: "Send to lane L-88" })).toBeTruthy();
    expect(document.querySelector(".talk-announce")?.textContent).toBe("Talking to lane L-88");
  });

  it("sends a lane a message as an instruction that is recorded with that target", async () => {
    withLanes();
    const { sent, dock } = await open(state);
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    await userEvent.type(await within(dock).findByRole("textbox", { name: "Message to lane L-88" }), "Use two retries");
    await userEvent.click(within(dock).getByRole("button", { name: "Send to lane L-88" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/messages", body: { to: { lane: "L-88" }, kind: "instruction", body: "Use two retries" } });
  });

  it("styles as the member's own only what the signed-in member sent, not another member's message to the same lane", async () => {
    withLanes();
    const other = (id: string, member: string, body: string) => ({
      id,
      at: Date.now(),
      kind: "member" as const,
      from: { kind: "member", id: member, label: member === "member-2" ? "Robin Park" : member },
      body,
      message: { id, state: "queued" as const, created_at: Date.now(), kind: "instruction", body, to: { lane: "L-88", generation: 2 }, from: { kind: "member", id: member, seq: 1 } },
    });
    state.lane.entries.push(other("mine", "member-1", "Retry twice, please"), other("theirs", "member-2", "Use three"));
    const { dock } = await open(state);
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    const log = await within(dock).findByRole("log", { name: "Event stream of lane L-88" });
    const block = (text: string) => within(log).getByText(text).closest(".msg-block") as HTMLElement;
    expect(block("Retry twice, please").classList.contains("from-me")).toBe(true);
    expect(within(block("Retry twice, please")).getByText("You")).toBeTruthy();
    expect(block("Use three").classList.contains("from-me")).toBe(false);
    expect(within(block("Use three")).getByText("Robin Park")).toBeTruthy();
  });

  it("keeps one draft for each target", async () => {
    withLanes();
    const { dock } = await open(state);
    await userEvent.type(await within(dock).findByRole("textbox", { name: "Message to the assistant" }), "Which merges wait?");
    await userEvent.click(within(dock).getByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    const box = await within(dock).findByRole("textbox", { name: "Message to lane L-88" });
    expect((box as HTMLTextAreaElement).value).toBe("");
    await userEvent.type(box, "Use two retries");
    await userEvent.click(within(dock).getByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Assistant/ }));
    expect(((await within(dock).findByRole("textbox", { name: "Message to the assistant" })) as HTMLTextAreaElement).value).toBe("Which merges wait?");
  });

  it("holds through navigation: moving to another view changes nothing", async () => {
    withLanes();
    const { dock } = await open(state);
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    await within(dock).findByRole("log", { name: "Event stream of lane L-88" });
    await userEvent.click(screen.getByRole("link", { name: "Floor" }));
    await screen.findByRole("heading", { level: 1, name: "Floor" });
    expect(await screen.findByRole("log", { name: "Event stream of lane L-88" })).toBeTruthy();
  });

  it("shows an interactive lane's pane and no composer, with what waits on the member above it", async () => {
    withLanes();
    state.targets.lanes = [lane({ run_mode: "tmux" })];
    state.lane.entries.push({
      id: "approval-ap-1",
      at: Date.now(),
      kind: "request",
      approval: {
        id: "ap-1", lane_id: "L-88", work_order_id: "WO-219", generation: 2, kind: "tool", subject: "Bash", subject_digest: "sha256:abc", summary: "Run npm install", requested_by: "lane:L-88", state: "pending",
        command: "npm install --save-dev fast-check",
      },
    });
    const { dock } = await open(state);
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    expect(await within(dock).findByText("This session is interactive: type in the pane.")).toBeTruthy();
    expect(within(dock).queryByRole("textbox", { name: "Message to lane L-88" })).toBeNull();
    expect(await within(dock).findByRole("region", { name: "Pane of lane L-88" })).toBeTruthy();
    const waiting = await within(dock).findByRole("region", { name: "Waiting on you in this session" });
    expect(within(waiting).getByRole("article", { name: "Allow Bash?" }).classList.contains("decision-compact")).toBe(true);
  });

  it("shows a headless lane's approval request inline, decided in the conversation", async () => {
    withLanes();
    state.lane.entries.push({
      id: "approval-ap-1",
      at: Date.now(),
      kind: "request",
      approval: { id: "ap-1", lane_id: "L-88", work_order_id: "WO-219", generation: 2, kind: "tool", subject: "Bash", subject_digest: "sha256:abc", summary: "Run npm install", requested_by: "lane:L-88", state: "pending" },
    });
    const { sent, dock } = await open(state);
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    const log = await within(dock).findByRole("log", { name: "Event stream of lane L-88" });
    await userEvent.click(within(within(log).getByRole("article", { name: "Allow Bash?" })).getByRole("button", { name: "Allow this one call" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/approvals/ap-1/decide", body: { allow: true, digest: "sha256:abc", place: "conversation" } });
  });

  it("replaces the composer of a stopped orchestrator with why", async () => {
    const { dock } = await open(state);
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Orchestrator.*atlas/ }));
    expect(await within(dock).findByRole("log", { name: "Event stream of the orchestrator on atlas" })).toBeTruthy();
    expect(within(dock).getByText("The orchestrator on atlas is not running. Start it from Workers.")).toBeTruthy();
    expect(within(dock).queryByRole("textbox")).toBeNull();
  });

  it("sends a running orchestrator a message to its worker", async () => {
    state.targets.orchestrators = [{ ...(state.targets.orchestrators[0] as object), state: "running" } as never];
    const { sent, dock } = await open(state);
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Orchestrator.*atlas/ }));
    await userEvent.type(await within(dock).findByRole("textbox", { name: "Message to the orchestrator on atlas" }), "Claim first");
    await userEvent.click(within(dock).getByRole("button", { name: "Send to the orchestrator on atlas" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/messages", body: { to: { kind: "orchestrator", worker: "worker-atlas" }, kind: "instruction", body: "Claim first" } });
  });

  it("starts a stopped conversation on choosing the assistant in the switcher", async () => {
    withLanes();
    state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped" })];
    const { sent, dock } = await open(state);
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    await within(dock).findByRole("log", { name: "Event stream of lane L-88" });
    expect(changes(sent)).toEqual([]);
    await userEvent.click(within(dock).getByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Assistant/ }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe("/v1/conversations/conv-1/open");
  });

  it("switches to a planning session the assistant starts while the member is in its conversation", async () => {
    withLanes();
    const { dock } = await open(state);
    await within(dock).findByRole("log", { name: "Messages with the assistant" });
    state.entries = [
      ...state.entries,
      { id: "action-new", at: Date.now(), kind: "action", action: { operation: "lane.start", label: "Start a lane", subject: "work_order:WO-233", outcome: "ran", planning_lane: "L-91" } },
    ];
    // The stream follows the ledger: any event it hears is a reason to read again.
    window.dispatchEvent(new Event("online"));
    (await openLedgerStream()).emit();
    expect(await within(dock).findByText("This session is interactive: type in the pane.", {}, { timeout: 4000 })).toBeTruthy();
    expect(within(dock).getByText("The assistant started a planning session on WO-233 (lane L-91). You are now talking to it.")).toBeTruthy();
    expect(within(dock).getByText("WO-233 · L-91 · atlas")).toBeTruthy();
  });
});

// A request to start the session is made for one conversation of one project, and answered once.
describe("choosing a conversation that already runs", () => {
  const withLane = () => {
    state.targets = { ...state.targets, lanes: [lane()] };
  };
  const toLaneAndBack = async (dock: HTMLElement) => {
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    await within(dock).findByRole("log", { name: "Event stream of lane L-88" });
    await userEvent.click(within(dock).getByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Assistant/ }));
    await within(dock).findByRole("log", { name: "Messages with the assistant" });
  };

  it("starts nothing, and a Stop session after it is not undone by an open", async () => {
    withLane();
    state.answers["POST /v1/conversations/conv-1/stop"] = () => {
      state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped" })];
      return { status: 200, body: state.conversations[0] };
    };
    const { sent, dock } = await open(state);
    await toLaneAndBack(dock);
    expect(changes(sent)).toEqual([]);
    await userEvent.click(within(dock).getByRole("button", { name: "Conversation actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Stop session" }));
    expect(await within(dock).findByText("Stopped · type to resume")).toBeTruthy();
    await new Promise((done) => setTimeout(done, 300));
    expect(changes(sent).map((call) => call.path)).toEqual(["/v1/conversations/conv-1/stop"]);
  });

  it("starts nothing when the dock opens later, after the session idled out", async () => {
    withLane();
    const { sent, dock } = await open(state);
    await toLaneAndBack(dock);
    await userEvent.click(within(dock).getByRole("button", { name: "Close the conversation panel" }));
    state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped" })];
    await userEvent.keyboard(".");
    const reopened = await screen.findByRole("complementary", { name: "Conversation" });
    expect(await within(reopened).findByText("Stopped · type to resume")).toBeTruthy();
    await new Promise((done) => setTimeout(done, 300));
    expect(changes(sent)).toEqual([]);
  });
});

describe("the switcher's assistant row", () => {
  const onLane = async (dock: HTMLElement) => {
    await userEvent.click(await within(dock).findByRole("button", { name: "Switch target" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: /Lane.*L-88/ }));
    await within(dock).findByRole("log", { name: "Event stream of lane L-88" });
    await userEvent.click(within(dock).getByRole("button", { name: "Switch target" }));
    return screen.findByRole("menu", { name: "Switch conversation target" });
  };

  it("names the conversation choosing it opens, from another kind of target", async () => {
    state.targets = { ...state.targets, lanes: [lane()] };
    state.conversations = [aConversation({ name: "harbor-web merges" })];
    const { dock } = await open(state);
    const menu = await onLane(dock);
    expect(within(menu).getByRole("menuitemradio", { name: /Assistant.*harbor-web merges/ })).toBeTruthy();
    expect(within(menu).queryByText("New conversation")).toBeNull();
  });

  it("says there is no conversation yet, and choosing it only switches the target", async () => {
    state.targets = { ...state.targets, lanes: [lane()] };
    state.conversations = [];
    state.entries = [];
    const { sent, dock } = await open(state);
    const menu = await onLane(dock);
    await userEvent.click(within(menu).getByRole("menuitemradio", { name: /Assistant.*No conversation yet/ }));
    expect(await within(dock).findByRole("log", { name: "Messages with the assistant" })).toBeTruthy();
    await new Promise((done) => setTimeout(done, 300));
    expect(changes(sent)).toEqual([]);
  });
});
