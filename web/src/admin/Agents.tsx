import { useEffect, useId, useState } from "react";

import { changeRoster, readRoster, readRosterHistory, revertRoster } from "../api/admin";
import type { AgentFields, RosterAgent, RosterChange, RosterHistory, RosterView, Scope } from "../api/admin";
import type { Live } from "../api/useView";
import { addressOf, go, Link } from "../app/route";
import { Page } from "../parts/parts";
import { Button, Segmented, Tabs, TabPanel } from "../ui/controls";
import { Badge, Banner, EmptyState, Panel, Refusal, TableRegion } from "../ui/display";
import { Diff } from "../ui/Diff";
import { lineDiff } from "../ui/lineDiff";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { agentLines, changedFields } from "./logic";
import { dateOf, Reading, SaveBar, sourceWords, useChange, useDraft, useLeaveGuard, useRead } from "./parts";

const kinds = ["lookup", "explore", "analysis", "reasoning", "mechanical", "implementation", "review"];
const accesses = ["read_only", "writing"];
const classes = ["small", "standard", "large", "frontier"];
const efforts = ["low", "medium", "high", "max"];
const approvals = ["none", "per_use"];

// rowSource is the nearest layer that changed any field of an agent, which the roster table names.
function rowSource(agent: RosterAgent): string {
  const layers = Object.values(agent.sources);
  return layers.includes("project") ? "project" : layers.includes("installation") ? "installation" : "builtin";
}

const fieldsOf = (agent: RosterAgent): AgentFields => ({
  name: agent.name,
  purpose: agent.purpose,
  kind: agent.kind,
  access: agent.access,
  tools: { allow: [...(agent.tools.allow ?? [])], deny: [...(agent.tools.deny ?? [])] },
  class: agent.class,
  models: { ...(agent.models ?? {}) },
  effort: agent.effort,
  approval: agent.approval,
  instructions: agent.instructions,
  report: agent.report,
  enabled: agent.enabled,
});

const listOf = (text: string) =>
  text
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);

type Tab = "roster" | "guide" | "history";

