import { useChange } from "../admin/parts";
import { decideQuestionNow } from "../api/client";
import type { Question } from "../api/types";
import { addressOf, go } from "../app/route";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";

// onTheLadder says a question waits for an orchestrator, not yet for a person, so Decide now has something to move.
export const onTheLadder = (question: Question) => question.status === "open" || question.status === "triage";

// useDecideNow takes a question still on the ladder into the member's Needs you, in place of the orchestrator that would
// judge it, and opens its card there with the primary button focused. It answers nothing itself.
export function useDecideNow(question: Question) {
  const change = useChange();
  const decide = () =>
    void change.run(async () => {
      const waiting = await decideQuestionNow(question.id);
      if (waiting.batch_id) {
        go(addressOf("/needs-you", { item: `questions:${waiting.batch_id}`, focus: "primary" }));
      }
    });
  return { decide, busy: change.busy, failure: change.failure };
}

// DecideNowButton is Decide now on a question still on the ladder, and says why it was refused.
export function DecideNowButton({ question, now }: { question: Question; now: ReturnType<typeof useDecideNow> }) {
  if (!onTheLadder(question)) {
    return null;
  }
  return (
    <>
      <Button small aria-label={`Decide now: ${question.text}`} loading={now.busy} disabled={now.busy} onClick={now.decide}>
        Decide now
      </Button>
      {now.failure ? <Refusal>{now.failure}</Refusal> : null}
    </>
  );
}
