import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { AdoptionPlan } from "../api/admin";
import { startedAt } from "../lanes/starting";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, refusal, serveViews } from "../testing";

type Answer = { status: number; body?: unknown };

const workers = { workers: [{ id: "worker-1", name: "atlas", epoch: 1, protocol: 3, manifest: { capacity: { max_lanes: 6 } } }] };

const plan = (name: string) => golden<AdoptionPlan>(`adoption-plan-${name}`);

// registered is what registering answers for the clones a plan lists.
function registered(paths: string[]) {
  return { dry_run: false, repositories: paths.map((path) => ({ path, outcome: "registered", repository: { id: path.split("/").pop(), name: path.split("/").pop() } })) };
}

// serveImport answers what Import reads and the calls it makes after, and lets a test answer anything first. The plan
// route answers the plan a test names for the request it reads.
function serveImport(read: (body: Record<string, unknown>, call: Sent) => AdoptionPlan | Answer, answer?: (sent: Sent) => Answer | undefined) {
  return serveViews((call) => {
    const own = answer?.(call);
    if (own) {
      return own;
    }
    if (call.method === "GET" && call.path === "/v1/workers") {
      return { status: 200, body: workers };
    }
    if (call.method === "GET" && call.path.startsWith("/v1/approvals")) {
      return { status: 200, body: { approvals: [] } };
    }
    if (call.method === "GET" && call.path === "/v1/repositories") {
      return { status: 200, body: { repositories: [] } };
    }
    if (call.method === "POST" && call.path === "/v1/adoption/plan") {
      const read_ = read(call.body as Record<string, unknown>, call);
      return "status" in read_ && typeof read_.status === "number" ? (read_ as Answer) : { status: 200, body: read_ };
    }
    if (call.method === "POST" && call.path === "/v1/projects") {
      const body = call.body as { name: string };
      return { status: 201, body: { id: body.name, name: body.name, state: "active", created_at: 3, repositories: [] } };
    }
    if (call.method === "POST" && call.path.endsWith("/review")) {
      return { status: 200, body: { state: "ready" } };
    }
    if (call.method === "POST" || call.method === "PATCH") {
      return { status: 200, body: { state: "ready" } };
    }
    return undefined;
  });
}

// fill enters the folders one to a line, and waits for the one worker to be chosen.
async function fill(folders: string) {
  await screen.findByRole("heading", { level: 1, name: "Import an existing project" });
  await userEvent.click(await screen.findByLabelText("Folders", { selector: "textarea" }));
  await userEvent.paste(folders);
  await waitFor(() => expect((screen.getByLabelText("Worker") as HTMLSelectElement).value).toBe("worker-1"));
}

const steps = (sent: Sent[]) => sent.filter((call) => call.method !== "GET").map((call) => `${call.method} ${call.project || "-"} ${call.path.replace(/\/[0-9A-Z]{26}(\/|$)/, "/ID$1")}`);
const planCalls = (sent: Sent[]) => sent.filter((call) => call.method === "POST" && call.path === "/v1/adoption/plan");