// AgentsPage is the roster editor (FR-AGENT-6) at the scope its address names: the installation's, or
// one project's, which resolves over it (ADR-0025). Every change is a member's, recorded as a version.
export function AgentsPage({ live, project, label, archived, scope, agent, tab }: { live: Live; project: string; label: string; archived: boolean; scope: Scope; agent: string; tab: string }) {
  const read = useRead<RosterView>(() => readRoster(scope), `roster:${scope}:${project}`, live);
  const history = useRead<RosterHistory>(() => readRosterHistory(scope), `roster-history:${scope}:${project}`, live);
  const [adding, setAdding] = useState(false);
  const idBase = useId();
  const shown: Tab = tab === "guide" || tab === "history" ? tab : "roster";
  const agents = read.view?.roster.agents ?? [];
  const version = read.view ? (scope === "project" ? read.view.project_version : read.view.installation_version) : 0;
  const readOnly = archived && scope === "project";
  const here = (params: Record<string, string | undefined>) => {
    const path = addressOf("/agents", { ...params });
    return scope === "project" && project ? `/projects/${encodeURIComponent(project)}${path}` : path;
  };
  const again = () => {
    read.again();
    history.again();
  };
  const selected = agents.find((row) => row.name === agent) ?? agents[0];
  return (
    <Page
      title="Agents"
      note="The roster every session receives, and the routing guide that tells a session which agent to pick."
      actions={
        <>
          <Segmented<Scope>
            label="Scope you edit"
            value={scope}
            choices={[
              { value: "installation", label: "Installation" },
              ...(project ? [{ value: "project" as const, label: `Project: ${label}` }] : []),
            ]}
            onChange={(next) => go(next === "installation" ? "/agents" : `/projects/${encodeURIComponent(project)}/agents`, { installation: true })}
          />
          <Button icon="plus" disabled={readOnly} onClick={() => setAdding(true)}>
            Add agent
          </Button>
        </>
      }
    >
      {readOnly ? <Banner tone="neutral">{label} is archived and read-only.</Banner> : null}
      <Tabs
        label="Agents sections"
        idBase={idBase}
        selected={shown}
        onSelect={(next) => go(here({ tab: next === "roster" ? undefined : next, agent: agent || undefined }), { installation: true, stay: true })}
        tabs={[
          { id: "roster", label: "Roster", count: read.view ? agents.length : undefined },
          { id: "guide", label: "Routing guide" },
          { id: "history", label: "History", count: history.view?.versions.length },
        ]}
      />
      <TabPanel idBase={idBase} selected={shown}>
        <div className="tab-body">
          {shown === "roster" ? (
            <Reading failure={read.failure} ready={read.view !== null}>
              {read.failure && read.view ? <Banner tone="warning">Saving waits until the roster reads again.</Banner> : null}
              <div className="grid grid-agents">
                <Panel flush>
                  <TableRegion label="Roster">
                    <table className="table stack-on-phone">
                      <thead>
                        <tr>
                          <th scope="col">Name</th>
                          <th scope="col">Access</th>
                          <th scope="col">Class</th>
                          <th scope="col">Effort</th>
                          <th scope="col">Approval</th>
                          <th scope="col">Source</th>
                        </tr>
                      </thead>
                      <tbody>
                        {agents.map((row) => {
                          const source = rowSource(row);
                          return (
                            <tr key={row.name} aria-selected={row.name === selected?.name || undefined}>
                              <td data-label="Name">
                                <Link className="mono" to={here({ agent: row.name })} installation stay>
                                  {row.name}
                                </Link>{" "}
                                {row.enabled ? null : (
                                  <Badge glyph="slash" tone="neutral">
                                    Disabled
                                  </Badge>
                                )}
                              </td>
                              <td data-label="Access">{row.access}</td>
                              <td data-label="Class">{row.class}</td>
                              <td data-label="Effort">{row.effort}</td>
                              <td data-label="Approval">{row.approval}</td>
                              <td data-label="Source">
                                <span className={`source${source === "builtin" ? "" : " override"}`}>{sourceWords[source]}</span>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </TableRegion>
                </Panel>
                {selected ? (
                  <AgentEditor
                    key={`${selected.name}:${read.view?.roster.digest}`}
                    agent={selected}
                    models={read.view?.models?.[selected.name] ?? {}}
                    scope={scope}
                    version={version}
                    readOnly={readOnly || Boolean(read.failure)}
                    after={again}
                  />
                ) : null}
              </div>
            </Reading>
          ) : null}
          {shown === "guide" ? (
            <Reading failure={read.failure} ready={read.view !== null}>
              {read.view ? <GuideEditor key={read.view.roster.digest} view={read.view} scope={scope} version={version} readOnly={readOnly} after={again} /> : null}
            </Reading>
          ) : null}
          {shown === "history" ? (
            <Reading failure={history.failure} ready={history.view !== null}>
              {history.view ? <History history={history.view} scope={scope} label={scope === "project" ? label : "the installation"} readOnly={readOnly} after={again} /> : null}
            </Reading>
          ) : null}
        </div>
      </TabPanel>
      {adding ? <AddAgentDialog agents={agents} scope={scope} version={version} onClose={() => setAdding(false)} after={(name) => go(here({ agent: name }), { installation: true, stay: true })} again={again} /> : null}
    </Page>
  );
}

type Review = { title: string; lines: ReturnType<typeof lineDiff>; change: RosterChange };

// SaveRoster asks for one change after its difference is read, and records it as the scope's next version.
function ReviewDialog({ review, scope, version, onClose, after }: { review: Review | null; scope: Scope; version: number; onClose: () => void; after: () => void }) {
  const change = useChange();
  const toast = useToast();
  useEffect(() => change.setFailure(""), [review]);
  return (
    <Dialog
      open={review !== null}
      wide
      title={review?.title ?? ""}
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Keep editing</Button>
          <Button
            variant="primary"
            loading={change.busy}
            onClick={() =>
              review &&
              void change.run(() => changeRoster(scope, review.change)).then((made) => {
                if (made) {
                  toast("success", `Saved ${scope} roster version ${made.version}.`);
                  onClose();
                  after();
                }
              })
            }
          >
            Save version {version + 1}
          </Button>
        </>
      }
    >
      <div className="stack">
        <p className="small">Saves {scope} roster version {version + 1}. Sessions started after this receive it; running lanes keep the roster they started with.</p>
        <Diff lines={review?.lines ?? []} label="Roster change" />
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </div>
    </Dialog>
  );
}

function AgentEditor({ agent, models, scope, version, readOnly, after }: { agent: RosterAgent; models: Record<string, string>; scope: Scope; version: number; readOnly: boolean; after: () => void }) {
  const [draft, setDraft, saved] = useDraft<AgentFields>(fieldsOf(agent));
  const [tab, setTab] = useState<"definition" | "preview">("definition");
  const [review, setReview] = useState<Review | null>(null);
  const toggle = useChange();
  const reset = useChange();
  const toast = useToast();
  const idBase = useId();
  const changes = changedFields(saved, draft);
  useLeaveGuard(Object.keys(changes).length > 0);
  const summary = Object.keys(changes).join(", ");
  const sourceOf = (field: string) => {
    const layer = agent.sources[field] ?? "";
    return { name: sourceWords[layer] ?? layer, overridden: layer !== "builtin" && layer !== "" };
  };
  const set = <K extends keyof AgentFields>(key: K, value: AgentFields[K]) => setDraft((was) => ({ ...was, [key]: value }));
  const before = Object.fromEntries(Object.keys(changes).map((name) => [name, name.startsWith("tools.") ? saved.tools[name.slice(6) as "allow" | "deny"].join(",") : name.startsWith("models.") ? (saved.models?.[name.slice(7)] ?? "") : String(saved[name as keyof AgentFields] ?? "")]));
  const ownsAny = Object.values(agent.sources).includes(scope);
  const drivers = [...new Set([...Object.keys(models), ...Object.keys(draft.models ?? {})])].sort();
  return (
    <section className="panel" aria-labelledby={`${idBase}-name`}>
      <div className="panel-head">
        <h2 id={`${idBase}-name`} className="mono">
          {agent.name}
        </h2>
        {agent.enabled ? (
          <Badge tone="active" glyph="dot">
            Enabled
          </Badge>
        ) : (
          <Badge tone="neutral" glyph="slash">
            Disabled
          </Badge>
        )}
        <span className="spacer" />
        <Button
          small
          disabled={readOnly}
          loading={toggle.busy}
          onClick={() =>
            void toggle.run(() => changeRoster(scope, { op: agent.enabled ? "disable" : "enable", agent: agent.name })).then((made) => {
              if (made) {
                toast("success", `${agent.enabled ? "Disabled" : "Enabled"} ${agent.name}: ${scope} roster version ${made.version}.`);
                after();
              }
            })
          }
        >
          {agent.enabled ? "Disable" : "Enable"}
        </Button>
        {ownsAny ? (
          <Button
            small
            variant="ghost"
            disabled={readOnly}
            loading={reset.busy}
            onClick={() =>
              void reset.run(() => changeRoster(scope, { op: "reset", agent: agent.name })).then((made) => {
                if (made) {
                  toast("success", `Reset ${agent.name} at the ${scope} scope.`);
                  after();
                }
              })
            }
          >
            Reset agent
          </Button>
        ) : null}
      </div>
      {toggle.failure || reset.failure ? (
        <div className="panel-body">
          <Refusal>{toggle.failure || reset.failure}</Refusal>
        </div>
      ) : null}
      <div className="panel-tabs">
        <Tabs
          label="Agent sections"
          idBase={idBase}
          selected={tab}
          onSelect={setTab}
          tabs={[
            { id: "definition", label: "Definition" },
            { id: "preview", label: "Driver preview" },
          ]}
        />
      </div>
      <TabPanel idBase={idBase} selected={tab}>
        {tab === "definition" ? (
          <fieldset className="panel-body field-stack plain-fieldset" disabled={readOnly} aria-label={`Definition of ${agent.name}`}>
            <Field label="Purpose" source={sourceOf("purpose")} help="What a session reads to pick this agent.">
              <textarea className="textarea" rows={3} value={draft.purpose} onChange={(event) => set("purpose", event.target.value)} />
            </Field>
            <div className="grid grid-2">
              <Field label="Kind" source={sourceOf("kind")}>
                <Select value={draft.kind} onChange={(event) => set("kind", event.target.value)}>
                  {kinds.map((one) => (
                    <option key={one}>{one}</option>
                  ))}
                </Select>
              </Field>
              <Field label="Access" source={sourceOf("access")}>
                <Select value={draft.access} onChange={(event) => set("access", event.target.value)}>
                  {accesses.map((one) => (
                    <option key={one}>{one}</option>
                  ))}
                </Select>
              </Field>
              <Field label="Class" source={sourceOf("class")}>
                <Select value={draft.class} onChange={(event) => set("class", event.target.value)}>
                  {classes.map((one) => (
                    <option key={one}>{one}</option>
                  ))}
                </Select>
              </Field>
              <Field label="Effort" source={sourceOf("effort")}>
                <Select value={draft.effort} onChange={(event) => set("effort", event.target.value)}>
                  {efforts.map((one) => (
                    <option key={one}>{one}</option>
                  ))}
                </Select>
              </Field>
              <Field label="Approval" source={sourceOf("approval")}>
                <Select value={draft.approval} onChange={(event) => set("approval", event.target.value)}>
                  {approvals.map((one) => (
                    <option key={one}>{one}</option>
                  ))}
                </Select>
              </Field>
              {drivers.map((driver) => (
                <Field key={driver} label={`Model for ${driver}`} source={sourceOf(`models.${driver}`)}>
                  <input
                    className="input mono"
                    autoComplete="off"
                    spellCheck={false}
                    placeholder={models[driver] ? `From class: ${models[driver]}…` : "From class…"}
                    value={draft.models?.[driver] ?? ""}
                    onChange={(event) => set("models", { ...(draft.models ?? {}), [driver]: event.target.value })}
                  />
                </Field>
              ))}
            </div>
            <div className="grid grid-2">
              <Field label="Tools allowed" source={sourceOf("tools.allow")} help="Comma-separated. Never more than the session's generated rules allow.">
                <input className="input mono" autoComplete="off" spellCheck={false} value={draft.tools.allow.join(", ")} onChange={(event) => set("tools", { ...draft.tools, allow: listOf(event.target.value) })} />
              </Field>
              <Field label="Tools denied" source={sourceOf("tools.deny")} help="Comma-separated.">
                <input className="input mono" autoComplete="off" spellCheck={false} value={draft.tools.deny.join(", ")} onChange={(event) => set("tools", { ...draft.tools, deny: listOf(event.target.value) })} />
              </Field>
            </div>
            <Field label="Instructions" source={sourceOf("instructions")}>
              <textarea className="textarea mono" rows={4} value={draft.instructions} onChange={(event) => set("instructions", event.target.value)} />
            </Field>
            <Field label="Report format" source={sourceOf("report")}>
              <textarea className="textarea" rows={2} value={draft.report} onChange={(event) => set("report", event.target.value)} />
            </Field>
          </fieldset>
        ) : (
          <div className="panel-body">
            {drivers.length === 0 ? (
              <p className="small muted">This control plane reads no driver's class mapping, so it renders no model per driver.</p>
            ) : (
              <TableRegion label="Model per driver">
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Driver</th>
                      <th scope="col">Model</th>
                    </tr>
                  </thead>
                  <tbody>
                    {drivers.map((driver) => (
                      <tr key={driver}>
                        <td>{driver}</td>
                        <td className="mono">{models[driver] || "none"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableRegion>
            )}
          </div>
        )}
      </TabPanel>
      <SaveBar
        summary={summary ? `Changed: ${summary}` : ""}
        saveLabel="Review change"
        disabled={readOnly}
        onDiscard={() => setDraft(saved)}
        onSave={() =>
          setReview({
            title: "Review the roster change",
            lines: [{ kind: "same", text: `[agents.${agent.name}]` }, ...lineDiff(agentLines(before).join("\n"), agentLines(changes).join("\n"))],
            change: { op: "set", agent: agent.name, fields: changes },
          })
        }
      />
      <ReviewDialog review={review} scope={scope} version={version} onClose={() => setReview(null)} after={after} />
    </section>
  );
}

function GuideEditor({ view, scope, version, readOnly, after }: { view: RosterView; scope: Scope; version: number; readOnly: boolean; after: () => void }) {
  const [guide, setGuide] = useState(view.roster.guide);
  const [review, setReview] = useState<Review | null>(null);
  const reset = useChange();
  const toast = useToast();
  const changed = guide !== view.roster.guide;
  useLeaveGuard(changed);
  return (
    <div className="panel">
      <div className="panel-body field-stack">
        <Field
          label="Routing guide"
          source={{ name: sourceWords[view.roster.guide_source] ?? view.roster.guide_source, overridden: view.roster.guide_source !== "builtin" }}
          help="Every session reads this to pick an agent for a piece of work."
        >
          <textarea className="textarea mono" rows={14} disabled={readOnly} value={guide} onChange={(event) => setGuide(event.target.value)} />
        </Field>
        {view.roster.guide_source === scope ? (
          <div className="row-tight">
            <Button
              small
              variant="ghost"
              disabled={readOnly}
              loading={reset.busy}
              onClick={() =>
                void reset.run(() => changeRoster(scope, { op: "guide_reset" })).then((made) => {
                  if (made) {
                    toast("success", `Reset the routing guide at the ${scope} scope.`);
                    after();
                  }
                })
              }
            >
              Reset to the layer below
            </Button>
          </div>
        ) : null}
        {reset.failure ? <Refusal>{reset.failure}</Refusal> : null}
      </div>
      <SaveBar
        summary={changed ? "Routing guide changed" : ""}
        saveLabel="Review change"
        disabled={readOnly}
        onDiscard={() => setGuide(view.roster.guide)}
        onSave={() => setReview({ title: "Review the routing guide change", lines: lineDiff(view.roster.guide, guide), change: { op: "guide_set", guide } })}
      />
      <ReviewDialog review={review} scope={scope} version={version} onClose={() => setReview(null)} after={after} />
    </div>
  );
}

function History({ history, scope, label, readOnly, after }: { history: RosterHistory; scope: Scope; label: string; readOnly: boolean; after: () => void }) {
  const change = useChange();
  const toast = useToast();
  if (history.versions.length === 0) {
    return (
      <div className="panel">
        <EmptyState icon="history" title={`No ${scope} roster version yet`}>
          The {scope} layer of {label} is the layer below as it stands. A saved change records version 1 here.
        </EmptyState>
      </div>
    );
  }
  const newest = history.versions[0]?.version ?? 0;
  return (
    <div className="stack">
      {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      {history.versions.map((entry) => (
        <Panel
          key={entry.id}
          level={3}
          title={`Version ${entry.version}`}
          actions={
            <>
              <span className="subtle small">
                {entry.authored_by}
                {entry.prepared_by ? `, prepared by ${entry.prepared_by}` : ""}, {dateOf(entry.created_at)}
              </span>
              <span className="spacer" />
              {entry.version === newest ? (
                <span className="small muted">In force</span>
              ) : (
                <Button
                  small
                  icon="restart"
                  disabled={readOnly}
                  loading={change.busy}
                  onClick={() =>
                    void change.run(() => revertRoster(scope, entry.version)).then((made) => {
                      if (made) {
                        toast("success", `Recorded version ${entry.version} as ${scope} roster version ${made.version}.`);
                        after();
                      }
                    })
                  }
                >
                  Revert
                </Button>
              )}
            </>
          }
        >
          <p className="small">{entry.summary}</p>
          <ul className="plain-list mono small">
            {entry.difference.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </Panel>
      ))}
    </div>
  );
}

function AddAgentDialog({ agents, scope, version, onClose, after, again }: { agents: RosterAgent[]; scope: Scope; version: number; onClose: () => void; after: (name: string) => void; again: () => void }) {
  const [name, setName] = useState("");
  const [from, setFrom] = useState(agents[0]?.name ?? "");
  const change = useChange();
  const toast = useToast();
  return (
    <Dialog
      open
      title="Add agent"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            loading={change.busy}
            onClick={() =>
              void change.run(() => changeRoster(scope, { op: "add", agent: name.trim(), from })).then((made) => {
                if (made) {
                  toast("success", `Added ${name.trim()}: ${scope} roster version ${made.version}.`);
                  onClose();
                  again();
                  after(name.trim());
                }
              })
            }
          >
            Save version {version + 1}
          </Button>
        </>
      }
    >
      <div className="field-stack">
        <Field label="Name" help="Lowercase and hyphenated, such as sql-migration." error={change.failure || undefined}>
          <input className="input mono" autoComplete="off" spellCheck={false} placeholder="sql-migration…" value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        <Field label="Copy from" help="The new agent starts as a copy of this one, which you then edit.">
          <Select value={from} onChange={(event) => setFrom(event.target.value)}>
            {agents.map((row) => (
              <option key={row.name}>{row.name}</option>
            ))}
          </Select>
        </Field>
      </div>
    </Dialog>
  );
}
