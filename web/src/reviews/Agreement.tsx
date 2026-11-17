import type { Agreement as AgreementPeriod, AgreementPeriods, MeasureReport } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Fact, Facts, Panel } from "../ui/display";
import { agreementOf } from "./inReview";

// What each merge authority level leaves to a member, as the repository's record names it.
const levelWords: Record<string, string> = {
  person_merges: "you decide every change after the review",
  person_approves: "you approve each change and ropeshift lands it",
  review_approves: "the review's approval stands for yours once every criterion passed",
};

// Period is one span of review agreement. A rate over fewer than ten decisions says little, so it shows the counts
// and says there are too few to rate.
function Period({ name, period }: { name: string; period: AgreementPeriod }) {
  const words = agreementOf(period);
  return (
    <div role="group" aria-label={name} className="agreement">
      <h4 className="agreement-title">{name}</h4>
      <Facts>
        <Fact name="Decisions">{words.decisions}</Fact>
        <Fact name="You agreed with the review">{words.rate}</Fact>
        <Fact name="Agreed and disagreed">
          {words.agreed} and {words.disagreed}
          {words.unknown ? `, ${words.unknown} the review could not judge` : ""}
        </Fact>
        <Fact name="Approved, then rejected">{words.approvedThenRejected}</Fact>
        <Fact name="Rejected, then approved">{words.rejectedThenApproved}</Fact>
        <Fact name="Later defects">{words.laterDefects}</Fact>
      </Facts>
    </div>
  );
}

function Periods({ label, title, periods, level }: { label: string; title: string; periods: AgreementPeriods; level?: string }) {
  return (
    <Panel title={title} level={3}>
      <div role="group" aria-label={label} className="stack">
        {level ? (
          <p>
            Merge authority in force: <span className="mono">{level}</span>
            {levelWords[level] ? `, ${levelWords[level]}` : ""}
          </p>
        ) : null}
        {periods.all_time.decisions === 0 ? (
          <p className="muted">No decision has followed a review yet.</p>
        ) : (
          <>
            <Period name="Last 30 days" period={periods.last_30_days} />
            <Period name="All time" period={periods.all_time} />
          </>
        )}
      </div>
    </Panel>
  );
}

// Agreement shows how often the member agreed with the review agent after it judged work, for the project and for
// each repository, beside the merge authority level in force there (FR-CAL-2). It is the record a member reads to
// decide when to raise a level, and it raises none (FR-CAL-3). The repositories go by the name it shows, then by
// ID, because the ledger orders them by ID, which a new installation draws at random.
export function Agreement({ report, repositories }: { report: MeasureReport; repositories: Map<string, string> }) {
  const { workspace } = report.review_agreement;
  const nameOf = (id: string) => repositories.get(id) ?? id;
  const each = [...report.review_agreement.repositories].sort(
    (a, b) => nameOf(a.repository_id).localeCompare(nameOf(b.repository_id)) || (a.repository_id < b.repository_id ? -1 : a.repository_id > b.repository_id ? 1 : 0),
  );
  return (
    <div className="stack">
      <p className="muted">
        How often you agreed with the review agent after it judged work. ropeshift never changes a merge authority level by itself: you change it in{" "}
        <Link to={addressOf("/repositories")}>Repositories</Link>, and this record is the evidence for doing so.
      </p>
      <Periods label="Review agreement of this project" title="This project" periods={workspace} />
      {each.map((row) => (
        <Periods
          key={row.repository_id}
          label={`Review agreement of ${nameOf(row.repository_id)}`}
          title={nameOf(row.repository_id)}
          periods={row}
          level={row.merge_authority}
        />
      ))}
    </div>
  );
}
