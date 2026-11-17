import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Calibration } from "../api/measures";
import cli from "../../testdata/calibration.cli.txt?raw";
import emptyCli from "../../testdata/calibration-empty.cli.txt?raw";
import { openPage } from "../opening";
import { golden, here, refusal, serveViews } from "../testing";

// The tab shows what `ropeshift measures estimates` prints for the same outcomes: this reads the command's own
// text, which a Go test wrote from the golden, and holds each row of the tab to it.

const open = (query = "") => openPage(here(`/measures?tab=estimates${query}`));

// served answers the calibration read with a golden, or with a report a test changed.
const served = (name = "calibration", change?: (report: Calibration) => void) => {
  const report = golden<Calibration>(name);
  change?.(report);
  return serveViews((call) => (call.path.startsWith("/v1/measures/estimates") ? { status: 200, body: report } : undefined));
};

// unrated takes every rate and median out of a report and leaves at most `most` outcomes behind each, as the API
// answers while fewer than 10 stand behind any.
function unrated(value: unknown, most: number): void {
  if (Array.isArray(value)) {
    value.forEach((one) => unrated(one, most));
    return;
  }
  if (value === null || typeof value !== "object") {
    return;
  }
  const record = value as Record<string, unknown>;
  if (typeof record.n === "number") {
    record.n = Math.min(record.n, most);
    if (typeof record.hits === "number") {
      record.hits = Math.min(record.hits, record.n as number);
    }
    delete record.rate;
    delete record.median;
  }
  Object.values(record).forEach((one) => unrated(one, most));
}

// lines are the printed lines that begin as given. A test that finds none would hold the tab to nothing, so none fails it.
function lines(text: string, prefix: string): string[] {
  const found = text.split("\n").filter((line) => line.startsWith(prefix));
  expect(found.length, `the command prints a line that begins ${JSON.stringify(prefix)}`).toBeGreaterThan(0);
  return found;
}

// The cells of a standing's row, after its name, as the command prints them: its counts, then time and cost each
// with the p50 rate, the p85 rate, and the median ratio. A cell holds what follows its word in the command's line.
const standingWords = ["outcomes ", "done ", "failed ", "canceled ", "p85 open ", "time p50 ", "p85 ", "ratio ", "cost p50 ", "p85 ", "ratio "];

function standingCells(fields: string[]): string[] {
  return standingWords.map((word, at) => {
    const field = fields[at] ?? "";
    expect(field.startsWith(word), `${field} starts with ${word}`).toBe(true);
    return field.slice(word.length);
  });
}

const rowOf = (table: HTMLElement, name: string): HTMLElement => {
  const row = within(table)
    .getAllByRole("row")
    .find((one) => within(one).queryByRole("rowheader")?.textContent === name);
  if (!row) {
    throw new Error(`no row named ${name}`);
  }
  return row;
};

const cellsOf = (row: HTMLElement) => within(row).getAllByRole("cell").map((cell) => cell.textContent ?? "");

const tableOf = async (name: string) => within(await screen.findByRole("region", { name })).getByRole("table");

const periodName: Record<string, string> = { window: "last 30 days", "all time": "all time" };
const periodTitle: Record<string, string> = { window: "Last 30 days", "all time": "All time" };

// holdsStandings holds each printed standing of one confidence to its row in the tab, in the table of its period.
async function holdsStandings(scope: string, confidence: "confident" | "low") {
  const words = confidence === "confident" ? "Confident estimates" : "Low confidence estimates";
  for (const period of ["window", "all time"]) {
    const table = await tableOf(`${words}, ${periodName[period]}`);
    for (const line of lines(cli, `${scope}\t${period}\t${confidence}\t`)) {
      const [, , , group, ...fields] = line.split("\t");
      const name = group === "all" ? "All" : (group ?? "").replace(/^(\w)/, (letter) => letter.toUpperCase());
      expect(cellsOf(rowOf(table, name)).slice(0, standingWords.length), `${scope} ${period} ${confidence} ${group}`).toEqual(standingCells(fields));
    }
  }
}

