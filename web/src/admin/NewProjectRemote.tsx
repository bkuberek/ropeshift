import { useEffect, useState } from "react";

import type { TemplateWriteReport } from "../api/admin";
import { readForgeAccounts } from "../api/projectRemote";
import type { ForgeAccountState, RepositoryReport, Visibility } from "../api/projectRemote";
import { Button, Segmented } from "../ui/controls";
import { Banner, SkeletonRows } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Icon } from "../ui/Icon";
import type { RemoteMode, Row, RowRemote } from "./projectRemoteRequest";
import { newRow, rowRemote } from "./projectRemoteRequest";
import { plainText } from "./untrustedText";

// The parts of the Start a project from a template dialog that make a new project's remote (FR-TPL-6, FR-TPL-7): the code
// repositories, the Remote section with the GitHub account it reads, and what each repository got. Text a tool printed on
// the worker, such as a step's detail, is untrusted: every part here shows it as plain text through plainText.

type Copy = (text: string, what: string) => void;

// The account a worker's gh is signed in as, read when the member chooses Create on GitHub.
export type Account =
  | { kind: "idle" }
  | { kind: "checking" }
  | { kind: "ready"; login: string; owners: string[] }
  | { kind: "problem"; state: ForgeAccountState | "expired" | "refused"; detail: string; remedy: string };

// useGitHubAccount reads the GitHub account of a worker while it is active, and again when asked. Leaving or changing the
// worker ends the wait for the one before.
export function useGitHubAccount(active: boolean, worker: string): { account: Account; check: () => void } {
  const [account, setAccount] = useState<Account>({ kind: "idle" });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!active || !worker) {
      setAccount({ kind: "idle" });
      return;
    }
    const controller = new AbortController();
    setAccount({ kind: "checking" });
    readForgeAccounts(worker, { signal: controller.signal }).then(
      (view) => {
        if (controller.signal.aborted) {
          return;
        }
        const report = view.report;
        if (view.state !== "reported" || !report) {
          setAccount({ kind: "problem", state: "expired", detail: "The worker did not report within two minutes.", remedy: "" });
        } else if (report.state === "ok" && (report.owners?.length || report.login)) {
          setAccount({ kind: "ready", login: report.login ?? "", owners: report.owners?.length ? report.owners : [report.login ?? ""] });
        } else {
          setAccount({ kind: "problem", state: report.state, detail: report.detail ?? "", remedy: report.remedy ?? "" });
        }
      },
      (error: unknown) => {
        if (!controller.signal.aborted) {
          setAccount({ kind: "problem", state: "refused", detail: error instanceof Error ? error.message : String(error), remedy: "" });
        }
      },
    );
    return () => controller.abort();
  }, [active, worker, attempt]);
  return { account, check: () => setAttempt((was) => was + 1) };
}

// Untrusted shows what a tool printed as plain text, with its line breaks, as a text node and nothing else.
function Untrusted({ text }: { text: string | undefined }) {
  return <pre className="event-record mono small">{plainText(text)}</pre>;
}

// Command is a command a person runs, with Copy.
function Command({ command, copy }: { command: string; copy: Copy }) {
  const text = plainText(command);
  return (
    <li className="row">
      <code className="mono small">{text}</code>
      <span className="spacer" />
      <Button small icon="copy" aria-label={`Copy ${text}`} onClick={() => copy(text, text)}>
        Copy
      </Button>
    </li>
  );
}

const unanswered = (message: string) => (
  <p className="msg msg-error" role="alert">
    <Icon name="warning" size="sm" />
    <span>{message}</span>
  </p>
);

const kinds = [
  { value: "new", label: "New" },
  { value: "existing", label: "Existing" },
] as const;

type RowsProps = {
  rows: Row[];
  setRows: (rows: Row[]) => void;
  mode: RemoteMode;
  remotes: boolean;
  worker: string;
  owners: string[];
  problem: string;
};

