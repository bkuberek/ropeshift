import { Fragment, useEffect, useId, useReducer, useState } from "react";
import type { ReactNode } from "react";

import {
  acknowledgeOneWay,
  acknowledgeRisk,
  closeWindow,
  downloadInstallationBackup,
  openWindow,
  publishPolicy,
  readAutonomy,
  readCaps,
  readCategories,
  readFleet,
  readPolicies,
  readRepositories,
  readRisk,
  readWorkers,
  setCaps,
  setFleetCaps,
  setFleetMode,
  setLevel,
} from "../api/admin";
import type { Autonomy, Fleet, Level, OneWayStanding, PolicyList, PolicyVersion, RepositoryList, Risk, WorkerCaps, WorkerList } from "../api/admin";
import { ApiError } from "../api/client";
import { readOperations } from "../api/delegates";
import type { Operation } from "../api/delegates";
import { useHealth } from "../api/useHealth";
import type { Live } from "../api/useView";
import { addressOf, go, Link } from "../app/route";
import type { Theme } from "../app/theme";
import { Ago, Until } from "../parts/parts";
import { Button, Segmented, Tabs, TabPanel } from "../ui/controls";
import { Banner, Fact, Facts, Panel, Refusal, SkeletonRows, TableRegion } from "../ui/display";
import { Diff } from "../ui/Diff";
import { lineDiff } from "../ui/lineDiff";
import { Field, Select } from "../ui/forms";
import { Icon } from "../ui/Icon";
import { Dialog, useToast } from "../ui/overlays";
import { capsAbove, capsDraftOf, capsProblems, capsSettings, capsWords, minutesBounds, newID, nextVersion, policies, policyBody, policySettings, problemOf } from "./logic";
import type { CapsDraft, DelegationClass, FieldSpec, PolicyKind, PolicyValue, PolicyValues } from "./logic";
import { PassphraseForm } from "./Passphrase";
import { RiskStatement } from "./RiskStatement";
import { PatternsField, RulesField, SpendChannelsField } from "./SpendFields";
import { SpendUsage } from "./SpendUsage";
import { dateOf, Reading, SaveBar, saveDownload, timeOf, useChange, useDraft, useLeaveGuard, useRead } from "./parts";

export type Prefs = { theme: Theme; setTheme: (theme: Theme) => void; singleKeys: boolean; setSingleKeys: (on: boolean) => void };

type Section = { id: string; title: string; scope: "installation" | "worker" | "project" | "you"; note: string };

