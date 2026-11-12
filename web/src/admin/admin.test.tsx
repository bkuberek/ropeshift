import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, here, refusal, serveViews } from "../testing";

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
        ["/v1/roster/history", () => golden("roster-history")],
        ["/v1/roster", () => golden("roster")],
        ["/v1/members", () => golden("members")],
        ["/v1/audit", () => golden("audit")],
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

  it("warns that review_approves in branch mode leaves the verdicts the only gate, and still saves", async () => {
    serveAdmin();
    openPage(here("/repositories?repo=id-api"));
    const record = await screen.findByRole("region", { name: "api" });
    await userEvent.click(within(record).getByRole("radio", { name: /review_approves/ }));
    expect(within(record).getByText("No forge runs checks in branch mode, so the verdicts are the only gate.")).toBeTruthy();
    expect((within(record).getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("registers a folder of clones after a dry run that records nothing", async () => {
    const sent = serveAdmin((call) => (call.method === "POST" && call.path === "/v1/repositories" ? { status: 200, body: golden("repository-dry-run") } : undefined));
    openPage(here("/repositories?dialog=register"));
    const dialog = await screen.findByRole("dialog", { name: "Register repositories in default" });
    await userEvent.type(within(dialog).getByLabelText("Folder"), "/work");
    await userEvent.click(within(dialog).getByRole("button", { name: "Run dry run" }));
    const table = await within(dialog).findByRole("region", { name: "Clones found" });
    expect(within(table).getByText("Will register")).toBeTruthy();
    expect(within(table).getByText("Already in this project")).toBeTruthy();
    expect(within(table).getByText(/^Refused: /)).toBeTruthy();
    expect(sentTo(sent, "POST", "/v1/repositories")[0]?.body).toEqual({ paths: ["/work"], depth: 1, dry_run: true });
    await userEvent.click(within(dialog).getByRole("button", { name: "Register 1 repository" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/repositories")).toHaveLength(2));
    expect(sentTo(sent, "POST", "/v1/repositories")[1]?.body).toEqual({ paths: ["/work"], depth: 1, dry_run: false });
  });
});

describe("Import an existing project", () => {
  it("makes the calls ropeshift adopt makes, in its order, and opens the session's lane", async () => {
    const sent = serveAdmin((call) => {
      if (call.method === "POST" && call.path === "/v1/projects") {
        return { status: 201, body: { id: "t", name: "tidewater", state: "active", created_at: 3, repositories: [] } };
      }
      if (call.method === "POST" && call.path === "/v1/repositories") {
        return { status: 200, body: golden("repository-dry-run") };
      }
      if (call.method === "GET" && call.path === "/v1/repositories") {
        return { status: 200, body: { repositories: [] } };
      }
      if (call.method === "POST" && call.path === "/v1/work-orders/" + (call.path.split("/")[3] ?? "") + "/review") {
        return { status: 200, body: { state: "ready" } };
      }
      if (call.method === "POST" || call.method === "PATCH") {
        return { status: 200, body: { state: "ready" } };
      }
      return undefined;
    });
    openPage("/import");
    await screen.findByRole("heading", { level: 1, name: "Import an existing project" });
    await userEvent.type(await screen.findByLabelText("Folder"), "/srv/code/Tidewater");
    expect((screen.getByLabelText("New project's name") as HTMLInputElement).value).toBe("tidewater");
    await waitFor(() => expect((screen.getByLabelText("Worker") as HTMLSelectElement).value).toBe("worker-1"));
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    await screen.findByRole("region", { name: "Clones found" });
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    await userEvent.click(await screen.findByRole("radio", { name: /web/ }));
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    await userEvent.click(screen.getByRole("button", { name: "Start adopt session" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/tidewater/lane"));
    const steps = sent.filter((call) => call.method !== "GET").map((call) => `${call.method} ${call.project || "-"} ${call.path.replace(/\/[0-9A-Z]{26}(\/|$)/, "/ID$1")}`);
    expect(steps).toEqual([
      "POST - /v1/projects",
      "POST tidewater /v1/repositories",
      "POST tidewater /v1/repositories",
      "PATCH tidewater /v1/repositories/id-web",
      "POST tidewater /v1/work-orders",
      "POST tidewater /v1/work-orders/ID/review",
      "POST tidewater /v1/work-orders/ID/claims",
      "POST tidewater /v1/work-orders/ID/lanes",
    ]);
    const work = sent.find((call) => call.method === "POST" && call.path === "/v1/work-orders");
    expect(work?.body).toMatchObject({ kind: "plan", adopt: true, title: "Adopt tidewater", repository_id: "id-web" });
  });

  it("refuses a project that already has an approved plan, and offers a planning session", async () => {
    serveAdmin((call) => (call.path.startsWith("/v1/approvals") ? { status: 200, body: { approvals: [{ id: "a", state: "approved", kind: "plan" }] } } : undefined));
    openPage("/import?project=default");
    await screen.findByRole("heading", { level: 1, name: "Import an existing project" });
    await userEvent.type(await screen.findByLabelText("Folder"), "/srv/code/default");
    await waitFor(() => expect((screen.getByLabelText("Worker") as HTMLSelectElement).value).toBe("worker-1"));
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    expect(await screen.findByText(/already has an approved plan/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Start a planning session instead" })).toBeTruthy();
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

  it("shows each machine's lane cap as its worker reports it, and says where it is set", async () => {
    serveAdmin();
    openPage("/settings?section=admission");
    const table = await screen.findByRole("region", { name: "Lane caps by machine" });
    expect(within(table).getByText("6")).toBeTruthy();
    expect(within(table).getByText("Your user profile")).toBeTruthy();
    expect(screen.getByText(/arrive with the worker's resource reports/)).toBeTruthy();
  });

  it("lists the project's sections under the project and the rest at the installation", async () => {
    serveAdmin();
    openPage(here("/settings"));
    const tree = await screen.findByRole("navigation", { name: "Settings sections" });
    expect(within(tree).getByRole("link", { name: "Recovery policy" }).getAttribute("href")).toBe(here("/settings?section=recovery"));
    expect(within(tree).getByRole("link", { name: "Backup" }).getAttribute("href")).toBe("/settings?section=backup");
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

describe("Members and the audit", () => {
  it("lists each member with a live session", async () => {
    serveAdmin();
    openPage("/members");
    const table = await screen.findByRole("region", { name: "Members" });
    expect(within(table).getByText("member-1")).toBeTruthy();
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
    const sent = serveAdmin((call) => (call.method === "POST" && call.path === "/v1/workers/tokens" ? { status: 201, body: { token: "join-secret" } } : undefined));
    openPage("/workers?dialog=token");
    const dialog = await screen.findByRole("dialog", { name: "Create join token" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText("join-secret")).toBeTruthy();
    expect(within(dialog).getByText(/only time the token is shown/)).toBeTruthy();
    expect(sentTo(sent, "POST", "/v1/workers/tokens")).toHaveLength(1);
  });
});
