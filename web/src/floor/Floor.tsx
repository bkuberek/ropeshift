import { useId, useState } from "react";

import { actOnWork, inProjectOf, readView, workActions } from "../api/client";
import type { FloorLane, Project, ResourceReport, ShopFloor, Stuck, StuckItem, WorkAction, WorkerLine } from "../api/types";
import { useHealth } from "../api/useHealth";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, go, Link } from "../app/route";
import { useAct } from "../needs/useAct";
import { actionWords } from "../needs/WorkCard";
import { Ago, Page, StampLine, useClock, ViewTime, words } from "../parts/parts";
import { Icon } from "../ui/Icon";
import { Button, Segmented } from "../ui/controls";
import { Absent, Banner, ConditionChip, EmptyState, Freshness, freshnessOf, Id, LaneBadge, Panel, ProjectTag, Refusal, SkeletonRows, TableRegion, TargetChip, WorkBadge } from "../ui/display";
import { Field } from "../ui/forms";
import { Dialog, Menu } from "../ui/overlays";
import { Talkable, TalkToLane, useTalkable } from "../talk/controls";
import { useTalk } from "../talk/state";
import { SessionUse } from "./Installation";
import { machineMeasures, sessionOf } from "./resources";
import { ReviewOf } from "./ReviewOf";
import { StartWork, startOf } from "./StartWork";
import { counting, laneLabel } from "./states";
import { WorkActionDialog } from "./WorkActions";
import type { RecoverAction } from "./recoverSpecs";
import { labelOf, recoveries, specs } from "./recoverSpecs";
import type { WorkIndex, WorkRow } from "./workIndex";
import { useWorkIndex } from "./workIndex";

type Scope = "project" | "all";

// A lane of the floor, with the project it belongs to, what the server offers to do about its work order, and
// what it offers to do about the lane itself.
type Row = { lane: FloorLane; project: string; own: boolean; stuck?: StuckItem; work?: WorkRow };

// What each act on the lane itself does, which its dialog says before a person presses it. What a member can do
// to the work order is the work actions' own.
const actWords: Partial<Record<WorkAction, string>> = {
  respawn: "Ends the current generation and starts the next, which counts toward the day's generations.",
  reset: "Closes the breaker and starts the lane again. A member's reset may pass the daily generation cap.",
  resume: "Lets the parked lane go on now, without waiting for what it waits on.",
};

const laneActs = ["reset", "respawn", "resume"] as const;
const isLaneAct = (action: string): action is (typeof laneActs)[number] => (laneActs as readonly string[]).includes(action);

