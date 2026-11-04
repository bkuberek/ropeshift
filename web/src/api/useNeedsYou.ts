import { useCallback, useEffect, useRef, useState } from "react";

import { ApiError, followEvents, readNeedsYou } from "./client";
import type { NeedsYou } from "./types";

export type Connection = "connecting" | "live" | "reconnecting";

type Read = {
  view: NeedsYou | null;
  // readAt says when this browser received the view, which turns its age into a wait that runs on.
  readAt: number;
  failure: string;
  needsSession: boolean;
  connection: Connection;
  // heard counts what happened since the page opened, so any other view knows to read again.
  heard: number;
};

// A burst of events is one change to a person, so one read follows it. A burst that never ends still
// reads at this bound, so a busy fleet cannot hold a page on an old read.
const settleMs = 80;
const settleBoundMs = 2000;
const reopenMs = 2000;

export function useNeedsYou(): Read & { reload: () => Promise<void>; checkSession: () => Promise<void>; forget: () => void } {
  const [read, setRead] = useState<Read>({ view: null, readAt: 0, failure: "", needsSession: false, connection: "connecting", heard: 0 });
  const revision = useRef<number | null>(null);

  // checkSession reads this view alone, which is how a view that was refused learns why.
  const checkSession = useCallback(async () => {
    try {
      const view = await readNeedsYou();
      revision.current = view.revision;
      setRead((was) => ({ ...was, view, readAt: Date.now(), failure: "", needsSession: false }));
    } catch (error) {
      const refused = error instanceof ApiError ? error : new ApiError(0, "unavailable", String(error));
      setRead((was) => ({
        ...was,
        needsSession: refused.code === "session_required",
        failure: refused.code === "session_required" ? "" : refused.message,
      }));
    }
  }, []);

  const reload = useCallback(async () => {
    await checkSession();
    setRead((was) => ({ ...was, heard: was.heard + 1 }));
  }, [checkSession]);

  // A session that ran out keeps the page behind a sign-in, for whoever comes back to finish. Signing
  // out says the opposite, so nothing of the ledger stays in the page.
  const forget = useCallback(() => {
    revision.current = null;
    setRead((was) => ({ ...was, view: null, needsSession: true, failure: "", connection: "connecting" }));
  }, []);

  useEffect(() => {
    void checkSession();
  }, [checkSession]);

  const ready = read.view !== null && !read.needsSession;
  useEffect(() => {
    if (!ready) {
      return;
    }
    let source: EventSource;
    let settling: ReturnType<typeof setTimeout> | undefined;
    let reopening: ReturnType<typeof setTimeout> | undefined;
    let firstHeard = 0;
    const connection = (state: Connection) => setRead((was) => ({ ...was, connection: state }));
    const settled = () => {
      firstHeard = 0;
      void reload();
    };
    const open = () => {
      source = followEvents(revision.current ?? 0);
      source.addEventListener("ledger", () => {
        const now = Date.now();
        firstHeard = firstHeard === 0 ? now : firstHeard;
        clearTimeout(settling);
        settling = setTimeout(settled, Math.min(settleMs, Math.max(0, firstHeard + settleBoundMs - now)));
      });
      source.onopen = () => connection("live");
      source.onerror = () => {
        connection("reconnecting");
        if (source.readyState === EventSource.CLOSED) {
          // The stream ended for good, which a session that ended does. A read says which.
          void reload();
          reopening = setTimeout(open, reopenMs);
        }
      };
    };
    open();
    return () => {
      clearTimeout(settling);
      clearTimeout(reopening);
      source.close();
    };
  }, [ready, reload]);

  return { ...read, reload, checkSession, forget };
}
