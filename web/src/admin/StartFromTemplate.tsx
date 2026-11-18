import { useEffect, useRef, useState } from "react";

import { createProject, protocolTemplateWrites, readTemplateWrite, readWorkers, writeTemplates } from "../api/admin";
import type { TemplateWriteView } from "../api/admin";
import { readView } from "../api/client";
import { protocolProjectRemotes } from "../api/projectRemote";
import type { Visibility } from "../api/projectRemote";
import type { Project, ShopFloor } from "../api/types";
import { go } from "../app/route";
import { Button } from "../ui/controls";
import { Banner, Refusal, SkeletonRows } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Icon } from "../ui/Icon";
import { Dialog, useToast } from "../ui/overlays";
import { RemoteSection, RepositoriesWritten, RepositoryRows, useGitHubAccount } from "./NewProjectRemote";
import { useChange } from "./parts";
import type { Draft, Problem, Question, RemoteMode, Row } from "./projectRemoteRequest";
import { buildRequest, creating, newRow, rememberedOwner, rememberOwner, remoteMode } from "./projectRemoteRequest";
import { plainText } from "./untrustedText";

// How often the dialog reads a request it waits on, and how long past its expiry it keeps reading, in milliseconds.
export const pollMs = 1000;
const graceMs = 5000;

// The page that describes how a project grows from one repository to several, by adopting and restructuring.
const growthPath = "https://github.com/bkuberek/ropeshift/blob/main/docs/project-structure.md#growing-a-project";

