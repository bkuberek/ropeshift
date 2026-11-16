import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import cliPlan from "../../testdata/plan-estimates.cli.txt?raw";
import type { PlanForecast } from "../api/measures";
import type { PlanView } from "../api/types";
import { golden } from "../testing";
import { PlanEstimates } from "./PlanEstimates";

// A plan version's estimates and forecasts say what `ropeshift plan forecast` prints for the same records: the
// command's own text, which a Go test wrote from the golden, holds each estimate, forecast, what-if, and
// explanation of the page.

type Estimated = { work: string; label: string; cells: string[]; explanation: string };
type Forecasted = { whose: string; cells: string[]; lines: string[] };

// printed reads the forecasts the command prints, each with the lines indented under it, and then the estimates.
function printed(text: string): { forecasts: Forecasted[]; estimates: Estimated[] } {
  const out: { forecasts: Forecasted[]; estimates: Estimated[] } = { forecasts: [], estimates: [] };
  for (const line of text.split("\n")) {
    const cells = line.split("\t");
    if (cells[0] === "forecast") {
      out.forecasts.push({ whose: cells[1] ?? "", cells: cells.slice(3), lines: [] });
    } else if (cells[0] === "estimate") {
      out.estimates.push({ work: cells[1] ?? "", label: cells[2] ?? "", cells: cells.slice(3), explanation: "" });
    } else if (line.startsWith("\t")) {
      const estimate = out.estimates.at(-1);
      if (estimate) {
        estimate.explanation = line.slice(1);
      } else {
        out.forecasts.at(-1)?.lines.push(line.slice(1));
      }
    }
  }
  return out;
}

const flat = (element: Element | null | undefined) => (element?.textContent ?? "").replaceAll(/\s+/g, " ");
const titles: Record<string, string> = { build: "Build the core", ship: "Ship the approval page", guide: "Guide a newcomer" };
const read = () => golden<PlanView>("plan-estimates").approval?.forecast as PlanForecast;

function shown(forecast: PlanForecast = read(), compact = false) {
  return render(<PlanEstimates forecast={forecast} titleOf={(id) => titles[id] ?? id} compact={compact} />);
}

describe("a plan version's estimates", () => {
  const commands = printed(cliPlan);

  it("shows each work order's estimate in the command's words with its explanation, and no other", () => {
    const { container } = shown();
    expect([...container.querySelectorAll("[data-estimate]")].map((one) => one.getAttribute("data-estimate"))).toEqual(commands.estimates.map((one) => one.work));
    for (const command of commands.estimates) {
      const one = flat(container.querySelector(`[data-estimate="${command.work}"]`));
      for (const cell of command.cells) {
        expect(one, `${command.work}: ${cell}`).toContain(cell);
      }
      expect(one, `${command.work}: explanation`).toContain(command.explanation);
    }
  });

  it("counts the estimates that are confident, low confidence, and absent", () => {
    const { container } = shown();
    expect(flat(container.querySelector(".estimate-tally"))).toBe("4 estimates: 1 confident, 2 low confidence, 1 with no history.");
  });

  it("says the ledger revision the estimates read", () => {
    const { container } = shown();
    expect(flat(container)).toContain("The estimates read ledger revision 412.");
  });

  it("shows the plan's and each milestone's forecast with its what-ifs and explanation, lower bounds as or later and at least", () => {
    const { container } = shown();
    expect([...container.querySelectorAll("[data-forecast]")].map((one) => one.getAttribute("data-forecast"))).toEqual(commands.forecasts.map((one) => one.whose));
    for (const command of commands.forecasts) {
      const one = flat(container.querySelector(`[data-forecast="${command.whose}"]`));
      for (const cell of command.cells) {
        expect(one, `${command.whose}: ${cell}`).toContain(cell);
      }
      // The what-ifs are one line of the command's, joined by "; ", and one item each on the page, which begin with a
      // capital.
      for (const line of command.lines) {
        for (const part of line.startsWith("with ") ? line.split("; ") : [line]) {
          expect(one.toLowerCase(), `${command.whose}: ${part}`).toContain(part.toLowerCase());
        }
      }
    }
    const plan = flat(container.querySelector('[data-forecast="plan"]'));
    expect(plan).toContain("p85 2025-11-16 13:33 UTC or later");
    expect(plan).toContain("cost p50 $18.40 p85 at least $31.20");
  });

  it("keeps each band's badge and holds its explanation under a control where space is short", () => {
    const { container } = shown(read(), true);
    const low = container.querySelector('[data-estimate="generated-9"]');
    expect(flat(low?.querySelector(".estimate-line"))).toContain("Low confidence");
    expect(low?.querySelector("details.estimate-more")).toBeTruthy();
    expect(flat(low)).toContain("Based on 7 finished medium changes");
  });

  it("says a version the forecast tick has not forecast is pending, and still shows the estimates it made", () => {
    const pending: PlanForecast = { ...read(), state: "pending", forecasts: [] };
    const { container } = shown(pending);
    expect(flat(container)).toContain("Forecast pending: the daemon's forecast tick has not forecast version 6 yet");
    expect(container.querySelectorAll("[data-estimate]")).toHaveLength(4);
    expect(container.querySelector("[data-forecast]")).toBeNull();
  });

  it("says a version a newer one replaced has no forecast, and says nothing of estimates it never made", () => {
    const superseded: PlanForecast = { ...read(), state: "superseded", forecasts: [], estimate_set: null };
    const { container } = shown(superseded);
    expect(flat(container)).toContain("No forecast: a newer version replaced version 6 before the daemon's forecast tick forecast it");
    expect(container.querySelector("[data-estimate]")).toBeNull();
    expect(container.querySelector(".estimate-tally")).toBeNull();
    expect(flat(container)).toContain("No estimates were made for this version.");
  });
});
