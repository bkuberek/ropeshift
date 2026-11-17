import { useRef, useState } from "react";

import { actOnWork, readView } from "../api/client";
import type { Completed, InProgress, Lane, OnHold, ShopFloor, WorkOrder } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { useChange } from "../admin/parts";
import { addressOf, Link, useArrivalFocus } from "../app/route";
import { LanePane } from "../lanes/LanePane";
import { useTalk } from "../talk/state";
import { Button } from "../ui/controls";
import { EmptyState, LaneBadge, Refusal, TargetChip } from "../ui/display";
import { Icon } from "../ui/Icon";
import { Outline } from "./Outline";
import { StartSession } from "./StartSession";

// A planning session: its work order, and the lane that runs it once a worker reported one.
export type Session = { order: WorkOrder; lane: Lane | undefined };

// sessionsOf lists the planning sessions a project has now: the `plan` work orders whose lane runs, and those
// whose lane is parked. A session that has not reported its lane yet is listed by its work order alone.
export function sessionsOf(running: InProgress | null, held: OnHold | null): Session[] {
  const lines = [...(running?.items ?? []), ...(held?.items ?? [])];
  return lines.filter((line) => line.work_order.kind === "plan").map((line) => ({ order: line.work_order, lane: line.lane }));
}

// An ended session is its work order and the lane that ran it last.
export type EndedSession = { order: WorkOrder; lane: string };

// lastSessionOf finds the last planning session a member accepted, from completed work, which lists the newest
// end first, with the lane of its latest artifact. A session that failed or was canceled has no plan anyone
// accepted, and one that left no artifact names no lane to read.
export function lastSessionOf(completed: Completed | null): EndedSession | undefined {
  for (const item of completed?.items ?? []) {
    const lane = item.standing.artifacts.findLast((artifact) => artifact.lane_id)?.lane_id;
    if (item.work_order.kind === "plan" && item.work_order.state === "done" && lane) {
      return { order: item.work_order, lane };
    }
  }
  return undefined;
}

// Completed work changes only when a work order ends, so a burst of events reads it once a gap.
const completedGapMs = 5000;

// LastSession shows the plan of the last session a member accepted, at the commit they accepted, so the outline
// it submitted stays in reach once its lane has ended and its branch is gone.
function LastSession({ session, live }: { session: EndedSession; live: Live }) {
  return (
    <section className="stack" aria-label="The last planning session">
      <p className="muted">
        The last session, <Link to={addressOf("/work-order", { id: session.order.id })}>{session.order.title || session.order.id}</Link>, on lane{" "}
        <span className="mono">{session.lane}</span>, has ended. Its plan, at the commit that was accepted:
      </p>
      <Outline lane={session.lane} live={live} title="Plan of the last session" settled />
    </section>
  );
}

const planningWords = "Writes the plan and its draft decisions on its own branch. Approving the plan stays yours.";

