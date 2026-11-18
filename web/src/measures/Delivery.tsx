import type { DeliveryGroup, DeliveryGrouping, DeliveryWeek, LookBack } from "../api/measures";
import { byProject, deliveryGroupings, readDelivery, readDeliveryByProject } from "../api/measures";
import type { Project } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, go, Link } from "../app/route";
import { Page } from "../parts/parts";
import { Segmented, LinkTabs } from "../ui/controls";
import { Absent, Banner, EmptyState, Panel, SkeletonRows, TableRegion } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { CIDurationPanel } from "./CIDurations";
import { defaultWindow, finished, groupName, labelOf, pick, sharesOf, weekDate, windows } from "./lookBack";
import type { Scope, Share } from "./lookBack";
import { fixed, span, usd, utcDate } from "./format";

// The delivery measures are read from the delivery records the daemon keeps, which change as work ends, so the
// page asks again no oftener than this however much it hears.
const gapMs = 30_000;

export type MeasuresTab = "factory" | "delivery" | "estimates";

// MeasuresTabs are the Measures view's tabs. Each has an address of its own.
export function MeasuresTabs({ tab }: { tab: MeasuresTab }) {
  return (
    <LinkTabs
      label="Measures view"
      tabs={[
        { to: "/measures", label: "Factory", current: tab === "factory" },
        { to: addressOf("/measures", { tab: "delivery" }), label: "Delivery", current: tab === "delivery" },
        { to: addressOf("/measures", { tab: "estimates" }), label: "Estimates", current: tab === "estimates" },
      ]}
    />
  );
}

// band says a duration's two percentiles: p50 15h · p85 1d 6h.
const band = (q: { p50: number; p85: number }) => `p50 ${span(q.p50)} · p85 ${span(q.p85)}`;

// costBand says a cost's two percentiles, with at least before them when some cost was never reported.
const costBand = (group: DeliveryGroup) =>
  group.cost ? `${group.cost_lower_bound ? "at least " : ""}p50 ${usd(group.cost.p50, false)} · p85 ${usd(group.cost.p85, false)}` : null;

