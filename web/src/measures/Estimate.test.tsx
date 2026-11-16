import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import cliAbsent from "../../testdata/work-estimate-absent.cli.txt?raw";
import cliConfident from "../../testdata/work-estimate.cli.txt?raw";
import cliLow from "../../testdata/work-estimate-low.cli.txt?raw";
import cliNone from "../../testdata/work-estimate-none.cli.txt?raw";
import cliOpen from "../../testdata/work-estimate-open.cli.txt?raw";
import type { WorkEstimate } from "../api/measures";
import { golden } from "../testing";
import { EstimateBand, WorkEstimateView } from "./Estimate";

// A work order's estimate and its baseline say what `ropeshift work show` prints for the same records: this reads
// the command's own text, which a Go test wrote from the golden, and holds each band of the page to it.

type Printed = { label: string; cells: string[]; explanation: string };

// printed reads the estimate and baseline lines the command prints, and the explanation indented under each.
function printed(text: string): Record<"estimate" | "baseline", Printed | undefined> {
  const out: Record<"estimate" | "baseline", Printed | undefined> = { estimate: undefined, baseline: undefined };
  let into: Printed | undefined;
  for (const line of text.split("\n")) {
    const [head, label, ...cells] = line.split("\t");
    if (head === "estimate" || head === "baseline") {
      into = { label: label ?? "", cells, explanation: "" };
      out[head] = into;
    } else if (line.startsWith("\t") && into) {
      into.explanation = line.slice(1);
    }
  }
  return out;
}

const flat = (element: Element | null | undefined) => (element?.textContent ?? "").replaceAll(/\s+/g, " ");
const read = (name: string) => golden<WorkEstimate>(name);

const cases = [
  ["a confident estimate beside a low-confidence baseline", "work-estimate", cliConfident],
  ["a low estimate whose cost is a lower bound", "work-estimate-low", cliLow],
  ["a low estimate whose p85 is open", "work-estimate-open", cliOpen],
  ["an estimate with no history", "work-estimate-none", cliNone],
] as const;