describe("the Estimates tab", () => {
  it("is a tab of the Measures view beside Factory and Delivery", async () => {
    served();
    open();
    const tabs = await screen.findByRole("navigation", { name: "Measures view" });
    expect(within(tabs).getByRole("link", { name: "Estimates" }).getAttribute("aria-current")).toBe("page");
    expect(within(tabs).getByRole("link", { name: "Delivery" }).getAttribute("href")).toBe(here("/measures?tab=delivery"));
    expect(within(tabs).getByRole("link", { name: "Factory" }).getAttribute("href")).toBe(here("/measures"));
    expect(screen.getByRole("heading", { level: 1, name: "Measures" })).toBeTruthy();
  });

  it("is reached from the Delivery tab", async () => {
    serveViews();
    openPage(here("/measures?tab=delivery"));
    const tabs = await screen.findByRole("navigation", { name: "Measures view" });
    expect(within(tabs).getByRole("link", { name: "Estimates" }).getAttribute("href")).toBe(here("/measures?tab=estimates"));
  });

  it("names the window it read, from the day the command prints to the day it prints", async () => {
    served();
    open();
    const [, from, to] = lines(cli, "window\t")[0]?.split("\t") ?? [];
    expect((await screen.findByText(/^Last 30 days, from /)).textContent).toContain(`from ${from} to ${to} UTC`);
  });

  it("puts the confident estimates first as the headline, and low confidence apart after them", async () => {
    served();
    open();
    await tableOf("Confident estimates, last 30 days");
    const headings = screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent);
    expect(headings.indexOf("Confident estimates")).toBeGreaterThanOrEqual(0);
    expect(headings.indexOf("Confident estimates")).toBeLessThan(headings.indexOf("Low confidence"));
    expect(screen.getByRole("region", { name: "Low confidence estimates, last 30 days" })).toBeTruthy();
    expect(screen.getByRole("region", { name: "Low confidence estimates, all time" })).toBeTruthy();
  });

  it("shows each confident standing as the command prints it, over the window and over all time", async () => {
    served();
    open();
    await holdsStandings("all", "confident");
  });

  it("shows each low-confidence standing as the command prints it, apart from the confident ones", async () => {
    served();
    open();
    await holdsStandings("all", "low");
  });

  it("shows a rate as a meter with a tick at its calibrated value, and a group under the minimum as counts and no rate", async () => {
    served();
    open();
    const table = await tableOf("Confident estimates, last 30 days");
    const all = cellsOf(rowOf(table, "All"));
    expect(all[5]).toBe("7 of 17 41.2%");
    const cells = within(rowOf(table, "All")).getAllByRole("cell");
    // Time p50 and p85 each draw a meter filled to the rate, with its tick at 50% and 85%.
    const p50 = cells[5]?.querySelector(".meter") as HTMLElement;
    expect((p50.querySelector("i") as HTMLElement).style.width).toBe("41.17647058823529%");
    expect((p50.querySelector("b") as HTMLElement).style.left).toBe("50%");
    const p85 = cells[6]?.querySelector(".meter") as HTMLElement;
    expect((p85.querySelector("i") as HTMLElement).style.width).toBe("75%");
    expect((p85.querySelector("b") as HTMLElement).style.left).toBe("85%");
    // The ratio is a number, with no meter.
    expect(cells[7]?.querySelector(".meter")).toBeNull();
    // Tier 1 has 5 outcomes: its counts stand, in the command's words, and no meter is drawn.
    const tier = within(rowOf(table, "Tier 1")).getAllByRole("cell");
    expect(tier[5]?.textContent).toBe("1 of 5, no rate");
    expect(tier[7]?.textContent).toBe("no median of 5");
    expect(tier.some((cell) => cell.querySelector(".meter"))).toBe(false);
    // A rate shows, so the tab says when one does, and not that there is too little work for one.
    expect(screen.getByText(/A rate shows when at least 10 outcomes stand behind it, and below that only its counts stand\./)).toBeTruthy();
    expect(screen.queryByText(/Not enough finished estimated work for a rate yet/)).toBeNull();
  });

  it("says how near a rate the work is while no group has 10 outcomes behind one", async () => {
    served("calibration", (report) => unrated(report, 6));
    open();
    await tableOf("Confident estimates, last 30 days");
    expect(
      screen.getByText(/Not enough finished estimated work for a rate yet: a rate needs 10 outcomes behind it, and the most any group has is 6\. Below that only its counts stand\./),
    ).toBeTruthy();
  });

  it("counts the open p85 bands that the p85 rate leaves out", async () => {
    served();
    open();
    const table = await tableOf("Confident estimates, last 30 days");
    const head = within(table).getAllByRole("columnheader").map((cell) => cell.textContent);
    expect(head).toEqual(["Group", "Outcomes", "Done", "Failed", "Canceled", "P85 open", "Time p50", "Time p85", "Time ratio", "Cost p50", "Cost p85", "Cost ratio"]);
    expect(cellsOf(rowOf(table, "All"))[4]).toBe("1");
    expect(screen.getAllByText(/counts in the p50 rate and the ratio, and is left out of the p85 rate/).length).toBeGreaterThan(0);
  });

  it("says how the work that ended was estimated, as the command prints the shares", async () => {
    served();
    open();
    const table = await tableOf("Share of ended work by how it was estimated");
    for (const period of ["window", "all time"]) {
      const [, , , ended, confident, low, none] = lines(cli, `all\t${period}\tshare\t`)[0]?.split("\t") ?? [];
      const row = rowOf(table, periodTitle[period] ?? "");
      expect(cellsOf(row)).toEqual([(ended ?? "").replace("ended ", ""), (confident ?? "").replace("confident ", ""), (low ?? "").replace("low ", ""), (none ?? "").replace("none ", "")]);
    }
  });

  it("lists the weeks that had outcomes, confident and low apart, as the command prints them", async () => {
    served();
    open();
    for (const confidence of ["confident", "low"] as const) {
      const table = await tableOf(`Weekly calibration, ${confidence === "confident" ? "confident estimates" : "low confidence estimates"}`);
      const printed = lines(cli, "all\tweek ").filter((line) => line.split("\t")[2] === confidence);
      const rows = within(table).getAllByRole("row").slice(1);
      expect(rows).toHaveLength(printed.length);
      printed.forEach((line, at) => {
        const [, week, , , ...fields] = line.split("\t");
        const row = rows[at] as HTMLElement;
        expect(within(row).getByRole("rowheader").textContent).toBe((week ?? "").replace("week ", ""));
        expect(cellsOf(row).slice(0, standingWords.length)).toEqual(standingCells(fields));
      });
    }
  });

  it("shows how milestone forecasts held, confident and low apart, as the command prints them", async () => {
    served();
    open();
    const table = await tableOf("How milestone forecasts held");
    for (const line of lines(cli, "milestones\t").filter((one) => /\t(confident|low)\toutcomes/.test(one))) {
      const [, period, confidence, outcomes, done, failed, canceled, open85, p50, p85] = line.split("\t");
      const row = rowOf(table, `${periodTitle[period ?? ""]}, ${confidence === "low" ? "low confidence" : confidence}`);
      expect(cellsOf(row)).toEqual([
        (outcomes ?? "").replace("outcomes ", ""),
        (done ?? "").replace("done ", ""),
        (failed ?? "").replace("failed ", ""),
        (canceled ?? "").replace("canceled ", ""),
        (open85 ?? "").replace("p85 open ", ""),
        (p50 ?? "").replace("p50 ", ""),
        (p85 ?? "").replace("p85 ", ""),
      ]);
    }
  });

  it("lists each milestone's forecast beside its actual date, as the command prints it", async () => {
    served();
    open();
    const table = await tableOf("Each milestone's forecast and its actual date");
    for (const line of lines(cli, "milestone\t")) {
      const [, plan, milestone, confidence, outcome, p50, p85, ended, within50, within85] = line.split("\t");
      const row = within(table)
        .getAllByRole("row")
        .find((one) => within(one).queryByRole("rowheader")?.textContent === milestone && cellsOf(one)[0] === plan);
      expect(row, `${plan} ${milestone}`).toBeTruthy();
      expect(cellsOf(row as HTMLElement)).toEqual([
        plan,
        confidence,
        outcome,
        (p50 ?? "").replace("forecast p50 ", ""),
        (p85 ?? "").replace("p85 ", ""),
        (ended ?? "").replace("ended ", ""),
        (within50 ?? "").replace("p50 ", ""),
        (within85 ?? "").replace("p85 ", ""),
      ]);
    }
  });

  it("says a milestone has no forecast dates and no confidence in the command's words", async () => {
    served("calibration", (report) => {
      const first = report.milestones.results[0];
      if (first) {
        delete first.forecast_p50;
        delete first.forecast_p85;
        delete first.confidence;
      }
    });
    open();
    const table = await tableOf("Each milestone's forecast and its actual date");
    const row = within(table).getAllByRole("row")[1] as HTMLElement;
    expect(cellsOf(row)[1]).toBe("none");
    expect(cellsOf(row)[3]).toBe("no forecast dates");
  });

  it("reads the window a person picks, over 30 days to begin with", async () => {
    const sent = served();
    open();
    await tableOf("Confident estimates, last 30 days");
    expect(sent.some((call) => call.path === "/v1/measures/estimates?window=30d")).toBe(true);
    const group = screen.getByRole("group", { name: "Window" });
    expect(within(group).getByRole("button", { name: "30 days" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(within(group).getByRole("button", { name: "7 days" }));
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/measures/estimates?window=7d")).toBe(true));
    await tableOf("Confident estimates, last 7 days");
    expect(window.location.search).toContain("window=7d");
    expect(within(screen.getByRole("group", { name: "Window" })).getByRole("button", { name: "7 days" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("opens on the window its address names, and on 30 days when it names one that does not exist", async () => {
    const sent = served();
    open("&window=24h");
    await tableOf("Confident estimates, last 24 hours");
    expect(sent.some((call) => call.path === "/v1/measures/estimates?window=24h")).toBe(true);
    expect(within(screen.getByRole("group", { name: "Window" })).getByRole("button", { name: "24 hours" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("reads 30 days when its address names a window that does not exist", async () => {
    const sent = served();
    open("&window=soon");
    await tableOf("Confident estimates, last 30 days");
    expect(sent.some((call) => call.path.includes("soon"))).toBe(false);
  });

  it("reads one repository's calibration when a person picks it, which the address keeps", async () => {
    served();
    open();
    await tableOf("Confident estimates, last 30 days");
    const select = screen.getByRole("combobox", { name: "Repository" });
    expect(within(select).getAllByRole("option").map((option) => option.textContent)).toEqual(["All work", "harbor-api", "harbor-web"]);
    fireEvent.change(select, { target: { value: "harbor-api" } });
    expect(window.location.search).toContain("repository=harbor-api");
    await holdsStandings("repository harbor-api", "confident");
    const shares = lines(cli, "repository harbor-api\twindow\tshare\t")[0]?.split("\t") ?? [];
    const table = await tableOf("Share of ended work by how it was estimated");
    expect(cellsOf(rowOf(table, "Last 30 days"))[0]).toBe((shares[3] ?? "").replace("ended ", ""));
  });

  it("shows all work when its address names a repository that has no calibration", async () => {
    served();
    open("&repository=nowhere");
    await tableOf("Confident estimates, last 30 days");
    expect(cellsOf(rowOf(await tableOf("Share of ended work by how it was estimated"), "Last 30 days"))[0]).toBe("26");
  });

  it("says nothing ended when no work has, with the one place work comes from", async () => {
    served("calibration-empty");
    open();
    expect(await screen.findByText("Estimate calibration appears when work orders finish.")).toBeTruthy();
    expect(within(screen.getByRole("main")).getByRole("link", { name: "Work orders" }).getAttribute("href")).toBe(here("/work"));
    expect(screen.queryByRole("table")).toBeNull();
    expect(emptyCli).toContain("share\tended 0");
  });

  it("says why it could not read, in the API's words, and keeps the controls", async () => {
    serveViews((call) => (call.path.startsWith("/v1/measures/estimates") ? refusal(500, "unavailable", "the ledger could not be read") : undefined));
    open();
    expect((await screen.findByRole("alert")).textContent).toContain("the ledger could not be read");
    expect(screen.getByRole("group", { name: "Window" })).toBeTruthy();
  });
});
