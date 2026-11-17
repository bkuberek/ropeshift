import type { AdoptionPlan, AdoptionPlanRequest, LayoutShape, PlannedRepository, RepositoryAddReport, Restructure } from "../api/admin";

// What the Import page reckons of an adoption's plan, as `ropeshift adopt` reckons it (internal/cli/adopt.go). None of
// it decides anything the API decides: the plan route reads the layout, and the work order's creation refuses a
// restructuring it cannot keep.

// foldersOf is the folders a member wrote, one absolute path to a line.
export function foldersOf(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

// relativeFolder is the first folder written that is no absolute path, or an empty string.
export function relativeFolder(folders: string[]): string {
  return folders.find((folder) => !folder.startsWith("/")) ?? "";
}

// planRequest is what the CLI sends to read the plan: the first folder is the clone adopt runs in, and each other is a
// path beside it, a clone or a folder that holds clones.
export function planRequest(folders: string[], depth: number, leaveOut: string[] = []): AdoptionPlanRequest {
  const [root = "", ...paths] = folders;
  const request: AdoptionPlanRequest = { root, depth };
  if (paths.length > 0) {
    request.paths = paths;
  }
  if (leaveOut.length > 0) {
    request.leave_out = leaveOut;
  }
  return request;
}

// restructures says a shape offers something to restructure.
export const restructures = (shape: LayoutShape): boolean => shape !== "single";

// shapeSummary says what a shape is, and what restructuring does in it.
export function shapeSummary(shape: LayoutShape): string {
  switch (shape) {
    case "single":
      return "One repository, so there is nothing to restructure.";
    case "planned":
      return "The repository holds plan content and clones were found. Restructuring adds the manifest of the clones when none exists.";
    case "consolidate":
      return "A workspace whose ignored folder holds the design repository. Restructuring imports its history into the workspace.";
    case "workspace_files":
      return "Clones with no design repository to import. Restructuring adds the manifest, the ignore line, and a Makefile, and lists the moves.";
  }
}

// registrablePaths are the clones and paths a registration is asked for: every one the plan lists except a repository
// it leaves out of the registration.
export function registrablePaths(plan: AdoptionPlan): string[] {
  return plan.repositories.filter((row) => row.outcome !== "not_registered").map((row) => row.path);
}

// planningRefusal is why a registration of the repository the plan lives in would be refused, when the plan says so and
// that repository is not the clone adopt runs in.
export function planningRefusal(plan: AdoptionPlan): string {
  const path = plan.planning.path ?? "";
  if (path === "" || path === plan.layout.root) {
    return "";
  }
  const row = plan.repositories.find((one) => one.path === path);
  return row?.outcome === "refused" ? (row.reason ?? "no reason given") : "";
}

// restructureOf is what the adopt work carries of the plan a member chose to restructure by.
export function restructureOf(plan: AdoptionPlan): Restructure {
  const chosen: Restructure = { shape: plan.shape, digest: plan.digest };
  if (plan.shape !== "consolidate" || !plan.design) {
    return chosen;
  }
  chosen.design_path = plan.design.path;
  chosen.design_root = plan.design.root;
  const left = (plan.code_trees ?? []).filter((tree) => tree.left_out).map((tree) => tree.path);
  if (left.length > 0) {
    chosen.leave_out = left;
  }
  return chosen;
}

// registeredAt is the record of the clone a registration reported at a path, or empty when it reported none.
export function registeredAt(report: RepositoryAddReport, path: string): string {
  const added = report.repositories.find((row) => row.path === path && row.repository && row.outcome !== "refused");
  return added?.repository?.id ?? "";
}

// candidatesOf are the repositories the plan lives in may be one of: each the registration would add or already holds.
export function candidatesOf(plan: AdoptionPlan): PlannedRepository[] {
  return plan.repositories.filter((row) => (row.outcome === "registered" || row.outcome === "known") && row.id);
}

// leftOutAfter is the code trees a consolidation leaves out once a tree is checked or unchecked.
export function leftOutAfter(plan: AdoptionPlan, tree: string, leave: boolean): string[] {
  const left = (plan.code_trees ?? []).filter((one) => one.left_out && one.path !== tree).map((one) => one.path);
  return leave ? [...left, tree] : left;
}

// whatChanged names what differs between the plan a member read and the plan the layout gives now, for the refusal
// that shows the new one.
export function whatChanged(before: AdoptionPlan, now: AdoptionPlan): string[] {
  const out: string[] = [];
  if (before.shape !== now.shape) {
    out.push(`the shape is now ${now.shape}, not ${before.shape}`);
  }
  const was = new Set(before.repositories.map((row) => row.path));
  const is = new Set(now.repositories.map((row) => row.path));
  for (const path of is) {
    if (!was.has(path)) {
      out.push(`${path} was found`);
    }
  }
  for (const path of was) {
    if (!is.has(path)) {
      out.push(`${path} was not found`);
    }
  }
  const sections: [string, unknown, unknown][] = [
    ["the files it adds", before.files, now.files],
    ["the paths it takes", before.takes, now.takes],
    ["the collisions", before.collisions, now.collisions],
    ["the code trees", before.code_trees, now.code_trees],
    ["the moves", before.moves, now.moves],
    ["where the plan lives", before.planning, now.planning],
  ];
  for (const [name, a, b] of sections) {
    if (JSON.stringify(a ?? null) !== JSON.stringify(b ?? null)) {
      out.push(`${name} changed`);
    }
  }
  return out;
}
