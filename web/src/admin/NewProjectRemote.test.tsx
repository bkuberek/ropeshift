import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { TemplateWriteReport, TemplateWriteView } from "../api/admin";
import type { ForgeAccountReadView, ForgeAccountReport } from "../api/projectRemote";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { refusal, serveViews } from "../testing";

// A new project's remote (FR-TPL-6, FR-TPL-7): the Remote section of Start a project from a template, the GitHub account it
// reads from the worker, the code repositories' own remotes, and what each repository got. What git and gh printed is untrusted.

const worker = "7c41e9a05d2b4f68b3a1e6d29c084f5a";
const older = "worker-v5";
const folder = "/srv/code/harbor";
const writes = `/v1/workers/${worker}/template-writes`;
const reads = `/v1/workers/${worker}/forge-account-reads`;

const workers = {
  workers: [
    { id: worker, name: "build-host-07", epoch: 1, protocol: 6 },
    { id: older, name: "solo", epoch: 1, protocol: 5 },
  ],
};

const signedIn: ForgeAccountReport = { request: "read-1", state: "ok", login: "ana", git_protocol: "ssh", owners: ["ana", "acme", "zeta-labs"] };

const readOf = (over: Partial<ForgeAccountReadView> = {}): ForgeAccountReadView => ({
  id: "read-1",
  worker,
  requested_by: "member-1",
  requested_at: Date.now(),
  expires_at: Date.now() + 120_000,
  forge: "github",
  state: "reported",
  report: signedIn,
  ...over,
});

const writeOf = (over: Partial<TemplateWriteView> = {}): TemplateWriteView => ({
  id: "request-1",
  worker,
  requested_by: "member-1",
  requested_at: Date.now(),
  expires_at: Date.now() + 120_000,
  shape: "workspace",
  path: folder,
  first_commit: true,
  state: "offered",
  registrable: false,
  ...over,
});

type Served = { sent: Sent[]; reads: ForgeAccountReadView[]; writes: TemplateWriteView[] };

// serve answers the workers, each forge account read the dialog asks, in order, and the template write and its reads.
function serve(account: ForgeAccountReadView[] = [readOf()], written: TemplateWriteView[] = [writeOf()], answer?: (call: Sent) => { status: number; body?: unknown } | undefined): Served {
  const served: Served = { sent: [], reads: [...account], writes: [...written] };
  served.sent = serveViews(
    (call) => {
      const own = answer?.(call);
      if (own) {
        return own;
      }
      if (call.method === "GET" && call.path === "/v1/workers") {
        return { status: 200, body: workers };
      }
      if (call.method === "POST" && call.path === reads) {
        return { status: 202, body: readOf({ state: "offered", report: undefined }) };
      }
      if (call.method === "GET" && call.path.startsWith(`${reads}/`)) {
        return { status: 200, body: served.reads.length > 1 ? served.reads.shift() : served.reads[0] };
      }
      if (call.method === "POST" && call.path === writes) {
        return { status: 202, body: writeOf({ state: "offered" }) };
      }
      if (call.method === "GET" && call.path.startsWith(`${writes}/`)) {
        const next = served.writes.length > 1 ? served.writes.shift() : served.writes[0];
        return { status: 200, body: next };
      }
      return undefined;
    },
    { projects: { status: 200, body: { projects: [{ id: "default", name: "default", state: "active", created_at: 1, repositories: [] }] } } },
  );
  return served;
}

const posts = (sent: Sent[], path: string) => sent.filter((call) => call.method === "POST" && call.path === path);

async function open(): Promise<HTMLElement> {
  openPage("/projects?dialog=template");
  const dialog = await screen.findByRole("dialog", { name: "Start a project from a template" });
  await waitFor(() => expect((within(dialog).getByLabelText("Worker") as HTMLSelectElement).value).toBe(worker));
  return dialog;
}

// remote is the Remote section, whose Owner is the one a repository is created under unless its own row says another.
const remote = (dialog: HTMLElement) => within(dialog).getByRole("group", { name: "Remote" });

const radio = (scope: HTMLElement, name: string | RegExp) => within(scope).getByRole("radio", { name });
const pick = (scope: HTMLElement, name: string | RegExp) => userEvent.click(radio(scope, name));

// reachOwners opens the dialog with the answer and the folder, and chooses Create on GitHub.
async function reachOwners(question: "One repository" | "Several repositories") {
  const dialog = await open();
  await pick(dialog, new RegExp(`^${question}`));
  await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
  await pick(dialog, "Create on GitHub");
  await within(remote(dialog)).findByLabelText("Owner");
  return dialog;
}

