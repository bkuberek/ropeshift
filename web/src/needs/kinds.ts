import type { Approval, CIKind, NeedItem } from "../api/types";
import type { IconName } from "../ui/Icon";
import { ciKinds } from "./ciWords";
import { isHeld, mergeContext, mergeHeading } from "./merge";
import { recoveryHeading } from "./recovery";
import { money, spendKind } from "./spendWords";

// The filters of Needs you, each a family of kinds.
export type NeedFilter = "all" | "decisions" | "approvals" | "cards" | "stuck";

export const filters: { id: NeedFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "decisions", label: "Decisions" },
  { id: "approvals", label: "Approvals" },
  { id: "cards", label: "Cards" },
  { id: "stuck", label: "Stuck" },
];

// filterOf is the family an item belongs to. A confirmation card a delegate prepared is a family of its own.
export function filterOf(item: NeedItem): Exclude<NeedFilter, "all"> {
  switch (item.kind) {
    case "confirm":
    case "repository_creation":
      return "cards";
    case "questions":
    case "plan_approval":
    case "plan_revision":
    case "contract_review":
    case "effect":
      return "decisions";
    case "approval":
      return "approvals";
    case "work":
      return item.work.reason === "acceptance" ? "approvals" : "stuck";
    case "ci":
    case "spend":
      return "stuck";
  }
}

// keyOf names an item in an address: its kind and its ID.
export function keyOf(item: NeedItem): string {
  return `${item.kind}:${item.id}`;
}

export const workLabels: Record<string, string> = {
  acceptance: "Waiting for acceptance",
  blocked: "Blocked",
  failed: "Failed",
  history_missing: "Design history missing",
  history_unread: "Design history unchecked",
  human_resume: "Parked",
  lapsed: "Lapsed",
  long_parked: "Long parked",
  stalled: "Stalled",
  tripped: "Tripped",
  worker_behind: "Waiting for upgrade",
};

const workIcons: Record<string, IconName> = {
  acceptance: "review",
  blocked: "warning",
  failed: "warning",
  history_missing: "warning",
  history_unread: "warning",
  human_resume: "pause",
  lapsed: "clock",
  long_parked: "pause",
  stalled: "warning",
  tripped: "power",
  worker_behind: "warning",
};

const fixedCards = ["questions", "recovery", "merge", "held-merge", "tool-call", "effect", "orchestrator", "plan-approval", "plan-revision", "contract-review", "confirm", "spend", "repository-creation"] as const;

// CardKind names a kind of card by what its overline says: one for each kind of item, a request by what it asks, and
// work by why it waits.
export type CardKind = (typeof fixedCards)[number] | `work-${string}` | `ci-${CIKind}`;

// ciKindNames is every kind of thing CI asks a person about, in the order a surface shows them.
export const ciKindNames: CIKind[] = ["ci_not_finished", "ci_queue_stalled", "ci_infrastructure_failure", "ci_base_red", "ci_base_unread", "ci_action_required"];

// cardKinds is every kind of card Needs you draws. The accessibility checks hold each to a state.
export const cardKinds: CardKind[] = [
  ...fixedCards,
  ...Object.keys(workLabels).map((reason): CardKind => `work-${reason}`),
  ...ciKindNames.map((kind): CardKind => `ci-${kind}`),
];

// cardKindOf is the kind of card an item draws.
export function cardKindOf(item: NeedItem): CardKind {
  switch (item.kind) {
    case "questions":
      return "questions";
    case "approval":
      return item.approval.kind === "recovery" ? "recovery" : item.approval.kind === "merge" ? (isHeld(item.approval) ? "held-merge" : "merge") : "tool-call";
    case "effect":
      return "effect";
    case "work":
      return item.work.orchestrator ? "orchestrator" : `work-${item.work.reason}`;
    case "plan_approval":
      return "plan-approval";
    case "plan_revision":
      return "plan-revision";
    case "contract_review":
      return "contract-review";
    case "confirm":
      return "confirm";
    case "ci":
      return `ci-${item.ci.kind}`;
    case "spend":
      return "spend";
    case "repository_creation":
      return "repository-creation";
  }
}

// approvalKind is the overline of a request's card: a recovery act, a merge, or a call a lane asked to run.
export function approvalKind(approval: Approval): { icon: IconName; name: string } {
  switch (approval.kind) {
    case "recovery":
      return { icon: "restart", name: "Recovery request" };
    case "merge":
      return isHeld(approval) ? { icon: "warning", name: "Held merge" } : { icon: "pr", name: "Merge approval" };
    default:
      return { icon: "terminal", name: "Approval request" };
  }
}

// approvalHeading says what a request asks, as a person reads it.
export function approvalHeading(approval: Approval): string {
  switch (approval.kind) {
    case "recovery":
      return recoveryHeading(approval);
    case "merge":
      return mergeHeading(approval);
    default:
      return `Allow ${approval.subject}?`;
  }
}

// kindOf is the overline of an item's card: its kind's icon and name.
export function kindOf(item: NeedItem): { icon: IconName; name: string } {
  switch (item.kind) {
    case "questions":
      return { icon: "question", name: "Questions" };
    case "approval":
      return approvalKind(item.approval);
    case "effect":
      return { icon: "bolt", name: "Uncertain effect" };
    case "work":
      return item.work.orchestrator
        ? { icon: "hub", name: "Orchestrator not running" }
        : { icon: workIcons[item.work.reason] ?? "warning", name: workLabels[item.work.reason] ?? item.work.reason };
    case "plan_approval":
      return { icon: "plan", name: "Plan approval" };
    case "plan_revision":
      return { icon: "plan", name: "Plan revision" };
    case "contract_review":
      return { icon: "review", name: "Contract review" };
    case "confirm":
      return { icon: "card", name: "Confirmation card" };
    case "ci":
      return { icon: ciKinds[item.ci.kind].icon, name: ciKinds[item.ci.kind].name };
    case "spend":
      return spendKind;
    case "repository_creation":
      return { icon: "repo", name: "Repository creation" };
  }
}

