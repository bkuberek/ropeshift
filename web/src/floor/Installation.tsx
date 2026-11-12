import { inProjectOf, readView, startOrchestrator } from "../api/client";
import type { Operations, Project, ShopFloor, WorkerLine } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { useAct } from "../needs/useAct";
import { Ago, Page, StampLine, useClock, ViewTime, words } from "../parts/parts";
import { Button, LinkTabs } from "../ui/controls";
import { Absent, Banner, EmptyState, Id, LaneBadge, Panel, ProjectTag, Refusal, SkeletonRows, StateBadge, TableRegion, TargetChip } from "../ui/display";
import { useToast } from "../ui/overlays";
import { laneLabel, workerStates } from "./states";

type FloorRead = { project: string; view: ShopFloor };

// useEveryFloor reads the floor of every active project, because a machine serves them all and its
// sessions belong to each.
function useEveryFloor(live: Live, projects: Project[]) {
  const names = projects.filter((row) => row.state === "active").map((row) => row.name);
  return useView<FloorRead[]>(
    () => Promise.all(names.map((name) => inProjectOf(name, () => readView<ShopFloor>("shop-floor")).then((view) => ({ project: name, view })))),
    `floors:${names.join(",")}`,
    live,
  );
}

const running = new Set(["provisioning", "starting", "briefing", "working", "needs_decision", "compacting", "handing_off", "idle", "backoff", "parking"]);

function StartOrchestrator({ line, after }: { line: WorkerLine; after: () => Promise<void> }) {
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
          void act(() => startOrchestrator(line.worker.id)).then((done) => {
            if (done) {
              toast("success", `Asked ${name} to run its orchestrator.`);
            }
          })
        }
      >
        Start orchestrator
      </Button>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </>
  );
}

// WorkersPage lists each machine with what it reports, and every session on it. A measure no source
// reports to this page yet says so, and is never drawn as zero.
export function WorkersPage({ live, projects, selected, after }: { live: Live; projects: Project[]; selected: string; after: () => Promise<void> }) {
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
    <Page title="Workers and resources" note="Machines that serve this installation, what each reports, and the sessions on each.">
      <LinkTabs
        label="Usage and resources"
        tabs={[
          { to: "/usage", label: "Usage", current: false },
          { to: "/workers", label: "Resources", current: true },
        ]}
      />
      <div className="tab-body stack">
        {floors.failure ? <Banner tone="danger">{floors.view ? `This page is not current. ${floors.failure}` : floors.failure}</Banner> : null}
        {!floors.view && !floors.failure ? (
          <div className="panel">
            <SkeletonRows rows={4} />
          </div>
        ) : null}
        {floors.view && workers.size === 0 ? (
          <div className="panel">
            <EmptyState icon="server" title="No worker has connected">
              A worker joins with a join token, from a terminal on its own machine. Creating a join token from this page arrives in a later build; a terminal
              creates one with ropeshift worker token.
            </EmptyState>
          </div>
        ) : null}
        {floors.view && workers.size > 0 ? (
          <ViewTime.Provider value={{ asOf: stamp?.as_of ?? 0, elapsed: Math.max(0, now - floors.readAt) }}>
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
                      const cap = line.worker.manifest?.capacity?.max_lanes;
                      const orchestrator = line.orchestrator;
                      const stopped = !orchestrator || !orchestrator.actual_state || !running.has(orchestrator.actual_state);
                      return (
                        <tr key={line.worker.id} aria-selected={selected === line.worker.id || undefined}>
                          <td data-label="Machine">
                            <strong>{line.worker.name || line.worker.id}</strong>
                            <span className="cell-note">
                              <Id value={line.worker.id} />
                            </span>
                          </td>
                          <td data-label="State">
                            <StateBadge table={workerStates} state={line.freshness} />
                          </td>
                          <td data-label="Lanes / cap" className="r">
                            {count}
                            {cap ? ` / ${cap}` : ""}
                          </td>
                          <td data-label="CPU">
                            <Absent>unavailable</Absent>
                          </td>
                          <td data-label="Memory">
                            <Absent>unavailable</Absent>
                          </td>
                          <td data-label="Free disk">
                            <Absent>unavailable</Absent>
                          </td>
                          <td data-label="Orchestrator">
                            <span className="row-tight">
                              {orchestrator ? (
                                <TargetChip kind="orchestrator" small>
                                  {orchestrator.actual_state ? laneLabel(orchestrator.actual_state) : `Wanted ${words(orchestrator.desired_state).toLowerCase()}`}
                                </TargetChip>
                              ) : (
                                <Absent>none</Absent>
                              )}
                              {stopped ? <StartOrchestrator line={line} after={after} /> : null}
                            </span>
                          </td>
                          <td data-label="Freshness">
                            <span className={line.freshness === "observed" ? "fresh" : "fresh reported"}>
                              {line.freshness === "observed" ? "Observed" : "Last reported"}
                              {line.worker.last_handshake_at ? (
                                <>
                                  , handshake <Ago at={line.worker.last_handshake_at} />
                                </>
                              ) : null}
                            </span>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </TableRegion>
            </Panel>
            <Panel title="Sessions" flush foot="Only lanes count toward a machine's caps. No source reports a session's CPU or memory to this page yet.">
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
                          <td data-label="State">{line.orchestrator?.actual_state ? laneLabel(line.orchestrator.actual_state) : <Absent>not reported</Absent>}</td>
                          <td data-label="Machine">{line.worker.name || line.worker.id}</td>
                          <td data-label="Project">Every project it serves</td>
                          <td data-label="Counts toward caps">No</td>
                          <td data-label="CPU · Mem">
                            <Absent>unavailable</Absent>
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
                          <Absent>unavailable</Absent>
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
  const operations = useView<Operations>(() => readView<Operations>("operations"), `usage:${project}`, live);
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
        {operations.failure ? <Banner tone="danger">{operations.failure}</Banner> : null}
        {report ? (
          <ViewTime.Provider value={{ asOf: operations.view?.as_of ?? 0, elapsed: Math.max(0, now - operations.readAt) }}>
            <Panel title={`Estimated cost by outcome in ${label}`} flush foot="Costs are estimated at list price from what agents reported, and are not a bill. A turn a kill stopped reports nothing, so a sum is a lower bound.">
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
