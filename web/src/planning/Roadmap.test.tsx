import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Roadmap } from "../api/types";
import { openPage } from "../opening";
import { golden, serveViews } from "../testing";

// The plan Detail lays out under its goal, which a test changes.
function docsPlan(view: Roadmap) {
  return view.goals.find((group) => group.goal?.title === "Ship solo mode")?.plans[0];
}

// doneShare is how much of a bar its done part fills.
const doneShare = (bar: HTMLElement) => bar.querySelector<HTMLElement>(".bar-done")?.style.width;

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
    expect(progress.getAttribute("aria-valuemax")).toBe("1");
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
      const plan = docsPlan(view);
      if (plan) {
        plan.total = 4;
        plan.done = 3;
        plan.states = { done: 3, blocked: 1 };
      }
    });
    openPage("/roadmap");
    const progress = await screen.findByRole("progressbar", { name: "Write the docs: 3 of 4 done" });
    expect(progress.getAttribute("aria-valuenow")).toBe("3");
    const goal = screen.getByRole("region", { name: "Ship solo mode" });
    expect(within(goal).getByText("Done 3")).toBeTruthy();
    expect(within(goal).getByText("Blocked 1")).toBeTruthy();
  });

  it("draws only the share that is done, so a plan with nothing done shows an empty bar", async () => {
    serveViews();
    openPage("/roadmap");
    const bar = await screen.findByRole("progressbar", { name: "Write the docs: 0 of 1 done" });
    expect(bar.getAttribute("aria-valuenow")).toBe("0");
    expect(bar.getAttribute("aria-valuemin")).toBe("0");
    expect(bar.getAttribute("aria-valuemax")).toBe("1");
    expect(bar.getAttribute("aria-valuetext")).toBe("0 of 1 done");
    expect(doneShare(bar)).toBe("0%");
  });

  it("draws the done share of a plan with some of its work done", async () => {
    serve((view) => {
      const plan = docsPlan(view);
      if (plan) {
        plan.total = 4;
        plan.done = 3;
      }
    });
    openPage("/roadmap");
    expect(doneShare(await screen.findByRole("progressbar", { name: "Write the docs: 3 of 4 done" }))).toBe("75%");
  });

  it("says a plan has no work, and draws no bar for it", async () => {
    serve((view) => {
      const plan = docsPlan(view);
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

describe("a plan's milestones and work", () => {
  const factory = async () => {
    serveViews();
    openPage("/roadmap");
    return screen.findByRole("region", { name: "Run the factory" });
  };
  const itemsOf = (list: HTMLElement) => within(list).getAllByRole("listitem").filter((item) => item.parentElement === list);

  it("lists milestones in priority order, each with its state and progress", async () => {
    const goal = await factory();
    const milestones = itemsOf(within(goal).getByRole("list", { name: "Milestones" }));
    expect(milestones.map((item) => item.querySelector(".milestone-title")?.textContent)).toEqual([
      "Build the core",
      "Ship the approval page",
      "Guide a newcomer",
    ]);
    expect(milestones[0]?.textContent).toContain("In progress");
    expect(milestones[1]?.textContent).toContain("Waiting");
    expect(milestones[2]?.textContent).toContain("Not started");
    const progress = within(goal).getByRole("progressbar", { name: "Build the core: 1 of 2 done" });
    expect(progress.getAttribute("aria-valuenow")).toBe("1");
    expect(doneShare(progress)).toBe("50%");
    expect(within(milestones[2] as HTMLElement).getByText("0 of 1 done")).toBeTruthy();
    expect(doneShare(within(goal).getByRole("progressbar", { name: "Guide a newcomer: 0 of 1 done" }))).toBe("0%");
  });

  it("says which milestones a milestone waits for, and how it exits", async () => {
    const goal = await factory();
    const [build, ship] = itemsOf(within(goal).getByRole("list", { name: "Milestones" }));
    expect(within(ship as HTMLElement).getByText("After Build the core")).toBeTruthy();
    expect(within(build as HTMLElement).queryByText(/^After /)).toBeNull();
    expect(within(build as HTMLElement).getByRole("list", { name: "Exit criteria" }).textContent).toContain("The client and the core run");
  });

  it("names a milestone it does not hold by its ID", async () => {
    serve((view) => {
      const plan = view.goals.find((group) => group.goal?.title === "Run the factory")?.plans[0];
      plan?.milestones[1]?.depends_on.push("later");
    });
    openPage("/roadmap");
    expect(await screen.findByText("After Build the core, later")).toBeTruthy();
  });

  it("groups the work that can run at the same time, in the order it can start", async () => {
    const goal = await factory();
    const steps = itemsOf(within(goal).getByRole("list", { name: "Work that can run at the same time" }));
    expect(steps).toHaveLength(3);
    const titles = (step: HTMLElement | undefined) => [...(step?.querySelectorAll(".layer-work-title") ?? [])].map((title) => title.textContent);
    expect(titles(steps[0])).toEqual(["Write the API client", "Write the guide"]);
    expect(titles(steps[1])).toEqual(["Build the planning core"]);
    expect(titles(steps[2])).toEqual(["Build the approval page", "Show the critical path"]);
    expect(steps[0]?.textContent).toContain("Done");
    expect(steps[0]?.textContent).toContain("Ready");
  });

  it("marks the critical path in words, and not by color alone", async () => {
    const goal = await factory();
    const path = within(goal).getByRole("list", { name: "Critical path" });
    expect(itemsOf(path).map((item) => item.textContent)).toEqual(["Build the planning core", "Build the approval page"]);
    const steps = within(goal).getByRole("list", { name: "Work that can run at the same time" });
    const marked = [...steps.querySelectorAll(".layer-work-critical")].map((item) => item.querySelector(".layer-work-title")?.textContent);
    expect(marked).toEqual(["Build the planning core", "Build the approval page"]);
    for (const item of steps.querySelectorAll(".layer-work-critical")) {
      expect(item.textContent).toContain("On the critical path");
    }
  });

  it("counts the critical path's work orders and size points apart, and says what a size point is", async () => {
    const goal = await factory();
    expect(within(goal).getByText("2 work orders, 3 size points left on the critical path")).toBeTruthy();
    expect(within(goal).getByText("A size point is what a work order's size counts for: small 1, medium 2, large 3.")).toBeTruthy();
  });

  it("counts one work order and one size point in the singular", async () => {
    serve((view) => {
      const plan = view.goals.find((group) => group.goal?.title === "Run the factory")?.plans[0];
      if (plan) {
        plan.critical_path = { work_orders: plan.critical_path.work_orders.slice(0, 1), weight: 1 };
      }
    });
    openPage("/roadmap");
    expect(await screen.findByText("1 work order, 1 size point left on the critical path")).toBeTruthy();
  });

  it("says when nothing is left on the critical path", async () => {
    serve((view) => {
      const plan = view.goals.find((group) => group.goal?.title === "Run the factory")?.plans[0];
      if (plan) {
        plan.critical_path = { work_orders: [], weight: 0 };
      }
    });
    openPage("/roadmap");
    expect(await screen.findByText("Nothing is left on the critical path.")).toBeTruthy();
  });

  it("keeps a plan of one work order to its line", async () => {
    serveViews();
    openPage("/roadmap");
    const goal = await screen.findByRole("region", { name: "Ship solo mode" });
    expect(within(goal).queryByRole("list", { name: "Milestones" })).toBeNull();
    expect(within(goal).queryByRole("list", { name: "Work that can run at the same time" })).toBeNull();
  });
});
