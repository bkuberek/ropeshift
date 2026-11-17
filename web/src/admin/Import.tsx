import { useEffect, useState } from "react";

import { addRepositories, changeRepository, createAdoptWork, createProject, readAdoptionPlan, readApprovals, readRepositories, readWorkers, reviewWork } from "../api/admin";
import type { AdoptionPlan, WorkerRecord } from "../api/admin";
import { inProjectOf } from "../api/client";
import { claimAndStart, newRunIDs } from "../api/runWork";
import type { Project } from "../api/types";
import { projectName } from "../app/Shell";
import { go, Link } from "../app/route";
import { startedEntry } from "../lanes/starting";
import { Page } from "../parts/parts";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { candidatesOf, foldersOf, leftOutAfter, planningRefusal, planRequest, registeredAt, registrablePaths, relativeFolder, restructureOf, whatChanged } from "./adoption";
import { PlanSummary, RestructureChoice } from "./AdoptionPlanView";
import { folderNameOf, newID, projectNameOf } from "./logic";
import { useChange } from "./parts";

const steps = ["Worker and folders", "Dry run", "Planning repository", "Adopt session"];

// What an adopt session's work order says it is for, and the one criterion it holds, as `ropeshift adopt` records
// them.
const criterion =
  "The project's existing documents are onboarded into the repository file format with their prose and IDs kept, and the plan of what remains is written to the repository as files, with the person in this session.";

function statementOf(project: string): string {
  return `Adopt the existing project ${project}: onboard the documents it already holds into the repository file format, mark what the code already does with its evidence, continue the in-flight branches the person keeps, and plan what remains.`;
}

// leftOutOf is the code trees a plan leaves out, which the plan is read again with to find out whether it changed.
const leftOutOf = (plan: AdoptionPlan): string[] => (plan.code_trees ?? []).filter((tree) => tree.left_out).map((tree) => tree.path);

