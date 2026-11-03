import type { Durations, MeasureReport, Ratio } from "../api/types";
import { Fact, Section, words } from "../parts/parts";

const noData = "No data yet";

// share reads a rate as a person would say it. A rate of nothing has no value, and says so.
export function share(ratio: Ratio): string {
  if (ratio.value === null) {
    return noData;
  }
  return `${Math.round(ratio.value * 1000) / 10}% (${ratio.numerator} of ${ratio.denominator})`;
}

function perCompleted(ratio: Ratio): string {
  if (ratio.value === null) {
    return noData;
  }
  return `${Math.round(ratio.value * 100) / 100} (${ratio.numerator} over ${ratio.denominator})`;
}

function span(ms: number): string {
  const minutes = ms / 60000;
  if (minutes < 60) {
    return `${Math.round(minutes)}m`;
  }
  const hours = minutes / 60;
  return hours < 48 ? `${Math.round(hours * 10) / 10}h` : `${Math.round((hours / 24) * 10) / 10}d`;
}

function spread(d: Durations): string {
  if (d.median_ms === null || d.p95_ms === null) {
    return noData;
  }
  return `median ${span(d.median_ms)}, 95th percentile ${span(d.p95_ms)}, of ${d.count}`;
}

function dollars(value: number): string {
  return `$${value.toFixed(4)}`;
}

// Measures shows the product measures the ledger alone gives. The north star stands beside the
// overturn rate and rework, because interruptions alone reward an agent for never asking.
export function Measures({ report }: { report: MeasureReport }) {
  const kinds = report.interruptions_by_kind;
  const triggers = Object.entries(report.park_time).sort(([a], [b]) => a.localeCompare(b));
  const costs = Object.entries(report.cost_per_outcome).sort(([a], [b]) => a.localeCompare(b));
  return (
    <Section title="Product measures">
      <p className="quiet">From the ledger, over the last {report.window.name}. Work in progress is a count of now.</p>
      <div role="group" aria-label="The north star and its counterweights">
        <dl className="facts north-star">
          <Fact name="Interruptions per completed work order">{perCompleted(report.interruptions_per_completed)}</Fact>
          <Fact name="Overturn rate">{share(report.overturn_rate)}</Fact>
          <Fact name="Rework">{share(report.rework)}</Fact>
          <Fact name="Autonomous decision rate">{share(report.autonomous_decision_rate)}</Fact>
        </dl>
      </div>
      <p className="quiet">
        Fewer interruptions mean more autonomy only while the overturn rate and rework hold steady. A rising overturn rate means agents guess instead of
        asking, and rising rework means assumptions cost more than questions would have.
      </p>
      <dl className="facts">
        <Fact name="Interruptions">
          {report.interruptions}: {kinds.batch ?? 0} batches of questions, {kinds.approval ?? 0} approvals, {kinds.forced ?? 0} forced actions
        </Fact>
        <Fact name="Completed work orders">{report.completed}</Fact>
        <Fact name="Repeated-question rate">{share(report.repeated_question_rate)}</Fact>
        <Fact name="Repeats prevented">{report.prevented_repeats}</Fact>
        <Fact name="Work in progress">{report.work_in_progress}</Fact>
        <Fact name="Flow time, ready to done">{spread(report.ready_to_done)}</Fact>
        <Fact name="Flow time, intake to done">{spread(report.intake_to_done)}</Fact>
      </dl>
      <div className="tables">
        <table className="counts">
          <caption>Park time by trigger</caption>
          <tbody>
            {triggers.length ? (
              triggers.map(([trigger, durations]) => (
                <tr key={trigger}>
                  <th scope="row">{words(trigger)}</th>
                  <td>{spread(durations)}</td>
                </tr>
              ))
            ) : (
              <tr>
                <td>No wake fired.</td>
              </tr>
            )}
          </tbody>
        </table>
        <table className="counts">
          <caption>Cost per outcome</caption>
          <tbody>
            {costs.length ? (
              costs.map(([kind, cost]) => {
                const each = cost.per_done_usd === null ? noData : `${cost.lower_bound ? "at least " : ""}${dollars(cost.per_done_usd)} each`;
                return (
                  <tr key={kind}>
                    <th scope="row">{words(kind)}</th>
                    <td>
                      {each}, {dollars(cost.total_usd)} over {cost.done} done
                    </td>
                  </tr>
                );
              })
            ) : (
              <tr>
                <td>No lane reported a cost.</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <p className="quiet tables-note">Cost is what the agent reported, an estimate and a lower bound: a turn a kill stopped reports nothing.</p>
    </Section>
  );
}
