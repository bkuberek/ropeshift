import { Fragment, useId } from "react";

import { readPlan } from "../api/client";
import type { Contract, ContractHistory, PlanDetail, PlanReview, Plans as View, WorkOrder } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { approvalStates, workLabel } from "../floor/states";
import { Bar, Fact, Id, LongFact, plural, Section, words } from "../parts/parts";
import { Banner, EmptyState, StateBadge } from "../ui/display";
import { DifferenceBlock, DraftDecisions } from "./PlanParts";
import { StateCounts } from "./Roadmap";
import { StartSession } from "./StartSession";

// A work order's size says how much of the critical path it weighs.
const sizeWords = (size: string) => `Size ${size || "unknown"}`;

// Names reads what a contract names by ID as a person reads it: a milestone of the plan by its title, a
// contract of the plan by the newest title of the contract whose file has that ID, and the milestone a
// work order is in through its contract. A name the plan does not hold stays the ID it was written as.
type Names = { milestone: (id: string) => string; contract: (key: string) => string; milestoneOf: (order: WorkOrder) => string };

function namesOf(detail: PlanDetail): Names {
  const milestone = (id: string) => detail.milestones.find((row) => row.id === id)?.title || id;
  const newest = (history?: ContractHistory) => history?.versions.at(-1);
  return {
    milestone,
    contract: (key) => newest(detail.contracts.find((history) => history.key === key))?.title || key,
    milestoneOf: (order) => {
      const id = newest(detail.contracts.find((history) => history.id === order.contract_id))?.milestone;
      return id ? milestone(id) : "";
    },
  };
}

function WorkRows({ orders, names }: { orders: WorkOrder[]; names?: Names }) {
  return (
    <ol className="list-rows" role="list">
      {orders.map((order) => {
        const milestone = names?.milestoneOf(order);
        return (
          <li key={order.id} className="list-row">
            <p className="list-row-title">{order.title || order.id}</p>
            <ul className="chips">
              <li className="chip-strong">{workLabel(order.state)}</li>
              {order.size !== undefined ? <li>{sizeWords(order.size)}</li> : null}
              {milestone ? <li>In milestone {milestone}</li> : null}
              <li>
                <Id value={order.id} />
              </li>
            </ul>
          </li>
        );
      })}
    </ol>
  );
}

function countsOf(orders: WorkOrder[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const order of orders) {
    counts[order.state] = (counts[order.state] ?? 0) + 1;
  }
  return counts;
}

// versionWords says which version of a plan is in force and which one waits.
export function versionWords(detail: PlanDetail): string {
  const parts = [detail.approved_version ? `Version ${detail.approved_version} approved` : "No version approved yet"];
  if (detail.pending_version) {
    parts.push(`version ${detail.pending_version} waits`);
  }
  return parts.join(" · ");
}

function ReviewVersion({ detail }: { detail: PlanDetail }) {
  return detail.pending_version ? (
    <Link className="btn btn-sm" to={addressOf("/plan", { id: detail.plan.id, version: String(detail.pending_version) })}>
      Review version {detail.pending_version}
    </Link>
  ) : null;
}

function PlanLine({ detail }: { detail: PlanDetail }) {
  const counts = countsOf(detail.work_orders);
  const total = detail.work_orders.length;
  const done = counts.done ?? 0;
  return (
    <li className="list-row">
      <Link className="list-row-title" to={addressOf("/plans", { plan: detail.plan.id })}>
        {detail.plan.title}
      </Link>
      <ul className="chips">
        <li>{words(detail.plan.status)}</li>
        <li>{versionWords(detail)}</li>
        <li>{plural(detail.contracts.length, "contract")}</li>
        <li>{plural(detail.reviews.length, "review")}</li>
      </ul>
      {total ? (
        <div className="list-row-progress">
          <Bar done={done} total={total} label={`${detail.plan.title}: ${done} of ${total} done`} />
          <p className="muted">
            {done} of {total} done
          </p>
        </div>
      ) : (
        <p className="muted">No work yet</p>
      )}
      <StateCounts states={counts} />
      <ReviewVersion detail={detail} />
    </li>
  );
}

