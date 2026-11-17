import { useEffect, useState } from "react";

import { readView } from "../api/client";
import type { Decision, Decisions } from "../api/types";
import { inForce } from "../planning/records";
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

// ReplacesField is the part of an answer that says it replaces a decision in force. It starts closed, and the
// decisions in force are read only when a person opens it, so an answer that replaces nothing costs no read. Until
// an item names the decisions its scope overlaps, a person picks from every decision in force.
export function ReplacesField({ value, onChange }: { value: string; onChange: (id: string) => void }) {
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
