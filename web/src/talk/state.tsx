import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";

import type { Target, TalkKind, TalkState } from "./target";
import { announcement, assistantOf, drafting, empty, rememberDraft, selectTarget, targetKey, targetOf } from "./target";

type SelectOptions = {
  // name is what the announcement calls the target, such as the worker an orchestrator runs on.
  name?: string;
  // notice stands at the top of the target's surface, such as why the member was switched to it.
  notice?: string;
  // open shows the dock, and focus puts the keyboard in the composer: a control elsewhere on a view, which asked
  // to talk to a target, does both.
  open?: boolean;
  focus?: boolean;
  // quiet leaves a switch unannounced, for one the member's own act in the same conversation made.
  quiet?: boolean;
  // start asks the session of the conversation named by `id` to start, which choosing a conversation does. The
  // request is for that conversation of this project alone, and is answered once.
  start?: boolean;
};

export type Talk = {
  target: Target;
  // select switches the target. Only a choice in the switcher, in the list, a control that asks to talk to a
  // target, and the assistant's start of a planning session call it; navigation never does (FR-CHAT-11).
  select: (kind: TalkKind, id: string, options?: SelectOptions) => void;
  draftOf: (target: Target) => string;
  setDraft: (target: Target, text: string) => void;
  noticeOf: (target: Target) => string;
  clearNotice: (target: Target) => void;
  // askAgain puts a message in the composer of an assistant conversation and takes the member there, and
  // sends nothing.
  askAgain: (conversation: string, text: string) => void;
  // Something outside the surface asks it to take the keyboard in the composer, open the switcher, or start a
  // new conversation. The counts change on each ask, so the surface reads again, and `take` says an ask has not
  // been answered yet, so a surface that opens after the ask still answers it, once.
  focusRequest: number;
  menuRequest: number;
  askCount: number;
  requestFocus: () => void;
  requestMenu: () => void;
  requestNew: () => void;
  take: (what: Ask) => boolean;
  // takeAsk answers a request that starts a session, which changes the ledger, so it is held to more than
  // `take`: it says yes once, only for the project and conversation it was made for, and only while it is
  // fresh. A request that does not match is dropped, so it can never start something else later.
  takeAsk: (what: Started, project: string, conversation: string) => boolean;
};

export type Ask = "focus" | "menu";

// Started is a request to start a session: a new conversation's, or an existing one's.
export type Started = "new" | "resume";

// How long a request to start a session may wait for the surface to answer it.
const askFreshMs = 10_000;

type Pending = { what: Started; project: string; conversation: string; at: number };

const quiet: Talk = {
  target: assistantOf(""),
  select: () => {},
  draftOf: () => "",
  setDraft: () => {},
  noticeOf: () => "",
  clearNotice: () => {},
  askAgain: () => {},
  focusRequest: 0,
  menuRequest: 0,
  askCount: 0,
  requestFocus: () => {},
  requestMenu: () => {},
  requestNew: () => {},
  take: () => false,
  takeAsk: () => false,
};

const TalkContext = createContext<Talk>(quiet);

export const useTalk = (): Talk => useContext(TalkContext);

// TalkProvider holds what a member is talking to and what they have written, for the whole page, so a view
// changing never changes either. It announces a switch politely, and holds a notice a switch left.
export function TalkProvider({ project, openDock, children }: { project: string; openDock: () => void; children: ReactNode }) {
  const [state, setState] = useState<TalkState>(empty);
  const [said, setSaid] = useState("");
  const [notices, setNotices] = useState<Record<string, string>>({});
  const [focusRequest, setFocusRequest] = useState(0);
  const [menuRequest, setMenuRequest] = useState(0);
  const [askCount, setAskCount] = useState(0);
  const pending = useRef<Pending | null>(null);
  const asked = useRef<Record<Ask, number>>({ focus: 0, menu: 0 });
  const taken = useRef<Record<Ask, number>>({ focus: 0, menu: 0 });
  const ask = useCallback((what: Ask) => {
    asked.current[what] += 1;
    const count = asked.current[what];
    (what === "focus" ? setFocusRequest : setMenuRequest)(count);
  }, []);
  const start = useCallback(
    (next: Pending | null) => {
      pending.current = next;
      setAskCount((was) => was + 1);
    },
    [],
  );
  const takeAsk = useCallback((what: Started, at: string, conversation: string) => {
    const held = pending.current;
    if (!held || held.what !== what) {
      return false;
    }
    pending.current = null;
    return held.project === at && held.conversation === conversation && Date.now() - held.at <= askFreshMs;
  }, []);
  // A request belongs to the project it was made in, so changing the project drops it.
  useEffect(() => {
    pending.current = null;
  }, [project]);
  const take = useCallback((what: Ask) => {
    if (asked.current[what] > taken.current[what]) {
      taken.current[what] = asked.current[what];
      return true;
    }
    return false;
  }, []);
  const target = targetOf(state, project);

  const select = useCallback(
    (kind: TalkKind, id: string, options: SelectOptions = {}) => {
      const next: Target = { project, kind, id };
      setState((was) => selectTarget(was, next));
      if (!options.quiet) {
        setSaid(announcement(next, options.name));
      }
      if (options.notice) {
        setNotices((was) => ({ ...was, [targetKey(next)]: options.notice ?? "" }));
      }
      if (options.open) {
        openDock();
      }
      if (options.focus) {
        ask("focus");
      }
      // Choosing a target drops what an earlier choice asked, and asks for this one's session only when it
      // names a conversation.
      start(options.start && id ? { what: "resume", project, conversation: id, at: Date.now() } : null);
    },
    [project, openDock, ask, start],
  );

  const draftOf = useCallback((of: Target) => drafting(state, of), [state]);
  const setDraft = useCallback((of: Target, text: string) => setState((was) => rememberDraft(was, of, text)), []);
  const noticeOf = useCallback((of: Target) => notices[targetKey(of)] ?? "", [notices]);
  const clearNotice = useCallback(
    (of: Target) =>
      setNotices((was) => {
        const rest = { ...was };
        delete rest[targetKey(of)];
        return rest;
      }),
    [],
  );
  const askAgain = useCallback(
    (conversation: string, text: string) => {
      const next: Target = { project, kind: "assistant", id: conversation };
      setState((was) => rememberDraft(selectTarget(was, next), next, text));
      setSaid(announcement(next));
      openDock();
      ask("focus");
    },
    [project, openDock, ask],
  );
  const requestFocus = useCallback(() => ask("focus"), [ask]);
  const requestMenu = useCallback(() => ask("menu"), [ask]);
  const requestNew = useCallback(() => start({ what: "new", project, conversation: "", at: Date.now() }), [project, start]);

  const value = useMemo<Talk>(
    () => ({ target, select, draftOf, setDraft, noticeOf, clearNotice, askAgain, focusRequest, menuRequest, askCount, requestFocus, requestMenu, requestNew, take, takeAsk }),
    [target.project, target.kind, target.id, select, draftOf, setDraft, noticeOf, clearNotice, askAgain, focusRequest, menuRequest, askCount, requestFocus, requestMenu, requestNew, take, takeAsk],
  );
  return (
    <TalkContext.Provider value={value}>
      {children}
      <div className="visually-hidden talk-announce" aria-live="polite" aria-atomic="true">
        {said}
      </div>
    </TalkContext.Provider>
  );
}
