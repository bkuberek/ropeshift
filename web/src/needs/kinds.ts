import type { NeedItem } from "../api/types";
import type { IconName } from "../ui/Icon";
import { recoveryHeading } from "./recovery";

// The filters of Needs you, each a family of kinds.
export type NeedFilter = "all" | "decisions" | "approvals" | "cards" | "stuck";

export const filters: { id: NeedFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "decisions", label: "Decisions" },
  { id: "approvals", label: "Approvals" },
  { id: "cards", label: "Cards" },
  { id: "stuck", label: "Stuck" },
];

// filterOf is the family an item belongs to. A confirmation card is a kind of its own, which this build
// does not raise, so the Cards filter counts none.
export function filterOf(item: NeedItem): Exclude<NeedFilter, "all"> {
  switch (item.kind) {
    case "questions":
    case "plan_approval":
    case "contract_review":
    case "effect":
      return "decisions";
    case "approval":
      return "approvals";
    case "work":
      return item.work.reason === "acceptance" ? "approvals" : "stuck";
  }
}

// keyOf names an item in an address: its kind and its ID.
export function keyOf(item: NeedItem): string {
  return `${item.kind}:${item.id}`;
}

export const workLabels: Record<string, string> = {
  acceptance: "Waiting for acceptance",
  blocked: "Blocked",
  human_resume: "Parked",
  lapsed: "Lapsed",
  long_parked: "Long parked",
  stalled: "Stalled",
  tripped: "Tripped",
};

const workIcons: Record<string, IconName> = {
  acceptance: "review",
  blocked: "warning",
  human_resume: "pause",
  lapsed: "clock",
  long_parked: "pause",
  stalled: "warning",
  tripped: "power",
};

// kindOf is the overline of an item's card: its kind's icon and name.
export function kindOf(item: NeedItem): { icon: IconName; name: string } {
  switch (item.kind) {
    case "questions":
      return { icon: "question", name: "Questions" };
    case "approval":
      return item.approval.kind === "recovery" ? { icon: "restart", name: "Recovery request" } : { icon: "terminal", name: "Approval request" };
    case "effect":
      return { icon: "bolt", name: "Uncertain effect" };
    case "work":
      return item.work.orchestrator
        ? { icon: "hub", name: "Orchestrator not running" }
        : { icon: workIcons[item.work.reason] ?? "warning", name: workLabels[item.work.reason] ?? item.work.reason };
    case "plan_approval":
      return { icon: "plan", name: "Plan approval" };
    case "contract_review":
      return { icon: "review", name: "Contract review" };
  }
}

// headingOf says what is asked, as a person reads it.
export function headingOf(item: NeedItem): string {
  switch (item.kind) {
    case "questions": {
      const count = item.batch.items.length;
      return count === 1 ? "A question" : `${count} questions`;
    }
    case "approval":
      return item.approval.kind === "recovery" ? recoveryHeading(item.approval) : `Allow ${item.approval.subject}?`;
    case "effect":
      return "Did it happen?";
    case "work":
      return item.work.work_order?.title || item.work.lane?.id || (item.work.orchestrator ? `Orchestrator on ${item.work.orchestrator.worker_id}` : item.id);
    case "plan_approval":
      return `Approve ${item.plan_approval.title}, version ${item.plan_approval.version}?`;
    case "contract_review":
      return item.contract_review.work_order.title || item.id;
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
      return [item.approval.work_order_id, item.approval.lane_id ? `lane ${item.approval.lane_id}` : "", item.approval.requested_by].filter(Boolean).join(" · ");
    case "effect":
      return [`Worker ${item.effect.worker_id}`, `attempt ${item.effect.attempt}`].join(" · ");
    case "work":
      return [item.work.work_order?.id ?? "", item.work.lane ? `lane ${item.work.lane.id}` : "", item.work.orchestrator ? `worker ${item.work.orchestrator.worker_id}` : ""]
        .filter(Boolean)
        .join(" · ");
    case "plan_approval":
      return [`Requested by ${item.plan_approval.requested_by}`, plural(item.plan_approval.drafts.length, "draft decision")].join(" · ");
    case "contract_review":
      return [item.contract_review.work_order.id, `${item.contract_review.contract} v${item.contract_review.version}`].join(" · ");
  }
}