// Every section, by scope. A project's sections live at the project's address and the rest at the
// installation's; the list links each to its own.
const sections: Section[] = [
  { id: "admission", title: "Admission and lane caps", scope: "installation", note: "Caps count the lanes of every project on a machine. The orchestrator and the assistant never count." },
  { id: "autonomy-policy", title: "Autonomy defaults", scope: "installation", note: "The level a new worker starts at, and what an attended worker may run. Every project shares this policy." },
  { id: "assistant", title: "Assistant", scope: "installation", note: "When an idle assistant session stops. A stopped conversation stays in the ledger and resumes when you send." },
  {
    id: "installation-spend",
    title: "Orchestrator spend",
    scope: "installation",
    note: "The class the orchestrator runs on, and its daily warning, for every project on the machine. An installation admin publishes this policy, and the form reads only for anyone the API refuses.",
  },
  { id: "backup", title: "Backup", scope: "installation", note: "A copy of the installation's own records: the projects' registry, the members and the passphrase's hash, and the workers." },
  { id: "about", title: "About", scope: "installation", note: "The build of the daemon this page talks to, and whether a newer ropeshift is installed beside it." },
  { id: "autonomy", title: "Autonomy and run windows", scope: "worker", note: "How freely each worker claims work, and the windows an attended worker runs in." },
  { id: "fleet", title: "Fleet", scope: "project", note: "The project's fleet mode and the caps a claim is held to, inside the machines' caps." },
  { id: "review", title: "Review policy", scope: "project", note: "How many review rounds a change gets before it waits on a member." },
  { id: "integration", title: "Integration policy", scope: "project", note: "Who merges each repository's work, and where dependent work starts from." },
  { id: "decision", title: "Decision policy", scope: "project", note: "What an orchestrator may decide and assume, and what always waits for a member." },
  { id: "lane", title: "Lane policy", scope: "project", note: "What a change under a running lane does to it." },
  { id: "recovery", title: "Recovery policy", scope: "project", note: "What an orchestrator may do on its own when a lane stalls, trips, fails, or blocks." },
  { id: "delegation", title: "Delegation policy", scope: "project", note: "The approval classes an orchestrator decides for a lane, so they never wait on a member." },
  { id: "delegate-policy", title: "Delegate policy", scope: "project", note: "Consent operations a delegate may run without a card, and whether a delegate's lane start may raise the model and effort. Only a member changes this policy." },
  { id: "notify", title: "Notifications", scope: "project", note: "How a member hears that something waits on them." },
  {
    id: "estimate",
    title: "Estimate policy",
    scope: "project",
    note: "How many comparable outcomes an estimate needs and reads, how many trials a forecast runs, and whether this project shares its delivery history with the installation's pool.",
  },
  {
    id: "spend",
    title: "Spend",
    scope: "project",
    note: "What the project's lanes were charged, and every limit and setting that stops waste: waits, work order and project limits, the hard stop, no progress, and the waiting alert. Each save is a new version.",
  },
  { id: "appearance", title: "Appearance and keyboard", scope: "you", note: "This browser's own choices." },
  { id: "passphrase", title: "Change passphrase", scope: "you", note: "The passphrase that signs you in on this machine. Changing it ends your other sessions." },
];

// The ID of every section, which the accessibility checks hold to a state each.
export const settingsSections = sections.map((section) => section.id);

// settingsList is each section's ID and title, and whether it is a project's, which the command palette lists.
export const settingsList = sections.map((section) => ({ id: section.id, title: section.title, project: section.scope === "project" }));

const groupWords: Record<Section["scope"], string> ={ installation: "Installation", worker: "Workers", project: "Project", you: "You" };

const policyOf: Partial<Record<string, PolicyKind>> = {
  "autonomy-policy": "autonomy",
  assistant: "assistant",
  decision: "decision",
  lane: "lane",
  recovery: "recovery",
  delegation: "delegation",
  "delegate-policy": "delegate",
  notify: "notify",
  estimate: "estimate",
  spend: "spend",
  "installation-spend": "installation_spend",
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
          {(["installation", "worker", "project", "you"] as const).map((scope) => {
            const listed = sections.filter((row) => row.scope === scope && (scope !== "project" || project));
            // The installation's address lists no project's section, and a group with nothing listed has no heading.
            return listed.length ? (
              <div key={scope}>
                <p className="overline tree-group">{scope === "project" && label ? `Project · ${label}` : groupWords[scope]}</p>
                {listed.map((row) => (
                  <Link key={row.id} to={addressFor(row)} installation aria-current={row.id === shown.id ? "page" : undefined}>
                    {row.title}
                  </Link>
                ))}
              </div>
            ) : null;
          })}
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
      {kind && section.id === "spend" ? <SpendSection section={section} live={live} project={project} archived={archived} /> : null}
      {kind && section.id !== "spend" ? <PolicySection section={section} kind={kind} live={live} project={project} archived={archived} /> : null}
      {section.id === "admission" ? <AdmissionSection section={section} live={live} selected={worker} /> : null}
      {section.id === "backup" ? <BackupSection section={section} /> : null}
      {section.id === "about" ? <AboutSection section={section} live={live} /> : null}
      {section.id === "autonomy" ? <AutonomySection section={section} live={live} selected={worker} /> : null}
      {section.id === "fleet" ? <FleetSection section={section} live={live} project={project} label={label} archived={archived} /> : null}
      {section.id === "review" ? <ReviewSection section={section} /> : null}
      {section.id === "integration" ? <IntegrationSection section={section} live={live} project={project} label={label} /> : null}
      {section.id === "appearance" ? <AppearanceSection section={section} prefs={prefs} /> : null}
      {section.id === "passphrase" ? (
        <>
          <SectionHead section={section} />
          <PassphraseForm />
        </>
      ) : null}
    </>
  );
}

