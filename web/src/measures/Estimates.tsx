import type { Calibrated, Calibration, CalibrationPeriod, CalibrationRate, CalibrationScope, MilestoneCalibration, MilestonePeriod, MilestoneStanding, Standing } from "../api/measures";
import { readCalibration } from "../api/measures";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, go, Link } from "../app/route";
import { Page } from "../parts/parts";
import { Segmented } from "../ui/controls";
import { Banner, EmptyState, Panel, SkeletonRows, TableRegion } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { MeasuresTabs } from "./Delivery";
import { calibrated, comparedText, confidenceText, forecastDates, hitsText, ratioText, shareText } from "./calibration";
import { utcDate, utcDay } from "./format";
import { defaultWindow, pick, windows } from "./lookBack";

// Calibration reads the delivery records and estimate outcomes the daemon keeps, which change as work ends, so the
// page asks again no oftener than this however much it hears.
const gapMs = 30_000;

type Spec = { window: string; repository: string };

// The address of the tab with what a person chose. A choice that is the default stays out of it.
const addressWith = (spec: Spec) => addressOf("/measures", { tab: "estimates", window: spec.window === defaultWindow ? undefined : spec.window, repository: spec.repository || undefined });

type Period = "recent" | "all_time";

// The two periods calibration reads, and what each is called over a window of the given name.
const periods: { key: Period; title: (windowName: string) => string }[] = [
  { key: "recent", title: (windowName) => `Last ${windowName}` },
  { key: "all_time", title: () => "All time" },
];

// Meter draws a rate as a bar filled to the rate, with a tick at the value a calibrated estimator's rate comes
// near. The words beside it say the same, so the bar is decoration.
function Meter({ rate, near }: { rate: number; near: number }) {
  return (
    <div className="meter" aria-hidden="true">
      <i style={{ width: `${rate * 100}%` }} />
      <b style={{ left: `${near * 100}%` }} />
    </div>
  );
}

// Hits is a hit rate: the counts it rests on and, where 10 or more outcomes stand behind it, the rate and its meter.
function Hits({ rate, near, label }: { rate: CalibrationRate; near: number; label: string }) {
  return (
    <td data-label={label} className="hits">
      {hitsText(rate)}
      {rate.rate === undefined ? null : <Meter rate={rate.rate} near={near} />}
    </td>
  );
}

// The first five columns are counts, and the six after them compare time and cost with their baselines.
const counts = 5;
const standingColumns = ["Outcomes", "Done", "Failed", "Canceled", "P85 open", "Time p50", "Time p85", "Time ratio", "Cost p50", "Cost p85", "Cost ratio"];

// StandingRow is one group's counts and its time and cost against their baselines.
function StandingRow({ name, standing }: { name: string; standing: Standing }) {
  const { time, cost } = standing;
  return (
    <tr>
      <th scope="row">{name}</th>
      <td data-label="Outcomes" className="r">
        {standing.outcomes}
      </td>
      <td data-label="Done" className="r">
        {standing.done}
      </td>
      <td data-label="Failed" className="r">
        {standing.failed}
      </td>
      <td data-label="Canceled" className="r">
        {standing.canceled}
      </td>
      <td data-label="P85 open" className="r">
        {standing.p85_open}
      </td>
      <Hits rate={time.p50} near={calibrated.p50} label="Time p50" />
      <Hits rate={time.p85} near={calibrated.p85} label="Time p85" />
      <td data-label="Time ratio">{ratioText(time.ratio)}</td>
      <Hits rate={cost.p50} near={calibrated.p50} label="Cost p50" />
      <Hits rate={cost.p85} near={calibrated.p85} label="Cost p85" />
      <td data-label="Cost ratio">{ratioText(cost.ratio)}</td>
    </tr>
  );
}

