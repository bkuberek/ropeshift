import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import doctor from "../../testdata/doctor.cli.txt?raw";
import type { ShopFloor } from "../api/types";
import { openPage } from "../opening";
import { golden, serveViews } from "../testing";

// The Workers view says each driver's doctor result in the words `ropeshift worker doctor` prints: this reads the
// command's own text, which a Go test wrote from the golden, and holds each row of the page to it.

type Printed = { worker: string; cells: string[] };

// printed reads what the command prints for each worker: its lines, one for each driver, after a line naming it.
function printed(text: string): Printed[] {
  const out: Printed[] = [];
  for (const line of text.split("\n")) {
    if (line.startsWith("worker\t")) {
      out.push({ worker: line.slice("worker\t".length), cells: [] });
    } else if (line !== "") {
      out.at(-1)?.cells.push(line);
    }
  }
  return out;
}

function serveDrivers(change: (view: ShopFloor) => void = () => {}) {
  const view = golden<ShopFloor>("shop-floor-drivers");
  change(view);
  return serveViews((call) => (call.path.startsWith("/v1/views/shop-floor") ? { status: 200, body: view } : undefined));
}

const cell = (row: HTMLElement, column: string) => row.querySelector<HTMLElement>(`[data-label="${column}"]`);
const flat = (element: Element | null | undefined) => (element?.textContent ?? "").replaceAll(/\s+/g, " ").trim();

async function drivers(): Promise<HTMLElement[]> {
  const regions = await screen.findAllByRole("region", { name: "Drivers" });
  const region = regions.find((one) => one.classList.contains("table-wrap"));
  if (!region) {
    throw new Error("no table of drivers");
  }
  return waitFor(() => {
    const rows = within(region).getAllByRole("row").slice(1);
    if (rows.length === 0) {
      throw new Error("no driver row");
    }
    return rows;
  });
}

// rowOf finds the one row of a machine, by its name.
const rowOf = (rows: HTMLElement[], machine: string) => {
  const found = rows.filter((row) => flat(cell(row, "Machine")).startsWith(machine));
  expect(found, `rows of ${machine}`).toHaveLength(1);
  return found[0] as HTMLElement;
};

describe("the drivers on the Workers page", () => {
  const commands = printed(doctor);

  it("lists one row for each driver of each machine, and one badged Not reported for a machine that names none", async () => {
    serveDrivers();
    openPage("/workers");
    const rows = await drivers();
    expect(rows).toHaveLength(commands.length);
    const solo = rowOf(rows, "solo");
    expect(flat(cell(solo, "Result"))).toBe("Not reported");
    for (const column of ["Version", "Qualification", "Sign-in", "Reason"]) {
      expect(flat(cell(solo, column)), column).toBe("");
    }
  });

  it("badges a driver Usable or Not usable by the rule an offer applies", async () => {
    serveDrivers();
    openPage("/workers");
    const rows = await drivers();
    const results = Object.fromEntries(["mint", "pear", "plum", "fig", "oak"].map((name) => [name, flat(cell(rowOf(rows, name), "Result"))]));
    expect(results).toEqual({ mint: "Usable", pear: "Usable", plum: "Not usable", fig: "Not usable", oak: "Not usable" });
  });

  it("says each driver as the command prints it: its word and reason, its fix, or its version, qualification, and sign-in", async () => {
    serveDrivers();
    openPage("/workers");
    const rows = await drivers();
    const machines: Record<string, string> = { "worker-mint": "mint", "worker-pear": "pear", "worker-plum": "plum", "worker-fig": "fig", "worker-oak": "oak" };
    for (const command of commands) {
      const name = machines[command.worker];
      if (!name) {
        expect(command.cells, `${command.worker} reports no driver`).toEqual([]);
        continue;
      }
      expect(command.cells, command.worker).toHaveLength(1);
      const row = rowOf(rows, name);
      const [driver, ...rest] = (command.cells[0] ?? "").split("\t");
      expect(flat(cell(row, "Driver")), command.worker).toBe(driver);
      const reason = flat(cell(row, "Reason"));
      if (["absent", "unusable", "signed out"].includes(rest[0] ?? "")) {
        // The command prints the word, the reason, and after a full stop the fix. A signed-out driver also prints its version and qualification.
        expect(reason, command.worker).toBe(`${rest[0]} ${rest[1]}`);
        expect(flat(cell(row, "Result")), command.worker).toBe("Not usable");
        if (rest[0] === "signed out") {
          expect(flat(cell(row, "Version")), command.worker).toBe(rest[2]);
          expect(flat(cell(row, "Qualification")), command.worker).toBe(rest[3]);
        }
      } else {
        const [version, qualified, kind, status] = rest;
        expect(flat(cell(row, "Version")), command.worker).toBe(version);
        expect(flat(cell(row, "Qualification")), command.worker).toBe(qualified);
        expect(flat(cell(row, "Sign-in")), command.worker).toBe(`${(kind ?? "").replace(/^sign-in /, "")} ${(status ?? "").replaceAll("_", " ")}`);
        expect(reason, command.worker).toBe("");
      }
    }
  });

  it("counts a result's age from the worker's last connect, and never marks it stale", async () => {
    serveDrivers();
    openPage("/workers");
    const rows = await drivers();
    for (const row of rows) {
      const freshness = flat(cell(row, "Freshness"));
      expect(freshness, flat(row)).toMatch(/^Last reported /);
      expect(freshness, flat(row)).not.toMatch(/stale/i);
    }
  });

  it("stacks each driver into a labelled block on a phone, with the reason in full", async () => {
    serveDrivers();
    openPage("/workers");
    const rows = await drivers();
    const table = rows[0]?.closest("table");
    expect(table?.classList.contains("stack-on-phone")).toBe(true);
    for (const row of rows) {
      for (const cellOf of row.querySelectorAll("td")) {
        expect(cellOf.getAttribute("data-label"), flat(cellOf)).toBeTruthy();
      }
    }
    const fig = rowOf(rows, "fig");
    expect(flat(cell(fig, "Reason"))).toBe("unusable claude --version exited with status 1. Run claude --version to see why, and repair or reinstall Claude Code.");
    expect(cell(fig, "Reason")?.classList.contains("driver-reason")).toBe(true);
    // A cell with nothing in it is left off a phone's block, so no label stands over an empty line.
    expect(cell(rowOf(rows, "solo"), "Reason")?.classList.contains("hide-phone")).toBe(true);
  });
});