describe("Import an existing project, reading the plan", () => {
  it("reads the plan of several folders through the plan route, with the first as the clone adopt runs in, and creates no project", async () => {
    const sent = serveImport(() => plan("workspace-files"));
    openPage("/import");
    await fill("/work/design\n  /work/api  \n\n/work/web\n");
    await userEvent.clear(screen.getByLabelText("Depth"));
    await userEvent.type(screen.getByLabelText("Depth"), "2");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    await screen.findByRole("region", { name: "Clones found" });
    expect(planCalls(sent)).toHaveLength(1);
    expect(planCalls(sent)[0]?.project, "a project not created yet has no route of its own").toBe("");
    expect(planCalls(sent)[0]?.body).toEqual({ root: "/work/design", paths: ["/work/api", "/work/web"], depth: 2, new_project: true, practices: true });
    expect(steps(sent), "the dry run records nothing and creates no project").toEqual(["POST - /v1/adoption/plan"]);
    expect(screen.getByRole("heading", { level: 2, name: "Dry run" })).toBeTruthy();
    const dry = screen.getByRole("region", { name: "Clones found" });
    expect(within(dry).getAllByRole("row")).toHaveLength(4);
  });

  it("reads the plan of a project that exists at the project's route", async () => {
    const sent = serveImport(() => plan("single"));
    openPage("/import?project=default");
    await fill("/work/solo");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    await screen.findByRole("region", { name: "Clones found" });
    expect(planCalls(sent)[0]?.project).toBe("default");
    expect(planCalls(sent)[0]?.body).toEqual({ root: "/work/solo", depth: 1, practices: true });
  });

  it("asks for an absolute path, and for a folder, before it reads anything", async () => {
    const sent = serveImport(() => plan("single"));
    openPage("/import");
    await fill("work/solo");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    expect(await screen.findByText(/Write each folder as an absolute path\. work\/solo is not one/)).toBeTruthy();
    expect(planCalls(sent)).toHaveLength(0);
  });

  it("shows the server's refusal at the first step", async () => {
    serveImport(() => refusal(400, "invalid", "run adopt in the top of a git clone with a commit; /srv/code is none"));
    openPage("/import");
    await fill("/srv/code");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    expect(await screen.findByText(/run adopt in the top of a git clone with a commit/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Run dry run" })).toBeTruthy();
  });

  it("refuses a project that already has an approved plan, and offers a planning session", async () => {
    const sent = serveImport(() => plan("single"), (call) => (call.path.startsWith("/v1/approvals") ? { status: 200, body: { approvals: [{ id: "a", state: "approved", kind: "plan" }] } } : undefined));
    openPage("/import?project=default");
    await fill("/srv/code/default");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    expect(await screen.findByText(/already has an approved plan/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Start a planning session instead" })).toBeTruthy();
    expect(planCalls(sent)).toHaveLength(0);
  });
});

describe("Import an existing project, starting the session", () => {
  it("makes the calls ropeshift adopt makes, in its order, with no restructuring unless it is chosen", async () => {
    const sent = serveImport(
      () => plan("workspace-files"),
      (call) => (call.method === "POST" && call.path === "/v1/repositories" ? { status: 200, body: registered(["/work/api", "/work/design", "/work/web"]) } : undefined),
    );
    openPage("/import");
    await fill("/work/design\n/work/api\n/work/web");
    expect((screen.getByLabelText("New project's name") as HTMLInputElement).value).toBe("design");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    await screen.findByRole("region", { name: "Clones found" });
    const choice = screen.getByRole("checkbox", { name: "Restructure the planning repository as listed" }) as HTMLInputElement;
    expect(choice.checked, "a restructuring happens only when it is chosen").toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect((await screen.findByRole("radio", { name: /design/ })) as HTMLInputElement).toHaveProperty("checked", true);
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    await userEvent.click(screen.getByRole("button", { name: "Start adopt session" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/design/planning"));
    const lane = new URLSearchParams(window.location.search).get("lane") ?? "";
    expect(lane).toMatch(/^[0-9A-Z]{26}$/);
    expect(startedAt(lane), "the entry says this lane was just started, so its pane waits for the lane").toBeGreaterThan(0);
    expect(steps(sent)).toEqual([
      "POST - /v1/adoption/plan",
      "POST - /v1/adoption/plan",
      "POST - /v1/projects",
      "POST design /v1/repositories",
      "PATCH design /v1/repositories/design",
      "POST design /v1/work-orders",
      "POST design /v1/work-orders/ID/review",
      "POST design /v1/work-orders/ID/claims",
      "POST design /v1/work-orders/ID/lanes",
    ]);
    expect(sent.find((call) => call.method === "POST" && call.path === "/v1/projects")?.body, "the new project is shown by its folder's name").toEqual({ name: "design", display_name: "design" });
    expect(sent.find((call) => call.method === "POST" && call.path === "/v1/repositories")?.body, "the clones the plan lists, one level deep, as the CLI registers them").toEqual({
      paths: ["/work/api", "/work/design", "/work/web"],
      depth: 1,
      dry_run: false,
    });
    const work = sent.find((call) => call.method === "POST" && call.path === "/v1/work-orders")?.body as Record<string, unknown>;
    expect(work).toMatchObject({ kind: "plan", adopt: true, title: "Adopt design", repository_id: "design" });
    expect(work, "no restructuring is sent").not.toHaveProperty("restructure");
  });

  it("sends the plan's digest and choices with the work when Restructure is chosen, leaving out the trees checked", async () => {
    const sent = serveImport(
      (body) => ((body.leave_out as string[] | undefined)?.includes("tools") ? plan("consolidate-left-out") : plan("consolidate")),
      (call) => (call.method === "POST" && call.path === "/v1/repositories" ? { status: 200, body: registered(["/work/ws", "/work/ws/repos/api"]) } : undefined),
    );
    openPage("/import");
    await fill("/work/ws");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    await screen.findByRole("region", { name: "Clones found" });
    const tree = await screen.findByRole("checkbox", { name: /Leave out tools/ });
    expect((tree as HTMLInputElement).checked).toBe(false);
    await userEvent.click(tree);
    await waitFor(() => expect((screen.getByRole("checkbox", { name: /Leave out tools/ }) as HTMLInputElement).checked).toBe(true));
    expect(planCalls(sent).map((call) => (call.body as { leave_out?: string[] }).leave_out)).toEqual([undefined, ["tools"]]);
    await userEvent.click(screen.getByRole("checkbox", { name: "Restructure the planning repository as listed" }));
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    const radios = (await screen.findAllByRole("radio")) as HTMLInputElement[];
    expect(radios.filter((radio) => radio.checked).map((radio) => radio.disabled), "the repository the plan names is the one it restructures").toEqual([false]);
    expect(radios.filter((radio) => !radio.checked).every((radio) => radio.disabled)).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    await userEvent.click(screen.getByRole("button", { name: "Start adopt session" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/ws/planning"));
    expect(planCalls(sent).at(-1)?.body, "Start reads the plan again, as the one the member read").toMatchObject({ root: "/work/ws", leave_out: ["tools"] });
    expect(sent.find((call) => call.method === "POST" && call.path === "/v1/repositories")?.body, "the design repository's history is imported, so it is not registered").toMatchObject({ paths: ["/work/ws", "/work/ws/repos/api"] });
    const left = plan("consolidate-left-out");
    const work = sent.find((call) => call.method === "POST" && call.path === "/v1/work-orders")?.body as Record<string, unknown>;
    expect(work).toMatchObject({
      adopt: true,
      repository_id: "ws",
      restructure: { shape: "consolidate", digest: left.digest, design_path: "/work/ws/repos/design", design_root: "design", leave_out: ["tools"], root: "/work/ws", depth: 1 },
    });
  });

  it("sends the shape, the digest, the request the plan was read with, and the plan's parts for a restructuring that is not a consolidation", async () => {
    const parts = { layout: "sha256:" + "a".repeat(64), files: "sha256:" + "b".repeat(64) };
    const sent = serveImport(
      () => ({ ...plan("planned"), parts }),
      (call) => (call.method === "POST" && call.path === "/v1/repositories" ? { status: 200, body: registered(["/work/ws", "/work/ws/repos/api", "/work/ws/repos/web"]) } : undefined),
    );
    openPage("/import");
    await fill("/work/ws");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    await userEvent.click(await screen.findByRole("checkbox", { name: "Restructure the planning repository as listed" }));
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    await userEvent.click(await screen.findByRole("button", { name: "Continue" }));
    await userEvent.click(screen.getByRole("button", { name: "Start adopt session" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/ws/planning"));
    const work = sent.find((call) => call.method === "POST" && call.path === "/v1/work-orders")?.body as { restructure: unknown };
    expect(work.restructure).toEqual({ shape: "planned", digest: plan("planned").digest, root: "/work/ws", depth: 1, parts });
  });

  it("shows the refusal and the new plan when the layout changed since the dry run, and starts nothing", async () => {
    let reads = 0;
    const sent = serveImport(() => {
      reads += 1;
      return reads === 1 ? plan("planned") : plan("workspace-files");
    });
    openPage("/import");
    await fill("/work/ws");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    await userEvent.click(await screen.findByRole("checkbox", { name: "Restructure the planning repository as listed" }));
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    await userEvent.click(await screen.findByRole("button", { name: "Continue" }));
    await userEvent.click(screen.getByRole("button", { name: "Start adopt session" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/The layout changed since you read the plan, so nothing was started/);
    expect(alert.textContent).toContain("the shape is now workspace_files, not planned");
    expect(steps(sent), "nothing was created, registered, or started").toEqual(["POST - /v1/adoption/plan", "POST - /v1/adoption/plan"]);
    expect(screen.getByRole("heading", { level: 2, name: "Dry run" })).toBeTruthy();
    expect(screen.getByText("workspace_files")).toBeTruthy();
    expect((screen.getByRole("checkbox", { name: "Restructure the planning repository as listed" }) as HTMLInputElement).checked, "the choice is made again on the new plan").toBe(false);
    expect(screen.getByRole("button", { name: "Continue" })).toBeTruthy();
    expect(window.location.pathname).toBe("/import");
  });

  it("says why a registration was refused, and starts no session", async () => {
    const sent = serveImport(
      () => plan("single"),
      (call) =>
        call.method === "POST" && call.path === "/v1/repositories"
          ? { status: 200, body: { dry_run: false, repositories: [{ path: "/work/solo", outcome: "refused", reason: "a project holds it" }] } }
          : undefined,
    );
    openPage("/import");
    await fill("/work/solo");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    await userEvent.click(await screen.findByRole("button", { name: "Continue" }));
    await userEvent.click(await screen.findByRole("button", { name: "Continue" }));
    await userEvent.click(screen.getByRole("button", { name: "Start adopt session" }));
    expect(await screen.findByText(/This repository was not registered: a project holds it/)).toBeTruthy();
    expect(steps(sent).some((step) => step.includes("/work-orders"))).toBe(false);
  });
});

describe("Import an existing project, where the plan lives", () => {
  it("offers no restructuring when the layout settles nowhere for the plan to live, and lets step 3 choose", async () => {
    serveImport(() => plan("undecided"));
    openPage("/import");
    await fill("/work/api\n/work/web");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    const choice = (await screen.findByRole("checkbox", { name: "Restructure the planning repository as listed" })) as HTMLInputElement;
    expect(choice.disabled).toBe(true);
    expect(screen.getByText(/Nothing in the layout settles where the plan lives/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    const radios = (await screen.findAllByRole("radio")) as HTMLInputElement[];
    expect(radios.map((radio) => radio.checked), "none is chosen for the member").toEqual([false, false]);
    expect(radios.every((radio) => !radio.disabled)).toBe(true);
    expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("does not offer Restructure for a single repository", async () => {
    serveImport(() => plan("single"));
    openPage("/import");
    await fill("/work/solo");
    await userEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    await screen.findByRole("region", { name: "Clones found" });
    expect(screen.queryByRole("group", { name: "Restructure" })).toBeNull();
  });
});
