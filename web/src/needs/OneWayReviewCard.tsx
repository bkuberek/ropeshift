import { keepDecision } from "../api/client";
import type { OneWayReviewAction, OneWayReviewNeed } from "../api/types";
import { plainLine } from "../planning/creation";
import { OverturnDecisionButton } from "../planning/OverturnDecision";
import { Button } from "../ui/controls";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { Fact, Facts, Refusal } from "../ui/display";
import type { Frame } from "./frame";
import { deciderOf, oneWayReviewKind } from "./kinds";
import { useAct } from "./useAct";

type Props = {
  need: OneWayReviewNeed;
  after: () => Promise<void>;
  frame: Frame;
  // onResolved says, in words, how the member settled the item from this card, so the page can say why it no longer waits.
  onResolved?: (words: string) => void;
};

// OneWayReviewCard is a one-way decision an orchestrator on an unattended worker made itself, which waits for a member's
// review and blocks no work (FR-QD-8). Its primary action is Keep, which records the same decision as the member's in
// its place, and Overturn beside it reverses the decision with what stands instead.
export function OneWayReviewCard({ need, after, frame, onResolved }: Props) {
  const { act, busy, failure } = useAct(after);
  const decision = need.decision;
  const offers = (action: OneWayReviewAction) => need.actions.includes(action);
  const decider = deciderOf(need);
  const keep = () =>
    void act(async () => {
      await keepDecision(decision);
      onResolved?.("you kept it as your decision");
    });
  return (
    <DecisionCard
      kind={oneWayReviewKind}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      binding={`Decision ${plainLine(decision.id)}. It leaves this list once you keep it or overturn it.`}
      actions={
        <>
          {offers("decision_overturn") ? <OverturnDecisionButton decision={{ ...decision, lineage: [decision.id] }} after={after} /> : null}
          {offers("decision_keep") ? (
            <Button variant="primary" loading={busy} disabled={busy} data-focus={`Keep ${plainLine(decision.id)}`} onClick={keep}>
              Keep
            </Button>
          ) : null}
        </>
      }
    >
      <DecisionSection title="What it changes">
        <p>
          The orchestrator on {decider} decided this one-way choice itself: the worker is unattended, a member accepted its one-way acknowledgement, and the
          decision policy keeps none of its categories for a member. No work waits on your review.
        </p>
        <p>Keep records the same decision as yours in its place. Overturn reverses it: each completed work order that rested on it goes to rework, and each lane that read it is told.</p>
        <p role="status" className="small">
          {busy ? `Keeping ${plainLine(decision.id)}` : ""}
        </p>
      </DecisionSection>
      <Facts>
        <Fact name="Decision">{plainLine(decision.statement)}</Fact>
        {need.question ? <Fact name="Question">{plainLine(need.question)}</Fact> : null}
        {decision.rationale ? <Fact name="Rationale">{plainLine(decision.rationale)}</Fact> : null}
        {decision.citations?.length ? (
          <Fact name="Citations" mono>
            {decision.citations.map(plainLine).join(", ")}
          </Fact>
        ) : null}
        {decision.scope ? (
          <Fact name="Scope" mono>
            {plainLine(decision.scope)}
          </Fact>
        ) : null}
        {decision.categories?.length ? <Fact name="Categories">{decision.categories.map(plainLine).join(", ")}</Fact> : null}
      </Facts>
      {failure ? <Refusal>{plainLine(failure)}</Refusal> : null}
    </DecisionCard>
  );
}
