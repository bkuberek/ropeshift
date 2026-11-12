import { useEffect, useState } from "react";

import { addRepositories, changeRepository, claimWork, createAdoptWork, createProject, readApprovals, readRepositories, readWorkers, reviewWork, startLane } from "../api/admin";
import type { RepositoryAddReport, WorkerRecord } from "../api/admin";
import { inProjectOf } from "../api/client";
import type { Project } from "../api/types";
import { projectName } from "../app/Shell";
import { go, Link } from "../app/route";
import { Page } from "../parts/parts";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { newID, projectNameOf } from "./logic";
import { useChange } from "./parts";
import { DryRun } from "./Repositories";

const steps = ["Worker and folder", "Dry run", "Planning repository", "Adopt session"];

// What an adopt session's work order says it is for, and the one criterion it holds, as `ropeshift adopt`
// records them.
const criterion =
  "The project's existing documents are onboarded into the repository file format with their prose and IDs kept, and the plan of what remains is written to the repository as files, with the person in this session.";

function statementOf(project: string): string {
  return `Adopt the existing project ${project}: onboard the documents it already holds into the repository file format, mark what the code already does with its evidence, continue the in-flight branches the person keeps, and plan what remains.`;
}

// ImportPage adopts an existing project in four steps (ADR-0023), each a member's call the CLI's adopt
// makes too: the folder and the project, a dry run that records nothing, the repository the plan lives
// in, then the adopt session, which registers, writes the inventory, and starts the planning lane.
export function ImportPage({ projects, chosen, reload }: { projects: Project[]; chosen: string; reload: () => Promise<void> }) {
  const active = projects.filter((row) => row.state === "active");
  const [step, setStep] = useState(1);
  const [folder, setFolder] = useState("");
  const [depth, setDepth] = useState("1");
  const [target, setTarget] = useState(chosen && active.some((row) => row.name === chosen) ? chosen : "new");
  const [newName, setNewName] = useState("");
  const [named, setNamed] = useState(false);
  const [workers, setWorkers] = useState<WorkerRecord[] | null>(null);
  const [worker, setWorker] = useState("");
  const [project, setProject] = useState("");
  const [dry, setDry] = useState<RepositoryAddReport | null>(null);
  const [planning, setPlanning] = useState("");
  const [planned, setPlanned] = useState(false);
  const [empty, setEmpty] = useState("");
  const change = useChange();

  useEffect(() => {
    void readWorkers().then(
      (list) => {
        setWorkers(list.workers);
        setWorker((was) => was || (list.workers.length === 1 ? (list.workers[0]?.id ?? "") : ""));
      },
      () => setWorkers([]),
    );
  }, []);
  useEffect(() => {
    if (!named) {
      setNewName(projectNameOf(folder));
    }
  }, [folder, named]);

  const levels = Math.max(1, Number.parseInt(depth, 10) || 1);
  const candidates = (dry?.repositories ?? []).filter((row) => row.repository && row.outcome !== "refused");
  const registering = (dry?.repositories ?? []).filter((row) => row.outcome === "registered").length;
  const label = projectName(projects.find((row) => row.name === project), project);

  // first settles the project: one an approved plan holds is refused, because it plans its next work with
  // a planning session (FR-ADOPT-10). A new project is created here, because a dry run reads a project.
  const first = async () => {
    if (!folder.trim()) {
      setEmpty("folder");
      return;
    }
    if (!worker) {
      setEmpty("worker");
      return;
    }
    const settled = await change.run(async () => {
      if (target !== "new") {
        const approved = await inProjectOf(target, () => readApprovals("approved"));
        if (approved.approvals.some((row) => row.kind === "plan")) {
          setPlanned(true);
          return "";
        }
        return target;
      }
      const made = await createProject(newName.trim(), "");
      await reload();
      return made.name;
    });
    if (settled) {
      setPlanned(false);
      setProject(settled);
      const report = await change.run(() => inProjectOf(settled, () => addRepositories([folder.trim()], levels, true)));
      if (report) {
        setDry(report);
        setStep(2);
      }
    }
  };

  const second = async () => {
    const list = await change.run(() => inProjectOf(project, () => readRepositories()));
    const planningHere = list?.repositories.find((view) => view.planning || view.plan_of_record);
    setPlanning(planningHere?.id ?? (candidates.length === 1 ? (candidates[0]?.repository?.id ?? "") : ""));
    setStep(3);
  };

  const start = async () => {
    // Each call names the project as it starts, because this page's address names none.
    const here = <T,>(run: () => Promise<T>) => inProjectOf(project, run);
    const lane = await change.run(async () => {
      await here(() => addRepositories([folder.trim()], levels, false));
      if (planning) {
        await here(() => changeRepository(planning, { planning: true }));
      }
      const id = newID();
      await here(() => createAdoptWork({ id, title: `Adopt ${project}`, statement: statementOf(project), criterion, repository: planning }));
      const review = await here(() => reviewWork(id));
      if (review.failures && review.failures.length > 0) {
        throw new Error(`Work order ${id} was recorded and is not ready: ${review.failures.map((failure) => `${failure.check}: ${failure.detail}`).join("; ")}`);
      }
      await here(() => claimWork(id, newID(), worker));
      const started = newID();
      await here(() => startLane(id, started));
      return started;
    });
    if (lane) {
      await reload();
      go(`/projects/${encodeURIComponent(project)}/lane?id=${encodeURIComponent(lane)}`);
    }
  };

  return (
    <Page
      title="Import an existing project"
      note="Registers the repositories, inventories them without a model, and starts an adopt session that onboards the project on a branch of its own."
      crumbs={
        <>
          <Link to="/projects">Projects</Link>
          <span aria-hidden="true">›</span>
          <span aria-current="page">Import an existing project</span>
        </>
      }
    >
      <ol className="steps" aria-label="Progress">
        {steps.map((name, at) => (
          <li key={name} className={at + 1 < step ? "done" : undefined} aria-current={at + 1 === step ? "step" : undefined}>
            <span className="n">{at + 1}</span>
            {name}
          </li>
        ))}
      </ol>
      <div className="grid grid-main-side">
        <div className="stack">
          <section className="panel" aria-labelledby="import-where">
            <div className="panel-head">
              <h2 id="import-where">Worker and folder</h2>
              {step > 1 ? (
                <Button small variant="ghost" onClick={() => setStep(1)}>
                  Change
                </Button>
              ) : null}
            </div>
            {step === 1 ? (
              <form
                className="panel-body field-stack"
                noValidate
                onSubmit={(event) => {
                  event.preventDefault();
                  void first();
                }}
              >
                <Field label="Worker" help="The machine whose disk holds the clones. Clones register from the worker of the machine the daemon runs on." error={empty === "worker" ? "Choose the worker that runs the session." : undefined}>
                  <Select className="field-narrow" value={worker} onChange={(event) => setWorker(event.target.value)}>
                    <option value="">{workers === null ? "Reading…" : workers.length === 0 ? "No worker has registered" : "Choose a worker"}</option>
                    {(workers ?? []).map((row) => (
                      <option key={row.id} value={row.id}>
                        {row.name || row.id}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field label="Folder" help="An absolute path on that machine: one repository, or a folder of clones." error={empty === "folder" ? "Enter a folder, such as /srv/code/tidewater." : undefined}>
                  <input className="input mono" autoComplete="off" spellCheck={false} placeholder="/srv/code/tidewater…" value={folder} onChange={(event) => setFolder(event.target.value)} />
                </Field>
                <Field label="Depth" help="How many folder levels down to look for clones.">
                  <input className="input num field-tiny" inputMode="numeric" autoComplete="off" value={depth} onChange={(event) => setDepth(event.target.value)} />
                </Field>
                <Field label="Project">
                  <Select className="field-narrow" value={target} onChange={(event) => setTarget(event.target.value)}>
                    <option value="new">A new project</option>
                    {active.map((row) => (
                      <option key={row.name} value={row.name}>
                        {projectName(row, row.name)}
                      </option>
                    ))}
                  </Select>
                </Field>
                {target === "new" ? (
                  <Field label="New project's name" help="Named after the folder until you change it.">
                    <input
                      className="input mono field-narrow"
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="tidewater…"
                      value={newName}
                      onChange={(event) => {
                        setNamed(true);
                        setNewName(event.target.value);
                      }}
                    />
                  </Field>
                ) : null}
                {planned ? (
                  <Refusal>
                    {projectName(projects.find((row) => row.name === target), target)} already has an approved plan, so there is nothing to adopt.{" "}
                    <Link to={`/projects/${encodeURIComponent(target)}/roadmap`}>Start a planning session instead</Link>.
                  </Refusal>
                ) : null}
                {change.failure ? <Refusal>{change.failure}</Refusal> : null}
                <div className="row-tight">
                  <Button type="submit" variant="primary" loading={change.busy}>
                    Run dry run
                  </Button>
                </div>
              </form>
            ) : (
              <dl className="kv panel-body">
                <dt>Worker</dt>
                <dd>{workers?.find((row) => row.id === worker)?.name || worker}</dd>
                <dt>Folder</dt>
                <dd>
                  <code>{folder}</code>, depth {levels}
                </dd>
                <dt>Project</dt>
                <dd>{target === "new" ? <>New project <strong>{project}</strong></> : <strong>{label}</strong>}</dd>
              </dl>
            )}
          </section>
          {step >= 2 && dry ? (
            <section className="panel" aria-labelledby="import-dry">
              <div className="panel-head">
                <h2 id="import-dry">Dry run</h2>
                <span className="subtle">nothing recorded yet</span>
              </div>
              <div className="panel-body stack">
                <DryRun report={dry} />
                {step === 2 ? (
                  <div className="row-tight">
                    <Button variant="primary" loading={change.busy} disabled={candidates.length === 0} onClick={() => void second()}>
                      Continue
                    </Button>
                  </div>
                ) : null}
              </div>
            </section>
          ) : null}
          {step >= 3 ? (
            <section className="panel" aria-labelledby="import-plan">
              <div className="panel-head">
                <h2 id="import-plan">Planning repository</h2>
              </div>
              <fieldset className="panel-body">
                <legend className="help">
                  {candidates.length > 1
                    ? "The project has several repositories, so the plan needs one to live in. Adoption writes only there, on its own branch."
                    : "The plan lives in the project's one repository. Adoption writes only there, on its own branch."}
                </legend>
                {candidates.map((row) => (
                  <label key={row.repository?.id} className="option">
                    <input type="radio" name="planning-repository" checked={planning === row.repository?.id} onChange={() => setPlanning(row.repository?.id ?? "")} />
                    <span>
                      <strong>{row.repository?.name || row.repository?.id}</strong>
                      <br />
                      <span className="small muted mono">{row.path}</span>
                    </span>
                  </label>
                ))}
                {step === 3 ? (
                  <div className="row-tight form-actions">
                    <Button variant="primary" disabled={!planning} onClick={() => setStep(4)}>
                      Continue
                    </Button>
                  </div>
                ) : null}
              </fieldset>
            </section>
          ) : null}
        </div>
        <aside className="stack" aria-label="Next step">
          <div className="decision">
            <div className="decision-head">
              <div className="spacer">
                <p className="decision-kind">Next</p>
                <h2>{["Find the clones", "Read the dry run", "Choose where the plan lives", "Start the adopt session"][step - 1]}</h2>
              </div>
            </div>
            <div className="decision-body small">
              <p>
                {step === 1
                  ? "The dry run lists each clone under the folder and what registering would do with it. It records nothing."
                  : step === 2
                    ? `Registering would add ${registering} ${registering === 1 ? "repository" : "repositories"} to ${label}.`
                    : step === 3
                      ? "The adopt session writes the plan, and the onboarded documents, to this repository on a branch of its own."
                      : `Registers ${registering} ${registering === 1 ? "repository" : "repositories"} in ${label}, writes the inventory, and starts the session's lane, whose pane opens next. The session asks you what only you can answer.`}
              </p>
              <p className="muted">Nothing reaches a default branch until you approve the plan and the adoption branch merges.</p>
              {step === 4 && change.failure ? <Refusal>{change.failure}</Refusal> : null}
            </div>
            <div className="decision-foot">
              <Link className="btn" to="/projects">
                Back
              </Link>
              <span className="spacer" />
              <Button variant="primary" loading={step === 4 && change.busy} disabled={step !== 4 || !worker} onClick={() => void start()}>
                Start adopt session
              </Button>
            </div>
          </div>
        </aside>
      </div>
    </Page>
  );
}
