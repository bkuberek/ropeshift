import { useCallback, useEffect, useState } from "react";

import { ApiError, readProjects } from "./client";
import type { Project } from "./types";

type Read = { list: Project[] | null; failure: string; needsSession: boolean };

// useProjects reads the installation's projects, which the switch lists and an address with no project
// chooses among. A refusal for want of a session asks for one.
export function useProjects(): Read & { reload: () => Promise<void> } {
  const [read, setRead] = useState<Read>({ list: null, failure: "", needsSession: false });
  const reload = useCallback(async () => {
    try {
      const { projects } = await readProjects();
      setRead({ list: projects, failure: "", needsSession: false });
    } catch (error) {
      const refused = error instanceof ApiError ? error : new ApiError(0, "unavailable", String(error));
      const needsSession = refused.code === "session_required";
      setRead((was) => ({ ...was, needsSession, failure: needsSession ? "" : refused.message }));
    }
  }, []);
  useEffect(() => {
    void reload();
  }, [reload]);
  return { ...read, reload };
}
