import { describe, expect, it } from "vitest";

import { goKeys, resolve, titleOf, views } from "./routes";

describe("an address", () => {
  it.each([
    ["/", "", "/needs-you"],
    ["/projects/harbor", "", "/needs-you?project=harbor"],
    ["/projects/harbor/", "", "/needs-you?project=harbor"],
    ["/projects/harbor/in-progress", "", "/projects/harbor/work?group=in-progress"],
    ["/projects/harbor/on-hold", "", "/projects/harbor/work?group=on-hold"],
    ["/projects/harbor/stuck", "", "/projects/harbor/work?group=stuck"],
    ["/projects/harbor/completed", "", "/projects/harbor/work?group=completed"],
    ["/projects/harbor/shop-floor", "", "/projects/harbor/floor"],
    ["/projects/harbor/operations", "", "/projects/harbor/measures"],
    ["/projects/harbor/requirements", "", "/projects/harbor/documents?kind=requirements"],
    ["/projects/harbor/requirements", "?key=FR-1", "/projects/harbor/documents?kind=requirements&key=FR-1"],
    ["/projects/harbor/specs", "", "/projects/harbor/documents?kind=spec"],
    ["/projects/harbor/designs", "", "/projects/harbor/documents?kind=design"],
    ["/projects/harbor/document", "?path=docs%2Fa.md&version=abc", "/projects/harbor/documents?path=docs%2Fa.md&version=abc"],
    ["/projects/harbor/document", "?id=req-1&version=2", "/projects/harbor/documents?id=req-1&version=2"],
  ])("%s%s moves to %s, the view that took its place", (path, search, to) => {
    expect(resolve(path, search)).toEqual({ kind: "redirect", to });
  });

  it.each([
    ["/needs-you", "needs-you"],
    ["/workers", "workers"],
    ["/usage", "usage"],
  ])("%s is an installation view", (path, view) => {
    expect(resolve(path, "")).toEqual({ kind: "view", view, project: "" });
  });

  it.each([
    ["/projects/harbor/roadmap", "roadmap"],
    ["/projects/harbor/plans", "plans"],
    ["/projects/harbor/documents", "documents"],
    ["/projects/harbor/decisions", "decisions"],
    ["/projects/harbor/questions", "questions"],
    ["/projects/harbor/work", "work"],
    ["/projects/harbor/work-order", "work-order"],
    ["/projects/harbor/floor", "floor"],
    ["/projects/harbor/lane", "lane"],
    ["/projects/harbor/measures", "measures"],
  ])("%s is a view of that project", (path, view) => {
    expect(resolve(path, "")).toEqual({ kind: "view", view, project: "harbor" });
  });

  it("reads a project's name that the address encodes", () => {
    expect(resolve("/projects/two%20words/floor", "")).toEqual({ kind: "view", view: "floor", project: "two words" });
  });

  it.each(["/roadmap", "/floor", "/in-progress", "/shop-floor", "/requirements"])("%s names a project view and no project, so it waits for one", (path) => {
    expect(resolve(path, "")).toEqual({ kind: "pick-project", path });
  });

  it.each(["/projects/harbor/nowhere", "/nowhere", "/projects"])("%s holds no view", (path) => {
    expect(resolve(path, "")).toEqual({ kind: "missing" });
  });
});

describe("the views", () => {
  it("each have a title that names the view and no record", () => {
    for (const view of views) {
      expect(titleOf(view).length).toBeGreaterThan(0);
    }
    expect(titleOf("work-order")).toBe("Work order");
    expect(titleOf("needs-you")).toBe("Needs you");
  });

  it("are reached with G and a key, each key once", () => {
    const keys = Object.keys(goKeys);
    expect(new Set(keys).size).toBe(keys.length);
    expect(goKeys.n).toBe("needs-you");
    expect(goKeys.r).toBe("roadmap");
    expect(goKeys.d).toBe("documents");
    expect(goKeys.w).toBe("work");
    expect(goKeys.f).toBe("floor");
    expect(goKeys.u).toBe("usage");
  });
});