// The two answers to the first question, and the shape each writes.
const answers: { value: "one" | "several"; label: string; says: string }[] = [
  { value: "one", label: "One repository", says: "Writes the plan and the code in one repository." },
  { value: "several", label: "Several repositories", says: "Writes a planning repository, with the code repositories you name cloned into its ignored repos/ folder." },
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

type Choice = { id: string; name: string; protocol: number; connected: boolean | null; old: boolean };

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
          protocol: worker.protocol ?? 0,
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
    : [
        choice.name,
        choice.connected === null ? "" : choice.connected ? "connected" : "last reported",
        choice.protocol < protocolProjectRemotes ? "Upgrade it to create a project's remote" : "",
      ]
        .filter(Boolean)
        .join(" · ");

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

// StartFromTemplate is the dialog that writes a project's starting files on a worker the member picks, as `ropeshift new` writes them on
// this machine (FR-TPL-5, FR-TPL-6, FR-TPL-7, FR-UI-7): whether the project lives in one repository or several, the worker, the folder, the
// code repositories, the first commit, and the remote. It waits for the worker's report and says what became of the request. Registering what
// was written is a separate act it hands to the Repositories view (FR-UI-8).
export function StartFromTemplate({ open, onClose, projects, reload }: { open: boolean; onClose: () => void; projects: Project[]; reload: () => Promise<void> }) {
  const found = useWorkers(open);
  const change = useChange();
  const copy = useCopy();
  const [worker, setWorker] = useState("");
  const [question, setQuestion] = useState<Question>("");
  const [folder, setFolder] = useState("");
  const [rows, setRows] = useState<Row[]>([newRow()]);
  const [project, setProject] = useState("");
  const [firstCommit, setFirstCommit] = useState(true);
  const [mode, setMode] = useState<RemoteMode>("none");
  const [remoteUrl, setRemoteUrl] = useState("");
  const [owner, setOwner] = useState("");
  const [visibility, setVisibility] = useState<Visibility>("private");
  const [problem, setProblem] = useState("");
  const [issue, setIssue] = useState<Problem | null>(null);
  const [phase, setPhase] = useState<Phase>({ kind: "form" });
  const [lapsed, setLapsed] = useState(false);
  const [readFailure, setReadFailure] = useState("");
  const folderField = useRef<HTMLInputElement>(null);
  const firstAnswer = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) {
      setPhase({ kind: "form" });
      setProblem("");
      setIssue(null);
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

  // What the worker takes decides what the form offers: a request that holds a remote needs plane protocol 6.
  const chosen = found.choices.find((choice) => choice.id === worker);
  const draft: Draft = { question, project, firstCommit, mode, url: remoteUrl, owner, visibility, rows, remotes: Boolean(chosen && chosen.protocol >= protocolProjectRemotes) };
  const effective = remoteMode(draft);
  const { account, check } = useGitHubAccount(open && phase.kind === "form" && effective === "create", worker);
  const blocked = !firstCommit
    ? "A remote needs the first commit, which ropeshift pushes. Check Make the first commit to add one."
    : chosen && !draft.remotes
      ? `Worker ${chosen.name} speaks protocol ${chosen.protocol}; upgrade it to create a project's remote.`
      : "";
  const owners = account.kind === "ready" ? account.owners : [];

  // The owner stands at the one the member chose last on this worker when the account still offers it, and at the login otherwise.
  useEffect(() => {
    if (account.kind === "ready") {
      setOwner((was) => (account.owners.includes(was) ? was : account.owners.includes(rememberedOwner(worker)) ? rememberedOwner(worker) : (account.owners[0] ?? "")));
    }
  }, [account, worker]);

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

  const name = question === "elsewhere" && project.trim() ? project.trim() : projectNameOf(folder);

  const send = async () => {
    const wrong = folderProblem(folder);
    setProblem(wrong);
    setIssue(null);
    if (!question) {
      setIssue({ field: "question", message: "Choose whether the project lives in one repository or in several." });
      firstAnswer.current?.focus();
      return;
    }
    if (wrong) {
      folderField.current?.focus();
      return;
    }
    const built = buildRequest(draft, folder.trim());
    if (built.problem) {
      setIssue(built.problem);
      return;
    }
    if (effective === "create") {
      rememberOwner(worker, owner);
    }
    const sent = await change.run(() => writeTemplates(worker, built.request));
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
  const sendDisabled = !worker || !found.ready || (effective === "create" && account.kind !== "ready");

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
          <fieldset className="field plain-fieldset">
            <legend className="label">Will this project live in one repository, or in several?</legend>
            {answers.map((row, at) => (
              <label className="check-row" key={row.value}>
                <input ref={at === 0 ? firstAnswer : undefined} type="radio" name="project-question" checked={question === row.value} onChange={() => setQuestion(row.value)} />
                <span>
                  {row.label}
                  <span className="help">{row.says}</span>
                </span>
              </label>
            ))}
            <p className="help">
              A project of one repository can grow into several later, by adopting and restructuring.{" "}
              <a href={growthPath} target="_blank" rel="noreferrer">
                How a project grows<span className="visually-hidden"> (opens in a new tab)</span>
              </a>
            </p>
            {question === "elsewhere" ? (
              <p className="help">
                You are adding a code repository to a project planned in another repository.{" "}
                <Button small variant="ghost" onClick={() => setQuestion("")}>
                  Start a project instead
                </Button>
              </p>
            ) : (
              <div>
                <Button small variant="ghost" onClick={() => setQuestion("elsewhere")}>
                  Add a code repository to a project planned elsewhere
                </Button>
              </div>
            )}
            {issue?.field === "question" ? (
              <p className="msg msg-error" role="alert">
                <Icon name="warning" size="sm" />
                <span>{issue.message}</span>
              </p>
            ) : null}
          </fieldset>
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
          {question === "several" ? (
            <RepositoryRows rows={rows} setRows={setRows} mode={effective} remotes={draft.remotes} worker={chosen?.name ?? worker} owners={owners} problem={issue?.field === "rows" ? issue.message : ""} />
          ) : null}
          {question === "elsewhere" ? (
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
          {question ? (
            <RemoteSection
              worker={chosen?.name ?? worker}
              blocked={blocked}
              mode={mode}
              setMode={setMode}
              url={remoteUrl}
              setUrl={setRemoteUrl}
              urlLabel={question === "several" ? "Remote URL of the planning repository" : "Remote URL"}
              problem={issue?.field === "remote" ? issue.message : ""}
              account={account}
              check={check}
              owner={owner}
              setOwner={setOwner}
              visibility={visibility}
              setVisibility={setVisibility}
              copy={copy}
            />
          ) : null}
          {effective === "create" && account.kind === "ready" ? (
            <p className="small" role="status">
              {`Write files creates ${creating(draft)} ${visibility} ${creating(draft) === 1 ? "repository" : "repositories"} on GitHub.`}
            </p>
          ) : null}
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
      {(report.repositories ?? []).length > 0 ? (
        <RepositoriesWritten report={report} copy={copy} />
      ) : (
        <>
          {report.commit ? (
            <p className="small">
              First commit <code>{report.commit}</code>
            </p>
          ) : null}
          {report.commit_error ? <Refusal>{`The first commit failed, and the files stay: ${plainText(report.commit_error)}`}</Refusal> : null}
        </>
      )}
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
          {view.shape === "workspace" && (view.repositories ?? []).some((repository) => !repository.new) ? (
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
      {view.shape === "workspace" ? <p className="small">Approving the project's first plan registers the code repositories through the manifest.</p> : null}
    </div>
  );
}