beforeEach(() => {
  try {
    localStorage.clear();
  } catch {
    // A browser that keeps nothing has nothing to clear.
  }
});

afterEach(() => {
  vi.useRealTimers();
  cleanup();
});

describe("the Remote section", () => {
  it("offers None, Add a remote URL, and Create on GitHub, with None chosen", async () => {
    serve();
    const dialog = await open();
    await pick(dialog, /^One repository/);
    const section = within(dialog).getByRole("group", { name: "Remote" });
    expect(within(section).getAllByRole("radio").map((choice) => [choice.parentElement?.textContent, (choice as HTMLInputElement).checked])).toEqual([
      ["None", true],
      ["Add a remote URL", false],
      ["Create on GitHub", false],
    ]);
  });

  it("is disabled while the first commit is unchecked, and says why", async () => {
    serve();
    const dialog = await open();
    await pick(dialog, /^One repository/);
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /Make the first commit/ }));
    const section = within(dialog).getByRole("group", { name: "Remote" }) as HTMLFieldSetElement;
    expect(section.disabled).toBe(true);
    expect(within(section).getByText(/A remote needs the first commit, which ropeshift pushes/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /Make the first commit/ }));
    expect((within(dialog).getByRole("group", { name: "Remote" }) as HTMLFieldSetElement).disabled).toBe(false);
  });

  it("is disabled for a worker on protocol 5, with the daemon's words, and sends no remote", async () => {
    const served = serve();
    const dialog = await open();
    await userEvent.selectOptions(within(dialog).getByLabelText("Worker"), older);
    await pick(dialog, /^Several repositories/);
    expect((within(dialog).getByRole("group", { name: "Remote" }) as HTMLFieldSetElement).disabled).toBe(true);
    expect(within(dialog).getByText("Worker solo speaks protocol 5; upgrade it to create a project's remote.")).toBeTruthy();
    expect(within(dialog).getByText(/solo speaks an older protocol, so it cannot write a new code repository/)).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "New" })).toBeNull();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.type(within(dialog).getByLabelText("Name"), "api");
    await userEvent.type(within(dialog).getByLabelText("Remote URL"), "git@example.com:acme/api.git");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(served.sent, `/v1/workers/${older}/template-writes`)).toHaveLength(1));
    expect(posts(served.sent, `/v1/workers/${older}/template-writes`)[0]?.body).toEqual({
      shape: "workspace",
      path: folder,
      first_commit: true,
      repositories: [{ name: "api", remote: "git@example.com:acme/api.git" }],
    });
    expect(posts(served.sent, `/v1/workers/${older}/forge-account-reads`)).toEqual([]);
  });

  it("sends a URL remote of one repository as it is typed, and refuses one git would read as an option", async () => {
    const served = serve();
    const dialog = await open();
    await pick(dialog, /^One repository/);
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await pick(dialog, "Add a remote URL");
    expect(posts(served.sent, reads)).toEqual([]);
    await userEvent.type(within(dialog).getByLabelText("Remote URL"), "--upload-pack=x");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    expect(await within(dialog).findByText("A remote URL has no spaces and does not start with a dash.")).toBeTruthy();
    expect(posts(served.sent, writes)).toEqual([]);
    await userEvent.clear(within(dialog).getByLabelText("Remote URL"));
    await userEvent.type(within(dialog).getByLabelText("Remote URL"), "git@example.com:acme/harbor.git");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(served.sent, writes)).toHaveLength(1));
    expect(posts(served.sent, writes)[0]?.body).toEqual({ shape: "monorepo", path: folder, first_commit: true, remote: { url: "git@example.com:acme/harbor.git" } });
  });

  it("asks one URL for each repository when a URL is added, and a repository with None gets none", async () => {
    const served = serve();
    const dialog = await open();
    await pick(dialog, /^Several repositories/);
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await pick(dialog, "Add a remote URL");
    await userEvent.type(within(dialog).getByLabelText("Remote URL of the planning repository"), "git@example.com:acme/harbor.git");
    const rows = within(dialog).getAllByRole("group", { name: /^Code repository \d$/ });
    await userEvent.type(within(rows[0] as HTMLElement).getByLabelText("Name"), "api");
    await userEvent.type(within(rows[0] as HTMLElement).getByLabelText("Remote URL"), "git@example.com:acme/api.git");
    await userEvent.type(within(rows[1] as HTMLElement).getByLabelText("Name"), "docs");
    expect(within(rows[0] as HTMLElement).queryByRole("option", { name: "Same as above" })).toBeNull();
    await userEvent.selectOptions(within(rows[1] as HTMLElement).getByLabelText("Remote"), "none");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(served.sent, writes)).toHaveLength(1));
    expect(posts(served.sent, writes)[0]?.body).toEqual({
      shape: "workspace",
      path: folder,
      first_commit: true,
      remote: { url: "git@example.com:acme/harbor.git" },
      repositories: [
        { name: "api", new: true, url: "git@example.com:acme/api.git" },
        { name: "docs", new: true },
      ],
    });
  });
});

