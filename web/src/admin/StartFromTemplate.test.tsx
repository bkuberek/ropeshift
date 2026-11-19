import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { TemplateWriteReport, TemplateWriteView } from "../api/admin";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, refusal, serveViews } from "../testing";
import { folderProblem, projectNameOf } from "./StartFromTemplate";

// Start a project from a template writes a shape's starting files on a worker the member picks, as `ropeshift new` writes them,
// waits for the worker's report, and hands what was written to registration as a separate act (FR-TPL-5, FR-UI-7, FR-UI-8).

const connected = "7c41e9a05d2b4f68b3a1e6d29c084f5a";
const quiet = "worker-a";
const older = "old-worker";

const workers = {
  workers: [
    { id: older, name: "elder", epoch: 1, protocol: 4 },
    { id: quiet, name: "solo", epoch: 1, protocol: 5 },
    { id: connected, name: "build-host-07", epoch: 1, protocol: 7 },
  ],
};

const folder = "/srv/code/harbor";
const writesTo = (worker: string) => `/v1/workers/${worker}/template-writes`;

function view(over: Partial<TemplateWriteView> = {}): TemplateWriteView {
  return {
    id: "request-1",
    worker: connected,
    requested_by: "member-1",
    requested_at: Date.now(),
    expires_at: Date.now() + 120_000,
    shape: "monorepo",
    path: folder,
    first_commit: true,
    state: "offered",
    registrable: false,
    ...over,
  };
}

const written = (over: Partial<TemplateWriteReport> = {}): TemplateWriteReport => ({
  request: "request-1",
  outcome: "written",
  project: "harbor",
  files: ["AGENTS.md", "CLAUDE.md", "plan/goal.md"],
  commit: "3f9c2ab",
  clone: true,
  commands: ["ropeshift project create harbor", `ropeshift repository add ${folder} --project harbor`],
  ...over,
});

type Served = { posted: TemplateWriteView; reads: TemplateWriteView[]; projects: { id: string; name: string; state: string; created_at: number; repositories: string[] }[] };

// serveStart answers the workers, the request the dialog sends, and the reads that follow it, in order, the last one repeating.
function serveStart(served: Served, answer?: (sent: Sent) => { status: number; body?: unknown } | undefined) {
  const remaining = [...served.reads];
  const projects = { status: 200, body: { projects: served.projects } };
  const sent = serveViews(
    (call) => {
      const own = answer?.(call);
      if (own) {
        return own;
      }
      if (call.method === "GET" && call.path === "/v1/workers") {
        return { status: 200, body: workers };
      }
      if (call.method === "POST" && call.path === writesTo(connected)) {
        return { status: 202, body: served.posted };
      }
      if (call.method === "GET" && call.path.startsWith(`${writesTo(connected)}/`)) {
        const next = remaining.length > 1 ? remaining.shift() : remaining[0];
        return next ? { status: 200, body: next } : undefined;
      }
      if (call.method === "POST" && call.path === "/v1/projects") {
        const made = { id: "harbor-id", name: (call.body as { name: string }).name, state: "active", created_at: 9, repositories: [] };
        served.projects.push(made);
        return { status: 201, body: made };
      }
      if (call.path === "/v1/repositories") {
        return call.method === "GET" ? { status: 200, body: { repositories: [] } } : { status: 200, body: golden("repository-dry-run") };
      }
      return undefined;
    },
    { projects },
  );
  return sent;
}

const one = [{ id: "default", name: "default", state: "active", created_at: 1, repositories: [] as string[] }];

const posts = (sent: Sent[], path: string) => sent.filter((call) => call.method === "POST" && call.path === path);

async function openDialog(): Promise<HTMLElement> {
  openPage("/projects?dialog=template");
  const dialog = await screen.findByRole("dialog", { name: "Start a project from a template" });
  await waitFor(() => expect((within(dialog).getByLabelText("Worker") as HTMLSelectElement).value).toBe(connected));
  return dialog;
}

// answer picks an answer to the first question, which stands unanswered until then.
const answer = (dialog: HTMLElement, label: "One repository" | "Several repositories") => userEvent.click(within(dialog).getByRole("radio", { name: new RegExp(`^${label}`) }));

