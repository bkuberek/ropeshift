import type { Approval, CIKind, NeedItem } from "../api/types";
import { plainLine } from "../planning/creation";
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
    case "personal_setup":
      return "decisions";
    case "questions":
    case "one_way_review":
    case "plan_approval":
    case "plan_revision":
    case "contract_review":
    case "effect":
    case "conflict":
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

const fixedCards = ["questions", "recovery", "merge", "held-merge", "tool-call", "effect", "orchestrator", "plan-approval", "plan-revision", "contract-review", "confirm", "spend", "repository-creation", "conflict", "one-way-review", "personal-setup"] as const;

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
    case "conflict":
      return "conflict";
    case "one_way_review":
      return "one-way-review";
    case "personal_setup":
      return "personal-setup";
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

// creationKind is the overline of a repository a plan approval did not create.
export const creationKind: { icon: IconName; name: string } = { icon: "repo", name: "Repository creation" };

// conflictKind is the overline of a lane whose branch changed files a person changed too.
export const conflictKind: { icon: IconName; name: string } = { icon: "branch", name: "Conflict" };
// oneWayReviewKind is the overline of a one-way decision an orchestrator made, which waits for a member's review.
export const oneWayReviewKind: { icon: IconName; name: string } = { icon: "decision", name: "One-way decision to review" };
// setupKind is the overline of a piece of the member's own agent CLI setup that reached a machine's lanes.
export const setupKind: { icon: IconName; name: string } = { icon: "sliders", name: "Your CLI setup in lanes" };

// pieceWords names each kind of a member's own setup as a heading says it.
export const pieceWords: Record<string, string> = { mcp_server: "MCP server", plugin: "plugin", agent: "agent", skill: "skill", command: "command" };

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
      return creationKind;
    case "conflict":
      return conflictKind;
    case "one_way_review":
      return oneWayReviewKind;
    case "personal_setup":
      return setupKind;
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

// headingOf says what is asked, as a person reads it. A batch of one question is titled by the title its asker gave
// it, or else by its text.
export function headingOf(item: NeedItem): string {
  switch (item.kind) {
    case "questions": {
      const [only] = item.batch.items;
      return item.batch.items.length === 1 && only ? questionTitle(only.title?.trim() || only.text) : `${item.batch.items.length} questions`;
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
      return `${plainLine(item.repository_creation.name)} ${item.repository_creation.state === "running" ? "is being created" : "was not created"}`;
    case "conflict":
      return `${plainLine(item.conflict.title || item.conflict.work_order)} changed files a person changed too`;
    case "one_way_review":
      return questionTitle(item.one_way_review.decision.statement);
    case "personal_setup":
      return `Your ${pieceWords[item.personal_setup.piece.kind] ?? item.personal_setup.piece.kind} ${plainLine(item.personal_setup.piece.name)} reached lanes on ${item.personal_setup.worker}`;
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
      return [`Plan ${plainLine(item.repository_creation.plan)}`, `version ${item.repository_creation.plan_version}`].join(" · ");
    case "conflict":
      return [item.conflict.work_order, `lane ${item.conflict.lane}`, plainLine(item.conflict.name), plural(item.conflict.paths.length, "file")].join(" · ");
    case "one_way_review":
      return [`Decided by the orchestrator on ${deciderOf(item.one_way_review)}`, plainLine(item.one_way_review.decision.id)].join(" · ");
    case "personal_setup":
      return [`Worker ${item.personal_setup.worker}`, `version ${item.personal_setup.version}`, plural(item.personal_setup.lanes.length, "lane")].join(" · ");
  }
}

// deciderOf names the worker whose orchestrator made a one-way decision: its name, or its ID when the ledger no longer
// holds the worker.
export function deciderOf(need: { decided_by_name?: string; decision: { decided_by?: string } }): string {
  return plainLine(need.decided_by_name || need.decision.decided_by || "its worker");
}

// preparerOf names the delegate that prepared a card: its credential's label, and the member it acts for.
export function preparerOf(card: { label: string; member: string }): string {
  return card.member ? `${card.label} for ${card.member}` : card.label;
}
