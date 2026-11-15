import type { AllowedAction, Allowance } from "../api/types";
import type { IconName } from "../ui/Icon";

// What a member can do to a work order besides starting it, in the words a menu, a dialog, and the command
// palette share. Which of them a work order offers is the server's to say, from its state machine, so nothing
// here decides that: it only says what each one reads like.

export type RecoverAction = Exclude<AllowedAction, "claim" | "start_lane">;

export type Spec = {
  // label is the action's name, which its menu item, its dialog, and its button carry.
  label: string;
  icon: IconName;
  // slug names the action in an address that opens its dialog.
  slug: string;
  // says is what it does, which its dialog tells before a person presses it.
  says: string;
  // reason says a person must give a reason.
  reason: boolean;
  // destructive says it cannot be undone, so its button is the danger one.
  destructive: boolean;
};

export const specs: Record<RecoverAction, Spec> = {
  retry: {
    label: "Retry",
    icon: "retry",
    slug: "retry",
    says: "Returns the failed work to ready for another attempt. The next attempt's first prompt says why the last lane failed and why you retried.",
    reason: true,
    destructive: false,
  },
  release: {
    label: "Release the claim",
    icon: "stop",
    slug: "release",
    says: "Gives the claim back, which returns the work to ready, so any worker can claim it.",
    reason: false,
    destructive: false,
  },
  send_back: {
    label: "Send back to planning",
    icon: "replan",
    slug: "send-back",
    says: "Returns the work to planning, because its contract must change. The review of this version is void.",
    reason: true,
    destructive: false,
  },
  reassign: {
    label: "End this claim",
    icon: "restart",
    slug: "reassign",
    says: "Ends the lapsed holder's claim, so the work can be claimed again.",
    reason: true,
    destructive: false,
  },
  resolve: {
    label: "Back to ready",
    icon: "play",
    slug: "resolve",
    says: "Returns the blocked work to ready.",
    reason: true,
    destructive: false,
  },
  replan: {
    label: "Back to planning",
    icon: "replan",
    slug: "replan",
    says: "Sends the blocked work back to planning, with the reason and the lane's evidence.",
    reason: true,
    destructive: false,
  },
  cancel: {
    label: "Cancel this work",
    icon: "x",
    slug: "cancel",
    says: "Ends the work for good, which stops its lane. Canceled work cannot be started again.",
    reason: true,
    destructive: true,
  },
  archive_lane: {
    label: "Archive the worktree",
    icon: "archive",
    slug: "archive",
    says: "Asks the worker that ran the lane to remove its worktree on its next pass.",
    reason: false,
    destructive: true,
  },
};

// recoverActions is every action the page offers besides a start, in the order the server lists them.
export const recoverActions = Object.keys(specs) as RecoverAction[];

export const isRecover = (action: AllowedAction): action is RecoverAction => action in specs;

// labelOf names an action as its control reads. A retry of work that used every attempt says it adds one.
export function labelOf(action: RecoverAction, allowance: Allowance | undefined): string {
  return action === "retry" && allowance?.extra_attempt ? "Retry with an extra attempt" : specs[action].label;
}

// recoveries are the actions of an allowance that this page offers, in the server's order.
export function recoveries(allowance: Allowance | undefined): RecoverAction[] {
  return (allowance?.actions ?? []).filter(isRecover);
}

// slugOf finds the action an address names.
export const actionOfSlug = (slug: string): RecoverAction | undefined => recoverActions.find((action) => specs[action].slug === slug);
