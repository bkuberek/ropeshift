import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Plans, Roadmap } from "../api/types";
import { openPage } from "../opening";
import { golden, here, serveViews } from "../testing";


const factory = (view: Roadmap) => view.goals.find((group) => group.goal?.title === "Run the factory")?.plans[0];
const docsPlan = (view: Roadmap) => view.goals.find((group) => group.goal?.title === "Ship solo mode")?.plans[0];

// doneShare is how much of a bar its done part fills.
const doneShare = (bar: HTMLElement) => bar.querySelector<HTMLElement>("i")?.style.width;

// The two goldens name their generated IDs apart, so a plan the roadmap and the plans view both hold is joined
// here by its title, as the page joins them by ID.
function serve(change: (view: Roadmap) => void = () => {}, plans?: (view: Plans) => void) {
  const view = golden<Roadmap>("roadmap");
  change(view);
  const held = golden<Plans>("plans");
  for (const row of held.plans) {
    const mine = view.goals.flatMap((group) => group.plans).find((candidate) => candidate.plan.title === row.plan.title);
    if (mine) {
      row.plan.id = mine.plan.id;
    }
  }
  plans?.(held);
  serveViews((call) => (call.path.startsWith("/v1/views/roadmap") ? { status: 200, body: view } : call.path.startsWith("/v1/views/plans") ? { status: 200, body: held } : undefined));
}

const items = (list: HTMLElement) => within(list).getAllByRole("listitem").filter((item) => item.parentElement === list);

async function opened() {
  serve();
  openPage(here("/roadmap"));
  return screen.findByRole("region", { name: "Run the factory" });
}

