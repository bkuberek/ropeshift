import { createContext, useContext, useMemo, useState } from "react";
import type { ReactNode } from "react";

import { newID } from "../admin/logic";
import { useChange } from "../admin/parts";
import { openWindow } from "../api/admin";
import type { ReadyWait } from "../api/types";
import { addressOf, Link } from "../app/route";
import { useClock } from "../parts/parts";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { useToast } from "../ui/overlays";

const capWords: Record<NonNullable<ReadyWait["cap"]>, (name: string) => string> = {
  wip_limit: () => "The fleet's cap on work in progress is reached, so no worker claims more work until a lane ends or a member raises the cap.",
  lanes_per_repository: () => "The cap on lanes in this repository is reached, so no worker claims more work in it until a lane ends or a member raises the cap.",
  lanes_per_worker: (name) => `${name} holds as many lanes as its cap allows, so it claims more work only when one of its lanes ends.`,
  attended_max_respawns_per_day: (name) =>
    `The lane this work resumes has run as many generations today as ${name} may at its level, so it waits for the day's count to fall, or for you to start it.`,
};

// readyWaitWords says, in plain words, why no worker's orchestrator claims a ready work order, and what would let one.
export function readyWaitWords(wait: ReadyWait): string {
  const name = wait.worker_name || wait.worker || "The worker";
  switch (wait.reason) {
    case "run_window":
      return `${name} is attended and no run window is open, so its orchestrator claims no work. Open a run window, which lets ${name} claim in every project until it closes, or let it run unattended.`;
    case "manual":
      return `${name} is manual, so its orchestrator claims no work: a member starts each lane.`;
    case "no_orchestrator":
      return `No orchestrator runs on ${name}, so nothing claims ready work. Start its orchestrator, or start the work yourself.`;
    case "cap":
      return capWords[wait.cap ?? "wip_limit"](name);
    case "fleet":
      return wait.fleet === "drain" ? "The fleet is draining, so no worker claims new work." : "The fleet is paused, so no worker claims work.";
    case "spend_limit":
      return "The project reached its spend limit, so no worker claims its work until the limit resets or a member raises it.";
    case "no_worker":
      return "No worker can run it: none meets its needs or holds the lane it resumes.";
  }
  return "No worker claims it.";
}

// A place a member fixes a wait in: a Settings section or Workers, with the name its link reads.
type Place = { to: string; installation: boolean; label: string };

const workers: Place = { to: "/workers", installation: true, label: "Workers" };
const fleetSettings: Place = { to: addressOf("/settings", { section: "fleet" }), installation: false, label: "Fleet settings" };

// placeOf names where a member fixes a wait that no button here fixes, or nothing for a wait that has its own buttons.
function placeOf(wait: ReadyWait): Place | null {
  const worker = wait.worker ?? "";
  switch (wait.reason) {
    case "manual":
      return { to: addressOf("/settings", { section: "autonomy", worker }), installation: true, label: "Choose a level" };
    case "no_orchestrator":
      return { ...workers, label: "Start its orchestrator" };
    case "fleet":
      return fleetSettings;
    case "spend_limit":
      return { to: addressOf("/settings", { section: "spend" }), installation: false, label: "Spend settings" };
    case "no_worker":
      return workers;
    case "cap":
      if (wait.cap === "lanes_per_worker") {
        return workers;
      }
      if (wait.cap === "attended_max_respawns_per_day") {
        return { to: addressOf("/settings", { section: "autonomy-policy" }), installation: true, label: "Autonomy defaults" };
      }
      return fleetSettings;
  }
  return null;
}

// The run windows a member opened from the web UI in this browser, by worker, each with when it closes.
type Windows = { closes: (worker: string) => number | undefined; opened: (worker: string, closesAt: number) => void };

const OpenedWindows = createContext<Windows | null>(null);

// RunWindows holds the run windows a member opens from any page inside it, which the application wraps around every page,
// so every wait on that worker shows its window open before a read says so, and no second press meets the refusal of a
// window that is already open.
export function RunWindows({ children }: { children: ReactNode }) {
  const [windows, setWindows] = useState<Record<string, number>>({});
  const value = useMemo<Windows>(() => ({ closes: (worker) => windows[worker], opened: (worker, closesAt) => setWindows((was) => ({ ...was, [worker]: closesAt })) }), [windows]);
  return <OpenedWindows.Provider value={value}>{children}</OpenedWindows.Provider>;
}

