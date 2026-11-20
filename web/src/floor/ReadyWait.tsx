import { newID } from "../admin/logic";
import { useChange } from "../admin/parts";
import { openWindow } from "../api/admin";
import type { ReadyWait } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { useToast } from "../ui/overlays";

const capWords: Record<NonNullable<ReadyWait["cap"]>, string> = {
  wip_limit: "The fleet's cap on work in progress is reached, so no worker claims more work until a lane ends or a member raises the cap.",
  lanes_per_repository: "The cap on lanes in this repository is reached, so no worker claims more work in it until a lane ends or a member raises the cap.",
  lanes_per_worker: "holds as many lanes as its cap allows, so it claims more work only when one of its lanes ends.",
};

// readyWaitWords says, in plain words, why no worker's orchestrator claims a ready work order, and what would let one.
export function readyWaitWords(wait: ReadyWait): string {
  const name = wait.worker_name || wait.worker || "The worker";
  switch (wait.reason) {
    case "run_window":
      return `${name} is attended and no run window is open, so its orchestrator claims no work. Open a run window, or let it run unattended.`;
    case "manual":
      return `${name} is manual, so its orchestrator claims no work: a member starts each lane.`;
    case "no_orchestrator":
      return `No orchestrator runs on ${name}, so nothing claims ready work. Start its orchestrator, or start the work yourself.`;
    case "cap":
      return wait.cap === "lanes_per_worker" ? `${name} ${capWords.lanes_per_worker}` : capWords[wait.cap ?? "wip_limit"];
    case "fleet":
      return wait.fleet === "drain" ? "The fleet is draining, so no worker claims new work." : "The fleet is paused, so no worker claims work.";
    case "spend_limit":
      return "The project reached its spend limit, so no worker claims its work until the limit resets or a member raises it.";
    case "no_worker":
      return "No worker can run it: none meets its needs.";
  }
  return "No worker claims it.";
}

// OpenWindowNow opens a run window on an attended worker for the length the server gives one nobody chose, so its
// orchestrator claims ready work. Only a member opens a window, and the server refuses anyone else.
function OpenWindowNow({ worker, name, after }: { worker: string; name: string; after?: () => Promise<void> | void }) {
  const change = useChange();
  const toast = useToast();
  const open = async () => {
    const opened = await change.run(() => openWindow(worker, newID(), 0));
    if (opened) {
      const hours = Math.max(1, Math.round((opened.closes_at - opened.opened_at) / 3_600_000));
      toast("success", `Opened a run window on ${name} for ${hours} hour${hours === 1 ? "" : "s"}. Its orchestrator claims ready work until it closes.`);
      await after?.();
    }
  };
  return (
    <>
      <Button small variant="primary" icon="clock" loading={change.busy} onClick={() => void open()}>
        Open a run window
      </Button>
      {change.failure ? <Refusal>{change.failure}</Refusal> : null}
    </>
  );
}

// ReadyWaitActions are what a member can do about a wait: for an attended worker with no window, open one, or go to
// its level, where running unattended takes the stated risk a member reads and acknowledges first; for a manual
// worker, its level; and for a worker with no orchestrator, the machine where one starts.
export function ReadyWaitActions({ wait, after }: { wait: ReadyWait; after?: () => Promise<void> | void }) {
  const worker = wait.worker ?? "";
  const name = wait.worker_name || worker;
  const level = addressOf("/settings", { section: "autonomy", worker });
  switch (wait.reason) {
    case "run_window":
      return (
        <span className="row-tight">
          <OpenWindowNow worker={worker} name={name} after={after} />
          <Link className="btn btn-sm" to={level} installation>
            Run unattended
          </Link>
        </span>
      );
    case "manual":
      return (
        <Link className="btn btn-sm" to={level} installation>
          Choose a level
        </Link>
      );
    case "no_orchestrator":
      return (
        <Link className="btn btn-sm" to="/workers" installation>
          Start its orchestrator
        </Link>
      );
  }
  return null;
}

// ReadyWaitNote is a ready work order's wait, in words, with what a member can do about it.
export function ReadyWaitNote({ wait, after }: { wait: ReadyWait; after?: () => Promise<void> | void }) {
  return (
    <span className="stack-8">
      <span className="cell-note">{readyWaitWords(wait)}</span>
      <ReadyWaitActions wait={wait} after={after} />
    </span>
  );
}