describe("Create on GitHub", () => {
  it("names GitHub as the only forge, and checks gh on the worker before it offers an owner", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const served = serve([readOf({ state: "offered", report: undefined }), readOf()]);
    const dialog = await open();
    await pick(dialog, /^One repository/);
    await pick(dialog, "Create on GitHub");
    expect(within(dialog).getByText(/GitHub is the only forge ropeshift creates repositories on. To create one elsewhere, create it by hand and choose Add a remote URL./)).toBeTruthy();
    expect(await within(dialog).findByText("Checking gh on build-host-07")).toBeTruthy();
    expect(posts(served.sent, reads)[0]?.body).toEqual({ forge: "github" });
    expect((within(dialog).getByRole("button", { name: "Write files" }) as HTMLButtonElement).disabled).toBe(true);
    await vi.advanceTimersByTimeAsync(1000);
    const owner = (await within(remote(dialog)).findByLabelText("Owner")) as HTMLSelectElement;
    expect([...owner.options].map((option) => option.value)).toEqual(["ana", "acme", "zeta-labs"]);
    expect(owner.value).toBe("ana");
    expect((radio(dialog, "Private") as HTMLInputElement).checked).toBe(true);
    expect(within(dialog).getByText("Anyone can see a public repository")).toBeTruthy();
    expect((within(dialog).getByRole("button", { name: "Write files" }) as HTMLButtonElement).disabled).toBe(false);
    expect(within(dialog).getByText("Write files creates 1 private repository on GitHub.")).toBeTruthy();
  });

  it("sends the chosen owner and visibility for the folder's own repository", async () => {
    const served = serve();
    const dialog = await reachOwners("One repository");
    await userEvent.selectOptions(within(remote(dialog)).getByLabelText("Owner"), "acme");
    await pick(dialog, /^Public/);
    expect(within(dialog).getByText("Write files creates 1 public repository on GitHub.")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(served.sent, writes)).toHaveLength(1));
    expect(posts(served.sent, writes)[0]?.body).toEqual({
      shape: "monorepo",
      path: folder,
      first_commit: true,
      remote: { create: { forge: "github", owner: "acme", visibility: "public" } },
    });
  });

  it("resolves the one owner and each repository's override before it sends, so the request names every owner", async () => {
    const served = serve();
    const dialog = await reachOwners("Several repositories");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await userEvent.selectOptions(within(remote(dialog)).getByLabelText("Owner"), "acme");
    const rows = within(dialog).getAllByRole("group", { name: /^Code repository \d$/ });
    await userEvent.type(within(rows[0] as HTMLElement).getByLabelText("Name"), "api");
    await userEvent.type(within(rows[1] as HTMLElement).getByLabelText("Name"), "web");
    await userEvent.selectOptions(within(rows[1] as HTMLElement).getByLabelText("Owner"), "zeta-labs");
    await userEvent.type(within(rows[2] as HTMLElement).getByLabelText("Name"), "docs");
    await userEvent.selectOptions(within(rows[2] as HTMLElement).getByLabelText("Remote"), "none");
    expect(within(dialog).getByText("Write files creates 3 private repositories on GitHub.")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(served.sent, writes)).toHaveLength(1));
    const owner = (name: string) => ({ create: { forge: "github", owner: name, visibility: "private" } });
    expect(posts(served.sent, writes)[0]?.body).toEqual({
      shape: "workspace",
      path: folder,
      first_commit: true,
      remote: owner("acme"),
      repositories: [
        { name: "api", new: true, ...owner("acme") },
        { name: "web", new: true, ...owner("zeta-labs") },
        { name: "docs", new: true },
      ],
    });
  });

  it("remembers the owner a member chose on a worker, and offers it again there", async () => {
    serve();
    const first = await reachOwners("One repository");
    await userEvent.selectOptions(within(remote(first)).getByLabelText("Owner"), "zeta-labs");
    await userEvent.click(within(first).getByRole("button", { name: "Write files" }));
    await within(first).findByText(/Waiting for build-host-07/);
    cleanup();
    serve();
    const again = await reachOwners("One repository");
    expect((within(remote(again)).getByLabelText("Owner") as HTMLSelectElement).value).toBe("zeta-labs");
  });

  it("shows what to run when gh is signed out, with Copy and Check again, and keeps Write files disabled", async () => {
    const out: ForgeAccountReport = { request: "read-1", state: "gh_signed_out", detail: "Run `gh auth login --hostname github.com` on build-host-07", remedy: "gh auth login --hostname github.com" };
    const served = serve([readOf({ report: out })]);
    const dialog = await open();
    await pick(dialog, /^One repository/);
    await pick(dialog, "Create on GitHub");
    expect(await within(dialog).findByText("build-host-07: The GitHub CLI is not signed in.")).toBeTruthy();
    expect((within(dialog).getByRole("button", { name: "Write files" }) as HTMLButtonElement).disabled).toBe(true);
    const copy = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    await userEvent.click(within(dialog).getByRole("button", { name: "Copy gh auth login --hostname github.com" }));
    expect(copy).toHaveBeenCalledWith("gh auth login --hostname github.com");
    served.reads.splice(0, 1, readOf());
    await userEvent.click(within(dialog).getByRole("button", { name: "Check again" }));
    expect(await within(remote(dialog)).findByLabelText("Owner")).toBeTruthy();
    expect(posts(served.sent, reads)).toHaveLength(2);
  });

  it("says the worker did not answer when the read expires, and the daemon's refusal when it refuses the read", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve([readOf({ state: "expired", report: undefined })]);
    const dialog = await open();
    await pick(dialog, /^One repository/);
    await pick(dialog, "Create on GitHub");
    expect(await within(dialog).findByText("build-host-07: The worker did not answer.")).toBeTruthy();
    expect(within(dialog).getByText("The worker did not report within two minutes.")).toBeTruthy();
    cleanup();
    serve([readOf()], [writeOf()], (call) => (call.method === "POST" && call.path === reads ? refusal(403, "member_only", "creating a repository is a member's act") : undefined));
    const refused = await open();
    await pick(refused, /^One repository/);
    await pick(refused, "Create on GitHub");
    expect(await within(refused).findByText("creating a repository is a member's act")).toBeTruthy();
    expect(within(refused).getByRole("button", { name: "Check again" })).toBeTruthy();
  });

  it("shows gh's own words as plain text: no markup, no link, and no escape sequence", async () => {
    const hostile = "\u001b[31mdenied\u001b[0m <img src=x onerror=alert(1)> [click](https://evil.example/) https://evil.example/x";
    const out: ForgeAccountReport = { request: "read-1", state: "gh_scope_missing", detail: hostile, remedy: "gh auth refresh --hostname github.com --scopes repo" };
    serve([readOf({ report: out })]);
    const dialog = await open();
    await pick(dialog, /^One repository/);
    await pick(dialog, "Create on GitHub");
    const text = await within(dialog).findByText(/denied <img src=x onerror=alert\(1\)>/);
    expect(text.textContent).toBe("denied <img src=x onerror=alert(1)> [click](https://evil.example/) https://evil.example/x");
    expect(text.querySelector("*")).toBeNull();
    expect(dialog.querySelector("img")).toBeNull();
    expect([...dialog.querySelectorAll("a")].map((link) => link.getAttribute("href"))).not.toContain("https://evil.example/");
  });
});

