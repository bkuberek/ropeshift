import { useEffect, useState } from "react";

import { readProjects } from "../api/client";
import type { Delegate, DelegateCreated, DelegateList } from "../api/delegates";
import { createDelegate, delegateLives, readDelegates, removeDelegate, revokeDelegate } from "../api/delegates";
import type { Project } from "../api/types";
import type { Live } from "../api/useView";
import { delegateStates } from "../floor/states";
import { Ago, Until } from "../parts/parts";
import { Button } from "../ui/controls";
import { Absent, EmptyState, Panel, Refusal, StateBadge, TableRegion } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { Reading, useChange, useRead } from "./parts";

const shownOnce = "This credential is shown once. Store it now; ropeshift keeps only its digest.";

const exportLine = (token: string) => `export ROPESHIFT_DELEGATE_TOKEN=${token}`;

// DelegatesPanel lists the member's own delegate credentials: an agent outside ropeshift acting for them in
// one project (FR-AUTH-10). A credential acts until it expires or the member revokes it, and stays listed,
// marked, until the member removes it from the list.
export function DelegatesPanel({ live, creating, onCreating }: { live: Live; creating: boolean; onCreating: (open: boolean) => void }) {
  const read = useRead<DelegateList>(() => readDelegates(), "delegates", live);
  const change = useChange();
  const [created, setCreated] = useState<DelegateCreated | null>(null);
  const list = read.view;
  const act = async (run: () => Promise<unknown>) => {
    await change.run(run);
    read.again();
  };
  const create = (
    <Button icon="key" onClick={() => onCreating(true)}>
      Create delegate
    </Button>
  );
  return (
    <>
      <Panel flush title="Delegates" actions={list && list.delegates.length > 0 ? create : undefined} foot="Signing out of every session revokes every delegate.">
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        <Reading failure={read.failure} ready={list !== null}>
          {list && list.delegates.length === 0 ? (
            <EmptyState icon="key" title="No delegates" action={create}>
              A delegate credential lets an agent outside ropeshift act for you in one project: it runs what is operational, and prepares a card for anything that needs your consent.
            </EmptyState>
          ) : null}
          {list && list.delegates.length > 0 ? (
            <TableRegion label="Delegate credentials">
              <table className="table stack-on-phone">
                <thead>
                  <tr>
                    <th scope="col">Label</th>
                    <th scope="col">Project</th>
                    <th scope="col">Kind</th>
                    <th scope="col">Expires</th>
                    <th scope="col">Last use</th>
                    <th scope="col">State</th>
                    <th scope="col">
                      <span className="visually-hidden">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {list.delegates.map((delegate) => (
                    <DelegateRow key={delegate.id} delegate={delegate} busy={change.busy} onRevoke={() => void act(() => revokeDelegate(delegate.id))} onRemove={() => void act(() => removeDelegate(delegate.id))} />
                  ))}
                </tbody>
              </table>
            </TableRegion>
          ) : null}
        </Reading>
      </Panel>
      <CreateDelegate
        open={creating}
        onClose={() => onCreating(false)}
        onCreated={(made) => {
          onCreating(false);
          setCreated(made);
          read.again();
        }}
      />
      <ShownOnce created={created} onClose={() => setCreated(null)} />
    </>
  );
}

function DelegateRow({ delegate, busy, onRevoke, onRemove }: { delegate: Delegate; busy: boolean; onRevoke: () => void; onRemove: () => void }) {
  return (
    <tr>
      <td data-label="Label">
        <strong>{delegate.label}</strong> <span className="mono small subtle">{delegate.id}</span>
      </td>
      <td data-label="Project">{delegate.project}</td>
      <td data-label="Kind">{delegate.kind}</td>
      <td data-label="Expires">
        <Until at={delegate.expires_at} />
      </td>
      <td data-label="Last use">{delegate.last_used_at ? <Ago at={delegate.last_used_at} /> : <Absent>never used</Absent>}</td>
      <td data-label="State">
        <StateBadge table={delegateStates} state={delegate.state} />
      </td>
      <td data-label="Actions" className="r">
        {delegate.state === "active" ? (
          <Button small disabled={busy} onClick={onRevoke}>
            Revoke<span className="visually-hidden"> {delegate.label}</span>
          </Button>
        ) : (
          <Button small disabled={busy} onClick={onRemove}>
            Remove from list<span className="visually-hidden"> {delegate.label}</span>
          </Button>
        )}
      </td>
    </tr>
  );
}

function CreateDelegate({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (made: DelegateCreated) => void }) {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [project, setProject] = useState("");
  const [label, setLabel] = useState("");
  const [life, setLife] = useState(delegateLives[1]?.label ?? "");
  const change = useChange();
  useEffect(() => {
    if (!open) {
      return;
    }
    void readProjects().then(
      (list) => {
        const active = list.projects.filter((one) => one.state === "active");
        setProjects(active);
        setProject((was) => was || active[0]?.name || "");
      },
      () => setProjects([]),
    );
  }, [open]);
  const tooLong = [...label].length > 64;
  const ready = project !== "" && label.trim() !== "" && !tooLong;
  const submit = async () => {
    const ms = delegateLives.find((row) => row.label === life)?.ms ?? 0;
    const made = await change.run(() => createDelegate(project, label.trim(), ms));
    if (made) {
      setLabel("");
      onCreated(made);
    }
  };
  return (
    <Dialog
      open={open}
      title="Create delegate"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={change.busy} disabled={!ready} onClick={() => void submit()}>
            Create
          </Button>
        </>
      }
    >
      <div className="field-stack">
        <p className="small">The delegate acts for you in one project, on this machine's socket and never on this page. Its consent operations become cards you confirm.</p>
        <Field label="Project">
          <Select value={project} onChange={(event) => setProject(event.target.value)} disabled={projects === null}>
            {(projects ?? []).map((one) => (
              <option key={one.id} value={one.name}>
                {one.display_name || one.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Label" help="What the delegate is, as the audit and its cards name it. 1 to 64 characters." error={tooLong ? "Use 64 characters or fewer." : undefined}>
          <input className="input" type="text" autoComplete="off" value={label} onChange={(event) => setLabel(event.target.value)} />
        </Field>
        <Field label="Expires in">
          <Select value={life} onChange={(event) => setLife(event.target.value)}>
            {delegateLives.map((row) => (
              <option key={row.label} value={row.label}>
                {row.label}
              </option>
            ))}
          </Select>
        </Field>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </div>
    </Dialog>
  );
}

function ShownOnce({ created, onClose }: { created: DelegateCreated | null; onClose: () => void }) {
  const toast = useToast();
  const line = created ? exportLine(created.token) : "";
  return (
    <Dialog
      open={created !== null}
      title="Delegate created"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button variant="primary" onClick={onClose}>
            Done
          </Button>
        </>
      }
    >
      {created ? (
        <div className="field-stack">
          <p className="small">
            <strong>{created.label}</strong> acts in project {created.project} until <Until at={created.expires_at} />. Run this in the agent's shell:
          </p>
          <div className="row-tight token-row">
            <code className="mono token">{line}</code>
            <Button
              small
              icon="copy"
              onClick={() =>
                void navigator.clipboard?.writeText(line).then(
                  () => toast("success", "Copied the delegate credential."),
                  () => toast("warning", "The browser did not let this page copy. Select the line and copy it."),
                )
              }
            >
              Copy
            </Button>
          </div>
          <p className="small">{shownOnce}</p>
        </div>
      ) : null}
    </Dialog>
  );
}