export function PlanList({ view, project }: { view: View; project: string }) {
  if (view.plans.length === 0 && view.unplanned.length === 0) {
    return (
      <div className="panel">
        <EmptyState icon="plan" title="No plans yet" action={<StartSession project={project} variant="primary" />}>
          A plan holds the contracts and work a goal needs. Start a planning session to write one.
        </EmptyState>
      </div>
    );
  }
  return (
    <>
      <ol className="list-rows" role="list">
        {view.plans.map((detail) => (
          <PlanLine key={detail.plan.id} detail={detail} />
        ))}
      </ol>
      {view.unplanned.length ? (
        <Section title="Work with no plan">
          <WorkRows orders={view.unplanned} />
        </Section>
      ) : null}
    </>
  );
}

// Requirements says which requirements a criterion serves, each linked to its page.
function Requirements({ keys }: { keys: string[] }) {
  return (
    <p className="muted">
      Serves{" "}
      {keys.map((key, at) => (
        <Fragment key={key}>
          {at ? ", " : null}
          <Link className="mono" to={addressOf("/documents", { kind: "requirements", key })}>
            {key}
          </Link>
        </Fragment>
      ))}
    </p>
  );
}

function Version({ contract, names }: { contract: Contract; names: Names }) {
  const heading = useId();
  return (
    <section className="version" aria-labelledby={heading}>
      <h4 className="version-title" id={heading}>
        Version {contract.version}
      </h4>
      {contract.statement ? <p className="statement">{contract.statement}</p> : null}
      {contract.criteria?.length ? (
        <ul className="criteria" aria-label="Acceptance criteria">
          {contract.criteria.map((criterion) => (
            <li key={criterion.id}>
              <p>
                <span className="mono">{criterion.id}</span> {criterion.text}
              </p>
              {criterion.requirements?.length ? <Requirements keys={criterion.requirements} /> : null}
            </li>
          ))}
        </ul>
      ) : null}
      <dl className="kv">
        <Fact name="Status">{words(contract.status)}</Fact>
        {contract.milestone ? <Fact name="Milestone">{names.milestone(contract.milestone)}</Fact> : null}
        {contract.shared ? <Fact name="Shared">Other contracts of this plan use it</Fact> : null}
        {contract.uses?.length ? <Fact name="Uses">{contract.uses.map(names.contract).join(", ")}</Fact> : null}
        {contract.requirements?.length ? (
          <Fact name="Requirements">
            <ul className="links">
              {contract.requirements.map((key) => (
                <li key={key}>
                  <Link className="mono" to={addressOf("/documents", { kind: "requirements", key })}>
                    {key}
                  </Link>
                </li>
              ))}
            </ul>
          </Fact>
        ) : null}
        {contract.decisions?.length ? (
          <Fact name="Decisions">
            <ul className="links">
              {contract.decisions.map((id) => (
                <li key={id}>
                  <Link className="mono" to={addressOf("/decisions", { id })}>
                    {id}
                  </Link>
                </li>
              ))}
            </ul>
          </Fact>
        ) : null}
      </dl>
      <dl className="facts-long">
        {contract.source ? <LongFact name="Source" value={contract.source} /> : null}
        <LongFact name="Digest" value={contract.digest} />
      </dl>
    </section>
  );
}

// A contract's work orders, each with its size, which is what the critical path weighs.
function ContractWork({ title, orders }: { title: string; orders: WorkOrder[] }) {
  if (orders.length === 0) {
    return null;
  }
  return (
    <ul className="contract-work" aria-label={`Work orders of ${title}`}>
      {orders.map((order) => (
        <li key={order.id}>
          <span className="contract-work-title">{order.title || order.id}</span>
          <ul className="chips">
            <li>{sizeWords(order.size ?? "")}</li>
          </ul>
        </li>
      ))}
    </ul>
  );
}

function History({ history, orders, names }: { history: ContractHistory; orders: WorkOrder[]; names: Names }) {
  const heading = useId();
  const [newest, ...older] = [...history.versions].reverse();
  if (!newest) {
    return null;
  }
  return (
    <article className="panel" aria-labelledby={heading}>
      <h3 className="panel-title" id={heading}>
        {newest.title}
      </h3>
      <Version contract={newest} names={names} />
      <ContractWork title={newest.title} orders={orders.filter((order) => order.contract_id === history.id)} />
      {older.length ? (
        <details className="older">
          <summary>{plural(older.length, "earlier version")}</summary>
          {older.map((contract) => (
            <Version key={contract.version} contract={contract} names={names} />
          ))}
        </details>
      ) : null}
    </article>
  );
}

