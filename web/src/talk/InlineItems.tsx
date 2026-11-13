import type { ReactNode } from "react";

import type { Approval, Batch, CardDetail, CardNeed } from "../api/types";
import type { TalkEntry, TalkQuestion } from "../api/talk";
import { approvalStates } from "../floor/states";
import type { StateLook } from "../floor/states";
import { ApprovalCard } from "../needs/ApprovalCard";
import { ConfirmCard, voidAsk } from "../needs/ConfirmCard";
import type { Frame } from "../needs/frame";
import { kindOf } from "../needs/kinds";
import { QuestionBatch } from "../needs/QuestionBatch";
import { recoveryHeading } from "../needs/recovery";
import { RecoveryCard } from "../needs/RecoveryCard";
import { DecisionCard } from "../ui/DecisionCard";
import { Resolved } from "../ui/resolution";
import { useTalk } from "./state";

// The records a session asks a member to decide appear inline in the conversation with it, as the same
// records and the same cards Needs you shows (FR-CHAT-7, FR-CHAT-13): deciding one here resolves both, and
// each copy shows how it came out, by whom, where, and when.

const questionStates: Record<string, StateLook> = {
  answered: { label: "Answered", tone: "success", glyph: "check" },
  dismissed: { label: "Dismissed", tone: "neutral", glyph: "slash" },
};

// detailOfCard reads a card's detail, which the approval keeps as JSON. A card whose detail cannot be read is
// not shown as a card that could be confirmed.
export function detailOfCard(approval: Approval): CardDetail | null {
  try {
    const detail = JSON.parse(approval.detail ?? "") as CardDetail;
    return detail && typeof detail.operation === "string" ? detail : null;
  } catch {
    return null;
  }
}

type ItemProps = {
  entry: TalkEntry;
  // member is the name of the member the assistant acts for, and conversation the one the card came from.
  member: string;
  conversation?: { id: string; name: string };
  after: () => Promise<void>;
};

function Unreadable({ title }: { title: string }) {
  return (
    <DecisionCard compact kind={{ icon: "card", name: "Confirmation card" }} title={title}>
      <p className="small muted">This card's detail could not be read, so it cannot be confirmed here. It stays in Needs you.</p>
    </DecisionCard>
  );
}

function CardItem({ entry, member, conversation, after }: ItemProps) {
  const talk = useTalk();
  const approval = entry.approval;
  if (!approval) {
    return <Unreadable title={entry.action?.label ?? "A card"} />;
  }
  const detail = detailOfCard(approval);
  if (!detail) {
    return <Unreadable title={approval.summary} />;
  }
  // A card in a conversation is the assistant's, which its provenance says with the assistant's chip.
  const need: CardNeed = { approval, detail, label: "assistant", member, conversation: { id: conversation?.id ?? "", name: conversation?.name ?? "" } };
  const frame: Frame = { title: approval.summary };
  return (
    <ConfirmCard
      compact
      place="conversation"
      need={need}
      frame={frame}
      after={after}
      askAgain={conversation ? () => talk.askAgain(conversation.id, voidAsk(approval.summary)) : undefined}
    />
  );
}

function RequestItem({ entry, after }: ItemProps) {
  const approval = entry.approval;
  if (!approval) {
    return null;
  }
  const recovery = approval.kind === "recovery";
  const frame: Frame = { title: recovery ? recoveryHeading(approval) : `Allow ${approval.subject}?`, provenance: approval.requested_by };
  if (approval.state === "pending") {
    return recovery ? (
      <RecoveryCard compact place="conversation" approval={approval} after={after} frame={frame} />
    ) : (
      <ApprovalCard compact place="conversation" approval={approval} after={after} frame={frame} />
    );
  }
  return (
    <DecisionCard
      compact
      kind={kindOf({ kind: "approval", approval, id: approval.id, since: 0 })}
      title={frame.title}
      provenance={frame.provenance}
      resolution={
        <div className="resolved-note" role="status">
          <Resolved table={approvalStates} state={approval.state} by={approval.decided_by} channel={approval.channel} at={approval.decided_at} />
        </div>
      }
    />
  );
}

// batchOf makes the batch a question reaches a person in, which the question form reads.
function batchOf({ question, batch }: TalkQuestion): Batch {
  return {
    batch: { id: batch || question.id, status: question.status, opened_at: 0, closes_at: 0 },
    items: [
      {
        id: question.id,
        text: question.text,
        categories: question.categories,
        blocking: question.blocking,
        options: question.options,
        recommendation: question.recommendation,
        escalation_reason: question.escalation_reason,
      },
    ],
  };
}

function QuestionItem({ entry, after }: ItemProps) {
  const held = entry.question;
  if (!held) {
    return null;
  }
  const frame: Frame = { title: "A question", provenance: held.question.asked_by };
  if (held.question.status === "needs_human") {
    return <QuestionBatch compact place="conversation" batch={batchOf(held)} after={after} frame={frame} />;
  }
  const settled = held.question.status in questionStates;
  return (
    <DecisionCard
      compact
      kind={{ icon: "question", name: "Question" }}
      title={held.question.text}
      provenance={frame.provenance}
      resolution={
        <div className="resolved-note" role="status">
          {settled ? <Resolved table={questionStates} state={held.question.status} channel={held.channel} /> : <span className="small muted">Not with you yet: it is still being worked.</span>}
        </div>
      }
    />
  );
}

// InlineItem shows one record of a stream that asks a member for a decision: a card, an approval request,
// or a question.
export function InlineItem(props: ItemProps): ReactNode {
  switch (props.entry.kind) {
    case "card":
      return <CardItem {...props} />;
    case "request":
      return <RequestItem {...props} />;
    case "question":
      return <QuestionItem {...props} />;
  }
  return null;
}

// openItems are the requests and questions of a stream that still wait on a member: what a strip above a
// live pane shows, because the pane cannot hold an inline item.
export function openItems(entries: TalkEntry[]): TalkEntry[] {
  return entries.filter((entry) => {
    if (entry.kind === "request") {
      return entry.approval?.state === "pending";
    }
    if (entry.kind === "question") {
      return entry.question?.question.status === "needs_human";
    }
    return false;
  });
}
