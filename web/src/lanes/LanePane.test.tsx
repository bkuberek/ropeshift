import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { InProgress, OnHold, ShopFloor } from "../api/types";
import { openPage } from "../opening";
import { FakeEvents, golden, here, refusal, serveViews } from "../testing";
import type { Sent } from "../testing";

const lane = "lane:plan 1";
const page = `/lane?id=${encodeURIComponent(lane)}`;
const panePath = `/v1/panes/${encodeURIComponent(lane)}`;

function paneStream(): FakeEvents {
  const found = FakeEvents.opened.find((stream) => stream.path === panePath);
  if (!found) {
    throw new Error(`no stream of ${panePath} among ${FakeEvents.opened.map((stream) => stream.path).join(", ")}`);
  }
  return found;
}

// serve answers the views, and keeps every input the page types into the pane.
function serve(answer?: (sent: Sent) => { status: number; body?: unknown } | undefined) {
  const typed: Sent[] = [];
  serveViews((call) => {
    const own = answer?.(call);
    if (own) {
      return own;
    }
    if (call.method === "POST" && call.path === `${panePath}/input`) {
      typed.push(call);
      return { status: 204 };
    }
    return undefined;
  });
  return typed;
}

async function opened() {
  await screen.findByRole("heading", { level: 1, name: /^Lane / });
  await waitFor(() => paneStream());
  return paneStream();
}

// paneStatus is the pane's own status line, apart from the page's.
function paneStatus(): HTMLElement {
  return within(screen.getByRole("region", { name: `Pane of lane ${lane}` })).getByRole("status");
}

// chordsOf reads the chords the page sent, in order, however it grouped them into requests.
function chordsOf(typed: Sent[]): string[] {
  return typed.flatMap((sent) => ((sent.body as { key?: string }).key ?? "").split(" ").filter(Boolean));
}

function show(stream: FakeEvents, text: string, generation = 1) {
  act(() => stream.send("screen", { lane_id: lane, generation, screen: text }));
}

