import { Fragment, useId } from "react";

import type { Contract, ContractHistory, PlanDetail, PlanReview, Plans as View, WorkOrder } from "../api/types";
import { addressOf, Link } from "../app/route";
import { workLabel } from "../floor/states";
import { Fact, Id, LongFact, plural, Section, words } from "../parts/parts";
import { StateCounts } from "./Roadmap";

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

function PlanLine({ detail }: { detail: PlanDetail }) {
  return (
    <li className="list-row">
      <Link className="list-row-title" to={addressOf("/plans", { plan: detail.plan.id })}>
        {detail.plan.title}
      </Link>
      <ul className="chips">
        <li>{words(detail.plan.status)}</li>
        <li>{plural(detail.contracts.length, "contract")}</li>
        <li>{plural(detail.reviews.length, "review")}</li>
      </ul>
      <StateCounts states={countsOf(detail.work_orders)} />
    </li>
  );
}

export function PlanList({ view }: { view: View }) {
  if (view.plans.length === 0 && view.unplanned.length === 0) {
    return <p className="empty">No plans yet.</p>;
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

export function PlanPage({ detail }: { detail: PlanDetail }) {
  const names = namesOf(detail);
  return (
    <>
      <p className="back">
        <Link to="/plans">All plans</Link>
      </p>
      <dl className="kv facts-page">
        <Fact name="Status">{words(detail.plan.status)}</Fact>
        {detail.goal ? <Fact name="Goal">{detail.goal.title}</Fact> : null}
      </dl>
      {detail.plan.source ? (
        <dl className="facts-long">
          <LongFact name="Source" value={detail.plan.source} />
        </dl>
      ) : null}
      {detail.pending_version ? (
        <p className="plan-waiting">
          Version {detail.pending_version} waits for approval in <Link to="/needs-you?kind=decisions">Needs you</Link>.
        </p>
      ) : null}
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