// useRunWindow says whether a window a member opened here stands on a worker now, and keeps one a member opens. Outside
// RunWindows, each caller keeps its own.
function useRunWindow(worker: string): { open: boolean; opened: (closesAt: number) => void } {
  const shared = useContext(OpenedWindows);
  const [own, setOwn] = useState<number | undefined>(undefined);
  const now = useClock();
  const closes = shared ? shared.closes(worker) : own;
  return { open: closes !== undefined && closes > now, opened: (closesAt) => (shared ? shared.opened(worker, closesAt) : setOwn(closesAt)) };
}

// ReadyWaitWords says a wait in words, or that a run window a member opened here now lets the worker claim the work.
export function ReadyWaitWords({ wait }: { wait: ReadyWait }) {
  const standing = useRunWindow(wait.worker ?? "");
  if (wait.reason === "run_window" && standing.open) {
    const name = wait.worker_name || wait.worker || "The worker";
    return <>{`A run window is open on ${name} in every project, so its orchestrator claims this work at its next wake.`}</>;
  }
  return <>{readyWaitWords(wait)}</>;
}

// OpenWindowNow opens a run window on an attended worker for the length the server gives one nobody chose, so its
// orchestrator claims ready work in every project. Only a member opens a window, and the server refuses anyone else.
// subject names the work the button stands beside, which tells one button from another of the same worker.
function OpenWindowNow({ worker, name, subject, after }: { worker: string; name: string; subject?: string; after?: () => Promise<void> | void }) {
  const change = useChange();
  const toast = useToast();
  const standing = useRunWindow(worker);
  const open = async () => {
    const opened = await change.run(() => openWindow(worker, newID(), 0));
    if (opened) {
      standing.opened(opened.closes_at);
      const hours = Math.max(1, Math.round((opened.closes_at - opened.opened_at) / 3_600_000));
      toast("success", `Opened a run window on ${name} for ${hours} hour${hours === 1 ? "" : "s"}, in every project. Its orchestrator claims ready work until it closes.`);
      await after?.();
    }
  };
  return (
    <>
      <Button small variant="primary" icon="clock" loading={change.busy} aria-label={`Open a run window on ${name}${subject ? ` for ${subject}` : ""}`} onClick={() => void open()}>
        Open a run window
      </Button>
      {change.failure ? <Refusal>{change.failure}</Refusal> : null}
    </>
  );
}

// ReadyWaitActions are what a member can do about a wait. For an attended worker with no window: open one, or go to its
// level, where running unattended takes the stated risk a member reads and acknowledges first. Once a window a member
// opened here stands, nothing is left to press. Any other wait links the place a member fixes it in.
export function ReadyWaitActions({ wait, subject, after }: { wait: ReadyWait; subject?: string; after?: () => Promise<void> | void }) {
  const worker = wait.worker ?? "";
  const name = wait.worker_name || worker;
  const standing = useRunWindow(worker);
  if (wait.reason === "run_window") {
    if (standing.open) {
      return null;
    }
    return (
      <span className="row-tight">
        <OpenWindowNow worker={worker} name={name} subject={subject} after={after} />
        <Link className="btn btn-sm" to={addressOf("/settings", { section: "autonomy", worker })} installation>
          Run unattended
        </Link>
      </span>
    );
  }
  const place = placeOf(wait);
  return place ? (
    <Link className="btn btn-sm" to={place.to} installation={place.installation}>
      {place.label}
    </Link>
  ) : null;
}

// ReadyWaitNote is a ready work order's wait, in words, with what a member can do about it.
export function ReadyWaitNote({ wait, subject, after }: { wait: ReadyWait; subject?: string; after?: () => Promise<void> | void }) {
  return (
    <span className="stack-8">
      <span className="cell-note">
        <ReadyWaitWords wait={wait} />
      </span>
      <ReadyWaitActions wait={wait} subject={subject} after={after} />
    </span>
  );
}
