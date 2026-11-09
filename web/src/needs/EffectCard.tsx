import { decideEffect } from "../api/client";
import type { Effect, EffectOutcome } from "../api/types";
import { Id } from "../parts/parts";
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

// Only a person can say what became of this, and the server says which answers still mean something.
export function EffectCard({ effect, after }: { effect: Effect; after: () => Promise<void> }) {
  const { act, busy, failure } = useAct(after);
  return (
    <div className="card-body">
      <p className="lead">
        A crash cut off {kindWords[effect.kind] ?? effect.kind} on lane <span className="mono">{effect.lane_id}</span>, and nothing can show
        whether it took place.
      </p>
      <dl className="facts">
        <div>
          <dt>Effect</dt>
          <dd>
            <Id value={effect.key} />
          </dd>
        </div>
        {effect.target ? (
          <div>
            <dt>Target</dt>
            <dd className="mono">{effect.target}</dd>
          </div>
        ) : null}
        <div>
          <dt>Attempt</dt>
          <dd>{effect.attempt}</dd>
        </div>
        <div>
          <dt>Worker</dt>
          <dd>
            <Id value={effect.worker_id} />
          </dd>
        </div>
      </dl>
      {effect.offers.includes("retry") ? <p className="quiet">Running it again may repeat it, if it did happen.</p> : null}
      {failure ? (
        <p className="refusal" role="alert">
          {failure}
        </p>
      ) : null}
      <div className="actions">
        {effect.offers.map((outcome) => (
          <button
            key={outcome}
            className="button"
            type="button"
            disabled={busy}
            onClick={() => void act(() => decideEffect(effect.id, outcome, ""))}
          >
            {outcomeWords[outcome]}
          </button>
        ))}
      </div>
    </div>
  );
}
