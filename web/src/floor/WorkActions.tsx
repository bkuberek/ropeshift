import { useEffect, useRef, useState } from "react";

import { archiveLane, readWorkers, releaseClaim } from "../api/admin";
import { ApiError, actOnWork, sendBack } from "../api/client";
import type { Allowance, FloorLane, WorkAction, WorkOrder } from "../api/types";
import { usePageActions } from "../app/pageActions";
import type { Entry } from "../app/ranking";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field } from "../ui/forms";
import { Dialog, Menu, useToast } from "../ui/overlays";
import { workStates } from "./states";
import type { RecoverAction } from "./recoverSpecs";
import { actionOfSlug, labelOf, recoveries, specs } from "./recoverSpecs";

// The recovery actions of a work order, besides starting it: retry failed work, give a claim back, send work
// back to planning, end a lapsed claim, take blocked work out of `blocked`, cancel work, and ask for the
// worktree of a lane that ended to be removed. The server says which of them the work order allows now, from its
// state machine, and this offers those and no others. Each opens a dialog that says what it does first, and the
// ones that change who owns the work ask why.

const titleOf = (order: WorkOrder) => order.title || order.id;

// A change that sends, one at a time, and keeps the refusal with its code, which the archive reads.
function useSend() {
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<{ code: string; message: string } | null>(null);
  const running = useRef(false);
  const send = async (change: () => Promise<unknown>): Promise<boolean> => {
    if (running.current) {
      return false;
    }
    running.current = true;
    setBusy(true);
    setFailure(null);
    try {
      await change();
      return true;
    } catch (error) {
      setFailure(error instanceof ApiError ? { code: error.code, message: error.message } : { code: "", message: String(error) });
      return false;
    } finally {
      running.current = false;
      setBusy(false);
    }
  };
  return { send, busy, failure };
}

// useHolder names the worker that holds a claim: the name its machine gave it, or else its ID when the
// workers cannot be read.
function useHolder(id: string | undefined): string {
  const [name, setName] = useState("");
  useEffect(() => {
    if (!id) {
      return;
    }
    let current = true;
    readWorkers()
      .then((list) => current && setName(list.workers.find((worker) => worker.id === id)?.name ?? ""))
      .catch(() => undefined);
    return () => {
      current = false;
    };
  }, [id]);
  return id ? name || id : "";
}

const stateWords = (order: WorkOrder) => (workStates[order.state]?.label ?? order.state).toLowerCase();

// done is what a toast says once an action went through.
const done: Record<RecoverAction, (title: string) => string> = {
  retry: (title) => `Returned ${title} to ready for another attempt.`,
  release: (title) => `Gave back the claim on ${title}.`,
  send_back: (title) => `Sent ${title} back to planning.`,
  reassign: (title) => `Ended the claim on ${title}.`,
  resolve: (title) => `Returned ${title} to ready.`,
  replan: (title) => `Sent ${title} back to planning.`,
  cancel: (title) => `Canceled ${title}.`,
  archive_lane: () => "Asked the worker to remove the worktree.",
};

type Choice = { action: RecoverAction; lane?: FloorLane };

// WorkActionDialog asks a member to confirm one recovery action on a work order, and sends it. Retrying, sending
// back, reassigning, resolving, replanning, and canceling ask why. Giving a claim back names the worker that
// holds it. Archiving a worktree asks first as the worker would, and when the worktree holds work no remote
// has, it says so and offers to archive anyway.
export function WorkActionDialog({
  order,
  action,
  allowance,
  lane,
  onClose,
  after,
}: {
  order: WorkOrder;
  action: RecoverAction;
  allowance?: Allowance;
  lane?: FloorLane;
  onClose: () => void;
  after?: () => Promise<void>;
}) {
  const spec = specs[action];
  const title = titleOf(order);
  const name = labelOf(action, allowance);
  const toast = useToast();
  const { send, busy, failure } = useSend();
  const [reason, setReason] = useState("");
  // Archiving that the worker refused, for work no remote holds, which a second press may force.
  const [unpushed, setUnpushed] = useState("");
  const holder = useHolder(action === "release" ? order.lease_worker : undefined);
  const missing = action === "release" && (!order.claim_id || !order.fence) ? "This page does not know the claim, so it cannot name it. Read the page again." : "";
  const lacking = spec.reason && reason.trim() === "";
  const forcing = unpushed !== "";
  const label = forcing ? "Archive anyway" : name;

  const press = async () => {
    const sent = await send(async () => {
      switch (action) {
        case "release":
          return releaseClaim(order.id, order.claim_id ?? "", order.fence ?? 0);
        case "archive_lane":
          return archiveLane(order.id, lane?.id ?? "", forcing);
        case "send_back":
          return sendBack(order.id, reason.trim());
        default:
          return actOnWork(order.id, action as WorkAction, reason.trim());
      }
    });
    if (sent) {
      toast("success", done[action](title));
      onClose();
      await after?.();
    }
  };

  // A refusal for work no remote holds is not a failure to show alone: it is why the next press is a forced one.
  useEffect(() => {
    if (action === "archive_lane" && failure?.code === "unpushed_work") {
      setUnpushed(failure.message);
    }
  }, [action, failure]);

  const shown = failure && !(action === "archive_lane" && failure.code === "unpushed_work") ? failure.message : "";
  return (
    <Dialog
      open
      title={action === "archive_lane" ? `${name}: lane ${lane?.id ?? ""}` : `${name}: ${title}`}
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          {/* The button stays enabled while it sends, as the other dialogs' do, so focus stays in the dialog and Escape still closes it. A second press finds a change running and does nothing. */}
          <Button
            variant={spec.destructive || forcing ? "danger" : "primary"}
            icon={spec.icon}
            loading={busy}
            disabled={lacking || missing !== ""}
            onClick={() => void press()}
          >
            {label}
          </Button>
        </>
      }
    >
      <form
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          if (!lacking && !missing) {
            void press();
          }
        }}
      >
        {action === "archive_lane" ? (
          <p className="small">
            Lane <span className="mono">{lane?.id}</span> of work order <span className="mono">{order.id}</span> has ended.
          </p>
        ) : (
          <p className="small">
            Work order <span className="mono">{order.id}</span> is {stateWords(order)}.
          </p>
        )}
        <p className="small">{spec.says}</p>
        {action === "retry" && allowance?.extra_attempt ? (
          <p className="small">
            It used every attempt it was given{order.max_attempts ? `, ${order.attempts ?? 0} of ${order.max_attempts}` : ""}, so this adds one. Only a member can add an attempt.
          </p>
        ) : null}
        {action === "release" ? (
          <p className="small">
            {holder ? (
              <>
                <strong>{holder}</strong> holds the claim.
              </>
            ) : (
              "No worker holds the claim."
            )}{" "}
            Its lane, if one was asked for, stops.
          </p>
        ) : null}
        {forcing ? (
          <>
            <Refusal>{unpushed}</Refusal>
            <p className="small">
              Archiving anyway removes the worktree with the work no remote holds. The worker saves what it can to a ref first, and a removal it could have saved loses nothing.
            </p>
          </>
        ) : null}
        {spec.reason ? (
          <Field label="Reason (required)">
            <textarea className="textarea" rows={2} autoComplete="off" value={reason} onChange={(event) => setReason(event.target.value)} />
          </Field>
        ) : null}
        {missing ? <p className="small muted">{missing}</p> : null}
        {shown ? <Refusal>{shown}</Refusal> : null}
      </form>
    </Dialog>
  );
}

