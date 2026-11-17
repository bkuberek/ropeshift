import { createPlanWork, reviewWork } from "../api/admin";
import { inProjectOf, reviseSession } from "../api/client";
import { claimAndStart, newRunIDs } from "../api/runWork";
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
  const lane = await claimAndStart(id, draft.worker, newRunIDs(), { project });
  return { workOrder: id, lane };
}

// startRevisionSession opens a planning session that revises a plan with an open revision, as `ropeshift plan
// revise` does: the control plane records the planning work order with a brief that lists the decisions since the
// approved version, and the calls that follow are the ones a planning session makes.
export async function startRevisionSession(project: string, plan: string, draft: { repository: string; worker: string }): Promise<StartedSession> {
  const here = <T,>(run: () => Promise<T>) => inProjectOf(project, run);
  const id = newID();
  await here(() => reviseSession(plan, { id, repository: draft.repository }));
  const review = await here(() => reviewWork(id));
  if (review.failures && review.failures.length > 0) {
    throw new Error(`Work order ${id} was recorded and is not ready: ${review.failures.map((failure) => `${failure.check}: ${failure.detail}`).join("; ")}`);
  }
  const lane = await claimAndStart(id, draft.worker, newRunIDs(), { project });
  return { workOrder: id, lane };
}
