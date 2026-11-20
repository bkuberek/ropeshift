import { lazy, Suspense, useState } from "react";
import type { ReactNode } from "react";

import { readAudit, readRepositories } from "../api/admin";
import { listPractices } from "../api/practices";
import type { PracticesList } from "../api/practices";
import type { AuditPage, RepositoryList } from "../api/admin";
import { readView } from "../api/client";
import type { NeedsYou, Operations, Plans, ReadyWait, Roadmap, ShopFloor } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { useRead } from "../admin/parts";
import { addressOf, Link } from "../app/route";
import { ReadyWaitActions, ReadyWaitWords } from "../floor/ReadyWait";
import { milestoneStates } from "../floor/states";
import { fullHeadingOf, headingOf, keyOf, kindOf } from "../needs/kinds";
import { Ago, Bar, Lasted, Page, StampLine, useClock, ViewTime } from "../parts/parts";
import { StartSession } from "../planning/StartSession";
import { PracticesPanel } from "../practices/PracticesPanel";
import { useCheck } from "../practices/useCheck";
import { agreementOf } from "../reviews/inReview";
import { Button } from "../ui/controls";
import { Absent, Actor, Banner, ConditionChip, EmptyState, Fact, Facts, Id, LaneBadge, Panel, SkeletonRows, StateBadge, TableRegion } from "../ui/display";
import { currentPlan, lanesOf, tilesOf, topNeeds } from "./figures";

// The register dialog belongs to Repositories, which loads when a person first opens it.
const RegisterDialog = lazy(() => import("../admin/Repositories").then((page) => ({ default: page.RegisterDialog })));

// The most lanes and events the page lists before it sends a person to the view that lists all.
const listed = 8;

// Unread says why a part of the page shows nothing: its read failed, in the API's words, and the rest of the page
// stands.
function Unread({ failure }: { failure: string }) {
  return <Banner tone="danger">{failure}</Banner>;
}

function Tiles({ tiles }: { tiles: ReturnType<typeof tilesOf> }) {
  return (
    <div className="kpis" role="group" aria-label="Key figures">
      {tiles.map((tile) => (
        <Link key={tile.id} to={tile.to} className="kpi">
          <span className="kpi-label">{tile.label}</span>
          <span className="kpi-value">{tile.value ?? <Absent>{tile.none}</Absent>}</span>
          {tile.note ? <span className="kpi-note">{tile.note}</span> : null}
        </Link>
      ))}
    </div>
  );
}

function Milestones({ roadmap, plans }: { roadmap: Roadmap; plans: Plans | null }) {
  const approved = new Map((plans?.plans ?? []).map((row) => [row.plan.id, row.approved_version ?? 0]));
  const row = currentPlan(roadmap, approved);
  if (!row) {
    return (
      <EmptyState icon="roadmap" title="No plan has milestones yet">
        A plan gives the project its milestones. Start a planning session to write one.
      </EmptyState>
    );
  }
  return (
    <div className="stack">
      <p className="row-tight">
        <Link to={addressOf("/plans", { plan: row.plan.id })}>{row.plan.title}</Link>
        <span className="small muted">
          {row.done} of {row.total} work orders done
        </span>
      </p>
      <ol className="milestones" aria-label="Milestones">
        {row.milestones.map((milestone) => {
          const title = milestone.title || milestone.id;
          return (
            <li key={milestone.id} className="milestone-row">
              <div>
                <p className="item-title">{title}</p>
                <p className="small muted">Priority {milestone.priority}</p>
              </div>
              <div className="stack-8">
                {milestone.total === 0 ? <span className="small muted">{milestone.existing ? "Delivered by existing code" : "No work yet"}</span> : <Bar done={milestone.done} total={milestone.total} label={`${title}: ${milestone.done} of ${milestone.total} done`} />}
              </div>
              <StateBadge table={milestoneStates} state={milestone.state} />
            </li>
          );
        })}
      </ol>
    </div>
  );
}

