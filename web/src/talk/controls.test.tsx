import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import type { SessionTarget } from "../api/talk";
import { openPage } from "../opening";
import { here } from "../testing";
import { serveTalk, talking } from "./serve";
import type { Talking } from "./serve";

const session = (lane: string, over: Partial<SessionTarget> = {}): SessionTarget => ({
  lane,
  work_order: "wo-x",
  title: "Some work",
  kind: "change",
  worker: "worker-a",
  worker_name: "solo",
  generation: 1,
  run_mode: "headless",
  state: "working",
  ...over,
});

let state: Talking;
beforeEach(() => {
  state = talking();
  state.targets = {
    ...state.targets,
    orchestrators: [{ worker: "worker-a", name: "solo", state: "running", desired_state: "running", generation: 1, projects: ["default"] }],
    planning: [session("lane-wo-running", { kind: "plan", run_mode: "tmux", title: "Plan the factory", work_order: "wo-running" })],
    lanes: [session("lane-wo-approval", { work_order: "wo-approval" }), session("lane-wo-tripped", { run_mode: "tmux", work_order: "wo-tripped" })],
  };
});

describe("talking from the floor", () => {
  it("offers Message on a headless lane and Open pane on an interactive one, each opening the dock on that lane", async () => {
    serveTalk(state);
    openPage(here("/floor"));
    const headless = (await screen.findAllByRole("button", { name: "Message lane lane-wo-approval" }))[0] as HTMLElement;
    expect(headless.textContent).toBe("Message");
    await userEvent.click(headless);
    const dock = await screen.findByRole("complementary", { name: "Conversation" });
    expect(await within(dock).findByRole("log", { name: "Event stream of lane lane-wo-approval" })).toBeTruthy();
    await waitFor(() => expect(document.activeElement).toBe(within(dock).getByRole("textbox", { name: "Message to lane lane-wo-approval" })));
    expect(screen.getByRole("heading", { level: 1, name: "Floor" })).toBeTruthy();

    const interactive = (await screen.findAllByRole("button", { name: "Open pane of lane lane-wo-tripped" }))[0] as HTMLElement;
    expect(interactive.textContent).toBe("Open pane");
    await userEvent.click(interactive);
    expect(await within(dock).findByRole("region", { name: "Pane of lane lane-wo-tripped" })).toBeTruthy();
  });

  it("offers neither on a lane that cannot be talked to", async () => {
    serveTalk(state);
    openPage(here("/floor"));
    await screen.findAllByRole("button", { name: "Message lane lane-wo-approval" });
    expect(screen.queryByRole("button", { name: "Message lane lane-generated-2" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Open pane of lane lane-generated-2" })).toBeNull();
  });

  it("makes the orchestrator's target chip a button that opens the dock on it", async () => {
    serveTalk(state);
    openPage(here("/floor"));
    await userEvent.click(await screen.findByRole("button", { name: /^Orchestrator.*, talk to the orchestrator on solo$/ }));
    const dock = await screen.findByRole("complementary", { name: "Conversation" });
    expect(await within(dock).findByRole("log", { name: "Event stream of the orchestrator on solo" })).toBeTruthy();
  });
});

describe("talking from a lane and from planning", () => {
  it("gives the lane view Talk to the lane", async () => {
    serveTalk(state);
    openPage(here("/lane?id=lane-wo-approval"));
    await userEvent.click(await screen.findByRole("button", { name: "Talk to the lane" }));
    const dock = await screen.findByRole("complementary", { name: "Conversation" });
    expect(await within(dock).findByRole("log", { name: "Event stream of lane lane-wo-approval" })).toBeTruthy();
  });

  it("gives the planning view Talk to the planning session, which opens the dock on its pane", async () => {
    serveTalk(state);
    openPage(here("/planning?lane=lane-wo-running"));
    await userEvent.click(await screen.findByRole("button", { name: "Talk to the planning session" }));
    const dock = await screen.findByRole("complementary", { name: "Conversation" });
    expect(await within(dock).findByText("WO-233 · L-91 · atlas".replace("WO-233", "wo-running").replace("L-91", "lane-wo-running").replace("atlas", "solo"))).toBeTruthy();
    expect(await within(dock).findByRole("region", { name: "Pane of lane lane-wo-running" })).toBeTruthy();
  });
});
