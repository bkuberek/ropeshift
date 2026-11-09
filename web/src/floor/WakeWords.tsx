import { Fragment } from "react";
import type { ReactNode } from "react";

import { addressOf, Link } from "../app/route";

// A wake is stored text in the ledger's wake language (internal/parking). This build reads the terms
// the daemon's evaluator reads, within the same bounds, and says anything else is beyond it. The words
// match internal/parking's, which `web/testdata/wake-words.json` holds them to, so `ropeshift top` and
// this page say a wake the same way.

type Named = "question_answered" | "approval_decided" | "decision_recorded" | "usage_reset" | "signin_restored" | "work_order_done" | "external_check";

export type WakeTerm =
  | { kind: "any_of" | "all_of"; terms: WakeTerm[] }
  | { kind: "at"; at: number }
  | { kind: "human_resume" }
  | { kind: Named; key: string };

const named = new Set<string>([
  "question_answered",
  "approval_decided",
  "decision_recorded",
  "usage_reset",
  "signin_restored",
  "work_order_done",
  "external_check",
]);

const maxBytes = 4 << 10;
const maxDepth = 8;

function read(raw: unknown, depth: number): WakeTerm | null {
  if (depth > maxDepth || typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    return null;
  }
  const entries = Object.entries(raw);
  const [name, value] = entries[0] ?? [];
  if (entries.length !== 1 || name === undefined) {
    return null;
  }
  if (name === "any_of" || name === "all_of") {
    if (!Array.isArray(value) || value.length === 0) {
      return null;
    }
    const terms = value.map((item: unknown) => read(item, depth + 1));
    return terms.every((term) => term !== null) ? { kind: name, terms: terms as WakeTerm[] } : null;
  }
  if (name === "at") {
    return typeof value === "number" && Number.isSafeInteger(value) ? { kind: "at", at: value } : null;
  }
  if (name === "human_resume") {
    return value === true ? { kind: "human_resume" } : null;
  }
  if (named.has(name) && typeof value === "string" && value !== "") {
    return { kind: name as Named, key: value };
  }
  return null;
}

// readWake reads a stored expression, or returns null for one this build cannot read.
export function readWake(expression: string): WakeTerm | null {
  if (expression.length > maxBytes) {
    return null;
  }
  try {
    return read(JSON.parse(expression), 1);
  } catch {
    return null;
  }
}

// flat folds a term into a parent of its own kind, and a composite of one term into that term.
function flat(term: WakeTerm): WakeTerm {
  if (term.kind !== "any_of" && term.kind !== "all_of") {
    return term;
  }
  const terms = term.terms.map(flat).flatMap((kid) => (kid.kind === term.kind ? (kid as { terms: WakeTerm[] }).terms : [kid]));
  return terms.length === 1 && terms[0] ? terms[0] : { kind: term.kind, terms };
}

// onlyAPerson says nothing but a person's word moves a lane.
export function onlyAPerson(term: WakeTerm): boolean {
  return flat(term).kind === "human_resume";
}

// A time reads in UTC, because a stored expression names no time zone.
function stamp(ms: number): string {
  try {
    const iso = new Date(ms).toISOString();
    return `${iso.slice(0, 10)} ${iso.slice(11, 16)} UTC`;
  } catch {
    return String(ms);
  }
}

const id = (key: string) => <span className="mono">{key}</span>;

function clause(term: WakeTerm): ReactNode {
  switch (term.kind) {
    case "at":
      return `the time reaches ${stamp(term.at)}`;
    case "human_resume":
      return "a person resumes it";
    case "question_answered":
      return (
        <>
          question{" "}
          <Link className="mono" to={addressOf("/questions", { id: term.key })}>
            {term.key}
          </Link>{" "}
          is answered
        </>
      );
    case "approval_decided":
      return <>approval {id(term.key)} is decided</>;
    case "decision_recorded":
      return <>a decision on {id(term.key)} is recorded</>;
    case "usage_reset":
      return <>the usage limit on {id(term.key)} resets</>;
    case "signin_restored":
      return <>the sign-in on {id(term.key)} is restored</>;
    case "work_order_done":
      return <>work order {id(term.key)} is done</>;
    case "external_check":
      return <>check {id(term.key)} reports back</>;
    default:
      return null;
  }
}

function joined(parts: ReactNode[], before: (index: number) => string): ReactNode {
  return parts.map((part, index) => (
    <Fragment key={index}>
      {index > 0 ? before(index) : null}
      {part}
    </Fragment>
  ));
}

// sentence says a term after its first word. A choice at the top repeats the word before each way
// out, a choice inside a whole reads as "either", and a comma goes before "and" when a list is long or
// a part of it is a choice.
function sentence(term: WakeTerm, top: boolean, word: string): ReactNode {
  if (term.kind === "any_of") {
    const parts = term.terms.map((kid) => sentence(kid, false, word));
    return top ? joined(parts, () => `, or ${word} `) : <>either {joined(parts, () => " or ")}</>;
  }
  if (term.kind === "all_of") {
    const parts = term.terms.map((kid) => sentence(kid, false, word));
    const comma = parts.length > 2 || term.terms.some((kid) => kid.kind === "any_of" || kid.kind === "all_of");
    return joined(parts, (index) => (index === parts.length - 1 ? (comma ? ", and " : " and ") : ", "));
  }
  return clause(term);
}

const capitalized = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

// WakeWords says what a parked lane waits for: "When question q-1 is answered, or when a person
// resumes it". A question links to where it stands.
export function WakeWords({ expression, word = "when", capital = true }: { expression: string; word?: "when" | "until"; capital?: boolean }) {
  const term = readWake(expression);
  if (!term) {
    return (
      <>
        {capital ? "Something" : "something"} this build cannot read: <span className="mono">{expression}</span>
      </>
    );
  }
  return (
    <>
      {capital ? capitalized(word) : word} {sentence(flat(term), true, word)}
    </>
  );
}
