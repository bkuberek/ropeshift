import type { CIKind } from "../api/types";
import type { IconName } from "../ui/Icon";

// What each kind of CI item is called on its card and what it says, in a person's words. None of them started an
// agent, and a card says so, because that is the reason a person sees it.
export const ciKinds: Record<CIKind, { icon: IconName; name: string; lead: string }> = {
  ci_not_finished: {
    icon: "clock",
    name: "CI has not finished",
    lead: "The head's required checks have run for longer than this repository's CI is expected to take. No agent was started, and none will be.",
  },
  ci_queue_stalled: {
    icon: "warning",
    name: "No runner picked it up",
    lead: "A required run has stayed queued for longer than a runner usually takes to pick one up. The fix is a runner, not a rerun, and no agent was started.",
  },
  ci_infrastructure_failure: {
    icon: "warning",
    name: "CI failed, not the code",
    lead: "A required check ended in a conclusion a runner or a timeout causes, and it stayed after the automatic reruns. No agent can fix that, so none was started.",
  },
  ci_base_red: {
    icon: "warning",
    name: "The base branch is red",
    lead: "A required check that failed also fails on the base branch's tip, so a rework cannot mend it. No agent was started.",
  },
  ci_action_required: {
    icon: "review",
    name: "A check needs a person",
    lead: "A required check concluded that a person on the forge has to act before it runs. No agent was started.",
  },
};
