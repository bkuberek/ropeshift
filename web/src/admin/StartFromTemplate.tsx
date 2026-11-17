import { useEffect, useRef, useState } from "react";

import { createProject, protocolTemplateWrites, readTemplateWrite, readWorkers, writeTemplates } from "../api/admin";
import type { TemplateRepository, TemplateShape, TemplateWriteRequest, TemplateWriteView } from "../api/admin";
import { readView } from "../api/client";
import type { Project, ShopFloor } from "../api/types";
import { go } from "../app/route";
import { Button, Segmented } from "../ui/controls";
import { Banner, Refusal, SkeletonRows } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { useChange } from "./parts";

// How often the dialog reads a request it waits on, and how long past its expiry it keeps reading, in milliseconds.
export const pollMs = 1000;
const graceMs = 5000;

// What each shape is, in a line, as `ropeshift new --help` says it.
const shapes: { value: TemplateShape; label: string; says: string }[] = [
  { value: "workspace", label: "Workspace", says: "The planning repository of a project of several repositories, whose code repositories are cloned into its ignored repos/ folder." },
  { value: "monorepo", label: "Monorepo", says: "One repository that holds the plan and the code." },
  { value: "repository", label: "Repository", says: "A code repository of a project that plans in another repository." },
];

// projectNameOf makes a project name of a folder's name, as `ropeshift new` does: lowercase letters and digits, with a hyphen for each
// run of anything else.
export function projectNameOf(folder: string): string {
  const base = folder.split("/").filter(Boolean).pop() ?? "";
  const name = base
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return name.length > 63 ? name.slice(0, 63).replace(/-+$/, "") : name;
}

// folderProblem says what is wrong with a folder before it is sent, or nothing: the field refuses a relative path and one with a ..
// element, because the worker would refuse them.
export function folderProblem(folder: string): string {
  const path = folder.trim();
  if (!path) {
    return "Enter an absolute folder on that worker, such as /srv/code/harbor.";
  }
  if (!path.startsWith("/")) {
    return "That is a relative path. Enter the folder's absolute path on that worker's machine.";
  }
  if (path.split("/").includes("..")) {
    return "That path holds a .. element. Name the folder itself.";
  }
  if ((path.length > 1 && path.endsWith("/")) || path.includes("//") || path.split("/").includes(".")) {
    return "Write the path with no trailing slash, //, or /./ in it.";
  }
  return "";
}

type Choice = { id: string; name: string; connected: boolean | null; old: boolean };

// useWorkers reads the workers the installation holds, with whether the daemon sees each now and whether it speaks a protocol that reads
// the request, once the dialog opens. Workers the daemon sees now come first.
function useWorkers(open: boolean): { choices: Choice[]; failure: string; ready: boolean } {
  const [read, setRead] = useState<{ choices: Choice[]; failure: string; ready: boolean }>({ choices: [], failure: "", ready: false });
  useEffect(() => {
    if (!open) {
      return;
    }
    let current = true;
    setRead({ choices: [], failure: "", ready: false });
    void (async () => {
      try {
        const [list, floor] = await Promise.all([readWorkers(), readView<ShopFloor>("shop-floor").catch(() => null)]);
        const choices = list.workers.map<Choice>((worker) => ({
          id: worker.id,
          name: worker.name || worker.id,
          connected: floor ? floor.workers.find((line) => line.worker.id === worker.id)?.freshness === "observed" : null,
          old: (worker.protocol ?? 0) < protocolTemplateWrites,
        }));
        // The workers the daemon sees now come first, then the others that can take the request, then those that cannot.
        const rank = (choice: Choice) => (choice.connected ? 0 : 1);
        const sorted = choices.map((choice, at) => ({ choice, at })).sort((a, b) => rank(a.choice) - rank(b.choice) || Number(a.choice.old) - Number(b.choice.old) || a.at - b.at);
        if (current) {
          setRead({ choices: sorted.map((row) => row.choice), failure: "", ready: true });
        }
      } catch (error) {
        if (current) {
          setRead({ choices: [], failure: error instanceof Error ? error.message : String(error), ready: true });
        }
      }
    })();
    return () => {
      current = false;
    };
  }, [open]);
  return read;
}

const optionLabel = (choice: Choice) =>
  choice.old
    ? `${choice.name} · Upgrade this worker to write files from the web UI`
    : [choice.name, choice.connected === null ? "" : choice.connected ? "connected" : "last reported"].filter(Boolean).join(" · ");

