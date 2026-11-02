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
};

// A burst of events is one change to a person, so one read follows it.
const settleMs = 80;
const reopenMs = 2000;

export function useNeedsYou(): Read & { reload: () => Promise<void>; forget: () => void } {
  const [read, setRead] = useState<Read>({ view: null, readAt: 0, failure: "", needsSession: false, connection: "connecting" });
  const revision = useRef<number | null>(null);

  const reload = useCallback(async () => {
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

  // A session that ran out keeps the page behind a sign-in, for whoever comes back to finish. Signing
  // out says the opposite, so nothing of the ledger stays in the page.
  const forget = useCallback(() => {
    revision.current = null;
    setRead((was) => ({ ...was, view: null, needsSession: true, failure: "", connection: "connecting" }));
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const ready = read.view !== null && !read.needsSession;
  useEffect(() => {
    if (!ready) {
      return;
    }
    let source: EventSource;
    let settling: ReturnType<typeof setTimeout> | undefined;
    let reopening: ReturnType<typeof setTimeout> | undefined;
    const connection = (state: Connection) => setRead((was) => ({ ...was, connection: state }));
    const open = () => {
      source = followEvents(revision.current ?? 0);
      source.addEventListener("ledger", () => {
        clearTimeout(settling);
        settling = setTimeout(() => void reload(), settleMs);
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

  return { ...read, reload, forget };
}
