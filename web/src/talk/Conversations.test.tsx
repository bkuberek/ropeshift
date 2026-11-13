import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import type { SessionTarget } from "../api/talk";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { here, refusal } from "../testing";
import { aConversation, serveTalk, talking } from "./serve";
import type { Talking } from "./serve";

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
  state.targets = { ...state.targets, lanes: [lane()], planning: [lane({ lane: "L-91", work_order: "WO-233", title: "Plan alerts", kind: "plan", run_mode: "tmux" })] };
});

describe("the Conversations view", () => {
  it("lists your conversations and the sessions you can talk to, beside the surface", async () => {
    state.conversations = [aConversation(), aConversation({ id: "conv-0", name: "Why is analytics blocked?", actual_state: "stopped", desired_state: "stopped", open_cards: 0, last_active_at: 1 })];
    serveTalk(state);
    openPage(here("/conversations"));
    const title = await screen.findByRole("heading", { level: 1, name: "Conversations" });
    expect(document.title).toBe("Conversations - ropeshift");
    const list = title.closest("section") as HTMLElement;
    const yours = await within(list).findByRole("list", { name: "Your conversations" });
    const rows = within(yours).getAllByRole("button");
    expect(rows).toHaveLength(2);
    expect(rows[0]?.textContent).toContain("Running");
    expect(rows[0]?.textContent).toContain("1 open card");
    expect(rows[1]?.textContent).toContain("Why is analytics blocked?");
    expect(rows[1]?.textContent).toContain("Stopped");
    const sessions = await within(list).findByRole("list", { name: "Sessions you can talk to" });
    expect(within(sessions).getAllByRole("button").map((row) => row.textContent)).toEqual([
      expect.stringContaining("Orchestratoratlas · serves default · stopped"),
      expect.stringContaining("Planning sessionWO-233 · L-91"),
      expect.stringContaining("LaneL-88 · WO-219 · headless"),
    ]);
    expect(await screen.findByRole("log", { name: "Messages with the assistant" })).toBeTruthy();
    // The surface here is the dock's: the dock stands aside.
    expect(screen.queryByRole("complementary", { name: "Conversation" })?.hasAttribute("hidden") ?? true).toBe(true);
  });

  it("is in the sidebar's Talk group, and G then C goes there", async () => {
    serveTalk(state);
    openPage(here("/roadmap"));
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    const nav = screen.getByRole("navigation", { name: "Views" });
    expect(within(nav).getByRole("link", { name: "Conversations" }).getAttribute("title")).toBe("Conversations (G then C)");
    expect(nav.querySelector('[id="nav-Talk"]')?.textContent).toBe("Talk");
    await userEvent.keyboard("gc");
    expect(await screen.findByRole("heading", { level: 1, name: "Conversations" })).toBeTruthy();
    expect(window.location.pathname).toBe(here("/conversations"));
    expect(window.location.search).toBe("");
  });

  it("chooses a session, which becomes the dock's target too, and a conversation, which starts its session", async () => {
    state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped" })];
    const sent = serveTalk(state);
    openPage(here("/conversations"));
    const list = (await screen.findByRole("heading", { level: 1, name: "Conversations" })).closest("section") as HTMLElement;
    await userEvent.click(await within(list).findByRole("button", { name: /Lane.*L-88/ }));
    expect(await screen.findByRole("log", { name: "Event stream of lane L-88" })).toBeTruthy();
    expect(within(list).getByRole("button", { name: /Lane.*L-88/ }).getAttribute("aria-current")).toBe("true");
    expect(changes(sent)).toEqual([]);
    await userEvent.click(within(list).getByRole("button", { name: /Cancel the retry work/ }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe("/v1/conversations/conv-1/open");
    // Leaving for another view, the dock holds the same target.
    await userEvent.click(screen.getByRole("link", { name: "Roadmap" }));
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    await userEvent.keyboard(".");
    expect(await screen.findByRole("log", { name: "Messages with the assistant" })).toBeTruthy();
  });

  it("starts a new conversation with New, saying the running one stopped first", async () => {
    const running = state.conversations[0];
    state.answers["POST /v1/conversations"] = () => ({ status: 201, body: { conversation: aConversation({ id: "conv-2", name: "New conversation" }), stopped: [running] } });
    const sent = serveTalk(state);
    openPage(here("/conversations"));
    await userEvent.click(await screen.findByRole("button", { name: "New" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe("/v1/conversations");
    expect(await screen.findByText("The running conversation stopped first.")).toBeTruthy();
  });

  it("says why a conversation cannot start, in the API's words, at the composer", async () => {
    state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped" })];
    state.answers["POST /v1/conversations/conv-1/open"] = () => refusal(409, "no_assistant_worker", "No worker can run the assistant: solo runs no headless session.");
    serveTalk(state);
    openPage(here("/conversations"));
    await userEvent.type(await screen.findByRole("textbox", { name: "Message to the assistant" }), "Hi");
    expect((await screen.findByRole("alert")).textContent).toBe("No worker can run the assistant: solo runs no headless session.");
  });
});

describe("the conversation's menu", () => {
  const open = async () => {
    const sent = serveTalk(state);
    openPage(here("/conversations"));
    await screen.findByRole("log", { name: "Messages with the assistant" });
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }));
    return { sent, menu: await screen.findByRole("menu", { name: "Conversation actions" }) };
  };

  it("holds Compact context, Clear context, Rename, Stop session, and Delete conversation", async () => {
    const { menu } = await open();
    expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Compact context", "Clear context", "Rename…", "Stop session", "Delete conversation…"]);
  });

  it("compacts and clears a running session, and stops it", async () => {
    const { sent, menu } = await open();
    await userEvent.click(within(menu).getByRole("menuitem", { name: "Compact context" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe("/v1/conversations/conv-1/compact");
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Clear context" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent)[1]?.path).toBe("/v1/conversations/conv-1/clear");
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Stop session" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(3));
    expect(changes(sent)[2]?.path).toBe("/v1/conversations/conv-1/stop");
  });

  it("renames, taking 1 to 80 characters", async () => {
    const { sent, menu } = await open();
    await userEvent.click(within(menu).getByRole("menuitem", { name: "Rename…" }));
    const dialog = await screen.findByRole("dialog", { name: "Rename conversation" });
    const name = within(dialog).getByRole("textbox", { name: /Name/ });
    expect((name as HTMLInputElement).value).toBe(state.conversations[0]?.name);
    fireEvent.change(name, { target: { value: "x".repeat(81) } });
    expect((within(dialog).getByRole("button", { name: "Rename" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(name, { target: { value: "Merge queue" } });
    await userEvent.click(within(dialog).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/conversations/conv-1/rename", body: { name: "Merge queue" } });
  });

  it("deletes after a dialog that names it and says what stays", async () => {
    const { sent, menu } = await open();
    await userEvent.click(within(menu).getByRole("menuitem", { name: "Delete conversation…" }));
    const dialog = await screen.findByRole("dialog", { name: `Delete “${state.conversations[0]?.name}”` });
    expect(dialog.textContent).toContain("its messages are deleted");
    expect(dialog.textContent).toContain("its actions are in the audit");
    expect(dialog.textContent).toContain("the cards it prepared stay");
    expect(document.activeElement?.textContent).not.toBe("Delete conversation");
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete conversation" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe("/v1/conversations/conv-1/delete");
  });

  it("disables what needs a running session when it is stopped, and says why", async () => {
    state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped" })];
    const { menu } = await open();
    const compact = within(menu).getByRole("menuitem", { name: "Compact context" });
    expect(compact.getAttribute("aria-disabled")).toBe("true");
    expect(compact.getAttribute("title")).toBe("The session is not running. Type to resume it.");
    expect(within(menu).getByRole("menuitem", { name: "Stop session" }).getAttribute("aria-disabled")).toBe("true");
  });
});

describe("the palette", () => {
  it("offers a new conversation, the target switcher, and Go to Conversations among its actions", async () => {
    serveTalk(state);
    openPage(here("/roadmap"));
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.keyboard(">conversation");
    for (const name of [/New assistant conversation/, /Switch conversation target/, /Go to Conversations/]) {
      expect(within(palette).getByRole("option", { name })).toBeTruthy();
    }
    await userEvent.click(within(palette).getByRole("option", { name: /Switch conversation target/ }));
    expect(await screen.findByRole("menu", { name: "Switch conversation target" })).toBeTruthy();
  });

  it("starts a new conversation from its action", async () => {
    const sent = serveTalk(state);
    openPage(here("/roadmap"));
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.keyboard(">new assistant");
    await userEvent.click(within(await screen.findByRole("dialog", { name: "Command palette" })).getByRole("option", { name: /New assistant conversation/ }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe("/v1/conversations");
    expect(await screen.findByRole("complementary", { name: "Conversation" })).toBeTruthy();
  });

  it("finds a conversation among the records and opens it in the surface", async () => {
    state.answers["GET /v1/views/search?q=merge"] = () => ({ status: 200, body: { as_of: 1, revision: 1, query: "merge", records: [{ kind: "conversation", id: "conv-1", title: "harbor-web merges", state: "running" }] } });
    serveTalk(state);
    openPage(here("/roadmap"));
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.keyboard("merge");
    const option = await screen.findByRole("option", { name: /harbor-web merges/ });
    expect(option.textContent).toContain("Conversation");
    await userEvent.click(option);
    expect(await screen.findByRole("heading", { level: 1, name: "Conversations" })).toBeTruthy();
    expect(await screen.findByRole("log", { name: "Messages with the assistant" })).toBeTruthy();
  });
});
