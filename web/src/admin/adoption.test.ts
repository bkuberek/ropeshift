import { describe, expect, it } from "vitest";

import type { AdoptionPlan } from "../api/admin";
import { golden } from "../testing";
import { candidatesOf, foldersOf, leftOutAfter, offersRestructure, planningRefusal, planRequest, registeredAt, registrablePaths, relativeFolder, restructureOf, whatChanged } from "./adoption";

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

describe("whether a plan offers a restructuring", () => {
  it("offers one in every shape but single, and in a single repository only for the files it lacks", () => {
    expect(offersRestructure(plan("planned"))).toBe(true);
    expect(offersRestructure(plan("consolidate"))).toBe(true);
    expect(offersRestructure(plan("single"))).toBe(false);
    expect(offersRestructure(plan("single-lacking"))).toBe(true);
  });
});

describe("what a plan registers and restructures by", () => {
  it("registers every repository the plan lists except the one it leaves out", () => {
    expect(registrablePaths(plan("consolidate"))).toEqual(["/work/ws", "/work/ws/repos/api"]);
    expect(registrablePaths(plan("workspace-files"))).toEqual(["/work/api", "/work/design", "/work/web"]);
  });

  it("carries the design repository and the trees left out of a consolidation, with the digest and the request", () => {
    const left = plan("consolidate-left-out");
    expect(restructureOf(left, planRequest(["/work/ws"], 1, ["tools"]))).toEqual({
      shape: "consolidate", digest: left.digest, design_path: "/work/ws/repos/design", design_root: "design", leave_out: ["tools"], root: "/work/ws", depth: 1,
    });
    const kept = plan("consolidate");
    expect(restructureOf(kept, planRequest(["/work/ws"], 1))).toEqual({
      shape: "consolidate", digest: kept.digest, design_path: "/work/ws/repos/design", design_root: "design", root: "/work/ws", depth: 1,
    });
  });

  it("carries the request the plan was read with, so a worker reads the layout again the way the plan did", () => {
    const planned = plan("workspace-files");
    const request = { ...planRequest(["/work/design", "/work/api", "/work/web"], 2), planning: "design", design: "docs" };
    expect(restructureOf(planned, request)).toEqual({
      shape: "workspace_files", digest: planned.digest, root: "/work/design", paths: ["/work/api", "/work/web"], depth: 2, planning: "design", design: "docs",
    });
  });

  it("carries the digest of each part of the plan the route answered, and none when the route answered none", () => {
    const planned = plan("planned");
    const parts = { layout: "sha256:" + "a".repeat(64), files: "sha256:" + "b".repeat(64) };
    expect(restructureOf({ ...planned, parts }, planRequest(["/work/ws"], 1))).toEqual({
      shape: "planned", digest: planned.digest, root: "/work/ws", depth: 1, parts,
    });
    expect(restructureOf(planned, planRequest(["/work/ws"], 1))).not.toHaveProperty("parts");
  });

  it("leaves out what the request does not name: no paths, no names", () => {
    const chosen = restructureOf(plan("planned"), planRequest(["/work/ws"], 1));
    for (const key of ["paths", "planning", "design"]) {
      expect(chosen).not.toHaveProperty(key);
    }
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

  it("names the steps for the code repositories when they differ", () => {
    expect(whatChanged(plan("planned"), { ...plan("planned"), steps: null })).toEqual(["the steps for the code repositories changed"]);
  });

  it("names nothing for a plan that is the same", () => {
    expect(whatChanged(plan("planned"), plan("planned"))).toEqual([]);
  });
});
