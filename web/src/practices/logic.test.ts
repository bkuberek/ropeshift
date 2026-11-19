import { describe, expect, it } from "vitest";

import type { PracticeFinding, PracticeRow, PracticesView } from "../api/practices";
import { checking, countsOf, reasonProblem, standingOf } from "./logic";

const finding = (over: Partial<PracticeFinding>): PracticeFinding => ({ id: "ci", practice: "ci", severity: "medium", detail: "", why: "", fix: "", ...over });
const row = (over: Partial<PracticeRow>): PracticeRow => ({ id: "ci", severity: "medium", status: "finding", why: "", fix: "", ...over });

const viewOf = (findings: PracticeFinding[], practices: PracticeRow[] = []): PracticesView => ({
  repository: "repo-1",
  name: "api",
  result: { request: "r", commit: "abc", base_branch: "main", worker: "w", checked_at: "2026-11-19T00:00:00Z", table_version: 1, rules_version: 1, practices, findings },
});

describe("countsOf", () => {
  it("counts open findings by severity, apart from the acknowledged, and the practices the check could not read", () => {
    const counts = countsOf(
      viewOf(
        [finding({ id: "a", severity: "high" }), finding({ id: "b", severity: "high" }), finding({ id: "c", severity: "low" }), finding({ id: "d", severity: "medium", acknowledged: true })],
        [row({ id: "license", status: "unknown" }), row({ id: "ci", status: "met" })],
      ),
    );
    expect(counts).toEqual({ high: 2, medium: 0, low: 1, open: 3, acknowledged: 1, unknown: 1 });
  });

  it("counts nothing for a repository with no result", () => {
    expect(countsOf({ repository: "r", name: "n" })).toEqual({ high: 0, medium: 0, low: 0, open: 0, acknowledged: 0, unknown: 0 });
  });
});

describe("standingOf", () => {
  it("reads a practice whose findings are all acknowledged as Acknowledged, with its severity muted", () => {
    const standing = standingOf(row({ id: "secrets", severity: "high" }), [finding({ practice: "secrets", severity: "high", acknowledged: true })]);
    expect(standing.look.label).toBe("Acknowledged");
    expect(standing.muted).toBe("high");
  });

  it("reads a practice with an open finding by the most serious open one", () => {
    const standing = standingOf(row({ id: "lockfile", severity: "low" }), [finding({ practice: "lockfile", severity: "low" }), finding({ id: "lockfile:npm", practice: "lockfile", severity: "medium" })]);
    expect(standing.look.label).toBe("medium");
    expect(standing.muted).toBeUndefined();
  });

  it("names the other three statuses", () => {
    expect(standingOf(row({ status: "met" }), []).look.label).toBe("Met");
    expect(standingOf(row({ status: "unknown" }), []).look.label).toBe("Unknown");
    expect(standingOf(row({ status: "not_applicable" }), []).look.label).toBe("Not applicable");
  });
});

describe("reasonProblem", () => {
  it("refuses an empty reason and one over 2 KiB, counting bytes", () => {
    expect(reasonProblem("  ")).not.toBe("");
    expect(reasonProblem("x".repeat(2048))).toBe("");
    expect(reasonProblem("x".repeat(2049))).not.toBe("");
    expect(reasonProblem("é".repeat(1025))).not.toBe("");
  });
});

describe("checking", () => {
  it("is true for an offered request no read has ended", () => {
    const request = { id: "r", repository: "x", worker: "w", cause: "member", requested_by: "m", requested_at: 1, expires_at: 2, state: "offered" as const };
    expect(checking(request, {})).toBe(true);
    expect(checking(request, { r: "reported" })).toBe(false);
    expect(checking(undefined, {})).toBe(false);
  });
});
