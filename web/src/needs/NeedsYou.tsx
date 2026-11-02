import { useEffect, useId, useRef, useState } from "react";

import type { NeedItem, NeedsYou as View } from "../api/types";
import { ApprovalCard } from "./ApprovalCard";
import { EffectCard } from "./EffectCard";
import { QuestionBatch } from "./QuestionBatch";
import { waitLabel, waitWords } from "./wait";
import { WorkCard } from "./WorkCard";

const clockMs = 30_000;

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
  }
}

const workLabels: Record<string, string> = {
  acceptance: "Accept",
  blocked: "Blocked",
  human_resume: "Parked",
  lapsed: "Lapsed",
  long_parked: "Long parked",
  stalled: "Stalled",
  tripped: "Tripped",
};

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
  }
}

function Waiting({ item, waitedMs, after }: { item: NeedItem; waitedMs: number; after: () => Promise<void> }) {
  const heading = useId();
  return (
    <li className="need">
      <article aria-labelledby={heading}>
        <div className="wait">
          <span aria-hidden="true" className="wait-time">
            {waitLabel(waitedMs)}
          </span>
          <span className="visually-hidden">{waitWords(waitedMs)}</span>
          <span className="wait-kind">{kindOf(item)}</span>
        </div>
        <div className="card">
          <h2 className="card-title" id={heading}>
            {headingOf(item)}
          </h2>
          <Card item={item} after={after} />
        </div>
      </article>
    </li>
  );
}

// A wait runs on between reads, so the page counts on from the one the server stated.
function useClock(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), clockMs);
    return () => clearInterval(timer);
  }, []);
  return now;
}

// The order is the server's: the longest wait first.
export function NeedsYou({ view, readAt, after }: { view: View; readAt: number; after: () => Promise<void> }) {
  const now = useClock();
  const elapsed = Math.max(0, now - readAt);
  const count = view.items.length;
  const title = useRef<HTMLHeadingElement>(null);
  const shown = useRef(count);

  // An answered item leaves with the button that answered it, so keyboard focus would fall out of the
  // page. It lands on the title, and the next item is one tab away. A page that just opened keeps its
  // own start, so the skip link stays the first stop.
  useEffect(() => {
    if (count < shown.current && document.activeElement === document.body) {
      title.current?.focus();
    }
    shown.current = count;
  }, [count]);

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
        <ol className="queue" role="list">
          {view.items.map((item) => (
            <Waiting key={`${item.kind}:${item.id}`} item={item} waitedMs={view.as_of - item.since + elapsed} after={after} />
          ))}
        </ol>
      )}
    </>
  );
}
