import { claimWork, createPlanWork, reviewWork, startLane } from "../api/admin";
import { inProjectOf } from "../api/client";
import { newID } from "../admin/logic";

export type SessionDraft = { goal: string; repository: string; worker: string };
export type StartedSession = { workOrder: string; lane: string };

// startPlanningSession opens a planning session as `ropeshift plan start` does: it chains the calls a member
// makes by hand, each one an audited member action, so the state machine has no shortcut for it. It records
// the `plan` work order with the goal, passes its contract review, claims it on the worker, and starts the
// lane. A step that fails says so, and a review that names what holds the work back says which work order it
// left behind.
export async function startPlanningSession(project: string, draft: SessionDraft): Promise<StartedSession> {
  const here = <T,>(run: () => Promise<T>) => inProjectOf(project, run);
  const id = newID();
  await here(() => createPlanWork({ id, goal: draft.goal.trim(), repository: draft.repository }));
  const review = await here(() => reviewWork(id));
  if (review.failures && review.failures.length > 0) {
    throw new Error(`Work order ${id} was recorded and is not ready: ${review.failures.map((failure) => `${failure.check}: ${failure.detail}`).join("; ")}`);
  }
  await here(() => claimWork(id, newID(), draft.worker));
  const lane = newID();
  await here(() => startLane(id, lane));
  return { workOrder: id, lane };
}
