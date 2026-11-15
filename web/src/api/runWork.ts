import { newID } from "../admin/logic";
import { claimWork, startLane } from "./admin";
import { inProjectOf } from "./client";

// RunIDs are the two IDs one attempt to run work sends. The ledger finds the claim and the lane a first
// try made when a retry sends the same IDs, so an attempt that failed between its two calls is pressed
// again with the same ones.
export type RunIDs = { claim: string; lane: string };

// newRunIDs makes the IDs of one attempt, as the CLI does for `work claim` and `lane start`. A lane a
// member named keeps its name.
export const newRunIDs = (lane = ""): RunIDs => ({ claim: newID(), lane: lane.trim() || newID() });

// Options say which project a change is for when it is not the page's, and tell the caller its claim was
// granted before the lane start that follows it.
type Options = { project?: string; onClaimed?: () => void };

const within = <T>(project: string | undefined, run: () => Promise<T>): Promise<T> => (project ? inProjectOf(project, run) : run());

// claimAndStart claims a work order for a worker and then asks the worker to start its lane, as `work claim`
// and `lane start` do. Each call is an audited member action, so the state machine has no shortcut for it.
// A claim that is refused starts no lane. A lane start that is refused leaves the work claimed, which
// onClaimed has already said. It answers with the lane's ID.
export async function claimAndStart(workOrder: string, worker: string, ids: RunIDs = newRunIDs(), { project, onClaimed }: Options = {}): Promise<string> {
  await within(project, () => claimWork(workOrder, ids.claim, worker));
  onClaimed?.();
  await within(project, () => startLane(workOrder, ids.lane));
  return ids.lane;
}

// startClaimed asks the worker that holds a work order's claim to start its lane, as `lane start` does. It
// answers with the lane's ID, the one named or a new one.
export async function startClaimed(workOrder: string, lane = "", { project }: Options = {}): Promise<string> {
  const id = lane.trim() || newID();
  await within(project, () => startLane(workOrder, id));
  return id;
}