// MachineSelect chooses the machine a section of a worker's settings shows, among the workers that registered. A
// section with one machine has nothing to choose, and shows no select.
function MachineSelect({ section, workers, worker }: { section: string; workers: WorkerList["workers"]; worker: string }) {
  const id = useId();
  return (
    <>
      <label className="visually-hidden" htmlFor={id}>
        Machine
      </label>
      <Select id={id} value={worker} onChange={(event) => go(addressOf("/settings", { section, worker: event.target.value }), { installation: true, stay: true })}>
        {workers.map((row) => (
          <option key={row.id} value={row.id}>
            Machine: {row.name || row.id}
          </option>
        ))}
      </Select>
    </>
  );
}

// AdmissionSection sets a machine's lane caps (FR-PROJ-7) through the worker's caps route, beside the
// recommendation the worker computed from its cores and memory. A cap above the recommendation saves with a
// warning that says why, because the cap is the member's decision.
function AdmissionSection({ section, live, selected }: { section: Section; live: Live; selected: string }) {
  const workers = useRead<WorkerList>(() => readWorkers(), "workers", live);
  const list = workers.view?.workers ?? [];
  const worker = list.find((row) => row.id === selected)?.id ?? list[0]?.id ?? "";
  return (
    <>
      <SectionHead section={section} actions={list.length > 1 ? <MachineSelect section="admission" workers={list} worker={worker} /> : undefined} />
      <Reading failure={workers.failure} ready={workers.view !== null}>
        {worker ? <WorkerCapsForm key={worker} worker={worker} name={list.find((row) => row.id === worker)?.name || worker} live={live} /> : <Panel>No worker has registered, so no machine reports a cap.</Panel>}
      </Reading>
    </>
  );
}

const lanesWords = (count: number) => `${count} ${count === 1 ? "lane" : "lanes"}`;

