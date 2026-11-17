import { useId, useState } from "react";

import { readAcceptance } from "../api/admin";
import { readView } from "../api/client";
import type { Allowance, FloorLane, NeedItem, NeedsYou, ShopFloor, Standing, WorkOrder } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, go, ItemProject, Link } from "../app/route";
import { ApprovalDecision } from "../needs/ApprovalDecision";
import { ContractReviewCard } from "../needs/ContractReviewCard";
import { headingOf } from "../needs/kinds";
import { WorkCard } from "../needs/WorkCard";
import { Lasted, Page, StampLine, useClock, ViewTime, words } from "../parts/parts";
import { Verdicts } from "../parts/Verdicts";
import { Icon } from "../ui/Icon";
import { Button, LinkTabs, TabPanel, Tabs } from "../ui/controls";
import { Absent, Banner, ConditionChip, Digest, EmptyState, Fact, Facts, Freshness, freshnessOf, Id, LaneBadge, Panel, RecordChip, SkeletonRows, TableRegion, WorkBadge } from "../ui/display";
import type { WorkGroup } from "./states";
import { workStates } from "./states";
import { Checks } from "./Checks";
import { standingWords, waitingStandingWords } from "./evidence";
import { ActivityTab, ChangesTab } from "./EvidenceTabs";
import { NewWorkOrder } from "./NewWorkOrder";
import { PullRequestPanel } from "./PullRequest";
import { ReviewOf } from "./ReviewOf";
import { StartWork } from "./StartWork";
import { WakeWords } from "./WakeWords";
import { WorkEstimatePanel } from "./WorkEstimatePanel";
import { WorkActionDialog, WorkActions } from "./WorkActions";
import type { WorkRow } from "./workIndex";
import { groupOf, useWorkIndex } from "./workIndex";

type GroupTab = WorkGroup | "all";

const groups: { id: GroupTab; label: string; empty: string }[] = [
  { id: "open", label: "Open", empty: "Nothing is open: no work waits in intake, planning, contract review, or ready." },
  { id: "in-progress", label: "In progress", empty: "Nothing is in progress." },
  { id: "on-hold", label: "On hold", empty: "Nothing is on hold." },
  { id: "stuck", label: "Stuck", empty: "Nothing is stuck." },
  { id: "review", label: "Review and merge", empty: "Nothing waits for acceptance or a merge." },
  { id: "completed", label: "Completed", empty: "Nothing has ended yet." },
  { id: "all", label: "All", empty: "No work order yet." },
];

export function isGroup(name: string): name is GroupTab {
  return groups.some((group) => group.id === name);
}

const titleOf = (row: WorkRow) => row.order.title || row.order.id;

export const workAddress = (id: string, tab = "") => addressOf("/work-order", { id, tab });

// Under is the line beneath a work order's title: what a parked lane waits for, why work stopped, or
// what a lane said of its end.
function Under({ row }: { row: WorkRow }) {
  // A planning work order's lane is its planning session, which a person talks to in its pane.
  if (row.order.kind === "plan" && row.lane) {
    return (
      <span className="cell-note">
        <Link to={addressOf("/lane", { id: row.lane.id })}>Open the planning session</Link>
      </span>
    );
  }
  const wake = row.held?.wakes[0];
  if (wake) {
    return (
      <span className="cell-note">
        <WakeWords expression={wake.expression} />
      </span>
    );
  }
  if (row.stuck) {
    // Failed work says what its lane said of why, which its state does not.
    if (row.stuck.reason === "failed" && row.stuck.detail) {
      return <span className="cell-note mono">{row.stuck.detail}</span>;
    }
    return <span className="cell-note">{row.stuck.work_order ? words(row.stuck.reason) : ""}</span>;
  }
  if (row.completed?.reason) {
    const { reason, detail } = row.completed;
    return <span className="cell-note mono">{detail ? `${reason}: ${detail}` : reason}</span>;
  }
  return null;
}

