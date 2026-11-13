import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { ResourceReport, ShopFloor } from "../api/types";
import { openPage } from "../opening";
import { golden, serveViews } from "../testing";
import { bytes } from "./Installation";

const gib = 1024 ** 3;

function report(): ResourceReport {
  return {
    interval_ms: 15000,
    machine: {
      cores: 8,
      cpu_percent: 42.4,
      load_1: 1,
      load_5: 2.5,
      load_15: 3,
      memory_total_bytes: 32 * gib,
      memory_used_bytes: 12 * gib,
      memory_available_bytes: 20 * gib,
      memory_pressure_some: 1.5,
      volumes: [
        { roles: ["state"], free_bytes: 5 * gib, total_bytes: 100 * gib },
        { roles: ["worktrees"], free_bytes: 120 * gib, total_bytes: 500 * gib },
      ],
    },
    recommendation: {
      max_lanes: 7,
      basis: "estimates",
      observed_lanes: 0,
      cores: 8,
      memory_total_bytes: 32 * gib,
      reserve_cpu: 1,
      reserve_memory_bytes: 4 * gib,
      lane_cpu: 1,
      lane_memory_bytes: 2 * gib,
      by_cpu: 7,
      by_memory: 14,
      reason: "8 cores less 1 reserved fit 7 lanes",
    },
    caps: { max_lanes: 7, max_lanes_set: false, max_lanes_per_sign_in: 0 },
    sessions: [
      { kind: "lane", lane: "lane-wo-running", cpu_cores: 1.25, rss_bytes: 300 * 1024 ** 2, processes: 3 },
      { kind: "orchestrator", lane: "orchestrator-1", rss_bytes: 200 * 1024 ** 2, processes: 2 },
    ],
    warnings: [{ measure: "disk_free", volume: "state", value: 5 * gib, threshold: 10 * gib, message: "The state volume has 5.0 GiB free, under 10.0 GiB." }],
  };
}

// serveFloor answers the shop floor with its golden, changed by a test.
function serveFloor(change: (view: ShopFloor) => void) {
  const view = golden<ShopFloor>("shop-floor");
  change(view);
  return serveViews((call) => (call.path.startsWith("/v1/views/shop-floor") ? { status: 200, body: view } : undefined));
}

// table finds the scrolling region that holds a panel's table, which shares the panel's name.
async function table(name: string): Promise<HTMLElement> {
  const regions = await screen.findAllByRole("region", { name });
  const found = regions.find((region) => region.classList.contains("table-wrap"));
  if (!found) {
    throw new Error(`no table named ${name}`);
  }
  return found;
}

async function machineRow(): Promise<HTMLElement> {
  const region = await table("Machines");
  return waitFor(() => {
    const row = within(region).getAllByRole("row")[1];
    if (!row) {
      throw new Error("no machine row");
    }
    return row;
  });
}

const cell = (row: HTMLElement, column: string) => row.querySelector<HTMLElement>(`[data-label="${column}"]`);

async function sessionRow(name: string): Promise<HTMLElement> {
  const region = await table("Sessions");
  return waitFor(() => {
    const row = within(region)
      .getAllByRole("row")
      .find((tr) => tr.textContent?.includes(name));
    if (!row) {
      throw new Error(`no session row names ${name}`);
    }
    return row;
  });
}

describe("the Workers page", () => {
  it("shows what each worker last measured of its machine, with the cap in force and the recommendation", async () => {
    serveFloor((view) => {
      const line = view.workers[0];
      if (line) {
        line.worker.report = report();
        line.worker.reported_at = view.as_of - 5000;
        line.report_freshness = "observed";
      }
    });
    openPage("/workers");
    const row = await machineRow();
    expect(cell(row, "CPU")?.textContent).toBe("42%load 2.50");
    expect(cell(row, "Memory")?.textContent).toBe("12.0 GiB of 32.0 GiB20.0 GiB available, pressure 1.5%");
    expect(cell(row, "Free disk")?.textContent).toBe("5.0 GiB state120.0 GiB worktrees");
    expect(cell(row, "Lanes / cap")?.textContent).toMatch(/ \/ 7recommended 7$/);
    expect(cell(row, "Freshness")?.textContent).toMatch(/Resources measured/);
    expect(cell(row, "Freshness")?.textContent).not.toMatch(/last reported$/);
    expect(await screen.findByText(/The state volume has 5\.0 GiB free, under 10\.0 GiB\. A warning stops no work\./)).toBeTruthy();
    expect(cell(await sessionRow("lane-wo-running"), "CPU · Mem")?.textContent).toBe("1.25 cores · 300.0 MiB");
    expect(cell(await sessionRow("Every project it serves"), "CPU · Mem")?.textContent).toBe("unavailable · 200.0 MiB");
  });

  it("says a measure the machine cannot provide is unavailable, and never draws it as zero", async () => {
    serveFloor((view) => {
      const line = view.workers[0];
      if (line) {
        line.worker.report = { ...report(), machine: { cores: 4, volumes: [] }, sessions: [], warnings: [] };
        line.worker.reported_at = view.as_of - 120000;
        line.report_freshness = "last_reported";
      }
    });
    openPage("/workers");
    const row = await machineRow();
    expect(cell(row, "CPU")?.textContent).toBe("unavailableload unavailable");
    expect(cell(row, "Memory")?.textContent).toBe("unavailableunavailable");
    expect(cell(row, "Free disk")?.textContent).toBe("unavailable");
    expect(cell(row, "Freshness")?.textContent).toMatch(/last reported$/);
    expect(row.textContent).not.toMatch(/\b0%/);
    expect(cell(await sessionRow("lane-wo-running"), "CPU · Mem")?.textContent).toBe("unavailable");
  });

  it("says when a worker has not reported at all", async () => {
    serveFloor(() => {});
    openPage("/workers");
    const row = await machineRow();
    for (const column of ["CPU", "Memory", "Free disk"]) {
      expect(cell(row, column)?.textContent).toBe("not reported");
    }
    expect(cell(await sessionRow("lane-wo-running"), "CPU · Mem")?.textContent).toBe("not reported");
  });
});

describe("bytes", () => {
  it("says a size in binary units", () => {
    expect([bytes(512), bytes(2048), bytes(5 * gib), bytes(1.5 * 1024 * gib)]).toEqual(["512 B", "2.0 KiB", "5.0 GiB", "1.5 TiB"]);
  });
});
