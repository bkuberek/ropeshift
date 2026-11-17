import { useEffect, useRef, useState } from "react";

import { ApiError } from "./client";

// Live is what the page knows of the ledger: what it has heard, and how to ask after a refused session.
export type Live = { heard: number; checkSession: () => Promise<void> };

export type ViewRead<T> = {
  view: T | null;
  readAt: number;
  failure: string;
  // refusedCode is what a failed read's refusal was, which a view decides on, and never on its text.
  refusedCode: string;
  // busy says a read is out, so what stands here may be about to change.
  busy: boolean;
};

const nothing = { view: null, readAt: 0, failure: "", refusedCode: "", busy: false };

// useView follows one read. A refused session asks the shell, which owns the sign-in. A failed read
// leaves the last answer standing beside the failure, because an old page says more than a blank one,
// and each answer but the newest is dropped, so a slow read cannot undo a later one. `key` names what
// is read, and `gapMs` holds a costly read to one per gap, however much a person hears. A read of something
// else, a new key, is not held. `waits` says what was read waits for something no ledger event announces, such as
// a forecast the daemon's tick keeps with no revision: while it does, the view asks again once a gap, heard or not.
export function useView<T>(read: () => Promise<T>, key: string, live: Live, gapMs = 0, waits?: (view: T) => boolean): ViewRead<T> {
  const [state, setState] = useState<ViewRead<T>>(nothing);
  const [again, setAgain] = useState(0);
  const asked = useRef(0);
  const lastRead = useRef(0);
  const lastKey = useRef(key);
  const latest = useRef(read);
  latest.current = read;
  const session = useRef(live.checkSession);
  session.current = live.checkSession;
  const waiting = waits !== undefined && state.view !== null && waits(state.view);

  useEffect(() => {
    if (!waiting) {
      return;
    }
    const timer = setInterval(() => setAgain((times) => times + 1), Math.max(gapMs, 1_000));
    return () => clearInterval(timer);
  }, [waiting, gapMs]);

  useEffect(() => {
    const run = async () => {
      asked.current += 1;
      const mine = asked.current;
      lastRead.current = Date.now();
      setState((was) => (was.view === null ? was : { ...was, busy: true }));
      try {
        const view = await latest.current();
        if (mine === asked.current) {
          setState({ view, readAt: Date.now(), failure: "", refusedCode: "", busy: false });
        }
      } catch (error) {
        if (mine !== asked.current) {
          return;
        }
        const refused = error instanceof ApiError ? error : new ApiError(0, "unavailable", String(error));
        if (refused.code === "session_required") {
          setState((was) => ({ ...was, busy: false }));
          await session.current();
          return;
        }
        setState((was) => ({ ...was, failure: refused.message, refusedCode: refused.code, busy: false }));
      }
    };
    const asksForOther = lastKey.current !== key;
    lastKey.current = key;
    const wait = lastRead.current === 0 || asksForOther ? 0 : lastRead.current + gapMs - Date.now();
    if (wait <= 0) {
      void run();
      return;
    }
    const held = setTimeout(() => void run(), wait);
    return () => clearTimeout(held);
  }, [key, live.heard, gapMs, again]);

  return state;
}
