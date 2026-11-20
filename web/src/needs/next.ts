import type { NeedItem } from "../api/types";
import type { Decided } from "./decided";

// NextStep says what happens once a member decided the last item that waited, and where to watch it happen.
export type NextStep = { words: string; link: string; to: string };

// laneNamed is the lane an item came from, when one did.
function laneNamed(item: NeedItem): string {
  switch (item.kind) {
    case "approval":
      return item.approval.lane_id;
    case "work":
      return item.work.lane?.id ?? "";
    case "questions": {
      const lanes = new Set(item.batch.items.map((question) => question.lane ?? ""));
      const [only] = lanes;
      return lanes.size === 1 ? (only ?? "") : "";
    }
    default:
      return "";
  }
}

// nextStepOf says what the decision a member just made sets going. Ropeshift runs on its own once nothing waits for a
// member, so the next step is always somewhere to watch it, never another thing to do.
export function nextStepOf(item: NeedItem, decided?: Decided): NextStep {
  const lane = laneNamed(item);
  const watch = { link: "Watch it on Floor", to: "/floor" };
  const declined = decided !== undefined && !decided.approved;
  switch (item.kind) {
    case "questions":
      return { ...watch, words: lane ? `Lane ${lane} goes on with your answer.` : "The work these questions held goes on with your answers." };
    case "plan_approval":
      return declined
        ? { link: "Open Plans", to: "/plans", words: "The plan stays unapproved." }
        : { ...watch, words: "Intake reads the plan, and its ready work starts on its own." };
    case "approval":
      if (item.approval.kind === "merge") {
        return { ...watch, words: declined ? "The work goes back, and its next attempt reads your reason." : "The merge goes ahead." };
      }
      return { ...watch, words: lane ? `Lane ${lane} goes on${declined ? ", and reads why you denied it" : ""}.` : "The work it held goes on." };
    default:
      return { ...watch, words: lane ? `Lane ${lane} goes on as you decided.` : "The work it held goes on as you decided." };
  }
}
