import { useEffect, useId, useRef, useState } from "react";

import { newID } from "../admin/logic";
import { useChange } from "../admin/parts";
import { readAutonomy, readWorkers } from "../api/admin";
import { readView } from "../api/client";
import { claimAndStart, startClaimed } from "../api/runWork";
import type { Allowance, ShopFloor, WorkOrder } from "../api/types";
import { usePageActions } from "../app/pageActions";
import type { Entry } from "../app/ranking";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { counting, workStates } from "./states";

// What a member can do to move a work order toward a lane: claim it for a worker and start its lane, start
// the lane of work already claimed, or nothing yet because the work is not ready. The server says which, from
// the state machine: a lane runs on claimed work, and only ready work is claimed.
export type Start = { kind: "claim" } | { kind: "lane" } | { kind: "wait"; why: string };

// beforeReady are the states a work order passes through before it is ready, which a person is told of when
// the work cannot start.
const beforeReady = new Set(["intake", "planning", "contract_review"]);

// startOf says what starting a work order does now, or nothing for work that is running or ended. The
// allowance the plans view holds of the work order says whether it may be claimed, or its claim may start a lane.
export function startOf(order: WorkOrder, allowed: Allowance | undefined): Start | null {
  if (allowed?.actions.includes("claim")) {
    return { kind: "claim" };
  }
  if (allowed?.actions.includes("start_lane")) {
    return { kind: "lane" };
  }
  if (beforeReady.has(order.state)) {
    return { kind: "wait", why: `Only ready work can start. This work is in ${(workStates[order.state]?.label ?? order.state).toLowerCase()}.` };
  }
  return null;
}

// A worker the dialog offers, with what a member weighs: whether the daemon sees it now, its lanes against
// its cap, and the level that says whether it claims on its own.
type Choice = { id: string; name: string; connected: boolean | null; lanes: number | null; cap?: number; level?: string };

function labelOf(choice: Choice): string {
  const parts = [choice.name];
  if (choice.connected !== null) {
    parts.push(choice.connected ? "connected" : "last reported");
  }
  if (choice.lanes !== null) {
    parts.push(choice.cap ? `${choice.lanes} of ${choice.cap} lanes` : `${choice.lanes} ${choice.lanes === 1 ? "lane" : "lanes"}`);
  }
  if (choice.level) {
    parts.push(choice.level);
  }
  return parts.join(" · ");
}

type Choices = { choices: Choice[] } | { failure: string } | null;

// useChoices reads the workers the installation holds, with what the floor and each worker's autonomy say
// of it, once the dialog opens. A worker the floor or its autonomy cannot be read for is still offered,
// with less said of it, because the API refuses what a worker may not do.
function useChoices(): Choices {
  const [read, setRead] = useState<Choices>(null);
  useEffect(() => {
    let current = true;
    void (async () => {
      try {
        const [list, floor] = await Promise.all([readWorkers(), readView<ShopFloor>("shop-floor").catch(() => null)]);
        const levels = await Promise.all(list.workers.map((worker) => readAutonomy(worker.id).catch(() => null)));
        const choices = list.workers.map<Choice>((worker, at) => {
          const line = floor?.workers.find((found) => found.worker.id === worker.id);
          return {
            id: worker.id,
            name: worker.name || worker.id,
            connected: floor ? line?.freshness === "observed" : null,
            lanes: floor ? floor.lanes.filter((lane) => lane.worker_id === worker.id && counting.has(lane.actual_state)).length : null,
            cap: worker.manifest?.capacity?.max_lanes,
            level: levels[at]?.level,
          };
        });
        // The workers the daemon sees now come first, and each group keeps the order the list gave.
        const seen = (choice: Choice) => (choice.connected ? 0 : 1);
        if (current) {
          setRead({ choices: choices.map((choice, at) => ({ choice, at })).sort((a, b) => seen(a.choice) - seen(b.choice) || a.at - b.at).map((row) => row.choice) });
        }
      } catch (error) {
        if (current) {
          setRead({ failure: error instanceof Error ? error.message : String(error) });
        }
      }
    })();
    return () => {
      current = false;
    };
  }, []);
  return read;
}

const titleOf = (order: WorkOrder) => order.title || order.id;

