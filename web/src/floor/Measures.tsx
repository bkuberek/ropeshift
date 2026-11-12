import type { Agreement, AgreementPeriods, Durations, MeasureReport, Ratio } from "../api/types";
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

// dollars says money in dollars and cents. The API keeps every digit, and a cost too small to show in
// cents says so rather than reading as nothing.
function dollars(value: number): string {
  return value > 0 && value < 0.005 ? "under $0.01" : `$${value.toFixed(2)}`;
}

const counted = (amount: number, one: string, many: string) => `${amount} ${amount === 1 ? one : many}`;

// levelWords says what each merge authority level leaves to the person, in words, so the level never rests on
// a colour or an identifier alone.
const levelWords: Record<string, string> = {
  person_merges: "person merges, you decide every change after the review",
  person_approves: "person approves, you approve each change and ropeshift lands it",
  review_approves: "review approves, the review's approval stands for yours once every criterion passed",
};

// twice says one figure over both periods of review agreement.
function twice(last: string, all: string): string {
  return `${last} in the last 30 days, ${all} over all time`;
}

function agreedWith(a: Agreement): string {
  return a.agreement_rate.value === null ? "none to compare" : share(a.agreement_rate);
}

// overturnedIn says how many approvals the person overturned in one period, and how many of them were a defect
// found after the work was accepted.
function overturnedIn(a: Agreement, period: string): string {
  return `${a.approvals_overturned} ${period}${a.later_defects ? ` (${a.later_defects} of them a later defect)` : ""}`;
}

// AgreementFacts reads one scope's agreement over both periods. Nothing here changes a merge authority level.
function AgreementFacts({ periods }: { periods: AgreementPeriods }) {
  const { last_30_days: last, all_time: all } = periods;
  if (all.decisions === 0) {
    return <p className="muted">No decision after a review yet.</p>;
  }
  return (
    <dl className="kv">
      <Fact name="Decisions after a review">{twice(`${last.decisions}`, `${all.decisions}`)}</Fact>
      <Fact name="You agreed with the review">{twice(agreedWith(last), agreedWith(all))}</Fact>
      <Fact name="Approvals you overturned">
        {overturnedIn(last, "in the last 30 days")}, {overturnedIn(all, "over all time")}
      </Fact>
      <Fact name="Failures you overrode">{twice(`${last.failures_overridden}`, `${all.failures_overridden}`)}</Fact>
    </dl>
  );
}

// ReviewAgreement shows how often the person agreed with the review agent, for the workspace and for each
// repository, beside the merge authority level in force there. It is the record a person reads to decide
// when to raise a level, and it never raises one.
export function ReviewAgreement({ report }: { report: MeasureReport }) {
  const { workspace, repositories } = report.review_agreement;
  return (
    <Section title="Review agreement">
      <p className="muted">
        How often you agreed with the review agent after it judged work, over the last 30 days and over all time. It is evidence for choosing a merge
        authority level, and ropeshift never changes a level itself.
      </p>
      <div role="group" aria-label="Review agreement for the whole workspace" className="agreement">
        <h3 className="agreement-title">The whole workspace</h3>
        <AgreementFacts periods={workspace} />
      </div>
      {repositories.map((repository) => (
        <div key={repository.repository_id} role="group" aria-label={`Review agreement for ${repository.repository_id}`} className="agreement">
          <h3 className="agreement-title mono">{repository.repository_id}</h3>
          <p>Merge authority: {levelWords[repository.merge_authority] ?? words(repository.merge_authority).toLowerCase()}</p>
          <AgreementFacts periods={repository} />
        </div>
      ))}
    </Section>
  );
}

// Measures shows the product measures the ledger alone gives. The north star stands beside the
// overturn rate and rework, because interruptions alone reward an agent for never asking.
export function Measures({ report }: { report: MeasureReport }) {
  const kinds = report.interruptions_by_kind;
  const triggers = Object.entries(report.park_time).sort(([a], [b]) => a.localeCompare(b));
  const costs = Object.entries(report.cost_per_outcome).sort(([a], [b]) => a.localeCompare(b));
  return (
    <Section title="Product measures">
      <p className="muted">From the ledger, over the last {report.window.name}. Work in progress is a count of now.</p>
      <div role="group" aria-label="The north star and its counterweights">
        <dl className="kv north-star">
          <Fact name="Interruptions per completed work order">{perCompleted(report.interruptions_per_completed)}</Fact>
          <Fact name="Overturn rate">{share(report.overturn_rate)}</Fact>
          <Fact name="Rework">{share(report.rework)}</Fact>
          <Fact name="Autonomous decision rate">{share(report.autonomous_decision_rate)}</Fact>
        </dl>
      </div>
      <p className="muted">
        Fewer interruptions mean more autonomy only while the overturn rate and rework hold steady. A rising overturn rate means agents guess instead of
        asking, and rising rework means assumptions cost more than questions would have.
      </p>
      <dl className="kv">
        <Fact name="Interruptions">
          {counted(kinds.batch ?? 0, "batch of questions", "batches of questions")}, {counted(kinds.approval ?? 0, "approval", "approvals")},{" "}
          {counted(kinds.forced ?? 0, "forced action", "forced actions")}
        </Fact>
        <Fact name="Completed work orders">{report.completed}</Fact>
        <Fact name="Repeated-question rate">{share(report.repeated_question_rate)}</Fact>
        <Fact name="Repeats prevented">{report.prevented_repeats}</Fact>
        <Fact name="Work in progress">{report.work_in_progress}</Fact>
        <Fact name="Flow time, ready to done">{spread(report.ready_to_done)}</Fact>
        <Fact name="Flow time, intake to done">{spread(report.intake_to_done)}</Fact>
      </dl>
      <div className="tables">
        <table className="table counts">
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
        <table className="table counts">
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
      <p className="muted tables-note">Cost is what the agent reported, an estimate and a lower bound: a turn a kill stopped reports nothing.</p>
    </Section>
  );
}
