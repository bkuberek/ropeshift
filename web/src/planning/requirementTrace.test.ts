import { describe, expect, it } from "vitest";

import type { Documents, PlanDocuments, RequirementTrace, TracedCriterion, TracedWork } from "../api/types";
import { golden } from "../testing";
import { documentsAddress, filterRows, requirementStatus, rowsOf, statusFilters } from "./requirementTrace";

const work = (id: string, state: string, verdict?: TracedWork["verdict"]): TracedWork => ({ id, title: id, state, verdict });

function trace(...criteria: { criterion: string; work: TracedWork[] }[]): RequirementTrace {
  return {
    key: "FR-1",
    text: "It retries.",
    document: "requirements:r",
    criteria: criteria.map(
      (row): TracedCriterion => ({ contract: "c", contract_title: "C", contract_version: 1, criterion: row.criterion, text: row.criterion, work_orders: row.work }),
    ),
  };
}

describe("a requirement's status", () => {
  it("is met by existing code when the plan says the code already meets it, whatever work it traces to", () => {
    expect(requirementStatus({ ...trace({ criterion: "c1", work: [work("a", "running")] }), status: "met", evidence: ["app:retry.go"] })).toBe("met_by_existing");
  });

  it("is planned until some work that serves it is under way", () => {
    expect(requirementStatus(trace())).toBe("planned");
    expect(requirementStatus(trace({ criterion: "c1", work: [work("a", "intake"), work("b", "contract_review"), work("c", "ready")] }))).toBe("planned");
  });

  it("is in progress while work that serves it is claimed, running, parked, or stuck", () => {
    for (const state of ["claimed", "running", "parked", "lapsed", "blocked", "failed"]) {
      expect(requirementStatus(trace({ criterion: "c1", work: [work("a", state)] })), state).toBe("in_progress");
    }
  });

  it("is in review when finished work waits for acceptance, and integrating when it is being merged", () => {
    expect(requirementStatus(trace({ criterion: "c1", work: [work("a", "acceptance"), work("b", "running")] }))).toBe("in_review");
    expect(requirementStatus(trace({ criterion: "c1", work: [work("a", "integrating"), work("b", "acceptance")] }))).toBe("integrating");
  });

  it("is met once every work order that serves it is done, and canceled work counts for nothing", () => {
    expect(requirementStatus(trace({ criterion: "c1", work: [work("a", "done"), work("b", "canceled")] }))).toBe("met");
    expect(requirementStatus(trace({ criterion: "c1", work: [work("a", "canceled")] }))).toBe("planned");
    expect(requirementStatus(trace({ criterion: "c1", work: [work("a", "done"), work("b", "running")] }))).toBe("in_progress");
  });

  it("counts a work order once, however many criteria it carries", () => {
    expect(requirementStatus(trace({ criterion: "c1", work: [work("a", "done")] }, { criterion: "c2", work: [work("a", "done")] }))).toBe("met");
  });
});

describe("the traceability matrix", () => {
  const view = golden<Documents>("documents");

  it("has a row for each requirement of each plan, with the plan it belongs to", () => {
    const rows = rowsOf(view.plans);
    expect(rows.map((row) => row.trace.key)).toEqual(view.plans.flatMap((plan) => plan.requirements.map((trace) => trace.key)));
    expect(rows.every((row) => row.plan.id === view.plans[0]?.plan.id)).toBe(true);
  });

  it("gives each row its criteria, its work orders once each, and the verdicts that speak for them", () => {
    const plan: PlanDocuments = {
      plan: { id: "p", title: "P", status: "active" },
      documents: [],
      requirements: [
        trace(
          { criterion: "c1", work: [work("a", "done", { verdict: "pass", commit: "abc1234" })] },
          { criterion: "c2", work: [work("a", "done", { verdict: "fail", commit: "abc1234" })] },
        ),
      ],
    };
    const [row] = rowsOf([plan]);
    expect(row?.criteria).toEqual(["c1", "c2"]);
    expect(row?.work.map((order) => order.id)).toEqual(["a"]);
    expect(row?.verdicts.map((verdict) => [verdict.criterion, verdict.verdict])).toEqual([
      ["c1", "pass"],
      ["c2", "fail"],
    ]);
  });

  it("filters by status and by plan, and either filter alone leaves the other open", () => {
    const base = { documents: [] };
    const plans: PlanDocuments[] = [
      { ...base, plan: { id: "p1", title: "One", status: "active" }, requirements: [trace({ criterion: "c1", work: [work("a", "done")] })] },
      { ...base, plan: { id: "p2", title: "Two", status: "active" }, requirements: [{ ...trace(), key: "FR-2" }, { ...trace({ criterion: "c1", work: [work("b", "running")] }), key: "FR-3" }] },
    ];
    const rows = rowsOf(plans);
    expect(filterRows(rows, { status: "", plan: "" })).toHaveLength(3);
    expect(filterRows(rows, { status: "met", plan: "" }).map((row) => row.trace.key)).toEqual(["FR-1"]);
    expect(filterRows(rows, { status: "", plan: "p2" }).map((row) => row.trace.key)).toEqual(["FR-2", "FR-3"]);
    expect(filterRows(rows, { status: "in_progress", plan: "p2" }).map((row) => row.trace.key)).toEqual(["FR-3"]);
    expect(filterRows(rows, { status: "met", plan: "p2" })).toEqual([]);
  });

  it("offers each status the table names, and no other", () => {
    expect(statusFilters).toEqual(["met_by_existing", "planned", "in_progress", "in_review", "integrating", "met"]);
  });
});

describe("the documents view's two tabs", () => {
  it("move between two paths, and a selected requirement goes with the tab", () => {
    expect(documentsAddress("reader", "")).toBe("/documents");
    expect(documentsAddress("traceability", "")).toBe("/traceability");
    expect(documentsAddress("traceability", "FR-SEARCH-2")).toBe("/traceability?key=FR-SEARCH-2");
    expect(documentsAddress("reader", "FR-SEARCH-2")).toBe("/documents?kind=requirements&key=FR-SEARCH-2");
  });
});
