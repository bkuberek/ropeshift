import { useContext, useId } from "react";

import { cancelScheduledStart, scheduleOrchestrator, startOrchestrator } from "../api/client";
import type { OrchestratorHold, ScheduledStart, WorkerLine } from "../api/types";
import { usePageActions } from "../app/pageActions";
import { useAct } from "../needs/useAct";
import { useClock, ViewTime } from "../parts/parts";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { useToast } from "../ui/overlays";
import { clockOf } from "../ui/resolution";

const minuteMs = 60_000;
const hourMs = 60 * minuteMs;
const dayMs = 24 * hourMs;

// countdownWords says how far off a reset is: in hours and minutes, or in days for a weekly window that is a day or
// more away.
export function countdownWords(ms: number, weekly: boolean): string {
  if (ms < minuteMs) {
    return "under a minute";
  }
  if (weekly && ms >= dayMs) {
    const days = Math.floor(ms / dayMs);
    return days === 1 ? "1 day" : `${days} days`;
  }
  const hours = Math.floor(ms / hourMs);
  const minutes = Math.floor((ms % hourMs) / minuteMs);
  return hours === 0 ? `${minutes} min` : `${hours} h ${minutes} min`;
}

const windowWords: Record<string, string> = { five_hour: "five-hour session window", seven_day: "weekly window" };

// when says a moment: its time of day when it falls on the day of now, and its day too when it does not.
export function when(at: number, now: number): string {
  if (new Date(at).toDateString() === new Date(now).toDateString()) {
    return clockOf(at);
  }
  return new Date(at).toLocaleString(undefined, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
}

// useServerNow is the server's time for what the view shows, which the view's stamp gives, run on by how long ago
// the browser read it, and the browser's own only where no view states one.
function useServerNow(): number {
  const { asOf, elapsed } = useContext(ViewTime);
  const clock = useClock();
  return asOf > 0 ? asOf + elapsed : clock;
}

// UsageLimit says which window of a sign-in reached its limit and when it resets, with a live countdown, and says
// when the reset is too old to trust, and when nobody reported one.
export function UsageLimit({ hold, now }: { hold: OrchestratorHold; now: number }) {
  const weekly = hold.window === "seven_day";
  const window = windowWords[hold.window ?? ""] ?? "sign-in";
  if (!hold.resets_at) {
    return <p>The usage limit of the {window} is reached, and no reset time was reported, so no countdown shows.</p>;
  }
  const left = hold.resets_at - now;
  return (
    <p>
      The usage limit of the {window} is reached. It resets at {when(hold.resets_at, now)}
      {left > 0 && !hold.stale ? ` (in ${countdownWords(left, weekly)})` : ""}.
      {hold.stale ? " This reset time may be out of date: it passed with the limit still holding, or was read longer ago than its window lasts." : ""}
    </p>
  );
}

// Scheduled says when a start scheduled for the reset starts the orchestrator.
function Scheduled({ start, now }: { start: ScheduledStart; now: number }) {
  if (!start.starts_at) {
    return (
      <p>
        {start.missed ? "The reset passed with the limit still holding, so the" : "The"} orchestrator starts at the next reset its worker reports.
      </p>
    );
  }
  const left = start.starts_at - now;
  return (
    <p>
      Starts at {when(start.starts_at, now)}
      {left > 0 ? ` (in ${countdownWords(left, left >= dayMs)})` : ""}.
    </p>
  );
}

const holdWords: Record<string, string> = {
  offline: "The worker is offline, so its orchestrator starts once it connects.",
  tripped: "Its breaker tripped, because it crashed too often. Reset it to run it again.",
};

const nameOf = (line: WorkerLine) => line.worker.name || line.worker.id;

// OrchestratorStart is the control that starts a worker's orchestrator, with what keeps it from starting beside it: an
// offline worker, a tripped breaker, which Reset clears, or a sign-in at its usage limit, where Start schedules the start
// for the reset. A start scheduled for the reset says when, and Cancel ends it.
export function OrchestratorStart({ line, after }: { line: WorkerLine; after: () => Promise<void> }) {
  const { act, busy, failure } = useAct(after);
  const toast = useToast();
  const now = useServerNow();
  const id = useId();
  const name = nameOf(line);
  const hold = line.orchestrator_hold;
  const start = line.scheduled_start;
  const limited = hold?.reason === "usage_limit";
  // An orchestrator already asked to run starts once the limit resets, so there is nothing to schedule.
  const asked = limited && !start && line.orchestrator?.desired_state === "running";
  const run = () =>
    void act(() => (limited ? scheduleOrchestrator(line.worker.id) : startOrchestrator(line.worker.id))).then((done) => {
      if (done) {
        toast("success", limited ? `${name} starts its orchestrator at the reset.` : `Asked ${name} to run its orchestrator.`);
      }
    });
  const cancel = () =>
    void act(() => cancelScheduledStart(line.worker.id)).then((done) => {
      if (done) {
        toast("success", `Canceled the start scheduled on ${name}.`);
      }
    });
  const label = start ? `Cancel the scheduled start on ${name}` : hold?.reason === "tripped" ? `Reset the orchestrator on ${name}` : limited ? `Start at reset on ${name}` : `Start orchestrator on ${name}`;
  // The palette takes the keyboard to the button and does nothing more: a start runs a session a person pays for, and
  // a cancel gives up a start a member asked for, so a person presses the button.
  usePageActions(asked ? [] : [{ key: `action:orchestrator-start:${line.worker.id}`, group: "Actions", id: line.worker.id, title: label, icon: start ? "x" : "play", keywords: "orchestrator start reset", run: () => document.getElementById(id)?.focus() }]);
  if (asked) {
    return (
      <div className="field-stack orchestrator-start">
        <UsageLimit hold={hold} now={now} />
        <p>Its orchestrator is asked to run, so it starts once the limit resets.</p>
      </div>
    );
  }
  return (
    <div className="field-stack orchestrator-start">
      {start ? <Scheduled start={start} now={now} /> : hold?.reason === "usage_limit" ? <UsageLimit hold={hold} now={now} /> : hold ? <p>{holdWords[hold.reason]}</p> : null}
      <div className="row-tight">
        {start ? (
          <Button id={id} small icon="x" loading={busy} disabled={busy} aria-label={label} onClick={cancel}>
            Cancel
          </Button>
        ) : (
          <Button id={id} small icon="play" loading={busy} disabled={busy} aria-label={label} onClick={run}>
            {hold?.reason === "tripped" ? "Reset" : limited ? "Start at reset" : "Start orchestrator"}
          </Button>
        )}
      </div>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </div>
  );
}
