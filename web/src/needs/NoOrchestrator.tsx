import { useCallback, useEffect, useId, useState } from "react";

import { readView, startOrchestrator } from "../api/client";
import type { ShopFloor, WorkerLine } from "../api/types";
import { usePageActions } from "../app/pageActions";
import { running } from "../floor/Installation";
import { Button } from "../ui/controls";
import { Banner, Refusal } from "../ui/display";
import { useToast } from "../ui/overlays";
import { useAct } from "./useAct";

// judging are the states an orchestrator judges in: those of a run, less backoff, where it waits out a failure.
const judging = new Set([...running].filter((state) => state !== "backoff"));

// runsOrchestrator says a worker's orchestrator judges now: it is wanted, its worker was heard from within the freshness
// window, and it last said a state it judges in. A worker that went quiet, or a run in backoff, judges nothing.
export const runsOrchestrator = (line: WorkerLine) =>
  line.freshness === "observed" && line.orchestrator?.desired_state === "running" && judging.has(line.orchestrator.actual_state ?? "");

const nameOf = (line: WorkerLine) => line.worker.name || line.worker.id;

// StartOn is one worker's start, with its own busy state and refusal. Its palette entry takes the keyboard to the
// button and starts nothing, because a start runs a session a person pays for, so a person presses it.
function StartOn({ line, many, after }: { line: WorkerLine; many: boolean; after: () => Promise<void> }) {
  const { act, busy, failure } = useAct(after);
  const toast = useToast();
  const id = useId();
  const name = nameOf(line);
  usePageActions([
    {
      key: `action:start-orchestrator:${line.worker.id}`,
      group: "Actions",
      id: line.worker.id,
      title: `Start orchestrator: ${name}`,
      icon: "play",
      keywords: "orchestrator judge questions",
      run: () => document.getElementById(id)?.focus(),
    },
  ]);
  const start = () =>
    void act(() => startOrchestrator(line.worker.id)).then((done) => {
      if (done) {
        toast("success", `Asked ${name} to run its orchestrator. It takes only the questions still open.`);
      }
    });
  return (
    <>
      <Button id={id} small icon="play" loading={busy} disabled={busy} aria-label={`Start orchestrator on ${name}`} onClick={start}>
        Start orchestrator{many ? ` on ${name}` : ""}
      </Button>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </>
  );
}

// NoOrchestrator tells a member that no orchestrator is judging the questions that wait, and offers both ways on:
// answer them here as the orchestrator would, or start an orchestrator on a worker. It reads the floor, and says
// nothing while any worker's orchestrator judges, because that one takes what is still open. With no worker at all,
// it says there is none to start one on.
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
  const lines = floor?.workers ?? [];
  if (!floor || lines.some(runsOrchestrator)) {
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
      <div className="row-tight">
        {lines.map((line) => (
          <StartOn key={line.worker.id} line={line} many={lines.length > 1} after={read} />
        ))}
      </div>
    </Banner>
  );
}
