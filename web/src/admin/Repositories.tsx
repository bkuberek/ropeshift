import { useEffect, useState } from "react";

import { addRepositories, changeRepository, readRepositories, removeRepository } from "../api/admin";
import type { MergeAuthority, MergeMethod, RepositoryAddReport, RepositoryChange, RepositoryList, RepositoryView, ReviewTarget, StartFrom } from "../api/admin";
import type { Live } from "../api/useView";
import { addressOf, go, Link } from "../app/route";
import { Page } from "../parts/parts";
import { Button, Segmented } from "../ui/controls";
import { Absent, Badge, EmptyState, LookBadge, Panel, Refusal, TableRegion } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Icon } from "../ui/Icon";
import { Dialog, useToast } from "../ui/overlays";
import { outcomeOf } from "./logic";
import { GridField, Reading, SaveBar, useChange, useDraft, useLeaveGuard, useRead } from "./parts";

const authorities: { level: MergeAuthority; text: string }[] = [
  { level: "person_merges", text: "You review and merge each change. The verdicts come first." },
  { level: "person_approves", text: "You approve each change in Needs you; ropeshift merges it at the reviewed commit." },
  { level: "review_approves", text: "The review agent's pass, with every required check green, lets ropeshift merge." },
];

const rank = (level: MergeAuthority | undefined) => authorities.findIndex((row) => row.level === (level ?? "person_merges"));

type Draft = { name: string; base: string; start_from: StartFrom; planning: boolean; review_target: ReviewTarget; merge_method: MergeMethod; forge_binding: string; merge_authority: MergeAuthority };

const draftOf = (view: RepositoryView): Draft => ({
  name: view.name ?? "",
  base: view.base_branch ?? "",
  start_from: view.start_from,
  planning: Boolean(view.planning),
  review_target: view.review_target,
  merge_method: view.merge_method,
  forge_binding: view.forge_binding ?? "",
  merge_authority: view.merge_authority ?? "person_merges",
});

const fieldWords: Record<keyof Draft, string> = {
  name: "name",
  base: "base branch",
  start_from: "start from",
  planning: "planning repository",
  review_target: "review target",
  merge_method: "merge method",
  forge_binding: "forge binding",
  merge_authority: "merge authority",
};

// changesOf is what a save sends: only the fields a member changed, as the API names them.
function changesOf(was: Draft, now: Draft): RepositoryChange {
  const out: RepositoryChange = {};
  for (const key of Object.keys(was) as (keyof Draft)[]) {
    if (was[key] !== now[key]) {
      (out as Record<string, unknown>)[key] = now[key];
    }
  }
  return out;
}

function shownValue(value: string | boolean): string {
  return typeof value === "boolean" ? (value ? "yes" : "no") : value || "unset";
}

const label = (view: RepositoryView) => view.name || view.id;

function Forge({ view }: { view: RepositoryView }) {
  const reports = view.forge ?? [];
  if (reports.some((report) => report.state === "ok")) {
    return (
      <Badge tone="success" glyph="check">
        gh signed in
      </Badge>
    );
  }
  const problem = reports.find((report) => report.state !== "ok");
  if (problem) {
    return (
      <span title={problem.detail}>
        <Badge tone="danger" glyph="cross">
          {problem.state === "missing" ? "No gh on " : "gh signed out on "}
          {problem.worker_name || problem.worker}
        </Badge>
        {problem.detail ? <span className="cell-note">{problem.detail}</span> : null}
      </span>
    );
  }
  return view.review_target === "branch" ? <span className="subtle">not needed</span> : <Absent>no report yet</Absent>;
}

