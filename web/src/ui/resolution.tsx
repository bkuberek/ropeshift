import { createContext, useContext } from "react";

import type { StateLook } from "../floor/states";
import { Actor, StateBadge } from "./display";

// You is the member who reads the page, whom a decision of theirs names as "you".
export const You = createContext("");

const places: Record<string, string> = { web: "in Needs you", needs_you: "in Needs you", conversation: "in the conversation", cli: "from the CLI" };

// whereWords says the place a decision was made, which a record keeps as its channel. A place this build
// does not know says nothing rather than guess.
export function whereWords(channel = ""): string {
  return places[channel] ?? "";
}

// clockOf says the time of day, on a 24-hour clock.
export function clockOf(at: number): string {
  return new Date(at).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
}

type Props = {
  table: Record<string, StateLook>;
  state: string;
  // by is a principal, such as `member:member-1`, and channel where it decided.
  by?: string;
  channel?: string;
  at?: number;
};

// Resolved says how a record came out: its outcome, who decided it, where, and when. It stands in place of
// the record's buttons once it is decided, wherever the record is shown.
export function Resolved({ table, state, by = "", channel = "", at = 0 }: Props) {
  const you = useContext(You);
  const [kind = "", id = ""] = by.split(/:(.*)/);
  const where = whereWords(channel);
  return (
    <>
      <StateBadge table={table} state={state} />
      <span className="small muted">
        {by ? (
          <>
            by <Actor kind={kind} id={id} you={you} />
          </>
        ) : null}
        {where ? ` ${where}` : ""}
        {at ? ` at ${clockOf(at)}` : ""}
      </span>
    </>
  );
}
