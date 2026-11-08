import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Roadmap } from "../api/types";
import { openPage } from "../opening";
import { golden, serveViews } from "../testing";

function serve(change: (view: Roadmap) => void) {
  const view = golden<Roadmap>("roadmap");
  change(view);
  serveViews((call) => (call.path.startsWith("/v1/views/roadmap") ? { status: 200, body: view } : undefined));
}

describe("the roadmap", () => {
  it("puts each plan under its goal, with how far its work got", async () => {
    serveViews();
    openPage("/roadmap");
    const goal = await screen.findByRole("region", { name: "Ship solo mode" });
    const plan = within(goal).getByRole("link", { name: "Write the docs" });
    expect(plan.getAttribute("href")).toMatch(/^\/plans\?plan=/);
    const progress = within(goal).getByRole("progressbar", { name: "Write the docs: 0 of 1 done" });
    expect(progress.getAttribute("max")).toBe("1");
    expect(within(goal).getByText("0 of 1 done")).toBeTruthy();
    expect(within(goal).getByText("Running 1")).toBeTruthy();
  });

  it("says what a goal is for and how it will be measured", async () => {
    serveViews();
    openPage("/roadmap");
    const goal = await screen.findByRole("region", { name: "Ship solo mode" });
    expect(within(goal).getByText("One machine runs the whole product.")).toBeTruthy();
    expect(within(goal).getByText("A lane survives a restart")).toBeTruthy();
  });

  it("gathers plans under no goal last", async () => {
    serveViews();
    openPage("/roadmap");
    const regions = await screen.findAllByRole("region");
    expect(regions.at(-1)?.getAttribute("aria-labelledby")).toBeTruthy();
    expect(within(regions.at(-1) as HTMLElement).getByRole("heading", { level: 2 }).textContent).toBe("Under no goal");
  });

  it("counts done work against all of it", async () => {
    serve((view) => {
      const plan = view.goals[0]?.plans[0];
      if (plan) {
        plan.total = 4;
        plan.done = 3;
        plan.states = { done: 3, blocked: 1 };
      }
    });
    openPage("/roadmap");
    const progress = await screen.findByRole("progressbar", { name: "Write the docs: 3 of 4 done" });
    expect((progress as HTMLProgressElement).value).toBe(3);
    const goal = screen.getByRole("region", { name: "Ship solo mode" });
    expect(within(goal).getByText("Done 3")).toBeTruthy();
    expect(within(goal).getByText("Blocked 1")).toBeTruthy();
  });

  it("says a plan has no work, and draws no bar for it", async () => {
    serve((view) => {
      const plan = view.goals[0]?.plans[0];
      if (plan) {
        plan.total = 0;
        plan.states = {};
      }
    });
    openPage("/roadmap");
    expect(await screen.findByText("No work yet")).toBeTruthy();
    expect(screen.queryByRole("progressbar", { name: /Write the docs/ })).toBeNull();
  });

  it("says so when nothing is planned", async () => {
    serve((view) => {
      view.goals = [];
    });
    openPage("/roadmap");
    expect(await screen.findByText("No goals or plans yet.")).toBeTruthy();
  });
});
