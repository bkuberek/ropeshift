import { useId } from "react";

import type { Standing } from "../api/types";
import { verdictStates } from "../floor/states";
import { RecordChip, StateBadge } from "../ui/display";

const openStates = new Set(["ready", "claimed", "running", "parked", "lapsed", "blocked"]);

// Verdicts lists what a review found of each criterion of a contract, in contract order, with its reasons
// and what it rests on. A verdict is said in a word and a glyph, so no one reads it from a color (WCAG
// 1.4.1). Every word of it is a model's, so it goes in as text. Work no review judged says nothing of one.
export function Verdicts({ standing }: { standing: Standing }) {
  const heading = useId();
  const verdicts = standing.verdicts ?? [];
  const open = (standing.reviews ?? []).filter((review) => openStates.has(review.state));
  const failed = (standing.reviews ?? []).some((review) => review.state === "failed");
  if (verdicts.length === 0 && open.length === 0 && !failed) {
    return null;
  }
  const commit = verdicts[0]?.commit;
  return (
    <section className="decision-section" aria-labelledby={heading}>
      <h3 className="h4" id={heading}>
        What the review found
      </h3>
      {verdicts.length === 0 ? (
        <p className="muted">{open.length ? "A review of this work has not ended. If you decide now, it ends." : "The review of this work failed, so you decide without it."}</p>
      ) : (
        <>
          {commit ? (
            <p className="small muted verdicts-head">
              Verdict on <span className="digest">{commit.slice(0, 12)}</span>
              {standing.rounds ? ` · round ${standing.rounds}` : ""}
            </p>
          ) : null}
          <ul className="verdicts" aria-label="Verdicts">
            {verdicts.map((verdict) => (
              <li key={verdict.criterion} className={`verdict${verdict.verdict === "fail" ? " v-fail" : ""}`}>
                <StateBadge table={verdictStates} state={verdict.verdict} />
                <div>
                  <p className="verdict-crit">
                    <span className="mono">{verdict.criterion}</span>
                  </p>
                  <p className="verdict-reason">{verdict.reasons}</p>
                  {verdict.citations?.length ? (
                    <ul className="verdict-cites" aria-label={`Citations for ${verdict.criterion}`}>
                      {verdict.citations.map((citation) => (
                        <li key={citation}>
                          <RecordChip id={citation} />
                        </li>
                      ))}
                    </ul>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