// copy puts text on the clipboard, and says when the browser did not let the page.
function useCopy() {
  const toast = useToast();
  return (text: string, what: string) =>
    void navigator.clipboard?.writeText(text).then(
      () => toast("success", `Copied ${what}.`),
      () => toast("warning", "The browser did not let this page copy. Select the text and copy it."),
    );
}

type Phase = { kind: "form" } | { kind: "sent"; view: TemplateWriteView };

// StartFromTemplate is the dialog that writes a shape's starting files on a worker the member picks, as `ropeshift new` writes them on
// this machine (FR-TPL-5, FR-UI-7): the worker, the shape, the folder, and the options. It waits for the worker's report and says
// what became of the request. Registering what was written is a separate act it hands to the Repositories view (FR-UI-8).
export function StartFromTemplate({ open, onClose, projects, reload }: { open: boolean; onClose: () => void; projects: Project[]; reload: () => Promise<void> }) {
  const found = useWorkers(open);
  const change = useChange();
  const copy = useCopy();
  const [worker, setWorker] = useState("");
  const [shape, setShape] = useState<TemplateShape>("workspace");
  const [folder, setFolder] = useState("");
  const [rows, setRows] = useState<TemplateRepository[]>([]);
  const [project, setProject] = useState("");
  const [firstCommit, setFirstCommit] = useState(true);
  const [problem, setProblem] = useState("");
  const [rowProblem, setRowProblem] = useState("");
  const [phase, setPhase] = useState<Phase>({ kind: "form" });
  const [lapsed, setLapsed] = useState(false);
  const [readFailure, setReadFailure] = useState("");
  const folderField = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) {
      setPhase({ kind: "form" });
      setProblem("");
      setRowProblem("");
      setLapsed(false);
      setReadFailure("");
      change.setFailure("");
    }
  }, [open]);

  // The first worker that can take the request stands chosen, so a person with one worker has nothing to pick.
  useEffect(() => {
    const first = found.choices.find((choice) => !choice.old);
    setWorker((was) => (was && found.choices.some((choice) => choice.id === was && !choice.old) ? was : (first?.id ?? "")));
  }, [found.choices]);

  const nameOf = (id: string) => found.choices.find((choice) => choice.id === id)?.name ?? id;
  const view = phase.kind === "sent" ? phase.view : null;

  // While the request is offered the dialog reads it once a second, until the worker settles it or it expires.
  useEffect(() => {
    if (!open || !view || view.state !== "offered") {
      return;
    }
    let current = true;
    const timer = window.setTimeout(async () => {
      if (Date.now() >= view.expires_at + graceMs) {
        setLapsed(true);
        return;
      }
      try {
        const next = await readTemplateWrite(view.worker, view.id);
        if (current) {
          setReadFailure("");
          setPhase({ kind: "sent", view: next });
        }
      } catch (error) {
        if (current) {
          setReadFailure(error instanceof Error ? error.message : String(error));
          setPhase({ kind: "sent", view: { ...view } });
        }
      }
    }, pollMs);
    return () => {
      current = false;
      window.clearTimeout(timer);
    };
  }, [open, view]);

  // A refusal comes back to the form, with the reason and the detail at the Folder field and everything typed kept.
  useEffect(() => {
    if (view?.state === "refused" && view.report) {
      setProblem(`${view.report.detail || "The worker refused the folder."} (${view.report.reason ?? "refused"})`);
      setPhase({ kind: "form" });
      folderField.current?.focus();
    }
  }, [view]);

  const name = shape === "repository" && project.trim() ? project.trim() : projectNameOf(folder);

  const send = async () => {
    const wrong = folderProblem(folder);
    setProblem(wrong);
    setRowProblem("");
    if (wrong) {
      folderField.current?.focus();
      return;
    }
    const kept = shape === "workspace" ? rows.map((row) => ({ name: row.name.trim(), remote: row.remote.trim() })).filter((row) => row.name || row.remote) : [];
    if (kept.some((row) => !row.name || !row.remote)) {
      setRowProblem("Give each code repository a name and a remote, or remove its row.");
      return;
    }
    const body: TemplateWriteRequest = { shape, path: folder.trim(), first_commit: firstCommit };
    if (kept.length > 0) {
      body.repositories = kept;
    }
    if (shape === "repository" && project.trim()) {
      body.project = project.trim();
    }
    const sent = await change.run(() => writeTemplates(worker, body));
    if (sent) {
      setLapsed(false);
      setPhase({ kind: "sent", view: sent });
    }
  };

  // The hand-off: create the project when none has the name, then open the Repositories view's Register dialog on the folder.
  const register = async (written: TemplateWriteView) => {
    const target = written.project || written.report?.project || "";
    const done = await change.run(async () => {
      if (!projects.some((row) => row.name === target)) {
        await createProject(target, "");
        await reload();
      }
      return true;
    });
    if (done) {
      onClose();
      go(`/projects/${encodeURIComponent(target)}/repositories?dialog=register&folder=${encodeURIComponent(written.path)}&worker=${encodeURIComponent(nameOf(written.worker))}`);
    }
  };

  const waiting = view?.state === "offered" && !lapsed;
  const expired = view?.state === "expired" || (view?.state === "offered" && lapsed);
  const written = view?.state === "written" ? view : null;
  const sendDisabled = !worker || !found.ready;

  const footer = written ? (
    <>
      <span className="spacer" />
      <Button onClick={onClose}>Close</Button>
      {written.registrable ? (
        <Button variant="primary" loading={change.busy} onClick={() => void register(written)}>
          {projects.some((row) => row.name === (written.project || written.report?.project)) ? "Register" : `Create project ${written.project || written.report?.project} and register`}
        </Button>
      ) : null}
    </>
  ) : waiting ? (
    <>
      <span className="spacer" />
      <Button onClick={onClose}>Close</Button>
    </>
  ) : expired ? (
    <>
      <span className="spacer" />
      <Button onClick={onClose}>Close</Button>
      <Button variant="primary" onClick={() => setPhase({ kind: "form" })}>
        Back to the form
      </Button>
    </>
  ) : (
    <>
      <span className="spacer" />
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" icon="play" loading={change.busy} disabled={sendDisabled} onClick={() => void send()}>
        Write files
      </Button>
    </>
  );

  return (
    <Dialog open={open} wide title="Start a project from a template" onClose={onClose} footer={footer}>
      {waiting && view ? (
        <div className="field-stack" role="status">
          <p>Waiting for {nameOf(view.worker)}</p>
          <p className="small muted">
            It writes the files for <code>{view.path}</code> and reports. Nothing is registered until you ask.
          </p>
          <SkeletonRows rows={2} />
          {readFailure ? <Refusal>{readFailure}</Refusal> : null}
        </div>
      ) : expired && view ? (
        <Banner tone="warning">{`${nameOf(view.worker)} did not take the request within two minutes. It is no longer offered.`}</Banner>
      ) : written && written.report ? (
        <>
          <Written view={written} worker={nameOf(written.worker)} copy={copy} />
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        </>
      ) : (
        <form
          className="field-stack"
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            void send();
          }}
        >
          <Field
            label="Worker"
            help="The machine that writes the files. Workers the daemon sees now come first."
            error={found.failure ? `The workers could not be read. ${found.failure}` : found.ready && found.choices.length === 0 ? "No worker has joined." : undefined}
          >
            <Select value={worker} onChange={(event) => setWorker(event.target.value)}>
              <option value="">{found.ready ? "Choose a worker" : "Reading…"}</option>
              {found.choices.map((choice) => (
                <option key={choice.id} value={choice.id} disabled={choice.old}>
                  {optionLabel(choice)}
                </option>
              ))}
            </Select>
          </Field>
          <div className="field">
            <span className="label" id="template-shape-label">
              Shape
            </span>
            <Segmented label="Shape" value={shape} choices={shapes.map(({ value, label }) => ({ value, label }))} onChange={setShape} />
            <ul className="plain-list small">
              {shapes.map((row) => (
                <li key={row.value} className={row.value === shape ? undefined : "muted"}>
                  <strong>{row.label}.</strong> {row.says}
                </li>
              ))}
            </ul>
          </div>
          <Field label="Folder" help="An absolute path on that worker's machine. The folder is made when it is absent, and must be empty when it exists." error={problem || undefined}>
            <input
              ref={folderField}
              className="input mono"
              autoComplete="off"
              spellCheck={false}
              placeholder="/srv/code/harbor…"
              value={folder}
              onChange={(event) => {
                setProblem("");
                setFolder(event.target.value);
              }}
            />
          </Field>
          {shape === "workspace" ? (
            <fieldset className="field plain-fieldset">
              <legend className="label">Code repositories</legend>
              <p className="help">Each is cloned into repos/&lt;name&gt; by the workspace's Makefile, and listed in its manifest.</p>
              {rows.map((row, at) => (
                <div className="repo-row" key={at}>
                  <Field label="Name">
                    <input
                      className="input mono"
                      autoComplete="off"
                      spellCheck={false}
                      value={row.name}
                      onChange={(event) => setRows(rows.map((other, index) => (index === at ? { ...other, name: event.target.value } : other)))}
                    />
                  </Field>
                  <Field label="Remote">
                    <input
                      className="input mono"
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="git@github.com:acme/api.git…"
                      value={row.remote}
                      onChange={(event) => setRows(rows.map((other, index) => (index === at ? { ...other, remote: event.target.value } : other)))}
                    />
                  </Field>
                  <Button small aria-label={`Remove repository ${at + 1}`} onClick={() => setRows(rows.filter((_, index) => index !== at))}>
                    Remove
                  </Button>
                </div>
              ))}
              {rowProblem ? <Refusal>{rowProblem}</Refusal> : null}
              <Button small icon="plus" onClick={() => setRows([...rows, { name: "", remote: "" }])}>
                Add
              </Button>
            </fieldset>
          ) : null}
          {shape === "repository" ? (
            <Field label="Project" help="The project the printed commands register the repository in. Leave it empty to use the folder's name.">
              <input className="input mono" autoComplete="off" spellCheck={false} value={project} onChange={(event) => setProject(event.target.value)} />
            </Field>
          ) : null}
          <label className="check-row">
            <input type="checkbox" checked={firstCommit} onChange={(event) => setFirstCommit(event.target.checked)} />
            <span>
              Make the first commit
              <span className="help">
                Runs git init, adds the files, and commits them as <code>chore: start {name || "<name>"}</code>
              </span>
            </span>
          </label>
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
          <button type="submit" hidden />
        </form>
      )}
    </Dialog>
  );
}