// WorkActions are a work order's recovery actions, as a page's header offers them: Retry as a button when the
// work failed, and every other action in a menu of its own. A decision card the page shows beside it offers some
// of them already, which omit lists, so the same action is not on the page twice. The command palette lists them
// all while the page stands. opened names the action an address asks to start on, and lanes are the lanes of
// this work order that a worktree may be removed for.
export function WorkActions({
  order,
  allowance,
  lanes = [],
  omit = [],
  opened = "",
  onClosed,
  after,
}: {
  order: WorkOrder;
  allowance?: Allowance;
  lanes?: FloorLane[];
  omit?: string[];
  opened?: string;
  onClosed?: () => void;
  after?: () => Promise<void>;
}) {
  const [open, setOpen] = useState<Choice | null>(null);
  const archivable = lanes.filter((lane) => lane.archivable);
  const offered = recoveries(allowance);
  // The actions that stand for a lane stand once for each lane that has a worktree to remove.
  const choices = offered.flatMap((action): Choice[] => (action === "archive_lane" ? archivable.map((lane) => ({ action, lane })) : [{ action }]));
  const title = titleOf(order);

  // An address that names an action opens it once the server says the work allows it, which for a worktree waits
  // for the floor's lanes.
  const standing = choices.map((choice) => `${choice.action}:${choice.lane?.id ?? ""}`).join(",");
  useEffect(() => {
    const asked = actionOfSlug(opened);
    const found = asked ? choices.find((choice) => choice.action === asked) : undefined;
    if (found) {
      setOpen(found);
    }
  }, [opened, standing]);

  const nameOf = (choice: Choice) => (choice.lane ? `${specs[choice.action].label} of lane ${choice.lane.id}` : labelOf(choice.action, allowance));
  const entries: Entry[] = choices.map((choice) => ({
    key: `action:${choice.action}:${order.id}${choice.lane ? `:${choice.lane.id}` : ""}`,
    group: "Actions",
    id: order.id,
    title: `${nameOf(choice)}: ${title}`,
    icon: specs[choice.action].icon,
    keywords: "recover work",
    run: () => setOpen(choice),
  }));
  usePageActions(entries);

  const close = () => {
    setOpen(null);
    onClosed?.();
  };
  const buttons = choices.filter((choice) => choice.action === "retry" && !omit.includes("retry"));
  const menu = choices.filter((choice) => choice.action !== "retry" && !omit.includes(choice.action));
  return (
    <>
      {buttons.map((choice) => (
        <Button key={choice.action} variant="primary" icon={specs[choice.action].icon} onClick={() => setOpen(choice)}>
          {labelOf(choice.action, allowance)}
        </Button>
      ))}
      {menu.length ? (
        <Menu
          label="More actions"
          showLabel
          icon="more"
          items={menu.map((choice) => ({ label: `${nameOf(choice)}…`, icon: specs[choice.action].icon, onSelect: () => setOpen(choice) }))}
        />
      ) : null}
      {open ? <WorkActionDialog order={order} action={open.action} allowance={allowance} lane={open.lane} onClose={close} after={after} /> : null}
    </>
  );
}
