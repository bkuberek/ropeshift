import { useEffect, useRef, useState } from "react";

import { readRepositories, readSources, readWorkers } from "../api/admin";
import type { WorkerRecord } from "../api/admin";
import { inProjectOf } from "../api/client";
import { addressOf, go, inProject } from "../app/route";
import { useChange } from "../admin/parts";
import { Button } from "../ui/controls";
import { Banner, Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog } from "../ui/overlays";
import { startPlanningSession } from "./session";

// A goal is a few pages. The request that carries it holds a megabyte, so a file much larger than any goal
// is refused here, in words, and not by a failed call.
export const maxGoalBytes = 256 * 1024;

type Option = { value: string; label: string };

function readText(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error ?? new Error("the file could not be read"));
    reader.readAsText(file);
  });
}

// StartSessionDialog starts a planning session as `ropeshift plan start` does: the goal as text or a file read
// in the browser, the repository to plan in, and the worker that runs the session.
export function StartSessionDialog({ project, onClose }: { project: string; onClose: () => void }) {
  const [goal, setGoal] = useState("");
  const [fileName, setFileName] = useState("");
  const [fileFailure, setFileFailure] = useState("");
  const [repositories, setRepositories] = useState<Option[] | null>(null);
  const [repository, setRepository] = useState("");
  const [workers, setWorkers] = useState<WorkerRecord[] | null>(null);
  const [worker, setWorker] = useState("");
  const [chosen, setChosen] = useState(false);
  const change = useChange();
  const field = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    let current = true;
    void inProjectOf(project, async () => {
      const [list, sources, registered] = await Promise.all([readRepositories().catch(() => null), readSources().catch(() => null), readWorkers().catch(() => null)]);
      if (!current) {
        return;
      }
      // A project plans in its planning repository, else in the one repository it has; a project that
      // registers none plans in a source the daemon reads, as the command does.
      const held = (list?.repositories ?? []).filter((row) => row.state !== "removed");
      const options: Option[] = held.length
        ? held.map((row) => ({ value: row.id, label: `${row.name || row.id}${row.planning || row.plan_of_record ? " (planning)" : ""}` }))
        : (sources?.sources ?? []).map((row) => ({ value: row.instance, label: row.instance }));
      setRepositories(options);
      const planning = held.find((row) => row.planning || row.plan_of_record);
      setRepository((was) => was || planning?.id || (options.length === 1 ? (options[0]?.value ?? "") : ""));
      setWorkers(registered?.workers ?? []);
      setWorker((was) => was || (registered?.workers.length === 1 ? (registered.workers[0]?.id ?? "") : ""));
    });
    return () => {
      current = false;
    };
  }, [project]);

  const choose = async (file: File | undefined) => {
    setFileFailure("");
    if (!file) {
      return;
    }
    if (file.size > maxGoalBytes) {
      setFileFailure(`${file.name} is ${Math.ceil(file.size / 1024)} KiB. A goal is a few pages: choose a file under ${maxGoalBytes / 1024} KiB.`);
      return;
    }
    try {
      setGoal(await readText(file));
      setFileName(file.name);
    } catch (error) {
      setFileFailure(error instanceof Error ? error.message : String(error));
    }
  };

  const start = async () => {
    if (!worker) {
      setChosen(true);
      return;
    }
    const started = await change.run(() => startPlanningSession(project, { goal, repository, worker }));
    if (started) {
      onClose();
      go(inProject(project, addressOf("/planning", { lane: started.lane })));
    }
  };

  const nothing = repositories !== null && repositories.length === 0;
  return (
    <Dialog
      open
      title="Start planning session"
      onClose={onClose}
      initialFocus={field}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={change.busy} disabled={nothing} onClick={() => void start()}>
            Start planning session
          </Button>
        </>
      }
    >
      <form
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void start();
        }}
      >
        <p className="small muted">
          The session writes the plan and its draft decisions to a branch of its own. Nothing reaches a default branch until you approve the plan.
        </p>
        <Field label="Goal" help="What the plan is for. Leave it empty, and the session asks.">
          <textarea ref={field} className="textarea" rows={5} autoComplete="off" placeholder="Saved searches, with alerts…" value={goal} onChange={(event) => setGoal(event.target.value)} />
        </Field>
        <Field label="Or read the goal from a file" help={fileName ? `Read from ${fileName}. Edit it above before you start.` : "The file is read in your browser and fills the goal."} error={fileFailure || undefined}>
          <input className="input" type="file" accept=".md,.txt,text/plain,text/markdown" onChange={(event) => void choose(event.target.files?.[0])} />
        </Field>
        {nothing ? (
          <Banner tone="warning">This project has no repository to plan in. Register one in Repositories first.</Banner>
        ) : (
          <Field label="Planning repository" help="Where the session writes the plan.">
            <Select value={repository} onChange={(event) => setRepository(event.target.value)}>
              <option value="">{repositories === null ? "Reading…" : "The project's planning repository"}</option>
              {(repositories ?? []).map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
          </Field>
        )}
        <Field label="Worker" help="The machine that runs the session." error={chosen && !worker ? "Choose the worker that runs the session." : undefined}>
          <Select value={worker} onChange={(event) => setWorker(event.target.value)}>
            <option value="">{workers === null ? "Reading…" : workers.length === 0 ? "No worker has registered" : "Choose a worker"}</option>
            {(workers ?? []).map((row) => (
              <option key={row.id} value={row.id}>
                {row.name || row.id}
              </option>
            ))}
          </Select>
        </Field>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </form>
    </Dialog>
  );
}

// StartSession is the button that opens the dialog, wherever a view offers to start a session.
export function StartSession({ project, variant = "secondary", label = "Start planning session" }: { project: string; variant?: "primary" | "secondary"; label?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant={variant} icon="flag" onClick={() => setOpen(true)}>
        {label}
      </Button>
      {open ? <StartSessionDialog project={project} onClose={() => setOpen(false)} /> : null}
    </>
  );
}
