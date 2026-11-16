import { useEffect, useRef, useState } from "react";

import { newID } from "../admin/logic";
import { useChange } from "../admin/parts";
import { createWork, readRepositories, readRoster, readSources } from "../api/admin";
import { addressOf, go } from "../app/route";
import { useDialogParam } from "../planning/useDialogParam";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { inMore, noWork, workProblems, workRequest } from "./newWork";
import type { WorkDraft } from "./newWork";

type Option = { value: string; label: string };

// What a member chooses among: the project's repositories, and the roster's agents that can take work. A
// read that failed leaves its list empty, and the agents say so, because the API checks the repository and the
// agent when the work becomes ready, and a member may leave both to its defaults.
type Choices = { repositories: Option[]; agents: Option[]; agentsFailed: boolean } | null;

// useChoices reads them once the dialog opens. A project that registers no repository plans in a source the
// daemon reads, as `ropeshift plan start` does, so the sources stand in for the repositories then.
function useChoices(): Choices {
  const [read, setRead] = useState<Choices>(null);
  useEffect(() => {
    let current = true;
    void (async () => {
      const [registered, roster] = await Promise.all([readRepositories().catch(() => null), readRoster("project").catch(() => null)]);
      const held = (registered?.repositories ?? []).filter((row) => row.state !== "removed");
      const repositories: Option[] = held.length
        ? held.map((row) => ({ value: row.id, label: row.name || row.id }))
        : ((await readSources().catch(() => null))?.sources ?? []).map((row) => ({ value: row.instance, label: row.instance }));
      const agents = (roster?.roster.agents ?? []).filter((row) => row.enabled).map((row) => ({ value: row.name, label: row.name }));
      if (current) {
        setRead({ repositories, agents, agentsFailed: roster === null });
      }
    })();
    return () => {
      current = false;
    };
  }, []);
  return read;
}

// NewWorkOrderDialog records a work order as `ropeshift work create` does: a title and the criteria first, and
// under More the rest of what the command takes. The work order passes contract review as every one does, and
// the page opens it once it is made.
function NewWorkOrderDialog({ onClose }: { onClose: () => void }) {
  const choices = useChoices();
  const [draft, setDraft] = useState<WorkDraft>(noWork);
  const [tried, setTried] = useState(false);
  const change = useChange();
  const toast = useToast();
  // The ID of this attempt holds until the dialog closes, so a press after a refusal finds what the first made.
  const id = useRef(newID());
  const field = useRef<HTMLInputElement>(null);
  const more = useRef<HTMLDetailsElement>(null);
  const problems = tried ? workProblems(draft) : {};
  const set = (next: Partial<WorkDraft>) => setDraft((was) => ({ ...was, ...next }));

  const create = async () => {
    setTried(true);
    const found = workProblems(draft);
    if (Object.keys(found).length > 0) {
      // A problem under More is shown where it is, so the section opens.
      if (inMore(found) && more.current) {
        more.current.open = true;
      }
      return;
    }
    const made = await change.run(() => createWork(workRequest(id.current, draft)));
    if (made) {
      toast("success", `Created ${made.title || made.id}.`);
      onClose();
      go(addressOf("/work-order", { id: made.id }));
    }
  };

  return (
    <Dialog
      open
      wide
      title="New work order"
      onClose={onClose}
      initialFocus={field}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" icon="plus" loading={change.busy} onClick={() => void create()}>
            Create work order
          </Button>
        </>
      }
    >
      <form
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void create();
        }}
      >
        <p className="small muted">The work order is recorded with the contract its criteria make, and it passes contract review like any other, before a lane can run it.</p>
        <Field label="Title" help="What the work is, in a line." error={problems.title}>
          <input ref={field} className="input" autoComplete="off" value={draft.title} onChange={(event) => set({ title: event.target.value })} />
        </Field>
        <Field label="Criteria" help="What must be true for the work to be accepted, one to a line. Work with no criterion does not become ready.">
          <textarea className="textarea" rows={4} autoComplete="off" value={draft.criteria} onChange={(event) => set({ criteria: event.target.value })} />
        </Field>
        <details className="disclosure" ref={more}>
          <summary>More</summary>
          <div className="field-stack">
            <Field label="Kind" help="A change is work a lane carries out. Plan work writes a plan to the repository, as a planning session does.">
              <Select value={draft.kind} onChange={(event) => set({ kind: event.target.value as WorkDraft["kind"] })}>
                <option value="change">Change</option>
                <option value="plan">Plan</option>
              </Select>
            </Field>
            <Field label="Statement" help="What this work must deliver, in words a lane can act on.">
              <textarea className="textarea" rows={3} autoComplete="off" value={draft.statement} onChange={(event) => set({ statement: event.target.value })} />
            </Field>
            <Field label="Repository" help="Where its lane runs. Leave it empty and the work names none.">
              <Select value={draft.repository} onChange={(event) => set({ repository: event.target.value })}>
                <option value="">{choices === null ? "Reading…" : "None"}</option>
                {(choices?.repositories ?? []).map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Depends on" help="Work orders this one waits for, by ID, separated by a space or a comma." error={problems.dependsOn}>
              <textarea className="textarea mono" rows={2} autoComplete="off" spellCheck={false} value={draft.dependsOn} onChange={(event) => set({ dependsOn: event.target.value })} />
            </Field>
            <Field label="Requirements" help="The requirements the criteria serve, by key such as FR-RETRY-1, separated by a space or a comma." error={problems.requirements}>
              <textarea className="textarea mono" rows={2} autoComplete="off" spellCheck={false} value={draft.requirements} onChange={(event) => set({ requirements: event.target.value })} />
            </Field>
            <Field
              label="Agent"
              help={choices?.agentsFailed ? "The agents could not be read. Leave it empty and the default for the work's kind and size applies." : "The roster agent its lane runs as."}
            >
              <Select value={draft.agent} onChange={(event) => set({ agent: event.target.value })}>
                <option value="">The default for its kind and size</option>
                {(choices?.agents ?? []).map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Size" help="How much work it is, which the estimates and the default agent read.">
              <Select value={draft.size} onChange={(event) => set({ size: event.target.value as WorkDraft["size"] })}>
                <option value="">Not set</option>
                <option value="small">Small</option>
                <option value="medium">Medium</option>
                <option value="large">Large</option>
              </Select>
            </Field>
            <Field label="Priority" help="A whole number. A higher priority comes first." error={problems.priority}>
              <input className="input" inputMode="numeric" autoComplete="off" value={draft.priority} onChange={(event) => set({ priority: event.target.value })} />
            </Field>
            <Field label="Work order ID (optional)" help="Leave it empty and ropeshift names the work order." error={problems.id}>
              <input className="input mono" autoComplete="off" spellCheck={false} value={draft.id} onChange={(event) => set({ id: event.target.value })} />
            </Field>
          </div>
        </details>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </form>
    </Dialog>
  );
}

// NewWorkOrder is the button that opens the dialog, in the heading of the work orders. The address can open it
// too, which is how the command palette does.
export function NewWorkOrder() {
  const { open, show, close } = useDialogParam("new");
  return (
    <>
      <Button icon="plus" onClick={show}>
        New work order
      </Button>
      {open ? <NewWorkOrderDialog onClose={close} /> : null}
    </>
  );
}
