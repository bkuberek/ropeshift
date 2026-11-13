import { useEffect, useId, useState } from "react";
import type { ReactNode } from "react";

import {
  acknowledgeRisk,
  closeWindow,
  downloadInstallationBackup,
  openWindow,
  publishPolicy,
  readAutonomy,
  readCategories,
  readFleet,
  readPolicies,
  readRepositories,
  readRisk,
  readWorkers,
  setFleetCaps,
  setFleetMode,
  setLevel,
} from "../api/admin";
import type { Autonomy, Fleet, Level, PolicyList, PolicyVersion, RepositoryList, Risk, WorkerList } from "../api/admin";
import { readOperations } from "../api/delegates";
import type { Operation } from "../api/delegates";
import type { Live } from "../api/useView";
import { addressOf, go, Link } from "../app/route";
import type { Theme } from "../app/theme";
import { Until } from "../parts/parts";
import { Button, Segmented, Tabs, TabPanel } from "../ui/controls";
import { Absent, Banner, Panel, Refusal, SkeletonRows, TableRegion } from "../ui/display";
import { Diff } from "../ui/Diff";
import { lineDiff } from "../ui/lineDiff";
import { Field, Select } from "../ui/forms";
import { Icon } from "../ui/Icon";
import { Dialog, useToast } from "../ui/overlays";
import { newID, nextVersion, policies, policyBody, settingsOf } from "./logic";
import type { DelegationClass, PolicyKind, PolicyValue, PolicyValues } from "./logic";
import { dateOf, Reading, SaveBar, saveDownload, useChange, useDraft, useLeaveGuard, useRead } from "./parts";

export type Prefs = { theme: Theme; setTheme: (theme: Theme) => void; singleKeys: boolean; setSingleKeys: (on: boolean) => void };

type Section = { id: string; title: string; scope: "installation" | "worker" | "project" | "you"; note: string };

// Every section, by scope. A project's sections live at the project's address and the rest at the
// installation's; the list links each to its own.
const sections: Section[] = [
  { id: "admission", title: "Admission and lane caps", scope: "installation", note: "Caps count the lanes of every project on a machine. The orchestrator and the assistant never count." },
  { id: "autonomy-policy", title: "Autonomy defaults", scope: "installation", note: "The level a new worker starts at, and what an attended worker may run. Every project shares this policy." },
  { id: "backup", title: "Backup", scope: "installation", note: "A copy of the installation's own records: the projects' registry, the members and the passphrase's hash, and the workers." },
  { id: "autonomy", title: "Autonomy and run windows", scope: "worker", note: "How freely each worker claims work, and the windows an attended worker runs in." },
  { id: "fleet", title: "Fleet", scope: "project", note: "The project's fleet mode and the caps a claim is held to, inside the machines' caps." },
  { id: "review", title: "Review policy", scope: "project", note: "How many review rounds a change gets before it waits on a member." },
  { id: "integration", title: "Integration policy", scope: "project", note: "Who merges each repository's work, and where dependent work starts from." },
  { id: "decision", title: "Decision policy", scope: "project", note: "What an orchestrator may decide and assume, and what always waits for a member." },
  { id: "lane", title: "Lane policy", scope: "project", note: "What a change under a running lane does to it." },
  { id: "recovery", title: "Recovery policy", scope: "project", note: "What an orchestrator may do on its own when a lane stalls, trips, fails, or blocks." },
  { id: "delegation", title: "Delegation policy", scope: "project", note: "The approval classes an orchestrator decides for a lane, so they never wait on a member." },
  { id: "delegate-policy", title: "Delegate policy", scope: "project", note: "Consent operations a delegate may run without a card. Only a member changes this policy." },
  { id: "notify", title: "Notifications", scope: "project", note: "How a member hears that something waits on them." },
  { id: "appearance", title: "Appearance and keyboard", scope: "you", note: "This browser's own choices." },
];

const groupWords: Record<Section["scope"], string> = { installation: "Installation", worker: "Workers", project: "Project", you: "You" };

const policyOf: Partial<Record<string, PolicyKind>> = {
  "autonomy-policy": "autonomy",
  decision: "decision",
  lane: "lane",
  recovery: "recovery",
  delegation: "delegation",
  "delegate-policy": "delegate",
  notify: "notify",
};

type SettingsProps = { live: Live; project: string; label: string; archived: boolean; section: string; worker: string; prefs: Prefs };