function attempts(row: WorkRow) {
  const { attempts: used = 0, max_attempts: max } = row.order;
  return max ? `${Math.min(used + 1, max)} of ${max}` : <Absent>none set</Absent>;
}

// Repository names the repository a work order is in, by the name the project calls it, with its ID in the
// title. A repository the project does not name shows its ID.
function Repository({ id, names }: { id: string; names: Map<string, string> }) {
  const name = names.get(id);
  return name ? <span title={id}>{name}</span> : <Id value={id} />;
}

// WorkTable lists work orders: ID, title, state, repository, lane, attempts, and time in state. A title
// links to its work order.
function WorkTable({ rows, label, repositories }: { rows: WorkRow[]; label: string; repositories: Map<string, string> }) {
  return (
    <TableRegion label={label}>
      <table className="table stack-on-phone">
        <thead>
          <tr>
            <th scope="col">ID</th>
            <th scope="col">Title</th>
            <th scope="col">State</th>
            <th scope="col">Repository</th>
            <th scope="col">Lane</th>
            <th scope="col" className="r">
              Attempt
            </th>
            <th scope="col" className="r">
              In state
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.order.id} data-work={row.order.id}>
              <td data-label="ID">
                <Id value={row.order.id} />
              </td>
              <td data-label="Title" className="title-cell">
                <Link to={workAddress(row.order.id)} data-item>
                  {titleOf(row)}
                </Link>
                <ReviewOf order={row.order} />
                <Under row={row} />
              </td>
              <td data-label="State">
                <span className="row-tight">
                  <WorkBadge state={row.order.state} />
                  {row.stuck && row.stuck.reason !== row.order.state ? <span className="cond warn">{words(row.stuck.reason)}</span> : null}
                </span>
              </td>
              <td data-label="Repository">{row.order.repository_id ? <Repository id={row.order.repository_id} names={repositories} /> : <Absent>none</Absent>}</td>
              <td data-label="Lane">
                {row.lane ? (
                  <Link to={addressOf("/lane", { id: row.lane.id })} className="id-link">
                    <Id value={row.lane.id} />
                  </Link>
                ) : (
                  <Absent>no lane</Absent>
                )}
              </td>
              <td data-label="Attempt" className="r">
                {attempts(row)}
              </td>
              <td data-label="In state" className="r">
                {row.since ? <Lasted at={row.since} ended={groupOf(row) === "completed"} /> : <Absent>not read</Absent>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

// WorkOrdersPage is every work order of the project, a tab for each group with its count.
export function WorkOrdersPage({ live, group }: { live: Live; group: string }) {
  const { index, failure, busy } = useWorkIndex(live);
  const now = useClock();
  const chosen: GroupTab = isGroup(group) ? group : "open";
  const rows = index?.rows ?? [];
  const inGroup = (id: GroupTab) => (id === "all" ? rows : rows.filter((row) => groupOf(row) === id));
  const spec = groups.find((row) => row.id === chosen) ?? groups[0];
  return (
    <Page title="Work orders" note={index ? `${rows.length} work order${rows.length === 1 ? "" : "s"} in this project` : undefined} actions={<NewWorkOrder />}>
      <LinkTabs
        label="Groups"
        tabs={groups.map((row) => ({ to: addressOf("/work", { group: row.id }), label: row.label, count: index ? inGroup(row.id).length : undefined, current: row.id === chosen }))}
      />
      <div className="tab-body">
        {failure ? <Banner tone="danger">{index ? `This list is not current. ${failure}` : failure}</Banner> : null}
        {index ? (
          <ViewTime.Provider value={{ asOf: index.stamp.as_of, elapsed: Math.max(0, now - index.readAt) }}>
            <div aria-busy={busy}>
              {inGroup(chosen).length ? (
                <div className="panel">
                  <WorkTable rows={inGroup(chosen)} label={`${spec?.label ?? "Work"} work orders`} repositories={index.repositories} />
                </div>
              ) : (
                <div className="panel">
                  <EmptyState icon="stack" title={spec?.empty ?? "Nothing here."} />
                </div>
              )}
            </div>
            <StampLine stamp={index.stamp} />
          </ViewTime.Provider>
        ) : failure ? null : (
          <div className="panel">
            <SkeletonRows rows={6} />
          </div>
        )}
      </div>
    </Page>
  );
}

// The path a work order takes through its states, which the stepper draws.
const path = ["intake", "planning", "contract_review", "ready", "running", "acceptance", "integrating", "done"];

// Stepper draws a work order's path: done steps with a check, the current one as its badge, later ones as
// rings. A state off the path, such as parked or failed, shows as the current badge where it stands.
function Stepper({ state, rounds }: { state: string; rounds?: number }) {
  const at = path.indexOf(state);
  // A state off the path, such as claimed, parked, or failed, stands where running would.
  const steps = at >= 0 ? path : path.map((step) => (step === "running" ? state : step));
  const reached = at >= 0 ? at : path.indexOf("running");
  return (
    <ol className="stepper" aria-label="Lifecycle">
      {steps.map((step, position) => {
        const done = position < reached;
        const current = position === reached;
        const label = workStates[step]?.label ?? words(step);
        return (
          <li key={step} className={`step${done ? " done" : ""}${current ? " current" : ""}`} aria-current={current ? "step" : undefined}>
            {current ? (
              <WorkBadge state={step} />
            ) : (
              <>
                <Icon name={done ? "check" : "info"} size="sm" className={done ? "step-done" : "step-later"} />
                <span>
                  {label}
                  {step === "acceptance" && rounds && rounds > 1 ? ` · ${rounds} rounds` : ""}
                </span>
                <span className="visually-hidden">{done ? ", done" : ", later"}</span>
              </>
            )}
          </li>
        );
      })}
    </ol>
  );
}

// Merges lists who approved each reviewed commit for merge, at which level, and what landed, and for work
// waiting to merge, what the merge waits on.
function Merges({ standing, merging }: { standing: Standing; merging: boolean }) {
  const records = standing.integrations ?? [];
  if (!records.length && !(merging && standing.why)) {
    return <p className="muted">No merge approval yet.</p>;
  }
  return (
    <ul className="criteria" aria-label="Merge">
      {records.map((record) => (
        <li key={record.id}>
          <Digest value={record.approved_commit} length={8} /> approved by {words(record.approved_by_kind)}
          {record.approved_by_id ? (
            <>
              {" "}
              <span className="mono">{record.approved_by_id}</span>
            </>
          ) : null}{" "}
          at {words(record.merge_authority)}
          {record.merged_at ? (
            <span>
              , merged
              {record.merged_commit ? (
                <>
                  {" "}
                  <span className="mono">{record.merged_commit.slice(0, 8)}</span>
                </>
              ) : null}
            </span>
          ) : null}
          {record.pull_request ? <span>, pull request #{record.pull_request}</span> : null}
        </li>
      ))}
      {merging && standing.why ? <li>The merge waits: {standing.why}.</li> : null}
    </ul>
  );
}

// ChecksTab reads where the work stands now, whatever its state, so the pull request shows from the moment the
// forge reports it: the pull request, the checks on record, each merge approval, and the artifacts the work left.
function ChecksTab({ id, live }: { id: string; live: Live }) {
  const { view: standing, failure } = useView<Standing>(() => readAcceptance(id), `work-order:${id}:acceptance`, live);
  if (!standing) {
    return failure ? <Banner tone="danger">{failure}</Banner> : <SkeletonRows rows={4} />;
  }
  const merging = standing.state === "integrating";
  // An artifact with no value says nothing, so it is left out.
  const artifacts = standing.artifacts.filter((artifact) => artifact.uri);
  const empty = !standing.pull_request && !standing.checks.length && !standing.integrations?.length && !artifacts.length && !(merging && standing.why);
  if (empty) {
    return (
      <EmptyState icon="pr" title="No checks yet">
        The pull request and the checks show here once the work has a commit.
      </EmptyState>
    );
  }
  return (
    <div className="stack-8">
      {failure ? <Banner tone="danger">{`This tab is not current. ${failure}`}</Banner> : null}
      {standing.pull_request ? <PullRequestPanel pr={standing.pull_request} /> : null}
      <Panel title="Checks" level={3}>
        <Checks standing={standing} />
      </Panel>
      <Panel title="Merge" level={3}>
        <Merges standing={standing} merging={merging} />
      </Panel>
      {artifacts.length ? (
        <Panel title="Artifacts" level={3}>
          <ul className="criteria" aria-label="Artifacts">
            {artifacts.map((artifact) => (
              <li key={artifact.id}>
                {words(artifact.kind)}: <span className="mono">{artifact.uri}</span>
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}
    </div>
  );
}

function Criteria({ standing, waiting }: { standing: Standing; waiting: boolean }) {
  if (!standing.assessment.criteria.length) {
    return null;
  }
  const table = waiting ? waitingStandingWords : standingWords;
  return (
    <ul className="criteria" aria-label="Acceptance criteria">
      {standing.assessment.criteria.map((criterion) => (
        <li key={criterion.criterion_id}>
          <span className="mono">{criterion.criterion_id}</span> {words(criterion.standing, table)}
        </li>
      ))}
    </ul>
  );
}

type Tab = "review" | "changes" | "checks" | "lanes" | "activity";

const tabs: { id: Tab; label: string }[] = [
  { id: "review", label: "Review" },
  { id: "changes", label: "Changes" },
  { id: "checks", label: "Pull request and checks" },
  { id: "lanes", label: "Lanes" },
  { id: "activity", label: "Activity" },
];

// The ID of every tab, which the accessibility checks hold to a state each.
export const workOrderTabs: string[] = tabs.map((tab) => tab.id);

// needFor finds what the work order waits on in Needs you: its acceptance, its review, a request of its
// lane, or why it stopped.
function needFor(needs: NeedsYou, id: string): NeedItem | undefined {
  return needs.items.find(
    (item) =>
      (item.kind === "work" && item.work.work_order?.id === id) ||
      (item.kind === "contract_review" && item.contract_review.work_order.id === id) ||
      (item.kind === "approval" && item.approval.work_order_id === id),
  );
}

function Decision({ item, after }: { item: NeedItem; after: () => Promise<void> }) {
  const frame = { project: item.project, title: headingOf(item) };
  return (
    <ItemProject.Provider value={item.project?.name ?? ""}>
      {item.kind === "work" ? (
        <WorkCard id={item.id} work={item.work} after={after} frame={frame} inPage />
      ) : item.kind === "contract_review" ? (
        <ContractReviewCard review={item.contract_review} after={after} frame={frame} />
      ) : item.kind === "approval" ? (
        <ApprovalDecision approval={item.approval} after={after} frame={frame} />
      ) : null}
    </ItemProject.Provider>
  );
}

// Worktrees lists the lanes of a work order that ended and left a worktree, each with the action that asks its
// worker to remove it. The server says which lanes those are, and the work order's allowance says a member may.
function Worktrees({ order, allowance, lanes, after }: { order: WorkOrder; allowance?: Allowance; lanes: FloorLane[]; after: () => Promise<void> }) {
  const [asking, setAsking] = useState<FloorLane | null>(null);
  const archivable = lanes.filter((lane) => lane.archivable);
  if (!allowance?.actions.includes("archive_lane") || archivable.length === 0) {
    return null;
  }
  return (
    <Panel title="Worktrees to remove" level={3}>
      <ul className="criteria" aria-label="Worktrees to remove">
        {archivable.map((lane) => (
          <li key={lane.id} className="row-tight">
            <Link to={addressOf("/lane", { id: lane.id })} className="id-link">
              <Id value={lane.id} />
            </Link>
            <span className="subtle small">ended, generation {lane.generation}</span>
            <Button small aria-label={`Archive the worktree of lane ${lane.id}`} onClick={() => setAsking(lane)}>
              Archive the worktree
            </Button>
          </li>
        ))}
      </ul>
      {asking ? <WorkActionDialog order={order} action="archive_lane" allowance={allowance} lane={asking} onClose={() => setAsking(null)} after={after} /> : null}
    </Panel>
  );
}

// WorkOrderPage is one work order: its header and lifecycle, tabs for its review, changes, checks,
// lanes, and activity, and beside them the decision it waits on and its facts.
export function WorkOrderPage({ live, id, tab, dialog, needs, after }: { live: Live; id: string; tab: string; dialog: string; needs: NeedsYou; after: () => Promise<void> }) {
  const { index, failure } = useWorkIndex(live);
  // The lanes the floor holds, which say whose worktree may be removed.
  const floor = useView<ShopFloor>(() => readView<ShopFloor>("shop-floor"), "work-order:floor", live);
  const now = useClock();
  const tabsId = useId();
  const row = index?.rows.find((found) => found.order.id === id);
  const chosen = (tabs.find((found) => found.id === tab)?.id ?? "review") as Tab;
  const crumbs = (
    <>
      <Link to="/work">Work orders</Link>
      <Icon name="chevron-right" size="sm" />
      <span>{id || "None"}</span>
    </>
  );
  if (!index) {
    return (
      <Page title="Work order" crumbs={crumbs}>
        {failure ? <Banner tone="danger">{failure}</Banner> : <SkeletonRows rows={5} />}
      </Page>
    );
  }
  if (!row) {
    return (
      <Page title="No such work order" crumbs={crumbs}>
        <div className="panel">
          <EmptyState icon="stack" title={`No work order ${id} in this project`} action={<Link to="/work">All work orders</Link>} />
        </div>
      </Page>
    );
  }
  const { order } = row;
  const standing = row.completed?.standing ?? row.stuck?.standing;
  // Stuck work's own read says what can be done about it now; anything else it waits on is in Needs you.
  const need = row.stuck ? ({ kind: "work", id: row.stuck.id, since: row.stuck.since, work: row.stuck } as NeedItem) : needFor(needs, order.id);
  const contract = row.plan?.contracts.find((history) => history.id === order.contract_id);
  const lanes = (floor.view?.lanes ?? []).filter((lane) => lane.work_order === order.id);
  // The lane the Lanes tab describes: the one the work order runs, or else the last lane the floor holds of it.
  const shown = row.lane ?? lanes[lanes.length - 1];
  // A decision card beside the page offers some actions already, so the header leaves those to it.
  const decided = need?.kind === "work" ? need.work.actions : [];
  const closed = () => dialog && go(workAddress(order.id, chosen === "review" ? "" : chosen), { stay: true, replace: true });
  return (
    <Page
      crumbs={crumbs}
      actions={
        <>
          <StartWork order={order} allowed={row.allowed} inView opened={dialog === "start"} onClosed={closed} after={after} />
          <WorkActions order={order} allowance={row.allowed} lanes={lanes} omit={decided} opened={dialog} onClosed={closed} after={after} />
        </>
      }
      title={
        <>
          <span className="title-id">{order.id}</span> {order.title || ""}
        </>
      }
      note={
        <span className="row-tight work-meta">
          <WorkBadge state={order.state} />
          <span>
            {words(order.kind)}
            {order.size ? ` · ${order.size}` : ""}
          </span>
          <ReviewOf order={order} />
          {order.repository_id ? (
            <span className="row-tight">
              <Icon name="repo" size="sm" />
              <Repository id={order.repository_id} names={index.repositories} />
            </span>
          ) : null}
          {order.branch ? (
            <span className="row-tight">
              <Icon name="branch" size="sm" />
              <span className="mono small">{order.branch}</span>
            </span>
          ) : null}
        </span>
      }
    >
      <ViewTime.Provider value={{ asOf: index.stamp.as_of, elapsed: Math.max(0, now - index.readAt) }}>
        {failure ? <Banner tone="danger">{`This page is not current. ${failure}`}</Banner> : null}
        <Stepper state={order.state} rounds={standing?.rounds} />
        <div className="grid grid-main-side work-order">
          <div className="stack">
            <Tabs label="Work order" idBase={tabsId} selected={chosen} tabs={tabs} onSelect={(next) => go(workAddress(order.id, next === "review" ? "" : next), { stay: true })} />
            <TabPanel idBase={tabsId} selected={chosen}>
              {chosen === "review" ? (
                standing && (standing.verdicts?.length || standing.reviews?.length || standing.assessment.criteria.length) ? (
                  <div className="stack-8">
                    <Verdicts standing={standing} />
                    <Criteria standing={standing} waiting={order.state === "acceptance"} />
                  </div>
                ) : (
                  <EmptyState icon="review" title="No verdict yet">
                    A review runs once the work's lane finishes, and its verdict on each criterion shows here.
                  </EmptyState>
                )
              ) : null}
              {chosen === "changes" ? <ChangesTab id={order.id} live={live} /> : null}
              {chosen === "checks" ? <ChecksTab id={order.id} live={live} /> : null}
              {chosen === "lanes" ? (
                shown ? (
                  <Panel title={<>Lane {shown.id}</>} level={3} actions={<Link to={addressOf("/lane", { id: shown.id })}>Open lane</Link>}>
                    <Facts>
                      <Fact name="State">
                        <span className="row-tight">
                          <LaneBadge state={shown.actual_state} />
                          {(shown.conditions ?? []).map((condition) => (
                            <ConditionChip key={condition} name={condition} />
                          ))}
                        </span>
                      </Fact>
                      <Fact name="Generation">{shown.generation}</Fact>
                      <Fact name="Seen">
                        <Freshness kind={freshnessOf(shown.freshness)} at={shown.as_of} source="the lane's worker" />
                      </Fact>
                      <Fact name="Worker">{shown.worker_id ? <Id value={shown.worker_id} /> : <Absent>none yet</Absent>}</Fact>
                      <Fact name="Estimated cost">
                        <Absent>unavailable</Absent>
                      </Fact>
                    </Facts>
                  </Panel>
                ) : (
                  <EmptyState icon="lanes" title="No lane has run this work yet" />
                )
              ) : null}
              {chosen === "lanes" ? <Worktrees order={order} allowance={row.allowed} lanes={lanes} after={after} /> : null}
              {chosen === "activity" ? <ActivityTab id={order.id} live={live} /> : null}
            </TabPanel>
          </div>
          <aside className="stack" aria-label="Decision and facts">
            {need ? <Decision item={need} after={after} /> : null}
            <Panel title="Facts" level={2}>
              <Facts>
                <Fact name="Contract">
                  {order.contract_id ? (
                    <span className="row-tight">
                      <RecordChip id={order.contract_id} />
                      {contract?.versions[contract.versions.length - 1]?.title ?? ""}
                    </span>
                  ) : (
                    <Absent>none</Absent>
                  )}
                </Fact>
                <Fact name="Milestone">{row.milestone ? row.milestone.title : <Absent>none</Absent>}</Fact>
                <Fact name="Plan">{row.plan ? <Link to={addressOf("/plans", { plan: row.plan.plan.id })}>{row.plan.plan.title}</Link> : <Absent>none</Absent>}</Fact>
                <Fact name="Attempt">{attempts(row)}</Fact>
                <Fact name="Held by">{order.lease_worker ? <Id value={order.lease_worker} /> : <Absent>nobody</Absent>}</Fact>
                <Fact name="In state">{row.since ? <Lasted at={row.since} ended={groupOf(row) === "completed"} /> : <Absent>not read</Absent>}</Fact>
                <Fact name="Merge authority">{standing?.authority ? <span className="mono">{standing.authority}</span> : <Absent>not set</Absent>}</Fact>
              </Facts>
            </Panel>
            <WorkEstimatePanel id={order.id} live={live} />
          </aside>
        </div>
        <StampLine stamp={index.stamp} />
      </ViewTime.Provider>
    </Page>
  );
}
