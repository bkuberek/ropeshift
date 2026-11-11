import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

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
  await screen.findByRole("heading", { level: 1, name: "Lane" });
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
    await screen.findByRole("heading", { level: 1, name: "On hold" });
    expect(screen.queryByRole("link", { name: "Open the planning session" })).toBeNull();
  });
});