// RepositoryRows are the code repositories of a workspace: each a name, new or existing, and, for an existing one, the remote
// its clone comes from, or, for a new one, the remote it gets. A new one is written at repos/<name> with its own first commit.
export function RepositoryRows({ rows, setRows, mode, remotes, worker, owners, problem }: RowsProps) {
  const change = (at: number, over: Partial<Row>) => setRows(rows.map((row, index) => (index === at ? { ...row, ...over } : row)));
  return (
    <fieldset className="field plain-fieldset">
      <legend className="label">Code repositories</legend>
      <p className="help">Each is at repos/&lt;name&gt; and is listed in the workspace's manifest. A new one is written from the repository shape, and an existing one is cloned by the workspace's Makefile.</p>
      {remotes ? null : <p className="help">{`${worker} speaks an older protocol, so it cannot write a new code repository. Upgrade it to create a project's remote.`}</p>}
      {rows.map((row, at) => {
        const existing = row.existing || !remotes;
        const choice = rowRemote(row, mode);
        return (
          <fieldset className="field plain-fieldset" key={at}>
            <legend className="label">{`Code repository ${at + 1}`}</legend>
            <div className="field-stack">
              <Field label="Name">
                <input className="input mono" autoComplete="off" spellCheck={false} value={row.name} onChange={(event) => change(at, { name: event.target.value })} />
              </Field>
              {remotes ? (
                <Segmented
                  label={`Code repository ${at + 1} is new or existing`}
                  value={existing ? "existing" : "new"}
                  choices={kinds.map((kind) => ({ ...kind }))}
                  onChange={(value) => change(at, { existing: value === "existing" })}
                />
              ) : null}
              {existing ? (
                <Field label="Remote URL" help="What its clone is made from.">
                  <input className="input mono" autoComplete="off" spellCheck={false} placeholder="git@github.com:acme/api.git…" value={row.remote} onChange={(event) => change(at, { remote: event.target.value })} />
                </Field>
              ) : mode === "none" ? null : (
                <>
                  <Field label="Remote">
                    <Select value={mode === "url" && row.choice === "same" ? "url" : row.choice} onChange={(event) => change(at, { choice: event.target.value as RowRemote })}>
                      {mode === "create" ? <option value="same">Same as above</option> : null}
                      <option value="url">A URL</option>
                      <option value="none">None</option>
                    </Select>
                  </Field>
                  {choice === "url" ? (
                    <Field label="Remote URL">
                      <input className="input mono" autoComplete="off" spellCheck={false} placeholder="git@github.com:acme/api.git…" value={row.url} onChange={(event) => change(at, { url: event.target.value })} />
                    </Field>
                  ) : null}
                  {choice === "same" && owners.length > 1 ? (
                    <Field label="Owner" help="Leave it at the owner above, or create this repository under another.">
                      <Select value={row.owner} onChange={(event) => change(at, { owner: event.target.value })}>
                        <option value="">Same as above</option>
                        {owners.map((owner) => (
                          <option key={owner} value={owner}>
                            {owner}
                          </option>
                        ))}
                      </Select>
                    </Field>
                  ) : null}
                </>
              )}
              <div>
                <Button small aria-label={`Remove code repository ${at + 1}`} disabled={rows.length === 1} onClick={() => setRows(rows.filter((_, index) => index !== at))}>
                  Remove
                </Button>
              </div>
            </div>
          </fieldset>
        );
      })}
      {problem ? unanswered(problem) : null}
      <Button small icon="plus" onClick={() => setRows([...rows, newRow()])}>
        Add
      </Button>
    </fieldset>
  );
}

const modes: { value: RemoteMode; label: string }[] = [
  { value: "none", label: "None" },
  { value: "url", label: "Add a remote URL" },
  { value: "create", label: "Create on GitHub" },
];

type RemoteProps = {
  worker: string;
  // blocked says why the section is disabled, or nothing: the first commit is unchecked, or the worker speaks an older protocol.
  blocked: string;
  mode: RemoteMode;
  setMode: (mode: RemoteMode) => void;
  url: string;
  setUrl: (url: string) => void;
  urlLabel: string;
  problem: string;
  account: Account;
  check: () => void;
  owner: string;
  setOwner: (owner: string) => void;
  visibility: Visibility;
  setVisibility: (visibility: Visibility) => void;
  copy: Copy;
};

// RemoteSection is what the folder's own repository gets: no remote, a URL added as origin, or a repository created on GitHub
// under an owner, private unless the member chooses public.
export function RemoteSection({ worker, blocked, mode, setMode, url, setUrl, urlLabel, problem, account, check, owner, setOwner, visibility, setVisibility, copy }: RemoteProps) {
  return (
    <fieldset className="field plain-fieldset" disabled={Boolean(blocked)}>
      <legend className="label">Remote</legend>
      {blocked ? <p className="help">{blocked}</p> : <p className="help">Ropeshift pushes the first commit to it. Choose None to add a remote later.</p>}
      {modes.map((row) => (
        <label className="check-row" key={row.value}>
          <input type="radio" name="project-remote" checked={mode === row.value} onChange={() => setMode(row.value)} />
          <span>{row.label}</span>
        </label>
      ))}
      {mode === "url" && !blocked ? (
        <Field label={urlLabel} error={problem || undefined}>
          <input className="input mono" autoComplete="off" spellCheck={false} placeholder="git@github.com:acme/harbor.git…" value={url} onChange={(event) => setUrl(event.target.value)} />
        </Field>
      ) : null}
      {mode === "create" && !blocked ? (
        <div className="field-stack">
          <p className="help">GitHub is the only forge ropeshift creates repositories on. To create one elsewhere, create it by hand and choose Add a remote URL.</p>
          <GitHubAccount worker={worker} account={account} check={check} owner={owner} setOwner={setOwner} visibility={visibility} setVisibility={setVisibility} copy={copy} problem={problem} />
        </div>
      ) : null}
    </fieldset>
  );
}