describe("what was written", () => {
  const failed: TemplateWriteReport = {
    request: "request-1",
    outcome: "written",
    project: "harbor",
    files: ["AGENTS.md", "Makefile"],
    commit: "3f9c2ab",
    clone: true,
    commands: ["ropeshift project create harbor"],
    repositories: [
      {
        name: "harbor",
        path: ".",
        commit: "3f9c2ab",
        remote: "git@github.com:acme/harbor.git",
        steps: [
          { step: "create", outcome: "done" },
          { step: "origin", outcome: "done" },
          { step: "push", outcome: "failed", detail: "\u001b[31mremote: Permission denied\u001b[0m\u0007\n<script>alert(1)</script> [a](https://evil.example/) https://evil.example/y" },
        ],
      },
      {
        name: "api",
        path: "repos/api",
        commit_error: "Author identity unknown\u001b]0;title\u0007",
        steps: [
          { step: "create", outcome: "skipped" },
          { step: "origin", outcome: "skipped" },
          { step: "push", outcome: "skipped" },
        ],
      },
    ],
    remaining: ["git -C /srv/code/harbor push --set-upstream origin main", "gh auth setup-git"],
  };

  async function reachWritten(report: TemplateWriteReport, registrable = true) {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve([readOf()], [writeOf(), writeOf({ state: "written", registrable, report })]);
    const dialog = await open();
    await pick(dialog, /^Several repositories/);
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.type(within(dialog).getByLabelText("Name"), "api");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await within(dialog).findByText("Waiting for build-host-07");
    await vi.advanceTimersByTimeAsync(1000);
    await within(dialog).findByText(/build-host-07 wrote/);
    return dialog;
  }

  it("lists each repository with its commit, remote, and the outcome of each step, then the commands that finish what did not run", async () => {
    const dialog = await reachWritten(failed);
    const repositories = within(dialog).getByRole("list", { name: "Repositories" });
    const harbor = within(repositories).getByRole("listitem", { name: "harbor" });
    expect(within(harbor).getByText("3f9c2ab").tagName).toBe("CODE");
    expect(within(harbor).getByText("git@github.com:acme/harbor.git").tagName).toBe("CODE");
    expect(harbor.textContent).toContain("create done");
    expect(harbor.textContent).toContain("origin done");
    expect(harbor.textContent).toContain("push failed");
    const api = within(repositories).getByRole("listitem", { name: "api" });
    expect(api.textContent).toContain("The first commit failed, and the files stay:");
    expect(api.textContent).toContain("push skipped");
    const remaining = within(dialog).getByRole("list", { name: "Remaining commands" });
    expect(within(remaining).getAllByRole("listitem").map((item) => item.querySelector("code")?.textContent)).toEqual(failed.remaining);
    const copy = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    await userEvent.click(within(remaining).getByRole("button", { name: "Copy gh auth setup-git" }));
    expect(copy).toHaveBeenCalledWith("gh auth setup-git");
    expect(within(dialog).getByText("Approving the project's first plan registers the code repositories through the manifest.")).toBeTruthy();
  });

  it("shows a step's detail as plain text: no markup, no link, and no control character", async () => {
    const dialog = await reachWritten(failed);
    const detail = within(dialog).getByText(/remote: Permission denied/);
    expect(detail.tagName).toBe("PRE");
    expect(detail.textContent).toBe("remote: Permission denied\n<script>alert(1)</script> [a](https://evil.example/) https://evil.example/y");
    expect(detail.querySelector("*")).toBeNull();
    expect(dialog.querySelector("script, img")).toBeNull();
    expect([...dialog.querySelectorAll("a")].some((link) => (link.getAttribute("href") ?? "").includes("evil.example"))).toBe(false);
    // eslint-disable-next-line no-control-regex
    expect(/[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/.test(dialog.textContent ?? "")).toBe(false);
    expect(within(dialog).getByText("Author identity unknown").tagName).toBe("PRE");
  });
});

describe("Start with new files", () => {
  it("is offered by Create project on the Projects view, and opens the dialog in its place", async () => {
    serve();
    openPage("/projects?dialog=create");
    const create = await screen.findByRole("dialog", { name: "Create project" });
    await userEvent.click(within(create).getByRole("button", { name: "Start with new files" }));
    expect(await screen.findByRole("dialog", { name: "Start a project from a template" })).toBeTruthy();
    expect(screen.queryByRole("dialog", { name: "Create project" })).toBeNull();
  });

  it("is offered in the project switcher beside Create project, and opens the dialog", async () => {
    serve();
    openPage("/projects");
    await userEvent.click(await screen.findByRole("button", { name: /^Project: / }));
    const menu = await screen.findByRole("menu", { name: "Projects" });
    const names = within(menu).getAllByRole("menuitem").map((item) => item.textContent?.trim());
    expect(names.indexOf("Start with new files")).toBe(names.indexOf("Create project") + 1);
    const entry = within(menu).getByRole("menuitem", { name: "Start with new files" });
    await userEvent.click(entry);
    expect(await screen.findByRole("dialog", { name: "Start a project from a template" })).toBeTruthy();
  });
});