// titleLimit is the most characters of a question's text its title holds before it is cut.
export const titleLimit = 90;

const oneLine = (text: string) => text.replace(/\s+/g, " ").trim();

// questionTitle is a question's own text as a title: on one line, and cut at a word boundary with an ellipsis
// when it is long. A question with no text is "A question".
export function questionTitle(text: string): string {
  const line = oneLine(text);
  if (!line) {
    return "A question";
  }
  if (line.length <= titleLimit) {
    return line;
  }
  const boundary = line.lastIndexOf(" ", titleLimit);
  const kept = boundary >= titleLimit / 2 ? line.slice(0, boundary) : line.slice(0, titleLimit);
  return `${kept.replace(/[\s,;:.\-–—]+$/, "")}…`;
}

// fullQuestionTitle is questionTitle with nothing cut: what a title attribute holds.
export function fullQuestionTitle(text: string): string {
  return oneLine(text) || "A question";
}

// fullHeadingOf is headingOf with nothing cut: what a title attribute holds, so the whole question stays one
// hover away.
export function fullHeadingOf(item: NeedItem): string {
  const [only] = item.kind === "questions" && item.batch.items.length === 1 ? item.batch.items : [];
  return only ? oneLine(only.text) || headingOf(item) : headingOf(item);
}

// headingOf says what is asked, as a person reads it. A batch of one question is titled by the question.
export function headingOf(item: NeedItem): string {
  switch (item.kind) {
    case "questions": {
      const [only] = item.batch.items;
      return item.batch.items.length === 1 && only ? questionTitle(only.text) : `${item.batch.items.length} questions`;
    }
    case "approval":
      return approvalHeading(item.approval);
    case "effect":
      return "Did it happen?";
    case "work":
      return item.work.work_order?.title || item.work.lane?.id || (item.work.orchestrator ? `Orchestrator on ${item.work.orchestrator.worker_id}` : item.id);
    case "plan_approval":
      return `Approve ${item.plan_approval.title}, version ${item.plan_approval.version}?`;
    case "plan_revision":
      return `${item.plan_revision.title} needs revision`;
    case "contract_review":
      return item.contract_review.work_order.title || item.id;
    case "confirm":
      return item.card.approval.summary;
    case "ci":
      return item.ci.work_order.title || item.ci.work_order.id;
    case "spend":
      return item.spend.work_order.title || item.spend.work_order.id;
    case "repository_creation":
      return item.repository_creation.state === "running" ? `${item.repository_creation.name} is being created` : `${item.repository_creation.name} was not created`;
  }
}

const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;

// contextOf is the one line under an item's title: what it concerns, in a few words.
export function contextOf(item: NeedItem): string {
  switch (item.kind) {
    case "questions": {
      const blocking = item.batch.items.filter((question) => question.blocking).length;
      return [plural(item.batch.items.length, "question"), blocking ? `${blocking} blocking` : ""].filter(Boolean).join(" · ");
    }
    case "approval":
      if (item.approval.kind === "merge") {
        return mergeContext(item.approval);
      }
      return [item.approval.work_order_id, item.approval.lane_id ? `lane ${item.approval.lane_id}` : "", item.approval.requested_by].filter(Boolean).join(" · ");
    case "effect":
      return [`Worker ${item.effect.worker_id}`, `attempt ${item.effect.attempt}`].join(" · ");
    case "work":
      return [item.work.work_order?.id ?? "", item.work.lane ? `lane ${item.work.lane.id}` : "", item.work.orchestrator ? `worker ${item.work.orchestrator.worker_id}` : ""]
        .filter(Boolean)
        .join(" · ");
    case "plan_approval":
      return [`Requested by ${item.plan_approval.requested_by}`, plural(item.plan_approval.drafts.length, "draft decision")].join(" · ");
    case "plan_revision":
      return [
        `${plural(item.plan_revision.decisions.length, "decision")} since version ${item.plan_revision.revision?.approved_version ?? ""}`,
        item.plan_revision.finished.length ? `${plural(item.plan_revision.finished.length, "finished work order")} reached` : "",
      ]
        .filter(Boolean)
        .join(" · ");
    case "contract_review":
      return [item.contract_review.work_order.id, `${item.contract_review.contract} v${item.contract_review.version}`].join(" · ");
    case "confirm":
      return preparerOf(item.card);
    case "ci":
      return [item.ci.work_order.id, `pull request #${item.ci.number}`].join(" · ");
    case "spend":
      return [item.spend.work_order.id, `${money(item.spend.spend.charged.charged_usd)} of ${money(item.spend.spend.hard_limit)}`].join(" · ");
    case "repository_creation":
      return [`Plan ${item.repository_creation.plan}`, `version ${item.repository_creation.plan_version}`].join(" · ");
  }
}

// preparerOf names the delegate that prepared a card: its credential's label, and the member it acts for.
export function preparerOf(card: { label: string; member: string }): string {
  return card.member ? `${card.label} for ${card.member}` : card.label;
}
