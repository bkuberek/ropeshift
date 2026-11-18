import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { JoinTokenMade } from "../api/admin";
import { createJoinToken, readOrchestrator, revokeJoinToken, stopOrchestrator } from "../api/admin";
import { inProjectOf, readView } from "../api/client";
import type { Operations, Orchestrator, Project, ResourceReport, SessionResources, ShopFloor, WorkerLine } from "../api/types";
import type { Live } from "../api/useView";
import { useHealth } from "../api/useHealth";
import { useView } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { useAct } from "../needs/useAct";
import { Ago, Page, StampLine, useClock, ViewTime, words } from "../parts/parts";
import { Button, LinkTabs } from "../ui/controls";
import { Field } from "../ui/forms";
import { Absent, Banner, EmptyState, Fact, Facts, Freshness, freshnessOf, Id, LaneBadge, Panel, ProjectTag, Refusal, SkeletonRows, StateBadge, TableRegion, TargetChip } from "../ui/display";
import { Dialog, useToast } from "../ui/overlays";
import { Icon } from "../ui/Icon";
import { DriversPanel } from "./Drivers";
import { LaneCosts } from "./LaneCosts";
import { bytes, sessionOf } from "./resources";
import { OrchestratorStart } from "./OrchestratorStart";
import { OpenRunWindow } from "./RunWindow";
import { conditionWords, orchestratorLabel, workerStates } from "./states";

type FloorRead = { project: string; view: ShopFloor };

// A worker reports its orchestrator and its resources to the installation's log, which no project's stream
// carries, so a read of the floor also runs this often.
const installationMs = 5000;

// useFloorLive is what a read of the floor follows: the project's stream, and the installation's reports at
// installationMs. The Floor, Workers and resources, and Projects all follow it, so they show an orchestrator
// alike. A hidden tab shows nobody the floor, so it reads nothing on its own and reads once when it is shown
// again, and a read still out when the next is due is not asked again. `track` wraps a read so it counts as out.
export function useFloorLive(live: Live): { live: Live; track: <T>(read: () => Promise<T>) => () => Promise<T> } {
  const [ticks, setTicks] = useState(0);
  const out = useRef(0);
  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | undefined;
    const due = () => {
      if (out.current === 0) {
        setTicks((was) => was + 1);
      }
    };
    const follow = () => {
      clearInterval(timer);
      timer = document.visibilityState === "hidden" ? undefined : setInterval(due, installationMs);
    };
    const shown = () => {
      follow();
      if (document.visibilityState !== "hidden") {
        due();
      }
    };
    follow();
    document.addEventListener("visibilitychange", shown);
    return () => {
      clearInterval(timer);
      document.removeEventListener("visibilitychange", shown);
    };
  }, []);
  const followed = useMemo(() => ({ heard: live.heard + ticks, checkSession: live.checkSession }), [live.heard, live.checkSession, ticks]);
  const track = useCallback(
    <T,>(read: () => Promise<T>) =>
      async () => {
        out.current += 1;
        try {
          return await read();
        } finally {
          out.current -= 1;
        }
      },
    [],
  );
  return { live: followed, track };
}

// useEveryFloor reads the floor of every active project, because a machine serves them all and its
// sessions belong to each.
export function useEveryFloor(live: Live, projects: Project[]) {
  const names = projects.filter((row) => row.state === "active").map((row) => row.name);
  const floor = useFloorLive(live);
  return useView<FloorRead[]>(
    floor.track(() => Promise.all(names.map((name) => inProjectOf(name, () => readView<ShopFloor>("shop-floor")).then((view) => ({ project: name, view }))))),
    `floors:${names.join(",")}`,
    floor.live,
  );
}

// measured shows a reported value, or says the machine could not provide it, and never draws it as zero.
function measured(value: number | undefined, show: (value: number) => string) {
  return value === undefined ? <Absent>unavailable</Absent> : show(value);
}

// NotReported is a worker that has sent no resource report yet.
function NotReported() {
  return <Absent>not reported</Absent>;
}

// SessionUse is a session's process tree as its worker last measured it.
export function SessionUse({ report, session }: { report?: ResourceReport; session?: SessionResources }) {
  if (!report) {
    return <NotReported />;
  }
  if (!session) {
    return <Absent>unavailable</Absent>;
  }
  return (
    <>
      {measured(session.cpu_cores, (cores) => `${cores.toFixed(2)} cores`)}
      {" · "}
      {measured(session.rss_bytes, bytes)}
    </>
  );
}

