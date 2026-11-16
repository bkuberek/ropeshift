import { render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { WorkEstimate } from "../api/measures";
import type { Completed, Standing, WorkTimeline } from "../api/types";
import { openPage } from "../opening";
import { golden, refusal, serveViews } from "../testing";
import { ActivityView, ChangesView } from "./EvidenceTabs";

// The Changes and Activity tabs read what `work acceptance` and `work timeline` print, as the API serves them
// from a real ledger: work that is running, work whose review ruled and waits for a member, work that ended with a
// delivery record, and work that never reached ready.

const standing = (name: "running" | "reviewed" | "ended" | "empty") => golden<Standing>(`acceptance-${name}`);
const timeline = (name: "running" | "reviewed" | "ended" | "empty") => golden<WorkTimeline>(`timeline-${name}`);

// factOf reads the value of the fact a region holds under a name.
function factOf(region: HTMLElement, name: string): string | null | undefined {
  return within(region).getByText(name, { selector: "dt" }).closest(".kv-row")?.querySelector("dd")?.textContent;
}

const totals = () => screen.getByLabelText("Totals");

describe("the Changes tab", () => {
  it("says a running work order has no branch, no verdict, and no check yet, and what a member decides", () => {
    render(<ChangesView standing={standing("running")} />);
    expect(screen.getByRole("heading", { level: 3, name: "Diff summary" })).toBeTruthy();
    expect(screen.getByText("The work has left no branch yet.")).toBeTruthy();
    const criteria = screen.getByRole("list", { name: "Criteria" });
    const c1 = within(criteria).getAllByRole("listitem")[0] as HTMLElement;
    expect(c1.textContent).toContain("c1");
    expect(c1.textContent).toContain("No check covered it");
    expect(c1.textContent).toContain("No verdict yet");
    expect(screen.getByText("No check reported on this work's commit.")).toBeTruthy();
    const left = screen.getByRole("region", { name: "What is left" });
    expect(left.textContent).toContain("A member decides");
  });

  it("shows the diff summary, each criterion's verdict bound to its commit, and the checks of reviewed work", () => {
    const reviewed = standing("reviewed");
    render(<ChangesView standing={reviewed} />);
    const branch = screen.getByRole("list", { name: "Branches" });
    expect(branch.textContent).toContain("lane/lane-wo-ruled");
    expect(branch.textContent).toContain("1 file changed");
    expect(branch.textContent).toContain("+4");
    expect(branch.textContent).toContain("−19");
    const c1 = within(screen.getByRole("list", { name: "Criteria" })).getAllByRole("listitem")[0] as HTMLElement;
    expect(c1.textContent).toContain("Pass");
    expect(c1.textContent).toContain("The budget stops the retries after the third.");
    expect(within(c1).getByText("cab321").getAttribute("title")).toBe("cab321");
    expect(c1.textContent).toContain("on commit");
    const checks = within(screen.getByRole("list", { name: "Checks" })).getAllByRole("listitem");
    expect(checks).toHaveLength(1);
    expect(checks[0]?.textContent).toContain("go test");
    expect(checks[0]?.textContent).toContain("Passed");
    expect(screen.getByRole("region", { name: "What is left" }).textContent).toContain("Only the lane says");
  });

  it("says nothing is left of work that ended, and still shows what it changed", () => {
    render(<ChangesView standing={standing("ended")} />);
    expect(screen.getByRole("list", { name: "Branches" }).textContent).toContain("3 files changed");
    expect(screen.getByRole("list", { name: "Branches" }).textContent).toContain("+42");
    expect(screen.getByRole("region", { name: "What is left" }).textContent).toContain("This work order is done. Nothing is left to do.");
  });

  it("names the checks that failed and a branch nobody measured", () => {
    const failed = golden<Completed>("completed").items.find((item) => item.work_order.id === "wo-failed");
    expect(failed).toBeTruthy();
    render(<ChangesView standing={(failed as NonNullable<typeof failed>).standing} />);
    expect(screen.getByRole("list", { name: "Branches" }).textContent).toContain("The change was not measured");
    expect(within(screen.getByRole("list", { name: "Checks" })).getByText("go vet")).toBeTruthy();
    const left = screen.getByRole("region", { name: "What is left" });
    expect(left.textContent).toContain("A check failed: go vet");
  });

  it("says a work order with no criteria has none to cover", () => {
    render(<ChangesView standing={standing("empty")} />);
    expect(screen.getByText("This work order has no acceptance criteria yet.")).toBeTruthy();
    expect(screen.queryByRole("list", { name: "Criteria" })).toBeNull();
  });

  it("shows the words of a verdict as text, so a model's markup is never a page's", () => {
    const hostile = standing("reviewed");
    const [verdict] = hostile.verdicts ?? [];
    if (verdict) {
      verdict.reasons = "<img src=x onerror=alert(1)>";
    }
    const { container } = render(<ChangesView standing={hostile} />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("<img src=x onerror=alert(1)>");
  });
});

describe("the Activity tab", () => {
  it("shows work under way with the time to now, and no delivery record", () => {
    render(<ActivityView read={timeline("running")} />);
    expect(within(screen.getByRole("region", { name: "Where the time went" })).getByText("Elapsed")).toBeTruthy();
    const segments = screen.getByRole("table", { name: "Segments" });
    const rows = within(segments).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(1);
    expect(rows[0]?.textContent).toContain("Agent working");
    expect(rows[0]?.textContent).toContain("lane-wo-running");
    expect(rows[0]?.textContent).toContain("under way");
    expect(screen.queryByRole("region", { name: "Delivery record" })).toBeNull();
    expect(screen.getByText("Under way. The last segment runs to the time this was read.")).toBeTruthy();
  });

  it("lists the segments in order, with their kinds and who ended each", () => {
    render(<ActivityView read={timeline("ended")} />);
    const rows = within(screen.getByRole("table", { name: "Segments" })).getAllByRole("row").slice(1);
    expect(rows.map((found) => within(found).getAllByRole("cell")[3]?.textContent)).toEqual(["Agent working", "In review", "Waiting for a person"]);
    expect(rows.map((found) => within(found).getAllByRole("cell")[2]?.textContent)).toEqual(["8m8 minutes", "3m3 minutes", "10m10 minutes"]);
    expect(rows[2]?.textContent).toContain("member-1");
    expect(rows[0]?.textContent).toContain("lane-wo-shipped");
  });

  it("shows a wait for a person apart from agent time, in the totals and in the table", () => {
    render(<ActivityView read={timeline("ended")} />);
    expect(factOf(totals(), "Agent time")).toBe("11m");
    expect(factOf(totals(), "Waiting for a person")).toBe("10m");
    expect(factOf(totals(), "Other waits")).toBe("0s");
    expect(factOf(totals(), "Elapsed")).toBe("21m");
    const person = within(screen.getByRole("table", { name: "Segments" })).getByText("Waiting for a person").closest("tr") as HTMLElement;
    expect(person.getAttribute("data-kind")).toBe("person");
    expect(person.querySelector(".badge")?.getAttribute("data-tone")).toBe("warning");
    const working = within(screen.getByRole("table", { name: "Segments" })).getByText("Agent working").closest("tr") as HTMLElement;
    expect(working.querySelector(".badge")?.getAttribute("data-tone")).toBe("active");
  });

  it("never adds a wait for a person to agent time, whatever the ledger derived", () => {
    const read = timeline("ended");
    const wait = read.timeline.segments.find((segment) => segment.kind === "person");
    expect(wait).toBeTruthy();
    // A longer wait for a person moves its own total and the elapsed time, and leaves agent time where it was.
    if (wait) {
      wait.to += 50 * 60_000;
      read.timeline.end += 50 * 60_000;
    }
    render(<ActivityView read={read} />);
    expect(factOf(totals(), "Agent time")).toBe("11m");
    expect(factOf(totals(), "Waiting for a person")).toBe("1h");
    expect(factOf(totals(), "Elapsed")).toBe("1h 11m");
  });

  it("shows the delivery record once the work order has ended", () => {
    render(<ActivityView read={timeline("ended")} />);
    const record = screen.getByRole("region", { name: "Delivery record" });
    expect(record.textContent).toContain("Done");
    expect(factOf(record, "Delivery")).toBe("21m");
    expect(factOf(record, "Lane")).toBe("8m");
    expect(factOf(record, "Waiting for a person")).toBe("10m");
    expect(factOf(record, "Rounds")).toBe("1");
    expect(factOf(record, "Cost")).toBe("$0.00, estimated, at list price");
    expect(factOf(record, "Change")).toBe("3 files changed, +42 \u22127");
    expect(within(record).getByRole("list", { name: "Waits for a person" }).textContent).toContain("10m");
  });

  it("says a cost with a part never reported is at least what it shows", () => {
    const read = timeline("ended");
    if (read.record) {
      read.record.usage = { input_tokens: 1200, output_tokens: 300, cost_usd: 1.5, cost_lower_bound: true };
    }
    render(<ActivityView read={read} />);
    const record = screen.getByRole("region", { name: "Delivery record" });
    expect(factOf(record, "Cost")).toBe("at least $1.50, estimated, at list price");
    expect(factOf(record, "Tokens")).toBe("1,200 in, 300 out");
  });

  it("names a later round a rework and draws one row of the bar for each", () => {
    const read = timeline("ended");
    const [first] = read.timeline.segments;
    if (first) {
      read.timeline.segments.push({ ...first, round: 2, from: read.timeline.end, to: read.timeline.end + 60_000 });
      read.timeline.end += 60_000;
    }
    render(<ActivityView read={read} />);
    const bars = screen.getAllByRole("img");
    expect(bars.map((bar) => bar.getAttribute("aria-label"))).toEqual([
      "Round 1: Agent working 8m, In review 3m, Waiting for a person 10m.",
      "Round 2, rework: Agent working 1m.",
    ]);
  });

  it("says why an ended work order has no delivery record when it was no change or rework", () => {
    const read = timeline("ended");
    delete read.record;
    render(<ActivityView read={read} />);
    expect(screen.queryByRole("region", { name: "Delivery record" })).toBeNull();
    expect(screen.getByText("Only a change or a rework leaves a delivery record.")).toBeTruthy();
  });

  it("says a work order that never reached ready has no timeline", () => {
    render(<ActivityView read={timeline("empty")} />);
    expect(screen.getByText("No timeline yet")).toBeTruthy();
    expect(screen.queryByRole("table", { name: "Segments" })).toBeNull();
  });

  it("shows the estimate band beside the baseline in full, with the actual delivery time of ended work", () => {
    render(<ActivityView read={timeline("ended")} estimate={golden<WorkEstimate>("work-estimate")} />);
    const panel = screen.getByRole("region", { name: "Estimate" });
    expect(panel.querySelector('[data-band="estimate"]')?.textContent).toContain("delivery p50 6h0m0s p85 11h0m0s");
    expect(panel.querySelector('[data-band="baseline"]')?.textContent).toContain("p85 more than 4h0m0s");
    expect(panel.querySelector("details")).toBeNull();
    expect(panel.querySelector('[data-band="baseline"] [data-tick="actual"]')).toBeTruthy();
    expect(panel.textContent).toContain("Actual 21m0s");
  });

  it("says the actual is none yet while the work is under way, and keeps the estimate beside a work order with no timeline", () => {
    const running = render(<ActivityView read={timeline("running")} estimate={golden<WorkEstimate>("work-estimate")} />);
    expect(within(running.container).getByText("Actual: none yet.")).toBeTruthy();
    running.unmount();
    render(<ActivityView read={timeline("empty")} estimate={golden<WorkEstimate>("work-estimate-low")} />);
    expect(screen.getByText("No timeline yet")).toBeTruthy();
    expect(screen.getByRole("region", { name: "Estimate" }).textContent).toContain("Low confidence");
  });

  it("shows no Estimate panel until the estimate has been read", () => {
    render(<ActivityView read={timeline("ended")} />);
    expect(screen.queryByRole("region", { name: "Estimate" })).toBeNull();
  });
});

describe("the tabs of a work order's page", () => {
  const serving = (over?: Parameters<typeof serveViews>[0]) =>
    serveViews((call) => {
      const own = over?.(call);
      if (own) {
        return own;
      }
      if (call.path === "/v1/work-orders/wo-running/acceptance") {
        return { status: 200, body: standing("running") };
      }
      if (call.path === "/v1/work-orders/wo-running/timeline") {
        return { status: 200, body: timeline("running") };
      }
      return undefined;
    });

  it("reads the acceptance route when Changes opens, and shows what it returns", async () => {
    const sent = serving();
    openPage(`/work-order?id=wo-running&tab=changes`);
    expect(await screen.findByRole("heading", { level: 3, name: "Diff summary" })).toBeTruthy();
    expect(sent.some((call) => call.method === "GET" && call.path === "/v1/work-orders/wo-running/acceptance" && call.project === "default")).toBe(true);
    expect(sent.some((call) => call.path.endsWith("/timeline"))).toBe(false);
    expect(await screen.findByText("The work has left no branch yet.")).toBeTruthy();
  });

  it("reads the timeline route when Activity opens, and shows what it returns", async () => {
    const sent = serving();
    openPage(`/work-order?id=wo-running&tab=activity`);
    expect(await screen.findByRole("table", { name: "Segments" })).toBeTruthy();
    expect(sent.some((call) => call.method === "GET" && call.path === "/v1/work-orders/wo-running/timeline" && call.project === "default")).toBe(true);
    expect(sent.some((call) => call.path.endsWith("/acceptance"))).toBe(false);
  });

  it("shows the API's words when a tab cannot be read, and keeps the page", async () => {
    serving((call) => (call.path.endsWith("/acceptance") ? refusal(500, "unavailable", "The ledger could not be read.") : undefined));
    openPage(`/work-order?id=wo-running&tab=changes`);
    expect((await screen.findByRole("alert")).textContent).toContain("The ledger could not be read.");
    expect(screen.getByRole("heading", { level: 1 })).toBeTruthy();
  });

  it("reads the estimate route for the side of the page and for the Activity tab", async () => {
    const sent = serving();
    openPage(`/work-order?id=wo-running&tab=activity`);
    const side = await screen.findByRole("complementary", { name: "Decision and facts" });
    const panel = await within(side).findByRole("region", { name: "Estimate" });
    expect(panel.querySelector('[data-band="estimate"]')?.textContent).toContain("delivery p50 6h0m0s p85 11h0m0s");
    expect(panel.querySelector('[data-band="baseline"]')?.textContent).toContain("Low confidence");
    expect(panel.querySelectorAll("details.estimate-more")).toHaveLength(2);
    expect(sent.some((call) => call.method === "GET" && call.path === "/v1/work-orders/wo-running/estimate" && call.project === "default")).toBe(true);
  });

  it("says a work order with no estimate and no baseline so on its page", async () => {
    serving((call) => (call.path.endsWith("/estimate") ? { status: 200, body: golden<WorkEstimate>("work-estimate-absent") } : undefined));
    openPage(`/work-order?id=wo-running`);
    const side = await screen.findByRole("complementary", { name: "Decision and facts" });
    expect(await within(side).findByText("No estimate was made for this work order.")).toBeTruthy();
    expect(within(side).getByText("No baseline is recorded for this work order yet.")).toBeTruthy();
  });

  it("keeps the page when the estimate cannot be read", async () => {
    serving((call) => (call.path.endsWith("/estimate") ? refusal(500, "unavailable", "The estimate could not be read.") : undefined));
    openPage(`/work-order?id=wo-running`);
    const side = await screen.findByRole("complementary", { name: "Decision and facts" });
    expect(await within(side).findByText("The estimate could not be read.")).toBeTruthy();
    expect(screen.getByRole("heading", { level: 1 })).toBeTruthy();
  });

  it("reads a work order of another state from its own routes", async () => {
    const sent = serveViews((call) => {
      if (call.path.endsWith("/timeline")) {
        return { status: 200, body: timeline("ended") };
      }
      return undefined;
    });
    openPage(`/work-order?id=wo-done&tab=activity`);
    await waitFor(() => expect(screen.getByRole("region", { name: "Delivery record" })).toBeTruthy());
    expect(sent.some((call) => call.path === "/v1/work-orders/wo-done/timeline")).toBe(true);
  });
});