// openAnswered opens the dialog with the project in one repository.
async function openAnswered(): Promise<HTMLElement> {
  const dialog = await openDialog();
  await answer(dialog, "One repository");
  return dialog;
}

afterEach(() => {
  vi.useRealTimers();
  cleanup();
});

describe("the names and folders the dialog checks before it sends", () => {
  it("makes a project name of a folder as `ropeshift new` does", () => {
    expect(projectNameOf("/srv/code/My Project")).toBe("my-project");
    expect(projectNameOf("/srv/code/harbor/")).toBe("harbor");
    expect(projectNameOf("/srv/code/___")).toBe("");
    expect(projectNameOf(`/srv/${"a".repeat(80)}`)).toHaveLength(63);
  });

  it("refuses a folder that is empty, relative, or holds a .. element", () => {
    expect(folderProblem("")).toMatch(/absolute/);
    expect(folderProblem("code/harbor")).toMatch(/relative/);
    expect(folderProblem("/srv/../harbor")).toMatch(/\.\./);
    for (const unclean of ["/srv/harbor/", "/srv//harbor", "//srv/harbor", "/srv/./harbor"]) {
      expect(folderProblem(unclean), unclean).toMatch(/no trailing slash/);
    }
    expect(folderProblem("/srv/code/harbor")).toBe("");
  });
});