// PlanningPage is the planning pane: a session's header, its live pane through the worker, and the plan it is
// writing, read from its branch.
export function PlanningPage({ live, project, label, lane, after }: { live: Live; project: string; label: string; lane: string; after: () => Promise<void> }) {
  const running = useView<InProgress>(() => readView<InProgress>("in-progress"), "planning:running", live);
  const held = useView<OnHold>(() => readView<OnHold>("on-hold"), "planning:held", live);
  const floor = useView<ShopFloor>(() => readView<ShopFloor>("shop-floor"), "planning:floor", live);
  const [attached, setAttached] = useState(true);
  const talk = useTalk();
  const resume = useChange();
  const heading = useRef<HTMLHeadingElement>(null);
  useArrivalFocus(heading);

  const sessions = sessionsOf(running.view, held.view);
  const ready = running.view !== null && held.view !== null;
  const failure = running.failure || held.failure;
  // The lane the address names, else the first session that has one. A lane the page has not heard of yet is
  // shown by its address, because a session that just started may not have reported its lane.
  const chosen = sessions.find((session) => session.lane?.id === lane) ?? (lane ? undefined : sessions.find((session) => session.lane));
  const laneId = lane || chosen?.lane?.id || "";
  // Completed work is read only while no session is shown, for the session that ended last.
  const idle = ready && !laneId;
  const completed = useView<Completed | null>(() => (idle ? readView<Completed>("completed") : Promise.resolve(null)), `planning:completed:${idle}`, live, completedGapMs);
  const last = idle ? lastSessionOf(completed.view) : undefined;

  if (!laneId) {
    return (
      <div className="screen">
        <div className="page-head">
          <div className="page-head-text">
            <h1 ref={heading} tabIndex={-1}>
              Planning
            </h1>
            <p className="page-note">The planning session writes the plan with you, in a terminal pane.</p>
          </div>
          <div className="page-actions">
            <StartSession project={project} />
          </div>
        </div>
        {failure ? <Refusal>{failure}</Refusal> : null}
        {ready ? (
          <div className="panel">
            <EmptyState icon="flag" title="No planning session is running" action={<StartSession project={project} variant="primary" />}>
              Start one to write a plan, or to adopt a project that already exists. Its pane and the plan it writes show here.
            </EmptyState>
          </div>
        ) : null}
        {last ? <LastSession session={last} live={live} /> : null}
      </div>
    );
  }

  const order = chosen?.order;
  const found = chosen?.lane;
  const worker = found?.worker_id ? (floor.view?.workers.find((line) => line.worker.id === found.worker_id)?.worker.name ?? found.worker_id) : "";
  const parked = found?.actual_state === "parked";
  const title = order?.title || "Planning session";
  return (
    <div className="screen screen-flush planning">
      <div className="convo-head t-planning">
        <div className="convo-head-top">
          <TargetChip kind="planning" />
          <h1 ref={heading} tabIndex={-1} className="convo-title truncate">
            {title}
          </h1>
          <span className="spacer" />
          {found ? <LaneBadge state={found.actual_state} /> : null}
          <Button small icon="chat" onClick={() => talk.select("planning", laneId, { open: true, focus: true })}>
            Talk to the planning session
          </Button>
          <Button small onClick={() => setAttached(!attached)}>
            {attached ? "Detach" : "Attach"}
          </Button>
          <Link className="icon-btn icon-btn-sm" to="/conversations" aria-label="Open in Conversations" title="Open in Conversations">
            <Icon name="expand" size="sm" />
          </Link>
        </div>
        <p className="convo-which">
          <span>{label}</span>
          {order ? (
            <>
              <span aria-hidden="true">·</span>
              <Link to={addressOf("/work-order", { id: order.id })} className="mono">
                {order.id}
              </Link>
            </>
          ) : null}
          <span aria-hidden="true">·</span>
          <span>
            lane <span className="mono">{laneId}</span>
            {worker ? <> on {worker}</> : null}
          </span>
        </p>
        <p className="convo-can">{planningWords}</p>
      </div>
      {failure ? <Refusal>{failure}</Refusal> : null}
      <div className="grid grid-pane">
        <div className="convo-term">
          <div className="pane-strip t-planning">
            <Icon name="attach" size="sm" />
            <span>
              {parked
                ? "The session is parked, so it has no process. Resume wakes it with your word."
                : attached
                  ? "You are attached. Keys you type in the pane go to the session as you, at its terminal."
                  : "You are detached. The session keeps running, and what you type here goes nowhere."}
            </span>
            <span className="spacer" />
            {parked && order ? (
              <Button
                small
                variant="primary"
                loading={resume.busy}
                onClick={() =>
                  void resume.run(async () => {
                    await actOnWork(order.id, "resume", "");
                    await after();
                  })
                }
              >
                Resume
              </Button>
            ) : null}
          </div>
          {resume.failure ? <Refusal>{resume.failure}</Refusal> : null}
          <LanePane lane={laneId} live={live} attached={attached && !parked} state={found?.actual_state} starting={!found} />
        </div>
        <Outline lane={laneId} live={live} />
      </div>
    </div>
  );
}