describe("an estimate band", () => {
  for (const [name, file, cli] of cases) {
    it(`says ${name} in the words the command prints, with its explanation`, () => {
      const commands = printed(cli);
      const view = read(file);
      const { container } = render(<WorkEstimateView read={view} />);
      for (const which of ["estimate", "baseline"] as const) {
        const command = commands[which];
        const shown = flat(container.querySelector(`[data-band="${which}"]`));
        expect(command, which).toBeTruthy();
        for (const cell of command?.cells ?? []) {
          expect(shown, `${which}: ${cell}`).toContain(cell);
        }
        expect(shown, `${which}: explanation`).toContain(command?.explanation);
        expect(shown, which).toContain(command?.label === "no history" ? "no history" : "");
      }
    });
  }

  it("puts the Low confidence badge before Estimate for a low estimate, and none on a confident one", () => {
    const low = render(<EstimateBand estimate={read("work-estimate-low").estimate} />);
    const range = low.container.querySelector(".estimate-line");
    expect(flat(range?.firstElementChild)).toBe("Low confidence");
    expect(flat(range).indexOf("Low confidence")).toBeLessThan(flat(range).indexOf("Estimate"));
    low.unmount();
    const confident = render(<EstimateBand estimate={read("work-estimate").estimate} />);
    expect(flat(confident.container.querySelector(".estimate-line"))).not.toContain("Low confidence");
  });

  it("draws an open band with no p85 tick, an arrow past the longest record, and the words more than", () => {
    const { container } = render(<EstimateBand estimate={read("work-estimate-open").estimate} />);
    const track = container.querySelector(".estimate-track");
    expect(track?.querySelector('[data-tick="p50"]')).toBeTruthy();
    expect(track?.querySelector('[data-tick="p85"]')).toBeNull();
    expect(track?.querySelector(".estimate-open")).toBeTruthy();
    expect(flat(container)).toContain("p85 more than 3h0m0s");
    expect(flat(container)).toContain("so the band is open above 3h");
  });

  it("draws a closed band with both ticks and the hatch only for low confidence", () => {
    const confident = render(<EstimateBand estimate={read("work-estimate").estimate} />);
    expect(confident.container.querySelector('[data-tick="p50"]')).toBeTruthy();
    expect(confident.container.querySelector('[data-tick="p85"]')).toBeTruthy();
    expect(confident.container.querySelector(".estimate-track")?.getAttribute("data-fill")).toBe("solid");
    confident.unmount();
    const low = render(<EstimateBand estimate={read("work-estimate-low").estimate} />);
    expect(low.container.querySelector(".estimate-track")?.getAttribute("data-fill")).toBe("hatch");
  });

  it("describes the track in a sentence and says an estimate with no history as no band, never as zero", () => {
    const { container } = render(<EstimateBand estimate={read("work-estimate-none").estimate} />);
    expect(container.querySelector(".estimate-track")).toBeNull();
    expect(flat(container)).toContain("Estimate no history");
    expect(flat(container)).toContain("No estimate: no medium change has finished in this project yet");
    expect(flat(container)).not.toContain("0s");
    expect(flat(container)).not.toContain("$0");
    const bar = render(<EstimateBand estimate={read("work-estimate").estimate} />).container.querySelector(".estimate-track");
    expect(bar?.getAttribute("role")).toBe("img");
    expect(bar?.getAttribute("aria-label")).toBe("Estimate: p50 6h0m0s, p85 11h0m0s");
  });

  it("keeps the badge and puts the explanation under a control when space is short, and in full otherwise", () => {
    const compact = render(<EstimateBand estimate={read("work-estimate-low").estimate} compact />);
    expect(flat(compact.container.querySelector(".estimate-line"))).toContain("Low confidence");
    const details = compact.container.querySelector("details.estimate-more") as HTMLDetailsElement;
    expect(details.open).toBe(false);
    expect(flat(details)).toContain("Based on 7 finished medium changes");
    compact.unmount();
    const full = render(<EstimateBand estimate={read("work-estimate-low").estimate} />);
    expect(full.container.querySelector("details")).toBeNull();
    expect(flat(full.container.querySelector(".estimate-explanation"))).toContain("Based on 7 finished medium changes");
  });
});

describe("a work order's estimate and its baseline", () => {
  it("shows the current estimate and the baseline it is measured against, each labeled", () => {
    const { container } = render(<WorkEstimateView read={read("work-estimate")} />);
    expect(flat(container.querySelector('[data-band="estimate"]'))).toContain("Estimate confident");
    expect(flat(container.querySelector('[data-band="baseline"]'))).toContain("Baseline");
    expect(flat(container.querySelector('[data-band="baseline"]'))).toContain("Low confidence");
    expect(flat(container.querySelector('[data-band="baseline"]'))).toContain("p85 more than 4h0m0s");
  });

  it("says a work order with no estimate and no baseline in words, and prints nothing as the command does", () => {
    expect(printed(cliAbsent)).toEqual({ estimate: undefined, baseline: undefined });
    const { container } = render(<WorkEstimateView read={read("work-estimate-absent")} />);
    expect(container.querySelector("[data-band]")).toBeNull();
    expect(screen.getByText("No estimate was made for this work order.")).toBeTruthy();
    expect(screen.getByText("No baseline is recorded for this work order yet.")).toBeTruthy();
  });

  it("says a baseline that is missing beside an estimate that is not", () => {
    const view = { ...read("work-estimate"), baseline: null };
    const { container } = render(<WorkEstimateView read={view} />);
    expect(within(container).getByText("No baseline is recorded for this work order yet.")).toBeTruthy();
    expect(container.querySelector('[data-band="estimate"]')).toBeTruthy();
  });
});
