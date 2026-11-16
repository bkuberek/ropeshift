import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { AuditPage } from "../api/admin";
import type { RepositoryList } from "../api/admin";
import type { NeedsYou, Operations, Roadmap, ShopFloor } from "../api/types";
import { openPage } from "../opening";
import { golden, here, refusal, serveViews, testProject } from "../testing";

// The Overview of a project reads what the API serves from a real ledger: what waits on the member, the lanes, the
// plan's milestones, the measures of the week, and the project's audit.

type Served = {
  needs?: (view: NeedsYou) => void;
  floor?: (view: ShopFloor) => void;
  operations?: (view: Operations) => void;
  roadmap?: (view: Roadmap) => void;
  repositories?: (view: RepositoryList) => void;
  audit?: (view: AuditPage) => void;
  failing?: string[];
  projects?: { status: number; body?: unknown };
};

function serving({ needs, floor, operations, roadmap, repositories, audit, failing = [], projects }: Served = {}) {
  const waiting = golden<NeedsYou>("needs-you");
  needs?.(waiting);
  const shop = golden<ShopFloor>("shop-floor");
  floor?.(shop);
  const measured = golden<Operations>("operations");
  operations?.(measured);
  const plan = golden<Roadmap>("roadmap");
  roadmap?.(plan);
  const registered = golden<RepositoryList>("repositories");
  repositories?.(registered);
  const events = golden<AuditPage>("audit");
  audit?.(events);
  const answer = (path: string, body: unknown) => (failing.includes(path) ? refusal(500, "unavailable", `${path} could not be read`) : { status: 200, body });
  return serveViews((call) => {
    if (call.method !== "GET") {
      return undefined;
    }
    if (call.path.startsWith("/v1/views/needs-you") || call.path === "/v1/needs-you") {
      return answer("needs-you", waiting);
    }
    if (call.path.startsWith("/v1/views/shop-floor")) {
      return answer("shop-floor", shop);
    }
    if (call.path.startsWith("/v1/views/operations")) {
      return answer("operations", measured);
    }
    if (call.path.startsWith("/v1/views/roadmap")) {
      return answer("roadmap", plan);
    }
    if (call.path === "/v1/repositories") {
      return answer("repositories", registered);
    }
    if (call.path.startsWith("/v1/audit")) {
      return answer("audit", events);
    }
    return undefined;
  }, { projects });
}

const tile = async (name: RegExp) => within(await screen.findByRole("link", { name }));

