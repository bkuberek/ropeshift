import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { PolicyList, PolicyVersion } from "../api/admin";
import { go } from "../app/route";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, groupHealth, here, refusal, serveViews } from "../testing";
import { DryRun } from "./Repositories";

type Answer = { status: number; body?: unknown };

const workers = { workers: [{ id: "worker-1", name: "atlas", epoch: 1, protocol: 3, manifest: { capacity: { max_lanes: 6 } } }] };

// serveAdmin answers what the projects, repositories, settings, agents, members, and audit pages read,
// each from what a Go test wrote from a real ledger, and lets a test answer anything else first.
function serveAdmin(answer?: (sent: Sent) => Answer | undefined, projects?: Answer) {
  return serveViews(
    (call) => {
      const own = answer?.(call);
      if (own) {
        return own;
      }
      const path = call.path;
      if (call.method !== "GET") {
        return undefined;
      }
      const reads: [string, () => unknown][] = [
        ["/v1/repositories", () => golden("repositories")],
        ["/v1/fleet", () => golden("fleet")],
        ["/v1/policies?kind=recovery", () => golden("policies-recovery")],
        ["/v1/policies?kind=estimate", () => golden("policies-estimate")],
        ["/v1/roster/history", () => golden("roster-history")],
        ["/v1/roster", () => golden("roster")],
        ["/v1/members", () => golden("members")],
        ["/v1/audit", () => golden("audit")],
        ["/v1/workers/worker-1/caps", () => golden("worker-caps")],
        ["/v1/workers", () => workers],
        ["/v1/categories", () => ({ categories: ["security", "data", "compliance"] })],
      ];
      const found = reads.find(([prefix]) => path === prefix || path.startsWith(`${prefix}?`));
      return found ? { status: 200, body: found[1]() } : undefined;
    },
    projects ? { projects } : {},
  );
}

// sentTo finds the calls to one route: by its path as one project's routes read under /v1, or by the
// whole address for a route of the installation's that names a project.
const sentTo = (sent: Sent[], method: string, path: string) => sent.filter((call) => call.method === method && (call.path.split("?")[0] === path || call.url === path));