// Trend draws the weeks that work reached done, oldest first, as a line. The words say each week's count.
function Trend({ weeks, label }: { weeks: DeliveryWeek[]; label: string }) {
  const most = Math.max(1, ...weeks.map((week) => week.done));
  const step = weeks.length > 1 ? 96 / (weeks.length - 1) : 0;
  const points = weeks.map((week, at) => `${(at * step).toFixed(1)},${(22 - (week.done / most) * 20).toFixed(1)}`).join(" ");
  return (
    <svg className="spark" viewBox="0 0 96 24" role="img" aria-label={`${label}: ${weeks.map((week) => week.done).join(", ")}`} focusable="false">
      <polyline points={points} fill="none" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}

// ShareBar draws where a group's time went, each kind of segment its own fill, and waiting for a person set
// apart. The list beside it says the same in words.
function ShareBar({ shares, label }: { shares: Share[]; label: string }) {
  return (
    <div className="share-bar" role="img" aria-label={`${label}: ${shares.map((share) => `${share.word} ${share.percent}`).join(", ")}`}>
      {shares.map((share) => (
        <i key={share.kind} className={`seg fill-${share.fill}${share.apart ? " apart" : ""}`} data-tone={share.tone} style={{ flexGrow: share.fraction }} />
      ))}
    </div>
  );
}

function GroupsTable({ report, by }: { report: LookBack; by: string }) {
  return (
    <TableRegion label={`Delivery by ${by}`}>
      <table className="table stack-on-phone">
        <thead>
          <tr>
            <th scope="col">{groupName[by] ?? by}</th>
            <th scope="col" className="r">
              Done
            </th>
            <th scope="col" className="r">
              Failed
            </th>
            <th scope="col" className="r">
              Canceled
            </th>
            <th scope="col" className="r">
              Per week
            </th>
            <th scope="col">Weekly trend</th>
            <th scope="col">Elapsed</th>
            <th scope="col">Delivery</th>
            <th scope="col">Estimated cost</th>
          </tr>
        </thead>
        <tbody>
          {report.groups.map((group) => {
            const name = labelOf(by, group.key);
            return (
              <tr key={group.key || "all"}>
                <th scope="row">{name}</th>
                <td data-label="Done" className="r">
                  {group.done}
                </td>
                <td data-label="Failed" className="r">
                  {group.failed}
                </td>
                <td data-label="Canceled" className="r">
                  {group.canceled}
                </td>
                <td data-label="Per week" className="r">
                  {fixed(group.per_week, 1)}
                </td>
                <td data-label="Weekly trend">
                  <Trend weeks={group.trend} label={`${name}, work done each week over the last ${group.trend.length} weeks, oldest first`} />
                </td>
                <td data-label="Elapsed">{group.elapsed ? band(group.elapsed) : <Absent>no data</Absent>}</td>
                <td data-label="Delivery">{group.delivery ? band(group.delivery) : <Absent>no data</Absent>}</td>
                <td data-label="Estimated cost">{costBand(group) ?? <Absent>no data</Absent>}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </TableRegion>
  );
}

// WhereTheTimeWent lists each group's time by kind of segment, as a bar and as words.
function WhereTheTimeWent({ report, by }: { report: LookBack; by: string }) {
  const rows = report.groups.map((group) => ({ group, name: labelOf(by, group.key), shares: sharesOf(group.share) })).filter((row) => row.shares.length > 0);
  return (
    <section className="panel" role="region" aria-label="Where the time went">
      <div className="panel-head">
        <h2>Where the time went</h2>
      </div>
      <div className="panel-body stack">
        <p className="muted">Each group's elapsed time, for the work that reached done, by what it was spent on. Time waiting for a person is set apart from the agents' time.</p>
        {rows.length === 0 ? (
          <p className="muted">No work that reached done has time to show.</p>
        ) : (
          <ul className="share-rows">
            {rows.map(({ group, name, shares }) => (
              <li key={group.key || "all"} className="share-row">
                <p className="share-name">{name}</p>
                <ShareBar shares={shares} label={name} />
                <ul className="share-legend" aria-label={`Time by kind for ${name}`}>
                  {shares.map((share) => (
                    <li key={share.kind} className={share.apart ? "apart" : undefined}>
                      <i className={`seg-swatch fill-${share.fill}`} data-tone={share.tone} aria-hidden="true" />
                      {share.word} {share.percent}
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

// WeeklyTrend is the twelve weeks up to the end of the window of all work: the work that reached done in each,
// and its delivery time.
function WeeklyTrend({ weeks }: { weeks: DeliveryWeek[] }) {
  return (
    <Panel title="Weekly trend" flush>
      <TableRegion label="Weekly trend of all work">
        <table className="table">
          <thead>
            <tr>
              <th scope="col">Week of</th>
              <th scope="col" className="r">
                Done
              </th>
              <th scope="col">Delivery</th>
            </tr>
          </thead>
          <tbody>
            {weeks.map((week) => (
              <tr key={week.from}>
                <th scope="row">{weekDate(week)}</th>
                <td className="r">{week.done}</td>
                <td>{week.delivery ? band(week.delivery) : <Absent>none finished</Absent>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
    </Panel>
  );
}

// DeliveryBody shows one look back: a row for all work and each group, where the time went, and the weekly
// trend. Work that has not finished leaves the page saying so.
export function DeliveryBody({ report, scope, windowName }: { report: LookBack; scope: Scope; windowName: string }) {
  if (!finished(report.groups)) {
    return (
      <div className="panel">
        <EmptyState
          icon="measures"
          title="Delivery measures appear when work orders finish."
          action={<Link to="/work">Work orders</Link>}
        >
          Work that reached done, failed, or was canceled in the last {windowName} is counted here.
        </EmptyState>
      </div>
    );
  }
  const all = report.groups[0]?.key === "" ? report.groups[0] : undefined;
  return (
    <>
      <Panel title={`By ${(groupName[report.by] ?? report.by).toLowerCase()}`} flush foot={`From ${utcDate(report.from, false)} to ${utcDate(report.to, false)}. Time reads as the work's delivery time, which leaves out the time it waited for a lane, and keeps every wait for a person. ${scope === "all" ? "Each project's own records are read apart, because the percentiles of separate projects do not combine." : ""}`}>
        <GroupsTable report={report} by={report.by} />
      </Panel>
      <WhereTheTimeWent report={report} by={report.by} />
      {all ? <WeeklyTrend weeks={all.trend} /> : null}
    </>
  );
}

const scopeChoices = [
  { value: "project" as const, label: "This project" },
  { value: "all" as const, label: "All projects" },
];

type Spec = { window: string; by: DeliveryGrouping; scope: Scope };

// The address of the tab with what a person chose. A choice that is the default stays out of it.
const addressWith = (spec: Spec) =>
  addressOf("/measures", { tab: "delivery", window: spec.window === defaultWindow ? undefined : spec.window, by: spec.by === "kind" ? undefined : spec.by, scope: spec.scope === "all" ? "all" : undefined });

// DeliveryRead reads one look back and shows it. A new choice mounts a new one, so it reads at once, while
// what the daemon says of the ledger asks again no oftener than the gap.
function DeliveryRead({ live, projects, spec }: { live: Live; projects: Project[]; spec: Spec }) {
  const read = useView<LookBack>(() => (spec.scope === "all" ? readDeliveryByProject(projects, spec.window) : readDelivery(spec.window, spec.by)), "delivery", live, gapMs);
  const windowName = windows.find((one) => one.value === spec.window)?.label ?? spec.window;
  const report = read.view;
  return (
    <>
      {read.failure ? <Banner tone="danger">{report ? `What this view shows is not current. ${read.failure}` : read.failure}</Banner> : null}
      {report ? (
        <div className="view-body stack" aria-busy={read.busy}>
          <DeliveryBody report={{ ...report, by: spec.scope === "all" ? byProject : spec.by }} scope={spec.scope} windowName={windowName} />
        </div>
      ) : null}
      {!report && !read.failure ? (
        <div className="panel">
          <SkeletonRows rows={5} />
        </div>
      ) : null}
    </>
  );
}

// DeliveryPage is the Measures view's Delivery tab: delivery by a grouping over a window, for this project or,
// read apart, for every active project.
export function DeliveryPage({ live, projects, params }: { live: Live; projects: Project[]; params: URLSearchParams }) {
  const spec: Spec = {
    window: pick(windows, params.get("window"), defaultWindow),
    by: pick(deliveryGroupings, params.get("by"), "kind"),
    scope: params.get("scope") === "all" ? "all" : "project",
  };
  const choose = (next: Partial<Spec>) => go(addressWith({ ...spec, ...next }), { stay: true });
  const names = projects.filter((one) => one.state === "active").map((one) => one.name);
  return (
    <Page title="Measures" note="Where delivery time went, and how much work finished, from the delivery records the daemon keeps.">
      <MeasuresTabs tab="delivery" />
      <div className="tab-body">
        <div className="filters-row delivery-controls">
          <Segmented<string> label="Window" value={spec.window} choices={windows.map((one) => ({ value: one.value, label: one.label }))} onChange={(value) => choose({ window: value })} />
          <Segmented<Scope> label="Scope" value={spec.scope} choices={scopeChoices} onChange={(value) => choose({ scope: value })} />
          {spec.scope === "project" ? (
            <Field label="Group by">
              <Select value={spec.by} onChange={(event) => choose({ by: pick(deliveryGroupings, event.target.value, "kind") })}>
                {deliveryGroupings.map((one) => (
                  <option key={one.value} value={one.value}>
                    {one.label}
                  </option>
                ))}
              </Select>
            </Field>
          ) : (
            <p className="muted delivery-by">By project, each project's own records, because the percentiles of separate projects do not combine.</p>
          )}
        </div>
        <DeliveryRead key={`${spec.scope}:${spec.window}:${spec.by}:${names.join(",")}`} live={live} projects={projects} spec={spec} />
        {spec.scope === "project" ? (
          <div className="view-body stack">
            <CIDurationPanel live={live} />
          </div>
        ) : null}
      </div>
    </Page>
  );
}