// RepositoriesPage lists the project's repositories, opens one's record below the table, and registers
// more from a folder of clones after a dry run.
export function RepositoriesPage({ live, project, label: projectLabel, selected, dialog, archived }: { live: Live; project: string; label: string; selected: string; dialog: string; archived: boolean }) {
  const read = useRead<RepositoryList>(() => readRepositories(), `repositories:${project}`, live);
  const [registering, setRegistering] = useState(dialog === "register");
  const rows = (read.view?.repositories ?? []).filter((view) => view.state !== "removed");
  const chosen = rows.find((view) => view.id === selected);
  return (
    <Page
      title="Repositories"
      note={read.view ? `${rows.length} ${rows.length === 1 ? "repository" : "repositories"} in ${projectLabel}. A repository belongs to one project.` : undefined}
      actions={
        <>
          <Link className="btn" to="/import">
            <Icon name="import" size="sm" />
            Import an existing project
          </Link>
          <Button variant="primary" icon="plus" disabled={archived} title={archived ? `${projectLabel} is archived and read-only` : undefined} onClick={() => setRegistering(true)}>
            Register repositories
          </Button>
        </>
      }
    >
      <Reading failure={read.failure} ready={read.view !== null}>
        {rows.length === 0 ? (
          <div className="panel">
            <EmptyState
              icon="repo"
              title={`No repositories in ${projectLabel} yet`}
              action={
                <span className="row-tight">
                  <Button variant="primary" icon="plus" onClick={() => setRegistering(true)}>
                    Register repositories
                  </Button>
                  <Link className="btn" to="/import">
                    Import an existing project
                  </Link>
                </span>
              }
            >
              A repository is a git clone on a worker's machine. Registering one lets lanes work in it.
            </EmptyState>
          </div>
        ) : (
          <div className="stack">
            <Panel flush>
              <TableRegion label="Repositories">
                <table className="table stack-on-phone">
                  <thead>
                    <tr>
                      <th scope="col">Name</th>
                      <th scope="col">Remote</th>
                      <th scope="col">Base</th>
                      <th scope="col">Review target</th>
                      <th scope="col">Merge method</th>
                      <th scope="col">Merge authority</th>
                      <th scope="col">Forge</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((view) => (
                      <tr key={view.id} aria-selected={view.id === selected || undefined}>
                        <td data-label="Name">
                          <Link to={addressOf("/repositories", { repo: view.id })} stay>
                            <strong>{label(view)}</strong>
                          </Link>{" "}
                          {view.plan_of_record ? (
                            <Badge glyph="target" tone="neutral">
                              Planning
                            </Badge>
                          ) : null}
                        </td>
                        <td data-label="Remote">{view.remote ? <span className="mono small">{view.remote}</span> : <Absent>no remote</Absent>}</td>
                        <td data-label="Base">
                          <code>{view.base || view.default_branch}</code>
                        </td>
                        <td data-label="Review target">{view.review_target}</td>
                        <td data-label="Merge method">{view.merge_method}</td>
                        <td data-label="Merge authority">{view.merge_authority ?? "person_merges"}</td>
                        <td data-label="Forge">
                          <Forge view={view} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableRegion>
            </Panel>
            {chosen ? <RecordForm key={chosen.id} view={chosen} archived={archived} projectLabel={projectLabel} after={read.again} /> : null}
            {!chosen && selected ? <Refusal>No repository {selected} in {projectLabel}.</Refusal> : null}
          </div>
        )}
      </Reading>
      <RegisterDialog open={registering} projectLabel={projectLabel} onClose={() => setRegistering(false)} after={read.again} />
    </Page>
  );
}

function RecordForm({ view, archived, projectLabel, after }: { view: RepositoryView; archived: boolean; projectLabel: string; after: () => void }) {
  const [draft, setDraft, saved] = useDraft<Draft>(draftOf(view));
  const [removing, setRemoving] = useState(false);
  const change = useChange();
  const toast = useToast();
  const changes = changesOf(saved, draft);
  const changed = Object.keys(changes) as (keyof Draft)[];
  useLeaveGuard(changed.length > 0);
  const summary = changed.map((key) => `${fieldWords[key]} ${shownValue(saved[key])} → ${shownValue(draft[key])}`).join(", ");
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((was) => ({ ...was, [key]: value }));
  const raising = rank(draft.merge_authority) > rank(saved.merge_authority);
  const agreement = view.review_agreement.last_30_days;
  const save = async () => {
    const done = await change.run(() => changeRepository(view.id, changes));
    if (done) {
      toast("success", `Saved ${label(view)}.`);
      after();
    }
  };
  return (
    <section className="panel" aria-labelledby={`repo-${view.id}`}>
      <div className="panel-head">
        <h2 id={`repo-${view.id}`}>{label(view)}</h2>
        <span className="subtle small">
          record <span className="mono">{view.id}</span>
        </span>
        <Button small variant="danger-outline" icon="trash" disabled={archived} onClick={() => setRemoving(true)}>
          Remove…
        </Button>
      </div>
      <form
        className="panel-body"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <fieldset className="form-grid record-form" disabled={archived} aria-label={`Record of ${label(view)}`}>
          <GridField label="Name">
            <input className="input mono field-narrow" autoComplete="off" spellCheck={false} value={draft.name} onChange={(event) => set("name", event.target.value)} />
          </GridField>
          <GridField label="Base branch" help={`Lanes start from this branch. Unset, the default branch: ${view.default_branch ?? "unknown"}.`}>
            <input className="input mono field-narrow" autoComplete="off" spellCheck={false} placeholder={view.default_branch ? `${view.default_branch}…` : "main…"} value={draft.base} onChange={(event) => set("base", event.target.value)} />
          </GridField>
          <span className="label">Start from</span>
          <div className="field">
            <Segmented<StartFrom>
              label="Start from"
              value={draft.start_from}
              choices={[
                { value: "merged", label: "merged" },
                { value: "accepted", label: "accepted" },
              ]}
              onChange={(value) => set("start_from", value)}
            />
            <p className="help">accepted lets dependent work start from accepted commits before they merge; squash and rebase rule it out.</p>
          </div>
          <span className="label">Planning repository</span>
          <div className="field">
            <label className="check-row">
              <input type="checkbox" checked={draft.planning} onChange={(event) => set("planning", event.target.checked)} />
              <span>The project's plan lives in this repository</span>
            </label>
          </div>
          <span className="label">Review target</span>
          <div className="field">
            <Segmented<ReviewTarget>
              label="Review target"
              value={draft.review_target}
              choices={[
                { value: "branch", label: "branch" },
                { value: "pull_request", label: "pull_request" },
              ]}
              onChange={(value) => set("review_target", value)}
            />
            <p className="help">pull_request opens a pull request before review and needs a forge binding. branch reviews locally and pushes nothing before a merge.</p>
          </div>
          <GridField label="Forge binding" help="The forge's name for this repository, such as owner/name, for pull requests.">
            <input className="input mono field-narrow" autoComplete="off" spellCheck={false} placeholder="owner/name…" value={draft.forge_binding} onChange={(event) => set("forge_binding", event.target.value)} />
          </GridField>
          <span className="label" id={`auth-${view.id}`}>
            Merge authority
          </span>
          <div className="field" role="radiogroup" aria-labelledby={`auth-${view.id}`}>
            {authorities.map((row) => (
              <label key={row.level} className="option">
                <input type="radio" name={`auth-${view.id}`} value={row.level} checked={draft.merge_authority === row.level} onChange={() => set("merge_authority", row.level)} />
                <span>
                  <strong>{row.level}</strong>
                  <br />
                  <span className="small muted">{row.text}</span>
                </span>
              </label>
            ))}
            {draft.merge_authority === "review_approves" && draft.review_target === "branch" ? (
              <p className="msg msg-warning">
                <Icon name="warning" size="sm" />
                <span>No forge runs checks in branch mode, so the verdicts are the only gate.</span>
              </p>
            ) : null}
            <p className="help">
              {raising ? "Raising it: " : ""}agreement on {label(view)}: {agreement.agreed} of {agreement.decisions} over 30 days. The level never rises by itself.
            </p>
          </div>
          <GridField label="Merge method" help="squash and rebase rule out starting dependent work from accepted commits.">
            <Select className="field-narrow" value={draft.merge_method} onChange={(event) => set("merge_method", event.target.value as MergeMethod)}>
              <option value="merge">merge</option>
              <option value="squash">squash</option>
              <option value="rebase">rebase</option>
            </Select>
          </GridField>
        </fieldset>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </form>
      <SaveBar summary={summary} onDiscard={() => setDraft(saved)} onSave={() => void save()} saving={change.busy} disabled={archived} />
      {removing ? <RemoveRepositoryDialog view={view} projectLabel={projectLabel} onClose={() => setRemoving(false)} after={after} /> : null}
    </section>
  );
}

function RemoveRepositoryDialog({ view, projectLabel, onClose, after }: { view: RepositoryView; projectLabel: string; onClose: () => void; after: () => void }) {
  const [typed, setTyped] = useState("");
  const change = useChange();
  const toast = useToast();
  const name = label(view);
  return (
    <Dialog
      open
      title={`Remove ${name}`}
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="danger"
            icon="trash"
            loading={change.busy}
            disabled={typed !== name}
            onClick={() =>
              void change.run(() => removeRepository(view.id)).then((done) => {
                if (done) {
                  toast("success", `Removed ${name} from ${projectLabel}.`);
                  onClose();
                  go("/repositories", { stay: true });
                  after();
                }
              })
            }
          >
            Remove repository
          </Button>
        </>
      }
    >
      <div className="field-stack">
        <p className="small">
          {name} leaves {projectLabel}, and another project may then register it. Its record stays as history. Open work that targets it holds it here.
        </p>
        <Field label={`Type ${name} to confirm`} error={change.failure || undefined}>
          <input className="input mono" autoComplete="off" spellCheck={false} value={typed} onChange={(event) => setTyped(event.target.value)} />
        </Field>
      </div>
    </Dialog>
  );
}

// RegisterDialog registers repositories in two steps: where the clones are, then a dry run that records
// nothing, then Register (FR-UI-8).
export function RegisterDialog({ open, projectLabel, onClose, after }: { open: boolean; projectLabel: string; onClose: () => void; after: () => void }) {
  const [mode, setMode] = useState<"one" | "folder">("folder");
  const [path, setPath] = useState("");
  const [depth, setDepth] = useState("1");
  const [dry, setDry] = useState<RepositoryAddReport | null>(null);
  const [empty, setEmpty] = useState(false);
  const change = useChange();
  const toast = useToast();
  useEffect(() => {
    if (open) {
      setDry(null);
      setEmpty(false);
      change.setFailure("");
    }
  }, [open]);
  const paths = [path.trim()];
  const levels = mode === "folder" ? Math.max(1, Number.parseInt(depth, 10) || 1) : 0;
  const registering = dry?.repositories.filter((row) => row.outcome === "registered").length ?? 0;
  const runDry = async () => {
    if (!path.trim()) {
      setEmpty(true);
      return;
    }
    const report = await change.run(() => addRepositories(paths, levels, true));
    if (report) {
      setDry(report);
    }
  };
  const register = async () => {
    const report = await change.run(() => addRepositories(paths, levels, false));
    if (report) {
      const made = report.repositories.filter((row) => row.outcome === "registered").length;
      toast("success", `Registered ${made} ${made === 1 ? "repository" : "repositories"} in ${projectLabel}.`);
      onClose();
      after();
    }
  };
  return (
    <Dialog
      open={open}
      wide
      title={`Register repositories in ${projectLabel}`}
      onClose={onClose}
      footer={
        <>
          <span className="small muted">
            {dry
              ? [
                  `${dry.repositories.filter((row) => row.outcome === "refused").length} refused`,
                  `${dry.repositories.filter((row) => row.outcome === "known").length} already here`,
                ].join(", ")
              : "Step 1 of 2"}
          </span>
          <span className="spacer" />
          {dry ? (
            <>
              <Button onClick={() => setDry(null)}>Back</Button>
              <Button variant="primary" loading={change.busy} disabled={registering === 0} onClick={() => void register()}>
                Register {registering} {registering === 1 ? "repository" : "repositories"}
              </Button>
            </>
          ) : (
            <>
              <Button onClick={onClose}>Cancel</Button>
              <Button variant="primary" loading={change.busy} onClick={() => void runDry()}>
                Run dry run
              </Button>
            </>
          )}
        </>
      }
    >
      <ol className="steps" aria-label="Progress">
        <li aria-current={dry ? undefined : "step"} className={dry ? "done" : undefined}>
          <span className="n">1</span>Where
        </li>
        <li aria-current={dry ? "step" : undefined}>
          <span className="n">2</span>Dry run
        </li>
      </ol>
      {dry ? (
        <>
          <p className="small muted dialog-note">
            Found {dry.repositories.length} under <code>{path}</code> on the daemon's worker. Nothing is recorded until you register.
          </p>
          <DryRun report={dry} />
        </>
      ) : (
        <form
          className="field-stack"
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            void runDry();
          }}
        >
          <div className="field">
            <span className="label">Worker</span>
            <p className="help">The worker of the machine the daemon runs on, whose disk holds the clones.</p>
          </div>
          <fieldset className="field">
            <legend className="label">What to register</legend>
            <label className="check-row">
              <input type="radio" name="register-mode" checked={mode === "one"} onChange={() => setMode("one")} />
              <span>One repository</span>
            </label>
            <label className="check-row">
              <input type="radio" name="register-mode" checked={mode === "folder"} onChange={() => setMode("folder")} />
              <span>A folder of clones</span>
            </label>
          </fieldset>
          <Field label={mode === "one" ? "Path to the clone" : "Folder"} help="An absolute path on that machine." error={empty ? "Enter a path, such as /srv/code/harbor." : undefined}>
            <input className="input mono" autoComplete="off" spellCheck={false} placeholder="/srv/code/harbor…" value={path} onChange={(event) => setPath(event.target.value)} />
          </Field>
          {mode === "folder" ? (
            <Field label="Depth" help="How many folder levels down to look for clones.">
              <input className="input num field-tiny" inputMode="numeric" autoComplete="off" value={depth} onChange={(event) => setDepth(event.target.value)} />
            </Field>
          ) : null}
          <button type="submit" hidden />
        </form>
      )}
      {change.failure ? <Refusal>{change.failure}</Refusal> : null}
    </Dialog>
  );
}

// DryRun is what registering would do with each clone, and with each path that holds none.
export function DryRun({ report }: { report: RepositoryAddReport }) {
  return (
    <div className="panel">
      <TableRegion label="Clones found">
        <table className="table stack-on-phone">
          <thead>
            <tr>
              <th scope="col">Clone</th>
              <th scope="col">Identity</th>
              <th scope="col">Result</th>
            </tr>
          </thead>
          <tbody>
            {report.repositories.map((row) => (
              <tr key={row.path}>
                <td data-label="Clone">
                  <code>{row.path}</code>
                </td>
                <td data-label="Identity">{row.repository ? <span className="mono small">{row.repository.remote || row.repository.id}</span> : <span className="subtle">none</span>}</td>
                <td data-label="Result">
                  <LookBadge look={outcomeOf(row, report.dry_run)} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
    </div>
  );
}