describe("a lane's pane", () => {
  it("is as tall as the screen's lines, without the blank rows a terminal keeps below them", async () => {
    serve();
    openPage(page);
    const stream = await opened();
    show(stream, "● Read(goal.md)\n  └ Read 9 lines\n\n❯ \n" + "\n".repeat(40) + "    \n\x1b[0m\n");
    expect(screen.getByRole("log", { name: `Screen of lane ${lane}` }).textContent).toBe("● Read(goal.md)\n  └ Read 9 lines\n\n❯ ");
  });

  it("shows the lane's screen with its colours, in a region a screen reader can find", async () => {
    serve();
    openPage(page);
    const stream = await opened();
    show(stream, "\x1b[1mPlanning\x1b[0m: what is the plan \x1b[31mfor\x1b[39m?");
    const region = await screen.findByRole("log", { name: `Screen of lane ${lane}` });
    expect(region.textContent).toBe("Planning: what is the plan for?");
    expect(within(region).getByText("Planning").className).toBe("sgr-bold");
    expect(within(region).getByText("for").className).toBe("sgr-refuse");
    expect(paneStatus().textContent).toMatch(/generation 1/i);
  });

  it("says a terminal attached with lane attach sees the same conversation", async () => {
    serve();
    openPage(page);
    await opened();
    expect(screen.getByText(`ropeshift lane attach ${lane}`)).toBeTruthy();
  });

  it("types a line and presses Enter after it, and keeps the keyboard in the field", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "What is the plan for?");
    const user = userEvent.setup();
    const field = screen.getByRole("textbox", { name: "Type into the session" });
    await user.type(field, "A planning tool{Enter}");
    await waitFor(() => expect(typed).toHaveLength(1));
    expect(typed[0]?.body).toEqual({ text: "A planning tool", enter: true });
    expect(typed[0]?.csrf).not.toBeNull();
    expect((field as HTMLInputElement).value).toBe("");
    expect(document.activeElement).toBe(field);
  });

  it("lists Send keys in the command palette, which puts the keyboard in the field and sends nothing", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "What is the plan for?");
    const user = userEvent.setup();
    await user.keyboard("{Control>}k{/Control}");
    await screen.findByRole("dialog", { name: "Command palette" });
    await user.keyboard("send keys");
    await user.click(await screen.findByRole("option", { name: new RegExp(`^Send keys to the pane of lane ${lane}`) }));
    const field = screen.getByRole("textbox", { name: "Type into the session" });
    await waitFor(() => expect(document.activeElement).toBe(field));
    expect(typed).toEqual([]);
  });

  it("types text with no Enter when the person asks for none", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "What is the plan for?");
    const user = userEvent.setup();
    await user.click(screen.getByRole("checkbox", { name: "Press Enter after the text" }));
    await user.type(screen.getByRole("textbox", { name: "Type into the session" }), "half a line");
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(typed).toHaveLength(1));
    expect(typed[0]?.body).toEqual({ text: "half a line", enter: false });
  });

  it("presses each key of the key row, and leaves the keyboard in the field", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "> Yes\n  No");
    const user = userEvent.setup();
    const field = screen.getByRole("textbox", { name: "Type into the session" });
    await user.click(field);
    const keys = screen.getByRole("group", { name: "Press a key" });
    const row = ["Esc", "Tab", "Shift-Tab", "Enter", "Ctrl-C", "Ctrl-O", "Up", "Down", "Left", "Right"];
    for (const key of row) {
      await user.click(within(keys).getByRole("button", { name: key }));
      expect(document.activeElement).toBe(field);
    }
    const sent = ["Escape", "Tab", "Shift-Tab", "Enter", "Ctrl-C", "Ctrl-O", "Up", "Down", "Left", "Right"];
    await waitFor(() => expect(chordsOf(typed)).toEqual(sent));
  });

  it("sends Enter, Escape, and the arrows from an empty field", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "> Yes\n  No");
    const user = userEvent.setup();
    await user.click(screen.getByRole("textbox", { name: "Type into the session" }));
    await user.keyboard("{ArrowDown}{ArrowUp}{Escape}{Enter}");
    await waitFor(() => expect(chordsOf(typed)).toEqual(["Down", "Up", "Escape", "Enter"]));
  });

  it("says when the lane is parked, and sends nothing while it is", async () => {
    const typed = serve();
    openPage(page);
    const stream = await opened();
    show(stream, "a screen");
    act(() => stream.send("lane", { lane_id: lane, generation: 1, state: "parked" }));
    await waitFor(() => expect(paneStatus().textContent).toMatch(/parked/i));
    const user = userEvent.setup();
    await user.type(screen.getByRole("textbox", { name: "Type into the session" }), "hello{Enter}");
    expect((screen.getByRole("button", { name: "Send" }) as HTMLButtonElement).disabled).toBe(true);
    expect((within(screen.getByRole("group", { name: "Press a key" })).getByRole("button", { name: "Enter" }) as HTMLButtonElement).disabled).toBe(true);
    expect(typed).toHaveLength(0);
  });

  it("follows the lane to its next generation", async () => {
    serve();
    openPage(page);
    const stream = await opened();
    show(stream, "generation one");
    act(() => stream.send("lane", { lane_id: lane, generation: 1, state: "working" }));
    show(stream, "generation two", 2);
    expect((await screen.findByRole("log", { name: `Screen of lane ${lane}` })).textContent).toBe("generation two");
    expect(paneStatus().textContent).toMatch(/generation 2/i);
  });

  it("stops following a lane that ended", async () => {
    serve();
    openPage(page);
    const stream = await opened();
    act(() => stream.send("lane", { lane_id: lane, generation: 1, state: "ended" }));
    await waitFor(() => expect(paneStatus().textContent).toMatch(/ended/i));
    expect(stream.closed).toBe(true);
  });

  it("says why the server refused the pane", async () => {
    serve((call) => (call.method === "GET" && call.path === panePath ? refusal(409, "no_pane", "lane x has no pane for generation 1") : undefined));
    openPage(page);
    const stream = await opened();
    act(() => stream.drop());
    expect((await screen.findByRole("alert")).textContent).toBe("lane x has no pane for generation 1");
  });

  it("shows what a program printed as text and nothing else", async () => {
    serve();
    openPage(page);
    show(await opened(), `<img src=x onerror=alert(1)>\x1b]8;;https://example.com\x1b\\here\x1b]8;;\x1b\\\x1b[2J`);
    const region = await screen.findByRole("log", { name: `Screen of lane ${lane}` });
    expect(region.textContent).toBe("<img src=x onerror=alert(1)>here");
    expect(region.querySelector("img, a")).toBeNull();
  });
});

