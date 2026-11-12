import type { Plan, PlanDocuments, RequirementTrace, TracedWork, VerdictKind } from "../api/types";
import { addressOf } from "../app/route";

// A requirement's status, in the words of the design's table, each with the badge `requirementStates` gives.
export type RequirementStatus = "met_by_existing" | "planned" | "in_progress" | "in_review" | "integrating" | "met";

export const statusFilters: RequirementStatus[] = ["met_by_existing", "planned", "in_progress", "in_review", "integrating", "met"];

// workOf lists the work orders that serve a requirement, each once however many of its criteria do.
export function workOf(trace: RequirementTrace): TracedWork[] {
  const seen = new Map<string, TracedWork>();
  for (const criterion of trace.criteria) {
    for (const order of criterion.work_orders) {
      if (!seen.has(order.id)) {
        seen.set(order.id, order);
      }
    }
  }
  return [...seen.values()];
}

// requirementStatus says where a requirement stands from the work that serves it, and for one the plan says
// existing code meets, that it is. Work that was canceled counts for nothing: it will not serve the
// requirement. A requirement is met when all the work that serves it is done, and otherwise it stands where
// its furthest work is, from merging, to waiting for acceptance, to being worked on. Work not yet claimed
// leaves it planned.
export function requirementStatus(trace: RequirementTrace): RequirementStatus {
  if (trace.status === "met") {
    return "met_by_existing";
  }
  const states = workOf(trace)
    .map((order) => order.state)
    .filter((state) => state !== "canceled");
  if (states.length === 0) {
    return "planned";
  }
  if (states.every((state) => state === "done")) {
    return "met";
  }
  if (states.includes("integrating")) {
    return "integrating";
  }
  if (states.includes("acceptance")) {
    return "in_review";
  }
  if (states.some((state) => ["claimed", "running", "parked", "lapsed", "blocked", "failed", "done"].includes(state))) {
    return "in_progress";
  }
  return "planned";
}

export type TraceVerdict = { criterion: string; work: string; verdict: VerdictKind; commit: string };

// Row is one requirement of the matrix: which plan holds it, where it stands, the criteria that serve it, the
// work that carries them, and the verdicts that speak for that work.
export type Row = {
  plan: Plan;
  trace: RequirementTrace;
  status: RequirementStatus;
  criteria: string[];
  work: TracedWork[];
  verdicts: TraceVerdict[];
};

export function rowsOf(plans: PlanDocuments[]): Row[] {
  return plans.flatMap((plan) =>
    plan.requirements.map((trace): Row => {
      const verdicts: TraceVerdict[] = [];
      for (const criterion of trace.criteria) {
        for (const order of criterion.work_orders) {
          if (order.verdict) {
            verdicts.push({ criterion: criterion.criterion, work: order.id, verdict: order.verdict.verdict, commit: order.verdict.commit });
          }
        }
      }
      return {
        plan: plan.plan,
        trace,
        status: requirementStatus(trace),
        criteria: [...new Set(trace.criteria.map((criterion) => criterion.criterion))],
        work: workOf(trace),
        verdicts,
      };
    }),
  );
}

// filterRows keeps the rows of one status and of one plan. A filter left empty leaves its part open.
export function filterRows(rows: Row[], filter: { status: string; plan: string }): Row[] {
  return rows.filter((row) => (!filter.status || row.status === filter.status) && (!filter.plan || row.plan.id === filter.plan));
}

export type DocumentsTab = "reader" | "traceability";

// documentsAddress is where a tab of the documents view lives. Each tab has a path of its own, so a person
// can send either, and a requirement a person selected goes with the tab. The reader names no document for
// a requirement, so it opens the requirements.
export function documentsAddress(tab: DocumentsTab, key: string): string {
  if (tab === "traceability") {
    return addressOf("/traceability", { key });
  }
  return key ? addressOf("/documents", { kind: "requirements", key }) : "/documents";
}
