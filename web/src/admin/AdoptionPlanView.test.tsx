import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AdoptionPlan } from "../api/admin";
import { golden } from "../testing";
import { PlanSummary, RestructureChoice } from "./AdoptionPlanView";

// The plan of an adoption in each shape, as the plan route answered it to the Go test that wrote it.
const plan = (name: string) => golden<AdoptionPlan>(`adoption-plan-${name}`);

function show(shown: AdoptionPlan, over: { chosen?: boolean; refusal?: string; onChoose?: (chosen: boolean) => void; onLeaveOut?: (tree: string, leave: boolean) => void } = {}) {
  return render(
    <>
      <PlanSummary plan={shown} />
      <RestructureChoice plan={shown} chosen={over.chosen ?? false} busy={false} refusal={over.refusal ?? ""} onChoose={over.onChoose ?? (() => undefined)} onLeaveOut={over.onLeaveOut ?? (() => undefined)} />
    </>,
  );
}

const rows = (label: string) => within(screen.getByRole("region", { name: label })).getAllByRole("row").slice(1);

describe("the plan of an adoption, as the dry run prints it", () => {
  it("shows a single repository with nothing to restructure", () => {
    show(plan("single"));
    expect(screen.getByText("single")).toBeTruthy();
    expect(screen.getByText(/One repository\. Restructuring adds the instruction files it lacks/)).toBeTruthy();
    expect(rows("Clones found")).toHaveLength(1);
    expect(within(rows("Clones found")[0] as HTMLElement).getByText("Will register")).toBeTruthy();
    expect(screen.queryByRole("group", { name: "Restructure" })).toBeNull();
  });

  it("offers a single repository that lacks instruction files a restructuring for those two files", () => {
    show(plan("single-lacking"));
    expect(screen.getByRole("group", { name: "Restructure" })).toBeTruthy();
    expect(rows("Files the layout lacks").map((row) => row.textContent)).toEqual([
      "AddAGENTS.mdthe project's instructions for agents",
      "AddCLAUDE.mdimports AGENTS.md",
    ]);
    expect(screen.queryByRole("region", { name: "Files added" })).toBeNull();
    expect(screen.queryByText(/Run these yourself/)).toBeNull();
  });

  it("shows a planned layout with the manifest it would add, the files it lacks, and a step for each code repository", () => {
    show(plan("planned"));
    expect(screen.getByText("planned")).toBeTruthy();
    expect(screen.getByText(/the clone adopt runs in/)).toBeTruthy();
    const clones = rows("Clones found");
    expect(clones).toHaveLength(3);
    expect(within(clones[0] as HTMLElement).getByText(/plan content, 2 Markdown documents/)).toBeTruthy();
    expect(within(clones[1] as HTMLElement).getByText(/in a folder the clone ignores/)).toBeTruthy();
    const files = rows("Files added");
    expect(files.map((row) => row.textContent)).toEqual([
      expect.stringContaining("plan/repositories.md"),
      "Append a line to.gitignore/.ropeshift/worktrees/",
    ]);
    expect(rows("Files the layout lacks").map((row) => row.textContent)).toEqual([
      "AddMakefilesetup, fetch, and status over the manifest",
      "AddAGENTS.mdthe project's instructions for agents",
      "AddCLAUDE.mdimports AGENTS.md",
    ]);
    expect(screen.getByRole("region", { name: "Commands that write the instructions of api" }).textContent).toBe("ropeshift template show repository/agents.md > /work/ws/repos/api/AGENTS.md");
    expect(screen.getByRole("region", { name: "Commands that write the instructions of web" })).toBeTruthy();
    expect(screen.getByText(/Ropeshift writes to no clone but the planning repository's/)).toBeTruthy();
    expect(screen.queryByRole("region", { name: /Paths taken/ })).toBeNull();
    expect(screen.queryByRole("region", { name: "Collisions" })).toBeNull();
  });

  it("lists, for a consolidation, the design repository as not registered, every path taken, every collision, and a check box per code tree", () => {
    show(plan("consolidate"));
    expect(screen.getByText("consolidate")).toBeTruthy();
    expect(screen.getByText("Design repository")).toBeTruthy();
    expect(screen.getByText(/it holds plan content \(2\)/)).toBeTruthy();
    expect(within(rows("Clones found")[2] as HTMLElement).getByText(/Not registered: its history is imported into ws/)).toBeTruthy();
    expect(rows("Paths taken from design").map((row) => row.textContent)).toEqual(["docs/a.md", "plan/goal.md", "tools/go.mod"]);
    expect(rows("Collisions").map((row) => row.textContent)).toEqual([".gitignoreBoth, joined"]);
    expect(rows("Files added").map((row) => row.textContent)).toEqual([
      "Addplan/repositories.mdthe manifest of the project's repositories",
      "Append a line to.gitignore/.ropeshift/worktrees/",
    ]);
    expect(rows("Files the layout lacks").map((row) => row.textContent)).toEqual([
      "AddMakefilesetup, fetch, and status over the manifest",
      "AddAGENTS.mdthe project's instructions for agents",
      "AddCLAUDE.mdimports AGENTS.md",
    ]);
    const tree = screen.getByRole("checkbox", { name: /Leave out tools \(tools\/go\.mod\)/ }) as HTMLInputElement;
    expect(tree.checked).toBe(false);
    expect(tree.disabled).toBe(false);
  });

  it("shows a code tree that is left out as checked, and one that is the whole repository as one that cannot be", () => {
    const left = plan("consolidate-left-out");
    show(left);
    expect((screen.getByRole("checkbox", { name: /Leave out tools/ }) as HTMLInputElement).checked).toBe(true);
    cleanup();
    show({ ...left, code_trees: [{ path: ".", build_file: "go.mod", whole: true }] });
    const whole = screen.getByRole("checkbox", { name: /Leave out \./ }) as HTMLInputElement;
    expect(whole.disabled).toBe(true);
    expect(screen.getByText(/the whole repository, which cannot be left out/)).toBeTruthy();
  });

  it("lists, for workspace files, the files added and the files the layout lacks, and each move as the commands the person runs", () => {
    show(plan("workspace-files"));
    expect(screen.getByText("workspace_files")).toBeTruthy();
    expect(rows("Files added").map((row) => row.textContent)).toEqual(["Addplan/repositories.mdthe manifest of the project's repositories"]);
    expect(rows("Files the layout lacks").map((row) => row.textContent)).toEqual([
      "Add.gitignoreignores the folder of clones and the folder lane worktrees go in",
      "AddMakefilesetup, fetch, and status over the manifest",
      "AddAGENTS.mdthe project's instructions for agents",
      "AddCLAUDE.mdimports AGENTS.md",
    ]);
    const commands = screen.getByRole("region", { name: "Commands that move api" });
    expect(commands.textContent).toBe("mv /work/api /work/design/repos/api\nropeshift repository add /work/design/repos/api");
    expect(screen.getByRole("region", { name: "Commands that move web" })).toBeTruthy();
    expect(screen.getByText(/Ropeshift never moves a clone/)).toBeTruthy();
  });

  it("says where the plan lives is undecided, and offers no restructuring, when nothing settles it", () => {
    show(plan("undecided"), { refusal: "Nothing in the layout settles where the plan lives." });
    expect(screen.getByText(/Undecided/)).toBeTruthy();
    expect((screen.getByRole("checkbox", { name: "Restructure the planning repository as listed" }) as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByText("Nothing in the layout settles where the plan lives.")).toBeTruthy();
  });

  it("shows a refused repository with its reason, the notes of the detection, and the digest", () => {
    const refused: AdoptionPlan = {
      ...plan("single"),
      repositories: [...plan("single").repositories, { path: "/work/gone", outcome: "refused", reason: "no git clone is there" }],
      notes: ["No repository is known to hold the plan; name it with --planning NAME."],
    };
    show(refused);
    expect(within(rows("Clones found")[1] as HTMLElement).getByText("Refused: no git clone is there")).toBeTruthy();
    expect(screen.getByRole("list", { name: "Notes" }).textContent).toContain("No repository is known to hold the plan");
    expect(document.querySelector(".digest")?.getAttribute("title")).toBe(refused.digest);
  });

  it("tells a planning repository that already holds what a restructuring adds, that there is nothing to list", () => {
    show({ ...plan("planned"), files: null, steps: null });
    expect(screen.getByText(/already holds everything a restructuring adds/)).toBeTruthy();
  });

  it("reports the choice and each code tree checked, and leaves the plan to the page", async () => {
    const onChoose = vi.fn();
    const onLeaveOut = vi.fn();
    show(plan("consolidate"), { onChoose, onLeaveOut });
    await userEvent.click(screen.getByRole("checkbox", { name: "Restructure the planning repository as listed" }));
    expect(onChoose).toHaveBeenCalledWith(true);
    await userEvent.click(screen.getByRole("checkbox", { name: /Leave out tools/ }));
    expect(onLeaveOut).toHaveBeenCalledWith("tools", true);
  });
});
