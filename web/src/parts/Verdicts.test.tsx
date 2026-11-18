import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Standing } from "../api/types";
import { golden } from "../testing";
import { Verdicts } from "./Verdicts";

const note = (seq: number, citations?: string[]) => ({
  point: "authority" as const,
  reasons: `The workflow gains write access, ${seq}.`,
  citations,
  review_id: "rv-1",
  work_order_id: "wo-ruled",
  commit: "cab321",
  seq,
});

// A review's notes are read with its verdicts, and also where it left none: a person who decides without the
// verdicts still reads what the review noted of the diff.
describe("the notes of a review", () => {
  it("show with no verdict, with no sentence that the review has not ended or failed", () => {
    const standing = golden<Standing>("acceptance-reviewed");
    standing.verdicts = [];
    standing.reviews = [];
    standing.notes = [note(0), note(1)];
    render(<Verdicts standing={standing} />);
    const region = screen.getByRole("region", { name: "What the review found" });
    expect(within(within(region).getByRole("list", { name: "Security notes" })).getAllByRole("listitem")).toHaveLength(2);
    expect(within(region).queryByRole("list", { name: "Verdicts" })).toBeNull();
    expect(region.textContent).not.toMatch(/has not ended|failed/);
  });

  it("show beside the sentence that a review failed, when it left notes and no verdict", () => {
    const standing = golden<Standing>("acceptance-reviewed");
    standing.verdicts = [];
    standing.reviews = [{ id: "rv-1", state: "failed", attempt: 2 }];
    standing.notes = [note(0)];
    render(<Verdicts standing={standing} />);
    const region = screen.getByRole("region", { name: "What the review found" });
    expect(within(region).getByText(/The review of this work failed/)).toBeTruthy();
    expect(within(region).getByRole("list", { name: "Security notes" })).toBeTruthy();
  });

  it("say nothing when there is no verdict, no note, and no open or failed review", () => {
    const standing = golden<Standing>("acceptance-reviewed");
    standing.verdicts = [];
    standing.reviews = [];
    delete standing.notes;
    const { container } = render(<Verdicts standing={standing} />);
    expect(container.textContent).toBe("");
  });

  it("keep a citation each under its own key, though two cite the same place", () => {
    const standing = golden<Standing>("acceptance-reviewed");
    standing.notes = [note(0, ["config.yaml:4", "config.yaml:4"])];
    render(<Verdicts standing={standing} />);
    const cites = screen.getByRole("list", { name: "Citations for the authority note" });
    expect(within(cites).getAllByRole("listitem")).toHaveLength(2);
  });
});