// Written is what a worker made: the files, the first commit or why it failed, and the commands that register the folder, each with
// Copy, and what the hand-off to registration needs or why the web UI cannot make it.
function Written({ view, worker, copy }: { view: TemplateWriteView; worker: string; copy: (text: string, what: string) => void }) {
  const report = view.report;
  if (!report) {
    return null;
  }
  const files = report.files ?? [];
  const commands = report.commands ?? [];
  const folder = view.path;
  const target = view.project || report.project;
  return (
    <div className="field-stack">
      <Banner tone="active" icon="check">{`${worker} wrote ${files.length} ${files.length === 1 ? "file" : "files"} in ${folder}.`}</Banner>
      <ul className="plain-list mono small" aria-label="Files written">
        {files.map((file) => (
          <li key={file}>{file}</li>
        ))}
      </ul>
      {report.commit ? (
        <p className="small">
          First commit <code>{report.commit}</code>
        </p>
      ) : null}
      {report.commit_error ? <Refusal>{`The first commit failed, and the files stay: ${report.commit_error}`}</Refusal> : null}
      {commands.length > 0 ? (
        <div>
          <p className="small">{view.registrable ? "The commands that register it from a terminal are:" : "Run these on that machine:"}</p>
          <ul className="plain-list" aria-label="Commands">
            {commands.map((command) => (
              <li className="row" key={command}>
                <code className="mono small">{command}</code>
                <span className="spacer" />
                <Button small icon="copy" aria-label={`Copy ${command}`} onClick={() => copy(command, command)}>
                  Copy
                </Button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {view.registrable ? (
        <div>
          <p className="small">
            Register opens the Register dialog on {target ? <code>{target}</code> : "the project"}'s Repositories view with this folder filled, at its dry run. Registering is a separate step you make there.
          </p>
          {view.shape === "workspace" && (view.repositories ?? []).length > 0 ? (
            <p className="small">
              Clone the code repositories with <code>make -C {folder} setup</code> on {worker}, then register the folder of clones.
            </p>
          ) : null}
        </div>
      ) : (
        <p className="small muted">
          {report.clone
            ? `The web UI registers only folders on the worker this daemon runs, and ${worker} is another machine's. Run the commands above there.`
            : "The folder is not a clone yet, so it cannot be registered. Run the commands above on that machine."}
        </p>
      )}
    </div>
  );
}
