import { useEffect, useId, useRef, useState } from "react";

import { useChange } from "../admin/parts";
import { claimAndStart, newRunIDs } from "../api/runWork";
import type { RunIDs } from "../api/runWork";
import type { WorkOrder } from "../api/types";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { labelOf, useChoices } from "./StartWork";

const titleOf = (order: WorkOrder) => order.title || order.id;

const counted = (count: number) => `${count} work order${count === 1 ? "" : "s"}`;

// A work order the server refused to start, with the API's words, and whether its claim went through before its lane
// start was refused, which leaves the work claimed.
type Refused = { order: WorkOrder; why: string; claimed: boolean };

// StartAllDialog claims every ready work order for one worker and starts each one's lane, in the list's order, as
// Start does for one. A refusal stops that work order and no other, and the dialog stays open on what was refused, so
// a second press tries those again with the IDs the first sent, which the ledger finds.
function StartAllDialog({ orders, onClose }: { orders: WorkOrder[]; onClose: () => void }) {
  const found = useChoices();
  const change = useChange();
  const toast = useToast();
  const why = useId();
  const [worker, setWorker] = useState("");
  const [refused, setRefused] = useState<Refused[]>([]);
  const ids = useRef(new Map<string, RunIDs>());
  const choices = found && "choices" in found ? found.choices : [];
  const left = refused.length ? refused.map((row) => row.order) : orders;

  useEffect(() => {
    if (choices.length === 1) {
      setWorker((was) => was || (choices[0]?.id ?? ""));
    }
  }, [found]);

  const nameOf = (id: string) => choices.find((choice) => choice.id === id)?.name ?? id;
  const reason = (() => {
    if (found === null) {
      return "Reading the workers.";
    }
    if ("failure" in found) {
      return `The workers could not be read. ${found.failure}`;
    }
    if (choices.length === 0) {
      return "No worker has joined.";
    }
    return worker === "" ? "Choose the worker that runs them." : "";
  })();

  const start = async () => {
    const failures: Refused[] = [];
    let started = 0;
    await change.run(async () => {
      for (const order of left) {
        const run = ids.current.get(order.id) ?? newRunIDs();
        ids.current.set(order.id, run);
        let claimed = false;
        try {
          const onClaimed = () => {
            claimed = true;
          };
          await claimAndStart(order.id, worker, run, { onClaimed });
          started++;
        } catch (error) {
          failures.push({ order, why: error instanceof Error ? error.message : String(error), claimed });
        }
      }
    });
    if (started) {
      toast("success", `Started ${counted(started)} on ${nameOf(worker)}.`);
    }
    if (failures.length === 0) {
      onClose();
      return;
    }
    setRefused(failures);
  };

  return (
    <Dialog
      open
      title="Start all ready work"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" icon="play" loading={change.busy} disabled={reason !== ""} aria-describedby={reason ? why : undefined} onClick={() => void start()}>
            {refused.length ? `Try ${counted(refused.length)} again` : `Start ${counted(left.length)}`}
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
        <p className="small muted">Claims each ready work order for the worker, then asks the worker to start its lane, one after another.</p>
        <Field label="Worker" help="The machine that runs the lanes. Workers the daemon sees now come first.">
          <Select value={worker} disabled={refused.some((row) => row.claimed)} onChange={(event) => setWorker(event.target.value)}>
            <option value="">{found === null ? "Reading…" : choices.length === 0 ? "No worker has joined" : "Choose a worker"}</option>
            {choices.map((choice) => (
              <option key={choice.id} value={choice.id}>
                {labelOf(choice)}
              </option>
            ))}
          </Select>
        </Field>
        <ul className="list-rows" aria-label={refused.length ? "Refused" : "Ready work"}>
          {refused.length
            ? refused.map((row) => (
                <li key={row.order.id} className="list-row">
                  <span className="list-row-title">{titleOf(row.order)}</span>
                  {row.claimed ? <p className="small">The work is claimed on {nameOf(worker)}, and no lane has started.</p> : null}
                  <Refusal>{row.why}</Refusal>
                </li>
              ))
            : orders.map((order) => (
                <li key={order.id} className="list-row">
                  <span className="list-row-title">{titleOf(order)}</span>
                </li>
              ))}
        </ul>
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

// StartAllReady is the button that starts every ready work order a member may claim, and nothing when there is none.
export function StartAllReady({ orders }: { orders: WorkOrder[] }) {
  const [open, setOpen] = useState(false);
  if (orders.length === 0 && !open) {
    return null;
  }
  return (
    <>
      <Button icon="play" onClick={() => setOpen(true)}>
        Start all ready
      </Button>
      {open ? <StartAllDialog orders={orders} onClose={() => setOpen(false)} /> : null}
    </>
  );
}
