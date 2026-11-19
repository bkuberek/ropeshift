import { useEffect, useState } from "react";

import { useChange } from "../admin/parts";
import { checkPractices, readPracticesCheck } from "../api/practices";
import type { PracticesRequest, PracticesView } from "../api/practices";
import { pollMs } from "./logic";

// useCheck asks for the practices check and follows each request that is open: it reads the request once a second until
// the request is reported or expired, and then reads the list again, whose result is the report. It keeps what an ask
// answered for a repository that was not asked, and the repositories whose request expired, until the next ask.
export function useCheck(views: PracticesView[], again: () => void) {
  const change = useChange();
  // The state each request was last read in, when it was no longer offered.
  const [ended, setEnded] = useState<Record<string, string>>({});
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const [expired, setExpired] = useState<Record<string, boolean>>({});

  const open = views.flatMap((view) => (view.request?.state === "offered" && !(view.request.id in ended) ? [view.request] : []));
  const key = open.map((request) => request.id).join(",");

  useEffect(() => {
    if (open.length === 0) {
      return;
    }
    let stopped = false;
    const timer = setInterval(() => {
      for (const request of open) {
        void readPracticesCheck(request.id).then(
          (read: PracticesRequest) => {
            if (stopped || read.state === "offered") {
              return;
            }
            setEnded((was) => ({ ...was, [request.id]: read.state }));
            if (read.state === "expired") {
              setExpired((was) => ({ ...was, [request.repository]: true }));
            }
            again();
          },
          () => undefined,
        );
      }
    }, pollMs);
    return () => {
      stopped = true;
      clearInterval(timer);
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
