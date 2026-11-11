import { useContext, useRef, useState } from "react";

import { ApiError, inProjectOf } from "../api/client";
import { ItemProject } from "../app/route";

// useAct runs one change at a time, so a second press cannot send a second decision, and it keeps what
// the server refused in words a person can act on.
export function useAct(after: () => Promise<void>) {
  const project = useContext(ItemProject);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  const running = useRef(false);

  const act = async (change: () => Promise<unknown>): Promise<boolean> => {
    if (running.current) {
      return false;
    }
    running.current = true;
    setBusy(true);
    setFailure("");
    try {
      await (project ? inProjectOf(project, change) : change());
      await after();
      return true;
    } catch (error) {
      setFailure(error instanceof ApiError ? error.message : String(error));
      await after();
      return false;
    } finally {
      running.current = false;
      setBusy(false);
    }
  };

  return { act, busy, failure };
}
