import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { paneKeys } from "../api/client";
import type { InProgress, OnHold } from "../api/types";
import { openPage } from "../opening";
import { FakeEvents, golden, here, refusal, serveViews, withStyles } from "../testing";
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

  it("presses each named key, and leaves the keyboard in the field", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "> Yes\n  No");
    const user = userEvent.setup();
    const field = screen.getByRole("textbox", { name: "Type into the session" });
    await user.click(field);
    const keys = screen.getByRole("group", { name: "Press a key" });
    for (const key of paneKeys) {
      await user.click(within(keys).getByRole("button", { name: key }));
      expect(document.activeElement).toBe(field);
    }
    await waitFor(() => expect(typed).toHaveLength(paneKeys.length));
    expect(typed.map((sent) => sent.body)).toEqual(paneKeys.map((key) => ({ key })));
  });

  it("sends Enter, Escape, and the arrows from an empty field", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "> Yes\n  No");
    const user = userEvent.setup();
    await user.click(screen.getByRole("textbox", { name: "Type into the session" }));
    await user.keyboard("{ArrowDown}{ArrowUp}{Escape}{Enter}");
    await waitFor(() => expect(typed).toHaveLength(4));
    expect(typed.map((sent) => sent.body)).toEqual([{ key: "Down" }, { key: "Up" }, { key: "Escape" }, { key: "Enter" }]);
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

describe("a lane's pane's scrollback", () => {
  const named = { name: `Screen of lane ${lane}` };
  const scrollbackPath = `/v1/panes/${encodeURIComponent(lane)}/scrollback`;

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // old is the history's lines from one to another, oldest first, each ending in a newline.
  const old = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, at) => `old ${from + at}\n`).join("");

  // serveHistory answers each read of the scrollback with the newest lines of a history of `size` lines.
  function serveHistory(size: number) {
    const asked: number[] = [];
    serve((call) => {
      if (call.method === "GET" && call.path.startsWith(`${scrollbackPath}?`)) {
        const lines = Number(new URL(call.url, "http://ropeshift").searchParams.get("lines"));
        asked.push(lines);
        return { status: 200, body: { lane_id: lane, generation: 1, lines: size === 0 ? "" : old(Math.max(1, size - lines + 1), size), size, limit: 2000 } };
      }
      return undefined;
    });
    return asked;
  }

  // lineHeights stands for the browser's layout: each line of the screen's text is 18 pixels tall, and the
  // screen shows 360 of them.
  function lineHeights() {
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(function (this: HTMLElement) {
      return (this.textContent ?? "").split("\n").length * 18;
    });
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(() => 360);
  }

  // The screen is the pane's scroller, held to a height, so it opens at the newest line and a scroll to its
  // top reads older lines.
  it("scrolls in the screen itself on the lane page", async () => {
    withStyles();
    serveHistory(2);
    openPage(page);
    show(await opened(), "the screen");
    const log = screen.getByRole("log", named);
    expect(getComputedStyle(log).overflow).toBe("auto");
    expect(getComputedStyle(log).maxHeight).toMatch(/px$/);
    expect(["auto", "scroll"]).not.toContain(getComputedStyle(screen.getByRole("region", { name: `Pane of lane ${lane}` })).overflowY);
  });

  it("shows the scrollback above the screen, at the newest line, and says how much there is", async () => {
    lineHeights();
    const asked = serveHistory(2);
    openPage(page);
    show(await opened(), "the screen");
    const log = screen.getByRole("log", named);
    await waitFor(() => expect(log.textContent).toBe("old 1\nold 2\nthe screen"));
    expect(asked).toEqual([200]);
    expect(screen.getByText("Showing 2 of the 2,000 lines tmux keeps")).toBeTruthy();
    expect(log.scrollTop).toBe(3 * 18 - 360);
  });

  it("loads older lines as the member scrolls up, and keeps their place", async () => {
    lineHeights();
    const asked = serveHistory(500);
    openPage(page);
    show(await opened(), "the screen");
    const log = screen.getByRole("log", named);
    await waitFor(() => expect(screen.getByText("Showing 200 of the 2,000 lines tmux keeps")).toBeTruthy());
    log.scrollTop = 0;
    fireEvent.scroll(log);
    await waitFor(() => expect(screen.getByText("Showing 400 of the 2,000 lines tmux keeps")).toBeTruthy());
    expect(log.scrollTop).toBe(200 * 18);
    expect(log.textContent?.startsWith("old 101\n")).toBe(true);
    log.scrollTop = 0;
    fireEvent.scroll(log);
    await waitFor(() => expect(screen.getByText("Showing 500 of the 2,000 lines tmux keeps")).toBeTruthy());
    log.scrollTop = 0;
    fireEvent.scroll(log);
    expect(asked).toEqual([200, 400, 800]);
  });

  it("keeps a colour the scrollback left open out of the screen", async () => {
    serve((call) => (call.method === "GET" && call.path.startsWith(`${scrollbackPath}?`) ? { status: 200, body: { lane_id: lane, generation: 1, lines: "\x1b[31mred line\n", size: 1, limit: 2000 } } : undefined));
    openPage(page);
    show(await opened(), "the screen");
    const log = screen.getByRole("log", named);
    await waitFor(() => expect(log.textContent).toBe("red line\nthe screen"));
    expect(within(log).getByText("the screen").className).toBe("");
  });

  it("says when the scrollback could not be read", async () => {
    serve((call) => (call.method === "GET" && call.path.startsWith(`${scrollbackPath}?`) ? refusal(409, "no_pane", "lane x has no pane for generation 1") : undefined));
    openPage(page);
    show(await opened(), "the screen");
    expect(await screen.findByText("The scrollback could not be read: lane x has no pane for generation 1")).toBeTruthy();
    expect(screen.queryByText("Reading the scrollback.")).toBeNull();
  });

  it("copies the whole scrollback and the screen with Copy all", async () => {
    const asked = serveHistory(500);
    openPage(page);
    show(await opened(), "the \x1b[1mscreen\x1b[0m");
    const user = userEvent.setup();
    await waitFor(() => expect(asked).toEqual([200]));
    await user.click(screen.getByRole("button", { name: "Copy all" }));
    await waitFor(() => expect(asked).toEqual([200, 2000]));
    await waitFor(async () => expect(await navigator.clipboard.readText()).toBe(old(1, 500) + "the screen"));
    expect(screen.getByText("Copied the scrollback and the screen: 501 lines.")).toBeTruthy();
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