describe("Start a project from a template", () => {
  it("lists the connected workers first and disables one on an older protocol, with what to do", async () => {
    serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openDialog();
    const options = [...(within(dialog).getByLabelText("Worker") as HTMLSelectElement).options].filter((option) => option.value);
    expect(options.map((option) => option.value)).toEqual([connected, quiet, older]);
    const elder = options.find((option) => option.value === older) as HTMLOptionElement;
    expect(elder.disabled).toBe(true);
    expect(elder.textContent).toContain("Upgrade this worker to write files from the web UI");
    expect(options.find((option) => option.value === connected)?.disabled).toBe(false);
  });

  it("asks first whether the project lives in one repository or in several, before any shape name, and links the growth path", async () => {
    serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openDialog();
    const question = within(dialog).getByRole("group", { name: "Will this project live in one repository, or in several?" });
    expect(within(question).getAllByRole("radio").map((radio) => (radio as HTMLInputElement).checked)).toEqual([false, false]);
    expect(within(question).getAllByRole("radio").map((radio) => radio.parentElement?.textContent)).toEqual([
      "One repositoryWrites the plan and the code in one repository.",
      "Several repositoriesWrites a planning repository, with the code repositories you name cloned into its ignored repos/ folder.",
    ]);
    expect(within(dialog).queryByRole("group", { name: "Shape" })).toBeNull();
    expect(within(dialog).queryByText("Monorepo")).toBeNull();
    const growth = within(dialog).getByRole("link", { name: /How a project grows/ });
    expect(growth.getAttribute("href")).toMatch(/docs\/project-structure\.md#growing-a-project$/);
    expect(growth.getAttribute("rel")).toContain("noreferrer");
    // The question comes before the worker and the folder.
    const order = [...dialog.querySelectorAll("legend, label.label")].map((node) => node.textContent);
    expect(order.indexOf("Will this project live in one repository, or in several?")).toBeLessThan(order.indexOf("Worker"));
    expect((within(dialog).getByRole("checkbox", { name: /Make the first commit/ }) as HTMLInputElement).checked).toBe(true);
    await userEvent.type(within(dialog).getByLabelText("Folder"), "/srv/code/My Project");
    expect(within(dialog).getByText("chore: start my-project")).toBeTruthy();
  });

  it("sends nothing while the first question is unanswered, and says so at the question", async () => {
    const sent = serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openDialog();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    expect(await within(dialog).findByText("Choose whether the project lives in one repository or in several.")).toBeTruthy();
    expect(posts(sent, writesTo(connected))).toEqual([]);
  });

  it("lists a worker on protocol 5 as able to write files but not a remote", async () => {
    serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openDialog();
    const solo = [...(within(dialog).getByLabelText("Worker") as HTMLSelectElement).options].find((option) => option.value === quiet) as HTMLOptionElement;
    expect(solo.disabled).toBe(false);
    expect(solo.textContent).toContain("Upgrade it to create a project's remote");
  });

  it("sends a workspace with a new code repository and an existing one, and the first commit", async () => {
    const sent = serveStart({ posted: view({ shape: "workspace" }), reads: [view({ shape: "workspace" })], projects: one });
    const dialog = await openDialog();
    await answer(dialog, "Several repositories");
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    expect(within(dialog).getAllByRole("group", { name: /^Code repository \d$/ })).toHaveLength(1);
    expect((within(dialog).getByRole("button", { name: "Remove code repository 1" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    const rows = within(dialog).getAllByRole("group", { name: /^Code repository \d$/ });
    await userEvent.type(within(rows[0] as HTMLElement).getByLabelText("Name"), "api");
    await userEvent.type(within(rows[1] as HTMLElement).getByLabelText("Name"), "web");
    await userEvent.click(within(rows[1] as HTMLElement).getByRole("button", { name: "Existing" }));
    await userEvent.type(within(rows[1] as HTMLElement).getByLabelText("Remote URL"), "https://example.com/acme/web.git");
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove code repository 3" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(sent, writesTo(connected))).toHaveLength(1));
    expect(posts(sent, writesTo(connected))[0]?.body).toEqual({
      shape: "workspace",
      path: folder,
      first_commit: true,
      repositories: [{ name: "api", new: true }, { name: "web", remote: "https://example.com/acme/web.git" }],
    });
    expect(posts(sent, writesTo(connected))[0]?.csrf).not.toBeNull();
  });

  it("sends a monorepo with no repositories and no first commit when it is unchecked", async () => {
    const sent = serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openAnswered();
    expect(within(dialog).queryByRole("button", { name: "Add" })).toBeNull();
    expect(within(dialog).queryByLabelText("Project")).toBeNull();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /Make the first commit/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(sent, writesTo(connected))).toHaveLength(1));
    expect(posts(sent, writesTo(connected))[0]?.body).toEqual({ shape: "monorepo", path: folder, first_commit: false });
  });

  it("sends a repository with the project its commands name", async () => {
    const sent = serveStart({ posted: view({ shape: "repository" }), reads: [view({ shape: "repository" })], projects: one });
    const dialog = await openDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Add a code repository to a project planned elsewhere" }));
    expect(within(dialog).queryByRole("button", { name: "Add" })).toBeNull();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.type(within(dialog).getByLabelText("Project"), "tidewater");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(sent, writesTo(connected))).toHaveLength(1));
    expect(posts(sent, writesTo(connected))[0]?.body).toEqual({ shape: "repository", path: folder, first_commit: true, project: "tidewater" });
    expect(within(dialog).queryByRole("button", { name: "Start a project instead" })).toBeNull();
  });

  it("offers a project name for a monorepo and a workspace, defaulting to the folder's name, and not for a repository", async () => {
    serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openDialog();
    expect(within(dialog).queryByLabelText("Project name")).toBeNull();
    await answer(dialog, "One repository");
    const field = within(dialog).getByLabelText("Project name") as HTMLInputElement;
    expect(field.disabled).toBe(false);
    expect(field.required).toBe(false);
    await userEvent.type(within(dialog).getByLabelText("Folder"), "/srv/code/Tide Water");
    expect(field.placeholder).toBe("tide-water");
    expect(within(dialog).getByText(/chore: start tide-water/)).toBeTruthy();
    await answer(dialog, "Several repositories");
    expect(within(dialog).getByLabelText("Project name")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Add a code repository to a project planned elsewhere" }));
    expect(within(dialog).queryByLabelText("Project name")).toBeNull();
    expect(within(dialog).getByLabelText("Project")).toBeTruthy();
  });

  it("sends the name a monorepo and a workspace are given, and none when the field is empty", async () => {
    const sent = serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openAnswered();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.type(within(dialog).getByLabelText("Project name"), "tidewater");
    expect(within(dialog).getByText(/chore: start tidewater/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(sent, writesTo(connected))).toHaveLength(1));
    expect(posts(sent, writesTo(connected))[0]?.body).toEqual({ shape: "monorepo", path: folder, first_commit: true, name: "tidewater" });
  });

  it("sends the name of a workspace beside its code repositories", async () => {
    const sent = serveStart({ posted: view({ shape: "workspace" }), reads: [view({ shape: "workspace" })], projects: one });
    const dialog = await openDialog();
    await answer(dialog, "Several repositories");
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.type(within(dialog).getByLabelText("Project name"), "tidewater");
    await userEvent.type(within(within(dialog).getByRole("group", { name: "Code repository 1" })).getByLabelText("Name"), "api");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await waitFor(() => expect(posts(sent, writesTo(connected))).toHaveLength(1));
    expect(posts(sent, writesTo(connected))[0]?.body).toMatchObject({ shape: "workspace", name: "tidewater", repositories: [{ name: "api", new: true }] });
  });

  it("sends nothing for a name that is no project name, and says why at the field", async () => {
    const sent = serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openAnswered();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.type(within(dialog).getByLabelText("Project name"), "Tide Water");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    const field = within(dialog).getByLabelText("Project name") as HTMLInputElement;
    expect(await within(dialog).findByText(/lowercase letters and digits in words joined by single hyphens/)).toBeTruthy();
    expect(field.getAttribute("aria-invalid")).toBe("true");
    expect(posts(sent, writesTo(connected))).toEqual([]);
  });

  it("disables the name for a worker on an older protocol, with what to do, and sends none", async () => {
    const six = { id: "worker-six", name: "six", epoch: 1, protocol: 6 };
    const sent = serveStart({ posted: view(), reads: [view()], projects: one }, (call) =>
      call.method === "GET" && call.path === "/v1/workers" ? { status: 200, body: { workers: [six] } } : undefined,
    );
    openPage("/projects?dialog=template");
    const dialog = await screen.findByRole("dialog", { name: "Start a project from a template" });
    await waitFor(() => expect((within(dialog).getByLabelText("Worker") as HTMLSelectElement).value).toBe("worker-six"));
    await answer(dialog, "One repository");
    const field = within(dialog).getByLabelText("Project name") as HTMLInputElement;
    expect(field.disabled).toBe(true);
    expect(within(dialog).getByText("Worker six speaks protocol 6; upgrade it to name a project.")).toBeTruthy();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    expect(sent.filter((call) => call.method === "POST")).toEqual([]);
  });

  it("goes back from a code repository of a project planned elsewhere to the first question", async () => {
    serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Add a code repository to a project planned elsewhere" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Start a project instead" }));
    expect(within(dialog).queryByLabelText("Project")).toBeNull();
    expect(within(dialog).getByRole("button", { name: "Add a code repository to a project planned elsewhere" })).toBeTruthy();
  });

  it("sends nothing for a relative folder or one with a .. element, and says why at the field", async () => {
    const sent = serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openAnswered();
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    expect(await within(dialog).findByText(/Enter an absolute folder/)).toBeTruthy();
    await userEvent.type(within(dialog).getByLabelText("Folder"), "code/harbor");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    expect(await within(dialog).findByText(/relative path/)).toBeTruthy();
    await userEvent.clear(within(dialog).getByLabelText("Folder"));
    await userEvent.type(within(dialog).getByLabelText("Folder"), "/srv/../harbor");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    expect(await within(dialog).findByText(/\.\. element/)).toBeTruthy();
    expect(posts(sent, writesTo(connected))).toEqual([]);
  });

  it("asks for a name of each code repository and a remote URL of an existing one, and sends nothing until it has them", async () => {
    const sent = serveStart({ posted: view(), reads: [view()], projects: one });
    const dialog = await openDialog();
    await answer(dialog, "Several repositories");
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    expect(await within(dialog).findByText(/Give each code repository a name/)).toBeTruthy();
    await userEvent.type(within(dialog).getByLabelText("Name"), "api");
    await userEvent.click(within(dialog).getByRole("button", { name: "Existing" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    expect(await within(dialog).findByText("Repository api: Enter the remote URL.")).toBeTruthy();
    expect(posts(sent, writesTo(connected))).toEqual([]);
  });

  it("waits for the worker, reading the request once a second, and lists what it wrote with the commands", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const sent = serveStart({ posted: view(), reads: [view(), view({ state: "written", report: written() })], projects: one });
    const dialog = await openAnswered();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    expect(await within(dialog).findByText("Waiting for build-host-07")).toBeTruthy();
    const reads = () => sent.filter((call) => call.method === "GET" && call.path === `${writesTo(connected)}/request-1`);
    expect(reads()).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1000);
    await waitFor(() => expect(reads().length).toBeGreaterThanOrEqual(1));
    await vi.advanceTimersByTimeAsync(1000);
    expect(await within(dialog).findByText(/build-host-07 wrote 3 files in \/srv\/code\/harbor/)).toBeTruthy();
    const files = within(dialog).getByRole("list", { name: "Files written" });
    expect(within(files).getAllByRole("listitem").map((item) => item.textContent)).toEqual(["AGENTS.md", "CLAUDE.md", "plan/goal.md"]);
    expect(within(dialog).getByText("3f9c2ab")).toBeTruthy();
    const commands = within(dialog).getByRole("list", { name: "Commands" });
    expect(within(commands).getAllByRole("listitem").map((item) => item.querySelector("code")?.textContent)).toEqual(written().commands);
    const copy = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    await userEvent.click(within(commands).getByRole("button", { name: "Copy ropeshift project create harbor" }));
    expect(copy).toHaveBeenCalledWith("ropeshift project create harbor");
    expect(reads().length).toBe(2);
  });

  it("says the worker did not take the request when the daemon reads it as expired", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serveStart({ posted: view(), reads: [view({ state: "expired" })], projects: one });
    const dialog = await openAnswered();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await within(dialog).findByText("Waiting for build-host-07");
    await vi.advanceTimersByTimeAsync(1000);
    expect(await within(dialog).findByText("build-host-07 did not take the request within two minutes. It is no longer offered.")).toBeTruthy();
    expect(within(dialog).queryByText(/Waiting for/)).toBeNull();
    await userEvent.click(within(dialog).getByRole("button", { name: "Back to the form" }));
    expect((within(dialog).getByLabelText("Folder") as HTMLInputElement).value).toBe(folder);
  });

  it("stops reading a request that is still offered once its expiry has passed, and says so", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const offered = view({ expires_at: Date.now() + 1500 });
    const sent = serveStart({ posted: offered, reads: [offered], projects: one });
    const dialog = await openAnswered();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await within(dialog).findByText("Waiting for build-host-07");
    await vi.advanceTimersByTimeAsync(9000);
    expect(await within(dialog).findByText(/did not take the request within two minutes/)).toBeTruthy();
    const reads = () => sent.filter((call) => call.method === "GET" && call.path === `${writesTo(connected)}/request-1`).length;
    const before = reads();
    expect(before).toBeGreaterThan(0);
    await vi.advanceTimersByTimeAsync(5000);
    expect(reads()).toBe(before);
  });

  it("brings a refusal back to the form, with the reason and detail at the Folder field and everything typed kept", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const refused = view({ state: "refused", report: { request: "request-1", outcome: "refused", reason: "path_not_empty", detail: "the folder holds files" } });
    serveStart({ posted: view({ shape: "workspace" }), reads: [refused], projects: one });
    const dialog = await openDialog();
    await answer(dialog, "Several repositories");
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.type(within(dialog).getByLabelText("Name"), "api");
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await within(dialog).findByText("Waiting for build-host-07");
    await vi.advanceTimersByTimeAsync(1000);
    const field = (await within(dialog).findByText(/the folder holds files \(path_not_empty\)/)).closest(".field") as HTMLElement;
    expect(within(field).getByLabelText("Folder")).toBeTruthy();
    expect((within(dialog).getByLabelText("Folder") as HTMLInputElement).value).toBe(folder);
    expect((within(dialog).getByLabelText("Name") as HTMLInputElement).value).toBe("api");
    expect(within(dialog).getByRole("button", { name: "Write files" })).toBeTruthy();
  });

  it("shows a first commit that failed with git's words, and the files that stay", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const failed = view({ state: "written", report: written({ commit: undefined, commit_error: "Author identity unknown", clone: false, commands: ["git -C /srv/code/harbor init"] }) });
    serveStart({ posted: view(), reads: [failed], projects: one });
    const dialog = await openAnswered();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await within(dialog).findByText("Waiting for build-host-07");
    await vi.advanceTimersByTimeAsync(1000);
    expect(await within(dialog).findByText(/The first commit failed, and the files stay: Author identity unknown/)).toBeTruthy();
    expect(within(dialog).getByText(/The folder is not a clone yet/)).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: /Register/ })).toBeNull();
  });
});