describe("the roadmap", () => {
  it("puts each plan under its goal, with its version and how far its work got", async () => {
    const goal = await opened();
    const plan = within(goal).getByRole("link", { name: "The factory floor" });
    expect(plan.getAttribute("href")).toMatch(/^\/projects\/default\/plans\?plan=/);
    expect(within(goal).getByText(/Version 5 approved · version 6 waits/)).toBeTruthy();
  });

  it("counts a plan with no milestones by its work, with a bar for what is done", async () => {
    serve();
    openPage(here("/roadmap"));
    const goal = await screen.findByRole("region", { name: "Ship solo mode" });
    const progress = within(goal).getByRole("progressbar", { name: "Write the docs: 0 of 1 done" });
    expect(progress.getAttribute("aria-valuemax")).toBe("1");
    expect(doneShare(progress)).toBe("0%");
    expect(within(goal).getByText("Running 1")).toBeTruthy();
  });

  it("says what a goal is for and how it will be measured", async () => {
    serve();
    openPage(here("/roadmap"));
    const goal = await screen.findByRole("region", { name: "Ship solo mode" });
    expect(within(goal).getByText("One machine runs the whole product.")).toBeTruthy();
    expect(within(goal).getByText("A lane survives a restart")).toBeTruthy();
  });

  it("gathers plans under no goal last", async () => {
    serve();
    openPage(here("/roadmap"));
    const regions = await screen.findAllByRole("region");
    expect(within(regions.at(-1) as HTMLElement).getByRole("heading", { level: 2 }).textContent).toBe("Under no goal");
  });

  it("says a plan has no work, and draws no bar for it", async () => {
    serve((view) => {
      const plan = docsPlan(view);
      if (plan) {
        plan.total = 0;
        plan.states = {};
        plan.work = [];
        plan.layers = [];
      }
    });
    openPage(here("/roadmap"));
    expect(await screen.findAllByText("No work yet")).toBeTruthy();
    expect(screen.queryByRole("progressbar", { name: /Write the docs/ })).toBeNull();
  });

  it("says so when nothing is planned, and offers the two ways to plan", async () => {
    serve((view) => {
      view.goals = [];
    });
    openPage(here("/roadmap"));
    expect(await screen.findByText("No goals or plans yet")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Start planning session" }).length).toBeGreaterThan(0);
    expect(screen.getByRole("link", { name: "Import an existing project" }).getAttribute("href")).toBe("/import");
  });

  it("offers to start a planning session from the page", async () => {
    await opened();
    await userEvent.click(screen.getAllByRole("button", { name: "Start planning session" })[0] as HTMLElement);
    expect(await screen.findByRole("dialog", { name: "Start planning session" })).toBeTruthy();
  });
});

describe("a plan version that waits", () => {
  it("shows a banner that names the version and the one in force, and leads to its approval", async () => {
    await opened();
    const banner = screen.getByText(/The factory floor, version 6, waits for your approval\./).closest(".banner") as HTMLElement;
    expect(within(banner).getByText(/Version 5 stays in force until you decide\./)).toBeTruthy();
    const link = within(banner).getByRole("link", { name: "Review version 6" });
    expect(link.getAttribute("href")).toMatch(/^\/projects\/default\/plan\?id=.+&version=6$/);
  });

  it("shows no banner when nothing waits", async () => {
    serve(
      () => {},
      (view) => {
        for (const plan of view.plans) {
          plan.pending_version = undefined;
        }
      },
    );
    openPage(here("/roadmap"));
    await screen.findByRole("region", { name: "Run the factory" });
    expect(screen.queryByText(/waits for your approval/)).toBeNull();
  });
});

describe("a plan's milestones and work", () => {
  it("lists milestones in priority order, each with its state, progress, and what it waits for", async () => {
    const goal = await opened();
    const milestones = items(within(goal).getByRole("list", { name: "Milestones" }));
    expect(milestones.map((item) => item.querySelector(".item-title")?.textContent)).toEqual(["Build the core", "Ship the approval page", "Guide a newcomer"]);
    expect(milestones[0]?.textContent).toContain("Priority 1");
    expect(milestones[0]?.textContent).toContain("In progress");
    expect(milestones[1]?.textContent).toContain("Waiting");
    expect(milestones[1]?.textContent).toContain("After Build the core");
    expect(milestones[2]?.textContent).toContain("Not started");
    expect(milestones[0]?.textContent).not.toContain("After");
    const progress = within(goal).getByRole("progressbar", { name: "Build the core: 1 of 2 done" });
    expect(progress.getAttribute("aria-valuenow")).toBe("1");
    expect(doneShare(progress)).toBe("50%");
    expect(milestones[0]?.textContent).toContain("1 of 2 done · 1 contract review");
    expect(within(milestones[0] as HTMLElement).getByRole("list", { name: "Exit criteria" }).textContent).toContain("The client and the core run");
  });

  it("names a milestone it does not hold by its ID", async () => {
    serve((view) => {
      factory(view)?.milestones[1]?.depends_on.push("later");
    });
    openPage(here("/roadmap"));
    expect((await screen.findAllByText(/After Build the core, later/)).length).toBeGreaterThan(0);
  });

  it("says a milestone the code already delivers exists, and counts no work for it", async () => {
    serve((view) => {
      const milestone = factory(view)?.milestones[2];
      if (milestone) {
        milestone.state = "existing";
        milestone.existing = true;
        milestone.total = 0;
        milestone.done = 0;
      }
    });
    openPage(here("/roadmap"));
    const goal = await screen.findByRole("region", { name: "Run the factory" });
    const milestones = items(within(goal).getByRole("list", { name: "Milestones" }));
    expect(milestones[2]?.textContent).toContain("Existing");
    expect(milestones[2]?.textContent).toContain("Delivered by existing code");
    expect(within(milestones[2] as HTMLElement).queryByRole("progressbar")).toBeNull();
  });

  it("lays out each milestone's work by dependency layer, as columns of work order cards", async () => {
    const goal = await opened();
    const build = within(goal).getByRole("region", { name: "Build the core · work by dependency layer" });
    const layers = items(within(build).getByRole("list", { name: "Work of Build the core by dependency layer" }));
    expect(layers.map((layer) => layer.querySelector(".overline")?.textContent)).toEqual(["Layer 1", "Layer 2"]);
    const titles = (layer: HTMLElement | undefined) => [...(layer?.querySelectorAll(".wo-title") ?? [])].map((title) => title.textContent);
    expect(titles(layers[0])).toEqual(["Write the API client"]);
    expect(titles(layers[1])).toEqual(["Build the planning core"]);
    expect(layers[0]?.textContent).toContain("Done");
    const card = within(layers[0] as HTMLElement).getByRole("link", { name: /Write the API client/ });
    expect(card.getAttribute("href")).toMatch(/\/work-order\?id=/);
  });

  it("outlines the critical path, and says so in words as well", async () => {
    const goal = await opened();
    const ship = within(goal).getByRole("region", { name: "Ship the approval page · work by dependency layer" });
    const critical = [...ship.querySelectorAll("a.wo-card.critical")];
    expect(critical.map((card) => card.textContent)).toEqual([expect.stringContaining("Build the approval page")]);
    for (const card of critical) {
      expect(card.textContent).toContain("On the critical path");
    }
    expect(within(ship).getByText(/critical path, 1 work order/)).toBeTruthy();
    expect(within(goal).getByText("2 work orders, 3 size points left on the critical path")).toBeTruthy();
    expect(within(goal).getByText("A size point is what a work order's size counts for: small 1, medium 2, large 3.")).toBeTruthy();
  });

  it("counts one work order and one size point in the singular", async () => {
    serve((view) => {
      const plan = factory(view);
      if (plan) {
        plan.critical_path = { work_orders: plan.critical_path.work_orders.slice(0, 1), weight: 1 };
      }
    });
    openPage(here("/roadmap"));
    expect((await screen.findAllByText("1 work order, 1 size point left on the critical path")).length).toBeGreaterThan(1);
  });

  it("says when nothing is left on the critical path", async () => {
    serve((view) => {
      const plan = factory(view);
      if (plan) {
        plan.critical_path = { work_orders: [], weight: 0 };
      }
    });
    openPage(here("/roadmap"));
    expect(await screen.findByText("Nothing is left on the critical path.")).toBeTruthy();
  });

  it("keeps a plan of one work order to its line", async () => {
    serve();
    openPage(here("/roadmap"));
    const goal = await screen.findByRole("region", { name: "Ship solo mode" });
    expect(within(goal).queryByRole("list", { name: "Milestones" })).toBeNull();
    expect(within(goal).queryByText(/work by dependency layer/)).toBeNull();
  });

  it("gathers a milestone's work that no layer holds after the last layer, so none goes missing", async () => {
    serve((view) => {
      const plan = factory(view);
      plan?.work.push({ id: "stray", title: "A stray work order", state: "ready", milestone: "build" });
    });
    openPage(here("/roadmap"));
    expect(await screen.findByRole("link", { name: /A stray work order/ })).toBeTruthy();
  });
});