describe("Projects", () => {
  const listed = {
    status: 200,
    body: {
      projects: [
        { id: "default", name: "default", display_name: "Default", state: "active", created_at: 1, repositories: ["a"], waiting: 3 },
        { id: "tern-id", name: "tern", display_name: "Tern", state: "archived", created_at: 2, repositories: [] },
      ],
    },
  };

  it("lists every project with its state, and offers only Restore and Remove on an archived one", async () => {
    serveAdmin(undefined, listed);
    openPage("/projects");
    const table = await screen.findByRole("region", { name: "Projects" });
    const tern = within(table).getByText("Tern").closest("tr") as HTMLElement;
    expect(within(tern).getByText("Archived")).toBeTruthy();
    expect(within(tern).getByRole("button", { name: "Restore" })).toBeTruthy();
    expect(within(tern).queryByRole("button", { name: "Archive…" })).toBeNull();
    const active = within(table).getByText("Default").closest("tr") as HTMLElement;
    expect(within(active).getByRole("button", { name: "Back up" })).toBeTruthy();
  });

  it("opens the dialog an address names, for the project it names", async () => {
    serveAdmin(undefined, listed);
    openPage("/projects?dialog=archive&project=default");
    expect(await screen.findByRole("dialog", { name: "Archive Default" })).toBeTruthy();
    cleanup();
    openPage("/projects?dialog=rename&project=default");
    expect(await screen.findByRole("dialog", { name: "Rename Default" })).toBeTruthy();
    cleanup();
    openPage("/projects?dialog=remove&project=tern");
    expect(await screen.findByRole("dialog", { name: "Remove Tern" })).toBeTruthy();
  });

  it("renames a project with the display name it is shown by, and changes the display name alone", async () => {
    const sent = serveAdmin((call) => (call.method === "POST" && call.url === "/v1/projects/default/rename" ? { status: 200, body: { id: "d", name: "harbor", display_name: "Harbor", state: "active", created_at: 1, repositories: [] } } : undefined), listed);
    openPage("/projects?dialog=rename&project=default");
    const dialog = await screen.findByRole("dialog", { name: "Rename Default" });
    const display = within(dialog).getByLabelText("Display name") as HTMLInputElement;
    expect(display.value).toBe("Default");
    await userEvent.clear(display);
    await userEvent.type(display, "Harbor");
    await userEvent.click(within(dialog).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/projects/default/rename")).toHaveLength(1));
    expect(sentTo(sent, "POST", "/v1/projects/default/rename")[0]?.body).toEqual({ name: "default", display_name: "Harbor" });
    cleanup();
    openPage("/projects?dialog=rename&project=default");
    const again = await screen.findByRole("dialog", { name: "Rename Default" });
    await userEvent.clear(within(again).getByLabelText("New name"));
    await userEvent.type(within(again).getByLabelText("New name"), "harbor");
    await userEvent.clear(within(again).getByLabelText("Display name"));
    await userEvent.type(within(again).getByLabelText("Display name"), "Harbor");
    await userEvent.click(within(again).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/projects/default/rename")).toHaveLength(2));
    expect(sentTo(sent, "POST", "/v1/projects/default/rename")[1]?.body).toEqual({ name: "harbor", display_name: "Harbor" });
  });

  it("opens no dialog for a project it does not list", async () => {
    serveAdmin(undefined, listed);
    openPage("/projects?dialog=archive&project=nowhere");
    await screen.findByRole("region", { name: "Projects" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("focuses the Back up and Restore of the project an address names", async () => {
    serveAdmin(undefined, listed);
    openPage("/projects?focus=Back up Default");
    const table = await screen.findByRole("region", { name: "Projects" });
    const active = within(table).getByText("Default").closest("tr") as HTMLElement;
    await waitFor(() => expect(document.activeElement).toBe(within(active).getByRole("button", { name: "Back up" })));
    cleanup();
    openPage("/projects?focus=Restore Tern");
    const again = await screen.findByRole("region", { name: "Projects" });
    const tern = within(again).getByText("Tern").closest("tr") as HTMLElement;
    await waitFor(() => expect(document.activeElement).toBe(within(tern).getByRole("button", { name: "Restore" })));
  });

  it("creates a project, and shows the API's refusal at the name", async () => {
    let refuse = true;
    const sent = serveAdmin((call) => {
      if (call.method === "POST" && call.path === "/v1/projects") {
        return refuse ? refusal(400, "invalid", "a project's name is lowercase letters, digits, and hyphens") : { status: 201, body: { id: "x", name: "tidewater", state: "active", created_at: 3, repositories: [] } };
      }
      return undefined;
    });
    openPage("/projects?dialog=create");
    const dialog = await screen.findByRole("dialog", { name: "Create project" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Create project" }));
    expect(await within(dialog).findByText("Enter a name, such as harbor-api.")).toBeTruthy();
    await userEvent.type(within(dialog).getByLabelText("Name"), "Tide Water");
    await userEvent.click(within(dialog).getByRole("button", { name: "Create project" }));
    expect(await within(dialog).findByText("a project's name is lowercase letters, digits, and hyphens")).toBeTruthy();
    refuse = false;
    await userEvent.clear(within(dialog).getByLabelText("Name"));
    await userEvent.type(within(dialog).getByLabelText("Name"), "tidewater");
    await userEvent.click(within(dialog).getByRole("button", { name: "Create project" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/tidewater/repositories"));
    expect(sentTo(sent, "POST", "/v1/projects").at(-1)?.body).toEqual({ name: "tidewater" });
  });

  it("removes an archived project only once its name is typed", async () => {
    const sent = serveAdmin((call) => (call.method === "POST" && call.url === "/v1/projects/tern/remove" ? { status: 204 } : undefined), listed);
    openPage("/projects");
    await userEvent.click(await screen.findByRole("button", { name: "Remove…" }));
    const dialog = await screen.findByRole("dialog", { name: "Remove Tern" });
    await userEvent.click(within(dialog).getByLabelText("Remove with no backup"));
    const remove = within(dialog).getByRole("button", { name: "Remove project" });
    expect((remove as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(within(dialog).getByLabelText("Type tern to confirm"), "tern");
    await userEvent.click(remove);
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/projects/tern/remove")).toHaveLength(1));
    expect(sentTo(sent, "POST", "/v1/projects/tern/remove")[0]?.body).toEqual({ confirm: "tern", no_backup: true });
  });

  it("names the way forward when archiving is refused while lanes run", async () => {
    serveAdmin((call) => (call.method === "POST" && call.url === "/v1/projects/default/archive" ? refusal(409, "project_state", "2 lanes of default have not ended") : undefined));
    openPage("/projects");
    await userEvent.click(await screen.findByRole("button", { name: "Archive…" }));
    const dialog = await screen.findByRole("dialog", { name: "Archive default" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Archive" }));
    expect(await within(dialog).findByText("2 lanes of default have not ended")).toBeTruthy();
    expect(within(dialog).getByRole("link", { name: "Drain the fleet" }).getAttribute("href")).toBe("/projects/default/settings?section=fleet");
  });

  it("reaches Create, Import, and Manage from the project switcher", async () => {
    serveAdmin();
    openPage("/projects/default/roadmap");
    await userEvent.click(await screen.findByRole("button", { name: /^Project: / }));
    const menu = await screen.findByRole("menu", { name: "Projects" });
    expect(within(menu).getByRole("menuitem", { name: "Create project" })).toBeTruthy();
    expect(within(menu).getByRole("menuitem", { name: "Import an existing project" })).toBeTruthy();
    await userEvent.click(within(menu).getByRole("menuitem", { name: "Manage projects" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects"));
  });
});

describe("Repositories", () => {
  it("opens a repository's record and saves only what changed", async () => {
    const sent = serveAdmin((call) => (call.method === "PATCH" ? { status: 200, body: golden<{ repositories: unknown[] }>("repositories").repositories[0] } : undefined));
    openPage(here("/repositories?repo=id-api"));
    const record = await screen.findByRole("region", { name: "api" });
    expect(within(record).getByText("No unsaved changes")).toBeTruthy();
    await userEvent.type(within(record).getByLabelText("Base branch"), "develop");
    expect(within(record).getByText("base branch unset → develop")).toBeTruthy();
    await userEvent.click(within(record).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sentTo(sent, "PATCH", "/v1/repositories/id-api")).toHaveLength(1));
    expect(sentTo(sent, "PATCH", "/v1/repositories/id-api")[0]?.body).toEqual({ base: "develop" });
  });

  it("shows each CI key with the value in force and saves only the keys that changed, as ci.<key>", async () => {
    const sent = serveAdmin((call) => (call.method === "PATCH" ? { status: 200, body: golden<{ repositories: unknown[] }>("repositories").repositories[0] } : undefined));
    openPage(here("/repositories?repo=id-api"));
    const record = await screen.findByRole("region", { name: "api" });
    const steady = within(record).getByLabelText("ci_steady_interval") as HTMLInputElement;
    expect(steady.value).toBe("5m");
    expect((within(record).getByLabelText("ci_auto_rerun") as HTMLInputElement).value).toBe("1");
    await userEvent.clear(steady);
    await userEvent.type(steady, "2m");
    expect(within(record).getByText("ci_steady_interval 5m → 2m")).toBeTruthy();
    await userEvent.click(within(record).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sentTo(sent, "PATCH", "/v1/repositories/id-api")).toHaveLength(1));
    expect(sentTo(sent, "PATCH", "/v1/repositories/id-api")[0]?.body).toEqual({ ci: { ci_steady_interval: "2m" } });
  });

  it("opens the removal an address asks for, and sends nothing", async () => {
    const sent = serveAdmin();
    openPage(here("/repositories?repo=id-api&dialog=remove"));
    expect(await screen.findByRole("dialog", { name: "Remove api" })).toBeTruthy();
    expect(sent.filter((call) => call.method !== "GET")).toEqual([]);
  });

  it("lists Change and Remove for each repository in the command palette, which select it and focus its name", async () => {
    serveAdmin();
    openPage(here("/repositories"));
    await screen.findByRole("region", { name: "Repositories" });
    await userEvent.keyboard("{Control>}k{/Control}");
    await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.keyboard("change repository");
    await userEvent.click(await screen.findByRole("option", { name: /^Change repository: api/ }));
    const record = await screen.findByRole("region", { name: "api" });
    await waitFor(() => expect(document.activeElement).toBe(within(record).getByLabelText("Name")));
    await userEvent.keyboard("{Control>}k{/Control}");
    await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.keyboard("remove repository");
    await userEvent.click(await screen.findByRole("option", { name: /^Remove repository: api/ }));
    expect(await screen.findByRole("dialog", { name: "Remove api" })).toBeTruthy();
  }, 15_000);

  it("warns that review_approves in branch mode leaves the verdicts the only gate, and still saves", async () => {
    serveAdmin();
    openPage(here("/repositories?repo=id-api"));
    const record = await screen.findByRole("region", { name: "api" });
    await userEvent.click(within(record).getByRole("radio", { name: /review_approves/ }));
    expect(within(record).getByText("No forge runs checks in branch mode, so the verdicts are the only gate.")).toBeTruthy();
    expect((within(record).getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(false);
  });

  describe("the forge binding", () => {
    const patches = (sent: Sent[]) => sentTo(sent, "PATCH", "/v1/repositories/id-api");
    const open = async () => {
      openPage(here("/repositories?repo=id-api"));
      const record = await screen.findByRole("region", { name: "api" });
      return { record, field: within(record).getByLabelText("Forge binding") as HTMLInputElement };
    };

    it("asks for HOST/OWNER/REPO, which is what the server reads, with github.com/owner/repo as the example", async () => {
      serveAdmin();
      const { record, field } = await open();
      expect(field.placeholder).toBe("github.com/owner/repo…");
      expect(within(record).getByText(/HOST\/OWNER\/REPO/).textContent).toContain("github.com/owner/repo");
      expect(record.textContent).not.toContain("owner/name");
    });

    it.each(["acme/widgets", "github.com/acme", "github.com//widgets", "github.com/acme/widgets/extra", "github.com/acme/wid gets", "github.com/-acme/widgets", "https://github.com/acme/widgets"])(
      "refuses %j at the field and sends nothing",
      async (value) => {
        const sent = serveAdmin();
        const { record, field } = await open();
        await userEvent.type(field, value);
        await userEvent.click(within(record).getByRole("button", { name: "Save" }));
        expect(await within(record).findByText(/HOST\/OWNER\/REPO|a forge would refuse/, { selector: ".msg-error span" })).toBeTruthy();
        expect(field.getAttribute("aria-invalid")).toBe("true");
        expect(patches(sent)).toHaveLength(0);
      },
    );

    it("saves a binding of three parts as typed", async () => {
      const sent = serveAdmin((call) => (call.method === "PATCH" ? { status: 200, body: golden<{ repositories: unknown[] }>("repositories").repositories[0] } : undefined));
      const { record, field } = await open();
      await userEvent.type(field, "github.com/acme/widgets");
      await userEvent.click(within(record).getByRole("button", { name: "Save" }));
      await waitFor(() => expect(patches(sent)).toHaveLength(1));
      expect(patches(sent)[0]?.body).toEqual({ forge_binding: "github.com/acme/widgets" });
      expect(field.getAttribute("aria-invalid")).toBeNull();
    });

    it("clears the binding when it is emptied, which takes it from the remote", async () => {
      const sent = serveAdmin((call) => (call.method === "PATCH" ? { status: 200, body: golden<{ repositories: unknown[] }>("repositories").repositories[0] } : undefined));
      const { record, field } = await open();
      await userEvent.type(field, "github.com/acme/widgets");
      await userEvent.clear(field);
      expect(within(record).getByText("No unsaved changes")).toBeTruthy();
      expect(patches(sent)).toHaveLength(0);
    });

    it("shows a refusal of the server at the field, in the server's words", async () => {
      const message = "invalid repository: review target pull_request needs a forge binding, and the remote (git@example.invalid:acme/api.git) names no repository on a forge; name one as HOST/OWNER/REPO";
      serveAdmin((call) => (call.method === "PATCH" ? refusal(400, "invalid", message) : undefined));
      const { record, field } = await open();
      await userEvent.click(within(record).getByRole("button", { name: "pull_request" }));
      await userEvent.click(within(record).getByRole("button", { name: "Save" }));
      const shown = await within(record).findByText(message, { selector: ".msg-error span" });
      expect(shown.closest(".field")?.contains(field)).toBe(true);
      expect(field.getAttribute("aria-invalid")).toBe("true");
      expect(within(record).queryByRole("alert")).toBeNull();
    });

    it("keeps any other refusal at the foot of the form", async () => {
      serveAdmin((call) => (call.method === "PATCH" ? refusal(409, "conflict", "another change got there first") : undefined));
      const { record } = await open();
      await userEvent.type(within(record).getByLabelText("Base branch"), "develop");
      await userEvent.click(within(record).getByRole("button", { name: "Save" }));
      expect((await within(record).findByRole("alert")).textContent).toContain("another change got there first");
      expect(within(record).getByLabelText("Forge binding").getAttribute("aria-invalid")).toBeNull();
    });
  });

  it("registers a folder of clones after a dry run that records nothing", async () => {
    const sent = serveAdmin((call) => (call.method === "POST" && call.path === "/v1/repositories" ? { status: 200, body: golden("repository-dry-run") } : undefined));
    openPage(here("/repositories?dialog=register"));
    const dialog = await screen.findByRole("dialog", { name: "Register repositories in default" });
    await userEvent.type(within(dialog).getByLabelText("Folder"), "/work");
    await userEvent.click(within(dialog).getByRole("button", { name: "Run dry run" }));
    const table = await within(dialog).findByRole("region", { name: "Clones found" });
    expect(within(table).getByText("Will register")).toBeTruthy();
    expect(within(table).getByText("/srv/code/web")).toBeTruthy();
    expect(within(table).queryByText("/work/web")).toBeNull();
    expect(within(table).getByText("Already in this project")).toBeTruthy();
    expect(within(table).getByText(/^Refused: /)).toBeTruthy();
    expect(sentTo(sent, "POST", "/v1/repositories")[0]?.body).toEqual({ paths: ["/work"], depth: 1, dry_run: true });
    await userEvent.click(within(dialog).getByRole("button", { name: "Register 1 repository" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/repositories")).toHaveLength(2));
    expect(sentTo(sent, "POST", "/v1/repositories")[1]?.body).toEqual({ paths: ["/work"], depth: 1, dry_run: false });
  });

  describe("the dry run's table", () => {
    const remote = "git@example.invalid:acme/a-repository-with-a-remote-too-long-for-its-column.git";
    const long = "/srv/code/a/deep/folder/of/clones/each/with/a/long/name/a-repository-with-a-long-name";
    const report = {
      dry_run: true,
      repositories: [
        { path: `/private${long}`, entered: long, outcome: "registered" as const, repository: { id: "id-a", remote } },
        { path: "/work/web", outcome: "known" as const },
      ],
    };

    it("gives the result its own cell, and lets the path and the identity wrap", () => {
      const { container } = render(<DryRun report={report as never} />);
      const [first] = container.querySelectorAll("tbody tr");
      const cells = first?.querySelectorAll("td") ?? [];
      expect(cells[0]?.classList.contains("wrap")).toBe(true);
      expect(cells[1]?.classList.contains("wrap")).toBe(true);
      expect(cells[2]?.classList.contains("result")).toBe(true);
      expect(cells[2]?.classList.contains("wrap")).toBe(false);
      expect(within(cells[2] as HTMLElement).getByText("Will register")).toBeTruthy();
    });

    it("shows the path the member named, with where it leads in its title, and the path as given when none was named", () => {
      const { container } = render(<DryRun report={report as never} />);
      const paths = [...container.querySelectorAll("tbody td:first-child code")];
      expect(paths.map((code) => code.textContent)).toEqual([long, "/work/web"]);
      expect(paths[0]?.getAttribute("title")).toBe(`Leads to /private${long}`);
      expect(paths[1]?.getAttribute("title")).toBeNull();
    });
  });

  it("moves keyboard focus into the register dialog when a move to the page opens it", async () => {
    serveAdmin();
    openPage(here("/decisions"));
    await screen.findByRole("heading", { level: 1, name: "Decisions" });
    act(() => go(here("/repositories?dialog=register")));
    const dialog = await screen.findByRole("dialog", { name: "Register repositories in default" });
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    expect(document.activeElement).not.toBe(screen.getByRole("heading", { level: 1, name: "Repositories" }));
  });
});

describe("Settings", () => {
  it("saves a recovery policy as a new version after its difference is reviewed", async () => {
    const sent = serveAdmin((call) => (call.method === "POST" && call.path === "/v1/policies" ? { status: 201, body: { version: 3 } } : undefined));
    openPage(here("/settings?section=recovery"));
    expect(await screen.findByRole("heading", { level: 1, name: "Recovery policy" })).toBeTruthy();
    fireEvent.change(await screen.findByLabelText(/Attempts per work order/), { target: { value: "5" } });
    expect(screen.getByText("max_attempts 3 → 5")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the recovery policy change" });
    const diff = within(dialog).getByRole("table", { name: "Policy change" });
    expect(within(diff).getByText("max_attempts = 5")).toBeTruthy();
    expect(within(diff).getByText("max_attempts = 3")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/policies")).toHaveLength(1));
    expect((sentTo(sent, "POST", "/v1/policies")[0]?.body as { body: string }).body).toContain("[recovery_policy]\nversion = 3\n");
  });

  it("keeps an edit made the moment the saved policy appears", async () => {
    serveAdmin();
    // The edit lands as the fields are inserted, before React has run the effects of the render that
    // brought the saved policy. The watcher is attached before the page opens so it cannot miss them.
    const edit = new Promise<void>((done) => {
      const watch = new MutationObserver(() => {
        const field = screen.queryByLabelText(/Attempts per work order/);
        if (field) {
          watch.disconnect();
          fireEvent.change(field, { target: { value: "5" } });
          done();
        }
      });
      watch.observe(document.body, { childList: true, subtree: true });
    });
    openPage(here("/settings?section=recovery"));
    await edit;
    expect(screen.getByRole("heading", { level: 1, name: "Recovery policy" })).toBeTruthy();
    expect(await screen.findByText("max_attempts 3 → 5")).toBeTruthy();
    expect((screen.getByLabelText(/Attempts per work order/) as HTMLInputElement).value).toBe("5");
  });

  it("reverts to an earlier version by recording it as the newest", async () => {
    const sent = serveAdmin((call) => (call.method === "POST" && call.path === "/v1/policies" ? { status: 201, body: { version: 3 } } : undefined));
    openPage(here("/settings?section=recovery"));
    await userEvent.click(await screen.findByRole("tab", { name: /History/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Revert" }));
    const dialog = await screen.findByRole("dialog", { name: "Revert to version 1" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/policies")).toHaveLength(1));
    expect((sentTo(sent, "POST", "/v1/policies")[0]?.body as { body: string }).body).toContain("max_breaker_resets_per_day = 2");
  });

  it("pauses the fleet, and caps it with a save bar", async () => {
    const sent = serveAdmin((call) => (call.method === "PUT" ? { status: 200, body: golden("fleet") } : undefined));
    openPage(here("/settings?section=fleet"));
    await userEvent.click(await screen.findByRole("button", { name: "Pause fleet" }));
    await waitFor(() => expect(sentTo(sent, "PUT", "/v1/fleet/mode")[0]?.body).toEqual({ fleet_mode: "pause" }));
    // A read that brings the same caps again, as the stream asks for, leaves a draft alone.
    await screen.findByText(/fleet is now paused/);
    fireEvent.change(screen.getByLabelText(/Lanes per worker/), { target: { value: "3" } });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sentTo(sent, "PUT", "/v1/fleet/caps")[0]?.body).toEqual({ wip_limit: 0, lanes_per_repository: 0, lanes_per_worker: 3 }));
  });

  it("says how long a run window stays open by the server's clock, not the epoch", async () => {
    const asOf = 1_760_000_000_000;
    serveAdmin((call) =>
      call.method === "GET" && call.path === "/v1/workers/worker-1/autonomy"
        ? {
            status: 200,
            body: {
              as_of: asOf,
              worker_id: "worker-1",
              sign_in: "subscription",
              level: "attended",
              risk_acknowledged: true,
              may_claim: true,
              window: { id: "w1", worker_id: "worker-1", opened_by: "member-1", opened_at: asOf - 60_000, closes_at: asOf + 2 * 3_600_000 },
            },
          }
        : undefined,
    );
    openPage("/settings?section=autonomy");
    const panel = (await screen.findByText(/^Open, closes/)).closest(".panel") as HTMLElement;
    expect(panel.querySelector('[aria-hidden="true"]')?.textContent).toBe("in 2h");
  });

  // The autonomy routes that change something answer 204 with no body, so the panel must read again and
  // toast on the answer's status alone.
  describe("autonomy actions that answer 204", () => {
    const asOf = 1_760_000_000_000;
    const autonomy = "/v1/workers/worker-1/autonomy";
    const serveAutonomy = (state: { acknowledged: boolean; window: boolean }, closing?: Answer) =>
      serveAdmin((call) => {
        if (call.path === autonomy && call.method === "GET") {
          return {
            status: 200,
            body: {
              as_of: asOf,
              worker_id: "worker-1",
              sign_in: "subscription",
              level: "attended",
              risk_acknowledged: state.acknowledged,
              may_claim: true,
              window: state.window ? { id: "w1", worker_id: "worker-1", opened_by: "member-1", opened_at: asOf - 60_000, closes_at: asOf + 3_600_000 } : undefined,
            },
          };
        }
        if (call.path === "/v1/autonomy/risk") {
          return { status: 200, body: { statement: "An unattended worker acts on its own.", digest: "digest-1" } };
        }
        if (call.method === "POST" && call.path === `${autonomy}/acknowledgement`) {
          state.acknowledged = true;
          return { status: 204 };
        }
        if (call.method === "DELETE" && call.path === "/v1/workers/worker-1/windows") {
          if (closing) {
            return closing;
          }
          state.window = false;
          return { status: 204 };
        }
        return undefined;
      });

    it("updates the panel and says so after the stated risk is acknowledged", async () => {
      const sent = serveAutonomy({ acknowledged: false, window: false });
      openPage("/settings?section=autonomy");
      await userEvent.click(await screen.findByRole("button", { name: "Read the stated risk" }));
      const dialog = await screen.findByRole("dialog", { name: "The stated risk" });
      await userEvent.click(within(dialog).getByRole("button", { name: "I stand behind it" }));
      expect(await screen.findByText("You stand behind the stated risk for atlas.")).toBeTruthy();
      expect(await screen.findByText("The stated risk is acknowledged for this worker.")).toBeTruthy();
      expect(screen.queryByRole("button", { name: "Read the stated risk" })).toBeNull();
      expect(sentTo(sent, "POST", `${autonomy}/acknowledgement`)[0]?.body).toEqual({ digest: "digest-1" });
    });

    it("updates the panel and says so after a run window is closed", async () => {
      const sent = serveAutonomy({ acknowledged: true, window: true });
      openPage("/settings?section=autonomy");
      await userEvent.click(await screen.findByRole("button", { name: "Close window" }));
      expect(await screen.findByText("Closed atlas's run window. Its lanes keep running.")).toBeTruthy();
      expect(await screen.findByRole("button", { name: "Open window" })).toBeTruthy();
      expect(screen.queryByRole("button", { name: "Close window" })).toBeNull();
      expect(sentTo(sent, "DELETE", "/v1/workers/worker-1/windows")).toHaveLength(1);
    });

    it("keeps the panel and says nothing after a refused action", async () => {
      serveAutonomy({ acknowledged: true, window: true }, refusal(403, "forbidden"));
      openPage("/settings?section=autonomy");
      await userEvent.click(await screen.findByRole("button", { name: "Close window" }));
      expect(await screen.findByText("forbidden")).toBeTruthy();
      expect(screen.queryByText(/Closed atlas/)).toBeNull();
    });
  });

  it("lists the project's sections under the project and the rest at the installation", async () => {
    serveAdmin();
    openPage(here("/settings"));
    const tree = await screen.findByRole("navigation", { name: "Settings sections" });
    expect(within(tree).getByRole("link", { name: "Recovery policy" }).getAttribute("href")).toBe(here("/settings?section=recovery"));
    expect(within(tree).getByRole("link", { name: "Backup" }).getAttribute("href")).toBe("/settings?section=backup");
  });

  // A project's sections live at the project's address, so the installation's address lists none of them, and
  // no group heading stands over nothing.
  it("shows no project group at the installation's address", async () => {
    serveAdmin();
    openPage("/settings");
    const tree = await screen.findByRole("navigation", { name: "Settings sections" });
    expect(within(tree).getByText("Installation")).toBeTruthy();
    expect(within(tree).queryByText(/^Project/)).toBeNull();
    expect(within(tree).queryByRole("link", { name: "Recovery policy" })).toBeNull();
    for (const group of tree.querySelectorAll(".tree-group")) {
      expect(group.parentElement?.querySelector("a"), group.textContent ?? "").not.toBeNull();
    }
  });
});

describe("Agents", () => {
  it("edits an agent at the scope its address names, after its difference is reviewed", async () => {
    const sent = serveAdmin((call) => (call.method === "POST" && call.path === "/v1/roster/changes" ? { status: 200, body: { version: 2 } } : undefined));
    openPage(here("/agents?agent=heavy-implementation"));
    const editor = await screen.findByRole("region", { name: "heavy-implementation" });
    expect(within(editor).getByLabelText(/Effort/).closest(".field")?.textContent).toContain("Project");
    await userEvent.selectOptions(within(editor).getByLabelText(/^Class/), "frontier");
    await userEvent.click(within(editor).getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the roster change" });
    expect(within(dialog).getByText('class = "frontier"')).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 2" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/roster/changes")).toHaveLength(1));
    expect(sentTo(sent, "POST", "/v1/roster/changes")[0]?.body).toEqual({ scope: "project", change: { op: "set", agent: "heavy-implementation", fields: { class: "frontier" } } });
  });

  it("reads the installation's layer at the installation's address", async () => {
    const sent = serveAdmin();
    openPage("/agents");
    await screen.findByRole("region", { name: "Roster" });
    expect(sent.some((call) => call.url === "/v1/roster")).toBe(true);
    expect(screen.getByRole("button", { name: "Installation" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("lists the history with what each version changed", async () => {
    serveAdmin();
    openPage(here("/agents?tab=history"));
    expect(await screen.findByText("~ heavy-implementation.effort (the layer below) -> max")).toBeTruthy();
  });
});

describe("Add agent", () => {
  const path = "/v1/roster/changes";
  const changes = (sent: Sent[]) => sentTo(sent, "POST", path);
  const saved = (call: Sent) => (call.method === "POST" && call.path === path ? { status: 200, body: { version: 2 } } : undefined);
  const scout = '[[agent]]\nname = "scout"\npurpose = "Finds the code a change touches."\nkind = "lookup"\naccess = "read_only"\nclass = "small"\neffort = "low"\n\n[agent.tools]\nallow = ["read", "search"]\n';
  const open = async () => {
    openPage(here("/agents"));
    await userEvent.click(await screen.findByRole("button", { name: "Add agent" }));
    return screen.findByRole("dialog", { name: "Add agent" });
  };
  it("opens on an address that asks for it, as the command palette does", async () => {
    serveAdmin();
    openPage(here("/agents?dialog=add"));
    expect(await screen.findByRole("dialog", { name: "Add agent" })).toBeTruthy();
  });

  const paste = async (dialog: HTMLElement, text: string) => {
    await userEvent.click(within(dialog).getByRole("button", { name: "Paste TOML" }));
    fireEvent.change(within(dialog).getByLabelText(/Agent definition/), { target: { value: text } });
  };

  it("adds an agent as a copy of another, as it did", async () => {
    const sent = serveAdmin(saved);
    const dialog = await open();
    expect(within(dialog).getByRole("button", { name: "Copy an agent" }).getAttribute("aria-pressed")).toBe("true");
    await userEvent.type(within(dialog).getByLabelText("Name"), "sql-migration");
    await userEvent.selectOptions(within(dialog).getByLabelText("Copy from"), "explore");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 2" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({ scope: "project", change: { op: "add", agent: "sql-migration", from: "explore" } });
  });

  it("reads a pasted [[agent]] table, previews the agent it holds, and adds it as a definition", async () => {
    const sent = serveAdmin(saved);
    const dialog = await open();
    await paste(dialog, scout);
    const preview = await within(dialog).findByRole("table", { name: "Parsed agent" });
    expect(within(preview).getByText("[agents.scout]")).toBeTruthy();
    expect(within(preview).getByText('kind = "lookup"')).toBeTruthy();
    expect(within(preview).getByText('tools.allow = "read,search"')).toBeTruthy();
    // The table's own name stands in the field until the member types one.
    expect((within(dialog).getByLabelText("Name") as HTMLInputElement).value).toBe("scout");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 2" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({
      scope: "project",
      change: {
        op: "add",
        agent: "scout",
        definition: {
          name: "scout",
          purpose: "Finds the code a change touches.",
          kind: "lookup",
          access: "read_only",
          tools: { allow: ["read", "search"], deny: [] },
          class: "small",
          effort: "low",
          approval: "",
          instructions: "",
          report: "",
          enabled: true,
        },
        // The history names it as pasted, not as read from a file.
        definition_from: "pasted",
      },
    });
  });

  it("adds the definition under the name the member types, as the command adds the file under the name it is given", async () => {
    const sent = serveAdmin(saved);
    const dialog = await open();
    await paste(dialog, scout);
    const field = within(dialog).getByLabelText("Name");
    await userEvent.clear(field);
    await userEvent.type(field, "finder");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 2" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    const change = (changes(sent)[0]?.body as { change: { agent: string; definition: { name: string } } }).change;
    expect([change.agent, change.definition.name]).toEqual(["finder", "scout"]);
  });

  it("shows what stops a text at its field, says nothing of an empty one, and holds Save back for both", async () => {
    const sent = serveAdmin(saved);
    const dialog = await open();
    await userEvent.click(within(dialog).getByRole("button", { name: "Paste TOML" }));
    const save = () => within(dialog).getByRole("button", { name: "Save version 2" }) as HTMLButtonElement;
    expect(save().disabled).toBe(true);
    expect(within(dialog).queryByText(/Unknown key/)).toBeNull();
    const field = within(dialog).getByLabelText(/Agent definition/);
    fireEvent.change(field, { target: { value: '[[agent]]\ncolour = "red"\n' } });
    expect(within(dialog).getByText("Unknown key colour.")).toBeTruthy();
    expect(within(dialog).queryByRole("table", { name: "Parsed agent" })).toBeNull();
    expect(save().disabled).toBe(true);
    fireEvent.change(field, { target: { value: '[[agent]]\npurpose = "never closed\n' } });
    expect(within(dialog).getByText(/line 2, column/)).toBeTruthy();
    expect(save().disabled).toBe(true);
    fireEvent.change(field, { target: { value: '[[agent]]\nname = "a"\n[[agent]]\nname = "b"\n' } });
    expect(within(dialog).getByText("A text adds one agent, and this holds 2 [[agent]] tables.")).toBeTruthy();
    expect(changes(sent)).toHaveLength(0);
  });

  it("shows the API's refusal of a definition in the dialog, and keeps the text and the dialog", async () => {
    serveAdmin((call) => (call.method === "POST" && call.path === path ? refusal(400, "invalid", 'invalid roster: agent "scout" allows no tools') : undefined));
    const dialog = await open();
    await paste(dialog, '[[agent]]\nname = "scout"\nkind = "lookup"\n');
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 2" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain('agent "scout" allows no tools');
    expect((within(dialog).getByLabelText(/Agent definition/) as HTMLTextAreaElement).value).toContain('name = "scout"');
  });

  it("keeps a pasted text while the member switches to a copy and back", async () => {
    serveAdmin();
    const dialog = await open();
    await paste(dialog, scout);
    await userEvent.click(within(dialog).getByRole("button", { name: "Copy an agent" }));
    expect(within(dialog).queryByLabelText(/Agent definition/)).toBeNull();
    await userEvent.click(within(dialog).getByRole("button", { name: "Paste TOML" }));
    expect((within(dialog).getByLabelText(/Agent definition/) as HTMLTextAreaElement).value).toBe(scout);
  });
});

describe("Members and the audit", () => {
  it("lists each member with a live session", async () => {
    serveAdmin();
    openPage("/members");
    const table = await screen.findByRole("region", { name: "Members" });
    expect(within(table).getByText("member-1")).toBeTruthy();
  });

  it("moves keyboard focus into the sign out dialog when a move to the page opens it", async () => {
    serveAdmin();
    openPage("/decisions");
    await screen.findByRole("heading", { level: 1, name: "Decisions" });
    act(() => go("/members?dialog=sign-out-all"));
    const dialog = await screen.findByRole("dialog", { name: "Sign out all sessions" });
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    expect(document.activeElement).not.toBe(screen.getByRole("heading", { level: 1, name: "Members" }));
  });

  it("lists events newest first, filters them, and opens one's whole record", async () => {
    const sent = serveAdmin();
    openPage(here("/audit"));
    const table = await screen.findByRole("region", { name: "Events" });
    await userEvent.click(within(table).getByRole("button", { name: "5" }));
    expect(await screen.findByRole("heading", { name: "Event 5" })).toBeTruthy();
    await userEvent.selectOptions(screen.getByLabelText("Actor"), "member");
    await userEvent.click(screen.getByRole("button", { name: "Filter" }));
    await waitFor(() => expect(sent.some((call) => call.path.startsWith("/v1/audit?") && call.path.includes("actor=member"))).toBe(true));
  });
});

describe("Workers", () => {
  it("creates a join token and shows it once", async () => {
    const sent = serveAdmin((call) => {
      if (call.method === "GET" && call.path === "/v1/health") {
        return { status: 200, body: groupHealth };
      }
      return call.method === "POST" && call.path === "/v1/workers/tokens" ? { status: 201, body: { token: "join-secret" } } : undefined;
    });
    openPage("/workers?dialog=token");
    const dialog = await screen.findByRole("dialog", { name: "Create join token" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText("join-secret")).toBeTruthy();
    expect(within(dialog).getByText(/only time the token is shown/)).toBeTruthy();
    expect(sentTo(sent, "POST", "/v1/workers/tokens")).toHaveLength(1);
  });

  it("shows the join string a group server makes, once, with when it expires and which token it is", async () => {
    const joinString = "ropeshift-join-1.eyJ3b3JrZXJfdXJsIjoiaHR0cHM6Ly9yb3Blc2hpZnQubGFuOjc0MDAiLCJ0b2tlbiI6ImpvaW4tc2VjcmV0In0";
    const sent = serveAdmin((call) => {
      if (call.method === "GET" && call.path === "/v1/health") {
        return { status: 200, body: groupHealth };
      }
      return call.method === "POST" && call.path === "/v1/workers/tokens"
        ? { status: 201, body: { id: "01TOKENID", token: "join-secret", expires_at: 1790086400000, join_string: joinString } }
        : undefined;
    });
    openPage("/workers?dialog=token");
    const dialog = await screen.findByRole("dialog", { name: "Create join token" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText(joinString)).toBeTruthy();
    // The join string holds the token, and the dialog shows the string a worker is given and not the token beside it.
    expect(within(dialog).queryByText("join-secret")).toBeNull();
    expect(within(dialog).getByText(/only time the join string is shown/)).toBeTruthy();
    expect(within(dialog).getByText(/ROPESHIFT_JOIN/)).toBeTruthy();
    // A used string is no harmless string, and the dialog does not say it is.
    expect(within(dialog).getByText(/credential/)).toBeTruthy();
    expect(within(dialog).getByText(/not harmless once a worker has used it/)).toBeTruthy();
    expect(within(dialog).queryByText(/works once/i)).toBeNull();
    expect(within(dialog).getByText(/It expires/)).toBeTruthy();
    expect(within(dialog).getByText("01TOKENID")).toBeTruthy();
    expect(sentTo(sent, "POST", "/v1/workers/tokens")).toHaveLength(1);
  });

  it("revokes a join token by its ID, and says so", async () => {
    const sent = serveAdmin((call) => {
      if (call.method === "GET" && call.path === "/v1/health") {
        return { status: 200, body: groupHealth };
      }
      return call.method === "POST" && call.path === "/v1/workers/tokens/01TOKENID/revoke" ? { status: 200, body: { id: "01TOKENID" } } : undefined;
    });
    openPage("/workers?dialog=revoke-token");
    const dialog = await screen.findByRole("dialog", { name: "Revoke join token" });
    const revoke = within(dialog).getByRole("button", { name: "Revoke token" });
    expect((revoke as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Token ID" }), "01TOKENID");
    await userEvent.click(revoke);
    expect(await within(dialog).findByText(/Revoked the join token/)).toBeTruthy();
    expect(sentTo(sent, "POST", "/v1/workers/tokens/01TOKENID/revoke")).toHaveLength(1);
  });

  it("says why a join token was not revoked", async () => {
    serveAdmin((call) => {
      if (call.method === "GET" && call.path === "/v1/health") {
        return { status: 200, body: groupHealth };
      }
      return call.method === "POST" && call.path === "/v1/workers/tokens/01USED/revoke"
        ? { status: 409, body: { error: { code: "join_token_used", message: "join token already used" } } }
        : undefined;
    });
    openPage("/workers?dialog=revoke-token");
    const dialog = await screen.findByRole("dialog", { name: "Revoke join token" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Token ID" }), "01USED");
    await userEvent.click(within(dialog).getByRole("button", { name: "Revoke token" }));
    expect(await within(dialog).findByText(/already used/)).toBeTruthy();
    expect(within(dialog).queryByText(/Revoked the join token/)).toBeNull();
  });

  it("offers no revocation on a solo installation", async () => {
    serveAdmin();
    openPage("/workers?dialog=revoke-token");
    await screen.findByRole("heading", { level: 1, name: "Workers and resources" });
    expect(screen.queryByRole("dialog", { name: "Revoke join token" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Revoke join token" })).toBeNull();
  });

  it("says what to set when a group server has no worker URL", async () => {
    serveAdmin((call) => {
      if (call.method === "GET" && call.path === "/v1/health") {
        return { status: 200, body: groupHealth };
      }
      return call.method === "POST" && call.path === "/v1/workers/tokens"
        ? { status: 409, body: { error: { code: "no_worker_url", message: "this server does not know the URL its workers reach it at: start it with --worker-url (or set ROPESHIFT_WORKER_URL)" } } }
        : undefined;
    });
    openPage("/workers?dialog=token");
    const dialog = await screen.findByRole("dialog", { name: "Create join token" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText(/--worker-url/)).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "Copy" })).toBeNull();
  });
});

describe("Workers dialog focus", () => {
  it("moves keyboard focus into the join token dialog when a move to the page opens it", async () => {
    serveAdmin((call) => (call.method === "GET" && call.path === "/v1/health" ? { status: 200, body: groupHealth } : undefined));
    openPage("/decisions");
    await screen.findByRole("heading", { level: 1, name: "Decisions" });
    act(() => go("/workers?dialog=token"));
    const dialog = await screen.findByRole("dialog", { name: "Create join token" });
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    expect(document.activeElement).not.toBe(screen.getByRole("heading", { level: 1, name: "Workers and resources" }));
  });
});

describe("Admission", () => {
  const capsPath = "/v1/workers/worker-1/caps";
  const caps = (over: Record<string, unknown> = {}) => ({ status: 200, body: { ...golden<Record<string, unknown>>("worker-caps"), ...over } });
  const set = (sent: Sent[]) => sentTo(sent, "PUT", capsPath);
  const save = async () => userEvent.click(screen.getByRole("button", { name: "Save" }));

  it("shows a machine's caps with the recommendation and how the worker computed it, and says where they are kept", async () => {
    serveAdmin();
    openPage("/settings?section=admission");
    const box = await screen.findByRole("region", { name: "Recommendation" });
    expect(within(box).getByText("6 lanes")).toBeTruthy();
    expect(within(box).getByText(/8 cores less 1 reserved, at 1 per lane, fit 7 lanes/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Recommended" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByText("Your user profile")).toBeTruthy();
    // What this page once said was still to come is built: it names where the caps live and when a change counts.
    expect(screen.queryByText(/arrive with the worker's resource reports/)).toBeNull();
    expect(screen.getByText(/saved in the user profile on atlas's machine/)).toBeTruthy();
  });

  it("saves a number above the recommendation with the warning that says why, and the API's answer in a toast", async () => {
    const sent = serveAdmin((call) => (call.method === "PUT" && call.path === capsPath ? { status: 200, body: golden("worker-caps-above") } : undefined));
    openPage("/settings?section=admission");
    await userEvent.click(await screen.findByRole("button", { name: "A number" }));
    const lanes = screen.getByLabelText("Number of lanes");
    fireEvent.change(lanes, { target: { value: "9" } });
    // The warning names the recommendation and why, never blocks saving, and the box beside it says the same.
    const field = lanes.closest(".field") as HTMLElement;
    expect(within(field).getByText(/9 is above the recommendation of 6: 8 cores less 1 reserved/)).toBeTruthy();
    expect(within(field).getByText(/the cap is your decision/)).toBeTruthy();
    expect(screen.getByText("max lanes the recommendation, 6 → 9")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(false);
    await save();
    await waitFor(() => expect(set(sent)[0]?.body).toEqual({ max_lanes: 9, max_lanes_per_sign_in: 0 }));
    expect(set(sent)[0]?.csrf).not.toBeNull();
    expect(await screen.findByText("Saved. atlas max lanes is 9, above the recommendation of 6.")).toBeTruthy();
  });

  it("saves no cap, and a return to the recommendation, as the route reads them", async () => {
    const sent = serveAdmin((call) => {
      if (call.method === "PUT" && call.path === capsPath) {
        return caps();
      }
      return call.method === "GET" && call.path === capsPath ? caps({ max_lanes: 5, effective_max_lanes: 5 }) : undefined;
    });
    openPage("/settings?section=admission");
    expect(((await screen.findByLabelText("Number of lanes")) as HTMLInputElement).value).toBe("5");
    await userEvent.click(screen.getByRole("button", { name: "No cap" }));
    await save();
    await waitFor(() => expect(set(sent)[0]?.body).toEqual({ max_lanes: 0, max_lanes_per_sign_in: 0 }));
    await userEvent.click(await screen.findByRole("button", { name: "Recommended" }));
    await save();
    await waitFor(() => expect(set(sent)[1]?.body).toEqual({ max_lanes: null, max_lanes_per_sign_in: 0 }));
  });

  it("saves the lanes on one sign-in, with the note on why a subscription wants one", async () => {
    const sent = serveAdmin((call) => (call.method === "PUT" && call.path === capsPath ? caps({ max_lanes_per_sign_in: 2 }) : undefined));
    openPage("/settings?section=admission");
    const field = await screen.findByLabelText(/Lanes per sign-in/);
    expect(screen.getByText(/draws from the same usage limits/)).toBeTruthy();
    fireEvent.change(field, { target: { value: "2" } });
    await save();
    await waitFor(() => expect(set(sent)[0]?.body).toEqual({ max_lanes: null, max_lanes_per_sign_in: 2 }));
    expect(await screen.findByText(/Saved atlas's lane caps/)).toBeTruthy();
  });

  it("holds Save back for a number that is no whole number of lanes, and says how to cap nothing", async () => {
    const sent = serveAdmin();
    openPage("/settings?section=admission");
    await userEvent.click(await screen.findByRole("button", { name: "A number" }));
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Number of lanes"), { target: { value: "0" } });
    expect(screen.getByText(/To cap nothing, choose No cap/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
    expect(set(sent)).toHaveLength(0);
  });

  it("warns of nothing while the worker has reported no recommendation, and says so", async () => {
    serveAdmin((call) => (call.method === "GET" && call.path === capsPath ? caps({ recommendation: undefined, effective_max_lanes: 0 }) : undefined));
    openPage("/settings?section=admission");
    const box = await screen.findByRole("region", { name: "Recommendation" });
    expect(within(box).getByText(/has not reported a recommendation/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "A number" }));
    fireEvent.change(screen.getByLabelText("Number of lanes"), { target: { value: "99" } });
    expect(screen.queryByText(/above the recommendation/)).toBeNull();
  });

  it("shows the API's refusal of a save at the form, and keeps the form", async () => {
    serveAdmin((call) => (call.method === "PUT" && call.path === capsPath ? refusal(400, "invalid", "max_lanes_per_sign_in is not a number of lanes") : undefined));
    openPage("/settings?section=admission");
    fireEvent.change(await screen.findByLabelText(/Lanes per sign-in/), { target: { value: "2" } });
    await save();
    expect((await screen.findByRole("alert")).textContent).toContain("max_lanes_per_sign_in is not a number of lanes");
    expect((screen.getByLabelText(/Lanes per sign-in/) as HTMLInputElement).value).toBe("2");
  });

  it("shows why a worker on another machine has no form here, in the API's words", async () => {
    serveAdmin((call) =>
      call.method === "GET" && call.path === capsPath ? refusal(400, "invalid", "worker worker-1 runs on another machine, so its lane caps are set in the user's profile on that machine") : undefined,
    );
    openPage("/settings?section=admission");
    expect(await screen.findByText(/runs on another machine/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  });

  it("chooses among several machines, and reads and saves the caps of the one chosen", async () => {
    const two = { workers: [...workers.workers, { id: "worker-2", name: "tern", epoch: 1, protocol: 3 }] };
    const sent = serveAdmin((call) => {
      if (call.method === "GET" && call.path === "/v1/workers") {
        return { status: 200, body: two };
      }
      if (call.path === "/v1/workers/worker-2/caps") {
        return caps({ worker: "worker-2" });
      }
      return undefined;
    });
    openPage("/settings?section=admission&worker=worker-2");
    expect(await screen.findByRole("region", { name: "Recommendation" })).toBeTruthy();
    expect((screen.getByLabelText("Machine") as HTMLSelectElement).value).toBe("worker-2");
    fireEvent.change(await screen.findByLabelText(/Lanes per sign-in/), { target: { value: "3" } });
    await save();
    await waitFor(() => expect(sentTo(sent, "PUT", "/v1/workers/worker-2/caps")[0]?.body).toEqual({ max_lanes: null, max_lanes_per_sign_in: 3 }));
  });
});

describe("Estimate policy", () => {
  const post = (sent: Sent[]) => sentTo(sent, "POST", "/v1/policies");
  const published = (over: Record<string, unknown> = {}) => ({ status: 201, body: { version: 3, ...over } });
  const open = () => openPage(here("/settings?section=estimate"));

  it("lists the estimate policy among the project's sections, with what each of its settings does", async () => {
    serveAdmin();
    open();
    expect(await screen.findByRole("heading", { level: 1, name: "Estimate policy" })).toBeTruthy();
    const tree = screen.getByRole("navigation", { name: "Settings sections" });
    expect(within(tree).getByRole("link", { name: "Estimate policy" }).getAttribute("href")).toBe(here("/settings?section=estimate"));
    expect((await screen.findByLabelText(/Comparable outcomes a confident estimate needs/) as HTMLInputElement).value).toBe("12");
    expect((screen.getByLabelText(/Recent comparable outcomes/) as HTMLInputElement).value).toBe("60");
    expect((screen.getByLabelText(/Trials in a forecast/) as HTMLInputElement).value).toBe("1000");
    expect((screen.getByRole("checkbox", { name: /Share delivery history/ }) as HTMLInputElement).checked).toBe(true);
    // The pool's note names what it holds, and what it never does.
    expect(screen.getByText(/kind, size, capability class, durations, and cost, and no ID, title, repository, or content/)).toBeTruthy();
  });

  it("says a policy no member has saved is the built-in default, with no author or date", async () => {
    // A project with no saved version answers the built-in one, which nothing recorded, so it has no time.
    const stored = golden<PolicyList>("policies-estimate");
    const builtIn = { ...stored.versions[stored.versions.length - 1], id: "built-in", authored_by: "built-in", created_at: 0 } as PolicyVersion;
    serveAdmin((call) => (call.method === "GET" && call.path === "/v1/policies?kind=estimate" ? { status: 200, body: { versions: [builtIn] } } : undefined));
    open();
    expect(await screen.findByText("Version 1, the built-in default.")).toBeTruthy();
    expect(screen.queryByText(/by built-in/)).toBeNull();
    await userEvent.click(screen.getByRole("tab", { name: /History/ }));
    const version = await screen.findByRole("region", { name: "Version 1" });
    expect(within(version).getByText("The built-in default")).toBeTruthy();
    expect(version.textContent).not.toMatch(/built-in,/);
  });

  it("saves the next version after its difference is reviewed, with every setting stated", async () => {
    const sent = serveAdmin((call) => (call.method === "POST" && call.path === "/v1/policies" ? published() : undefined));
    open();
    fireEvent.change(await screen.findByLabelText(/Comparable outcomes a confident estimate needs/), { target: { value: "15" } });
    expect(screen.getByText("min_samples 12 → 15")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the estimate policy change" });
    const diff = within(dialog).getByRole("table", { name: "Policy change" });
    expect(within(diff).getByText("min_samples = 15")).toBeTruthy();
    expect(within(diff).getByText("min_samples = 12")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(post(sent)).toHaveLength(1));
    expect((post(sent)[0]?.body as { body: string }).body).toBe("[estimate_policy]\nversion = 3\nmin_samples = 15\nrecent_samples = 60\nforecast_trials = 1000\nshare_history = true\n");
    expect(post(sent)[0]?.csrf).not.toBeNull();
  });

  it("holds Review change back for a number the API would refuse, and names the fix at the field", async () => {
    const sent = serveAdmin();
    open();
    const review = async () => screen.getByRole("button", { name: "Review change" }) as HTMLButtonElement;
    fireEvent.change(await screen.findByLabelText(/Comparable outcomes a confident estimate needs/), { target: { value: "4" } });
    expect(screen.getByText("Enter a whole number, 5 or more.")).toBeTruthy();
    expect((await review()).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(/Comparable outcomes a confident estimate needs/), { target: { value: "70" } });
    expect(screen.getByText("Enter at least 70, the outcomes a confident estimate needs.")).toBeTruthy();
    expect((await review()).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(/Recent comparable outcomes/), { target: { value: "80" } });
    expect(screen.queryByText(/Enter at least/)).toBeNull();
    fireEvent.change(screen.getByLabelText(/Trials in a forecast/), { target: { value: "20001" } });
    expect(screen.getByText("Enter a whole number from 1 to 20,000.")).toBeTruthy();
    expect((await review()).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(/Trials in a forecast/), { target: { value: "20000" } });
    expect((await review()).disabled).toBe(false);
    expect(post(sent)).toHaveLength(0);
  });

  it("says when the project's records are still in the pool after it stops sharing, in the API's words", async () => {
    const notice = "The policy is in force, and the project's delivery records are still in the installation's pool: the next delivery tick takes them out.";
    const sent = serveAdmin((call) => (call.method === "POST" && call.path === "/v1/policies" ? published({ notice }) : undefined));
    open();
    await userEvent.click(await screen.findByRole("checkbox", { name: /Share delivery history/ }));
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the estimate policy change" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(post(sent)).toHaveLength(1));
    expect((post(sent)[0]?.body as { body: string }).body).toContain("share_history = false\n");
    expect(await screen.findByText(notice)).toBeTruthy();
  });

  it("shows the API's refusal in the review, and keeps the draft", async () => {
    serveAdmin((call) => (call.method === "POST" && call.path === "/v1/policies" ? refusal(400, "invalid", "invalid estimate policy: version 3 is not newer than version 3") : undefined));
    open();
    fireEvent.change(await screen.findByLabelText(/Trials in a forecast/), { target: { value: "500" } });
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the estimate policy change" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("is not newer than version 3");
    await userEvent.click(within(dialog).getByRole("button", { name: "Keep editing" }));
    expect((screen.getByLabelText(/Trials in a forecast/) as HTMLInputElement).value).toBe("500");
  });

  it("lists every version with its difference, and reverts to the built-in one by recording it as the newest", async () => {
    const sent = serveAdmin((call) => (call.method === "POST" && call.path === "/v1/policies" ? published() : undefined));
    open();
    await userEvent.click(await screen.findByRole("tab", { name: /History/ }));
    expect(await screen.findByText("Version 2")).toBeTruthy();
    expect(screen.getByText("In force")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Revert" }));
    const dialog = await screen.findByRole("dialog", { name: "Revert to version 1" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(post(sent)).toHaveLength(1));
    expect((post(sent)[0]?.body as { body: string }).body).toBe("[estimate_policy]\nversion = 3\nmin_samples = 10\nrecent_samples = 50\nforecast_trials = 2000\nshare_history = false\n");
  });

  it("reads and shows an archived project's policy without a way to change it", async () => {
    serveAdmin(undefined, { status: 200, body: { projects: [{ id: "default", name: "default", state: "archived", created_at: 1, repositories: [] }] } });
    open();
    const field = (await screen.findByLabelText(/Comparable outcomes a confident estimate needs/)) as HTMLInputElement;
    expect(field.disabled).toBe(true);
    expect(screen.getByText(/is archived and read-only/)).toBeTruthy();
  });
});
