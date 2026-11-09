import { useId } from "react";

import type { Contract, ContractHistory, PlanDetail, PlanReview, Plans as View, WorkOrder } from "../api/types";
import { addressOf, Link } from "../app/route";
import { workLabel } from "../floor/states";
import { Fact, Id, Section, words } from "../parts/parts";
import { StateCounts } from "./Roadmap";

const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;

function WorkRows({ orders }: { orders: WorkOrder[] }) {
  return (
    <ol className="rows" role="list">
      {orders.map((order) => (
        <li key={order.id} className="row">
          <p className="row-title">{order.title || order.id}</p>
          <ul className="tags">
            <li className="tag-strong">{workLabel(order.state)}</li>
            <li>
              <Id value={order.id} />
            </li>
          </ul>
        </li>
      ))}
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
    <li className="row">
      <Link className="row-title" to={addressOf("/plans", { plan: detail.plan.id })}>
        {detail.plan.title}
      </Link>
      <ul className="tags">
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
      <ol className="rows" role="list">
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

function Version({ contract }: { contract: Contract }) {
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
              <span className="mono">{criterion.id}</span> {criterion.text}
            </li>
          ))}
        </ul>
      ) : null}
      <dl className="facts">
        <Fact name="Status">{words(contract.status)}</Fact>
        <Fact name="Digest" mono>
          {contract.digest}
        </Fact>
        {contract.source ? (
          <Fact name="Source" mono>
            {contract.source}
          </Fact>
        ) : null}
        {contract.requirements?.length ? (
          <Fact name="Requirements">
            <ul className="links">
              {contract.requirements.map((key) => (
                <li key={key}>
                  <Link className="mono" to={addressOf("/requirements", { key })}>
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
    </section>
  );
}

function History({ history }: { history: ContractHistory }) {
  const heading = useId();
  const [newest, ...older] = [...history.versions].reverse();
  if (!newest) {
    return null;
  }
  return (
    <article className="card" aria-labelledby={heading}>
      <h3 className="card-title" id={heading}>
        {newest.title}
      </h3>
      <Version contract={newest} />
      {older.length ? (
        <details className="older">
          <summary>{plural(older.length, "earlier version")}</summary>
          {older.map((contract) => (
            <Version key={contract.version} contract={contract} />
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
    <article className="card" aria-labelledby={heading}>
      <h3 className="card-title" id={heading}>
        {order.title || order.id}
      </h3>
      <div className="card-body">
        <dl className="facts">
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
          <p className="quiet">It left nothing open.</p>
        )}
      </div>
    </article>
  );
}

export function PlanPage({ detail }: { detail: PlanDetail }) {
  return (
    <>
      <p className="back">
        <Link to="/plans">All plans</Link>
      </p>
      <dl className="facts facts-page">
        <Fact name="Status">{words(detail.plan.status)}</Fact>
        {detail.goal ? <Fact name="Goal">{detail.goal.title}</Fact> : null}
        {detail.plan.source ? (
          <Fact name="Source" mono>
            {detail.plan.source}
          </Fact>
        ) : null}
      </dl>
      <Section title="Contracts">
        {detail.contracts.length ? (
          detail.contracts.map((history) => <History key={history.id} history={history} />)
        ) : (
          <p className="quiet">This plan holds no contract yet.</p>
        )}
      </Section>
      <Section title="Reviews">
        {detail.reviews.length ? (
          detail.reviews.map((review) => <Review key={review.work_order.id} review={review} />)
        ) : (
          <p className="quiet">No review has run.</p>
        )}
      </Section>
      <Section title="Work">
        {detail.work_orders.length ? <WorkRows orders={detail.work_orders} /> : <p className="quiet">This plan holds no work yet.</p>}
      </Section>
    </>
  );
}
