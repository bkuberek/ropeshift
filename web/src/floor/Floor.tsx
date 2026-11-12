import { useId, useState } from "react";

import { actOnWork, inProjectOf, readView, workActions } from "../api/client";
import type { FloorLane, Project, ShopFloor, Stuck, StuckItem, WorkAction, WorkerLine } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, go, Link } from "../app/route";
import { useAct } from "../needs/useAct";
import { actionWords } from "../needs/WorkCard";
import { Ago, Page, StampLine, useClock, ViewTime, words } from "../parts/parts";
import { Icon } from "../ui/Icon";
import { Button, Segmented } from "../ui/controls";
import { Absent, Banner, ConditionChip, EmptyState, Id, LaneBadge, ProjectTag, Refusal, SkeletonRows, TableRegion, TargetChip } from "../ui/display";
import { Field } from "../ui/forms";
import { Dialog, Menu } from "../ui/overlays";
import { laneLabel } from "./states";

type Scope = "project" | "all";

// A lane of the floor, with the project it belongs to and what the server offers to do about it.
type Row = { lane: FloorLane; project: string; own: boolean; stuck?: StuckItem };

// What each act does, which its dialog says before a person presses it.
const actWords: Partial<Record<WorkAction, string>> = {
  respawn: "Ends the current generation and starts the next, which counts toward the day's generations.",
  reset: "Closes the breaker and starts the lane again. A member's reset may pass the daily generation cap.",
  resume: "Lets the parked lane go on now, without waiting for what it waits on.",
  reassign: "Ends the lapsed holder's claim, so the work can be claimed again.",
  resolve: "Returns the blocked work to ready.",
  replan: "Sends the blocked work back to planning, with the reason and the lane's evidence.",
  cancel: "Ends the work for good, which stops its lane.",
};

function LaneActions({ row, after }: { row: Row; after: () => Promise<void> }) {
  const [act, setAct] = useState<WorkAction | null>(null);
  const [reason, setReason] = useState("");
  const { act: run, busy, failure } = useAct(after);
  const actions = (row.stuck?.actions ?? []).filter((action): action is WorkAction => action !== "start_orchestrator");
  const workOrder = row.stuck?.work_order?.id ?? row.lane.work_order;
  const needsReason = act ? workActions[act].needsReason : false;
  const send = async () => {
    if (act && (await run(() => actOnWork(workOrder, act, needsReason ? reason.trim() : "")))) {
      setAct(null);
      setReason("");
    }
  };
  return (
    <span className="row-actions">
      <Link to={addressOf("/lane", { id: row.lane.id })} className="btn btn-sm">
        Open lane
      </Link>
      {actions.length ? <Menu label={`Actions for lane ${row.lane.id}`} items={actions.map((action) => ({ label: `${actionWords[action]}…`, onSelect: () => setAct(action) }))} /> : null}
      <Dialog
        open={act !== null}
        title={act ? `${actionWords[act]}: lane ${row.lane.id}` : ""}
        onClose={() => setAct(null)}
        footer={
          <>
            <span className="spacer" />
            <Button onClick={() => setAct(null)}>Cancel</Button>
            <Button variant={act === "cancel" ? "danger" : "primary"} loading={busy} disabled={busy || (needsReason && reason.trim() === "")} onClick={() => void send()}>
              {act ? actionWords[act] : ""}
            </Button>
          </>
        }
      >
        <p>
          The lane is <strong>{laneLabel(row.lane.actual_state)}</strong> on work order <span className="mono">{workOrder}</span>. {act ? actWords[act] : ""}
        </p>
        {needsReason ? (
          <Field label="Reason (required)">
            <textarea className="textarea" rows={2} autoComplete="off" value={reason} onChange={(event) => setReason(event.target.value)} />
          </Field>
        ) : null}
        {failure ? <Refusal>{failure}</Refusal> : null}
      </Dialog>
    </span>
  );
}

