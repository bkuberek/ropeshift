import { describe, expect, it } from "vitest";

import type { Documents } from "../api/types";
import { golden } from "../testing";
import { homeOfKind, openedDocument, reviewWords, treeOf, versionLabel } from "./documents";

const view = golden<Documents>("documents");

describe("the document tree", () => {
  const tree = treeOf(view);

  it("groups documents by the kind a person looks for them under, and none falls out", () => {
    expect(Object.keys(tree)).toEqual(["requirements", "spec", "design", "other"]);
    const everything = Object.values(tree).flat();
    const recorded = view.plans.flatMap((plan) => plan.documents);
    const live = view.documents.filter((document) => !document.record);
    expect(everything).toHaveLength(recorded.length + live.length);
  });

  it("puts architecture documents and experience designs with the designs, and milestones with the others", () => {
    expect(homeOfKind("architecture")).toBe("design");
    expect(homeOfKind("experience-design")).toBe("design");
    expect(homeOfKind("milestone")).toBe("other");
    expect(homeOfKind("something-new")).toBe("other");
    expect(tree.design.map((item) => item.title)).toContain("How the factory is built");
    expect(tree.other.map((item) => item.title)).toContain("Build the core");
  });

  it("lists a file a plan recorded once, from the ledger, and every other file of the repository as it stands", () => {
    const recordedPaths = new Set(view.documents.filter((document) => document.record).map((document) => document.path));
    expect(recordedPaths.size).toBeGreaterThan(0);
    const paths = Object.values(tree)
      .flat()
      .filter((item) => !item.recorded)
      .map((item) => item.path);
    expect(paths.every((path) => !recordedPaths.has(path))).toBe(true);
    expect(tree.design.some((item) => !item.recorded && item.path === "design.md")).toBe(true);
  });

  it("addresses a recorded document by its ID and a file by its path and instance", () => {
    const recorded = tree.spec.find((item) => item.recorded);
    expect(recorded?.params).toEqual({ id: "spec:factory-api" });
    const file = tree.design.find((item) => !item.recorded);
    expect(file?.params).toEqual({ path: "design.md", instance: "memory" });
  });
});

describe("the document the address opens", () => {
  it("is the one the address names", () => {
    expect(openedDocument(view, new URLSearchParams("id=spec:factory-api"))?.params).toEqual({ id: "spec:factory-api" });
    expect(openedDocument(view, new URLSearchParams("path=design.md&instance=memory"))?.params).toEqual({ path: "design.md", instance: "memory" });
  });

  it("is the requirements document that holds a requirement the address selects", () => {
    expect(openedDocument(view, new URLSearchParams("key=FAC-1"))?.params).toEqual({ id: "requirements:factory" });
  });

  it("is the first document of the kind the address names, and else the first requirements document", () => {
    expect(openedDocument(view, new URLSearchParams("kind=design"))?.kind).toBe("design");
    expect(openedDocument(view, new URLSearchParams(""))?.kind).toBe("requirements");
  });

  it("is asked for all the same when the address names a document the lists do not hold, so the server says what it says", () => {
    expect(openedDocument(view, new URLSearchParams("id=spec:absent"))).toMatchObject({ key: "spec:absent", recorded: true, params: { id: "spec:absent" } });
    expect(openedDocument(view, new URLSearchParams("path=absent.md"))).toMatchObject({ path: "absent.md", recorded: false, params: { path: "absent.md" } });
  });

  it("is nothing when no document is held", () => {
    expect(openedDocument({ ...view, documents: [], plans: [] }, new URLSearchParams(""))).toBeUndefined();
  });
});

describe("where a version stands", () => {
  it("says approved with its plan version, waiting in one, or in none", () => {
    expect(reviewWords({ version: 1, digest: "d", recorded_at: 1, review: "approved", plan_version: 3 })).toBe("Approved with plan version 3");
    expect(reviewWords({ version: 1, digest: "d", recorded_at: 1, review: "pending", plan_version: 4 })).toBe("Waits for approval in plan version 4");
    expect(reviewWords({ version: 1, digest: "d", recorded_at: 1, review: "none" })).toBe("In no plan approval");
  });

  it("labels a version in the selector with its number and where it stands", () => {
    expect(versionLabel({ version: 7, digest: "d", recorded_at: 1, review: "approved", plan_version: 3 })).toBe("v7 · approved in plan v3");
    expect(versionLabel({ version: 8, digest: "d", recorded_at: 1, review: "pending", plan_version: 4 })).toBe("v8 · in pending plan v4");
    expect(versionLabel({ version: 6, digest: "d", recorded_at: 1, review: "none" })).toBe("v6");
  });
});
