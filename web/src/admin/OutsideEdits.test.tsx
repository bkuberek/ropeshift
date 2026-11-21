import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { OutsideEdits } from "../api/admin";
import { openPage } from "../opening";
import { golden, refusal, serveViews } from "../testing";

// The files of each registered clone that intake and lanes cannot read, on Repositories and Plans (FR-UI-14), as the API
// serves them from a real ledger: each path with its state and the step it needs, and each step's exact commands with
// Copy.

const served = () => JSON.parse(JSON.stringify(golden<OutsideEdits>("outside-edits"))) as OutsideEdits;

function serving(list: OutsideEdits | { status: number; body: unknown }) {
  const answer = "clones" in list ? { status: 200, body: list } : list;
  return serveViews((call) => (call.method === "GET" && call.path.split("?")[0] === "/v1/outside-edits" ? answer : undefined));
}

const readsOf = (sent: ReturnType<typeof serving>) => sent.filter((call) => call.path.startsWith("/v1/outside-edits"));

const region = () => screen.findByRole("region", { name: "Files ropeshift cannot read" });

describe("the files ropeshift cannot read", () => {
  it("lists each clone's paths by path, with each one's state and next step", async () => {
    serving(served());
    openPage("/repositories");
    const files = await region();
    const table = await within(files).findByRole("table", { name: "Files in app" });
    const rowOf = (path: string) => within(table).getByText(path).closest("tr") as HTMLElement;
    expect(rowOf("plans/retry.md").textContent).toContain("Uncommitted");
    expect(rowOf("plans/retry.md").textContent).toContain("Commit them to a new branch");
    expect(rowOf("plans/notes on retries.md").textContent).toContain("Untracked");
    expect(rowOf("docs/design.md").textContent).toContain("Unpushed");
    expect(rowOf("docs/design.md").textContent).toContain("Push the commits");
    expect(rowOf("plans/dm.yrter").textContent).toContain("holds a character that is not seen");
    expect(within(files).getByText("Nothing in this clone is out of reach of intake and lanes.")).toBeTruthy();
    expect(files.textContent).toContain("The commands are for POSIX sh, bash or zsh.");
    expect(within(files).getByText(/This machine keeps no clone of it/)).toBeTruthy();
  });

  it("copies a step's exact commands, every line of them", async () => {
    const copy = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    const list = served();
    serving(list);
    openPage("/repositories");
    const files = await region();
    const step = list.clones[0]!.steps[0]!;
    const block = await within(files).findByRole("group", { name: `${step.title} in app` });
    expect(within(block).getByText(step.commands[3]!.trim(), { exact: false })).toBeTruthy();
    const commands = within(block).getByRole("region", { name: `Commands for ${step.title}` });
    expect(commands.tabIndex).toBe(0);
    await userEvent.click(within(block).getByRole("button", { name: `Copy the commands: ${step.title}` }));
    expect(copy).toHaveBeenCalledWith(step.commands.join("\n"));
  });

  it("shows a path and a clone's name as text, with no escape a tool printed", async () => {
    const list = served();
    list.clones[0]!.name = "app\u001b[31m";
    list.clones[0]!.edits[0]!.path = "docs/\u202edesign.md";
    serving(list);
    openPage("/repositories");
    const files = await region();
    expect(await within(files).findByRole("table", { name: "Files in app" })).toBeTruthy();
    expect(files.textContent).not.toMatch(/[\u001b\u202e]/);
    expect(within(files).getByText("docs/design.md")).toBeTruthy();
  });

  it("lists on Plans only the planning repository's clone, with the copy into the planning branch", async () => {
    serving(served());
    openPage("/plans");
    const files = await region();
    expect(await within(files).findByRole("table", { name: "Files in app" })).toBeTruthy();
    expect(within(files).queryByText("app-2")).toBeNull();
    expect(within(files).getByRole("group", { name: "Copy them into the planning branch lane/lane-plan-1 in app" })).toBeTruthy();
  });

  it("asks for a fresh reading when Read again is pressed, and for none when it reads on its own", async () => {
    const sent = serving(served());
    openPage("/repositories");
    const files = await region();
    expect(await within(files).findByRole("table", { name: "Files in app" })).toBeTruthy();
    expect(readsOf(sent).map((call) => call.path)).toEqual(["/v1/outside-edits"]);
    await userEvent.click(within(files).getByRole("button", { name: "Read again" }));
    await waitFor(() => expect(readsOf(sent)).toHaveLength(2));
    expect(readsOf(sent).map((call) => call.path)).toEqual(["/v1/outside-edits", "/v1/outside-edits?fresh=1"]);
  });

  it("says why the clones could not be read", async () => {
    serving(refusal(503, "repository_reads_unavailable", "reading the edits in a clone: this daemon reads no clone"));
    openPage("/repositories");
    const files = await region();
    expect(await within(files).findByText(/this daemon reads no clone/)).toBeTruthy();
  });
});