describe("a lane's pane that holds the keys", () => {
  const named = { name: /^Screen of lane lane:plan 1/ };

  async function captured() {
    const typed = serve();
    openPage(page);
    show(await opened(), "Accept the plan? (Tab to accept)");
    const user = userEvent.setup();
    await user.click(screen.getByRole("log", named));
    const held = screen.getByRole("application", named);
    return { typed, user, held };
  }

  it("never holds the keys when the page opens", async () => {
    serve();
    openPage(page);
    show(await opened(), "a screen");
    expect(screen.getByRole("log", named)).toBeTruthy();
    expect(screen.queryByText(/Keys go to the session/)).toBeNull();
  });

  it("takes the keys on a click, names the release key, and sends every key to the session", async () => {
    const { typed, user, held } = await captured();
    expect(screen.getByText(/Keys go to the session/).closest(".pane-capture")?.textContent).toMatch(/Ctrl\s*\]\s*releases/);
    expect(held.getAttribute("aria-label")).toContain("Ctrl-] releases them");
    await user.keyboard("{Tab}{Shift>}{Tab}{/Shift}{Escape}{Control>}o{/Control}{Control>}x{/Control}{Control>}s{/Control}y?");
    expect(document.activeElement).toBe(held);
    await waitFor(() => expect(chordsOf(typed)).toEqual(["Tab", "Shift-Tab", "Escape", "Ctrl-o", "Ctrl-x", "Ctrl-s", "y", "?"]));
    expect(screen.queryByRole("dialog", { name: "Keyboard shortcuts" })).toBeNull();
  });

  it("gives the keys back on Ctrl-], with focus left on the screen, so Tab moves on from there", async () => {
    const { typed, user, held } = await captured();
    await user.keyboard("{Control>}]{/Control}");
    expect(screen.getByRole("log", named)).toBe(held);
    expect(screen.queryByText(/Keys go to the session/)).toBeNull();
    expect(document.activeElement).toBe(held);
    await user.tab();
    expect(document.activeElement).not.toBe(held);
    expect(typed).toEqual([]);
  });

  it("gives the keys back on Release and on a click outside the pane", async () => {
    const { user, held } = await captured();
    await user.click(screen.getByRole("button", { name: "Release" }));
    expect(screen.getByRole("log", named)).toBe(held);
    expect(document.activeElement).toBe(held);
    await user.click(held);
    expect(screen.getByRole("application", named)).toBe(held);
    await user.click(screen.getByRole("heading", { level: 1, name: /^Lane / }));
    expect(screen.getByRole("log", named)).toBe(held);
  });

  it("takes the keys on Enter while the screen has focus, and sends nothing for that Enter", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "a screen");
    const user = userEvent.setup();
    const log = screen.getByRole("log", named);
    act(() => log.focus());
    await user.keyboard("{Enter}");
    expect(screen.getByRole("application", named)).toBe(log);
    await user.keyboard("{Enter}");
    await waitFor(() => expect(chordsOf(typed)).toEqual(["Enter"]));
  });

  it("takes no keys while the lane is parked", async () => {
    serve();
    openPage(page);
    const stream = await opened();
    show(stream, "a screen");
    act(() => stream.send("lane", { lane_id: lane, generation: 1, state: "parked" }));
    await waitFor(() => expect(paneStatus().textContent).toMatch(/parked/i));
    const user = userEvent.setup();
    await user.click(screen.getByRole("log", named));
    expect(screen.queryByRole("application", named)).toBeNull();
  });

  it("sends any sequence the member types in Keys", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "a screen");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Keys…" }));
    const composer = screen.getByRole("group", { name: "Compose keys" });
    const keys = within(composer).getByRole("textbox", { name: "Keys to send" });
    expect(document.activeElement).toBe(keys);
    await user.type(keys, "Ctrl-X Ctrl-S");
    expect([...composer.querySelectorAll("kbd")].map((cap) => cap.textContent)).toEqual(["Ctrl", "X", "Ctrl", "S"]);
    await user.click(within(composer).getByRole("button", { name: "Send keys" }));
    await waitFor(() => expect(typed.map((sent) => sent.body)).toEqual([{ key: "Ctrl-X Ctrl-S" }]));
    expect((keys as HTMLInputElement).value).toBe("");
  });

  it("records the keys a member presses in Keys, until Ctrl-]", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "a screen");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Keys…" }));
    const composer = screen.getByRole("group", { name: "Compose keys" });
    const record = within(composer).getByRole("button", { name: "Record" });
    await user.click(record);
    expect(record.getAttribute("aria-pressed")).toBe("true");
    expect(within(composer).getByText(/Ctrl-\] stops the recording/)).toBeTruthy();
    await user.keyboard("{Control>}x{/Control}{Shift>}{Tab}{/Shift}{Escape}{Control>}]{/Control}");
    expect(record.getAttribute("aria-pressed")).toBe("false");
    expect((within(composer).getByRole("textbox", { name: "Keys to send" }) as HTMLInputElement).value).toBe("Ctrl-x Shift-Tab Escape");
    await user.keyboard("{Enter}");
    await waitFor(() => expect(typed.map((sent) => sent.body)).toEqual([{ key: "Ctrl-x Shift-Tab Escape" }]));
  });

  it("refuses a sequence longer than the server takes before it sends it", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "a screen");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Keys…" }));
    const composer = screen.getByRole("group", { name: "Compose keys" });
    await user.type(within(composer).getByRole("textbox", { name: "Keys to send" }), "a b c d e f g h i");
    expect(within(composer).getByRole("alert").textContent).toMatch(/at most 8 chords/);
    expect((within(composer).getByRole("button", { name: "Send keys" }) as HTMLButtonElement).disabled).toBe(true);
    expect(typed).toEqual([]);
  });
});