// StartDialog claims a ready work order for a worker and starts its lane, or starts the lane of work that is
// claimed, as `ropeshift work claim` and `ropeshift lane start` do. A claim that went through before a lane
// start that was refused leaves the work claimed, says so, and offers Start lane.
function StartDialog({ order, kind, onClose, after }: { order: WorkOrder; kind: "claim" | "lane"; onClose: () => void; after?: () => Promise<void> }) {
  const found = useChoices();
  const change = useChange();
  const toast = useToast();
  const why = useId();
  const [mode, setMode] = useState(kind);
  const [worker, setWorker] = useState("");
  const [lane, setLane] = useState("");
  const [claimedOn, setClaimedOn] = useState("");
  // The IDs of this attempt hold until the dialog closes, so a press after a refusal finds what the first made.
  const claim = useRef(newID());
  const named = useRef(newID());
  const choices = found && "choices" in found ? found.choices : [];
  const title = titleOf(order);

  useEffect(() => {
    if (choices.length === 1) {
      setWorker((was) => was || (choices[0]?.id ?? ""));
    }
  }, [found]);

  const nameOf = (id: string | undefined) => choices.find((choice) => choice.id === id)?.name ?? id ?? "its worker";
  const holder = nameOf(claimedOn || order.lease_worker);
  const claiming = mode === "claim";
  const reason = (() => {
    if (!claiming) {
      return "";
    }
    if (found === null) {
      return "Reading the workers.";
    }
    if ("failure" in found) {
      return `The workers could not be read. ${found.failure}`;
    }
    if (choices.length === 0) {
      return "No worker has joined. A worker joins from a terminal on its machine, with its join token.";
    }
    return worker === "" ? "Choose the worker that runs it." : "";
  })();
  const laneID = lane.trim() || named.current;

  const start = async () => {
    const started = await change.run(async () => {
      if (claiming) {
        const onClaimed = () => {
          setClaimedOn(nameOf(worker));
          setMode("lane");
        };
        await claimAndStart(order.id, worker, { claim: claim.current, lane: laneID }, { onClaimed });
        return `Claimed ${title} on ${nameOf(worker)}. Its lane is starting.`;
      }
      await startClaimed(order.id, laneID);
      return `Started the lane of ${title} on ${holder}.`;
    });
    if (started) {
      toast("success", started);
      onClose();
      await after?.();
    }
  };

  return (
    <Dialog
      open
      title={mode === "claim" ? `Start ${title}` : `Start lane: ${title}`}
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          {/* The button stays enabled while it sends, as the other dialogs' do, so focus stays in the dialog and Escape still closes it. A second press finds a change running and does nothing. */}
          <Button variant="primary" icon="play" loading={change.busy} disabled={reason !== ""} aria-describedby={reason ? why : undefined} onClick={() => void start()}>
            {mode === "claim" ? "Start" : "Start lane"}
          </Button>
        </>
      }
    >
      <form
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          if (reason === "") {
            void start();
          }
        }}
      >
        {mode === "claim" ? (
          <>
            <p className="small muted">Claims the work for a worker, then asks the worker to start its lane.</p>
            <Field label="Worker" help="The machine that runs the lane. Workers the daemon sees now come first.">
              <Select value={worker} onChange={(event) => setWorker(event.target.value)}>
                <option value="">{found === null ? "Reading…" : choices.length === 0 ? "No worker has joined" : "Choose a worker"}</option>
                {choices.map((choice) => (
                  <option key={choice.id} value={choice.id}>
                    {labelOf(choice)}
                  </option>
                ))}
              </Select>
            </Field>
          </>
        ) : claimedOn ? (
          <p className="small">The work is claimed on {holder}, and no lane has started.</p>
        ) : (
          <p className="small muted">The work is claimed on {holder}. Starting the lane asks that worker to run it.</p>
        )}
        <Field label="Lane ID (optional)" help="Leave it empty and ropeshift names the lane.">
          <input className="input mono" autoComplete="off" value={lane} onChange={(event) => setLane(event.target.value)} />
        </Field>
        {reason ? (
          <p className="small muted" id={why}>
            {reason}
          </p>
        ) : null}
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </form>
    </Dialog>
  );
}

// StartWork is the button that opens the dialog, wherever a view offers to move a work order forward: Start on
// ready work, Start lane on claimed work with no lane, and a disabled Start with its reason on work that is
// not ready. It offers nothing on work that runs or has ended. A view that shows one work order sets inView,
// which lists the same action in the command palette. opened opens the dialog, as a link into the page does.
export function StartWork({
  order,
  allowed,
  small = false,
  inView = false,
  opened = false,
  onClosed,
  after,
}: {
  order: WorkOrder;
  allowed?: Allowance;
  small?: boolean;
  inView?: boolean;
  opened?: boolean;
  onClosed?: () => void;
  after?: () => Promise<void>;
}) {
  const start = startOf(order, allowed);
  const [open, setOpen] = useState(opened);
  const reason = useId();
  useEffect(() => {
    if (opened) {
      setOpen(true);
    }
  }, [opened]);
  const entries: Entry[] =
    inView && start && start.kind !== "wait"
      ? [
          {
            key: `action:start:${order.id}`,
            group: "Actions",
            id: order.id,
            title: `${start.kind === "claim" ? "Start work" : "Start lane"}: ${titleOf(order)}`,
            icon: "play",
            keywords: "claim run worker lane",
            run: () => setOpen(true),
          },
        ]
      : [];
  usePageActions(entries);
  if (!start) {
    return null;
  }
  const close = () => {
    setOpen(false);
    onClosed?.();
  };
  if (start.kind === "wait") {
    return (
      <span className="row-tight">
        <Button variant="primary" small={small} icon="play" disabled aria-describedby={reason}>
          Start
        </Button>
        <span className="small muted" id={reason}>
          {start.why}
        </span>
      </span>
    );
  }
  const verb = start.kind === "claim" ? "Start" : "Start lane";
  return (
    <>
      <Button variant="primary" small={small} icon="play" aria-label={small ? `${verb} ${titleOf(order)}` : undefined} onClick={() => setOpen(true)}>
        {verb}
      </Button>
      {open ? <StartDialog order={order} kind={start.kind} onClose={close} after={after} /> : null}
    </>
  );
}