describe("registering what was written, which is a separate act", () => {
  const registrable = (over: Partial<TemplateWriteView> = {}) => view({ state: "written", registrable: true, report: written(), ...over });

  async function reachWritten(served: Served, answer?: (sent: Sent) => { status: number; body?: unknown } | undefined) {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const sent = serveStart(served, answer);
    const dialog = await openAnswered();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    await within(dialog).findByText("Waiting for build-host-07");
    await vi.advanceTimersByTimeAsync(1000);
    await within(dialog).findByText(/build-host-07 wrote/);
    return { sent, dialog };
  }

  it("creates the project when none has the name, then opens the Register dialog on the folder at its dry run", async () => {
    const served: Served = { posted: view(), reads: [registrable()], projects: [...one] };
    const { sent, dialog } = await reachWritten(served);
    expect(within(dialog).queryByText(/make -C/)).toBeNull();
    await userEvent.click(within(dialog).getByRole("button", { name: "Create project harbor and register" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/harbor/repositories"));
    expect(posts(sent, "/v1/projects")[0]?.body).toEqual({ name: "harbor" });
    const params = new URLSearchParams(window.location.search);
    expect(params.get("dialog")).toBe("register");
    expect(params.get("folder")).toBe(folder);
    const register = await screen.findByRole("dialog", { name: "Register repositories in harbor" });
    await within(register).findByText(/Nothing is recorded until you register/);
    const dry = posts(sent, "/v1/repositories").map((call) => call.body);
    expect(dry).toEqual([{ paths: [folder], depth: 0, dry_run: true }]);
    expect(within(register).getByText("Dry run")).toBeTruthy();
    expect(within(register).getByRole("button", { name: /^Register \d/ })).toBeTruthy();
    expect(posts(sent, "/v1/repositories").some((call) => (call.body as { dry_run: boolean }).dry_run === false)).toBe(false);
  });

  it("offers Register on a project that exists, creating nothing", async () => {
    const harbor = { id: "harbor-id", name: "harbor", state: "active", created_at: 2, repositories: [] as string[] };
    const served: Served = { posted: view(), reads: [registrable()], projects: [...one, harbor] };
    const { sent, dialog } = await reachWritten(served);
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/harbor/repositories"));
    expect(posts(sent, "/v1/projects")).toEqual([]);
    await screen.findByRole("dialog", { name: "Register repositories in harbor" });
  });

  it("names the project a repository's commands name, not the folder's", async () => {
    const tidewater = { id: "tidewater-id", name: "tidewater", state: "active", created_at: 2, repositories: [] as string[] };
    const served: Served = { posted: view({ shape: "repository", project: "tidewater" }), reads: [registrable({ shape: "repository", project: "tidewater" })], projects: [...one, tidewater] };
    const { dialog } = await reachWritten(served);
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/tidewater/repositories"));
  });

  it("tells a workspace's person to clone its code repositories first", async () => {
    const repositories = [{ name: "api", remote: "git@example.com:acme/api.git" }, { name: "web", new: true }];
    const served: Served = { posted: view({ shape: "workspace", repositories }), reads: [registrable({ shape: "workspace", repositories })], projects: [...one] };
    const { dialog } = await reachWritten(served);
    expect(within(dialog).getByText(`make -C ${folder} setup`).tagName).toBe("CODE");
    expect(within(dialog).getByText(/Clone the code repositories with/).textContent).toContain("on build-host-07, then register the folder of clones");
    expect(within(dialog).getByText("Approving the project's first plan registers the code repositories through the manifest.")).toBeTruthy();
  });

  it("says why it cannot register a clone on another machine, and shows the commands to run there", async () => {
    const served: Served = { posted: view(), reads: [view({ state: "written", registrable: false, report: written() })], projects: [...one] };
    const { dialog } = await reachWritten(served);
    expect(within(dialog).queryByRole("button", { name: /Register|Create project/ })).toBeNull();
    expect(within(dialog).getByText(/registers only folders on the worker this daemon runs, and build-host-07 is another machine's/)).toBeTruthy();
    expect(within(dialog).getByText("Run these on that machine:")).toBeTruthy();
    expect(within(within(dialog).getByRole("list", { name: "Commands" })).getAllByRole("listitem")).toHaveLength(2);
  });

  it("keeps the dialog open and says why when creating the project is refused", async () => {
    const served: Served = { posted: view(), reads: [registrable()], projects: [...one] };
    const { dialog } = await reachWritten(served, (call) => (call.method === "POST" && call.path === "/v1/projects" ? refusal(409, "conflict", "a project of that name exists") : undefined));
    await userEvent.click(within(dialog).getByRole("button", { name: "Create project harbor and register" }));
    expect(await within(dialog).findByText("a project of that name exists")).toBeTruthy();
    expect(window.location.pathname).toBe("/projects");
  });
});

describe("where the dialog starts", () => {
  it("opens from Start a project from a template on Projects, beside Create project and Import", async () => {
    serveStart({ posted: view(), reads: [view()], projects: one });
    openPage("/projects");
    const start = await screen.findByRole("button", { name: "Start a project from a template" });
    const names = [...(start.parentElement?.querySelectorAll("button, a") ?? [])].map((control) => control.textContent?.trim());
    expect(names.indexOf("Start a project from a template")).toBeLessThan(names.indexOf("Create project"));
    expect(names).toContain("Import an existing project");
    await userEvent.click(start);
    expect(await screen.findByRole("dialog", { name: "Start a project from a template" })).toBeTruthy();
  });

  it("opens from the Templates view's header", async () => {
    serveStart({ posted: view(), reads: [view()], projects: one });
    openPage("/templates");
    await userEvent.click(await screen.findByRole("button", { name: "Start a project from a template" }));
    expect(await screen.findByRole("dialog", { name: "Start a project from a template" })).toBeTruthy();
  });

  it("reaches the dialog from the command palette's Actions", async () => {
    serveStart({ posted: view(), reads: [view()], projects: one });
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), "start a project");
    await userEvent.click(within(palette).getByRole("option", { name: /^Start a project from a template/ }));
    expect(await screen.findByRole("dialog", { name: "Start a project from a template" })).toBeTruthy();
    expect(window.location.pathname).toBe("/projects");
  });

  it("says no worker has joined when the installation holds none", async () => {
    serveViews((call) => (call.method === "GET" && call.path === "/v1/workers" ? { status: 200, body: { workers: [] } } : undefined));
    openPage("/projects?dialog=template");
    const dialog = await screen.findByRole("dialog", { name: "Start a project from a template" });
    expect(await within(dialog).findByText("No worker has joined.")).toBeTruthy();
    expect((within(dialog).getByRole("button", { name: "Write files" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows what the daemon refused at the foot of the form, and keeps the form", async () => {
    serveStart({ posted: view(), reads: [view()], projects: one }, (call) =>
      call.method === "POST" && call.path === writesTo(connected) ? refusal(400, "invalid", "worker build-host-07 is not connected") : undefined,
    );
    const dialog = await openAnswered();
    await userEvent.type(within(dialog).getByLabelText("Folder"), folder);
    await userEvent.click(within(dialog).getByRole("button", { name: "Write files" }));
    expect(await within(dialog).findByText("worker build-host-07 is not connected")).toBeTruthy();
    expect((within(dialog).getByLabelText("Folder") as HTMLInputElement).value).toBe(folder);
  });
});
