import { decideEffect } from "../api/client";
import type { Effect, EffectOutcome } from "../api/types";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { Fact, Facts, Id, Refusal } from "../ui/display";
import type { Frame } from "./frame";
import { useAct } from "./useAct";

const kindWords: Record<string, string> = {
  input: "an input to a session",
  hook: "a profile's hook",
};

const outcomeWords: Record<EffectOutcome, string> = {
  happened: "It happened",
  not_happened: "It did not happen",
  retry: "Run it again",
};

// The safe answers first, and running it again, which may repeat it, last.
const outcomeOrder: EffectOutcome[] = ["not_happened", "happened", "retry"];

// Only a person can say what became of this, and the server says which answers still mean something.
export function EffectCard({ effect, after, frame }: { effect: Effect; after: () => Promise<void>; frame: Frame }) {
  const { act, busy, failure } = useAct(after);
  const offers = outcomeOrder.filter((outcome) => effect.offers.includes(outcome));
  return (
    <DecisionCard
      kind={{ icon: "bolt", name: "Uncertain effect" }}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      binding={effect.offers.includes("retry") ? "Running it again may repeat it, if it did happen." : undefined}
      actions={offers.map((outcome) => (
        <Button key={outcome} disabled={busy} onClick={() => void act(() => decideEffect(effect.id, outcome, ""))}>
          {outcomeWords[outcome]}
        </Button>
      ))}
    >
      <p>
        A crash cut off {kindWords[effect.kind] ?? effect.kind} on lane <span className="mono">{effect.lane_id}</span>, and nothing can show
        whether it took place.
      </p>
      <Facts>
        <Fact name="Effect">
          <Id value={effect.key} />
        </Fact>
        {effect.target ? (
          <Fact name="Target" mono>
            {effect.target}
          </Fact>
        ) : null}
        <Fact name="Attempt">{effect.attempt}</Fact>
        <Fact name="Worker">
          <Id value={effect.worker_id} />
        </Fact>
      </Facts>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
