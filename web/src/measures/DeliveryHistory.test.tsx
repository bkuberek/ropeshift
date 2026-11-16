import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import cliEmpty from "../../testdata/history-empty.cli.txt?raw";
import cliHistory from "../../testdata/history.cli.txt?raw";
import type { DeliveryHistory as History } from "../api/measures";
import { golden } from "../testing";
import { DeliveryHistoryView } from "./DeliveryHistory";
import { durationWords } from "./historyWords";

// The History tab says what a planning session sees in the words `ropeshift measures history` prints for the same
// records: this reads the command's own text, which a Go test wrote from the golden, and holds each group of the page
// to it.

type Printed = { section: string; label: string; finished: string; fragments: string[]; explanation: string };

const line = /^- (.+?): (\d+ finished); (delivery .+?); (lane time .+?); (cost .+?); (mean .+?); (.+?)\. (.*)$/;

// printed reads each group the command prints, under the heading of its section.
function printed(text: string): Printed[] {
  const out: Printed[] = [];
  let section = "";
  for (const row of text.split("\n")) {
    if (row.startsWith("### ")) {
      section = row.slice(4);
    } else if (row.startsWith("- ")) {
      const found = line.exec(row);
      expect(found, row).toBeTruthy();
      const [, label = "", finished = "", delivery = "", lane = "", cost = "", rounds = "", change = "", said = ""] = found ?? [];
      out.push({ section, label, finished, fragments: [delivery, lane, cost, rounds, change], explanation: said.replace(/^Confident\. /, "") });
    }
  }
  return out;
}

const flat = (element: Element | null | undefined) => (element?.textContent ?? "").replaceAll(/\s+/g, " ");
const read = (name = "history") => golden<History>(name);
const shown = (view: History = read()) => render(<DeliveryHistoryView view={view} />);

describe("the delivery history a planning session sees", () => {
  const commands = printed(cliHistory);

  it("lists each group the command prints, under its section, and no other", () => {
    const { container } = shown();
    const rows = [...container.querySelectorAll("[data-group]")].map((one) => `${one.closest("[data-section]")?.getAttribute("data-section")}: ${one.getAttribute("data-group")}`);
    expect(rows).toEqual(commands.map((one) => `${one.section}: ${one.label}`));
  });

  it("says each group's count, delivery time, lane time, cost, rounds, change size, and explanation in the command's words", () => {
    const { container } = shown();
    for (const command of commands) {
      const row = flat(container.querySelector(`[data-section="${command.section}"] [data-group="${command.label}"]`));
      expect(row, command.label).toContain(command.finished);
      for (const fragment of command.fragments) {
        expect(row, `${command.label}: ${fragment}`).toContain(fragment);
      }
      expect(row, `${command.label}: explanation`).toContain(command.explanation);
    }
  });

  it("puts the Low confidence badge on a group under the minimum and none on a confident one", () => {
    const { container } = shown();
    const badges = [...container.querySelectorAll("[data-group]")].map((one) => flat(one.querySelector(".badge")));
    expect(badges).toEqual(["", "Low confidence", "Low confidence", "", "Low confidence", "Low confidence"]);
  });

  it("says a cost lower bound as at least, and an open p85 as more than, in the words of the figures", () => {
    const { container } = shown();
    const small = flat(container.querySelector('[data-section="By kind and size"] [data-group="change, small"]'));
    expect(small).toContain("cost p50 at least $4.00, p85 at least $7.00");
    const rework = flat(container.querySelector('[data-section="By kind and size"] [data-group="rework, small"]'));
    expect(rework).toContain("delivery p50 3h, p85 more than 4h");
    expect(rework).toContain("cost p50 $3.00, p85 more than $4.00");
  });

  it("is labeled as what the planning session sees, and says which work its groups read", () => {
    const { container } = shown();
    expect(flat(container.querySelector("h3"))).toBe("What the planning session sees");
    expect(flat(container)).toContain("Each group reads its 50 most recent work orders that finished done");
    expect(flat(container)).toContain("a group under 10 is labeled low confidence");
  });

  it("names a repository as the project names it, and by its ID where it has no name", () => {
    const view = read();
    view.repositories = { "harbor-web": "Harbor web" };
    const { container } = shown(view);
    expect(container.querySelector('[data-group="change, medium, implement, Harbor web"]')).toBeTruthy();
    expect(container.querySelector('[data-group="change, small, implement, harbor-api"]')).toBeTruthy();
  });

  it("says a project with no finished work has no history, and shows no group", () => {
    expect(cliEmpty).toContain("No work has finished in this project yet, so it has no history.");
    const { container } = shown(read("history-empty"));
    expect(flat(container)).toContain("No finished work yet. Work orders will have no estimate until comparable work finishes.");
    expect(container.querySelector("[data-group]")).toBeNull();
  });
});

describe("a duration as the history says it", () => {
  it("reads hours and minutes from an hour on, minutes and seconds from a minute on, and seconds below, rounding up", () => {
    const minute = 60_000;
    expect([0, 59_400, 59_500, minute, 90 * minute, 60 * minute, 61 * minute - 31_000].map(durationWords)).toEqual(["0s", "59s", "1m", "1m", "1h30m", "1h", "1h"]);
    expect([90_000, 3_599_500, 3 * 60 * minute + 29_999].map(durationWords)).toEqual(["1m30s", "1h", "3h1m"]);
  });
});
