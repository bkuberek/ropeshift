import type { PlannedWork, PlanProgress } from "../api/types";

// noMilestone is the milestone of work no milestone holds.
export const noMilestone = "";

export type LayerWork = PlannedWork & { critical: boolean };
export type Layer = { layer: number; work: LayerWork[] };

// layersByMilestone lays out one milestone's work by dependency layer: a column for each layer of the plan
// that holds some of the milestone's work, numbered as the plan numbers its layers, with the work on the
// plan's critical path marked. A work order the layers do not hold has a layer after the last, so none goes
// missing.
export function layersByMilestone(plan: PlanProgress, milestone: string): Layer[] {
  const critical = new Set(plan.critical_path.work_orders);
  const mine = new Map(plan.work.filter((work) => (work.milestone ?? noMilestone) === milestone).map((work) => [work.id, work]));
  const layered = new Set(plan.layers.flat());
  const columns = plan.layers.map((ids) => ids);
  const stray = [...mine.keys()].filter((id) => !layered.has(id));
  if (stray.length > 0) {
    columns.push(stray);
  }
  const out: Layer[] = [];
  columns.forEach((ids, at) => {
    const work = ids.flatMap((id): LayerWork[] => {
      const found = mine.get(id);
      return found ? [{ ...found, critical: critical.has(id) }] : [];
    });
    if (work.length > 0) {
      out.push({ layer: at + 1, work });
    }
  });
  return out;
}

export type Waiting = { id: string; title: string; version: number; approved: number };

// waitingVersions names each plan whose newest version waits for a member's approval, with that version and
// the one in force.
export function waitingVersions(plans: { plan: { id: string; title: string }; pending_version?: number; approved_version?: number }[]): Waiting[] {
  return plans.flatMap((row) => (row.pending_version ? [{ id: row.plan.id, title: row.plan.title, version: row.pending_version, approved: row.approved_version ?? 0 }] : []));
}
