import type { TemplateRepository, TemplateShape, TemplateWriteRequest } from "../api/admin";
import type { ProjectRemote, RemoteCreate, Visibility } from "../api/projectRemote";

// The first question of the dialog and `ropeshift new`: whether a project lives in one repository or in several. One
// writes the monorepo shape, several the workspace shape, and a code repository of a project planned elsewhere the
// repository shape. Unanswered, nothing is sent.
export type Question = "" | "one" | "several" | "elsewhere";

// What the folder's own repository gets: no remote, a URL, or a repository created on GitHub.
export type RemoteMode = "none" | "url" | "create";

// What a new code repository gets: the same as the folder's own repository, a URL of its own, or no remote.
export type RowRemote = "same" | "url" | "none";

export type Row = {
  name: string;
  existing: boolean;
  // remote is an existing repository's remote, which `make setup` clones from.
  remote: string;
  choice: RowRemote;
  url: string;
  // owner overrides the one owner for a repository created on GitHub, and is empty to use it.
  owner: string;
};

export const newRow = (): Row => ({ name: "", existing: false, remote: "", choice: "same", url: "", owner: "" });

export type Draft = {
  question: Question;
  project: string;
  firstCommit: boolean;
  mode: RemoteMode;
  url: string;
  owner: string;
  visibility: Visibility;
  rows: Row[];
  // remotes says the chosen worker is offered a request that holds a remote and new code repositories.
  remotes: boolean;
};

export const shapeOf = (question: Question): TemplateShape | "" => (question === "one" ? "monorepo" : question === "several" ? "workspace" : question === "elsewhere" ? "repository" : "");

// remoteMode is what the folder's own repository gets: a remote needs the first commit, which ropeshift pushes, and a
// worker that speaks an older protocol would drop it.
export const remoteMode = (draft: Draft): RemoteMode => (draft.firstCommit && draft.remotes ? draft.mode : "none");

// rowRemote is what a new code repository gets, given what the folder's own repository gets. With a URL for each
// repository there is no URL to share, so Same as above means a URL of its own.
export function rowRemote(row: Row, mode: RemoteMode): RowRemote {
  if (row.existing || mode === "none") {
    return "none";
  }
  if (mode === "url") {
    return row.choice === "none" ? "none" : "url";
  }
  return row.choice;
}

// How many repositories the request creates on GitHub.
export function creating(draft: Draft): number {
  const mode = remoteMode(draft);
  const own = mode === "create" ? 1 : 0;
  return own + (draft.question === "several" ? draft.rows.filter((row) => rowRemote(row, mode) === "same").length : 0);
}

// urlProblem says what is wrong with a remote URL before it is sent, or nothing: the daemon refuses a URL that is empty,
// holds whitespace, or starts with a dash, which git would read as an option.
export function urlProblem(url: string): string {
  const text = url.trim();
  if (!text) {
    return "Enter the remote URL.";
  }
  if (/\s/.test(text) || text.startsWith("-")) {
    return "A remote URL has no spaces and does not start with a dash.";
  }
  return "";
}

export type Problem = { field: "question" | "rows" | "remote"; message: string };

export type Built = { request: TemplateWriteRequest; problem?: undefined } | { request?: undefined; problem: Problem };

// create is the repository the dialog asks for under an owner: private unless the member chose public.
const create = (owner: string, visibility: Visibility): RemoteCreate => ({ forge: "github", owner, visibility });

// buildRequest is the request the daemon is sent for a draft, or the first thing wrong with it. The dialog resolves the one
// owner choice and each repository's override here, so the request names every repository's owner and the daemon holds no
// default.
export function buildRequest(draft: Draft, folder: string): Built {
  const shape = shapeOf(draft.question);
  if (!shape) {
    return { problem: { field: "question", message: "Choose whether the project lives in one repository or in several." } };
  }
  const mode = remoteMode(draft);
  const body: TemplateWriteRequest = { shape, path: folder, first_commit: draft.firstCommit };
  if (mode === "url" || mode === "create") {
    if (mode === "url") {
      const wrong = urlProblem(draft.url);
      if (wrong) {
        return { problem: { field: "remote", message: wrong } };
      }
    } else if (!draft.owner) {
      return { problem: { field: "remote", message: "Choose an owner." } };
    }
    const remote: ProjectRemote = mode === "url" ? { url: draft.url.trim() } : { create: create(draft.owner, draft.visibility) };
    body.remote = remote;
  }
  if (draft.question === "several") {
    const kept: TemplateRepository[] = [];
    const seen = new Set<string>();
    for (const row of draft.rows) {
      const name = row.name.trim();
      if (!name || seen.has(name.toLowerCase())) {
        return { problem: { field: "rows", message: "Give each code repository a name, and a different one from the others." } };
      }
      seen.add(name.toLowerCase());
      if (row.existing || !draft.remotes) {
        const wrong = urlProblem(row.remote);
        if (wrong) {
          return { problem: { field: "rows", message: `Repository ${name}: ${wrong}` } };
        }
        kept.push({ name, remote: row.remote.trim() });
        continue;
      }
      const entry: TemplateRepository = { name, new: true };
      const choice = rowRemote(row, mode);
      if (choice === "url") {
        const wrong = urlProblem(row.url);
        if (wrong) {
          return { problem: { field: "rows", message: `Repository ${name}: ${wrong}` } };
        }
        entry.url = row.url.trim();
      } else if (choice === "same") {
        entry.create = create(row.owner || draft.owner, draft.visibility);
      }
      kept.push(entry);
    }
    body.repositories = kept;
  }
  if (draft.question === "elsewhere" && draft.project.trim()) {
    body.project = draft.project.trim();
  }
  return { request: body };
}

// The owner a member chose last on a worker, kept in the browser as a convenience for that viewer alone. Reading and
// writing can fail where the browser keeps nothing, and the dialog works the same without it.
const ownerKey = (worker: string) => `ropeshift.newProject.owner.${worker}`;

export function rememberedOwner(worker: string): string {
  try {
    return localStorage.getItem(ownerKey(worker)) ?? "";
  } catch {
    return "";
  }
}

export function rememberOwner(worker: string, owner: string): void {
  try {
    localStorage.setItem(ownerKey(worker), owner);
  } catch {
    // A browser that keeps nothing starts at the login again.
  }
}
