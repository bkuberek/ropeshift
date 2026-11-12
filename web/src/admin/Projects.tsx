import { useEffect, useRef, useState } from "react";

import { archiveProject, createProject, downloadBackup, importUpload, removeProject, renameProject, restoreProject } from "../api/admin";
import type { Project } from "../api/types";
import type { Live } from "../api/useView";
import { projectName } from "../app/Shell";
import { go, Link } from "../app/route";
import { running, useEveryFloor } from "../floor/Installation";
import { projectStates } from "../floor/states";
import { Page } from "../parts/parts";
import { Button } from "../ui/controls";
import { Absent, EmptyState, Panel, Refusal, StateBadge, TableRegion } from "../ui/display";
import { Field } from "../ui/forms";
import { Icon } from "../ui/Icon";
import { Dialog, Menu, useToast } from "../ui/overlays";
import { base64Of, dateOf, saveDownload, useChange } from "./parts";

type Open = { kind: "create" } | { kind: "import"; file: File } | { kind: "rename" | "archive" | "remove"; project: Project } | null;

// ProjectsPage lists every project of the installation, with what makes, imports, backs up, renames,
// archives, restores, and removes one. Each step is a member's call the CLI makes too.
export function ProjectsPage({ live, projects, reload, dialog }: { live: Live; projects: Project[]; reload: () => Promise<void>; dialog: string }) {
  const [open, setOpen] = useState<Open>(dialog === "create" ? { kind: "create" } : null);
  const floors = useEveryFloor(live, projects);
  const toast = useToast();
  const chooser = useRef<HTMLInputElement>(null);
  const backup = useChange();
  const restore = useChange();
  const lanes = (name: string) => floors.view?.find((read) => read.project === name)?.view.lanes.filter((lane) => running.has(lane.actual_state)).length;
  useEffect(() => {
    if (dialog === "create") {
      setOpen({ kind: "create" });
    }
  }, [dialog]);
  const done = async (text: string) => {
    setOpen(null);
    await reload();
    toast("success", text);
  };
  return (
    <Page
      title="Projects"
      note="Every project in this installation. Each has a ledger of its own."
      actions={
        <>
          <input
            ref={chooser}
            type="file"
            accept=".db,application/vnd.sqlite3,application/octet-stream"
            hidden
            aria-hidden="true"
            tabIndex={-1}
            onChange={(event) => {
              const file = event.target.files?.[0];
              event.target.value = "";
              if (file) {
                setOpen({ kind: "import", file });
              }
            }}
          />
          <Button icon="upload" onClick={() => chooser.current?.click()}>
            Import backup
          </Button>
          <Link className="btn" to="/import">
            <Icon name="import" size="sm" />
            Import an existing project
          </Link>
          <Button variant="primary" icon="plus" onClick={() => setOpen({ kind: "create" })}>
            Create project
          </Button>
        </>
      }
    >
      {backup.failure || restore.failure ? <Refusal>{backup.failure || restore.failure}</Refusal> : null}
      {projects.length === 0 ? (
        <div className="panel">
          <EmptyState icon="projects" title="No project yet" action={<Button variant="primary" icon="plus" onClick={() => setOpen({ kind: "create" })}>Create project</Button>}>
            A project holds its repositories, plans, and work in a ledger of its own. Create one, import a backup, or import an existing project.
          </EmptyState>
        </div>
      ) : (
        <Panel flush>
          <TableRegion label="Projects">
            <table className="table stack-on-phone">
              <thead>
                <tr>
                  <th scope="col">Project</th>
                  <th scope="col">State</th>
                  <th scope="col" className="r">
                    Repositories
                  </th>
                  <th scope="col" className="r">
                    Waiting
                  </th>
                  <th scope="col" className="r">
                    Lanes
                  </th>
                  <th scope="col">Created</th>
                  <th scope="col">
                    <span className="visually-hidden">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {projects.map((row) => {
                  const shown = projectName(row, row.name);
                  const archived = row.state === "archived";
                  const count = lanes(row.name);
                  return (
                    <tr key={row.id} className={archived ? "muted-row" : undefined}>
                      <td data-label="Project">
                        <span className="proj">
                          <span className="proj-mark" aria-hidden="true">
                            {shown.charAt(0).toUpperCase()}
                          </span>
                          <strong>{shown}</strong>
                        </span>{" "}
                        <span className="mono small subtle">{row.name}</span>
                      </td>
                      <td data-label="State">
                        <StateBadge table={projectStates} state={row.state} />
                      </td>
                      <td data-label="Repositories" className="r num">
                        {row.repositories.length}
                      </td>
                      <td data-label="Waiting" className="r num">
                        {archived ? <Absent>none</Absent> : (row.waiting ?? 0)}
                      </td>
                      <td data-label="Lanes" className="r num">
                        {archived ? 0 : count === undefined ? <Absent>reading</Absent> : count}
                      </td>
                      <td data-label="Created">{dateOf(row.created_at)}</td>
                      <td className="actions-cell">
                        {archived ? (
                          <>
                            <Button
                              small
                              loading={restore.busy}
                              onClick={() =>
                                void restore.run(() => restoreProject(row.name)).then((back) => (back ? done(`${shown} restored to active.`) : undefined))
                              }
                            >
                              Restore
                            </Button>{" "}
                            <Button small variant="danger-outline" onClick={() => setOpen({ kind: "remove", project: row })}>
                              Remove…
                            </Button>
                          </>
                        ) : (
                          <>
                            <Button
                              small
                              loading={backup.busy}
                              onClick={() =>
                                void backup.run(() => downloadBackup(row.name)).then((file) => {
                                  if (file) {
                                    saveDownload(file);
                                    toast("success", `Backup downloaded: ${file.name}`);
                                  }
                                })
                              }
                            >
                              Back up
                            </Button>{" "}
                            <Button small onClick={() => setOpen({ kind: "archive", project: row })}>
                              Archive…
                            </Button>{" "}
                            <Menu label={`Actions for ${shown}`} items={[{ label: "Rename…", icon: "pencil", onSelect: () => setOpen({ kind: "rename", project: row }) }]} />
                          </>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </TableRegion>
        </Panel>
      )}
      <CreateDialog open={open?.kind === "create"} onClose={() => setOpen(null)} onDone={(made) => void done(`Created project ${made.name}.`).then(() => go(`/projects/${encodeURIComponent(made.name)}/repositories`))} />
      {open?.kind === "import" ? <ImportBackupDialog file={open.file} onClose={() => setOpen(null)} onDone={(made) => void done(`Imported project ${made.name}.`)} /> : null}
      {open?.kind === "rename" ? <RenameDialog project={open.project} onClose={() => setOpen(null)} onDone={(made) => void done(`Renamed to ${made.name}.`)} /> : null}
      {open?.kind === "archive" ? <ArchiveDialog project={open.project} onClose={() => setOpen(null)} onDone={() => void done(`${projectName(open.project, open.project.name)} is archived and read-only.`)} /> : null}
      {open?.kind === "remove" ? <RemoveDialog project={open.project} onClose={() => setOpen(null)} onDone={() => void done(`Removed ${projectName(open.project, open.project.name)}.`)} /> : null}
    </Page>
  );
}

function CreateDialog({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: (made: Project) => void }) {
  const [display, setDisplay] = useState("");
  const [name, setName] = useState("");
  const [empty, setEmpty] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  const change = useChange();
  useEffect(() => {
    if (open) {
      setDisplay("");
      setName("");
      setEmpty(false);
      change.setFailure("");
    }
  }, [open]);
  const submit = async () => {
    if (!name.trim()) {
      setEmpty(true);
      field.current?.focus();
      return;
    }
    const made = await change.run(() => createProject(name.trim(), display.trim()));
    if (made) {
      onDone(made);
    } else {
      field.current?.focus();
    }
  };
  return (
    <Dialog
      open={open}
      title="Create project"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={change.busy} onClick={() => void submit()}>
            Create project
          </Button>
        </>
      }
    >
      <form
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <Field label="Display name">
          <input className="input" autoComplete="off" placeholder="Tidewater…" value={display} onChange={(event) => setDisplay(event.target.value)} />
        </Field>
        <Field
          label="Name"
          help={
            <>
              Lowercase and hyphenated. Used in addresses and in <code>--project</code>.
            </>
          }
          error={empty ? "Enter a name, such as harbor-api." : change.failure || undefined}
        >
          <input
            ref={field}
            className="input mono"
            autoComplete="off"
            spellCheck={false}
            placeholder="tidewater…"
            value={name}
            onChange={(event) => {
              setEmpty(false);
              setName(event.target.value);
            }}
          />
        </Field>
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

function ImportBackupDialog({ file, onClose, onDone }: { file: File; onClose: () => void; onDone: (made: Project) => void }) {
  const [name, setName] = useState("");
  const change = useChange();
  return (
    <Dialog
      open
      title="Import backup"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            icon="upload"
            loading={change.busy}
            onClick={() => void change.run(async () => importUpload(await base64Of(file), name.trim())).then((made) => made && onDone(made))}
          >
            Import project
          </Button>
        </>
      }
    >
      <div className="field-stack">
        <p className="small">
          Registers the project in <code className="mono">{file.name}</code> under its own ID, with every record it holds.
        </p>
        <Field label="Name" help="Leave it empty to keep the name the backup holds." error={change.failure || undefined}>
          <input className="input mono" autoComplete="off" spellCheck={false} placeholder="tidewater…" value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
      </div>
    </Dialog>
  );
}

function RenameDialog({ project, onClose, onDone }: { project: Project; onClose: () => void; onDone: (made: Project) => void }) {
  const [name, setName] = useState(project.name);
  const change = useChange();
  return (
    <Dialog
      open
      title={`Rename ${projectName(project, project.name)}`}
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={change.busy} disabled={name.trim() === project.name} onClick={() => void change.run(() => renameProject(project.name, name.trim())).then((made) => made && onDone(made))}>
            Rename
          </Button>
        </>
      }
    >
      <Field label="New name" help="The project keeps its ID and every record. Addresses and --project use the new name." error={change.failure || undefined}>
        <input className="input mono" autoComplete="off" spellCheck={false} value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
    </Dialog>
  );
}

function ArchiveDialog({ project, onClose, onDone }: { project: Project; onClose: () => void; onDone: () => void }) {
  const change = useChange();
  const shown = projectName(project, project.name);
  return (
    <Dialog
      open
      title={`Archive ${shown}`}
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" icon="archive" loading={change.busy} onClick={() => void change.run(() => archiveProject(project.name)).then((made) => made && onDone())}>
            Archive
          </Button>
        </>
      }
    >
      <p className="small">{shown} becomes read-only: its views stay, and no lane starts in it until it is restored. Every lane of it must have ended first.</p>
      {change.failure ? (
        <>
          <Refusal>{change.failure}</Refusal>
          <p className="small">
            <Link to={`/projects/${encodeURIComponent(project.name)}/settings?section=fleet`}>Drain the fleet</Link> to let its lanes end.
          </p>
        </>
      ) : null}
    </Dialog>
  );
}

function RemoveDialog({ project, onClose, onDone }: { project: Project; onClose: () => void; onDone: () => void }) {
  const [typed, setTyped] = useState("");
  const [keep, setKeep] = useState<"download" | "none">("download");
  const change = useChange();
  const toast = useToast();
  const shown = projectName(project, project.name);
  const remove = () =>
    change.run(async () => {
      if (keep === "download") {
        const file = await downloadBackup(project.name);
        saveDownload(file);
        toast("success", `Backup downloaded: ${file.name}`);
      }
      await removeProject(project.name, typed);
      return true;
    });
  return (
    <Dialog
      open
      title={`Remove ${shown}`}
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="danger" icon="trash" loading={change.busy} disabled={typed !== project.name} onClick={() => void remove().then((ok) => ok && onDone())}>
            Remove project
          </Button>
        </>
      }
    >
      <div className="field-stack">
        <p className="small">This deletes {shown}'s database and every record in it. It cannot be undone except from a backup.</p>
        <fieldset className="field">
          <legend className="label">Backup</legend>
          <label className="check-row">
            <input type="radio" name="remove-backup" checked={keep === "download"} onChange={() => setKeep("download")} />
            <span>Download a backup first</span>
          </label>
          <label className="check-row">
            <input type="radio" name="remove-backup" checked={keep === "none"} onChange={() => setKeep("none")} />
            <span>Remove with no backup</span>
          </label>
        </fieldset>
        <Field label={`Type ${project.name} to confirm`} error={change.failure || undefined}>
          <input className="input mono" autoComplete="off" spellCheck={false} value={typed} onChange={(event) => setTyped(event.target.value)} />
        </Field>
      </div>
    </Dialog>
  );
}
