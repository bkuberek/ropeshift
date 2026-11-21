import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { NeedItem, NeedsYou as View } from "../api/types";
import { CurrentProject } from "../app/route";
import { golden, here, testProject } from "../testing";
import { cardKindOf, cardKinds, contextOf, filterOf, headingOf, keyOf, kindOf } from "./kinds";
import { NeedsYou } from "./NeedsYou";

type ConflictItem = Extract<NeedItem, { kind: "conflict" }>;

// The item a Go test wrote from a real ledger: a lane whose branch changed a file a person left uncommitted and a file a
// person pushed, with the three ways to settle it (FR-UI-14).
const found = (): ConflictItem => {
  const item = golden<View>("conflict-needs-you").items.find((row): row is ConflictItem => row.kind === "conflict");
  if (!item) {
    throw new Error("the golden holds no conflict");
  }
  return JSON.parse(JSON.stringify(item)) as ConflictItem;
};

function show(item: ConflictItem) {
  const view: View = { ...golden<View>("conflict-needs-you"), items: [item] };
  window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(keyOf(item))}`);
  render(
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={async () => {}} />
    </CurrentProject.Provider>,
  );
  return screen.getByRole("article", { name: "Retry within a budget changed files a person changed too" });
}

describe("a conflict between a lane and a person's edits, in Needs you", () => {
  it("is a card kind of its own, under Decisions, titled by the work", () => {
    const item = found();
    expect(cardKinds).toContain("conflict");
    expect(cardKindOf(item)).toBe("conflict");
    expect(filterOf(item)).toBe("decisions");
    expect(kindOf(item)).toEqual({ icon: "branch", name: "Conflict" });
    expect(headingOf(item)).toBe("Retry within a budget changed files a person changed too");
    expect(contextOf(item)).toBe("wo-1 · lane lane-wo-1 · app · 2 files");
    expect(keyOf(item)).toBe("conflict:wo-1");
  });

  it("names both sides of each file, and says ropeshift overwrites neither", () => {
    const card = show(found());
    const sides = within(card).getByRole("table", { name: "Files both sides changed" });
    const rowOf = (path: string) => within(sides).getByText(path).closest("tr") as HTMLElement;
    expect(rowOf("plans/plan.md").textContent).toContain("Uncommitted in the clone /srv/repository-1");
    expect(rowOf("docs/guide.md").textContent).toContain("Pushed to origin/main");
    expect(rowOf("plans/plan.md").textContent).toContain("lane/lane-wo-1");
    expect(card.textContent).toContain("Ropeshift settles none of it and overwrites neither side");
    expect(card.textContent).toContain(found().conflict.clears);
    expect(card.textContent).toContain("The commands are for POSIX sh, bash or zsh.");
  });

  it("offers each way to settle it as its exact commands, copied as shown", async () => {
    const copy = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    const item = found();
    const card = show(item);
    for (const option of item.conflict.options) {
      const group = within(card).getByRole("group", { name: option.title });
      expect(within(group).getByText(option.commands[option.commands.length - 1]!.trim(), { exact: false })).toBeTruthy();
      expect(within(group).getByRole("region", { name: `Commands for ${option.title}` }).tabIndex).toBe(0);
    }
    const stop = item.conflict.options[2]!;
    await userEvent.click(within(card).getByRole("button", { name: `Copy the commands: ${stop.title}` }));
    expect(copy).toHaveBeenCalledWith(stop.commands.join("\n"));
  });

  it("shows a path and a title as text, with no escape a tool printed", () => {
    const item = found();
    item.conflict.title = "Retry within a budget\u001b[31m";
    item.conflict.paths[0]!.path = "docs/\u202eguide.md";
    const card = show(item);
    expect(card.textContent).not.toMatch(/[\u001b\u202e]/);
    expect(within(card).getByText("docs/guide.md")).toBeTruthy();
  });
});