const states: Record<string, string> = {
  gh_missing: "The GitHub CLI is not installed.",
  gh_outdated: "The GitHub CLI is too old.",
  gh_signed_out: "The GitHub CLI is not signed in.",
  gh_scope_missing: "The GitHub CLI's sign-in cannot create repositories.",
  expired: "The worker did not answer.",
  refused: "The daemon refused the check.",
};

type AccountProps = Pick<RemoteProps, "worker" | "account" | "check" | "owner" | "setOwner" | "visibility" | "setVisibility" | "copy" | "problem">;

// GitHubAccount says what a worker's gh can do: that it is being checked, what to run on the worker when it cannot create
// repositories, or the owners to choose from and the visibility.
function GitHubAccount({ worker, account, check, owner, setOwner, visibility, setVisibility, copy, problem }: AccountProps) {
  if (account.kind === "checking" || account.kind === "idle") {
    return (
      <div role="status" className="field-stack">
        <p>{`Checking gh on ${worker}`}</p>
        <SkeletonRows rows={1} />
      </div>
    );
  }
  if (account.kind === "problem") {
    return (
      <div className="field-stack">
        <Banner tone="warning">
          <p>{`${worker}: ${states[account.state] ?? "The GitHub CLI cannot create repositories."}`}</p>
          {account.detail ? <Untrusted text={account.detail} /> : null}
        </Banner>
        {account.remedy ? (
          <div>
            <p className="small">Run this on that machine, then check again:</p>
            <ul className="plain-list" aria-label="Command that fixes it">
              <Command command={account.remedy} copy={copy} />
            </ul>
          </div>
        ) : null}
        <div>
          <Button small onClick={check}>
            Check again
          </Button>
        </div>
      </div>
    );
  }
  return (
    <div className="field-stack">
      <Field label="Owner" help={`Signed in to GitHub as ${account.login || "this account"}.`} error={problem || undefined}>
        <Select value={owner} onChange={(event) => setOwner(event.target.value)}>
          {account.owners.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </Select>
      </Field>
      <fieldset className="field plain-fieldset">
        <legend className="label">Visibility</legend>
        <label className="check-row">
          <input type="radio" name="project-visibility" checked={visibility === "private"} onChange={() => setVisibility("private")} />
          <span>Private</span>
        </label>
        <label className="check-row">
          <input type="radio" name="project-visibility" checked={visibility === "public"} onChange={() => setVisibility("public")} />
          <span>
            Public
            <span className="help">Anyone can see a public repository</span>
          </span>
        </label>
      </fieldset>
    </div>
  );
}

// RepositoriesWritten lists each repository a worker made with its commit, its remote, and the outcome of each step, then the
// commands that finish what did not run, each with Copy.
export function RepositoriesWritten({ report, copy }: { report: TemplateWriteReport; copy: Copy }) {
  const repositories = report.repositories ?? [];
  const remaining = report.remaining ?? [];
  return (
    <>
      <ul className="plain-list" aria-label="Repositories">
        {repositories.map((repository: RepositoryReport) => (
          <li key={repository.path} aria-label={repository.name}>
            <p>
              <strong>{plainText(repository.name)}</strong> <code className="mono small">{plainText(repository.path)}</code>
            </p>
            <ul className="plain-list small">
              <li>
                {repository.commit ? (
                  <>
                    First commit <code>{plainText(repository.commit)}</code>
                  </>
                ) : repository.commit_error ? (
                  <>
                    The first commit failed, and the files stay:
                    <Untrusted text={repository.commit_error} />
                  </>
                ) : (
                  "No first commit"
                )}
              </li>
              {repository.remote ? (
                <li>
                  Remote <code>{plainText(repository.remote)}</code>
                </li>
              ) : null}
              {(repository.steps ?? []).map((step) => (
                <li key={step.step}>
                  <strong>{plainText(step.step)}</strong> {plainText(step.outcome)}
                  {step.detail ? <Untrusted text={step.detail} /> : null}
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ul>
      {remaining.length > 0 ? (
        <div>
          <p className="small">These commands finish what did not run:</p>
          <ul className="plain-list" aria-label="Remaining commands">
            {remaining.map((command) => (
              <Command key={command} command={command} copy={copy} />
            ))}
          </ul>
        </div>
      ) : null}
    </>
  );
}