describe("a lane's facts", () => {
  it("name the agent CLI version the lane's worker checks, beside the lane's state", async () => {
    const shown = "lane-wo-effect";
    const checkingWords = "Checking Claude Code 2.1.400: this lane runs on documented surfaces until the check passes; a respawn then picks up the qualified surfaces";
    serveViews((call) => {
      if (call.method === "GET" && call.path === "/v1/views/shop-floor") {
        const floor = golden<ShopFloor>("shop-floor");
        const row = floor.lanes.find((line) => line.id === shown);
        if (!row) {
          throw new Error(`the golden holds no lane ${shown}`);
        }
        row.checking = { driver: "claude-code", version: "2.1.400", words: checkingWords };
        return { status: 200, body: floor };
      }
      return undefined;
    });
    openPage(`/lane?id=${shown}`);
    const facts = await screen.findByRole("complementary", { name: "Lane facts" });
    const state = (await within(facts).findByText("State")).closest(".kv-row") as HTMLElement;
    expect(within(state).getByText(checkingWords).className).toContain("checking");
  });

  it("say beside Degraded mode, in a sentence the chip points to, when the lane restarts on qualified surfaces", async () => {
    const shown = "lane-wo-effect";
    serveViews((call) => {
      if (call.method === "GET" && call.path === "/v1/views/shop-floor") {
        const floor = golden<ShopFloor>("shop-floor");
        const row = floor.lanes.find((line) => line.id === shown);
        if (!row) {
          throw new Error(`the golden holds no lane ${shown}`);
        }
        row.conditions = ["degraded_mode"];
        return { status: 200, body: floor };
      }
      return undefined;
    });
    openPage(`/lane?id=${shown}`);
    const facts = await screen.findByRole("complementary", { name: "Lane facts" });
    const state = (await within(facts).findByText("State")).closest(".kv-row") as HTMLElement;
    const chip = within(state).getByText("Degraded mode");
    const sentence = within(state).getByText("Runs on documented surfaces until it restarts on a qualified version.");
    expect(sentence.id).not.toBe("");
    expect(chip.getAttribute("aria-describedby")).toBe(sentence.id);
    expect(chip.nextElementSibling).toBe(sentence);
  });

  it("name no check on a lane whose worker checks nothing", async () => {
    serveViews();
    openPage("/lane?id=lane-wo-effect");
    const facts = await screen.findByRole("complementary", { name: "Lane facts" });
    await within(facts).findByText("State");
    expect(within(facts).queryByText(/^Checking /)).toBeNull();
  });
});

