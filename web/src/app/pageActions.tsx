import type { ReactNode } from "react";
import { createContext, useCallback, useContext, useEffect, useId, useRef } from "react";

import type { Entry } from "./ranking";

// The shell holds the actions the page in view lists in the command palette, and a page hands them over. A page
// may have several parts that each list their own, so each part hands over under a name of its own, and the
// shell holds what they all handed over.
type Hand = (owner: string, entries: Entry[]) => void;

const Publish = createContext<Hand>(() => {});

// PageActionsProvider keeps what each part of the page handed over, and gives the shell all of it at once.
export function PageActionsProvider({ value, children }: { value: (entries: Entry[]) => void; children: ReactNode }) {
  const held = useRef(new Map<string, Entry[]>());
  const hand = useCallback<Hand>(
    (owner, entries) => {
      if (entries.length === 0) {
        held.current.delete(owner);
      } else {
        held.current.set(owner, entries);
      }
      value([...held.current.values()].flat());
    },
    [value],
  );
  return <Publish.Provider value={hand}>{children}</Publish.Provider>;
}

// usePageActions lists actions of the page in view in the command palette for as long as the part that lists them
// stands. An entry runs something in place, so it holds that part's own state, which is why the part lists it.
export function usePageActions(entries: Entry[]): void {
  const hand = useContext(Publish);
  const owner = useId();
  const key = entries.map((entry) => `${entry.key}\n${entry.title}`).join("\n\n");
  useEffect(() => {
    hand(owner, entries);
    return () => hand(owner, []);
  }, [key, hand, owner]);
}
