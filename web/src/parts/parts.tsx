import { createContext, useContext, useEffect, useId, useRef, useState } from "react";
import type { ReactNode } from "react";

import type { Stamp } from "../api/types";
import { useArrivalFocus } from "../app/route";
import { agoLabel, agoWords, forWords, inLabel, inWords, waitLabel } from "../needs/wait";

const clockMs = 30_000;

// useClock lets an age run on between reads.
export function useClock(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), clockMs);
    return () => clearInterval(timer);
  }, []);
  return now;
}

// ViewTime is the server's moment for what a view shows, and how long ago this browser read it. Every
// age counts from there, because a browser's clock is not the ledger's.
export const ViewTime = createContext({ asOf: 0, elapsed: 0 });

function useAge(at: number): number {
  const { asOf, elapsed } = useContext(ViewTime);
  return Math.max(0, asOf - at + elapsed);
}

export function Ago({ at }: { at: number }) {
  const age = useAge(at);
  return (
    <>
      <span aria-hidden="true">{agoLabel(age)}</span>
      <span className="visually-hidden">{agoWords(age)}</span>
    </>
  );
}

// Until says how far off a moment is, by the same clock.
export function Until({ at }: { at: number }) {
  const { asOf, elapsed } = useContext(ViewTime);
  const left = Math.max(0, at - asOf - elapsed);
  return (
    <>
      <span aria-hidden="true">{inLabel(left)}</span>
      <span className="visually-hidden">{inWords(left)}</span>
    </>
  );
}

// Lasted is the large age that heads a line of work. `ended` reads it as a time past, and otherwise it
// is how long something has gone on.
export function Lasted({ at, ended = false }: { at: number; ended?: boolean }) {
  const age = useAge(at);
  return (
    <>
      <span aria-hidden="true" className="wait-time">
        {waitLabel(age)}
      </span>
      <span className="visually-hidden">{ended ? agoWords(age) : forWords(age)}</span>
    </>
  );
}

// Page is a view's heading, which stands while its data comes and goes, so focus stays on it: a
// breadcrumb, the title, one line under it, and the view's actions on the right.
export function Page({ title, note, actions, crumbs, children }: { title: ReactNode; note?: ReactNode; actions?: ReactNode; crumbs?: ReactNode; children: ReactNode }) {
  const heading = useRef<HTMLHeadingElement>(null);
  useArrivalFocus(heading);
  return (
    <div className="screen">
      <div className="page-head">
        <div className="page-head-text">
          {crumbs ? <nav className="crumbs" aria-label="Breadcrumb">{crumbs}</nav> : null}
          <h1 ref={heading} tabIndex={-1}>
            {title}
          </h1>
          {note ? <p className="page-note">{note}</p> : null}
        </div>
        {actions ? <div className="page-actions">{actions}</div> : null}
      </div>
      {children}
    </div>
  );
}

// StampLine says which revision a view read, and how long ago.
export function StampLine({ stamp }: { stamp: Stamp }) {
  return (
    <p className="stamp">
      As of revision {stamp.revision}, <Ago at={stamp.as_of} />.
    </p>
  );
}

// Id shows an identifier on one line. One too long for its column ends in an ellipsis, and the whole of
// it stays in the page for a screen reader and a copy, and in the title a pointer brings up.
export function Id({ value }: { value: string }) {
  return (
    <span className="id" title={value}>
      {value}
    </span>
  );
}

// Bar draws how much of some work is done. Its track stays empty, so nothing done reads as an empty
// bar, and the done part fills its share. The words say the same to a screen reader.
export function Bar({ done, total, label }: { done: number; total: number; label: string }) {
  const share = total > 0 ? Math.min(Math.max(done, 0), total) / total : 0;
  return (
    <div
      className="progress"
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={total}
      aria-valuenow={done}
      aria-valuetext={`${done} of ${total} done`}
    >
      <i style={{ width: `${Math.round(share * 1000) / 10}%` }} />
    </div>
  );
}

// plural counts something in the words a person would use.
export const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;

export function Fact({ name, mono = false, children }: { name: string; mono?: boolean; children: ReactNode }) {
  return (
    <div className="kv-row">
      <dt>{name}</dt>
      <dd className={mono ? "mono" : undefined}>{children}</dd>
    </div>
  );
}

// LongFact is a fact too long for a column, such as a source or a digest. An identifier too long for the
// line ends in an ellipsis with the whole of it in the page for a copy and in its title.
export function LongFact({ name, value }: { name: string; value: string }) {
  return (
    <div className="kv-row">
      <dt>{name}</dt>
      <dd>
        <Id value={value} />
      </dd>
    </div>
  );
}

// Section is a part of a view a screen reader can jump to: a panel with its title.
export function Section({ title, actions, children }: { title: ReactNode; actions?: ReactNode; children: ReactNode }) {
  const heading = useId();
  return (
    <section className="panel group" aria-labelledby={heading}>
      <div className="panel-head">
        <h2 id={heading}>{title}</h2>
        {actions}
      </div>
      <div className="panel-body">{children}</div>
    </section>
  );
}

// Line is one record in a list: what stands at its side, its kind, its title, and what it holds.
export function Line({ side, kind, title, plain = false, children }: {
  side?: ReactNode;
  kind: ReactNode;
  title: ReactNode;
  // plain marks a record no person can do anything about.
  plain?: boolean;
  children?: ReactNode;
}) {
  const heading = useId();
  return (
    <li className={plain ? "record record-plain" : "record"}>
      <article aria-labelledby={heading}>
        <div className="record-side">
          <span className="record-kind overline">{kind}</span>
          {side}
        </div>
        <div className="record-body">
          <h2 className="record-title" id={heading}>
            {title}
          </h2>
          {children}
        </div>
      </article>
    </li>
  );
}

// words says one of the API's own names to a person: `needs_human` reads as "Needs human". A table
// decides where the plain reading would say something else.
export function words(name: string, table: Record<string, string> = {}): string {
  const said = table[name];
  if (said) {
    return said;
  }
  const spaced = name.replaceAll("_", " ");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}