describe("the Overview", () => {
  it("is what a project's own address shows, and the address stays", async () => {
    serving();
    openPage(`/projects/${testProject}`);
    expect(await screen.findByRole("heading", { level: 1, name: "Overview" })).toBeTruthy();
    expect(window.location.pathname).toBe(`/projects/${testProject}`);
    expect(document.title).toBe("Overview - ropeshift");
  });

  it("shows the six key figures, each a link to the view that holds what it counts", async () => {
    serving();
    openPage(`/projects/${testProject}`);
    const figures = await screen.findByRole("group", { name: "Key figures" });
    const links = within(figures).getAllByRole("link") as HTMLAnchorElement[];
    expect(links.map((link) => link.getAttribute("href"))).toEqual([
      "/needs-you?project=default",
      "/projects/default/floor",
      "/projects/default/reviews",
      "/projects/default/work?group=completed",
      "/projects/default/measures",
      "/usage",
    ]);
    await waitFor(() => expect(links.map((link) => link.querySelector(".kpi-value")?.textContent)).toEqual(["12", "5", "1", "2", "1", "None reported"]));
    expect(links.map((link) => link.querySelector(".kpi-label")?.textContent)).toEqual([
      "Waiting on you",
      "Lanes running",
      "In review or merging",
      "Done in 7 days",
      "Interruptions per completed",
      "Estimated cost this week",
    ]);
  });

  it("counts what the measures say, and the cost as at least a sum that is a lower bound", async () => {
    serving({
      operations: (view) => {
        view.work_orders = { acceptance: 2, integrating: 1 };
        view.measures.completed = 9;
        view.measures.interruptions_per_completed = { numerator: 3, denominator: 2, value: 1.5 };
        view.measures.cost_per_outcome = { change: { total_usd: 12.5, done: 9, per_done_usd: 1.39, lower_bound: true } };
      },
    });
    openPage(`/projects/${testProject}`);
    expect(await (await tile(/^In review or merging/)).findByText("3")).toBeTruthy();
    expect(await (await tile(/^Done in 7 days/)).findByText("9")).toBeTruthy();
    expect(await (await tile(/^Interruptions per completed/)).findByText("1.5")).toBeTruthy();
    expect(await (await tile(/^Estimated cost this week/)).findByText("at least $12.50")).toBeTruthy();
  });

  it("says a rate that counted nothing has no data, and never zero", async () => {
    serving({ operations: (view) => void (view.measures.interruptions_per_completed = { numerator: 0, denominator: 0, value: null }) });
    openPage(`/projects/${testProject}`);
    expect(await (await tile(/^Interruptions per completed/)).findByText("No data yet")).toBeTruthy();
  });

  it("lists the plan's milestones with their progress and state", async () => {
    serving();
    openPage(`/projects/${testProject}`);
    const plan = await screen.findByRole("region", { name: "Plan" });
    const milestones = within(await within(plan).findByRole("list", { name: "Milestones" })).getAllByRole("listitem");
    expect(milestones.map((row) => row.querySelector(".item-title")?.textContent)).toEqual(["Build the core", "Ship the approval page", "Guide a newcomer"]);
    expect(within(milestones[0] as HTMLElement).getByRole("progressbar").getAttribute("aria-valuetext")).toBe("1 of 2 done");
    expect(milestones[0]?.textContent).toContain("In progress");
    expect(milestones[1]?.textContent).toContain("Waiting");
    expect(within(plan).getByRole("link", { name: "Roadmap" }).getAttribute("href")).toBe("/projects/default/roadmap");
  });

  it("lists the lanes that have not ended with their state and machine", async () => {
    serving();
    openPage(`/projects/${testProject}`);
    const lanes = within(await screen.findByRole("region", { name: "Lanes" }));
    const rows = (await lanes.findAllByRole("row")).slice(1);
    expect(rows).toHaveLength(7);
    const first = within(rows[0] as HTMLElement);
    expect(first.getByRole("link", { name: "lane-wo-approval" }).getAttribute("href")).toBe("/projects/default/lane?id=lane-wo-approval");
    expect(first.getByText("Tag the release")).toBeTruthy();
    expect(first.getByText("Working")).toBeTruthy();
    expect(first.getByText("solo")).toBeTruthy();
    expect(rows.map((row) => row.textContent).join(" ")).not.toContain("Ended");
  });

  it("lists the top five of what waits on the member, the longest wait first, each opening its item", async () => {
    serving();
    openPage(`/projects/${testProject}`);
    const waiting = within(await screen.findByRole("region", { name: "Needs you" }));
    const items = await waiting.findAllByRole("listitem");
    expect(items).toHaveLength(5);
    const links = within(items[0] as HTMLElement).getAllByRole("link") as HTMLAnchorElement[];
    expect(links[0]?.getAttribute("href")).toMatch(/^\/needs-you\?project=default&item=[a-z_]+%3A/);
    expect(waiting.getByRole("link", { name: /All 12/ }).getAttribute("href")).toBe("/needs-you?project=default");
  });

  it("says nothing waits when nothing does", async () => {
    serving({ needs: (view) => void (view.items = []) });
    openPage(`/projects/${testProject}`);
    expect(await screen.findByText("Nothing needs you in this project.")).toBeTruthy();
  });

  it("shows review agreement over thirty days, the counts and no rate under ten decisions", async () => {
    serving();
    openPage(`/projects/${testProject}`);
    const agreement = within(await screen.findByRole("region", { name: "Review agreement, 30 days" }));
    expect(await agreement.findByText("Not enough decisions for a rate yet")).toBeTruthy();
    expect(agreement.getByRole("link", { name: "Reviews" }).getAttribute("href")).toBe("/projects/default/reviews?tab=agreement");
  });

  it("shows the rate from ten decisions", async () => {
    serving({
      operations: (view) => {
        view.measures.review_agreement.workspace.last_30_days = {
          decisions: 20,
          agreed: 18,
          disagreed: 2,
          unknown: 0,
          agreement_rate: { numerator: 18, denominator: 20, value: 0.9 },
          approvals_overturned: 1,
          later_defects: 0,
          failures_overridden: 1,
        };
      },
    });
    openPage(`/projects/${testProject}`);
    expect(await within(await screen.findByRole("region", { name: "Review agreement, 30 days" })).findByText("90% (18 of 20)")).toBeTruthy();
  });

  it("lists the recent activity with who acted", async () => {
    serving();
    openPage(`/projects/${testProject}`);
    const activity = within(await screen.findByRole("region", { name: "Recent activity" }));
    const rows = await activity.findAllByRole("listitem");
    expect(rows).toHaveLength(5);
    expect(rows[0]?.textContent).toContain("roster.changed");
    expect(rows[0]?.textContent).toContain("member-1");
    expect(activity.getByRole("link", { name: "Audit" }).getAttribute("href")).toBe("/projects/default/audit");
  });

  it("shows a model's words as text, so markup in an event never becomes the page's", async () => {
    serving({
      audit: (view) => {
        const [event] = view.events;
        if (event) {
          event.type = "<img src=x onerror=alert(1)>";
        }
      },
    });
    openPage(`/projects/${testProject}`);
    expect(await within(await screen.findByRole("region", { name: "Recent activity" })).findByText("<img src=x onerror=alert(1)>")).toBeTruthy();
    expect(document.querySelector("img[src='x']")).toBeNull();
  });

  it("keeps the rest of the page when one read fails, and says which", async () => {
    serving({ failing: ["audit"] });
    openPage(`/projects/${testProject}`);
    expect(await within(await screen.findByRole("region", { name: "Recent activity" })).findByText("audit could not be read")).toBeTruthy();
    expect(await within(await screen.findByRole("region", { name: "Plan" })).findByRole("list", { name: "Milestones" })).toBeTruthy();
  });
});

