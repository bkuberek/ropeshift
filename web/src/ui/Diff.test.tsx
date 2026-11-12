import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Diff } from "./Diff";
import { lineDiff } from "./diff";

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

  it("keeps a line of markup as text", () => {
    render(<Diff label="Change" lines={lineDiff("", '<img src=x onerror="alert(1)">')} />);
    expect(screen.getByText('<img src=x onerror="alert(1)">')).toBeTruthy();
    expect(document.querySelector("img")).toBeNull();
  });
});
