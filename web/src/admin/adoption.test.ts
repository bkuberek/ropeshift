import { describe, expect, it } from "vitest";

import type { AdoptionPlan } from "../api/admin";
import { golden } from "../testing";
import { candidatesOf, foldersOf, leftOutAfter, planningRefusal, planRequest, registeredAt, registrablePaths, relativeFolder, restructureOf, whatChanged } from "./adoption";

const plan = (name: string) => golden<AdoptionPlan>(`adoption-plan-${name}`);

describe("the folders of an adoption", () => {
  it("are the lines written, trimmed, with the blank ones dropped", () => {
    expect(foldersOf("  /a \n\n/b\n \n")).toEqual(["/a", "/b"]);
    expect(foldersOf("")).toEqual([]);
  });

  it("name the first that is no absolute path", () => {
    expect(relativeFolder(["/a", "b/c", "d"])).toBe("b/c");
    expect(relativeFolder(["/a"])).toBe("");
  });

  it("make the request the CLI sends: the first is the clone adopt runs in, and the others are its paths", () => {
    expect(planRequest(["/a", "/b", "/c"], 2)).toEqual({ root: "/a", depth: 2, paths: ["/b", "/c"] });
    expect(planRequest(["/a"], 1)).toEqual({ root: "/a", depth: 1 });
    expect(planRequest(["/a"], 1, ["tools"])).toEqual({ root: "/a", depth: 1, leave_out: ["tools"] });
  });
});

describe("what a plan registers and restructures by", () => {
  it("registers every repository the plan lists except the one it leaves out", () => {
    expect(registrablePaths(plan("consolidate"))).toEqual(["/work/ws", "/work/ws/repos/api"]);
    expect(registrablePaths(plan("workspace-files"))).toEqual(["/work/api", "/work/design", "/work/web"]);
  });

  it("carries the design repository and the trees left out of a consolidation, with the digest", () => {
    const left = plan("consolidate-left-out");
    expect(restructureOf(left)).toEqual({ shape: "consolidate", digest: left.digest, design_path: "/work/ws/repos/design", design_root: "design", leave_out: ["tools"] });
    const kept = plan("consolidate");
    expect(restructureOf(kept)).toEqual({ shape: "consolidate", digest: kept.digest, design_path: "/work/ws/repos/design", design_root: "design" });
  });

  it("carries the shape and the digest alone for any other shape", () => {
    const planned = plan("planned");
    expect(restructureOf(planned)).toEqual({ shape: "planned", digest: planned.digest });
  });

  it("lists as candidates for the plan the repositories a registration adds or already holds", () => {
    expect(candidatesOf(plan("consolidate")).map((row) => row.id)).toEqual(["ws", "api"]);
    const refused = { ...plan("single"), repositories: [{ path: "/x", outcome: "refused" as const, reason: "no" }] };
    expect(candidatesOf(refused)).toEqual([]);
  });

  it("finds the record a registration made at a path", () => {
    const report = { dry_run: false, repositories: [{ path: "/a", outcome: "registered" as const, repository: { id: "id-a", merge_method: "merge" as const, review_target: "branch" as const, start_from: "merged" as const } }, { path: "/b", outcome: "refused" as const }] };
    expect(registeredAt(report, "/a")).toBe("id-a");
    expect(registeredAt(report, "/b")).toBe("");
  });
});

describe("the repository that holds the plan", () => {
  it("is refused when its registration is, unless it is the clone adopt runs in", () => {
    const refused: AdoptionPlan = {
      ...plan("workspace-files"),
      planning: { name: "api", path: "/work/api", why: "named" },
      repositories: [{ path: "/work/api", outcome: "refused", reason: "a project holds it" }],
    };
    expect(planningRefusal(refused)).toBe("a project holds it");
    expect(planningRefusal({ ...refused, planning: { name: "design", path: "/work/design", why: "here" } })).toBe("");
    expect(planningRefusal(plan("undecided"))).toBe("");
  });
});

describe("the code trees left out", () => {
  it("add a tree checked, and drop one unchecked", () => {
    const left = plan("consolidate-left-out");
    expect(leftOutAfter(plan("consolidate"), "tools", true)).toEqual(["tools"]);
    expect(leftOutAfter(left, "tools", false)).toEqual([]);
    expect(leftOutAfter(left, "other", true)).toEqual(["tools", "other"]);
  });
});

describe("what changed between two plans", () => {
  it("names a new shape, a repository found or not found, and a list that differs", () => {
    const before = plan("planned");
    const now = plan("workspace-files");
    const changed = whatChanged(before, now);
    expect(changed).toContain("the shape is now workspace_files, not planned");
    expect(changed).toContain("/work/design was found");
    expect(changed).toContain("/work/ws was not found");
    expect(changed).toContain("the files it adds changed");
    expect(changed).toContain("the moves changed");
  });

  it("names nothing for a plan that is the same", () => {
    expect(whatChanged(plan("planned"), plan("planned"))).toEqual([]);
  });
});