describe("the Overview's actions", () => {
  it("opens the register dialog, in the project", async () => {
    serving();
    openPage(`/projects/${testProject}`);
    await userEvent.click(await screen.findByRole("button", { name: "Register repositories" }));
    expect(await screen.findByRole("dialog", { name: "Register repositories in default" })).toBeTruthy();
  });

  it("opens the planning session dialog", async () => {
    serving();
    openPage(`/projects/${testProject}`);
    await userEvent.click(await screen.findByRole("button", { name: "Start planning session" }));
    expect(await screen.findByRole("dialog", { name: "Start planning session" })).toBeTruthy();
  });

  it("offers Register repositories and Import, and nothing else, for a project with no repository", async () => {
    serving({ repositories: (view) => void (view.repositories = []) });
    openPage(`/projects/${testProject}`);
    const empty = await screen.findByText("This project has no repository yet.");
    const region = within(empty.closest(".empty") as HTMLElement);
    expect(region.getByRole("button", { name: "Register repositories" })).toBeTruthy();
    expect(region.getByRole("link", { name: "Import an existing project" }).getAttribute("href")).toBe("/import");
    expect(screen.queryByRole("group", { name: "Key figures" })).toBeNull();
  });

  it("disables both for an archived project, and says how to restore it", async () => {
    const archived = { status: 200, body: { projects: [{ id: "default", name: "default", display_name: "Default", state: "archived", created_at: 1, repositories: [] }] } };
    serving({ projects: archived });
    openPage(`/projects/${testProject}`);
    expect(await screen.findByText(/Default is archived and read-only/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "Register repositories" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Start planning session" }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("Overview and the address", () => {
  it("reaches Overview at the project's address from any other view with G and O", async () => {
    serving();
    openPage(here("/roadmap"));
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    await userEvent.keyboard("go");
    expect(await screen.findByRole("heading", { level: 1, name: "Overview" })).toBeTruthy();
  });

  it("moves an address that names the view to the project's own", async () => {
    serving();
    openPage(here("/overview"));
    await waitFor(() => expect(window.location.pathname).toBe(`/projects/${testProject}`));
    expect(await screen.findByRole("heading", { level: 1, name: "Overview" })).toBeTruthy();
  });
});