function Lanes({ floor }: { floor: ShopFloor }) {
  const lanes = lanesOf(floor);
  if (lanes.length === 0) {
    return <EmptyState icon="lanes" title="No lane is running." />;
  }
  return (
    <>
      <TableRegion label="Lanes in this project">
        <table className="table stack-on-phone">
          <thead>
            <tr>
              <th scope="col">Lane</th>
              <th scope="col">Work</th>
              <th scope="col">State</th>
              <th scope="col">Machine</th>
            </tr>
          </thead>
          <tbody>
            {lanes.slice(0, listed).map((lane) => (
              <tr key={lane.id}>
                <td data-label="Lane">
                  <Link to={addressOf("/lane", { id: lane.id })} className="id-link">
                    <Id value={lane.id} />
                  </Link>
                </td>
                <td data-label="Work" className="title-cell">
                  {lane.title}
                </td>
                <td data-label="State">
                  <span className="row-tight">
                    <LaneBadge state={lane.state} />
                    {lane.conditions.map((condition) => (
                      <ConditionChip key={condition} name={condition} />
                    ))}
                  </span>
                </td>
                <td data-label="Machine">{lane.machine || <Absent>none yet</Absent>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
      {lanes.length > listed ? (
        <p className="panel-body small muted">
          {lanes.length - listed} more on <Link to="/floor">the Floor</Link>.
        </p>
      ) : null}
    </>
  );
}

function Waiting({ needs, project }: { needs: NeedsYou; project: string }) {
  const top = topNeeds(needs.items, project);
  if (top.length === 0) {
    return <p className="muted">Nothing needs you in this project.</p>;
  }
  return (
    <ul className="list-rows">
      {top.map((item) => (
        <li key={keyOf(item)} className="list-row">
          <Link to={addressOf("/needs-you", { project, item: keyOf(item) })} className="list-row-title" title={fullHeadingOf(item) === headingOf(item) ? undefined : fullHeadingOf(item)}>
            {headingOf(item)}
          </Link>
          <p className="small muted row-tight">
            <span>{kindOf(item).name}</span>
            <Lasted at={item.since} />
          </p>
        </li>
      ))}
    </ul>
  );
}

function Activity({ page }: { page: AuditPage }) {
  const events = page.events.slice(0, listed);
  if (events.length === 0) {
    return <p className="muted">No event yet.</p>;
  }
  return (
    <ul className="list-rows">
      {events.map((event) => (
        <li key={event.rev} className="list-row">
          <p className="row-tight">
            <Actor kind={event.actor_kind} id={event.actor_id} actorFor={event.actor_for} label={event.actor_label} delegateKind={event.delegate_kind} />
            <span className="mono small">{event.type}</span>
          </p>
          <p className="small muted row-tight">
            <span>
              {event.subject_kind} <span className="mono">{event.subject_id}</span>
            </span>
            <Ago at={event.recorded_at} />
          </p>
        </li>
      ))}
    </ul>
  );
}

// ReadyWaits says what holds ready work from a claim, once for each wait, with how many work orders it holds and what
// a member can do about it. Work a worker's orchestrator will claim says nothing.
function ReadyWaits({ plans }: { plans: Plans }) {
  const groups = new Map<string, { wait: ReadyWait; count: number }>();
  for (const wait of Object.values(plans.ready_waits ?? {})) {
    const key = [wait.reason, wait.worker ?? "", wait.cap ?? "", wait.fleet ?? ""].join("/");
    const group = groups.get(key) ?? { wait, count: 0 };
    group.count++;
    groups.set(key, group);
  }
  return (
    <>
      {[...groups.entries()].map(([key, { wait, count }]) => (
        <Banner key={key} tone="warning" action={<ReadyWaitActions wait={wait} />}>
          {count} ready work order{count === 1 ? " waits" : "s wait"}. <ReadyWaitWords wait={wait} /> <Link to={addressOf("/work", { group: "open" })}>Work orders</Link>
        </Banner>
      ))}
    </>
  );
}

// What a read gives a panel: the answer, or the failure in the API's words, or skeleton rows while it comes.
function Read<T>({ state, children }: { state: { view: T | null; failure: string }; children: (view: T) => ReactNode }) {
  if (state.view) {
    return <>{children(state.view)}</>;
  }
  return state.failure ? <Unread failure={state.failure} /> : <SkeletonRows rows={3} />;
}

type Props = { live: Live; project: string; label: string; archived: boolean };

// OverviewPage is a project at a glance: six key figures, the plan's milestones and the lanes, what waits on the
// member, how far the member agrees with the review, and what happened lately. Every figure is read from a read
// model, and each part of the page stands on its own read.
export function OverviewPage({ live, project, label, archived }: Props) {
  const needs = useView<NeedsYou>(() => readView<NeedsYou>("needs-you"), "overview:needs", live);
  const floor = useView<ShopFloor>(() => readView<ShopFloor>("shop-floor"), "overview:floor", live);
  const operations = useView<Operations>(() => readView<Operations>("operations"), "overview:operations", live);
  const roadmap = useView<Roadmap>(() => readView<Roadmap>("roadmap"), "overview:roadmap", live);
  const plans = useView<Plans>(() => readView<Plans>("plans"), "overview:plans", live);
  const activity = useView<AuditPage>(() => readAudit("project", { limit: listed }), "overview:audit", live);
  const repositories = useRead<RepositoryList>(() => readRepositories(), "overview:repositories", live);
  const practices = useRead<PracticesList>(() => listPractices(), "overview:practices", live);
  const check = useCheck(practices.view?.repositories ?? [], practices.again, live);
  const [registering, setRegistering] = useState(false);
  const now = useClock();
  const held = repositories.view ? (repositories.view.repositories ?? []).filter((row) => row.state !== "removed") : undefined;
  const bare = held !== undefined && held.length === 0;
  const stay = archived ? `${label} is archived and read-only` : undefined;
  const register = (
    <Button variant="primary" icon="plus" disabled={archived} title={stay} onClick={() => setRegistering(true)}>
      Register repositories
    </Button>
  );
  const time = (read: { view: { as_of: number } | null; readAt: number }) => ({ asOf: read.view?.as_of ?? 0, elapsed: Math.max(0, now - read.readAt) });
  const last30 = operations.view?.measures.review_agreement.workspace.last_30_days;
  const words = last30 ? agreementOf(last30) : null;
  return (
    <Page
      title="Overview"
      note={label}
      actions={
        <>
          {bare ? null : register}
          <StartSession project={project} disabled={archived} title={stay} />
        </>
      }
    >
      {archived ? (
        <Banner tone="warning" action={<Link to="/projects">Projects</Link>}>
          {label} is archived and read-only. Restore it in Projects to change it.
        </Banner>
      ) : null}
      {bare ? (
        <div className="panel">
          <EmptyState
            icon="repo"
            title="This project has no repository yet."
            action={
              <div className="row-tight">
                {register}
                <Link className="btn" to="/import" installation>
                  Import an existing project
                </Link>
              </div>
            }
          >
            A repository is a git clone on a worker's machine. Register one so lanes can work in it, or import a project that already exists.
          </EmptyState>
        </div>
      ) : (
        <>
          {plans.view ? <ReadyWaits plans={plans.view} /> : null}
          <Tiles tiles={tilesOf({ project, needs: needs.view?.items, floor: floor.view ?? undefined, operations: operations.view ?? undefined })} />
          <div className="grid grid-main-side">
            <div className="stack">
              <Panel title="Plan" actions={<Link to="/roadmap">Roadmap</Link>}>
                <Read state={roadmap}>{(view) => <Milestones roadmap={view} plans={plans.view} />}</Read>
              </Panel>
              <Panel title="Lanes" flush actions={<Link to="/floor">Floor</Link>}>
                <Read state={floor}>{(view) => <Lanes floor={view} />}</Read>
              </Panel>
            </div>
            <aside className="stack" aria-label="Waiting, agreement, and activity">
              <Panel title="Needs you" actions={needs.view ? <Link to={addressOf("/needs-you", { project })}>All {needs.view.items.length} waiting</Link> : undefined}>
                <ViewTime.Provider value={time(needs)}>
                  <Read state={needs}>{(view) => <Waiting needs={view} project={project} />}</Read>
                </ViewTime.Provider>
              </Panel>
              <PracticesPanel list={practices.view} failure={practices.failure} readAt={practices.readAt} check={check} archived={archived} now={now} />
              <Panel title="Review agreement, 30 days" actions={<Link to={addressOf("/reviews", { tab: "agreement" })}>Reviews</Link>}>
                <Read state={operations}>
                  {() =>
                    words ? (
                      <Facts>
                        <Fact name="Decisions after a review">{words.decisions}</Fact>
                        <Fact name="You agreed with the review">{words.rate}</Fact>
                      </Facts>
                    ) : null
                  }
                </Read>
              </Panel>
              <Panel title="Recent activity" actions={<Link to="/audit">Audit</Link>}>
                <ViewTime.Provider value={time(activity)}>
                  <Read state={activity}>{(view) => <Activity page={view} />}</Read>
                </ViewTime.Provider>
              </Panel>
            </aside>
          </div>
          {needs.view ? (
            <ViewTime.Provider value={time(needs)}>
              <StampLine stamp={needs.view} />
            </ViewTime.Provider>
          ) : null}
        </>
      )}
      {registering ? (
        <Suspense fallback={null}>
          <RegisterDialog open projectLabel={label} onClose={() => setRegistering(false)} after={repositories.again} />
        </Suspense>
      ) : null}
    </Page>
  );
}
