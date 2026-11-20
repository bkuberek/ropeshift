import { useCallback, useSyncExternalStore } from "react";

import type { WorkerLine } from "../api/types";
import { running } from "../floor/Installation";
import { OrchestratorStart } from "../floor/OrchestratorStart";
import { useClock, ViewTime } from "../parts/parts";
import { Banner } from "../ui/display";
import { floorSnapshot, refreshFloor, subscribeFloor } from "./floorStore";

// judging are the states an orchestrator judges in: those of a run, less backoff, where it waits out a failure.
const judging = new Set([...running].filter((state) => state !== "backoff"));

// runsOrchestrator says a worker's orchestrator judges now: it is wanted, its worker was heard from within the freshness
// window, and it last said a state it judges in. A worker that went quiet, or a run in backoff, judges nothing.
export const runsOrchestrator = (line: WorkerLine) =>
  line.freshness === "observed" && line.orchestrator?.desired_state === "running" && judging.has(line.orchestrator.actual_state ?? "");

// NoOrchestrator tells a member that no orchestrator is judging the questions that wait, and offers both ways on:
// answer them here as the orchestrator would, or start an orchestrator on a worker, with what keeps each from starting
// beside its control. It reads the floor, and says nothing while any worker's orchestrator judges, because that one
// takes what is still open. With no worker at all, it says there is none to start one on. Every copy of the notice
// follows one shared read of the floor, which goes on while a copy shows, so the notice goes away once an orchestrator
// starts, and a read that fails leaves what it last read standing.
export function NoOrchestrator({ what }: { what: string }) {
  const { floor, readAt } = useSyncExternalStore(subscribeFloor, floorSnapshot);
  const now = useClock();
  const read = useCallback(() => refreshFloor(true), []);
  const lines = floor?.workers ?? [];
  const standing = floor !== undefined && !lines.some(runsOrchestrator);
  if (!floor || !standing) {
    return null;
  }
  if (lines.length === 0) {
    return (
      <Banner tone="warning">
        <p>No orchestrator is judging {what}, and no worker is registered to start one on. Answer, assume, or merge each one here as the orchestrator would.</p>
      </Banner>
    );
  }
  return (
    <Banner tone="warning">
      <p>No orchestrator is judging {what}. Answer, assume, or merge each one here as the orchestrator would, or start an orchestrator, which takes only the questions still open.</p>
      <ViewTime.Provider value={{ asOf: floor.as_of, elapsed: Math.max(0, now - readAt) }}>
        {lines.map((line) => (
          <div key={line.worker.id}>
            <p className="label">{line.worker.name || line.worker.id}</p>
            <OrchestratorStart line={line} after={read} />
          </div>
        ))}
      </ViewTime.Provider>
    </Banner>
  );
}
