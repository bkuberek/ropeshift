import { useEffect, useId, useState } from "react";

import { readView } from "../api/client";
import type { Decision, Decisions } from "../api/types";
import { inForce } from "../planning/records";
import { Button } from "../ui/controls";
import { RecordChip } from "../ui/display";
import { Field, Select } from "../ui/forms";

const shortened = (text: string, most = 72) => (text.length > most ? `${text.slice(0, most - 1)}…` : text);

type Read = { state: "reading" } | { state: "failed"; failure: string } | { state: "read"; decisions: Decision[] };

// useDecisionsInForce reads the project's decisions that still stand, once, when a control that offers to replace
// one opens. A read that fails says so, and the answer it sits beside can still be sent.
function useDecisionsInForce(): Read {
  const [read, setRead] = useState<Read>({ state: "reading" });
  useEffect(() => {
    let current = true;
    readView<Decisions>("decisions").then(
      (view) => current && setRead({ state: "read", decisions: (view.decisions ?? []).filter(inForce) }),
      (error: unknown) => current && setRead({ state: "failed", failure: error instanceof Error ? error.message : String(error) }),
    );
    return () => {
      current = false;
    };
  }, []);
  return read;
}

function ReplacesPicker({ value, onChange }: { value: string; onChange: (id: string) => void }) {
  const read = useDecisionsInForce();
  if (read.state === "reading") {
    return <p className="small muted">Reading the decisions in force.</p>;
  }
  if (read.state === "failed") {
    return <p className="small muted">{`The decisions in force could not be read (${read.failure}). The answer can still be sent, and replaces nothing.`}</p>;
  }
  if (read.decisions.length === 0) {
    return <p className="small muted">No decision is in force to replace.</p>;
  }
  return (
    <Field label="Replaces" help="Takes the place of a decision in force, with no rework, as this answer is recorded. To reverse one and rework what rested on it, overturn it instead.">
      <Select value={value} onChange={(event) => onChange(event.target.value)}>
        <option value="">Nothing</option>
        {read.decisions.map((row) => (
          <option key={row.id} value={row.id}>
            {row.id}: {shortened(row.statement)}
          </option>
        ))}
      </Select>
    </Field>
  );
}

type ReplacesProps = { value: string; onChange: (id: string) => void };

// OverlapRow is one decision in force that the question's scope overlapped: its ID, its statement when it could be
// read, and the control that makes the answer replace it. Replacing is a choice of one decision, so choosing another
// takes the place of the first.
function OverlapRow({ id, statement, value, onChange }: { id: string; statement?: string; value: string; onChange: (id: string) => void }) {
  const said = useId();
  const chosen = value === id;
  return (
    <div className="row-tight">
      <RecordChip id={id} icon="decision" />
      {statement ? (
        <span id={said} className="small">
          {statement}
        </span>
      ) : null}
      <Button small aria-pressed={chosen} aria-describedby={statement ? said : undefined} onClick={() => onChange(chosen ? "" : id)}>
        Replace it
      </Button>
      {chosen ? <span className="cond">This answer replaces it</span> : null}
    </div>
  );
}

// OverlapField shows each decision in force that the question's scope overlapped when it was escalated, with its
// statement, and offers to replace it. It takes the place of the picker over every decision in force, because the
// question names the ones an answer may contradict. A decision that no longer stands is left out, and one that
// cannot be read is shown by its ID.
function OverlapField({ overlaps, value, onChange }: ReplacesProps & { overlaps: string[] }) {
  const read = useDecisionsInForce();
  const heading = useId();
  const body = () => {
    if (read.state === "reading") {
      return <p className="small muted">Reading the decisions this question overlaps.</p>;
    }
    if (read.state === "failed") {
      return (
        <>
          <p className="small muted">{`The statements could not be read (${read.failure}). The answer can still be sent.`}</p>
          {overlaps.map((id) => (
            <OverlapRow key={id} id={id} value={value} onChange={onChange} />
          ))}
        </>
      );
    }
    const standing = overlaps.map((id) => read.decisions.find((row) => row.id === id)).filter((row): row is Decision => row !== undefined);
    if (standing.length === 0) {
      return <p className="small muted">The decisions this question overlapped no longer stand.</p>;
    }
    return standing.map((row) => <OverlapRow key={row.id} id={row.id} statement={row.statement} value={value} onChange={onChange} />);
  };
  return (
    <div role="group" aria-labelledby={heading} className="question-overlaps">
      <p id={heading} className="small">
        This question overlaps a decision in force. An answer that contradicts it can replace it, with no rework.
      </p>
      {body()}
    </div>
  );
}

function ReplacesDisclosure({ value, onChange }: ReplacesProps) {
  const [opened, setOpened] = useState(false);
  return (
    <details className="disclosure" open={opened}>
      <summary
        onClick={(event) => {
          event.preventDefault();
          setOpened(!opened);
        }}
      >
        Replaces a decision{value ? `: ${value}` : ""}
      </summary>
      {opened ? <ReplacesPicker value={value} onChange={onChange} /> : null}
    </details>
  );
}

// ReplacesField is the part of an answer that says it replaces a decision in force. An item that names the decisions
// its scope overlaps shows them, each with its statement and a Replace it control. Any other starts closed, and the
// decisions in force are read only when a person opens it, so an answer that replaces nothing costs no read: a person
// picks from every decision in force.
export function ReplacesField({ value, onChange, overlaps = [] }: ReplacesProps & { overlaps?: string[] }) {
  return overlaps.length > 0 ? <OverlapField overlaps={overlaps} value={value} onChange={onChange} /> : <ReplacesDisclosure value={value} onChange={onChange} />;
}
