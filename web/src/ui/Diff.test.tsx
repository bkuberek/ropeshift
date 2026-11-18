import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Diff } from "./Diff";
import { lineDiff } from "./lineDiff";

describe("the diff", () => {
  it("shows each changed line with its sign in words as well as in color", () => {
    render(<Diff label="Policy change" lines={lineDiff("a\nb", "a\nc")} />);
    const table = screen.getByRole("table", { name: "Policy change" });
    const removed = within(table).getByText("b").closest("tr");
    const added = within(table).getByText("c").closest("tr");
    expect(removed?.className).toBe("del");
    expect(within(removed as HTMLElement).getByText("removed")).toBeTruthy();
    expect(added?.className).toBe("add");
    expect(within(added as HTMLElement).getByText("added")).toBeTruthy();
    expect(within(table).getByText("a").closest("tr")?.className).toBe("");
  });

  it("says so when nothing differs, and draws no table", () => {
    render(<Diff label="Policy change" lines={[]} />);
    expect(screen.getByText("No difference.")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("folds a long run of unchanged lines into one row that says how many, and keeps the lines around each change", () => {
    const body = (middle: string) => Array.from({ length: 30 }, (_, at) => (at === 15 ? middle : `line ${at}`)).join("\n");
    render(<Diff label="Policy change" lines={lineDiff(body("old"), body("new"))} context={2} />);
    const table = screen.getByRole("table", { name: "Policy change" });
    expect(within(table).getAllByRole("row")).toHaveLength(2 + 2 + 2 + 2);
    expect(within(table).getByText("line 13")).toBeTruthy();
    expect(within(table).getByText("line 17")).toBeTruthy();
    expect(within(table).queryByText("line 12")).toBeNull();
    expect(within(table).getByText("13 unchanged lines")).toBeTruthy();
    expect(within(table).getByText("12 unchanged lines")).toBeTruthy();
  });

  it("shows every line when none is held back, and folds nothing a context covers", () => {
    render(<Diff label="Policy change" lines={lineDiff("a\nb\nc\nd\ne", "a\nb\nX\nd\ne")} context={2} />);
    expect(within(screen.getByRole("table", { name: "Policy change" })).getAllByRole("row")).toHaveLength(6);
    expect(screen.queryByText(/unchanged lines?$/)).toBeNull();
  });

  it("keeps a line of markup as text", () => {
    render(<Diff label="Change" lines={lineDiff("", '<img src=x onerror="alert(1)">')} />);
    expect(screen.getByText('<img src=x onerror="alert(1)">')).toBeTruthy();
    expect(document.querySelector("img")).toBeNull();
  });
});
