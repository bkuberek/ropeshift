import { createContext, useContext, useEffect } from "react";

import type { Entry } from "./ranking";

// The shell holds the actions the page in view lists in the command palette, and a page hands them over.
const Publish = createContext<(entries: Entry[]) => void>(() => {});

export const PageActionsProvider = Publish.Provider;

// usePageActions lists actions of the page in view in the command palette for as long as the page stands.
// An entry runs something in place, so it holds the page's own state, which is why the page lists it.
export function usePageActions(entries: Entry[]): void {
  const publish = useContext(Publish);
  const key = entries.map((entry) => `${entry.key}\n${entry.title}`).join("\n\n");
  useEffect(() => {
    publish(entries);
    return () => publish([]);
  }, [key, publish]);
}