// SettingsPage lists every section by scope on the left and shows one on the right, each with its save
// bar. A policy saves as a new version after its difference is reviewed, and its history reverts one.
export function SettingsPage({ live, project, label, archived, section, worker, prefs }: SettingsProps) {
  const shown = sections.find((row) => row.id === section) ?? sections.find((row) => row.id === (project ? "fleet" : "admission"))!;
  const addressFor = (row: Section) => (row.scope === "project" && project ? `/projects/${encodeURIComponent(project)}/settings?section=${row.id}` : `/settings?section=${row.id}`);
  return (
    <div className="screen">
      <div className="grid grid-settings">
        <nav className="tree hide-phone" aria-label="Settings sections">
          {(["installation", "worker", "project", "you"] as const).map((scope) => (
            <div key={scope}>
              <p className="overline tree-group">{scope === "project" && label ? `Project · ${label}` : groupWords[scope]}</p>
              {sections
                .filter((row) => row.scope === scope && (scope !== "project" || project))
                .map((row) => (
                  <Link key={row.id} to={addressFor(row)} installation aria-current={row.id === shown.id ? "page" : undefined}>
                    {row.title}
                  </Link>
                ))}
            </div>
          ))}
        </nav>
        <div className="settings-body">
          <SectionPicker sections={sections.filter((row) => row.scope !== "project" || project)} shown={shown} addressFor={addressFor} />
          <SectionBody key={`${shown.id}:${project}`} section={shown} live={live} project={project} label={label} archived={archived && shown.scope === "project"} worker={worker} prefs={prefs} />
        </div>
      </div>
    </div>
  );
}

// SectionPicker is the section list on a phone, where the tree does not fit.
function SectionPicker({ sections: list, shown, addressFor }: { sections: Section[]; shown: Section; addressFor: (row: Section) => string }) {
  const id = useId();
  return (
    <div className="show-phone field">
      <label className="label" htmlFor={id}>
        Section
      </label>
      <Select
        id={id}
        value={shown.id}
        onChange={(event) => {
          const row = list.find((one) => one.id === event.target.value);
          if (row) {
            go(addressFor(row), { installation: true, stay: true });
          }
        }}
      >
        {list.map((row) => (
          <option key={row.id} value={row.id}>
            {groupWords[row.scope]}: {row.title}
          </option>
        ))}
      </Select>
    </div>
  );
}

function SectionHead({ section, actions }: { section: Section; actions?: ReactNode }) {
  return (
    <div className="page-head">
      <div className="page-head-text">
        <h1 tabIndex={-1}>{section.title}</h1>
        <p className="page-note">{section.note}</p>
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </div>
  );
}

function SectionBody({ section, live, project, label, archived, worker, prefs }: { section: Section; live: Live; project: string; label: string; archived: boolean; worker: string; prefs: Prefs }) {
  const kind = policyOf[section.id];
  return (
    <>
      {archived ? <Banner tone="neutral">{label} is archived and read-only. Restore it from Projects to change its settings.</Banner> : null}
      {kind ? <PolicySection section={section} kind={kind} live={live} project={project} archived={archived} /> : null}
      {section.id === "admission" ? <AdmissionSection section={section} live={live} /> : null}
      {section.id === "backup" ? <BackupSection section={section} /> : null}
      {section.id === "autonomy" ? <AutonomySection section={section} live={live} selected={worker} /> : null}
      {section.id === "fleet" ? <FleetSection section={section} live={live} project={project} label={label} archived={archived} /> : null}
      {section.id === "review" ? <ReviewSection section={section} /> : null}
      {section.id === "integration" ? <IntegrationSection section={section} live={live} project={project} label={label} /> : null}
      {section.id === "appearance" ? <AppearanceSection section={section} prefs={prefs} /> : null}
    </>
  );
}

