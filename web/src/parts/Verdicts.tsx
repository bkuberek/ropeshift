import type { Standing, VerdictKind } from "../api/types";

// A verdict is said in words, so no one reads it from a colour (WCAG 1.4.1). Its class styles the word and
// carries no meaning of its own.
const verdictWords: Record<VerdictKind, string> = {
  pass: "Pass",
  fail: "Fail",
  cannot_judge: "Cannot judge",
};

const openStates = new Set(["ready", "claimed", "running", "parked", "lapsed", "blocked"]);

// Verdicts lists what a review found of each criterion of a contract, with its reasons and what it rests on.
// Every word of it is a model's, so it goes in as text. Work no review judged says nothing of one.
export function Verdicts({ standing }: { standing: Standing }) {
  const verdicts = standing.verdicts ?? [];
  const open = (standing.reviews ?? []).filter((review) => openStates.has(review.state));
  const failed = (standing.reviews ?? []).some((review) => review.state === "failed");
  if (verdicts.length === 0 && open.length === 0 && !failed) {
    return null;
  }
  return (
    <section className="card-part" aria-label="What the review found">
      <h3 className="part-title">What the review found</h3>
      {verdicts.length === 0 ? (
        <p className="quiet">
          {open.length ? "A review of this work has not ended. If you decide now, it ends." : "The review of this work failed, so you decide without it."}
        </p>
      ) : (
        <ul className="verdicts">
          {verdicts.map((verdict) => (
            <li key={verdict.criterion} className={`verdict verdict-${verdict.verdict}`}>
              <p className="verdict-head">
                <span className="mono">{verdict.criterion}</span> <strong className="verdict-word">{verdictWords[verdict.verdict] ?? verdict.verdict}</strong>
              </p>
              <p className="verdict-reasons">{verdict.reasons}</p>
              {verdict.citations?.length ? (
                <ul className="verdict-citations" aria-label={`Citations for ${verdict.criterion}`}>
                  {verdict.citations.map((citation) => (
                    <li key={citation}>{citation}</li>
                  ))}
                </ul>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