describe("a lane's pane, copied, selected, and searched", () => {
  const named = { name: `Screen of lane ${lane}` };
  const text = "Should the berths page list berths that are \x1b[1mclosed\x1b[0m?\nA Closed berth has no sailings.\nclosed: 2 of 14";

  it("copies the screen as text", async () => {
    serve();
    openPage(page);
    show(await opened(), text);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Copy screen" }));
    expect(await navigator.clipboard.readText()).toBe("Should the berths page list berths that are closed?\nA Closed berth has no sailings.\nclosed: 2 of 14");
    expect(within(screen.getByRole("toolbar", { name: "Pane tools" })).getByText("Copied the screen: 3 lines.")).toBeTruthy();
  });

  it("selects the pane's text and nothing else, from Select all and from the shortcut on the screen", async () => {
    serve();
    openPage(page);
    show(await opened(), text);
    const user = userEvent.setup();
    const log = screen.getByRole("log", named);
    await user.click(screen.getByRole("button", { name: "Select all" }));
    expect(window.getSelection()?.toString()).toBe(log.textContent);
    expect(document.activeElement).toBe(log);
    window.getSelection()?.removeAllRanges();
    await user.keyboard("{Control>}a{/Control}");
    expect(window.getSelection()?.toString()).toBe(log.textContent);
  });

  it("searches the pane's text, one match after another, in case or apart from it", async () => {
    serve();
    openPage(page);
    show(await opened(), text);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Search" }));
    const search = screen.getByRole("search", { name: "Search the pane" });
    const field = within(search).getByRole("searchbox", { name: "Find in the pane" });
    expect(document.activeElement).toBe(field);
    await user.type(field, "closed");
    const log = screen.getByRole("log", named);
    expect(log.querySelectorAll("mark")).toHaveLength(3);
    expect(search.querySelector(".pane-count")?.textContent).toBe("1 of 3");
    expect(log.querySelector("mark.is-current")?.textContent).toBe("closed");
    await user.keyboard("{Enter}");
    expect(search.querySelector(".pane-count")?.textContent).toBe("2 of 3");
    expect(log.querySelector("mark.is-current")?.textContent).toBe("Closed");
    await user.keyboard("{Shift>}{Enter}{/Shift}");
    expect(search.querySelector(".pane-count")?.textContent).toBe("1 of 3");
    await user.click(within(search).getByRole("button", { name: "Match case" }));
    expect(search.querySelector(".pane-count")?.textContent).toBe("1 of 2");
    expect(log.textContent).toBe("Should the berths page list berths that are closed?\nA Closed berth has no sailings.\nclosed: 2 of 14");
    await user.click(field);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("search", { name: "Search the pane" })).toBeNull();
    expect(log.querySelectorAll("mark")).toHaveLength(0);
    expect(document.activeElement).toBe(log);
  });

  it("opens the search from the find shortcut on the screen", async () => {
    serve();
    openPage(page);
    show(await opened(), text);
    const user = userEvent.setup();
    act(() => screen.getByRole("log", named).focus());
    await user.keyboard("{Control>}f{/Control}");
    expect(document.activeElement).toBe(screen.getByRole("searchbox", { name: "Find in the pane" }));
  });
});

describe("a planning session's work order", () => {
  it("leads to its lane's pane from In progress and On hold", async () => {
    const running = golden<InProgress>("in-progress");
    const first = running.items.find((line) => line.lane);
    if (!first?.lane) {
      throw new Error("the golden holds no running lane");
    }
    first.work_order.kind = "plan";
    serveViews((call) => (call.path.startsWith("/v1/views/in-progress") ? { status: 200, body: running } : undefined));
    openPage("/in-progress");
    const link = await screen.findByRole("link", { name: "Open the planning session" });
    expect(link.getAttribute("href")).toBe(here(`/lane?id=${encodeURIComponent(first.lane.id)}`));
  });

  it("leads nowhere for other work", async () => {
    const held = golden<OnHold>("on-hold");
    for (const line of held.items) {
      line.work_order.kind = "change";
    }
    serveViews((call) => (call.path.startsWith("/v1/views/on-hold") ? { status: 200, body: held } : undefined));
    openPage("/on-hold");
    await screen.findByRole("heading", { level: 1, name: "Work orders" });
    await screen.findByRole("table");
    expect(screen.queryByRole("link", { name: "Open the planning session" })).toBeNull();
  });
});
