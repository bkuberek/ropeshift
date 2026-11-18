import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { LookBack } from "../api/measures";
import cli from "../../testdata/delivery.cli.txt?raw";
import { openPage } from "../opening";
import { golden, here, refusal, serveViews } from "../testing";
import { span } from "./format";

// The page shows what `ropeshift measures delivery` prints for the same records: this reads the command's own
// text, which a Go test wrote from the golden, and holds each row of the page to it.

const open = (query = "") => openPage(here(`/measures?tab=delivery${query}`));

// goMs reads a duration as the command prints it, such as 15h0m0s.
function goMs(text: string): number {
  const found = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(text);
  if (!found) {
    throw new Error(`not a duration: ${text}`);
  }
  return ((Number(found[1] ?? 0) * 60 + Number(found[2] ?? 0)) * 60 + Number(found[3] ?? 0)) * 1000;
}

type Printed = { label: string; done: string; failed: string; canceled: string; perWeek: string; elapsed: string; delivery: string; cost: string };

// printed reads the rows the command prints: all work, then each group.
function printed(text: string): Printed[] {
  return text
    .split("\n")
    .filter((line) => line.startsWith("all\t") || /^(kind|size|agent|repository)\t/.test(line))
    .map((line) => {
      const cells = line.split("\t");
      const named = line.startsWith("all\t");
      const [label, ...rest] = named ? ["all", ...cells.slice(1)] : [cells[1] ?? "", ...cells.slice(2)];
      const [done, failed, canceled, perWeek, elapsed, delivery, cost] = rest;
      return {
        label: label ?? "",
        done: (done ?? "").replace("done ", ""),
        failed: (failed ?? "").replace("failed ", ""),
        canceled: (canceled ?? "").replace("canceled ", ""),
        perWeek: (perWeek ?? "").replace("per week ", ""),
        elapsed: (elapsed ?? "").replace("elapsed ", ""),
        delivery: (delivery ?? "").replace("delivery ", ""),
        cost: (cost ?? "").replace("cost ", ""),
      };
    });
}

// words says a printed band as the page says it: each duration in the page's units.
function band(text: string): string {
  const found = /^p50 (\S+) p85 (\S+)$/.exec(text);
  return found ? `p50 ${span(goMs(found[1] ?? ""))} · p85 ${span(goMs(found[2] ?? ""))}` : text;
}

async function groupsTable(by = "kind"): Promise<HTMLElement> {
  const region = await screen.findByRole("region", { name: `Delivery by ${by}` });
  return within(region).getByRole("table");
}

