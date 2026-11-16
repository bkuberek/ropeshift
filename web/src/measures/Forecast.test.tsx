import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { PlanForecast } from "../api/measures";
import cliForecast from "../../testdata/forecast.cli.txt?raw";
import cliPending from "../../testdata/forecast-pending.cli.txt?raw";
import cliSuperseded from "../../testdata/forecast-superseded.cli.txt?raw";
import type { Roadmap } from "../api/types";
import { openPage } from "../opening";
import { golden, here, refusal, serveViews } from "../testing";
import { PlanForecastView } from "./Forecast";

// The page says a forecast in the words `ropeshift plan forecast` prints for the same records: this reads the
// command's own text, which a Go test wrote from the golden, and holds each forecast of the page to it, its
// dates and costs, its what-ifs, its explanation, and what makes it again.

type Printed = { whose: string; label: string; cells: string[]; lines: string[] };

// printed reads the forecasts the command prints: the line of each, then the lines indented under it.
function printed(text: string): Printed[] {
  const out: Printed[] = [];
  for (const line of text.split("\n")) {
    if (line.startsWith("forecast\t")) {
      const [, whose, label, ...cells] = line.split("\t");
      out.push({ whose: whose ?? "", label: label ?? "", cells, lines: [] });
    } else if (line.startsWith("\t") && out.length > 0 && !line.startsWith("\tNo estimate")) {
      out.at(-1)?.lines.push(line.slice(1));
    } else if (line.startsWith("estimate\t")) {
      break;
    }
  }
  return out;
}

// said reads what the command prints as a sentence the page begins with a capital.
const said = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);
const flat = (element: Element | null | undefined) => (element?.textContent ?? "").replaceAll(/\s+/g, " ");

const titles: Record<string, string> = { build: "Build the core", ship: "Ship the approval page", guide: "Guide a newcomer" };

function forecasts(view: PlanForecast = golden<PlanForecast>("forecast")) {
  return render(<PlanForecastView forecast={view} titleOf={(id) => titles[id] ?? id} />);
}

describe("a plan's forecast", () => {
  const commands = printed(cliForecast);

  it("shows the forecast of the plan and of each milestone the command prints, and no other", () => {
    const { container } = forecasts();
    expect([...container.querySelectorAll("[data-forecast]")].map((one) => one.getAttribute("data-forecast"))).toEqual(commands.map((one) => one.whose));
  });

  it("says each date, cost, lower bound, what-if, explanation, and basis as the command prints them", () => {
    const { container } = forecasts();
    for (const command of commands) {
      const shown = flat(container.querySelector(`[data-forecast="${command.whose}"]`));
      for (const cell of command.cells) {
        expect(shown, `${command.whose}: ${cell}`).toContain(cell);
      }
      for (const line of command.lines) {
        for (const part of line.split("; ")) {
          // A what-if begins a sentence on the page, and the rest reads as the command prints it.
          expect(shown, `${command.whose}: ${part}`).toContain(part.startsWith("with ") ? said(part) : part);
        }
      }
    }
  });

  it("puts the Low confidence badge before Forecast for a low forecast, and none on a confident one or one with no history", () => {
    const { container } = forecasts();
    for (const command of commands) {
      const range = container.querySelector(`[data-forecast="${command.whose}"] .forecast-range`);
      const first = range?.firstElementChild;
      if (command.label === "low") {
        expect(flat(first), command.whose).toBe("Low confidence");
        expect(flat(range).indexOf("Low confidence")).toBeLessThan(flat(range).indexOf("Forecast"));
      } else {
        expect(flat(range), command.whose).not.toContain("Low confidence");
      }
    }
  });

  it("says a forecast with no history in the command's words, with its explanation and no dates", () => {
    const { container } = forecasts();
    const none = commands.find((command) => command.label === "no history");
    expect(none).toBeTruthy();
    const shown = flat(container.querySelector(`[data-forecast="${none?.whose}"]`));
    expect(shown).toContain("Forecast no history");
    expect(shown).toContain("No forecast: none of the 2 unfinished work orders has finished comparable work.");
    expect(shown).not.toContain("p50");
  });

  it("names a milestone by its title, and the plan's own as the plan", () => {
    forecasts();
    expect(screen.getByText("Build the core")).toBeTruthy();
    expect(screen.getByText("Plan")).toBeTruthy();
  });

  it("says what-ifs for a plan with no lane cap without the lane what-if, as the command does", () => {
    const { container } = forecasts();
    const open = flat(container.querySelector('[data-forecast="milestone ship"]'));
    expect(open).not.toContain("one more lane");
    expect(open).toContain("With each wait for a person cut to 30m0s, p85 2025-11-20 14:46 UTC or later");
    expect(open).toContain("no lane cap");
  });

  it("says pending as the command does, with no badge, no range, and no explanation", () => {
    const { container } = forecasts(golden<PlanForecast>("forecast-pending"));
    const line = cliPending.split("\n").find((one) => one.startsWith("forecast pending")) ?? "";
    expect(flat(container)).toBe(said(line));
    expect(container.querySelector("[data-forecast]")).toBeNull();
  });

  it("says a version a newer one replaced as the command does", () => {
    const { container } = forecasts(golden<PlanForecast>("forecast-superseded"));
    const line = cliSuperseded.split("\n").find((one) => one.startsWith("no forecast")) ?? "";
    expect(flat(container)).toBe(said(line));
  });

  it("says pending for a version the tick forecast with nothing to show, as the command does", () => {
    const { container } = forecasts({ ...golden<PlanForecast>("forecast"), forecasts: [] });
    expect(flat(container)).toBe("Forecast pending: the daemon's forecast tick has not forecast version 6 yet");
  });
});