// AdmissionSection shows each machine's lane cap as its worker reports it. The cap is set in the user
// profile on that machine, and this build serves no recommendation to compare it with.
function AdmissionSection({ section, live }: { section: Section; live: Live }) {
  const read = useRead<WorkerList>(() => readWorkers(), "workers", live);
  const workers = read.view?.workers ?? [];
  return (
    <>
      <SectionHead section={section} />
      <Reading failure={read.failure} ready={read.view !== null}>
        <Panel flush foot="A worker reads its caps from the user profile on its own machine, and a repository profile can lower them for its own lanes. Changing a cap from this page, and the recommendation each machine's cores and memory give, arrive with the worker's resource reports.">
          {workers.length === 0 ? (
            <p className="panel-body small muted">No worker has registered, so no machine reports a cap.</p>
          ) : (
            <TableRegion label="Lane caps by machine">
              <table className="table stack-on-phone">
                <thead>
                  <tr>
                    <th scope="col">Machine</th>
                    <th scope="col" className="r">
                      Max lanes
                    </th>
                    <th scope="col">Source</th>
                    <th scope="col">Recommendation</th>
                  </tr>
                </thead>
                <tbody>
                  {workers.map((row) => (
                    <tr key={row.id}>
                      <td data-label="Machine">
                        <strong>{row.name || row.id}</strong>
                      </td>
                      <td data-label="Max lanes" className="r num">
                        {row.manifest?.capacity?.max_lanes ? row.manifest.capacity.max_lanes : "No cap"}
                      </td>
                      <td data-label="Source">
                        <span className="source override">Your user profile</span>
                      </td>
                      <td data-label="Recommendation">
                        <Absent>not reported</Absent>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableRegion>
          )}
        </Panel>
      </Reading>
    </>
  );
}

function BackupSection({ section }: { section: Section }) {
  const change = useChange();
  const toast = useToast();
  return (
    <>
      <SectionHead section={section} />
      <Panel>
        <p className="small">The browser downloads the copy. A project's own records back up from Projects, one project at a time.</p>
        <div className="row-tight form-actions">
          <Button
            variant="primary"
            icon="download"
            loading={change.busy}
            onClick={() =>
              void change.run(() => downloadInstallationBackup()).then((file) => {
                if (file) {
                  saveDownload(file);
                  toast("success", `Backup downloaded: ${file.name}`);
                }
              })
            }
          >
            Back up installation
          </Button>
        </div>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </Panel>
    </>
  );
}

const levelWords: Record<Level, string> = {
  manual: "Claims nothing on its own; a member starts each lane.",
  attended: "Claims while a member has opened a run window, within the attended limits.",
  unattended: "Claims whenever work is ready, with nobody present.",
};

function AutonomySection({ section, live, selected }: { section: Section; live: Live; selected: string }) {
  const workers = useRead<WorkerList>(() => readWorkers(), "workers", live);
  const list = workers.view?.workers ?? [];
  const worker = list.find((row) => row.id === selected)?.id ?? list[0]?.id ?? "";
  const id = useId();
  return (
    <>
      <SectionHead
        section={section}
        actions={
          list.length > 1 ? (
            <>
              <label className="visually-hidden" htmlFor={id}>
                Machine
              </label>
              <Select
                id={id}
                value={worker}
                onChange={(event) => go(addressOf("/settings", { section: "autonomy", worker: event.target.value }), { installation: true, stay: true })}
              >
                {list.map((row) => (
                  <option key={row.id} value={row.id}>
                    Machine: {row.name || row.id}
                  </option>
                ))}
              </Select>
            </>
          ) : null
        }
      />
      <Reading failure={workers.failure} ready={workers.view !== null}>
        {worker ? <WorkerAutonomy key={worker} worker={worker} name={list.find((row) => row.id === worker)?.name || worker} live={live} /> : <Panel>No worker has registered.</Panel>}
      </Reading>
    </>
  );
}

function WorkerAutonomy({ worker, name, live }: { worker: string; name: string; live: Live }) {
  const read = useRead<Autonomy>(() => readAutonomy(worker), `autonomy:${worker}`, live);
  const [risk, setRisk] = useState<Risk | null>(null);
  const [hours, setHours] = useState("2");
  const change = useChange();
  const toast = useToast();
  const view = read.view;
  const act = async (run: () => Promise<unknown>, text: string) => {
    if ((await change.run(run)) !== undefined) {
      toast("success", text);
      read.again();
    }
  };
  return (
    <Reading failure={read.failure} ready={view !== null}>
      {view ? (
        <div className="stack">
          <Panel title={`Level on ${name}`}>
            <div className="field-stack">
              <Segmented<Level>
                label={`Autonomy level of ${name}`}
                value={view.chosen_level ?? view.level}
                choices={[
                  { value: "manual", label: "manual" },
                  { value: "attended", label: "attended" },
                  { value: "unattended", label: "unattended" },
                ]}
                onChange={(level) => void act(() => setLevel(worker, level), `${name} is now ${level}.`)}
              />
              <p className="help">{levelWords[view.chosen_level ?? view.level]}</p>
              {view.chosen_level && view.chosen_level !== view.level ? (
                <p className="msg msg-warning">
                  <Icon name="warning" size="sm" />
                  <span>
                    {name} runs at {view.level} until a member stands behind the stated risk for its sign-in, {view.sign_in}.
                  </span>
                </p>
              ) : null}
              {!view.risk_acknowledged ? (
                <div className="row-tight">
                  <Button small onClick={() => void change.run(() => readRisk()).then((stated) => stated && setRisk(stated))}>
                    Read the stated risk
                  </Button>
                </div>
              ) : (
                <p className="small muted">The stated risk is acknowledged for this worker.</p>
              )}
            </div>
          </Panel>
          <Panel title="Run window">
            {view.window ? (
              <div className="row-tight">
                <span className="small">
                  Open, closes <Until at={view.window.closes_at} />.
                </span>
                <Button small onClick={() => void act(() => closeWindow(worker), `Closed ${name}'s run window. Its lanes keep running.`)}>
                  Close window
                </Button>
              </div>
            ) : (
              <div className="row-tight">
                <Field label="Hours">
                  <input className="input num field-tiny" inputMode="numeric" autoComplete="off" value={hours} onChange={(event) => setHours(event.target.value)} />
                </Field>
                <Button
                  small
                  variant="primary"
                  onClick={() => void act(() => openWindow(worker, newID(), Math.max(1, Number.parseFloat(hours) || 1) * 3_600_000), `Opened a run window on ${name}.`)}
                >
                  Open window
                </Button>
              </div>
            )}
            <p className="help">An attended worker claims only inside a window. Closing one stops no lane.</p>
          </Panel>
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
          <Dialog
            open={risk !== null}
            title="The stated risk"
            onClose={() => setRisk(null)}
            footer={
              <>
                <span className="spacer" />
                <Button onClick={() => setRisk(null)}>Cancel</Button>
                <Button
                  variant="primary"
                  onClick={() => {
                    const digest = risk?.digest ?? "";
                    setRisk(null);
                    void act(() => acknowledgeRisk(worker, digest), `You stand behind the stated risk for ${name}.`);
                  }}
                >
                  I stand behind it
                </Button>
              </>
            }
          >
            <p className="prose small pre-wrap">{risk?.statement}</p>
          </Dialog>
        </div>
      ) : null}
    </Reading>
  );
}

function FleetSection({ section, live, project, label, archived }: { section: Section; live: Live; project: string; label: string; archived: boolean }) {
  const read = useRead<Fleet>(() => readFleet(), `fleet:${project}`, live);
  const saved = read.view;
  const [caps, setCaps, base] = useDraft({
    wip_limit: String(saved?.wip_limit ?? ""),
    lanes_per_repository: String(saved?.lanes_per_repository ?? ""),
    lanes_per_worker: String(saved?.lanes_per_worker ?? ""),
  });
  const change = useChange();
  const toast = useToast();
  const words = { wip_limit: "work in progress", lanes_per_repository: "lanes per repository", lanes_per_worker: "lanes per worker" } as const;
  const changed = saved ? (Object.keys(words) as (keyof typeof words)[]).filter((key) => caps[key] !== base[key]) : [];
  useLeaveGuard(changed.length > 0);
  const summary = saved ? changed.map((key) => `${words[key]} ${saved[key]} → ${caps[key]}`).join(", ") : "";
  const mode = async (next: Fleet["fleet_mode"]) => {
    if (await change.run(() => setFleetMode(next))) {
      toast("success", `${label}'s fleet is now ${next === "run" ? "running" : next === "pause" ? "paused" : "draining"}.`);
      read.again();
    }
  };
  return (
    <>
      <SectionHead section={section} />
      <Reading failure={read.failure} ready={saved !== null}>
        {saved ? (
          <div className="panel">
            <div className="panel-body field-stack">
              <div className="field">
                <span className="label">Fleet mode</span>
                <div className="row-tight">
                  <strong>{saved.fleet_mode === "run" ? "Running" : saved.fleet_mode === "pause" ? "Paused" : "Draining"}</strong>
                  {saved.fleet_mode !== "run" ? (
                    <Button small disabled={archived} onClick={() => void mode("run")}>
                      Run fleet
                    </Button>
                  ) : null}
                  {saved.fleet_mode !== "pause" ? (
                    <Button small disabled={archived} onClick={() => void mode("pause")}>
                      Pause fleet
                    </Button>
                  ) : null}
                  {saved.fleet_mode !== "drain" ? (
                    <Button small disabled={archived} onClick={() => void mode("drain")}>
                      Drain fleet
                    </Button>
                  ) : null}
                </div>
                <p className="help">Pause takes no new claims and starts no new lanes. Drain also starts no new generations, so running lanes end.</p>
              </div>
              <div className="grid grid-3">
                {(Object.keys(words) as (keyof typeof words)[]).map((key) => (
                  <Field key={key} label={key === "wip_limit" ? "Work in progress limit" : key === "lanes_per_repository" ? "Lanes per repository" : "Lanes per worker"} help="0 caps nothing.">
                    <input className="input num" type="number" min={0} step={1} inputMode="numeric" autoComplete="off" disabled={archived} value={caps[key]} onChange={(event) => setCaps((was) => ({ ...was, [key]: event.target.value }))} />
                  </Field>
                ))}
              </div>
              {change.failure ? <Refusal>{change.failure}</Refusal> : null}
            </div>
            <SaveBar
              summary={summary}
              onDiscard={() => setCaps(base)}
              saving={change.busy}
              disabled={archived}
              onSave={() =>
                void change
                  .run(() => setFleetCaps({ wip_limit: Number(caps.wip_limit) || 0, lanes_per_repository: Number(caps.lanes_per_repository) || 0, lanes_per_worker: Number(caps.lanes_per_worker) || 0 }))
                  .then((done) => {
                    if (done) {
                      toast("success", `Saved ${label}'s fleet caps.`);
                      read.again();
                    }
                  })
              }
            />
          </div>
        ) : null}
      </Reading>
    </>
  );
}

// ReviewSection says where review rounds are set today. A repository's profile holds them, and this build
// serves no route that changes them.
function ReviewSection({ section }: { section: Section }) {
  return (
    <>
      <SectionHead section={section} />
      <Panel>
        <p className="small">
          <code>max_review_rounds</code> is set in each repository's profile, which the repository holds as a file, and the review lane reads it from there. This build
          serves no route that changes it, so it is not changed from this page yet.
        </p>
      </Panel>
    </>
  );
}

function IntegrationSection({ section, live, project, label }: { section: Section; live: Live; project: string; label: string }) {
  const read = useRead<RepositoryList>(() => readRepositories(), `repositories:${project}`, live);
  const rows = (read.view?.repositories ?? []).filter((view) => view.state !== "removed");
  return (
    <>
      <SectionHead section={section} />
      <Reading failure={read.failure} ready={read.view !== null}>
        <Panel flush foot="Each repository's record holds its merge authority and where dependent work starts from. Edit one to change it; raising authority shows its review agreement first.">
          {rows.length === 0 ? (
            <p className="panel-body small muted">No repositories in {label} yet.</p>
          ) : (
            <TableRegion label="Integration by repository">
              <table className="table stack-on-phone">
                <thead>
                  <tr>
                    <th scope="col">Repository</th>
                    <th scope="col">Merge authority</th>
                    <th scope="col">Start from</th>
                    <th scope="col">Review target</th>
                    <th scope="col">
                      <span className="visually-hidden">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((view) => (
                    <tr key={view.id}>
                      <td data-label="Repository">
                        <strong>{view.name || view.id}</strong>
                      </td>
                      <td data-label="Merge authority">{view.merge_authority ?? "person_merges"}</td>
                      <td data-label="Start from">{view.start_from}</td>
                      <td data-label="Review target">{view.review_target}</td>
                      <td className="actions-cell">
                        <Link className="btn btn-sm" to={addressOf("/repositories", { repo: view.id })}>
                          Edit record
                        </Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableRegion>
          )}
        </Panel>
      </Reading>
    </>
  );
}

function AppearanceSection({ section, prefs }: { section: Section; prefs: Prefs }) {
  return (
    <>
      <SectionHead section={section} />
      <Panel>
        <div className="field-stack">
          <div className="field">
            <span className="label">Theme</span>
            <Segmented<Theme>
              label="Theme"
              value={prefs.theme}
              choices={[
                { value: "system", label: "System" },
                { value: "light", label: "Light" },
                { value: "dark", label: "Dark" },
              ]}
              onChange={prefs.setTheme}
            />
          </div>
          <div className="field">
            <label className="check-row">
              <input type="checkbox" checked={prefs.singleKeys} onChange={(event) => prefs.setSingleKeys(event.target.checked)} />
              <span>Single-key shortcuts</span>
            </label>
            <p className="help">With these off, only shortcuts with a modifier key work, such as the command palette's.</p>
          </div>
        </div>
      </Panel>
    </>
  );
}

const valueWords = (value: PolicyValue | undefined): string => {
  if (Array.isArray(value)) {
    return value.length === 0 ? "none" : value.map((item) => (typeof item === "string" ? item : item.tool)).join(", ");
  }
  return value === undefined || value === "" ? "unset" : String(value);
};

const same = (a: PolicyValue | undefined, b: PolicyValue | undefined) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null);

// PolicySection edits one versioned policy: its settings, a review of the difference before Save version
// N, and its history, where Revert records an earlier version's settings as the newest.
function PolicySection({ section, kind, live, project, archived }: { section: Section; kind: PolicyKind; live: Live; project: string; archived: boolean }) {
  const read = useRead<PolicyList>(() => readPolicies(kind), `policies:${kind}:${project}`, live);
  const [tab, setTab] = useState<"settings" | "history">("settings");
  const versions = read.view?.versions ?? [];
  const current = versions[0];
  const [values, setValues, saved] = useDraft<PolicyValues>(settingsOf(current?.settings ?? "{}"));
  const [review, setReview] = useState<{ body: string; version: number; title: string } | null>(null);
  const change = useChange();
  const toast = useToast();
  const idBase = useId();
  const fields = policies[kind].fields;
  const changed = fields.filter((field) => !same(values[field.key], saved[field.key]));
  useLeaveGuard(changed.length > 0);
  const summary = changed.map((field) => `${field.key} ${valueWords(saved[field.key])} → ${valueWords(values[field.key])}`).join(", ");
  const next = nextVersion(versions);
  const invalid = fields.some((field) => field.type === "int" && (values[field.key] === "" || Number.isNaN(values[field.key])));
  const save = async () => {
    if (!review) {
      return;
    }
    const made = await change.run(() => publishPolicy(review.body));
    if (made) {
      setReview(null);
      toast("success", `Saved ${section.title.toLowerCase()} version ${made.version}.`);
      read.again();
    }
  };
  return (
    <>
      <SectionHead section={section} />
      <Tabs
        label={`${section.title} sections`}
        idBase={idBase}
        selected={tab}
        onSelect={setTab}
        tabs={[
          { id: "settings", label: "Settings" },
          { id: "history", label: "History", count: versions.length },
        ]}
      />
      <TabPanel idBase={idBase} selected={tab}>
        <Reading failure={read.failure} ready={read.view !== null}>
          {tab === "settings" ? (
            <div className="panel">
              <div className="panel-body field-stack">
                <p className="small muted">
                  {current ? (
                    <>
                      Version {current.version}, by {current.authored_by}, {dateOf(current.created_at)}.
                    </>
                  ) : (
                    "No version yet."
                  )}
                </p>
                {fields.map((field) => (
                  <PolicyField
                    key={field.key}
                    kind={kind}
                    spec={field}
                    value={values[field.key]}
                    saved={saved[field.key]}
                    disabled={archived}
                    failure={review ? "" : change.failure}
                    onChange={(value) => setValues((was) => ({ ...was, [field.key]: value }))}
                  />
                ))}
                {change.failure && !review && !fields.some((field) => field.type === "operations") ? <Refusal>{change.failure}</Refusal> : null}
              </div>
              <SaveBar
                summary={summary}
                saveLabel="Review change"
                disabled={archived || invalid}
                onDiscard={() => setValues(saved)}
                onSave={() => setReview({ body: policyBody(kind, next, values), version: next, title: `Review the ${section.title.toLowerCase()} change` })}
              />
            </div>
          ) : (
            <PolicyHistory
              versions={versions}
              archived={archived}
              onRevert={(version) => setReview({ body: policyBody(kind, next, settingsOf(version.settings)), version: next, title: `Revert to version ${version.version}` })}
            />
          )}
        </Reading>
      </TabPanel>
      <Dialog
        open={review !== null}
        wide
        title={review?.title ?? ""}
        onClose={() => setReview(null)}
        footer={
          <>
            <span className="spacer" />
            <Button onClick={() => setReview(null)}>Keep editing</Button>
            <Button variant="primary" loading={change.busy} onClick={() => void save()}>
              Save version {review?.version}
            </Button>
          </>
        }
      >
        <div className="stack">
          <p className="small">Records version {review?.version}. What reads this policy reads the new version from now on; the earlier ones stay in its history.</p>
          <Diff lines={lineDiff(current?.body ?? "", review?.body ?? "")} label="Policy change" />
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        </div>
      </Dialog>
    </>
  );
}

function PolicyHistory({ versions, archived, onRevert }: { versions: PolicyVersion[]; archived: boolean; onRevert: (version: PolicyVersion) => void }) {
  return (
    <div className="stack">
      {versions.map((version, at) => (
        <Panel
          key={version.id}
          title={`Version ${version.version}`}
          level={3}
          actions={
            <>
              <span className="subtle small">
                {version.authored_by}, {dateOf(version.created_at)}
              </span>
              <span className="spacer" />
              {at === 0 ? (
                <span className="small muted">In force</span>
              ) : (
                <Button small icon="restart" disabled={archived} onClick={() => onRevert(version)}>
                  Revert
                </Button>
              )}
            </>
          }
        >
          <Diff lines={lineDiff(versions[at + 1]?.body ?? "", version.body)} label={`Version ${version.version}'s change`} />
        </Panel>
      ))}
    </div>
  );
}

function PolicyField({ kind, spec, value, saved, disabled, failure = "", onChange }: {
  kind: PolicyKind;
  spec: (typeof policies)[PolicyKind]["fields"][number];
  value: PolicyValue | undefined;
  saved: PolicyValue | undefined;
  disabled: boolean;
  // failure is the API's refusal of the version this field's value made, which the field shows.
  failure?: string;
  onChange: (value: PolicyValue) => void;
}) {
  const source = same(value, saved) ? undefined : { name: "Changed", overridden: true };
  if (spec.type === "bool") {
    return (
      <div className="field">
        <label className="check-row">
          <input type="checkbox" disabled={disabled} checked={Boolean(value)} onChange={(event) => onChange(event.target.checked)} />
          <span>
            {spec.label} <span className="mono small subtle">{spec.key}</span>
          </span>
        </label>
        {spec.help ? <p className="help">{spec.help}</p> : null}
      </div>
    );
  }
  if (spec.type === "int") {
    return (
      <Field label={spec.label} source={source} help={spec.help} error={value === "" ? "Enter a whole number, 0 or more." : undefined}>
        <input
          className="input num field-tiny"
          type="number"
          min={0}
          step={1}
          inputMode="numeric"
          autoComplete="off"
          disabled={disabled}
          value={String(value ?? "")}
          onChange={(event) => onChange(event.target.value === "" ? "" : Number(event.target.value))}
        />
      </Field>
    );
  }
  if (spec.type === "choice") {
    return (
      <Field label={spec.label} source={source} help={spec.help}>
        <Select className="field-narrow" disabled={disabled} value={String(value ?? "")} onChange={(event) => onChange(event.target.value)}>
          {(spec.choices ?? []).map((choice) => (
            <option key={choice} value={choice}>
              {choice}
            </option>
          ))}
        </Select>
      </Field>
    );
  }
  if (spec.type === "list") {
    return <CategoryField label={spec.label} help={spec.help} value={(value as string[] | undefined) ?? []} disabled={disabled} onChange={onChange} />;
  }
  if (spec.type === "operations") {
    return <OperationsField label={spec.label} help={spec.help} value={(value as string[] | undefined) ?? []} disabled={disabled} failure={failure} onChange={onChange} />;
  }
  if (spec.type === "classes") {
    return <ClassesField label={spec.label} help={spec.help} value={(value as DelegationClass[] | undefined) ?? []} disabled={disabled} onChange={onChange} />;
  }
  return (
    <Field label={spec.label} source={source} help={spec.help}>
      <input className="input mono" autoComplete="off" spellCheck={false} disabled={disabled} value={String(value ?? "")} onChange={(event) => onChange(event.target.value)} placeholder={kind === "notify" && spec.key === "webhook_url" ? "https://example.com/hook…" : undefined} />
    </Field>
  );
}

// OperationsField chooses the consent operations a delegate runs without a card, among the ones the class
// table lists. One no policy may exempt is shown disabled, with the API's reason.
function OperationsField({ label, help, value, disabled, failure, onChange }: { label: string; help?: string; value: string[]; disabled: boolean; failure: string; onChange: (value: string[]) => void }) {
  const [operations, setOperations] = useState<Operation[] | null>(null);
  useEffect(() => {
    void readOperations().then(
      (list) => setOperations(list.operations.filter((op) => op.class === "consent")),
      () => setOperations([]),
    );
  }, []);
  const order = (ids: string[]) => (operations ?? []).map((op) => op.id).filter((id) => ids.includes(id)).concat(ids.filter((id) => !operations?.some((op) => op.id === id)));
  return (
    <fieldset className="field">
      <legend className="label">{label}</legend>
      {operations === null ? <SkeletonRows rows={3} /> : null}
      <div className="check-list">
        {(operations ?? []).map((op) => (
          <div key={op.id} className="check-item">
            <label className="check-row">
              <input
                type="checkbox"
                disabled={disabled || !op.exemptable}
                checked={value.includes(op.id)}
                onChange={(event) => onChange(order(event.target.checked ? [...value, op.id] : value.filter((one) => one !== op.id)))}
              />
              <span>
                {op.label} <span className="mono small subtle">{op.id}</span>
              </span>
            </label>
            {!op.exemptable && op.never_reason ? <p className="help">{op.never_reason}</p> : null}
          </div>
        ))}
      </div>
      {help ? <p className="help">{help}</p> : null}
      {failure ? <Refusal>{failure}</Refusal> : null}
    </fieldset>
  );
}

// CategoryField chooses categories among the project's own, which the API lists.
function CategoryField({ label, help, value, disabled, onChange }: { label: string; help?: string; value: string[]; disabled: boolean; onChange: (value: string[]) => void }) {
  const [known, setKnown] = useState<string[] | null>(null);
  useEffect(() => {
    void readCategories().then(
      (list) => setKnown(list.categories),
      () => setKnown([]),
    );
  }, []);
  const every = [...new Set([...(known ?? []), ...value])].sort();
  return (
    <fieldset className="field">
      <legend className="label">{label}</legend>
      <div className="check-grid">
        {every.map((name) => (
          <label key={name} className="check-row">
            <input type="checkbox" disabled={disabled} checked={value.includes(name)} onChange={(event) => onChange(event.target.checked ? [...value, name] : value.filter((one) => one !== name))} />
            <span>{name}</span>
          </label>
        ))}
      </div>
      {help ? <p className="help">{help}</p> : null}
    </fieldset>
  );
}

function ClassesField({ label, help, value, disabled, onChange }: { label: string; help?: string; value: DelegationClass[]; disabled: boolean; onChange: (value: DelegationClass[]) => void }) {
  const set = (at: number, key: keyof DelegationClass, text: string) => onChange(value.map((row, index) => (index === at ? { ...row, [key]: text } : row)));
  return (
    <fieldset className="field">
      <legend className="label">{label}</legend>
      {value.length === 0 ? <p className="small muted">No class: every approval request waits for a member.</p> : null}
      {value.map((row, at) => (
        <div key={at} className="class-row">
          {(["tool", "command_prefix", "path", "agent"] as const).map((key) => (
            <Field key={key} label={key === "command_prefix" ? "Command prefix" : key.charAt(0).toUpperCase() + key.slice(1)}>
              <input className="input mono input-sm" autoComplete="off" spellCheck={false} disabled={disabled} value={row[key] ?? ""} onChange={(event) => set(at, key, event.target.value)} />
            </Field>
          ))}
          <Button small variant="ghost" icon="trash" disabled={disabled} aria-label={`Remove class ${at + 1}`} onClick={() => onChange(value.filter((_, index) => index !== at))} />
        </div>
      ))}
      <div className="row-tight">
        <Button small icon="plus" disabled={disabled} onClick={() => onChange([...value, { tool: "", command_prefix: "", path: "", agent: "" }])}>
          Add class
        </Button>
      </div>
      {help ? <p className="help">{help}</p> : null}
    </fieldset>
  );
}