// ImportPage adopts an existing project in four steps (ADR-0023, ADR-0027), each a member's call the CLI's adopt makes
// too: the folders and the project, a dry run that reads the plan and records nothing, the repository the plan lives
// in, then the adopt session, which registers, writes the inventory, and starts the planning lane. A restructuring
// happens only when the member chooses it after reading the plan.
export function ImportPage({ projects, chosen, reload }: { projects: Project[]; chosen: string; reload: () => Promise<void> }) {
  const active = projects.filter((row) => row.state === "active");
  const [step, setStep] = useState(1);
  const [folders, setFolders] = useState("");
  const [depth, setDepth] = useState("1");
  const [target, setTarget] = useState(chosen && active.some((row) => row.name === chosen) ? chosen : "new");
  const [newName, setNewName] = useState("");
  const [named, setNamed] = useState(false);
  const [workers, setWorkers] = useState<WorkerRecord[] | null>(null);
  const [worker, setWorker] = useState("");
  const [plan, setPlan] = useState<AdoptionPlan | null>(null);
  const [restructure, setRestructure] = useState(false);
  const [changed, setChanged] = useState<string[] | null>(null);
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
  const listed = foldersOf(folders);
  const root = listed[0] ?? "";
  useEffect(() => {
    if (!named) {
      setNewName(projectNameOf(root));
    }
  }, [root, named]);

  const levels = Math.max(1, Number.parseInt(depth, 10) || 1);
  const project = target === "new" ? newName.trim() : target;
  const label = projectName(projects.find((row) => row.name === project), project);
  const candidates = plan ? candidatesOf(plan) : [];
  const registering = (plan?.repositories ?? []).filter((row) => row.outcome === "registered").length;
  const refusedHome = plan ? planningRefusal(plan) : "";

  // readPlan reads the plan as the CLI's dry run does: the first folder is the clone adopt runs in, and the others are
  // paths beside it. A project not created yet has no route of its own, so it is read with no record of any project.
  const readPlan = (leaveOut: string[] = []): Promise<AdoptionPlan> => {
    const request = planRequest(listed, levels, leaveOut);
    return target === "new" ? readAdoptionPlan("installation", request) : inProjectOf(target, () => readAdoptionPlan("project", request));
  };

  // first settles the project: one an approved plan holds is refused, because it plans its next work with a planning
  // session (FR-ADOPT-10). Then it reads the plan, and creates nothing: a new project is created when the session
  // starts.
  const first = async () => {
    setChanged(null);
    if (listed.length === 0) {
      setEmpty("folder");
      return;
    }
    if (relativeFolder(listed)) {
      setEmpty("absolute");
      return;
    }
    if (!worker) {
      setEmpty("worker");
      return;
    }
    if (target === "new" && !newName.trim()) {
      setEmpty("name");
      return;
    }
    setEmpty("");
    const read = await change.run(async () => {
      if (target !== "new") {
        const approved = await inProjectOf(target, () => readApprovals("approved"));
        if (approved.approvals.some((row) => row.kind === "plan")) {
          setPlanned(true);
          return undefined;
        }
      }
      setPlanned(false);
      return readPlan();
    });
    if (read) {
      setPlan(read);
      setRestructure(false);
      setStep(2);
    }
  };

  // leaveOut checks or unchecks a code tree, and reads the plan again, whose digest then differs.
  const leaveOut = async (tree: string, leave: boolean) => {
    if (!plan) {
      return;
    }
    const next = await change.run(() => readPlan(leftOutAfter(plan, tree, leave)));
    if (next) {
      setPlan(next);
    }
  };

  // second settles where the plan lives, as the CLI does: the repository the project already plans in, else the one
  // the layout settled, else the project's one repository.
  const second = async () => {
    if (!plan) {
      return;
    }
    setChanged(null);
    // A project not created yet holds no repository, and so no planning repository.
    const read = await change.run(async () => ({ list: target === "new" ? null : await inProjectOf(target, () => readRepositories()) }));
    if (!read) {
      return;
    }
    const existing = read.list?.repositories.find((view) => view.planning || view.plan_of_record)?.id;
    const settled = candidates.find((row) => row.path === plan.planning.path)?.id;
    setPlanning(existing ?? settled ?? (candidates.length === 1 ? (candidates[0]?.id ?? "") : ""));
    setStep(3);
  };

  const start = async () => {
    if (!plan) {
      return;
    }
    const outcome = await change.run(async () => {
      // The layout is read again, so a plan that changed since the dry run is shown, not adopted.
      const now = await readPlan(leftOutOf(plan));
      if (now.digest !== plan.digest) {
        return { kind: "changed", now } as const;
      }
      if (restructure && refusedHome) {
        throw new Error(`The repository that holds the plan, ${plan.planning.name}, was refused: ${refusedHome}`);
      }
      let name = project;
      if (target === "new") {
        const made = await createProject(newName.trim(), folderNameOf(root));
        await reload();
        name = made.name;
      }
      // Each call names the project as it starts, because this page's address names none.
      const here = <T,>(run: () => Promise<T>) => inProjectOf(name, run);
      const report = await here(() => addRepositories(registrablePaths(plan), 1, false));
      const rootRefused = report.repositories.find((row) => row.path === plan.layout.root && row.outcome === "refused");
      if (rootRefused) {
        throw new Error(`This repository was not registered: ${rootRefused.reason ?? "no reason given"}`);
      }
      const home = candidates.find((row) => row.id === planning);
      const homeRefused = report.repositories.find((row) => row.path === home?.path && row.outcome === "refused");
      if (homeRefused) {
        throw new Error(`The repository that holds the plan, ${home?.name ?? planning}, was refused: ${homeRefused.reason ?? "no reason given"}`);
      }
      const repository = (home && registeredAt(report, home.path)) || planning;
      if (repository) {
        await here(() => changeRepository(repository, { planning: true }));
      }
      const id = newID();
      await here(() => createAdoptWork({ id, title: `Adopt ${name}`, statement: statementOf(name), criterion, repository, restructure: restructure ? restructureOf(plan, planRequest(listed, levels, leftOutOf(plan))) : undefined }));
      const review = await here(() => reviewWork(id));
      if (review.failures && review.failures.length > 0) {
        throw new Error(`Work order ${id} was recorded and is not ready: ${review.failures.map((failure) => `${failure.check}: ${failure.detail}`).join("; ")}`);
      }
      return { kind: "started", name, lane: await claimAndStart(id, worker, newRunIDs(), { project: name }) } as const;
    });
    if (!outcome) {
      return;
    }
    if (outcome.kind === "changed") {
      setPlan(outcome.now);
      setRestructure(false);
      setChanged(whatChanged(plan, outcome.now));
      setStep(2);
      return;
    }
    await reload();
    go(`/projects/${encodeURIComponent(outcome.name)}/planning?lane=${encodeURIComponent(outcome.lane)}`, { state: startedEntry(outcome.lane) });
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
              <h2 id="import-where">Worker and folders</h2>
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
                <Field
                  label="Folders"
                  help="Absolute paths on that machine, one to a line. The first is the clone you adopt from, the top of a git clone with a commit. Each other line is a clone, or a folder of clones."
                  error={
                    empty === "folder"
                      ? "Enter a folder, such as /srv/code/tidewater."
                      : empty === "absolute"
                        ? `Write each folder as an absolute path. ${relativeFolder(listed)} is not one.`
                        : undefined
                  }
                >
                  <textarea className="textarea mono" rows={4} autoComplete="off" spellCheck={false} placeholder={"/srv/code/tidewater…\n/srv/code/tidewater-web…"} value={folders} onChange={(event) => setFolders(event.target.value)} />
                </Field>
                <Field label="Depth" help="How many folder levels down to look for clones in each folder after the first, and in the folders the first clone ignores.">
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
                  <Field
                    label="New project's name"
                    help="Named after the first folder until you change it. The project is shown by the folder's name, which Rename on Projects changes."
                    error={empty === "name" ? "Name the project: the first folder gives no name." : undefined}
                  >
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
                <dt>Folders</dt>
                <dd>
                  {listed.map((folder) => (
                    <div key={folder}>
                      <code>{folder}</code>
                    </div>
                  ))}
                  <span className="small muted">Depth {levels}</span>
                </dd>
                <dt>Project</dt>
                <dd>{target === "new" ? <>New project <strong>{project}</strong></> : <strong>{label}</strong>}</dd>
              </dl>
            )}
          </section>
          {step >= 2 && plan ? (
            <section className="panel" aria-labelledby="import-dry">
              <div className="panel-head">
                <h2 id="import-dry">Dry run</h2>
                <span className="subtle">nothing recorded yet</span>
              </div>
              <div className="panel-body stack">
                {changed ? (
                  <Refusal>
                    The layout changed since you read the plan, so nothing was started: {changed.length > 0 ? changed.join("; ") : "the layout around the clones differs"}. The plan below is what it reads now. Read it, choose again, and continue.
                  </Refusal>
                ) : null}
                <PlanSummary plan={plan} />
                {step === 2 ? (
                  <RestructureChoice
                    plan={plan}
                    chosen={restructure}
                    busy={change.busy}
                    refusal={refusedHome ? `The repository that holds the plan, ${plan.planning.name}, was refused: ${refusedHome}` : plan.planning.undecided ? "Nothing in the layout settles where the plan lives, so there is nothing to restructure. Choose the repository in the next step to adopt without restructuring." : ""}
                    onChoose={setRestructure}
                    onLeaveOut={(tree, leave) => void leaveOut(tree, leave)}
                  />
                ) : (
                  <p className="small">{restructure ? "Restructuring is chosen: the adoption branch makes the changes the plan lists." : "Adopting without restructuring."}</p>
                )}
                {step === 2 ? (
                  <>
                    {change.failure ? <Refusal>{change.failure}</Refusal> : null}
                    <div className="row-tight">
                      <Button variant="primary" loading={change.busy} disabled={candidates.length === 0} onClick={() => void second()}>
                        Continue
                      </Button>
                    </div>
                  </>
                ) : null}
              </div>
            </section>
          ) : null}
          {step >= 3 && plan ? (
            <section className="panel" aria-labelledby="import-plan">
              <div className="panel-head">
                <h2 id="import-plan">Planning repository</h2>
              </div>
              <fieldset className="panel-body">
                <legend className="help">
                  {restructure
                    ? "A restructuring writes to the repository the plan lives in, so it is the one the plan names. Adoption writes only there, on its own branch."
                    : candidates.length > 1
                      ? "The project has several repositories, so the plan needs one to live in. Adoption writes only there, on its own branch."
                      : "The plan lives in the project's one repository. Adoption writes only there, on its own branch."}
                </legend>
                {candidates.map((row) => (
                  <label key={row.id} className="option">
                    <input type="radio" name="planning-repository" checked={planning === row.id} disabled={restructure && planning !== row.id} onChange={() => setPlanning(row.id ?? "")} />
                    <span>
                      <strong>{row.name || row.id}</strong>
                      <br />
                      <code className="small muted mono">{row.path}</code>
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
                  ? "The dry run lists each clone the folders hold and what registering would do with it. It records nothing, and creates no project."
                  : step === 2
                    ? `Registering would add ${registering} ${registering === 1 ? "repository" : "repositories"} to ${label}.`
                    : step === 3
                      ? "The adopt session writes the plan, and the onboarded documents, to this repository on a branch of its own."
                      : `Registers ${registering} ${registering === 1 ? "repository" : "repositories"} in ${label}, ${restructure ? "restructures the planning repository as the plan lists, " : ""}writes the inventory, and starts the session's lane, whose planning pane opens next. The session asks you what only you can answer.`}
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