describe("the forecast on a plan's page", () => {
  it("shows the plan's forecast and each milestone's under Forecast, with the version it is of", async () => {
    const sent = serveViews();
    openPage(here("/plans?plan=generated-20"));
    const heading = await screen.findByRole("heading", { level: 2, name: "Forecast" });
    const section = heading.closest("section") as HTMLElement;
    expect(await within(section).findByText(/Version 6/)).toBeTruthy();
    expect(within(section).getAllByText(/^Forecast$/).length).toBeGreaterThan(1);
    expect(flat(section)).toContain("p85 2025-11-16 13:33 UTC or later");
    expect(flat(section)).toContain("With one more lane, p85 2025-11-15 23:40 UTC or later");
    expect(sent.some((call) => call.method === "GET" && /^\/v1\/plans\/generated-20\/forecast$/.test(call.path))).toBe(true);
  });

  it("says pending while the tick has not forecast the version", async () => {
    serveViews((call) => (/\/forecast$/.test(call.path) ? { status: 200, body: golden("forecast-pending") } : undefined));
    openPage(here("/plans?plan=generated-20"));
    expect(await screen.findByText("Forecast pending: the daemon's forecast tick has not forecast version 6 yet")).toBeTruthy();
  });

  it("says why it cannot read a forecast, in the API's words, and keeps the page", async () => {
    serveViews((call) => (/\/forecast$/.test(call.path) ? refusal(404, "not_found", "plan generated-20 has no version yet") : undefined));
    openPage(here("/plans?plan=generated-20"));
    expect((await screen.findByRole("alert")).textContent).toContain("plan generated-20 has no version yet");
    expect(screen.getByRole("heading", { level: 2, name: "Contracts" })).toBeTruthy();
  });
});

describe("the forecast on the Roadmap", () => {
  const factory = () => golden<Roadmap>("roadmap").goals.flatMap((goal) => goal.plans).find((row) => row.plan.title === "The factory floor");

  it("shows the plan's forecast range under the plan, and each milestone's beside it with its badge and explanation", async () => {
    expect(factory()?.milestones.map((one) => one.id)).toEqual(["build", "ship", "guide"]);
    const sent = serveViews();
    openPage(here("/roadmap"));
    const plan = await screen.findByRole("group", { name: "Forecast of The factory floor" });
    await within(plan).findByText(/p85 2025-11-16 13:33 UTC or later/);
    expect(flat(plan)).toContain("p85 2025-11-16 13:33 UTC or later");
    expect(within(plan).getByText("Low confidence")).toBeTruthy();
    const milestones = screen.getAllByRole("list", { name: "Milestones" })[0] as HTMLElement;
    const build = within(milestones).getByText("Build the core").closest("li") as HTMLElement;
    expect(flat(build)).toContain("p50 2025-11-14 19:53 UTC");
    expect(flat(build)).not.toContain("Low confidence");
    const ship = within(milestones).getByText("Ship the approval page").closest("li") as HTMLElement;
    expect(flat(ship)).toContain("Low confidence");
    expect(flat(ship)).toContain("It leaves out 1 work order with no history");
    const guide = within(milestones).getByText("Guide a newcomer").closest("li") as HTMLElement;
    expect(flat(guide)).toContain("Forecast no history");
    expect(flat(guide)).toContain("No forecast: none of the 2 unfinished work orders has finished comparable work.");
    expect(sent.filter((call) => /^\/v1\/plans\/[^/]+\/forecast$/.test(call.path)).length).toBeGreaterThan(0);
  });

  it("keeps the what-ifs and the basis of a milestone's forecast one press away", async () => {
    serveViews();
    openPage(here("/roadmap"));
    const milestones = (await screen.findAllByRole("list", { name: "Milestones" }))[0] as HTMLElement;
    const build = within(milestones).getByText("Build the core").closest("li") as HTMLElement;
    const more = (await within(build).findByText("What-ifs and how it was made")).closest("details") as HTMLDetailsElement;
    expect(more.open).toBe(false);
    expect(flat(more)).toContain("With one more lane, p85 2025-11-15 07:00 UTC");
    expect(flat(more)).toContain("seed sha256:forecast-seed, 2000 trials, at most 2 lanes, revision 412");
  });

  it("says pending beside the plan, and shows no range or badge, while the tick has not forecast it", async () => {
    serveViews((call) => (/\/forecast$/.test(call.path) ? { status: 200, body: golden("forecast-pending") } : undefined));
    openPage(here("/roadmap"));
    const plan = await screen.findByRole("group", { name: "Forecast of The factory floor" });
    expect(flat(plan)).toBe("Forecast pending: the daemon's forecast tick has not forecast version 6 yet");
  });

  it("shows the roadmap when a plan has no forecast to read", async () => {
    serveViews((call) => (/\/forecast$/.test(call.path) ? refusal(404, "not_found", "plan has no version yet") : undefined));
    openPage(here("/roadmap"));
    expect(await screen.findByRole("heading", { level: 2, name: "Run the factory" })).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