function LaneTable({ rows, label, after }: { rows: Row[]; label: string; after: () => Promise<void> }) {
  return (
    <TableRegion label={label}>
      <table className="table floor-table stack-on-phone">
        <thead>
          <tr>
            <th scope="col">Lane</th>
            <th scope="col">Work order</th>
            <th scope="col">State</th>
            <th scope="col">Liveness</th>
            <th scope="col">Reach</th>
            <th scope="col" className="r">
              Gen
            </th>
            <th scope="col" className="r">
              Context
            </th>
            <th scope="col" className="r">
              CPU · Mem
            </th>
            <th scope="col">Freshness</th>
            <th scope="col" className="actions-cell">
              <span className="visually-hidden">Actions</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={`${row.project}:${row.lane.id}`} data-lane={row.lane.id} className={row.lane.freshness === "observed" ? undefined : "row-reported"}>
              <td data-label="Lane">
                <Id value={row.lane.id} />
              </td>
              <td data-label="Work order" className="title-cell">
                {row.own ? (
                  <Link to={addressOf("/work-order", { id: row.lane.work_order })} data-item>
                    {row.lane.work_order_title || row.lane.work_order}
                  </Link>
                ) : (
                  <span>{row.lane.work_order_title || row.lane.work_order}</span>
                )}
                <span className="cell-note">
                  {row.own ? null : <ProjectTag name={row.project} />} <Id value={row.lane.work_order} />
                </span>
              </td>
              <td data-label="State">
                <span className="row-tight">
                  <LaneBadge state={row.lane.actual_state} />
                  {(row.lane.conditions ?? []).map((condition) => (
                    <ConditionChip key={condition} name={condition} />
                  ))}
                </span>
              </td>
              <td data-label="Liveness">{words(row.lane.liveness)}</td>
              <td data-label="Reach">
                {row.lane.model_reach ? <span>Model {words(row.lane.model_reach).toLowerCase()}</span> : null}
                {row.lane.ledger_reach ? <span className="cell-note">Ledger {words(row.lane.ledger_reach).toLowerCase()}</span> : null}
              </td>
              <td data-label="Generation" className="r">
                {row.lane.generation}
              </td>
              <td data-label="Context" className="r">
                <Absent>unavailable</Absent>
              </td>
              <td data-label="CPU · Mem" className="r">
                <Absent>unavailable</Absent>
              </td>
              <td data-label="Freshness">
                <span className={row.lane.freshness === "observed" ? "fresh" : "fresh reported"}>
                  {row.lane.freshness === "observed" ? "Observed" : "Last reported"} <Ago at={row.lane.as_of} />
                </span>
              </td>
              <td className="actions-cell">{row.own ? <LaneActions row={row} after={after} /> : <span className="visually-hidden">No action: another project's lane</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

const counting = new Set(["provisioning", "starting", "briefing", "working", "needs_decision", "compacting", "handing_off", "idle", "backoff", "parking"]);

// Machine is one worker's panel: its name, its orchestrator, its lanes against its cap, the measures no
// source reports to this page yet, how fresh its word is, and its lanes.
function Machine({ line, rows, after }: { line: WorkerLine; rows: Row[]; after: () => Promise<void> }) {
  const heading = useId();
  const { worker, orchestrator } = line;
  const name = worker.name || worker.id;
  const cap = worker.manifest?.capacity?.max_lanes;
  const running = rows.filter((row) => counting.has(row.lane.actual_state)).length;
  return (
    <section className="panel machine" aria-labelledby={heading}>
      <div className="worker-head">
        <h2 className="worker-name" id={heading}>
          <Icon name="server" />
          {name}
        </h2>
        {orchestrator ? (
          <TargetChip kind="orchestrator" small>
            Orchestrator · {orchestrator.actual_state ? laneLabel(orchestrator.actual_state).toLowerCase() : `wanted ${words(orchestrator.desired_state).toLowerCase()}`}
            {orchestrator.current_generation ? ` · gen ${orchestrator.current_generation}` : ""}
          </TargetChip>
        ) : (
          <span className="subtle small">No orchestrator</span>
        )}
        <span className="res">
          <span>Lanes</span>
          <strong>
            {running}
            {cap ? ` / ${cap}` : ""}
          </strong>
        </span>
        {["CPU", "Memory", "Memory pressure", "Disk free", "Load per core"].map((measure) => (
          <span className="res" key={measure}>
            <span>{measure}</span>
            <Absent>unavailable</Absent>
          </span>
        ))}
        <span className="spacer" />
        <span className={line.freshness === "observed" ? "fresh" : "fresh reported"}>
          {line.freshness === "observed" ? "Observed" : "Last reported"}
          {worker.last_handshake_at ? (
            <>
              , handshake <Ago at={worker.last_handshake_at} />
            </>
          ) : null}
        </span>
      </div>
      {rows.length ? <LaneTable rows={rows} label={`Lanes on ${name}`} after={after} /> : <p className="panel-body muted">No lane runs on {name}.</p>}
    </section>
  );
}

type FloorRead = { project: string; view: ShopFloor };

// FloorBody is one panel per machine that serves the project, and a panel for lanes no machine holds.
function FloorBody({ reads, stuck, project, after }: { reads: FloorRead[]; stuck: Stuck | null; project: string; after: () => Promise<void> }) {
  const own = reads.find((read) => read.project === project)?.view;
  const workers = new Map<string, WorkerLine>();
  for (const read of reads) {
    for (const line of read.view.workers) {
      if (!workers.has(line.worker.id) || read.project === project) {
        workers.set(line.worker.id, line);
      }
    }
  }
  const rows: Row[] = reads.flatMap((read) =>
    read.view.lanes.map((lane) => ({
      lane,
      project: read.project,
      own: read.project === project,
      stuck: read.project === project ? stuck?.items.find((item) => item.lane?.id === lane.id) : undefined,
    })),
  );
  if (!own || (own.workers.length === 0 && own.lanes.length === 0)) {
    return (
      <div className="panel">
        <EmptyState icon="server" title="No worker has joined">
          Lanes appear here, one panel per machine, once a worker joins. A worker starts from a terminal on its machine, with its join token.
        </EmptyState>
      </div>
    );
  }
  const loose = rows.filter((row) => !row.lane.worker_id || !workers.has(row.lane.worker_id));
  return (
    <div className="stack">
      {[...workers.values()].map((line) => (
        <Machine key={line.worker.id} line={line} rows={rows.filter((row) => row.lane.worker_id === line.worker.id)} after={after} />
      ))}
      {loose.length ? (
        <section className="panel machine" aria-label="Lanes on no machine">
          <div className="worker-head">
            <h2 className="worker-name">On no machine yet</h2>
          </div>
          <LaneTable rows={loose} label="Lanes on no machine" after={after} />
        </section>
      ) : null}
    </div>
  );
}

// FloorPage shows the lanes on every machine that serves the project. Caps count every project's lanes on
// a machine, so the scope can show all of them, each other project's lane with its project and no action.
export function FloorPage({ live, project, projects, show, after }: { live: Live; project: string; projects: Project[]; show: string; after: () => Promise<void> }) {
  const scope: Scope = show === "all" ? "all" : "project";
  const others = projects.filter((row) => row.state === "active" && row.name !== project).map((row) => row.name);
  const own = useView<ShopFloor>(() => readView<ShopFloor>("shop-floor"), "floor:own", live);
  const rest = useView<FloorRead[]>(
    () => (scope === "all" ? Promise.all(others.map((name) => inProjectOf(name, () => readView<ShopFloor>("shop-floor")).then((view) => ({ project: name, view })))) : Promise.resolve([])),
    `floor:rest:${scope}:${others.join(",")}`,
    live,
  );
  const stuck = useView<Stuck>(() => readView<Stuck>("stuck"), "floor:stuck", live);
  const now = useClock();
  const label = projects.find((row) => row.name === project)?.display_name || project;
  const reads: FloorRead[] = own.view ? [{ project, view: own.view }, ...(scope === "all" ? (rest.view ?? []) : [])] : [];
  const failure = own.failure || rest.failure || stuck.failure;
  return (
    <Page
      title="Floor"
      note={`Lanes live on every machine that serves ${label}. Caps count lanes of every project on a machine.`}
      actions={
        others.length ? (
          <Segmented
            label="Show"
            value={scope}
            choices={[
              { value: "project", label },
              { value: "all", label: "All projects" },
            ]}
            onChange={(value) => go(addressOf("/floor", { show: value === "all" ? "all" : "" }), { stay: true })}
          />
        ) : undefined
      }
    >
      {failure ? <Banner tone="danger">{own.view ? `The floor is not current. ${failure}` : failure}</Banner> : null}
      {own.view ? (
        <ViewTime.Provider value={{ asOf: own.view.as_of, elapsed: Math.max(0, now - own.readAt) }}>
          <div aria-busy={own.busy}>
            <FloorBody reads={reads} stuck={stuck.view} project={project} after={after} />
          </div>
          <StampLine stamp={own.view} />
        </ViewTime.Provider>
      ) : failure ? null : (
        <div className="panel">
          <SkeletonRows rows={5} />
        </div>
      )}
    </Page>
  );
}