function WorkerCapsForm({ worker, name, live }: { worker: string; name: string; live: Live }) {
  const read = useRead<WorkerCaps>(() => readCaps(worker), `caps:${worker}`, live);
  const saved = read.view;
  const [draft, setDraft, base] = useDraft<CapsDraft>(capsDraftOf(saved ?? undefined));
  const change = useChange();
  const toast = useToast();
  const problems = capsProblems(draft);
  const recommendation = saved?.recommendation;
  const above = capsAbove(draft, recommendation);
  const changed = [
    draft.mode !== base.mode || (draft.mode === "number" && draft.lanes.trim() !== base.lanes) ? `max lanes ${capsWords(base, recommendation?.max_lanes)} → ${capsWords(draft, recommendation?.max_lanes)}` : "",
    draft.perSignIn.trim() !== base.perSignIn ? `lanes per sign-in ${base.perSignIn} → ${draft.perSignIn.trim() || "0"}` : "",
  ].filter(Boolean);
  useLeaveGuard(changed.length > 0);
  const invalid = Boolean(problems.lanes || problems.perSignIn);
  const save = () =>
    void change.run(() => setCaps(worker, capsSettings(draft))).then((made) => {
      if (!made) {
        return;
      }
      toast(
        made.warning ? "warning" : "success",
        made.warning ? `Saved. ${name} max lanes is ${made.max_lanes}, above the recommendation of ${made.warning.recommended}.` : `Saved ${name}'s lane caps.`,
      );
      read.again();
    });
  return (
    <Reading failure={read.failure} ready={saved !== null}>
      {saved ? (
        <div className="stack">
          <div className="panel">
            <div className="panel-body field-stack">
              <fieldset className="field plain-fieldset">
                <legend className="label">
                  Max lanes of {name} <span className="source override">Your user profile</span>
                </legend>
                <Segmented<CapsDraft["mode"]>
                  label={`How the cap of ${name} is set`}
                  value={draft.mode}
                  choices={[
                    { value: "recommended", label: "Recommended" },
                    { value: "none", label: "No cap" },
                    { value: "number", label: "A number" },
                  ]}
                  onChange={(mode) => setDraft((was) => ({ ...was, mode }))}
                />
                {draft.mode === "number" ? (
                  <Field
                    label="Number of lanes"
                    error={problems.lanes}
                    warning={
                      above ? `${above.value} is above the recommendation of ${above.recommended}: ${above.reason}. It saves as you enter it: the cap is your decision.` : undefined
                    }
                  >
                    <input className="input num field-tiny" type="number" min={1} step={1} inputMode="numeric" autoComplete="off" value={draft.lanes} onChange={(event) => setDraft((was) => ({ ...was, lanes: event.target.value }))} />
                  </Field>
                ) : null}
                <p className="help">
                  The most lanes this machine runs at once.{" "}
                  {draft.mode === "recommended"
                    ? recommendation
                      ? `Following the recommendation caps it at ${recommendation.max_lanes}.`
                      : "Until the worker reports a recommendation, nothing caps it."
                    : draft.mode === "none"
                      ? "Nothing caps the lanes of this machine."
                      : null}
                </p>
              </fieldset>
              <Field label="Lanes per sign-in" error={problems.perSignIn} help="Every session on a subscription draws from the same usage limits, so a cap on the lanes of one sign-in keeps parallel lanes from using them up together. 0 caps nothing.">
                <input
                  className="input num field-tiny"
                  type="number"
                  min={0}
                  step={1}
                  inputMode="numeric"
                  autoComplete="off"
                  value={draft.perSignIn}
                  onChange={(event) => setDraft((was) => ({ ...was, perSignIn: event.target.value }))}
                />
              </Field>
              {change.failure ? <Refusal>{change.failure}</Refusal> : null}
            </div>
            <SaveBar summary={changed.join(", ")} onDiscard={() => setDraft(base)} saving={change.busy} disabled={invalid} onSave={save} />
          </div>
          <Panel
            title="Recommendation"
            foot={
              <>
                The caps are saved in the user profile on {name}'s machine, so this page, <code>ropeshift worker caps</code>, and the file are one setting. The worker reads a change at its next measure, with no
                restart. A repository's profile can lower them for its own lanes.
              </>
            }
          >
            {recommendation ? (
              <div className="stack">
                <p>
                  <strong>{lanesWords(recommendation.max_lanes)}</strong>
                </p>
                <p className="small">{recommendation.reason}</p>
                <p className="small muted">
                  {saved.effective_max_lanes ? `In force: ${lanesWords(saved.effective_max_lanes)}.` : "In force: no cap."}
                  {saved.reported_at ? ` Reported ${timeOf(saved.reported_at)}.` : null}
                </p>
              </div>
            ) : (
              <p className="small">{name} has not reported a recommendation yet, so nothing recommends a cap. It reports one after it measures its machine.</p>
            )}
          </Panel>
        </div>
      ) : null}
    </Reading>
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
  return (
    <>
      <SectionHead section={section} actions={list.length > 1 ? <MachineSelect section="autonomy" workers={list} worker={worker} /> : undefined} />
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
  // act reads again and says so after any request that resolved, whatever its body: these routes answer 204.
  const act = async (run: () => Promise<unknown>, text: string) => {
    let answered = false;
    await change.run(async () => {
      await run();
      answered = true;
    });
    if (answered) {
      toast("success", text);
      read.again();
    }
  };
  return (
    <Reading failure={read.failure} ready={view !== null} asOf={view?.as_of} readAt={read.readAt}>
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
              {view.one_way ? <OneWayAcknowledgement worker={worker} name={name} standing={view.one_way} act={act} /> : null}
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
            <RiskStatement text={risk?.statement ?? ""} />
          </Dialog>
        </div>
      ) : null}
    </Reading>
  );
}