export const running = new Set(["provisioning", "starting", "briefing", "working", "needs_decision", "compacting", "handing_off", "idle", "backoff", "parking"]);

function StopOrchestrator({ line, after }: { line: WorkerLine; after: () => Promise<void> }) {
  const { act, busy, failure } = useAct(after);
  const toast = useToast();
  const name = line.worker.name || line.worker.id;
  return (
    <>
      <Button
        small
        loading={busy}
        disabled={busy}
        onClick={() =>
          void act(() => stopOrchestrator(line.worker.id)).then((done) => {
            if (done) {
              toast("success", `Asked ${name} to stop its orchestrator. Its lanes keep running.`);
            }
          })
        }
      >
        Stop orchestrator
      </Button>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </>
  );
}

// OrchestratorDetails reads one worker's orchestrator as the ledger holds it, as `ropeshift orchestrator show`
// does: what is wanted of it, what its worker last said, how many times a member started it and reset its
// breaker, and what it carried beside its state.
function OrchestratorDetails({ line }: { line: WorkerLine }) {
  const [open, setOpen] = useState(false);
  const [read, setRead] = useState<{ row: Orchestrator } | { failure: string } | null>(null);
  const name = line.worker.name || line.worker.id;
  useEffect(() => {
    if (!open) {
      return;
    }
    let current = true;
    setRead(null);
    readOrchestrator(line.worker.id)
      .then((row) => current && setRead({ row }))
      .catch((error: unknown) => current && setRead({ failure: error instanceof Error ? error.message : String(error) }));
    return () => {
      current = false;
    };
  }, [open, line.worker.id]);
  return (
    <>
      <Button small icon="info" aria-label={`Details of the orchestrator on ${name}`} onClick={() => setOpen(true)}>
        Details
      </Button>
      <Dialog open={open} title={`Orchestrator on ${name}`} onClose={() => setOpen(false)} footer={<><span className="spacer" /><Button onClick={() => setOpen(false)}>Close</Button></>}>
        {read === null ? (
          <SkeletonRows rows={3} />
        ) : "failure" in read ? (
          <Refusal>{read.failure}</Refusal>
        ) : (
          <Facts>
            <Fact name="Wanted">{words(read.row.desired_state)}</Fact>
            <Fact name="State">{orchestratorLabel(read.row) ?? <Absent>not reported</Absent>}</Fact>
            <Fact name="Generation">{read.row.current_generation ?? <Absent>none yet</Absent>}</Fact>
            <Fact name="Starts">{read.row.run}</Fact>
            <Fact name="Breaker resets">{read.row.resets}</Fact>
            <Fact name="Conditions">{read.row.conditions?.length ? read.row.conditions.map((condition) => conditionWords[condition] ?? words(condition)).join(", ") : <Absent>none</Absent>}</Fact>
            <Fact name="Reported">{read.row.reported_at ? <Ago at={read.row.reported_at} /> : <Absent>never</Absent>}</Fact>
          </Facts>
        )}
      </Dialog>
    </>
  );
}

