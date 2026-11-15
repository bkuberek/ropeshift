import type { Approval, MergeDetail } from "../api/types";

// merge reads a merge request: the approval a member decides about the merge of a work order's reviewed commit
// (ADR-0020). Its subject says which: `approve` asks for the merge at person_approves, and `held` asks what to do
// about a merge that cannot go on.

export const isMerge = (approval: Approval): boolean => approval.kind === "merge";

export const isHeld = (approval: Approval): boolean => approval.subject === "held";

// mergeDetailOf reads what the request says beyond its summary. A request raised before the repository was
// recorded has no repository, and one that cannot be read says nothing.
export function mergeDetailOf(approval: Approval): Partial<MergeDetail> {
  try {
    return JSON.parse(approval.detail ?? "{}") as Partial<MergeDetail>;
  } catch {
    return {};
  }
}

// mergeHeading says what the request asks.
export function mergeHeading(approval: Approval): string {
  return isHeld(approval) ? `Retry the held merge of ${approval.work_order_id}?` : `Approve the merge of ${approval.work_order_id}?`;
}

// mergeContext is the line under the request in the list: the work, and the repository it lands in.
export function mergeContext(approval: Approval): string {
  return [approval.work_order_id, mergeDetailOf(approval).repository].filter(Boolean).join(" · ");
}

// What each merge authority leaves to a member, in a few words.
export const authorityWords: Record<string, string> = {
  person_merges: "you merge on the forge, and ropeshift never merges",
  person_approves: "you approve each change, and ropeshift merges it",
  review_approves: "the review's pass approves, and ropeshift merges it",
};
