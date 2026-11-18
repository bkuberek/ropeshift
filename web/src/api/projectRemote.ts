import { call } from "./client";

// The remote a new project's repositories get (FR-TPL-6, FR-TPL-7): a URL added as origin, or a repository created on
// GitHub and pushed to. The request, the report, and the forge account read that names the owners a repository may be
// created under are what a worker on plane protocol 6 or later understands.

// The oldest plane protocol a worker is offered a request that holds a remote, and a forge account read. A worker on an
// older one drops those fields and would write the files with no remote, so the daemon refuses a request for it.
export const protocolProjectRemotes = 6;

export type Visibility = "private" | "public";

// A repository to create on GitHub under an owner. An absent visibility is private.
export type RemoteCreate = { forge: "github"; owner: string; visibility?: Visibility };

// The remote of one repository: a URL, or a repository to create, and never both.
export type ProjectRemote = { url?: string; create?: RemoteCreate };

// One step of giving a repository its remote, with how it ended and what git or gh said. The detail is clipped stderr of
// a tool the worker ran, so a page shows it as plain text and nothing else.
export type RemoteStep = { step: "create" | "origin" | "push"; outcome: "done" | "failed" | "skipped"; detail?: string };

// What a worker did for a repository it made: the folder's own, at path ".", or a new code repository, at repos/<name>.
export type RepositoryReport = { name: string; path: string; commit?: string; commit_error?: string; remote?: string; steps?: RemoteStep[] };

export type ForgeAccountState = "ok" | "gh_missing" | "gh_outdated" | "gh_signed_out" | "gh_scope_missing";

// What a worker's gh can do for a member: whether it can create repositories, the account it is signed in as, and the
// owners a repository may be created under, the login first. A state other than ok comes with the command that fixes it.
export type ForgeAccountReport = {
  request: string;
  state: ForgeAccountState;
  login?: string;
  git_protocol?: string;
  owners?: string[];
  detail?: string;
  remedy?: string;
};

// A forge account read as the daemon reads it: offered to its worker for two minutes, then reported or expired.
export type ForgeAccountReadView = {
  id: string;
  worker: string;
  requested_by: string;
  requested_at: number;
  expires_at: number;
  forge: string;
  state: "offered" | "reported" | "expired";
  report?: ForgeAccountReport;
  reported_at?: number;
};

const readsPath = (worker: string) => `/v1/workers/${encodeURIComponent(worker)}/forge-account-reads`;

// How long the read waits between two looks at a request, and how long past its expiry it keeps looking, in milliseconds.
const everyMs = 1000;
const graceMs = 5000;

function pause(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException("aborted", "AbortError"));
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", stop);
      resolve();
    }, ms);
    const stop = () => {
      clearTimeout(timer);
      reject(new DOMException("aborted", "AbortError"));
    };
    signal?.addEventListener("abort", stop, { once: true });
  });
}

// readForgeAccounts asks a worker to read its GitHub account, as `ropeshift new` does before it asks for an owner, and
// waits for the report. It answers with the read as it ended: reported with the worker's findings, or expired when the
// worker did not report within two minutes. An aborted signal ends the wait with an AbortError.
export async function readForgeAccounts(worker: string, options: { signal?: AbortSignal; every?: number } = {}): Promise<ForgeAccountReadView> {
  const asked = await call<ForgeAccountReadView>("POST", readsPath(worker), { forge: "github" });
  let view = asked;
  for (;;) {
    view = await call<ForgeAccountReadView>("GET", `${readsPath(worker)}/${encodeURIComponent(asked.id)}`);
    if (view.state !== "offered") {
      return view;
    }
    if (Date.now() >= view.expires_at + graceMs) {
      return { ...view, state: "expired" };
    }
    await pause(options.every ?? everyMs, options.signal);
  }
}