function Review({ review }: { review: PlanReview }) {
  const heading = useId();
  const order = review.work_order;
  return (
    <article className="panel" aria-labelledby={heading}>
      <h3 className="panel-title" id={heading}>
        {order.title || order.id}
      </h3>
      <div className="panel-body">
        <dl className="kv">
          <Fact name="State">{workLabel(order.state)}</Fact>
          <Fact name="Read" mono>
            {order.review_pins?.join(", ") || "Nothing recorded"}
          </Fact>
        </dl>
        {review.open_questions.length ? (
          <ul className="links links-stacked" aria-label="Left open">
            {review.open_questions.map((question) => (
              <li key={question.id}>
                <Link to={addressOf("/questions", { id: question.id })}>{question.text}</Link>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">It left nothing open.</p>
        )}
      </div>
    </article>
  );
}

// PlanVersions reads the plan beside the plans view, which names its versions with where each request stands,
// how the newest version differs from the one a member approved, and the answers it drafts.
function PlanVersions({ detail, live }: { detail: PlanDetail; live: Live }) {
  const read = useView(() => readPlan(detail.plan.id), `plan:${detail.plan.id}`, live);
  const view = read.view;
  const approval = view?.approval;
  return (
    <>
      {read.failure ? <Banner tone="danger">{read.failure}</Banner> : null}
      {view && view.versions.length ? (
        <Section title="Versions">
          <ol className="list-rows" aria-label="Versions of the plan">
            {view.versions.map((line) => (
              <li key={line.version} className="list-row row">
                <Link to={addressOf("/plan", { id: detail.plan.id, version: String(line.version) })}>Version {line.version}</Link>
                {line.state ? <StateBadge table={approvalStates} state={line.state} /> : <span className="unavailable">no request</span>}
              </li>
            ))}
          </ol>
        </Section>
      ) : null}
      {approval ? (
        <>
          <Section title={`Version ${approval.approval.plan_version}: difference from ${approval.detail.difference?.base ? `version ${approval.detail.difference.base}` : "the approved version"}`}>
            <DifferenceBlock difference={approval.detail.difference ?? {}} live={live} />
          </Section>
          <Section title="Draft decisions">
            <DraftDecisions drafts={approval.drafts ?? []} proposedBy={approval.approval.requested_by} />
          </Section>
        </>
      ) : null}
    </>
  );
}

export function PlanPage({ detail, live }: { detail: PlanDetail; live: Live }) {
  const names = namesOf(detail);
  return (
    <>
      <p className="back">
        <Link to="/plans">All plans</Link>
      </p>
      <dl className="kv facts-page">
        <Fact name="Status">{words(detail.plan.status)}</Fact>
        <Fact name="Version">{versionWords(detail)}</Fact>
        {detail.goal ? <Fact name="Goal">{detail.goal.title}</Fact> : null}
      </dl>
      {detail.plan.source ? (
        <dl className="facts-long">
          <LongFact name="Source" value={detail.plan.source} />
        </dl>
      ) : null}
      {detail.pending_version ? (
        <Banner tone="warning" action={<ReviewVersion detail={detail} />}>
          <strong>Version {detail.pending_version} waits for your approval.</strong> It also waits in <Link to="/needs-you?kind=decisions">Needs you</Link>.
        </Banner>
      ) : null}
      <PlanVersions detail={detail} live={live} />
      <Section title="Contracts">
        {detail.contracts.length ? (
          detail.contracts.map((history) => <History key={history.id} history={history} orders={detail.work_orders} names={names} />)
        ) : (
          <p className="muted">This plan holds no contract yet.</p>
        )}
      </Section>
      <Section title="Reviews">
        {detail.reviews.length ? (
          detail.reviews.map((review) => <Review key={review.work_order.id} review={review} />)
        ) : (
          <p className="muted">No review has run.</p>
        )}
      </Section>
      <Section title="Work">
        {detail.work_orders.length ? <WorkRows orders={detail.work_orders} names={names} /> : <p className="muted">This plan holds no work yet.</p>}
      </Section>
    </>
  );
}
