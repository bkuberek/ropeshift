import { useEffect, useRef, useState } from "react";

import { useChange } from "../admin/parts";
import { ApiError } from "../api/client";
import { checkPractices, readPracticesCheck } from "../api/practices";
import type { PracticesView } from "../api/practices";
import type { Live } from "../api/useView";
import { pollMs } from "./logic";

// useCheck asks for the practices check and follows each request that is open: it reads the request once a second until
// the request is reported or expired, and then reads the list again, whose result is the report. It follows as the floor's
// installation read does: a hidden tab shows nobody the check, so it reads nothing and reads once when the tab is shown
// again, and one read is out at a time. A read the session refused goes to the shell, which owns the sign-in. It keeps what
// an ask answered for a repository that was not asked, and the repositories whose request expired, until the next ask.
export function useCheck(views: PracticesView[], again: () => void, live: Live) {
  const change = useChange();
  // The state each request was last read in, once it was no longer offered.
  const [ended, setEnded] = useState<Record<string, string>>({});
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const [expired, setExpired] = useState<Record<string, boolean>>({});
  const latest = useRef({ again, checkSession: live.checkSession });
  latest.current = { again, checkSession: live.checkSession };

  const open = views.flatMap((view) => (view.request?.state === "offered" && !(view.request.id in ended) ? [view.request] : []));
  const key = open.map((request) => request.id).join(",");

  useEffect(() => {
    if (open.length === 0) {
      return;
    }
    let stopped = false;
    let out = false;
    let timer: ReturnType<typeof setInterval> | undefined;
    const poll = async () => {
      if (out || stopped) {
        return;
      }
      out = true;
      try {
        for (const request of open) {
          if (stopped) {
            return;
          }
          try {
            const read = await readPracticesCheck(request.id);
            if (stopped || read.state === "offered") {
              continue;
            }
            setEnded((was) => ({ ...was, [request.id]: read.state }));
            if (read.state === "expired") {
              setExpired((was) => ({ ...was, [request.repository]: true }));
            }
            latest.current.again();
          } catch (error) {
            if (error instanceof ApiError && error.code === "session_required") {
              await latest.current.checkSession();
              return;
            }
            // Any other failure is read again at the next tick.
          }
        }
      } finally {
        out = false;
      }
    };
    const follow = () => {
      clearInterval(timer);
      timer = document.visibilityState === "hidden" ? undefined : setInterval(() => void poll(), pollMs);
    };
    const shown = () => {
      follow();
      if (document.visibilityState !== "hidden") {
        void poll();
      }
    };
    follow();
    document.addEventListener("visibilitychange", shown);
    return () => {
      stopped = true;
      clearInterval(timer);
      document.removeEventListener("visibilitychange", shown);
    };
  }, [key]);

  // run asks for a check of the repositories named, or of every active repository when none is.
  const run = async (repositories: string[]) => {
    const answer = await change.run(() => checkPractices(repositories));
    if (!answer) {
      return;
    }
    setExpired((was) => (repositories.length === 0 ? {} : Object.fromEntries(Object.entries(was).filter(([repository]) => !repositories.includes(repository)))));
    setReasons(Object.fromEntries((answer.checks ?? []).filter((entry) => entry.reason).map((entry) => [entry.repository, entry.reason ?? ""])));
    again();
  };

  return { run, busy: change.busy, failure: change.failure, ended, reasons, expired };
}

export type Check = ReturnType<typeof useCheck>;
