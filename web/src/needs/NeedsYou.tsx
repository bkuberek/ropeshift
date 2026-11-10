import { useEffect, useRef } from "react";

import type { NeedItem, NeedsYou as View } from "../api/types";
import { useArrivalFocus } from "../app/route";
import { Line, useClock, ViewTime } from "../parts/parts";
import { ApprovalCard } from "./ApprovalCard";
import { ContractReviewCard } from "./ContractReviewCard";
import { EffectCard } from "./EffectCard";
import { PlanApprovalCard } from "./PlanApprovalCard";
import { QuestionBatch } from "./QuestionBatch";
import { waitLabel, waitWords } from "./wait";
import { WorkCard, workLabels } from "./WorkCard";

function headingOf(item: NeedItem): string {
  switch (item.kind) {
    case "questions": {
      const count = item.batch.items.length;
      return count === 1 ? "A question" : `${count} questions`;
    }
    case "approval":
      return `Allow ${item.approval.subject}?`;
    case "effect":
      return "Did it happen?";
    case "work":
      return item.work.work_order?.title || item.work.lane?.id || item.id;
    case "plan_approval":
      return `Approve ${item.plan_approval.title}, version ${item.plan_approval.version}?`;
    case "contract_review":
      return item.contract_review.work_order.title || item.id;
  }
}

function kindOf(item: NeedItem): string {
  switch (item.kind) {
    case "questions":
      return "Questions";
    case "approval":
      return "Approval";
    case "effect":
      return "Unknown effect";
    case "work":
      return workLabels[item.work.reason] ?? item.work.reason;
    case "plan_approval":
      return "Plan approval";
    case "contract_review":
      return "Contract review";
  }
}

function Card({ item, after }: { item: NeedItem; after: () => Promise<void> }) {
  switch (item.kind) {
    case "questions":
      return <QuestionBatch batch={item.batch} after={after} />;
    case "approval":
      return <ApprovalCard approval={item.approval} after={after} />;
    case "effect":
      return <EffectCard effect={item.effect} after={after} />;
    case "work":
      return <WorkCard id={item.id} work={item.work} after={after} />;
    case "plan_approval":
      return <PlanApprovalCard id={item.id} need={item.plan_approval} after={after} />;
    case "contract_review":
      return <ContractReviewCard review={item.contract_review} after={after} />;
  }
}

function Waiting({ item, waitedMs, after }: { item: NeedItem; waitedMs: number; after: () => Promise<void> }) {
  const side = (
    <>
      <span aria-hidden="true" className="wait-time">
        {waitLabel(waitedMs)}
      </span>
      <span className="visually-hidden">{waitWords(waitedMs)}</span>
    </>
  );
  return (
    <Line side={side} kind={kindOf(item)} title={headingOf(item)}>
      <Card item={item} after={after} />
    </Line>
  );
}

// The order is the server's: the longest wait first. A wait runs on between reads, so the page counts
// on from the one the server stated.
export function NeedsYou({ view, readAt, after, active = true }: { view: View; readAt: number; after: () => Promise<void>; active?: boolean }) {
  const now = useClock();
  const elapsed = Math.max(0, now - readAt);
  const count = view.items.length;
  const title = useRef<HTMLHeadingElement>(null);
  const shown = useRef(count);
  useArrivalFocus(title, active);

  // An answered item leaves with the button that answered it, so keyboard focus would fall out of the
  // page. It lands on the title, and the next item is one tab away. A page that just opened keeps its
  // own start, so the skip link stays the first stop.
  useEffect(() => {
    if (active && count < shown.current && document.activeElement === document.body) {
      title.current?.focus();
    }
    shown.current = count;
  }, [count, active]);

  return (
    <>
      <div className="summary">
        <h1 className="title" ref={title} tabIndex={-1}>
          Needs you
        </h1>
        <p className="count" aria-live="polite">
          {count === 0 ? "Nothing is waiting." : `${count} waiting`}
        </p>
      </div>
      {count === 0 ? (
        <p className="empty">Every lane has what it needs from you.</p>
      ) : (
        <ViewTime.Provider value={{ asOf: view.as_of, elapsed }}>
          <ol className="queue" role="list">
            {view.items.map((item) => (
              <Waiting key={`${item.kind}:${item.id}`} item={item} waitedMs={view.as_of - item.since + elapsed} after={after} />
            ))}
          </ol>
        </ViewTime.Provider>
      )}
    </>
  );
}