// JoinToken makes a join token and shows it once: the ledger keeps only its digest. A group server answers a join
// string with it, which is what a worker is given. The token lives a day, and the dialog says when it ends and which
// token it is, which Revoke join token asks for.
function JoinToken({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [made, setMade] = useState<JoinTokenMade | null>(null);
  const joinString = made?.join_string ?? "";
  const shown = joinString || made?.token || "";
  const { act, busy, failure } = useAct(async () => {});
  const toast = useToast();
  const close = () => {
    setMade(null);
    onClose();
  };
  return (
    <Dialog
      open={open}
      title="Create join token"
      onClose={close}
      footer={
        <>
          <span className="spacer" />
          {shown ? (
            <Button variant="primary" onClick={close}>
              Done
            </Button>
          ) : (
            <>
              <Button onClick={close}>Cancel</Button>
              <Button variant="primary" loading={busy} onClick={() => void act(() => createJoinToken().then(setMade))}>
                Create token
              </Button>
            </>
          )}
        </>
      }
    >
      {shown ? (
        <div className="field-stack">
          {joinString ? (
            <p className="small">
              This is the only time the join string is shown. It is a credential: whoever holds it can join a worker to this server, and the token in it is recorded as made by you. On the worker&apos;s machine, run{" "}
              <code className="mono">ropeshiftd worker join</code> with it as the argument, or set it in <code className="mono">ROPESHIFT_JOIN</code> to keep it out of the process list. It joins one worker. It is not harmless once a worker has used it: until that worker opens
              its first session, whoever holds the string and the worker&apos;s ID can take the worker&apos;s credential, so give it to the one machine only.
            </p>
          ) : (
            <p className="small">This is the only time the token is shown. A worker on another machine registers with it once, from a terminal on that machine.</p>
          )}
          {made?.expires_at ? (
            <p className="small">
              It expires {new Date(made.expires_at).toLocaleString()}. Until a worker uses it, Revoke join token ends it{made.id ? <>, with the ID <code className="mono">{made.id}</code></> : null}.
            </p>
          ) : null}
          <div className="row-tight token-row">
            <code className="mono token">{shown}</code>
            <Button
              small
              icon="copy"
              onClick={() =>
                void navigator.clipboard?.writeText(shown).then(
                  () => toast("success", joinString ? "Copied the join string." : "Copied the join token."),
                  () => toast("warning", joinString ? "The browser did not let this page copy. Select the join string and copy it." : "The browser did not let this page copy. Select the token and copy it."),
                )
              }
            >
              Copy
            </Button>
          </div>
        </div>
      ) : (
        <p className="small">A join token lets one new worker register with this installation. It is shown once, and the ledger keeps only its digest.</p>
      )}
      {failure ? <Refusal>{failure}</Refusal> : null}
    </Dialog>
  );
}

// RevokeJoinToken ends a join token no worker used, by the ID Create join token showed when it made it. A token that
// is revoked already is revoked, and one a worker used is refused, because that worker holds a credential from it.
function RevokeJoinToken({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [id, setId] = useState("");
  const [revoked, setRevoked] = useState("");
  const { act, busy, failure } = useAct(async () => {});
  const close = () => {
    setId("");
    setRevoked("");
    onClose();
  };
  const ready = id.trim() !== "";
  return (
    <Dialog
      open={open}
      title="Revoke join token"
      onClose={close}
      footer={
        <>
          <span className="spacer" />
          {revoked ? (
            <Button variant="primary" onClick={close}>
              Done
            </Button>
          ) : (
            <>
              <Button onClick={close}>Cancel</Button>
              <Button variant="primary" loading={busy} disabled={!ready} onClick={() => void act(() => revokeJoinToken(id.trim()).then((done) => setRevoked(done.id)))}>
                Revoke token
              </Button>
            </>
          )}
        </>
      }
    >
      {revoked ? (
        <p className="small">
          Revoked the join token <code className="mono">{revoked}</code>. The server refuses it from now on.
        </p>
      ) : (
        <div className="field-stack">
          <p className="small">A join token that no worker used can be revoked. The server then refuses it, and the join string that holds it. A token a worker used cannot be, because that worker holds a credential from it.</p>
          <Field label="Token ID" help="The ID Create join token showed with the token, or that ropeshift worker token printed.">
            <input className="input" type="text" autoComplete="off" value={id} onChange={(event) => setId(event.target.value)} />
          </Field>
        </div>
      )}
      {failure ? <Refusal>{failure}</Refusal> : null}
    </Dialog>
  );
}

// WorkersPage lists each machine with what its worker last measured, and every session on it. A worker
// that has not reported says so, a measure its machine cannot provide says it is unavailable, and neither is
// ever drawn as zero. A value past a threshold warns, and stops no work.
export function WorkersPage({ live, projects, selected, after, dialog = "" }: { live: Live; projects: Project[]; selected: string; after: () => Promise<void>; dialog?: string }) {
  // The palette opens Create join token with `?dialog=token`. The dialog opens after the page has taken its
  // arrival focus, so that focus does not leave it outside.
  const [joining, setJoining] = useState(false);
  const [revoking, setRevoking] = useState(false);
  useEffect(() => {
    if (dialog === "token") {
      setJoining(true);
    }
    if (dialog === "revoke-token") {
      setRevoking(true);
    }
  }, [dialog]);
  // A join token is for a worker on another machine, which only an installation in group mode takes in.
  const group = useHealth(live, true).view?.mode === "group";
  const floors = useEveryFloor(live, projects);
  const now = useClock();
  const reads = floors.view ?? [];
  const workers = new Map<string, WorkerLine>();
  for (const read of reads) {
    for (const line of read.view.workers) {
      workers.set(line.worker.id, line);
    }
  }
  const lanes = reads.flatMap((read) => read.view.lanes.map((lane) => ({ lane, project: read.project })));
  const stamp = reads[0]?.view;
  return (
    <Page
      title="Workers and resources"
      note="Machines that serve this installation, what each reports, and the sessions on each."
      actions={
        group ? (
          <>
            <Button icon="key" onClick={() => setJoining(true)}>
              Create join token
            </Button>
            <Button onClick={() => setRevoking(true)}>Revoke join token</Button>
          </>
        ) : undefined
      }
    >
      <JoinToken open={joining && group} onClose={() => setJoining(false)} />
      <RevokeJoinToken open={revoking && group} onClose={() => setRevoking(false)} />
      <LinkTabs
        label="Usage and resources"
        tabs={[
          { to: "/usage", label: "Usage", current: false },
          { to: "/workers", label: "Resources", current: true },
        ]}
      />
      <div className="tab-body stack">
        {floors.failure ? <Banner tone="danger">{floors.view ? `This page is not current. ${floors.failure}` : floors.failure}</Banner> : null}
        {(!floors.view || projects.length === 0) && !floors.failure ? (
          <div className="panel">
            <SkeletonRows rows={4} />
          </div>
        ) : null}
        {floors.view && projects.length > 0 && workers.size === 0 ? (
          <div className="panel">
            <EmptyState
              icon="server"
              title="No worker has connected"
              action={
                group ? (
                  <Button variant="primary" icon="key" onClick={() => setJoining(true)}>
                    Create join token
                  </Button>
                ) : undefined
              }
            >
              {group ? "A worker joins with a join string, from a terminal on its own machine." : "The worker that runs with this installation has not reported yet."}
            </EmptyState>
          </div>
        ) : null}
        {floors.view && workers.size > 0 ? (
          <ViewTime.Provider value={{ asOf: stamp?.as_of ?? 0, elapsed: Math.max(0, now - floors.readAt) }}>
            {[...workers.values()].flatMap((line) =>
              (line.worker.report?.warnings ?? []).map((warning) => (
                <Banner key={`${line.worker.id}:${warning.measure}:${warning.volume ?? ""}`} tone="warning">
                  {line.worker.name || line.worker.id}: {warning.message} A warning stops no work.
                </Banner>
              )),
            )}
            <Panel title="Machines" flush>
              <TableRegion label="Machines">
                <table className="table stack-on-phone">
                  <thead>
                    <tr>
                      <th scope="col">Machine</th>
                      <th scope="col">State</th>
                      <th scope="col" className="r">
                        Lanes / cap
                      </th>
                      <th scope="col">CPU</th>
                      <th scope="col">Memory</th>
                      <th scope="col">Free disk</th>
                      <th scope="col">Orchestrator</th>
                      <th scope="col">Freshness</th>
                    </tr>
                  </thead>
                  <tbody>
                    {[...workers.values()].map((line) => {
                      const count = lanes.filter((row) => row.lane.worker_id === line.worker.id && running.has(row.lane.actual_state)).length;
                      const report = line.worker.report;
                      const machine = report?.machine;
                      const cap = report ? report.caps.max_lanes : line.worker.manifest?.capacity?.max_lanes;
                      const orchestrator = line.orchestrator;
                      const stopped = !orchestrator || !orchestrator.actual_state || !running.has(orchestrator.actual_state);
                      return (
                        <tr key={line.worker.id} aria-selected={selected === line.worker.id || undefined}>
                          <td data-label="Machine">
                            <strong>{line.worker.name || line.worker.id}</strong>
                            <span className="cell-note">
                              <Id value={line.worker.id} />
                            </span>
                            <span className="cell-note">
                              <Link className="btn btn-sm" to={addressOf("/settings", { section: "autonomy", worker: line.worker.id })} installation>
                                <Icon name="sliders" size="sm" />
                                Autonomy
                              </Link>
                            </span>
                          </td>
                          <td data-label="State">
                            <StateBadge table={workerStates} state={line.freshness} />
                          </td>
                          <td data-label="Lanes / cap" className="r">
                            {count}
                            {cap ? ` / ${cap}` : ""}
                            {report?.recommendation ? <span className="cell-note">recommended {report.recommendation.max_lanes}</span> : null}
                          </td>
                          <td data-label="CPU">
                            {machine ? (
                              <>
                                {measured(machine.cpu_percent, (share) => `${Math.round(share)}%`)}
                                <span className="cell-note">load {measured(machine.load_5, (load) => load.toFixed(2))}</span>
                              </>
                            ) : (
                              <NotReported />
                            )}
                          </td>
                          <td data-label="Memory" className="wrap">
                            {machine ? (
                              <>
                                {machine.memory_used_bytes !== undefined && machine.memory_total_bytes !== undefined ? (
                                  `${bytes(machine.memory_used_bytes)} of ${bytes(machine.memory_total_bytes)}`
                                ) : (
                                  <Absent>unavailable</Absent>
                                )}
                                <span className="cell-note">
                                  {measured(machine.memory_available_bytes, (available) => `${bytes(available)} available`)}
                                  {machine.memory_pressure_some !== undefined ? `, pressure ${machine.memory_pressure_some.toFixed(1)}%` : null}
                                </span>
                              </>
                            ) : (
                              <NotReported />
                            )}
                          </td>
                          <td data-label="Free disk">
                            {!machine ? (
                              <NotReported />
                            ) : machine.volumes.length === 0 ? (
                              <Absent>unavailable</Absent>
                            ) : (
                              machine.volumes.map((volume) => (
                                <div key={volume.roles.join(",")}>
                                  {bytes(volume.free_bytes)} <span className="cell-note">{volume.roles.join(" and ")}</span>
                                </div>
                              ))
                            )}
                          </td>
                          <td data-label="Orchestrator">
                            <span className="row-tight">
                              {orchestrator ? (
                                <TargetChip kind="orchestrator" small>
                                  {orchestratorLabel(orchestrator) ?? `Wanted ${words(orchestrator.desired_state).toLowerCase()}`}
                                </TargetChip>
                              ) : (
                                <Absent>none</Absent>
                              )}
                              {orchestrator?.waiting_for === "run_window" ? <OpenRunWindow worker={line.worker.id} name={line.worker.name || line.worker.id} /> : null}
                              {stopped ? <OrchestratorStart line={line} after={after} /> : <StopOrchestrator line={line} after={after} />}
                              {orchestrator ? <OrchestratorDetails line={line} /> : null}
                            </span>
                          </td>
                          <td data-label="Freshness" className="wrap">
                            <Freshness kind={freshnessOf(line.freshness)} source="the worker's heartbeat">
                              {line.worker.last_handshake_at ? (
                                <>
                                  , handshake <Ago at={line.worker.last_handshake_at} />
                                </>
                              ) : null}
                            </Freshness>
                            {report && line.worker.reported_at ? (
                              <span className="cell-note">
                                Resources measured <Ago at={line.worker.reported_at} />
                                {line.report_freshness === "observed" ? "" : ", last reported"}
                              </span>
                            ) : null}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </TableRegion>
            </Panel>
            <DriversPanel workers={[...workers.values()]} />
            <Panel
              title="Sessions"
              flush
              foot="Only lanes count toward a machine's caps. CPU is the cores a session's process tree kept busy since its worker last measured, and memory is the tree's resident memory. A measure the machine cannot provide is unavailable, never zero."
            >
              <TableRegion label="Sessions">
                <table className="table stack-on-phone">
                  <thead>
                    <tr>
                      <th scope="col">Session</th>
                      <th scope="col">State</th>
                      <th scope="col">Machine</th>
                      <th scope="col">Project</th>
                      <th scope="col">Counts toward caps</th>
                      <th scope="col">CPU · Mem</th>
                    </tr>
                  </thead>
                  <tbody>
                    {[...workers.values()]
                      .filter((line) => line.orchestrator)
                      .map((line) => (
                        <tr key={`orchestrator:${line.worker.id}`}>
                          <td data-label="Session">
                            <TargetChip kind="orchestrator" small />
                          </td>
                          <td data-label="State">
                            {(line.orchestrator && orchestratorLabel(line.orchestrator)) ?? <Absent>not reported</Absent>}
                            {line.orchestrator?.waiting_for === "run_window" ? (
                              <span className="cell-note">
                                <OpenRunWindow worker={line.worker.id} name={line.worker.name || line.worker.id} />
                              </span>
                            ) : null}
                          </td>
                          <td data-label="Machine">{line.worker.name || line.worker.id}</td>
                          <td data-label="Project">Every project it serves</td>
                          <td data-label="Counts toward caps">No</td>
                          <td data-label="CPU · Mem">
                            <SessionUse report={line.worker.report} session={sessionOf(line.worker.report, "orchestrator")} />
                          </td>
                        </tr>
                      ))}
                    {lanes.map(({ lane, project }) => (
                      <tr key={`${project}:${lane.id}`}>
                        <td data-label="Session">
                          <span className="row-tight">
                            <TargetChip kind="lane" small />
                            <Link to={`/projects/${encodeURIComponent(project)}${addressOf("/lane", { id: lane.id })}`}>
                              <Id value={lane.id} />
                            </Link>
                          </span>
                        </td>
                        <td data-label="State">
                          <LaneBadge state={lane.actual_state} />
                        </td>
                        <td data-label="Machine">{lane.worker_id ? (workers.get(lane.worker_id)?.worker.name ?? lane.worker_id) : <Absent>none yet</Absent>}</td>
                        <td data-label="Project">
                          <ProjectTag name={project} display={projects.find((row) => row.name === project)?.display_name} />
                        </td>
                        <td data-label="Counts toward caps">Yes</td>
                        <td data-label="CPU · Mem">
                          {lane.worker_id ? (
                            <SessionUse report={workers.get(lane.worker_id)?.worker.report} session={sessionOf(workers.get(lane.worker_id)?.worker.report, "lane", lane.id)} />
                          ) : (
                            <Absent>none yet</Absent>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableRegion>
            </Panel>
            {stamp ? <StampLine stamp={stamp} /> : null}
          </ViewTime.Provider>
        ) : null}
      </div>
    </Page>
  );
}

function dollars(value: number): string {
  return value > 0 && value < 0.005 ? "under $0.01" : `$${value.toFixed(2)}`;
}

// UsagePage shows each sign-in's windows and the estimated cost. No source reports a sign-in's windows to
// this page yet, so that part says so and names the sources that will; the estimated cost per outcome
// comes from the project's measures.
export function UsagePage({ live, project, label }: { live: Live; project: string; label: string }) {
  // An installation view's project comes from the list of projects, so it reads once that is known.
  const operations = useView<Operations | null>(() => (project ? readView<Operations>("operations") : Promise.resolve(null)), `usage:${project}`, live);
  const now = useClock();
  const report = operations.view?.measures;
  const costs = Object.entries(report?.cost_per_outcome ?? {}).sort(([a], [b]) => a.localeCompare(b));
  return (
    <Page title="Usage" note="Sign-in windows, their resets, and estimated cost.">
      <LinkTabs
        label="Usage and resources"
        tabs={[
          { to: "/usage", label: "Usage", current: true },
          { to: "/workers", label: "Resources", current: false },
        ]}
      />
      <div className="tab-body stack">
        <Panel title="Sign-ins">
          <EmptyState icon="gauge" title="No sign-in has reported a window yet">
            A sign-in's session and weekly windows show here once a source reports them: a status line, a lane's stream events, or a /usage read. This build
            has no such source yet, so no window is drawn, and none is ever drawn as zero.
          </EmptyState>
        </Panel>
        <Panel title="History">
          <p className="muted">No history yet: no source has reported a window or a day's estimated cost.</p>
        </Panel>
        <LaneCosts live={live} />
        {operations.failure ? <Banner tone="danger">{operations.failure}</Banner> : null}
        {report ? (
          <ViewTime.Provider value={{ asOf: operations.view?.as_of ?? 0, elapsed: Math.max(0, now - operations.readAt) }}>
            <Panel title={`Estimated cost by outcome in ${label}`} flush foot="Costs are estimated at list price from what agents reported, and are not a bill. A cost reads at least a figure only when some of it was never reported, because a turn stopped, or a session ended before a turn's result.">
              {costs.length ? (
                <TableRegion label="Estimated cost by outcome">
                  <table className="table stack-on-phone">
                    <caption className="visually-hidden">Estimated cost by outcome over the last {report.window.name}</caption>
                    <thead>
                      <tr>
                        <th scope="col">Outcome</th>
                        <th scope="col" className="r">
                          Done
                        </th>
                        <th scope="col" className="r">
                          Est. cost
                        </th>
                        <th scope="col" className="r">
                          Est. per done
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {costs.map(([kind, cost]) => (
                        <tr key={kind}>
                          <td data-label="Outcome">{words(kind)}</td>
                          <td data-label="Done" className="r">
                            {cost.done}
                          </td>
                          <td data-label="Est. cost" className="r">
                            {cost.lower_bound ? "at least " : ""}
                            {dollars(cost.total_usd)}
                          </td>
                          <td data-label="Est. per done" className="r">
                            {cost.per_done_usd === null ? <Absent>no work done</Absent> : dollars(cost.per_done_usd)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </TableRegion>
              ) : (
                <p className="panel-body muted">No lane reported a cost over the last {report.window.name}.</p>
              )}
            </Panel>
            {operations.view ? <StampLine stamp={operations.view} /> : null}
          </ViewTime.Provider>
        ) : null}
      </div>
    </Page>
  );
}
