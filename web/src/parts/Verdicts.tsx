import { useId } from "react";

import type { NotePoint, ReviewNote, Standing } from "../api/types";
import { verdictStates } from "../floor/states";
import { Badge, RecordChip, StateBadge } from "../ui/display";

const openStates = new Set(["ready", "claimed", "running", "parked", "lapsed", "blocked"]);

// The security points a review judges in every diff, in the words a person reads them.
const pointWords: Record<NotePoint, string> = {
  secrets: "Secrets",
  injection: "Injection",
  dependencies: "Dependencies",
  authority: "Authority",
};

// Verdicts lists what a review found of each criterion of a contract, in contract order, with its reasons
// and what it rests on, and after them the security notes it left. A verdict is said in a word and a glyph,
// so no one reads it from a color (WCAG 1.4.1). Every word of it is a model's, so it goes in as text. Work no
// review judged says nothing of one.
export function Verdicts({ standing }: { standing: Standing }) {
  const heading = useId();
  const verdicts = standing.verdicts ?? [];
  const notes = standing.notes ?? [];
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
          {notes.length ? (
            <div className="verdict-notes">
              <h4 className="overline">Security notes</h4>
              <SecurityNotes notes={notes} />
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

// SecurityNotes lists what a review noted of the security points in a diff: each with its point, its reasons, and
// what it rests on. A note is not a verdict and says so, because it fails no criterion. Every word of it is a
// model's, so it goes in as text.
export function SecurityNotes({ notes }: { notes: ReviewNote[] }) {
  return (
    <>
      <p className="small muted notes-lead">A note fails no criterion and changes no outcome. It is here for you to read.</p>
      <ul className="verdicts" aria-label="Security notes">
        {notes.map((note) => (
          <li key={`${note.review_id}:${note.seq}`} className="verdict">
            <Badge tone="warning" glyph="triangle">
              {pointWords[note.point] ?? note.point}
            </Badge>
            <div>
              <p className="verdict-reason">{note.reasons}</p>
              {note.citations?.length ? (
                <ul className="verdict-cites" aria-label={`Citations for the ${note.point} note`}>
                  {note.citations.map((citation) => (
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
  );
}
