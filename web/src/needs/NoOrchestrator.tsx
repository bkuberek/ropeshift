import { useCallback, useEffect, useState } from "react";

import { readView, startOrchestrator } from "../api/client";
import type { ShopFloor, WorkerLine } from "../api/types";
import { usePageActions } from "../app/pageActions";
import { running } from "../floor/Installation";
import { Button } from "../ui/controls";
import { Banner, Refusal } from "../ui/display";
import { useToast } from "../ui/overlays";
import { useAct } from "./useAct";

// runsOrchestrator says a worker's orchestrator is wanted and its worker last said it runs.
export const runsOrchestrator = (line: WorkerLine) => line.orchestrator?.desired_state === "running" && running.has(line.orchestrator.actual_state ?? "");

const nameOf = (line: WorkerLine) => line.worker.name || line.worker.id;

// NoOrchestrator tells a member that no orchestrator is judging the questions that wait, and offers both ways on:
// answer them here as the orchestrator would, or start an orchestrator on a worker. It reads the floor, and says
// nothing while any worker's orchestrator runs, because that one takes what is still open.
export function NoOrchestrator({ what }: { what: string }) {
  const [floor, setFloor] = useState<ShopFloor>();
  const read = useCallback(async () => {
    try {
      setFloor(await readView<ShopFloor>("shop-floor"));
    } catch {
      setFloor(undefined);
    }
  }, []);
  useEffect(() => {
    void read();
  }, [read]);
  const { act, busy, failure } = useAct(read);
  const toast = useToast();
  const lines = floor?.workers ?? [];
  const stopped = lines.some(runsOrchestrator) ? [] : lines;
  const start = (line: WorkerLine) =>
    void act(() => startOrchestrator(line.worker.id)).then((done) => {
      if (done) {
        toast("success", `Asked ${nameOf(line)} to run its orchestrator. It takes only the questions still open.`);
      }
    });
  // The command palette offers each start the notice offers.
  usePageActions(
    stopped.map((line) => ({
      key: `action:start-orchestrator:${line.worker.id}`,
      group: "Actions",
      id: line.worker.id,
      title: `Start orchestrator: ${nameOf(line)}`,
      icon: "play",
      keywords: "orchestrator judge questions",
      run: () => start(line),
    })),
  );
  if (stopped.length === 0) {
    return null;
  }
  return (
    <Banner tone="warning">
      <p>No orchestrator is judging {what}. Answer, assume, or merge each one here as the orchestrator would, or start an orchestrator, which takes only the questions still open.</p>
      <div className="row-tight">
        {stopped.map((line) => (
          <Button key={line.worker.id} small icon="play" loading={busy} disabled={busy} aria-label={`Start orchestrator on ${nameOf(line)}`} onClick={() => start(line)}>
            Start orchestrator{stopped.length > 1 ? ` on ${nameOf(line)}` : ""}
          </Button>
        ))}
      </div>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </Banner>
  );
}