describe("the Delivery tab", () => {
  it("is a tab of the Measures view beside Factory, which stays the view's first", async () => {
    serveViews();
    open();
    const tabs = await screen.findByRole("navigation", { name: "Measures view" });
    expect(within(tabs).getByRole("link", { name: "Delivery" }).getAttribute("aria-current")).toBe("page");
    expect(within(tabs).getByRole("link", { name: "Factory" }).getAttribute("href")).toBe(here("/measures"));
    expect(screen.getByRole("heading", { level: 1, name: "Measures" })).toBeTruthy();
  });

  it("shows a row for all work and one for each group, with the numbers the command prints", async () => {
    serveViews();
    open();
    const rows = within(await groupsTable()).getAllByRole("row").slice(1);
    const expected = printed(cli);
    expect(rows).toHaveLength(expected.length);
    expected.forEach((line, at) => {
      const row = rows[at] as HTMLElement;
      // The cells after the group's name, in the order of the table's columns.
      const cells = within(row).getAllByRole("cell");
      const columns = ["Done", "Failed", "Canceled", "Per week", "Trend", "Elapsed", "Delivery", "Estimated cost"];
      const cell = (name: string) => cells[columns.indexOf(name)]?.textContent ?? "";
      expect(within(row).getByRole("rowheader").textContent).toBe(line.label === "all" ? "All work" : line.label.charAt(0).toUpperCase() + line.label.slice(1));
      expect(cell("Done")).toContain(line.done);
      expect(cell("Failed")).toContain(line.failed);
      expect(cell("Canceled")).toContain(line.canceled);
      expect(cell("Per week")).toContain(line.perWeek);
      if (line.elapsed !== "no data") {
        expect(cell("Elapsed")).toContain(band(line.elapsed));
        expect(cell("Delivery")).toContain(band(line.delivery));
      }
      const cost = /^(at least )?p50 (\$\S+) p85 (\$\S+)$/.exec(line.cost);
      expect(cell("Estimated cost")).toContain(`${cost?.[1] ?? ""}p50 ${cost?.[2]} · p85 ${cost?.[3]}`);
    });
  });

  it("says where the time went in each group, waiting for a person apart, as the command prints it", async () => {
    serveViews();
    open();
    const where = await screen.findByRole("region", { name: "Where the time went" });
    const all = within(where).getByRole("img", { name: /^All work: / });
    expect(all.getAttribute("aria-label")).toContain("Agent working 59.9%");
    expect(all.getAttribute("aria-label")).toContain("Waiting for a person 23.6%");
    const legend = within(where).getAllByRole("list", { name: /^Time by kind for / })[0] as HTMLElement;
    expect(within(legend).getByText(/Waiting for a person/).closest("li")?.className).toContain("apart");
    const printedShares = cli.split("\n").find((line) => line.startsWith("share\tall\t")) ?? "";
    for (const part of printedShares.split("\t").slice(2)) {
      const [kind, percent] = part.split(" ");
      expect(legend.textContent).toContain(percent);
      expect(kind).toBeTruthy();
    }
  });

  it("shows the weekly trend of all work, a row for each of the twelve weeks, as the command prints it", async () => {
    serveViews();
    open();
    const trend = await screen.findByRole("region", { name: "Weekly trend of all work" });
    const rows = within(trend).getAllByRole("row").slice(1);
    const weeks = cli.split("\n").filter((line) => line.startsWith("week\t"));
    expect(rows).toHaveLength(12);
    weeks.forEach((line, at) => {
      const [, date, done, delivery] = line.split("\t");
      const row = rows[at] as HTMLElement;
      expect(row.textContent).toContain(date);
      expect(row.textContent).toContain((done ?? "").replace("done ", ""));
      const printedDelivery = (delivery ?? "").replace("delivery ", "");
      expect(row.textContent).toContain(printedDelivery === "no data" ? "none finished" : band(printedDelivery));
    });
  });

  it("reads the window a person picks, over 30 days to begin with", async () => {
    const sent = serveViews();
    open();
    await groupsTable();
    expect(sent.some((call) => call.path === "/v1/measures/delivery?window=30d&by=kind")).toBe(true);
    const group = screen.getByRole("group", { name: "Window" });
    expect(within(group).getByRole("button", { name: "30 days" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(within(group).getByRole("button", { name: "7 days" }));
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/measures/delivery?window=7d&by=kind")).toBe(true));
    expect(window.location.search).toContain("window=7d");
    expect(within(screen.getByRole("group", { name: "Window" })).getByRole("button", { name: "7 days" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("groups by what a person picks, which the address keeps", async () => {
    const sent = serveViews();
    open();
    await groupsTable();
    fireEvent.change(screen.getByRole("combobox", { name: "Group by" }), { target: { value: "size" } });
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/measures/delivery?window=30d&by=size")).toBe(true));
    const rows = within(await groupsTable("size")).getAllByRole("row").slice(1);
    expect(rows.map((row) => within(row).getByRole("rowheader").textContent)).toEqual(["All work", "Large", "Medium", "Small"]);
    expect(window.location.search).toContain("by=size");
  });

  it("reads 30 days by kind when its address names a window or a grouping that does not exist", async () => {
    const sent = serveViews();
    open("&window=soon&by=colour");
    await groupsTable();
    expect(sent.some((call) => call.path === "/v1/measures/delivery?window=30d&by=kind")).toBe(true);
    expect(sent.some((call) => call.path.includes("soon") || call.path.includes("colour"))).toBe(false);
  });

  it("opens on what its address names", async () => {
    const sent = serveViews();
    open("&window=24h&by=size");
    await groupsTable("size");
    expect(sent.some((call) => call.path === "/v1/measures/delivery?window=24h&by=size")).toBe(true);
    expect(within(screen.getByRole("group", { name: "Window" })).getByRole("button", { name: "24 hours" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("reads every active project apart for all projects, each project one group, and groups by project alone", async () => {
    const sent = serveViews();
    open();
    await groupsTable();
    fireEvent.click(within(screen.getByRole("group", { name: "Scope" })).getByRole("button", { name: "All projects" }));
    const rows = within(await groupsTable("project")).getAllByRole("row").slice(1);
    expect(rows.map((row) => within(row).getByRole("rowheader").textContent)).toEqual(["default"]);
    expect(sent.some((call) => call.project === "default" && call.path === "/v1/measures/delivery?window=30d&by=kind")).toBe(true);
    expect(screen.queryByRole("combobox", { name: "Group by" })).toBeNull();
    expect(screen.getByText(/each project's own records, because the percentiles of separate projects do not combine/i)).toBeTruthy();
  });

  it("says nothing finished when no work has, with the one place work comes from", async () => {
    serveViews((call) => (call.path.startsWith("/v1/measures/delivery") ? { status: 200, body: golden<LookBack>("delivery-empty") } : undefined));
    open();
    expect(await screen.findByText("Delivery measures appear when work orders finish.")).toBeTruthy();
    expect(within(screen.getByRole("main")).getByRole("link", { name: "Work orders" }).getAttribute("href")).toBe(here("/work"));
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("says why it could not read, in the API's words, and keeps the controls", async () => {
    serveViews((call) => {
      if (call.path === "/v1/measures/ci") {
        return { status: 200, body: golden("ci-durations") };
      }
      return call.path.startsWith("/v1/measures/delivery") ? refusal(500, "unavailable", "the ledger could not be read") : undefined;
    });
    open();
    expect((await screen.findByRole("alert")).textContent).toContain("the ledger could not be read");
    expect(screen.getByRole("group", { name: "Window" })).toBeTruthy();
  });
});
