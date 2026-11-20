import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { paneKeys } from "../api/client";
import type { InProgress, OnHold } from "../api/types";
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

describe("a lane's pane, scrolled and sized", () => {
  const named = { name: `Screen of lane ${lane}` };

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // geometry stands for the browser's layout, which jsdom has none of: how tall the screen's text is, and
  // how tall the screen shows it.
  function geometry(sizes: { scrollHeight: number; clientHeight: number }) {
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(() => sizes.scrollHeight);
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(() => sizes.clientHeight);
    return sizes;
  }

  async function scrolledPane() {
    const sizes = geometry({ scrollHeight: 1000, clientHeight: 200 });
    serve();
    openPage(page);
    const stream = await opened();
    show(stream, "line\n".repeat(60) + "the newest line");
    return { sizes, stream, log: screen.getByRole("log", named) };
  }

  it("opens scrolled to the newest line", async () => {
    const { log } = await scrolledPane();
    expect(log.scrollTop).toBe(800);
  });

  it("stays at the newest line as output arrives", async () => {
    const { sizes, stream, log } = await scrolledPane();
    sizes.scrollHeight = 1500;
    show(stream, "line\n".repeat(90) + "a newer line");
    expect(log.scrollTop).toBe(1300);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
  });

  it("stays where the member scrolled up to as output arrives, and offers Jump to latest", async () => {
    const { sizes, stream, log } = await scrolledPane();
    log.scrollTop = 100;
    fireEvent.scroll(log);
    const jump = await screen.findByRole("button", { name: "Jump to latest" });
    sizes.scrollHeight = 1500;
    show(stream, "line\n".repeat(90) + "a newer line");
    expect(log.scrollTop).toBe(100);
    await userEvent.setup().click(jump);
    expect(log.scrollTop).toBe(1300);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
  });

  it("follows the newest line again once the member scrolls back to the bottom", async () => {
    const { sizes, stream, log } = await scrolledPane();
    log.scrollTop = 100;
    fireEvent.scroll(log);
    await screen.findByRole("button", { name: "Jump to latest" });
    log.scrollTop = 800;
    fireEvent.scroll(log);
    await waitFor(() => expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull());
    sizes.scrollHeight = 1500;
    show(stream, "line\n".repeat(90) + "a newer line");
    expect(log.scrollTop).toBe(1300);
  });

  it("fills the height under the page's header down to the bottom of the view", async () => {
    geometry({ scrollHeight: 0, clientHeight: 900 });
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
      const box = (top: number, bottom: number) => ({ top, bottom, left: 0, right: 0, width: 0, height: bottom - top, x: 0, y: top, toJSON: () => ({}) });
      if (this.classList.contains("main")) {
        return box(60, 960);
      }
      if (this.classList.contains("pane-screen")) {
        return box(300, 500);
      }
      if (this.classList.contains("pane")) {
        return box(280, 700);
      }
      return box(0, 0);
    });
    serve();
    openPage(page);
    show(await opened(), "a screen");
    // 900 visible, less the 240 above the screen, the 200 of controls under it, and a 16 gap.
    expect(screen.getByRole("log", named).style.height).toBe("444px");
  });

  it("takes the whole view in Full view, and leaves it on Escape", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "a screen");
    const user = userEvent.setup();
    const region = screen.getByRole("region", { name: `Pane of lane ${lane}` });
    const full = screen.getByRole("button", { name: "Full view" });
    await user.click(full);
    expect(full.getAttribute("aria-pressed")).toBe("true");
    expect(region.classList.contains("is-full")).toBe(true);
    await user.keyboard("{Escape}");
    expect(full.getAttribute("aria-pressed")).toBe("false");
    expect(region.classList.contains("is-full")).toBe(false);
    expect(typed).toEqual([]);
  });

  it("sends Escape to the session from the empty line field, and stays in Full view", async () => {
    const typed = serve();
    openPage(page);
    show(await opened(), "a screen");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Full view" }));
    await user.click(screen.getByRole("textbox", { name: "Type into the session" }));
    await user.keyboard("{Escape}");
    await waitFor(() => expect(typed.map((sent) => sent.body)).toEqual([{ key: "Escape" }]));
    expect(screen.getByRole("region", { name: `Pane of lane ${lane}` }).classList.contains("is-full")).toBe(true);
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
