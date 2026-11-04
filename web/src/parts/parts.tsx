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

// Page is a view's heading, which stands while its data comes and goes, so focus stays on it.
export function Page({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  const heading = useRef<HTMLHeadingElement>(null);
  useArrivalFocus(heading);
  return (
    <>
      <div className="summary">
        <h1 className={title.length > 18 ? "title title-long" : "title"} ref={heading} tabIndex={-1}>
          {title}
        </h1>
        {note ? <p className="count">{note}</p> : null}
      </div>
      {children}
    </>
  );
}

export function StampLine({ stamp }: { stamp: Stamp }) {
  return (
    <p className="stamp">
      As of revision {stamp.revision}, <Ago at={stamp.as_of} />.
    </p>
  );
}

export function Fact({ name, mono = false, children }: { name: string; mono?: boolean; children: ReactNode }) {
  return (
    <div>
      <dt>{name}</dt>
      <dd className={mono ? "mono" : undefined}>{children}</dd>
    </div>
  );
}

// Section is a part of a view a screen reader can jump to.
export function Section({ title, children }: { title: ReactNode; children: ReactNode }) {
  const heading = useId();
  return (
    <section className="group" aria-labelledby={heading}>
      <h2 className="group-title" id={heading}>
        {title}
      </h2>
      {children}
    </section>
  );
}

// Line is one row of a lane of work: what stands at the end of its rung, and a card.
export function Line({ side, kind, title, plain = false, children }: {
  side?: ReactNode;
  kind: ReactNode;
  title: ReactNode;
  // plain draws no rung, which is for a line no person can do anything about.
  plain?: boolean;
  children?: ReactNode;
}) {
  const heading = useId();
  return (
    <li className={plain ? "need need-plain" : "need"}>
      <article aria-labelledby={heading}>
        <div className="wait">
          {side}
          <span className="wait-kind">{kind}</span>
        </div>
        <div className="card">
          <h2 className="card-title" id={heading}>
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