function StandingsTable({ label, first, rows }: { label: string; first: string; rows: { key: string; name: string; standing: Standing }[] }) {
  return (
    <TableRegion label={label}>
      <table className="table stack-on-phone">
        <thead>
          <tr>
            <th scope="col">{first}</th>
            {standingColumns.map((name, at) => (
              <th key={name} scope="col" className={at < counts ? "r" : undefined}>
                {name}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <StandingRow key={row.key} name={row.name} standing={row.standing} />
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

// groupsOf lists a confidence's outcomes over all tiers first, then by tier and by estimator version.
function groupsOf(group: Calibrated) {
  return [
    { key: "all", name: "All", standing: group.total },
    ...group.tiers.map((tier) => ({ key: `tier ${tier.tier}`, name: `Tier ${tier.tier}`, standing: tier })),
    ...group.estimator_versions.map((version) => ({ key: `estimator ${version.estimator_version}`, name: `Estimator ${version.estimator_version}`, standing: version })),
  ];
}

// Confidence is one confidence's standing over each period: the headline for confident estimates, and low
// confidence apart. A period with no work estimated this way says so.
function Confidence({ title, regionName, words, pick: of, report, windowName, foot }: {
  title: string;
  regionName: string;
  words: string;
  pick: (period: CalibrationPeriod) => Calibrated;
  report: CalibrationScope;
  windowName: string;
  foot: string;
}) {
  return (
    <Panel title={title} foot={foot}>
      <div className="stack">
        {periods.map(({ key, title: periodTitle }) => {
          const group = of(report[key]);
          const name = periodTitle(windowName);
          return (
            <section key={key} className="stack">
              <h3>{name}</h3>
              {group.total.outcomes === 0 ? (
                <p className="muted">No work that ended in this period had {words} estimate.</p>
              ) : (
                <StandingsTable label={`${regionName}, ${name.toLowerCase()}`} first="Group" rows={groupsOf(group)} />
              )}
            </section>
          );
        })}
      </div>
    </Panel>
  );
}

// Shares says how the work that ended was estimated: with a confident estimate, a low-confidence one, or none.
function Shares({ report, windowName }: { report: CalibrationScope; windowName: string }) {
  return (
    <Panel title="How the work that ended was estimated" flush>
      <TableRegion label="Share of ended work by how it was estimated">
        <table className="table stack-on-phone">
          <thead>
            <tr>
              <th scope="col">Period</th>
              <th scope="col" className="r">
                Ended
              </th>
              <th scope="col">Confident</th>
              <th scope="col">Low confidence</th>
              <th scope="col">Absent</th>
            </tr>
          </thead>
          <tbody>
            {periods.map(({ key, title }) => {
              const { share } = report[key];
              return (
                <tr key={key}>
                  <th scope="row">{title(windowName)}</th>
                  <td data-label="Ended" className="r">
                    {share.ended}
                  </td>
                  <td data-label="Confident">{shareText(share.confident)}</td>
                  <td data-label="Low confidence">{shareText(share.low)}</td>
                  <td data-label="Absent">{shareText(share.none)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </TableRegion>
    </Panel>
  );
}

// Weekly lists the weeks up to the end of the window that had outcomes, a table for each confidence.
function Weekly({ report }: { report: CalibrationScope }) {
  return (
    <Panel title="Weekly calibration">
      <div className="stack">
        {(["confident", "low"] as const).map((confidence) => {
          const rows = report.weeks
            .filter((week) => week[confidence].outcomes > 0)
            .map((week) => ({ key: String(week.from), name: utcDay(week.from), standing: week[confidence] }));
          const words = confidence === "confident" ? "confident estimates" : "low confidence estimates";
          return (
            <section key={confidence} className="stack">
              <h3>{confidence === "confident" ? "Confident estimates" : "Low confidence"}</h3>
              {rows.length === 0 ? <p className="muted">No work ended with {confidence === "confident" ? "a confident" : "a low-confidence"} estimate in these weeks.</p> : <StandingsTable label={`Weekly calibration, ${words}`} first="Week of" rows={rows} />}
            </section>
          );
        })}
      </div>
    </Panel>
  );
}

function MilestoneHeld({ calibration, windowName }: { calibration: MilestoneCalibration; windowName: string }) {
  const rows: { key: string; name: string; standing: MilestoneStanding }[] = [];
  for (const { key, title } of periods) {
    const period: MilestonePeriod = calibration[key];
    for (const confidence of ["confident", "low"] as const) {
      if (period[confidence].outcomes > 0) {
        rows.push({ key: `${key} ${confidence}`, name: `${title(windowName)}, ${confidence === "low" ? "low confidence" : confidence}`, standing: period[confidence] });
      }
    }
  }
  if (rows.length === 0) {
    return <p className="muted">No milestone has ended with a forecast to compare.</p>;
  }
  return (
    <TableRegion label="How milestone forecasts held">
      <table className="table stack-on-phone">
        <thead>
          <tr>
            <th scope="col">Period</th>
            {["Outcomes", "Done", "Failed", "Canceled", "P85 open"].map((name) => (
              <th key={name} scope="col" className="r">
                {name}
              </th>
            ))}
            <th scope="col">Ended at or before p50</th>
            <th scope="col">Ended at or before p85</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ key, name, standing }) => (
            <tr key={key}>
              <th scope="row">{name}</th>
              <td data-label="Outcomes" className="r">
                {standing.outcomes}
              </td>
              <td data-label="Done" className="r">
                {standing.done}
              </td>
              <td data-label="Failed" className="r">
                {standing.failed}
              </td>
              <td data-label="Canceled" className="r">
                {standing.canceled}
              </td>
              <td data-label="P85 open" className="r">
                {standing.p85_open}
              </td>
              <Hits rate={standing.p50} near={calibrated.p50} label="Ended at or before p50" />
              <Hits rate={standing.p85} near={calibrated.p85} label="Ended at or before p85" />
            </tr>
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

// Milestones shows how milestone forecasts held, and each ended milestone's baseline forecast beside the day its
// last work order ended.
function Milestones({ calibration, windowName }: { calibration: MilestoneCalibration; windowName: string }) {
  return (
    <Panel title="Milestones">
      <div className="stack">
        <MilestoneHeld calibration={calibration} windowName={windowName} />
        {calibration.results.length === 0 ? (
          <p className="muted">No milestone has ended yet.</p>
        ) : (
          <TableRegion label="Each milestone's forecast and its actual date">
            <table className="table stack-on-phone">
              <thead>
                <tr>
                  <th scope="col">Milestone</th>
                  <th scope="col">Plan</th>
                  <th scope="col">Confidence</th>
                  <th scope="col">Outcome</th>
                  <th scope="col">Forecast p50</th>
                  <th scope="col">Forecast p85</th>
                  <th scope="col">Ended</th>
                  <th scope="col">At or before p50</th>
                  <th scope="col">At or before p85</th>
                </tr>
              </thead>
              <tbody>
                {calibration.results.map((result) => {
                  const dates = forecastDates(result);
                  return (
                    <tr key={`${result.plan_id} ${result.milestone_id}`}>
                      <th scope="row">{result.milestone_id}</th>
                      <td data-label="Plan">{result.plan_id}</td>
                      <td data-label="Confidence">{confidenceText(result)}</td>
                      <td data-label="Outcome">{result.outcome}</td>
                      {dates ? (
                        <>
                          <td data-label="Forecast p50">{dates.p50}</td>
                          <td data-label="Forecast p85">{dates.p85}</td>
                        </>
                      ) : (
                        <td data-label="Forecast" colSpan={2}>
                          no forecast dates
                        </td>
                      )}
                      <td data-label="Ended">{utcDate(result.actual_end, false)}</td>
                      <td data-label="At or before p50">{comparedText(result.at_or_below_p50)}</td>
                      <td data-label="At or before p85">{comparedText(result.at_or_below_p85)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </TableRegion>
        )}
      </div>
    </Panel>
  );
}

const openBands = "An outcome whose baseline p85 was open counts in the p50 rate and the ratio, and is left out of the p85 rate. A tick on a meter marks the value a calibrated estimator's rate comes near: 50% for p50, and 85% for p85. A ratio is the median of actual over p50, near 1 when calibrated.";

// nothingEnded says no work ended in any period and no milestone has: there is no outcome to calibrate against.
const nothingEnded = (report: Calibration) => report.scopes.every((scope) => scope.all_time.share.ended === 0) && report.milestones.results.length === 0;

// EstimatesBody shows one scope's calibration: confident estimates as the headline, low confidence apart, the share
// of work estimated each way, the weekly line, and the milestones. A rate under 10 outcomes shows counts alone.
export function EstimatesBody({ report, scope, windowName }: { report: Calibration; scope: CalibrationScope; windowName: string }) {
  if (nothingEnded(report)) {
    return (
      <div className="panel">
        <EmptyState icon="measures" title="Estimate calibration appears when work orders finish." action={<Link to="/work">Work orders</Link>}>
          Work that ended with a baseline estimate is compared with it here, over the last {windowName} and over all time.
        </EmptyState>
      </div>
    );
  }
  return (
    <>
      <p className="muted">
        Last {windowName}, from {utcDay(report.from)} to {utcDay(report.to)} UTC. All time reads every outcome the project has. Not enough finished estimated work for a rate yet: a rate shows when at
        least 10 outcomes stand behind it, and below that only its counts stand.
      </p>
      <Shares report={scope} windowName={windowName} />
      <Confidence title="Confident estimates" regionName="Confident estimates" words="a confident" pick={(period) => period.confident} report={scope} windowName={windowName} foot={openBands} />
      <Confidence title="Low confidence" regionName="Low confidence estimates" words="a low-confidence" pick={(period) => period.low} report={scope} windowName={windowName} foot="Low-confidence estimates are counted apart, so a thin band never hides whether the confident estimates are calibrated." />
      <Weekly report={scope} />
      <Milestones calibration={report.milestones} windowName={windowName} />
    </>
  );
}

// EstimatesRead reads calibration over the window a person chose and shows it, with the controls above it. The read
// carries the window it was made for, so a slow answer for another window never stands under this one's name.
function EstimatesRead({ live, spec, choose }: { live: Live; spec: Spec; choose: (next: Partial<Spec>) => void }) {
  const read = useView<{ window: string; report: Calibration }>(async () => ({ window: spec.window, report: await readCalibration(spec.window) }), `estimates:${spec.window}`, live, gapMs);
  const windowName = windows.find((one) => one.value === spec.window)?.label.toLowerCase() ?? spec.window;
  const current = read.view?.window === spec.window ? read.view.report : undefined;
  const repositories = current?.scopes.flatMap((one) => (one.repository ? [one.repository] : [])) ?? [];
  const scope = current?.scopes.find((one) => (one.repository ?? "") === spec.repository) ?? current?.scopes[0];
  return (
    <>
      <div className="filters-row delivery-controls">
        <Segmented<string> label="Window" value={spec.window} choices={windows.map((one) => ({ value: one.value, label: one.label }))} onChange={(value) => choose({ window: value })} />
        {repositories.length > 0 ? (
          <Field label="Repository">
            <Select value={scope?.repository ?? ""} onChange={(event) => choose({ repository: event.target.value })}>
              <option value="">All work</option>
              {repositories.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </Select>
          </Field>
        ) : null}
      </div>
      {read.failure ? <Banner tone="danger">{current ? `What this view shows is not current. ${read.failure}` : read.failure}</Banner> : null}
      {current && scope ? (
        <div className="view-body stack" aria-busy={read.busy}>
          <EstimatesBody report={current} scope={scope} windowName={windowName} />
        </div>
      ) : null}
      {!current && !read.failure ? (
        <div className="panel">
          <SkeletonRows rows={5} />
        </div>
      ) : null}
    </>
  );
}

// EstimatesPage is the Measures view's Estimates tab: how often finished work landed inside the estimates it was
// baselined with, over a window and over all time, for all of the project's work or one repository's.
export function EstimatesPage({ live, params }: { live: Live; params: URLSearchParams }) {
  const spec: Spec = { window: pick(windows, params.get("window"), defaultWindow), repository: params.get("repository") ?? "" };
  const choose = (next: Partial<Spec>) => go(addressWith({ ...spec, ...next }), { stay: true });
  return (
    <Page title="Measures" note="How often finished work landed inside the estimates it was baselined with, from this project's own outcomes.">
      <MeasuresTabs tab="estimates" />
      <div className="tab-body">
        <EstimatesRead live={live} spec={spec} choose={choose} />
      </div>
    </Page>
  );
}
