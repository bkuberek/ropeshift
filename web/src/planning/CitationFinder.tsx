import { useId, useState } from "react";

import { useChange } from "../admin/parts";
import { searchCandidates } from "../api/client";
import type { Candidate } from "../api/types";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";

const citationOf = (candidate: Candidate) => `${candidate.kind}:${candidate.record_id}`;

// CitationFinder looks a record up by some words, as `ropeshift search` does, so a member cites what they
// found and does not type its name. cited is what the citations already hold, and onCite adds one.
export function CitationFinder({ cited, onCite }: { cited: string[]; onCite: (citation: string) => void }) {
  const id = useId();
  const [words, setWords] = useState("");
  const [found, setFound] = useState<Candidate[] | null>(null);
  const read = useChange();
  const asked = words.trim();

  const search = async () => {
    if (asked === "") {
      return;
    }
    const list = await read.run(() => searchCandidates(asked));
    if (list) {
      setFound(list.candidates);
    }
  };

  return (
    <div className="field">
      <label className="label" htmlFor={id}>
        Find a record to cite
      </label>
      <div className="row-tight">
        <input
          id={id}
          className="input"
          autoComplete="off"
          aria-describedby={`${id}-help`}
          value={words}
          onChange={(event) => setWords(event.target.value)}
          onKeyDown={(event) => {
            // Enter searches, and does not go on to the form this sits in.
            if (event.key === "Enter") {
              event.preventDefault();
              void search();
            }
          }}
        />
        <Button loading={read.busy} disabled={asked === ""} onClick={() => void search()}>
          Search
        </Button>
      </div>
      <p className="help" id={`${id}-help`}>
        Words from a decision, decision record, requirement, contract, or question. The best matches come first.
      </p>
      {read.failure ? <Refusal>{read.failure}</Refusal> : null}
      {found && found.length === 0 ? <p className="small muted">Nothing matches those words.</p> : null}
      {found && found.length > 0 ? (
        <ul className="list-rows" role="list" aria-label="Records found">
          {found.map((candidate) => {
            const citation = citationOf(candidate);
            const done = cited.includes(citation);
            return (
              <li key={citation} className="list-row row-tight">
                <span className="mono">{citation}</span>
                {candidate.kind === "decision_document" ? <span className="small muted">Decision record</span> : null}
                <span className="spacer" />
                <Button small disabled={done} aria-label={`${done ? "Cited" : "Cite"} ${citation}`} onClick={() => onCite(citation)}>
                  {done ? "Cited" : "Cite"}
                </Button>
              </li>
            );
          })}
        </ul>
      ) : null}
    </div>
  );
}