// OneWayAcknowledgement is what a member accepts, once for each worker, before the worker's orchestrator at unattended
// decides a one-way question itself: its statement, which is apart from the stated risk, and who accepted it. Until a
// member accepts it, every one-way question reaches a member at unattended too (FR-QD-8).
function OneWayAcknowledgement({ worker, name, standing, act }: { worker: string; name: string; standing: OneWayStanding; act: (run: () => Promise<unknown>, text: string) => Promise<void> }) {
  const accepted = standing.accepted;
  return (
    <div className="field-stack">
      <p className="small">
        <strong>One-way acknowledgement:</strong> {standing.statement}
      </p>
      {accepted ? (
        <p className="small muted">
          Accepted for this worker by {accepted.by}, <Ago at={accepted.at} />. Needs you shows each one-way decision its orchestrator makes, for you to keep or overturn.
        </p>
      ) : (
        <>
          <p className="help">
            Until a member accepts it, every one-way question on {name} reaches a member, at unattended too. Accepting it lets the orchestrator of an unattended worker decide a
            one-way question outside the categories the decision policy keeps, and Needs you shows each such decision for review. It blocks no work.
          </p>
          <div className="row-tight">
            <Button small onClick={() => void act(() => acknowledgeOneWay(worker, standing.digest), `You accepted the one-way acknowledgement for ${name}.`)}>
              Accept the one-way acknowledgement
            </Button>
          </div>
        </>
      )}
    </div>
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

// AboutSection gives the version the daemon runs, with the commit and build date a release build stamps, and
// the release installed at the daemon's path when the daemon could read it. A newer installed release is a
// warning that says how to run it.
function AboutSection({ section, live }: { section: Section; live: Live }) {
  const read = useHealth(live, true);
  const health = read.view;
  return (
    <>
      <SectionHead section={section} />
      <Reading failure={read.failure} ready={health !== null}>
        {health ? (
          <>
            {health.warning ? <Banner tone="warning">{health.warning}</Banner> : null}
            <Panel>
              <Facts label="Daemon build">
                <Fact name="Version" mono>
                  {health.version}
                </Fact>
                {health.commit ? (
                  <Fact name="Commit" mono>
                    {health.commit}
                  </Fact>
                ) : null}
                {health.date ? <Fact name="Built">{health.date}</Fact> : null}
                {health.installed ? (
                  <Fact name="Installed" mono>
                    {health.installed}
                  </Fact>
                ) : null}
              </Facts>
            </Panel>
          </>
        ) : null}
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

// held is what a value holds, where a table with nothing in it holds nothing, as the policy reads it.
const held = (value: PolicyValue | undefined): unknown => (value && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === 0 ? null : (value ?? null));

const valueWords = (value: PolicyValue | undefined, minutes = false): string => {
  if (Array.isArray(value)) {
    return value.length === 0 ? "none" : value.map((item) => (typeof item === "string" ? item : item.tool)).join(", ");
  }
  if (held(value) === null && typeof value === "object") {
    return "none";
  }
  if (value && typeof value === "object") {
    return Object.entries(value as Record<string, string[]>)
      .map(([kind, channels]) => `${kind} to ${channels.join(" and ")}`)
      .join(", ");
  }
  return value === undefined || value === "" ? "unset" : minutes ? `${value} min` : String(value);
};

const same = (a: PolicyValue | undefined, b: PolicyValue | undefined) => JSON.stringify(held(a)) === JSON.stringify(held(b));

// SpendSection is the project's Spend: what its lanes were charged against each limit, above the form that edits every spend
// setting. A saved version moves the limits of every work order at once, so the charged spend is read again with it.
function SpendSection({ section, live, project, archived }: { section: Section; live: Live; project: string; archived: boolean }) {
  const [saved, again] = useReducer((count: number) => count + 1, 0);
  return <PolicySection section={section} kind="spend" live={live} project={project} archived={archived} above={<SpendUsage live={live} project={project} again={saved} />} onSaved={again} />;
}

// PolicySection edits one versioned policy: its settings, a review of the difference before Save version
// N, and its history, where Revert records an earlier version's settings as the newest. A publish the API refuses as
// forbidden leaves the form to read only, with the reason.
function PolicySection({ section, kind, live, project, archived, above, onSaved }: {
  section: Section;
  kind: PolicyKind;
  live: Live;
  project: string;
  archived: boolean;
  above?: ReactNode;
  onSaved?: () => void;
}) {
  const read = useRead<PolicyList>(() => readPolicies(kind), `policies:${kind}:${project}`, live);
  const [tab, setTab] = useState<"settings" | "history">("settings");
  const versions = read.view?.versions ?? [];
  const current = versions[0];
  const fields = policies[kind].fields;
  const consent = useConsentOperations(fields.some((field) => field.type === "operations"));
  // The draft starts from the stored version without what no policy may exempt any more, so dropping it is
  // no change of the person's, and a discard returns to it. A read-only project's form shows the version
  // as it stands.
  const stored = policySettings(kind, current?.settings ?? "{}");
  const { settings: start, dropped } = archived ? { settings: stored, dropped: [] } : withoutStale(stored, fields, consent.exemptable);
  const [values, setValues, saved] = useDraft<PolicyValues>(start);
  const [review, setReview] = useState<{ body: string; version: number; title: string } | null>(null);
  // notice is what the API says still follows from the version just saved, until the next change.
  const [notice, setNotice] = useState("");
  // forbidden says the API refused a publish as the member's to make no longer: the form reads and changes nothing.
  const [forbidden, setForbidden] = useState(false);
  const change = useChange();
  const toast = useToast();
  const idBase = useId();
  const changed = fields.filter((field) => !same(values[field.key], saved[field.key]));
  useLeaveGuard(changed.length > 0);
  const summary = changed.map((field) => `${field.key} ${valueWords(saved[field.key], field.type === "minutes")} → ${valueWords(values[field.key], field.type === "minutes")}`).join(", ");
  const next = nextVersion(versions);
  const invalid = fields.some((field) => problemOf(field, values) !== "");
  const save = async () => {
    if (!review) {
      return;
    }
    const made = await change.run(() =>
      publishPolicy(review.body).catch((error: unknown) => {
        setForbidden(error instanceof ApiError && error.code === "forbidden");
        throw error;
      }),
    );
    if (made) {
      setReview(null);
      setNotice(made.notice ?? "");
      toast("success", `Saved ${section.title.toLowerCase()} version ${made.version}.`);
      read.again();
      onSaved?.();
    }
  };
  const readOnly = archived || forbidden;
  return (
    <>
      <SectionHead section={section} />
      {above}
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
          {notice ? <Banner tone="warning">{notice}</Banner> : null}
          {tab === "settings" ? (
            <div className="panel">
              <div className="panel-body field-stack">
                <p className="small muted">
                  {current && builtInDefault(current) ? (
                    <>Version {current.version}, the built-in default.</>
                  ) : current ? (
                    <>
                      Version {current.version}, by {current.authored_by}, {dateOf(current.created_at)}.
                    </>
                  ) : (
                    "No version yet."
                  )}
                </p>
                {forbidden ? <Banner tone="warning">The API refused this change: you may read this policy, and not publish a version of it. The form reads only.</Banner> : null}
                {fields.map((field, at) => (
                  <Fragment key={field.key}>
                    {field.group && field.group !== fields[at - 1]?.group ? (
                      <h2 className="h4">{field.group}</h2>
                    ) : null}
                    <PolicyField
                      kind={kind}
                      spec={field}
                      value={values[field.key]}
                      saved={saved[field.key]}
                      form={values}
                      disabled={readOnly}
                      operations={consent.operations}
                      dropped={dropped}
                      problem={problemOf(field, values)}
                      failure={review ? "" : change.failure}
                      onChange={(value) => setValues((was) => ({ ...was, [field.key]: value }))}
                    />
                  </Fragment>
                ))}
                {change.failure && !review && !fields.some((field) => field.type === "operations") ? <Refusal>{change.failure}</Refusal> : null}
              </div>
              <SaveBar
                summary={summary}
                saveLabel="Review change"
                disabled={readOnly || invalid}
                onDiscard={() => setValues(saved)}
                onSave={() => setReview({ body: policyBody(kind, next, values), version: next, title: `Review the ${section.title.toLowerCase()} change` })}
              />
            </div>
          ) : (
            <PolicyHistory
              versions={versions}
              archived={readOnly}
              onRevert={(version) => setReview({ body: policyBody(kind, next, policySettings(kind, version.settings)), version: next, title: `Revert to version ${version.version}` })}
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
          <Diff lines={lineDiff(current?.body ?? "", review?.body ?? "")} label="Policy change" context={3} />
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        </div>
      </Dialog>
    </>
  );
}

// builtInDefault says a version is the built-in policy no member has saved. Nothing recorded it, so the API answers
// it with no time, and it has no author to name.
const builtInDefault = (version: PolicyVersion) => version.created_at === 0;

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
              <span className="subtle small">{builtInDefault(version) ? "The built-in default" : `${version.authored_by}, ${dateOf(version.created_at)}`}</span>
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
          <Diff lines={lineDiff(versions[at + 1]?.body ?? "", version.body)} label={`Version ${version.version}'s change`} context={3} />
        </Panel>
      ))}
    </div>
  );
}

function PolicyField({ kind, spec, value, saved, form = {}, disabled, operations = null, dropped = [], problem = "", failure = "", onChange }: {
  kind: PolicyKind;
  spec: (typeof policies)[PolicyKind]["fields"][number];
  value: PolicyValue | undefined;
  saved: PolicyValue | undefined;
  // form is every value of the draft, which a field that depends on another reads.
  form?: PolicyValues;
  disabled: boolean;
  // operations are the class table's consent operations, null until it is read, and dropped the IDs the
  // draft left out of the stored version, which an operations field lists and names.
  operations?: Operation[] | null;
  dropped?: string[];
  // problem is what stops this value from being saved, which the field names.
  problem?: string;
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
      <Field label={spec.label} source={source} help={spec.help} error={problem || undefined}>
        <input
          className="input num field-tiny"
          type="number"
          min={spec.min ?? 0}
          max={spec.max}
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
  if (spec.type === "decimal") {
    return (
      <Field label={spec.label} source={source} help={spec.help} error={problem || undefined}>
        <input
          className="input num field-tiny"
          type="number"
          min={spec.min}
          max={spec.max}
          step="any"
          inputMode="decimal"
          autoComplete="off"
          disabled={disabled}
          value={String(value ?? "")}
          onChange={(event) => onChange(event.target.value === "" ? "" : Number(event.target.value))}
        />
      </Field>
    );
  }
  if (spec.type === "duration") {
    return (
      <Field label={spec.label} source={source} help={spec.help} error={problem || undefined}>
        <input className="input mono field-tiny" autoComplete="off" spellCheck={false} disabled={disabled} value={String(value ?? "")} onChange={(event) => onChange(event.target.value)} />
      </Field>
    );
  }
  if (spec.type === "rules") {
    return <RulesField label={spec.label} help={spec.help} value={(value as string[] | undefined) ?? []} disabled={disabled} onChange={onChange} />;
  }
  if (spec.type === "patterns") {
    return <PatternsField label={spec.label} help={spec.help} value={(value as string[] | undefined) ?? []} disabled={disabled} error={problem || undefined} onChange={onChange} />;
  }
  if (spec.type === "channels") {
    return (
      <SpendChannelsField
        label={spec.label}
        help={spec.help}
        value={(value as unknown as Record<string, string[]> | undefined) ?? {}}
        form={form}
        disabled={disabled}
        error={problem || undefined}
        onChange={(next) => onChange(next as unknown as PolicyValue)}
      />
    );
  }
  if (spec.type === "minutes") {
    return (
      <Field label={spec.label} source={source} help={spec.help} error={problem || undefined}>
        <input
          className="input num field-tiny"
          type="number"
          min={minutesBounds.min}
          max={minutesBounds.max}
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
    return <OperationsField label={spec.label} help={spec.help} value={(value as string[] | undefined) ?? []} operations={operations} dropped={dropped} disabled={disabled} failure={failure} onChange={onChange} />;
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

// useConsentOperations reads the class table for a policy that lists operations: its consent operations,
// and the IDs a delegate policy may exempt. Both are null until it is read, and a read that fails lists no
// operation and leaves what may be exempt unknown.
function useConsentOperations(wanted: boolean): { operations: Operation[] | null; exemptable: Set<string> | null } {
  const [table, setTable] = useState<{ operations: Operation[]; exemptable: Set<string> | null } | null>(null);
  useEffect(() => {
    if (!wanted) {
      return;
    }
    void readOperations().then(
      (list) => setTable({ operations: list.operations.filter((op) => op.class === "consent"), exemptable: new Set(list.operations.filter((op) => op.exemptable).map((op) => op.id)) }),
      () => setTable({ operations: [], exemptable: null }),
    );
  }, [wanted]);
  return { operations: table?.operations ?? null, exemptable: table?.exemptable ?? null };
}

// withoutStale leaves out of a policy's operations fields each ID no policy may exempt any more, once the
// class table says which those are, and names what it left out. A stored version may list one: it exempts
// nothing, and the API refuses it in a new version.
function withoutStale(settings: PolicyValues, fields: FieldSpec[], exemptable: Set<string> | null): { settings: PolicyValues; dropped: string[] } {
  if (!exemptable) {
    return { settings, dropped: [] };
  }
  const out = { ...settings };
  const dropped: string[] = [];
  for (const field of fields) {
    const ids = settings[field.key];
    if (field.type !== "operations" || !Array.isArray(ids)) {
      continue;
    }
    const kept = (ids as string[]).filter((id) => exemptable.has(id));
    dropped.push(...(ids as string[]).filter((id) => !exemptable.has(id)));
    out[field.key] = kept;
  }
  return { settings: out, dropped };
}

// OperationsField chooses the consent operations a delegate runs without a card, among the ones the class
// table lists. One no policy may exempt is shown disabled, with the API's reason, and dropped names what the
// draft left out of the stored version because no policy may exempt it any more.
function OperationsField({ label, help, value, operations, dropped, disabled, failure, onChange }: {
  label: string;
  help?: string;
  value: string[];
  operations: Operation[] | null;
  dropped: string[];
  disabled: boolean;
  failure: string;
  onChange: (value: string[]) => void;
}) {
  const order = (ids: string[]) => (operations ?? []).map((op) => op.id).filter((id) => ids.includes(id)).concat(ids.filter((id) => !operations?.some((op) => op.id === id)));
  return (
    <fieldset className="field">
      <legend className="label">{label}</legend>
      {operations === null ? <SkeletonRows rows={3} /> : null}
      {dropped.length > 0 ? (
        <p className="help">
          The current version lists {dropped.length === 1 ? "an operation" : "operations"} no policy may exempt any more, which exempt nothing and are left out of the next version: <span className="mono">{dropped.join(", ")}</span>.
        </p>
      ) : null}
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