// LaneActions are what a member can do about a lane: the acts the server offers for the lane itself, and the
// recovery actions the work order allows, such as canceling the work whatever state it is in, and removing the
// worktree of a lane that ended.
function LaneActions({ row, after }: { row: Row; after: () => Promise<void> }) {
  const [act, setAct] = useState<WorkAction | null>(null);
  const [work, setWork] = useState<RecoverAction | null>(null);
  const [reason, setReason] = useState("");
  const { act: run, busy, failure } = useAct(after);
  const acts = (row.stuck?.actions ?? []).filter(isLaneAct);
  const order = row.work?.order;
  const recover = recoveries(row.work?.allowed).filter((action) => action !== "archive_lane" || row.lane.archivable);
  const workOrder = row.stuck?.work_order?.id ?? row.lane.work_order;
  const needsReason = act ? workActions[act].needsReason : false;
  const send = async () => {
    if (act && (await run(() => actOnWork(workOrder, act, needsReason ? reason.trim() : "")))) {
      setAct(null);
      setReason("");
    }
  };
  const items = [
    ...acts.map((action) => ({ label: `${actionWords[action]}…`, onSelect: () => setAct(action) })),
    ...recover.map((action) => ({ label: `${action === "archive_lane" ? specs[action].label : labelOf(action, row.work?.allowed)}…`, icon: specs[action].icon, onSelect: () => setWork(action) })),
  ];
  return (
    <span className="row-actions">
      {row.own ? <TalkToLane lane={row.lane.id} /> : null}
      <Link to={addressOf("/lane", { id: row.lane.id })} className="btn btn-sm" aria-label={`Open lane ${row.lane.id}`}>
        Open
      </Link>
      {items.length ? <Menu label={`Actions for lane ${row.lane.id}`} items={items} /> : null}
      {order && work ? <WorkActionDialog order={order} action={work} allowance={row.work?.allowed} lane={row.lane} onClose={() => setWork(null)} after={after} /> : null}
      <Dialog
        open={act !== null}
        title={act ? `${actionWords[act]}: lane ${row.lane.id}` : ""}
        onClose={() => setAct(null)}
        footer={
          <>
            <span className="spacer" />
            <Button onClick={() => setAct(null)}>Cancel</Button>
            <Button variant="primary" loading={busy} disabled={busy || (needsReason && reason.trim() === "")} onClick={() => void send()}>
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

// LaneTable lists lanes. A lane's CPU and memory are its session's, as the report of the worker that runs it last
// measured them, so a table of lanes on no machine has no report to read.
function LaneTable({ rows, label, report, after }: { rows: Row[]; label: string; report?: ResourceReport; after: () => Promise<void> }) {
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
              <td data-label="CPU · Mem" className="r">
                <SessionUse report={report} session={sessionOf(report, "lane", row.lane.id)} />
              </td>
              <td data-label="Freshness">
                <Freshness kind={freshnessOf(row.lane.freshness)} at={row.lane.as_of} source="the lane's worker" />
              </td>
              <td className="actions-cell">{row.own ? <LaneActions row={row} after={after} /> : <span className="visually-hidden">No action: another project's lane</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

// Machine is one worker's panel: its name, its orchestrator, its lanes against its cap, what its worker last
// measured of the machine, how fresh its word is, and its lanes.
function Machine({ line, rows, after }: { line: WorkerLine; rows: Row[]; after: () => Promise<void> }) {
  const heading = useId();
  const talk = useTalk();
  const { worker, orchestrator } = line;
  const name = worker.name || worker.id;
  const cap = worker.manifest?.capacity?.max_lanes;
  const running = rows.filter((row) => counting.has(row.lane.actual_state)).length;
  const machine = worker.report?.machine;
  const measures = machine ? machineMeasures(machine) : undefined;
  return (
    <section className="panel machine" aria-labelledby={heading}>
      <div className="worker-head">
        <h2 className="worker-name" id={heading}>
          <Icon name="server" />
          {name}
        </h2>
        {orchestrator ? (
          <button
            type="button"
            className="target-button"
            title={`Talk to the orchestrator on ${name}`}
            onClick={() => talk.select("orchestrator", worker.id, { name, open: true, focus: true })}
          >
            <TargetChip kind="orchestrator" small>
              Orchestrator · {orchestrator.actual_state ? laneLabel(orchestrator.actual_state).toLowerCase() : `wanted ${words(orchestrator.desired_state).toLowerCase()}`}
              {orchestrator.current_generation ? ` · gen ${orchestrator.current_generation}` : ""}
            </TargetChip>
            <span className="visually-hidden">, talk to the orchestrator on {name}</span>
          </button>
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
        {(
          [
            ["CPU", measures?.cpu],
            ["Memory", measures?.memory],
            ["Memory pressure", measures?.pressure],
            ["Disk free", measures?.disk],
            ["Load per core", measures?.load],
          ] as const
        ).map(([measure, value]) => (
          <span className="res" key={measure}>
            <span>{measure}</span>
            {!measures ? <Absent>not reported</Absent> : value === undefined ? <Absent>unavailable</Absent> : <strong>{value}</strong>}
          </span>
        ))}
        <span className="spacer" />
        <Freshness kind={freshnessOf(line.freshness)} source="the worker's heartbeat">
          {worker.last_handshake_at ? (
            <>
              , handshake <Ago at={worker.last_handshake_at} />
            </>
          ) : null}
        </Freshness>
      </div>
      {rows.length ? <LaneTable rows={rows} label={`Lanes on ${name}`} report={worker.report} after={after} /> : <p className="panel-body muted">No lane runs on {name}.</p>}
    </section>
  );
}

// ReadyToStart lists the work that waits for a lane, which is ready work and claimed work with no lane yet,
// each with the action that moves it forward, the same as its work order's page offers. Work the server
// lists as stuck stands in Needs you with the actions that fit it. The panel stands aside when nothing waits.
function ReadyToStart({ index, after }: { index: WorkIndex | null; after: () => Promise<void> }) {
  const rows = (index?.rows ?? []).filter((row) => !row.stuck && ["claim", "lane"].includes(startOf(row.order, row.allowed)?.kind ?? ""));
  if (rows.length === 0) {
    return null;
  }
  return (
    <Panel title="Ready to start">
      <ul className="ready-list">
        {rows.map((row) => (
          <li key={row.order.id} data-work={row.order.id}>
            <span className="ready-title">
              <Link to={addressOf("/work-order", { id: row.order.id })} data-item>
                {row.order.title || row.order.id}
              </Link>
              <span className="cell-note">
                <Id value={row.order.id} />
              </span>
              <ReviewOf order={row.order} />
            </span>
            <WorkBadge state={row.order.state} />
            <StartWork order={row.order} allowed={row.allowed} small after={after} />
          </li>
        ))}
      </ul>
    </Panel>
  );
}

type FloorRead = { project: string; view: ShopFloor };

// FloorBody is one panel per machine that serves the project, and a panel for lanes no machine holds.
function FloorBody({
  reads,
  stuck,
  project,
  index,
  group,
  after,
}: {
  reads: FloorRead[];
  stuck: Stuck | null;
  project: string;
  index: WorkIndex | null;
  group: boolean;
  after: () => Promise<void>;
}) {
  const own = reads.find((read) => read.project === project)?.view;
  const workers = new Map<string, WorkerLine>();
  for (const read of reads) {
    for (const line of read.view.workers) {
      if (!workers.has(line.worker.id) || read.project === project) {
        workers.set(line.worker.id, line);
      }
    }
  }
  const works = new Map((index?.rows ?? []).map((found) => [found.order.id, found]));
  const rows: Row[] = reads.flatMap((read) =>
    read.view.lanes.map((lane) => ({
      lane,
      project: read.project,
      own: read.project === project,
      stuck: read.project === project ? stuck?.items.find((item) => item.lane?.id === lane.id) : undefined,
      work: read.project === project ? works.get(lane.work_order) : undefined,
    })),
  );
  if (!own || (own.workers.length === 0 && own.lanes.length === 0)) {
    return (
      <div className="stack">
        {own ? <ReadyToStart index={index} after={after} /> : null}
        <div className="panel">
          <EmptyState icon="server" title="No worker has joined">
            {group
              ? "Lanes appear here, one panel per machine, once a worker joins. A worker starts from a terminal on its machine, with its join token."
              : "Lanes appear here, one panel per machine, once a worker joins."}
          </EmptyState>
        </div>
      </div>
    );
  }
  const loose = rows.filter((row) => !row.lane.worker_id || !workers.has(row.lane.worker_id));
  return (
    <div className="stack">
      <ReadyToStart index={index} after={after} />
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
  // What each work order allows, which the lanes' actions and the list of work ready to start read.
  const { index } = useWorkIndex(live);
  const talkable = useTalkable(live, project);
  // A join token is for a worker on another machine, which only an installation in group mode takes in.
  const group = useHealth(live, true).view?.mode === "group";
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
            <Talkable.Provider value={talkable}>
              <FloorBody reads={reads} stuck={stuck.view} project={project} index={index} group={group} after={after} />
            </Talkable.Provider>
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
