// @vitest-environment node
import { readFileSync } from "node:fs";
import { join } from "node:path";

import type { Browser, Page, Route } from "playwright-core";
import type { ViteDevServer } from "vite";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import routes from "../testdata/routes.json";
import { paletteActions } from "./app/paletteActions";
import { openBrowser, suiteFor } from "./browser";
import { goldens, ready, serveApp } from "./browserApp";
import type { Call } from "./keyboardTesting";
import { Keys, serveChanges } from "./keyboardTesting";

// FR-UI-9 and ui-design.md: every operation that changes something has a keyboard path, and each is tested. For each
// changing route with a control in the UI, a path starts at the default view, uses only the keyboard (the palette,
// the G keys, Tab, Enter, the arrows, and Esc), reaches the control, and presses it. The daemon is a stand-in that
// records each change it is sent, so the assertion is on the call, and a route with no path fails.

type Sent = { method: string; path: RegExp; body?: unknown };

type KeyPath = {
  // sent is the call the path ends in.
  sent: Sent;
  run: (keys: Keys, page: Page) => Promise<void>;
  // answers answers a request that the shared daemon does not, and says whether it did.
  answers?: (route: Route) => Promise<boolean> | boolean;
};

// json answers a request with a body.
const json = (route: Route, body: unknown) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) }).then(() => true);

// reply answers the requests a list names, each a pattern of the address, a body, and the method, which is a read unless it
// says otherwise.
const reply =
  (...rows: [pattern: RegExp, body: unknown, method?: string][]) =>
  async (route: Route): Promise<boolean> => {
    const row = rows.find(([pattern, , method = "GET"]) => method === route.request().method() && pattern.test(route.request().url()));
    return row ? json(route, row[1]) : false;
  };

// limitedFloor is the floor with worker-a's sign-in at its usage limit, resetting in two hours, and with a start
// scheduled for that reset when scheduled says.
function limitedFloor(scheduled = false): unknown {
  const floor = JSON.parse(goldens["shop-floor"] ?? "{}") as { workers: { worker: { id: string }; orchestrator?: unknown; orchestrator_hold?: unknown; scheduled_start?: unknown }[] };
  const resets = Date.now() + 2 * 60 * 60 * 1000;
  for (const line of floor.workers) {
    if (line.worker.id === "worker-a") {
      line.orchestrator = undefined;
      line.orchestrator_hold = { reason: "usage_limit", window: "five_hour", resets_at: resets };
      line.scheduled_start = scheduled ? { worker_id: "worker-a", starts_at: resets, scheduled_by: "member-1", scheduled_at: Date.now() } : undefined;
    }
  }
  return floor;
}

const signing = new Set(["POST /v1/sessions", "POST /v1/sessions/end"]);
const key = (route: { method: string; pattern: string }) => `${route.method} ${route.pattern}`;
const changing = (routes as { method: string; pattern: string }[]).filter((route) => route.method !== "GET" && !signing.has(key(route))).map(key);

const post = (path: RegExp, body?: unknown): Sent => ({ method: "POST", path, body });

// matches says whether a change the daemon was sent is the one a path ends in: its method, its address, and what it holds.
function matches(call: Call, want: Sent): boolean {
  if (call.method !== want.method || !want.path.test(call.path)) {
    return false;
  }
  try {
    if (want.body !== undefined) {
      expect(call.body).toEqual(want.body);
    }
    return true;
  } catch {
    return false;
  }
}

// inDialog names a control of the dialog that stands open.
const inDialog = (page: Page) => page.getByRole("dialog");

// dialog runs a palette action that opens a dialog, and waits for it, so the keys that follow land in it.
async function dialog(keys: Keys, page: Page, title: string, query?: string): Promise<void> {
  await keys.palette(title, query);
  await page.getByRole("dialog").waitFor();
}

// autonomy answers what a worker's autonomy page reads: a worker at attended, whose stated risk is not acknowledged, with
// a run window open or none.
const autonomy = (window?: unknown, accepted?: unknown) =>
  reply(
    [/\/v1\/workers\/worker-1\/autonomy$/, { as_of: 1790000000000, worker_id: "worker-1", sign_in: "subscription", level: "attended", chosen_level: "attended", risk_acknowledged: false, may_claim: true, window, one_way: { statement: "The orchestrator will make one-way decisions outside security, compliance and data for you.", digest: "sha256:def", accepted } }],
    [/\/v1\/autonomy\/risk$/, { statement: "A worker at this level runs the agent's commands without asking.", digest: "sha256:abc" }],
  );

// lane makes the search and the plans view say that a work order allows more than its state in the golden does: the
// actions are the server's to name, so a test that needs one the golden lacks names it here.
const lane = (id: string, actions: string[], title = id) => async (route: Route): Promise<boolean> => {
  const url = route.request().url();
  if (route.request().method() !== "GET") {
    return false;
  }
  if (/\/views\/search/.test(url)) {
    // The search holds the work order the page does, under the ID the page reads it by.
    const found = JSON.parse(goldens.search ?? "{}") as { records: { id: string; kind: string; title?: string; state?: string; actions?: string[] }[] };
    const records = found.records.filter((record) => !(record.kind === "work_order" && record.id === id));
    const known = found.records.find((record) => record.kind === "work_order" && record.id === id);
    return json(route, { ...found, records: [{ id, kind: "work_order", state: known?.state ?? "ready", title: known?.title ?? title, actions }, ...records] });
  }
  if (/\/views\/plans$/.test(url)) {
    const plans = JSON.parse(goldens.plans ?? "{}") as { work_actions: Record<string, { actions: string[] }> };
    return json(route, { ...plans, work_actions: { ...plans.work_actions, [id]: { actions } } });
  }
  return false;
};

// history answers a roster's history with two versions, so the older can be restored.
const history = () => {
  const held = JSON.parse(goldens["roster-history"] ?? "{}") as { versions: Record<string, unknown>[] };
  const body = { ...held, versions: [{ ...held.versions[0], id: "generated-2", version: 2 }, ...held.versions] };
  return reply([/\/roster\/history(\?.*)?$/, body]);
};

// livePane answers a lane's pane with a screen, which is what makes it live to type into.
const livePane = async (route: Route): Promise<boolean> => {
  if (route.request().method() !== "GET" || !/\/panes\/lane-wo-running$/.test(route.request().url())) {
    return false;
  }
  const screen = { lane_id: "lane-wo-running", generation: 1, screen: "Planning: what is the plan for?" };
  await route.fulfill({ status: 200, contentType: "text/event-stream", body: `retry: 60000\nevent: screen\ndata: ${JSON.stringify(screen)}\n\n` });
  return true;
};

// stopped answers what the conversations read with the assistant's session stopped, so it can be resumed.
const stopped = () => {
  const targets = JSON.parse(goldens["conversation-targets"] ?? "{}") as { assistant: { conversation: Record<string, unknown> } };
  const conversation = { ...targets.assistant.conversation, actual_state: "stopped", desired_state: "stopped", worker_id: undefined };
  return reply(
    [/\/conversation-targets$/, { ...targets, assistant: { ...targets.assistant, conversation } }],
    [/\/conversations$/, { as_of: 1790000010000, conversations: [conversation], workers: [{ id: "worker-atlas", name: "atlas", available: true }] }],
  );
};

// addAgent is the path to the Add agent dialog of the roster, which a member fills from a pasted definition.
function addAgent(title: string): KeyPath["run"] {
  const scout = '[[agent]]\nname = "scout"\npurpose = "Finds the code a change touches."\nkind = "lookup"\naccess = "read_only"\nclass = "small"\neffort = "low"\n\n[agent.tools]\nallow = ["read", "search"]\n';
  return async (k, page) => {
    await k.palette(title);
    await page.getByRole("dialog", { name: "Add agent" }).waitFor();
    await k.activate(inDialog(page).getByRole("button", { name: "Paste TOML" }));
    await k.typeInto(inDialog(page).getByLabel(/Agent definition/), scout);
    await k.activate(inDialog(page).getByRole("button", { name: /^Save version/ }));
  };
}

// withArchived lists the project, and one that is archived beside it.
const withArchived = (route: Route) =>
  /\/v1\/projects$/.test(route.request().url()) &&
  json(route, {
    projects: [
      { id: "default", name: "default", display_name: "Default", state: "active", created_at: 1, repositories: [] },
      { id: "old", name: "old", display_name: "Old", state: "archived", created_at: 1, repositories: [] },
    ],
  });

// decide is the path to a card of Needs you: the palette's Decide action for the card, then the button that does what the
// card says, with the reason it asks for when it asks for one.
function decide(heading: string, press: string, reason?: string): KeyPath["run"] {
  return async (k, page) => {
    await k.palette(`Decide: ${heading}`);
    const button = page.getByRole("button", { name: press, exact: true });
    await button.waitFor();
    await k.activate(button);
    if (reason) {
      await k.typeInto(page.locator("#main").getByRole("textbox"), reason);
      await k.activate(page.getByRole("button", { name: press, exact: true }));
    }
  };
}

// startAnswers answers what Start a project from a template reads: a worker that takes a remote, the account its gh is signed in
// as, with an organization beside the login, and the request the dialog sends.
const startAnswers = () =>
  reply(
    [/\/v1\/workers$/, { workers: [{ id: "worker-1", name: "atlas", epoch: 1, protocol: 7 }] }],
    [/\/v1\/workers\/worker-1\/forge-account-reads$/, { id: "read-1", worker: "worker-1", requested_by: "member", requested_at: 1, expires_at: 4102444800000, forge: "github", state: "offered" }, "POST"],
    [
      /\/v1\/workers\/worker-1\/forge-account-reads\/read-1$/,
      {
        id: "read-1",
        worker: "worker-1",
        requested_by: "member",
        requested_at: 1,
        expires_at: 4102444800000,
        forge: "github",
        state: "reported",
        report: { request: "read-1", state: "ok", login: "ana", git_protocol: "ssh", owners: ["ana", "acme"] },
      },
    ],
    [
      /\/v1\/workers\/worker-1\/template-writes$/,
      { id: "request-1", worker: "worker-1", requested_by: "member", requested_at: 1, expires_at: 4102444800000, shape: "workspace", path: "/srv/code/harbor", state: "offered", registrable: false },
      "POST",
    ],
  );

// chooseCreateOnGitHub is the path from the palette through Several repositories, two names, and Create on GitHub, to the
// dialog with its owners read. A group of radios takes Tab to one and the arrows to the others.
async function chooseCreateOnGitHub(k: Keys, page: Page) {
  await k.palette("Start a project from a template");
  const start = inDialog(page);
  await page.waitForFunction(() => (document.querySelector("[role=dialog] select") as HTMLSelectElement | null)?.value === "worker-1");
  await k.tabTo(start.getByRole("radio", { name: /^One repository/ }));
  await k.press("ArrowDown");
  await k.typeInto(start.getByLabel("Folder", { exact: true }), "/srv/code/harbor");
  await k.typeInto(start.getByLabel("Project name", { exact: true }), "tidewater");
  await k.typeInto(start.getByRole("group", { name: "Code repository 1" }).getByLabel("Name", { exact: true }), "api");
  await k.activate(start.getByRole("button", { name: "Add", exact: true }));
  await k.typeInto(start.getByRole("group", { name: "Code repository 2" }).getByLabel("Name", { exact: true }), "web");
  await k.tabTo(start.getByRole("radio", { name: "None" }));
  await k.press("ArrowDown", "ArrowDown");
  await start.getByRole("group", { name: "Remote" }).getByLabel("Owner", { exact: true }).waitFor();
  return start;
}

const paths: Record<string, KeyPath> = {
  "POST /v1/projects": {
    sent: post(/^\/v1\/projects$/, expect.objectContaining({ name: "harbor" })),
    run: async (k, page) => {
      await k.palette("Create project");
      await k.typeInto(inDialog(page).getByLabel("Name", { exact: true }), "harbor");
      await k.press("Enter");
    },
  },
  "POST /v1/workers/{id}/forge-account-reads": {
    sent: post(/^\/v1\/workers\/worker-1\/forge-account-reads$/, { forge: "github" }),
    answers: startAnswers(),
    run: async (k, page) => {
      await chooseCreateOnGitHub(k, page);
    },
  },
  "POST /v1/workers/{id}/template-writes": {
    sent: post(/^\/v1\/workers\/worker-1\/template-writes$/, {
      shape: "workspace",
      path: "/srv/code/harbor",
      name: "tidewater",
      first_commit: true,
      remote: { create: { forge: "github", owner: "acme", visibility: "private" } },
      repositories: [
        { name: "api", new: true, create: { forge: "github", owner: "acme", visibility: "private" } },
        { name: "web", new: true, create: { forge: "github", owner: "acme", visibility: "private" } },
      ],
    }),
    answers: startAnswers(),
    run: async (k, page) => {
      const start = await chooseCreateOnGitHub(k, page);
      await k.tabTo(start.getByRole("group", { name: "Remote" }).getByLabel("Owner", { exact: true }));
      await k.choose("acme");
      await k.activate(start.getByRole("button", { name: "Write files" }));
      await start.getByText("Waiting for atlas").waitFor();
    },
  },
  "POST /v1/workers/tokens": {
    sent: post(/^\/v1\/workers\/tokens$/),
    // Only an installation in group mode offers a join token.
    answers: reply([/\/v1\/health$/, { status: "ok", revision: 1, mode: "group", version: "v0.1.0-alpha.16" }]),
    run: async (k, page) => {
      await k.palette("Create join token");
      await k.activate(inDialog(page).getByRole("button", { name: "Create token" }));
    },
  },
  "POST /v1/workers/tokens/{id}/revoke": {
    sent: post(/^\/v1\/workers\/tokens\/01TOKENID\/revoke$/),
    // Only an installation in group mode offers it, as it offers a join token.
    answers: reply(
      [/\/v1\/health$/, { status: "ok", revision: 1, mode: "group", version: "v0.1.0-alpha.16" }],
      [/\/v1\/workers\/tokens\/01TOKENID\/revoke$/, { id: "01TOKENID" }, "POST"],
    ),
    run: async (k, page) => {
      await k.palette("Revoke join token");
      await k.typeInto(inDialog(page).getByLabel("Token ID", { exact: true }), "01TOKENID");
      await k.activate(inDialog(page).getByRole("button", { name: "Revoke token" }));
      await page.getByText(/Revoked the join token/).waitFor();
    },
  },
  "PUT /v1/passphrase": {
    sent: { method: "PUT", path: /^\/v1\/passphrase$/, body: { current: "tin-roof-anvil-1", passphrase: "copper-kettle-moss-2" } },
    answers: reply([/\/v1\/passphrase$/, { sessions_ended: 1, delegates: 0 }, "PUT"]),
    run: async (k, page) => {
      await k.palette("Settings: Change passphrase");
      await k.typeInto(page.getByLabel("Current passphrase", { exact: true }), "tin-roof-anvil-1");
      await k.typeInto(page.getByLabel("New passphrase", { exact: true }), "copper-kettle-moss-2");
      await k.typeInto(page.getByLabel("New passphrase, again", { exact: true }), "copper-kettle-moss-2");
      await k.activate(page.getByRole("button", { name: "Change passphrase" }));
      await page.getByText(/Passphrase changed\. 1 other session ended/).waitFor();
    },
  },
  "POST /v1/sessions/end-all": {
    sent: post(/^\/v1\/sessions\/end-all$/),
    run: async (k, page) => {
      await k.palette("Sign out all sessions");
      await k.activate(inDialog(page).getByRole("button", { name: "Sign out everywhere" }));
    },
  },
  "POST /v1/delegates": {
    sent: post(/^\/v1\/delegates$/, expect.objectContaining({ label: "ci", project: "default" })),
    run: async (k, page) => {
      await k.palette("Create delegate");
      await k.typeInto(inDialog(page).getByLabel("Label"), "ci");
      await k.activate(inDialog(page).getByRole("button", { name: "Create", exact: true }));
    },
  },
  "POST /v1/projects/{project}/questions": {
    sent: post(/^\/v1\/projects\/default\/questions$/, expect.objectContaining({ text: "Why retry?" })),
    run: async (k, page) => {
      await dialog(k, page, "Record question");
      await k.type("Why retry?");
      await k.activate(inDialog(page).getByRole("button", { name: "Record question" }));
    },
  },
  "POST /v1/projects/{project}/categories": {
    sent: post(/^\/v1\/projects\/default\/categories$/, expect.objectContaining({ name: "release-planning" })),
    run: async (k, page) => {
      await dialog(k, page, "Add category");
      await k.type("release-planning");
      await k.activate(inDialog(page).getByRole("button", { name: "Add category" }));
    },
  },
  "POST /v1/projects/{project}/repositories": {
    sent: post(/^\/v1\/projects\/default\/repositories$/, expect.objectContaining({ dry_run: false })),
    answers: reply([/\/v1\/projects\/default\/repositories$/, JSON.parse(goldens["repository-dry-run"] ?? "{}"), "POST"]),
    run: async (k, page) => {
      await dialog(k, page, "Register repositories");
      await k.typeInto(inDialog(page).getByPlaceholder("/srv/code/harbor…"), "/work/api");
      await k.press("Enter");
      await k.activate(inDialog(page).getByRole("button", { name: /^Register/ }));
    },
  },
  "PATCH /v1/projects/{project}/repositories/{id}": {
    sent: { method: "PATCH", path: /^\/v1\/projects\/default\/repositories\/id-api$/ },
    run: async (k, page) => {
      await k.palette("Repositories", "Repositories");
      await k.palette("Change repository: api");
      await k.landed(page.getByLabel("Name", { exact: true }));
      await k.type("2");
      await k.activate(page.getByRole("button", { name: "Save", exact: true }));
    },
  },
  // The palette takes the keyboard to Run check on the Overview, and Enter presses it, for every repository.
  "POST /v1/projects/{project}/practices/checks": {
    sent: post(/^\/v1\/projects\/default\/practices\/checks$/, { repositories: [] }),
    answers: reply([/\/v1\/projects\/default\/practices\/checks$/, { checks: [] }, "POST"]),
    run: async (k, page) => {
      await k.palette("Run the practices check");
      await k.landed(page.getByRole("button", { name: "Run check", exact: true }));
      await k.press("Enter");
    },
  },
  // The dialog opens on its first field, the Repository picker, over the first repository with an open finding and its first open finding, and Tab reaches the Reason.
  "POST /v1/projects/{project}/practices/acknowledgements": {
    sent: post(/^\/v1\/projects\/default\/practices\/acknowledgements$/, { repository: "id-api", finding: "env-ignored:ignore:.", reason: "the folder is not a project root" }),
    run: async (k, page) => {
      await dialog(k, page, "Acknowledge a practices finding");
      await k.landed(inDialog(page).getByLabel("Repository", { exact: true }));
      await k.tabTo(inDialog(page).getByLabel("Reason", { exact: true }));
      await k.type("the folder is not a project root");
      await k.activate(inDialog(page).getByRole("button", { name: "Acknowledge", exact: true }));
    },
  },
  "POST /v1/projects/{project}/practices/acknowledgements/withdraw": {
    sent: post(/^\/v1\/projects\/default\/practices\/acknowledgements\/withdraw$/, { repository: "id-api", finding: "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f" }),
    run: async (k, page) => {
      await dialog(k, page, "Withdraw an acknowledgement");
      await k.activate(inDialog(page).getByRole("button", { name: "Withdraw acknowledgement", exact: true }));
    },
  },
  "POST /v1/projects/{project}/repositories/{id}/remove": {
    sent: post(/^\/v1\/projects\/default\/repositories\/id-api\/remove$/),
    run: async (k, page) => {
      await k.palette("Repositories", "Repositories");
      await dialog(k, page, "Remove repository: api");
      await k.typeInto(inDialog(page).getByRole("textbox"), "api");
      await k.activate(inDialog(page).getByRole("button", { name: /^Remove/ }).last());
    },
  },
  "POST /v1/adoption/plan": {
    sent: post(/^\/v1\/adoption\/plan$/, expect.objectContaining({ root: "/work/api", new_project: true })),
    answers: reply([/\/v1\/adoption\/plan$/, JSON.parse(goldens["adoption-plan-single"] ?? "{}"), "POST"]),
    run: async (k, page) => {
      await k.palette("Adopt a project");
      await k.typeInto(page.getByLabel("Folders", { exact: true }), "/work/api");
      await k.activate(page.getByRole("button", { name: "Run dry run" }));
    },
  },
  "POST /v1/projects/{project}/adoption/plan": {
    sent: post(/^\/v1\/projects\/default\/adoption\/plan$/, expect.objectContaining({ root: "/work/api" })),
    answers: reply(
      [/\/v1\/projects\/default\/adoption\/plan$/, JSON.parse(goldens["adoption-plan-single"] ?? "{}"), "POST"],
      [/\/v1\/projects\/default\/approvals\?state=approved$/, { approvals: [] }],
    ),
    run: async (k, page) => {
      await k.palette("Adopt a project");
      await k.typeInto(page.getByLabel("Folders", { exact: true }), "/work/api");
      // The first option of the list is a new project, and the project is the second.
      await k.tabTo(page.getByLabel("Project", { exact: true }));
      await k.choose("Default");
      await k.activate(page.getByRole("button", { name: "Run dry run" }));
    },
  },
  "POST /v1/projects/{project}/questions/{id}/answer": {
    sent: post(/^\/v1\/projects\/default\/questions\/question-1\/answer$/, expect.objectContaining({ answer: "Keep the budget" })),
    run: async (k, page) => {
      await k.palette("Open questions", "Open questions");
      await k.palette("Answer question: Should the budget hold across restarts?");
      await k.activate(page.getByRole("button", { name: "Send this answer" }));
    },
  },
  // The batch holds one question whose answer can be taken back, which starts chosen, and one that is one-way, which
  // waits for its own choice, so the one press sends the first in one request.
  "POST /v1/projects/{project}/batches/{id}/answers": {
    sent: post(/\/batches\/[^/]+\/answers$/, expect.objectContaining({ answers: [expect.objectContaining({ question: "question-1", accepted: true })] })),
    run: async (k, page) => {
      await k.palette("Decide: 2 questions");
      const accept = page.getByRole("button", { name: "Accept all", exact: true });
      await accept.waitFor();
      await k.activate(accept);
      const send = page.getByRole("button", { name: "Agree with 1 recommendation", exact: true });
      await send.waitFor();
      await k.activate(send);
    },
  },
  "POST /v1/projects/{project}/questions/{id}/dismiss": {
    sent: post(/^\/v1\/projects\/default\/questions\/question-1\/dismiss$/),
    run: async (k, page) => {
      await k.palette("Open questions", "Open questions");
      await dialog(k, page, "Dismiss question: Should the budget hold across restarts?");
      await k.activate(inDialog(page).getByRole("button", { name: "Dismiss question" }));
    },
  },
  // The member merges a question into the first other question that waits, in the orchestrator's place.
  "POST /v1/projects/{project}/questions/{id}/merge": {
    sent: post(/^\/v1\/projects\/default\/questions\/question-1\/merge$/, { into: "question-2" }),
    run: async (k, page) => {
      await k.palette("Open questions", "Open questions");
      await dialog(k, page, "Merge question: Should the budget hold across restarts?");
      await k.tabTo(inDialog(page).getByRole("radio").first());
      await k.press("Space");
      await k.activate(inDialog(page).getByRole("button", { name: "Merge question" }));
    },
  },
  "POST /v1/projects/{project}/questions/{id}/decide-now": {
    sent: post(/^\/v1\/projects\/default\/questions\/question-review\/decide-now$/),
    run: async (k) => {
      await k.palette("Open questions", "Open questions");
      await k.palette("Decide now: Does the plan cover a restart?");
    },
  },
  "POST /v1/projects/{project}/decisions/{id}/overturn": {
    sent: post(/^\/v1\/projects\/default\/decisions\/decision-3\/overturn$/, expect.objectContaining({ statement: "Retry twice, and no more." })),
    run: async (k, page) => {
      await k.palette("Decisions", "Decisions");
      await dialog(k, page, "Overturn decision: Retry until the budget runs out.");
      await k.type("Retry twice, and no more.");
      await k.activate(inDialog(page).getByRole("button", { name: "Overturn decision" }));
    },
  },
  "POST /v1/projects/{project}/work-orders/{id}/claims": {
    sent: post(/^\/v1\/projects\/default\/work-orders\/generated-28\/claims$/),
    answers: lane("generated-28", ["claim", "cancel"], "Write the guide"),
    run: async (k, page) => {
      await k.palette("Start work: Write the guide", "start work guide");
      await k.dialogRead();
      await k.activate(inDialog(page).getByRole("button", { name: "Start", exact: true }));
    },
  },
  "POST /v1/projects/{project}/work-orders/{id}/lanes": {
    sent: post(/^\/v1\/projects\/default\/work-orders\/wo-running\/lanes$/),
    answers: lane("wo-running", ["start_lane", "cancel"]),
    run: async (k, page) => {
      await dialog(k, page, "Start lane: Write the docs", "start lane docs");
      await k.activate(inDialog(page).getByRole("button", { name: "Start lane", exact: true }));
    },
  },
  "POST /v1/projects/{project}/work-orders/{id}/claims/{claim}/release": {
    sent: post(/^\/v1\/projects\/default\/work-orders\/wo-running\/claims\/[^/]+\/release$/),
    answers: lane("wo-running", ["release", "cancel"]),
    run: async (k, page) => {
      await k.palette("Release the claim: Write the docs", "release the claim docs");
      await k.activate(inDialog(page).getByRole("button", { name: "Release the claim", exact: true }));
    },
  },
  "POST /v1/projects/{project}/work-orders/{id}/lane-archive": {
    sent: post(/^\/v1\/projects\/default\/work-orders\/wo-done\/lane-archive$/),
    run: async (k, page) => {
      await k.palette("wo-done", "#wo-done");
      await dialog(k, page, "Archive the worktree of lane");
      await k.activate(inDialog(page).getByRole("button", { name: /^Archive/ }).last());
    },
  },
  "POST /v1/roster/changes": {
    sent: post(/^\/v1\/roster\/changes$/),
    run: addAgent("Installation agents: add an agent"),
  },
  "POST /v1/projects/{project}/roster/changes": {
    sent: post(/^\/v1\/projects\/default\/roster\/changes$/),
    run: addAgent("Add agent"),
  },
  "POST /v1/roster/revert": {
    sent: post(/^\/v1\/roster\/revert$/),
    answers: history(),
    run: async (k, page) => {
      await k.palette("Installation agents: restore a version");
      await k.activate(page.getByRole("button", { name: "Revert" }).first());
    },
  },
  "POST /v1/projects/{project}/roster/revert": {
    sent: post(/^\/v1\/projects\/default\/roster\/revert$/),
    answers: history(),
    run: async (k, page) => {
      await k.palette("Agents: restore a version");
      await k.activate(page.getByRole("button", { name: "Revert" }).first());
    },
  },
  "POST /v1/projects/{project}/panes/{id}/input": {
    sent: post(/^\/v1\/projects\/default\/panes\/lane-wo-running\/input$/, expect.objectContaining({ text: "y" })),
    answers: livePane,
    run: async (k) => {
      await k.palette("lane-wo-running", "#lane-wo-running");
      await k.palette("Send keys to the pane of lane lane-wo-running");
      await k.type("y");
      await k.press("Enter");
    },
  },
  "POST /v1/projects/{project}/conversations": {
    sent: post(/^\/v1\/projects\/default\/conversations$/),
    run: async (k, page) => {
      await k.palette("New assistant conversation");
      await k.landed(page.getByRole("textbox", { name: "Message to the assistant" }));
      await k.type("Hello there.");
      await k.press("Mod+Enter");
    },
  },
  "POST /v1/projects/{project}/conversations/{id}/messages": {
    sent: post(/^\/v1\/projects\/default\/conversations\/conv-1\/messages$/),
    run: async (k, page) => {
      await k.palette("Message the assistant");
      await k.landed(page.getByRole("textbox", { name: "Message to the assistant" }));
      await k.type("Hello again.");
      await k.press("Mod+Enter");
    },
  },
  "POST /v1/projects/{project}/messages": {
    sent: post(/^\/v1\/projects\/default\/messages$/, expect.objectContaining({ to: { lane: "lane-wo-1" }, body: "Go on." })),
    run: async (k, page) => {
      await k.palette("Open the conversation panel");
      await page.getByRole("complementary", { name: "Conversation" }).waitFor();
      await k.palette("Switch conversation target");
      await page.getByRole("menu", { name: "Switch conversation target" }).waitFor();
      await k.arrowTo(page.getByRole("menuitemradio", { name: /Lane.*lane-wo-1/ }));
      await k.press("Enter");
      await k.tabTo(page.getByRole("textbox", { name: "Message to lane lane-wo-1" }));
      await k.type("Go on.");
      await k.press("Mod+Enter");
    },
  },
  "POST /v1/projects/{project}/conversations/{id}/compact": {
    sent: post(/\/conversations\/conv-1\/compact$/),
    run: async (k) => {
      await k.palette("Go to Conversations");
      await k.palette("Compact the conversation's context");
    },
  },
  "POST /v1/projects/{project}/conversations/{id}/clear": {
    sent: post(/\/conversations\/conv-1\/clear$/),
    run: async (k) => {
      await k.palette("Go to Conversations");
      await k.palette("Clear the conversation's context");
    },
  },
  "POST /v1/projects/{project}/conversations/{id}/stop": {
    sent: post(/\/conversations\/conv-1\/stop$/),
    run: async (k) => {
      await k.palette("Go to Conversations");
      await k.palette("Stop the assistant session");
    },
  },
  "POST /v1/projects/{project}/conversations/{id}/rename": {
    sent: post(/\/conversations\/conv-1\/rename$/, expect.objectContaining({ name: "Retry work" })),
    run: async (k, page) => {
      await k.palette("Go to Conversations");
      await dialog(k, page, "Rename the conversation");
      await k.press("Mod+a");
      await k.type("Retry work");
      await k.activate(inDialog(page).getByRole("button", { name: "Rename", exact: true }));
    },
  },
  "POST /v1/projects/{project}/conversations/{id}/delete": {
    sent: post(/\/conversations\/conv-1\/delete$/),
    run: async (k, page) => {
      await k.palette("Go to Conversations");
      await dialog(k, page, "Delete the conversation");
      await k.activate(inDialog(page).getByRole("button", { name: /^Delete/ }).last());
    },
  },
  "POST /v1/projects/{project}/conversations/{id}/open": {
    sent: post(/\/conversations\/conv-1\/open$/),
    answers: stopped(),
    run: async (k) => {
      await k.palette("Go to Conversations");
      await k.palette("Resume the assistant session");
    },
  },
  "PUT /v1/projects/{project}/fleet/mode": {
    sent: { method: "PUT", path: /^\/v1\/projects\/default\/fleet\/mode$/, body: expect.objectContaining({ fleet_mode: "pause" }) },
    run: async (k, page) => {
      await k.palette("Pause or drain the fleet");
      await k.activate(page.getByRole("button", { name: "Pause fleet" }));
    },
  },
  "PUT /v1/projects/{project}/fleet/caps": {
    sent: { method: "PUT", path: /^\/v1\/projects\/default\/fleet\/caps$/ },
    run: async (k, page) => {
      await k.palette("Set the fleet's caps");
      await k.landed(page.getByLabel("Work in progress limit"));
      await k.press("Mod+a");
      await k.type("4");
      await k.activate(page.getByRole("button", { name: "Save", exact: true }));
    },
  },
  "PUT /v1/workers/{id}/caps": {
    sent: { method: "PUT", path: /^\/v1\/workers\/worker-1\/caps$/ },
    run: async (k, page) => {
      await k.palette("Settings: Admission and lane caps");
      await k.activate(page.getByRole("button", { name: "A number" }));
      await k.typeInto(page.getByLabel("Number of lanes"), "9");
      await k.activate(page.getByRole("button", { name: "Save", exact: true }));
    },
  },
  "PUT /v1/workers/{id}/autonomy": {
    sent: { method: "PUT", path: /^\/v1\/workers\/worker-1\/autonomy$/, body: expect.objectContaining({ level: "unattended" }) },
    answers: autonomy(),
    run: async (k, page) => {
      await k.palette("Settings: Autonomy and run windows");
      await k.activate(page.getByRole("button", { name: "unattended" }));
    },
  },
  "POST /v1/workers/{id}/windows": {
    sent: post(/^\/v1\/workers\/worker-1\/windows$/),
    answers: autonomy(),
    run: async (k, page) => {
      await k.palette("Settings: Autonomy and run windows");
      await k.activate(page.getByRole("button", { name: "Open window" }));
    },
  },
  "DELETE /v1/workers/{id}/windows": {
    sent: { method: "DELETE", path: /^\/v1\/workers\/worker-1\/windows$/ },
    answers: autonomy({ id: "window-1", worker_id: "worker-1", opened_by: "member-1", opened_at: 1790000000000, closes_at: 1790007200000 }),
    run: async (k, page) => {
      await k.palette("Settings: Autonomy and run windows");
      await k.activate(page.getByRole("button", { name: "Close window" }));
    },
  },
  "POST /v1/workers/{id}/autonomy/acknowledgement": {
    sent: post(/^\/v1\/workers\/worker-1\/autonomy\/acknowledgement$/),
    answers: autonomy(),
    run: async (k, page) => {
      await k.palette("Settings: Autonomy and run windows");
      await k.activate(page.getByRole("button", { name: "Read the stated risk" }));
      await k.activate(inDialog(page).getByRole("button", { name: "I stand behind it" }));
    },
  },
  "POST /v1/workers/{id}/autonomy/one-way-acknowledgement": {
    sent: post(/^\/v1\/workers\/worker-1\/autonomy\/one-way-acknowledgement$/),
    answers: autonomy(),
    run: async (k, page) => {
      await k.palette("Settings: Autonomy and run windows");
      await k.activate(page.getByRole("button", { name: "Accept the one-way acknowledgement" }));
    },
  },
  "DELETE /v1/workers/{id}/autonomy/one-way-acknowledgement": {
    sent: { method: "DELETE", path: /^\/v1\/workers\/worker-1\/autonomy\/one-way-acknowledgement$/ },
    answers: autonomy(undefined, { worker_id: "worker-1", by: "member-1", digest: "sha256:def", at: 1790000000000 }),
    run: async (k, page) => {
      await k.palette("Settings: Autonomy and run windows");
      await k.activate(page.getByRole("button", { name: "Withdraw the one-way acknowledgement" }));
    },
  },
  "POST /v1/projects/{project}/policies": {
    sent: post(/^\/v1\/projects\/default\/policies$/),
    run: async (k, page) => {
      await k.palette("Settings: Estimate policy");
      await k.replaceIn(page.getByLabel(/Comparable outcomes a confident estimate needs/), "15");
      await k.activate(page.getByRole("button", { name: "Review change" }));
      await k.activate(inDialog(page).getByRole("button", { name: /^Save version/ }));
    },
  },
  "POST /v1/projects/{project}/rename": {
    sent: post(/^\/v1\/projects\/default\/rename$/, expect.objectContaining({ name: "harbor" })),
    run: async (k, page) => {
      await dialog(k, page, "Rename project: Default");
      await k.press("Mod+a");
      await k.type("harbor");
      await k.activate(inDialog(page).getByRole("button", { name: "Rename", exact: true }));
    },
  },
  "POST /v1/projects/{project}/archive": {
    sent: post(/^\/v1\/projects\/default\/archive$/),
    run: async (k, page) => {
      await dialog(k, page, "Archive project: Default");
      await k.activate(inDialog(page).getByRole("button", { name: "Archive", exact: true }));
    },
  },
  "POST /v1/projects/{project}/restore": {
    sent: post(/^\/v1\/projects\/old\/restore$/),
    answers: withArchived,
    run: async (k, page) => {
      await k.palette("Restore project: Old");
      await k.activate(page.getByRole("button", { name: "Restore", exact: true }));
    },
  },
  "POST /v1/projects/{project}/remove": {
    sent: post(/^\/v1\/projects\/old\/remove$/),
    answers: withArchived,
    run: async (k, page) => {
      await dialog(k, page, "Remove project: Old");
      await k.typeInto(inDialog(page).getByLabel("Type old to confirm"), "old");
      await k.activate(inDialog(page).getByRole("button", { name: "Remove project", exact: true }));
    },
  },
  "POST /v1/projects/{project}/backup/download": {
    sent: post(/^\/v1\/projects\/default\/backup\/download$/),
    run: async (k, page) => {
      await k.palette("Back up project: Default");
      await k.activate(page.getByRole("button", { name: "Back up", exact: true }));
    },
  },
  "POST /v1/installation/backup/download": {
    sent: post(/^\/v1\/installation\/backup\/download$/),
    run: async (k, page) => {
      await k.palette("Back up the installation");
      await k.activate(page.getByRole("button", { name: "Back up installation" }));
    },
  },
  "POST /v1/projects/import/upload": {
    sent: post(/^\/v1\/projects\/import\/upload$/),
    run: async (k, page) => {
      await k.palette("Import a project from a backup");
      // The button opens the browser's file chooser, which a script answers: choosing a file is the browser's own.
      const chosen = page.waitForEvent("filechooser");
      await k.activate(page.getByRole("button", { name: "Import backup", exact: true }));
      await (await chosen).setFiles({ name: "tidewater.db", mimeType: "application/octet-stream", buffer: Buffer.from("SQLite format 3\0") });
      await page.getByRole("dialog", { name: "Import backup" }).waitFor();
      await k.activate(inDialog(page).getByRole("button", { name: "Import project" }));
    },
  },
  "PUT /v1/workers/{id}/orchestrator": {
    sent: { method: "PUT", path: /^\/v1\/workers\/[^/]+\/orchestrator$/ },
    run: async (k, page) => {
      await k.palette("Start orchestrator");
      await k.activate(page.getByRole("button", { name: "Start orchestrator" }).first());
    },
  },
  // A worker whose sign-in is at its usage limit: Start schedules the start for the reset, and a scheduled one cancels.
  "POST /v1/workers/{id}/orchestrator/schedule": {
    sent: post(/^\/v1\/workers\/worker-a\/orchestrator\/schedule$/),
    answers: reply([/\/views\/shop-floor/, limitedFloor()]),
    run: async (k, page) => {
      await k.palette("Workers and resources", "Workers");
      await k.activate(page.getByRole("button", { name: "Start at reset on solo" }));
    },
  },
  "DELETE /v1/workers/{id}/orchestrator/schedule": {
    sent: { method: "DELETE", path: /^\/v1\/workers\/worker-a\/orchestrator\/schedule$/ },
    answers: reply([/\/views\/shop-floor/, limitedFloor(true)]),
    run: async (k, page) => {
      await k.palette("Workers and resources", "Workers");
      await k.activate(page.getByRole("button", { name: "Cancel the scheduled start on solo" }));
    },
  },
  "POST /v1/delegates/{id}/revoke": {
    sent: post(/^\/v1\/delegates\/[^/]+\/revoke$/),
    run: async (k, page) => {
      await k.palette("Revoke a delegate");
      await k.activate(page.getByRole("button", { name: /^Revoke/ }).first());
    },
  },
  "POST /v1/delegates/{id}/remove": {
    sent: post(/^\/v1\/delegates\/[^/]+\/remove$/),
    run: async (k, page) => {
      await k.palette("Remove a delegate");
      await k.activate(page.getByRole("button", { name: /^Remove from list/ }).first());
    },
  },
  "POST /v1/projects/{project}/decisions": {
    sent: post(/^\/v1\/projects\/default\/decisions$/, expect.objectContaining({ statement: "Retry twice." })),
    run: async (k, page) => {
      await dialog(k, page, "Record decision");
      await k.typeInto(inDialog(page).getByLabel("Statement"), "Retry twice.");
      await k.activate(inDialog(page).getByRole("button", { name: "Review" }));
      await k.activate(inDialog(page).getByRole("button", { name: "Record decision" }));
    },
  },
  "POST /v1/projects/{project}/plans/{id}/reviews": {
    sent: post(/^\/v1\/projects\/default\/plans\/generated-2\/reviews$/),
    answers: reply([/\/v1\/projects\/default\/plans$/, { plans: [{ id: "generated-2", title: "Add the retry budget", status: "active" }] }]),
    run: async (k, page) => {
      await dialog(k, page, "Start contract review");
      await k.dialogRead();
      await k.press("ArrowDown");
      await k.activate(inDialog(page).getByRole("button", { name: "Start contract review" }));
    },
  },
  "POST /v1/projects/{project}/work-orders": {
    sent: post(/^\/v1\/projects\/default\/work-orders$/, expect.objectContaining({ title: "Write the guide" })),
    run: async (k, page) => {
      await dialog(k, page, "New work order");
      await k.type("Write the guide");
      await k.typeInto(inDialog(page).getByLabel("Criteria"), "The guide builds.");
      await k.activate(inDialog(page).getByRole("button", { name: "Create work order" }));
    },
  },
  "POST /v1/projects/{project}/approvals/{id}/decide": {
    sent: post(/^\/v1\/projects\/default\/approvals\/approval-1\/decide$/, expect.objectContaining({ allow: true })),
    run: decide("Allow Bash?", "Allow this one call"),
  },
  "POST /v1/projects/{project}/effects/{id}/decide": {
    sent: post(/^\/v1\/projects\/default\/effects\/[^/]+\/decide$/, expect.objectContaining({ outcome: "happened" })),
    run: decide("Did it happen?", "It happened"),
  },
  "POST /v1/projects/{project}/work-orders/{id}/accept": { sent: post(/\/work-orders\/wo-accept\/accept$/), run: decide("Trim the log", "Accept") },
  "POST /v1/projects/{project}/work-orders/{id}/rework": { sent: post(/\/work-orders\/wo-accept\/rework$/, expect.objectContaining({ reason: "The log drops a day." })), run: decide("Trim the log", "Send back for rework", "The log drops a day.") },
  "POST /v1/projects/{project}/work-orders/{id}/reassign": { sent: post(/\/work-orders\/wo-lapsed\/reassign$/), run: decide("Rotate the keys", "End this claim", "The worker is gone.") },
  "POST /v1/projects/{project}/work-orders/{id}/cancel": { sent: post(/\/work-orders\/wo-lapsed\/cancel$/), run: decide("Rotate the keys", "Cancel this work", "No longer needed.") },
  "POST /v1/projects/{project}/work-orders/{id}/resolve": { sent: post(/\/work-orders\/wo-blocked\/resolve$/), run: decide("Move the archive", "Back to ready", "The block is gone.") },
  "POST /v1/projects/{project}/work-orders/{id}/replan": { sent: post(/\/work-orders\/wo-blocked\/replan$/), run: decide("Move the archive", "Back to planning", "The plan was wrong.") },
  "POST /v1/projects/{project}/work-orders/{id}/lane-resume": { sent: post(/\/work-orders\/wo-parked\/lane-resume$/), run: decide("Wait for the check", "Resume") },
  "POST /v1/projects/{project}/work-orders/{id}/lane-respawn": { sent: post(/\/work-orders\/wo-stalled\/lane-respawn$/), run: decide("Read the logs", "Start the next generation") },
  "POST /v1/projects/{project}/work-orders/{id}/lane-reset": { sent: post(/\/work-orders\/wo-tripped\/lane-reset$/), run: decide("Fix the flaky test", "Reset the breaker") },
  "POST /v1/projects/{project}/work-orders/{id}/retry": { sent: post(/\/work-orders\/wo-failed\/retry$/), run: decide("Try the impossible", "Retry with an extra attempt", "The check was flaky.") },
  "POST /v1/projects/{project}/work-orders/{id}/review": { sent: post(/\/work-orders\/wo-review\/review$/), run: decide("Document the retry budget", "Pass the review") },
  "POST /v1/projects/{project}/work-orders/{id}/send-back": {
    sent: post(/\/work-orders\/wo-review\/send-back$/),
    run: decide("Document the retry budget", "Send back to planning", "The contract is unclear."),
  },
  "POST /v1/projects/{project}/plans/{id}/revision/confirm": {
    sent: post(/\/plans\/plan-revise\/revision\/confirm$/),
    run: decide("Retry budget needs revision", "Confirm version 2"),
  },
  "POST /v1/projects/{project}/plans/{id}/revision/sessions": {
    sent: post(/\/plans\/plan-revise\/revision\/sessions$/, expect.objectContaining({ id: expect.any(String) })),
    run: async (k, page) => {
      await k.palette("Decide: Retry budget needs revision");
      const open = page.getByRole("button", { name: "Revise the plan", exact: true });
      await open.waitFor();
      await k.activate(open);
      await k.dialogRead();
      await k.activate(inDialog(page).getByRole("button", { name: "Start revision session", exact: true }));
    },
  },
  "POST /v1/projects/{project}/work-orders/{id}/decision-rework": {
    sent: post(/\/work-orders\/wo-built\/decision-rework$/, expect.objectContaining({ decisions: ["d-logo"] })),
    run: decide("Retry budget needs revision", "Create rework for Add the budget"),
  },
  "POST /v1/projects/{project}/work-orders/{id}/ci-wait": {
    sent: post(/\/work-orders\/wo-ci\/ci-wait$/, {}),
    run: decide("Add the retry budget (ci_not_finished)", "Wait longer"),
  },
  "POST /v1/projects/{project}/work-orders/{id}/ci-rerun": {
    sent: post(/\/work-orders\/wo-ci\/ci-rerun$/),
    run: decide("Add the retry budget (ci_not_finished)", "Rerun"),
  },
  "POST /v1/projects/{project}/approvals/{id}/repositories/{name}/retry": {
    sent: post(/\/approvals\/generated-1\/repositories\/ghost\/retry$/, {}),
    run: async (k, page) => {
      await k.palette("Retry creating ghost", "retry creating");
      const retry = page.getByRole("button", { name: "Retry", exact: true });
      await k.landed(retry);
      await k.press("Enter");
    },
  },
  "POST /v1/projects/{project}/approvals/{id}/repositories/{name}/skip": {
    sent: post(/\/approvals\/generated-1\/repositories\/ghost\/skip$/, { reason: "built by hand" }),
    run: async (k, page) => {
      await k.palette("Skip creating ghost", "skip creating");
      await k.landed(page.getByRole("button", { name: "Skip", exact: true }));
      await k.press("Enter");
      await page.getByRole("dialog", { name: "Leave ghost uncreated?" }).waitFor();
      await k.typeInto(inDialog(page).getByLabel(/^Reason/), "built by hand");
      await k.activate(inDialog(page).getByRole("button", { name: "Skip repository" }));
    },
  },
  "POST /v1/projects/{project}/work-orders/{id}/spend/raise": {
    sent: post(/\/work-orders\/wo-stop\/spend\/raise$/, { by_percent: 50 }),
    run: decide("Add the retry budget (spend stop)", "Raise by 50 percent"),
  },
  "POST /v1/projects/{project}/work-orders/{id}/spend/resume": {
    sent: post(/\/work-orders\/wo-stop\/spend\/resume$/, {}),
    run: decide("Add the retry budget (spend stop)", "Resume once"),
  },
  "POST /v1/projects/{project}/work-orders/{id}/spend/block": {
    sent: post(/\/work-orders\/wo-stop\/spend\/block$/, {}),
    run: decide("Add the retry budget (spend stop)", "Block"),
  },
  "POST /v1/projects/{project}/work-orders/{id}/spend/replan": {
    sent: post(/\/work-orders\/wo-stop\/spend\/replan$/, {}),
    run: decide("Add the retry budget (spend stop)", "Send to replanning"),
  },
  "POST /v1/projects/{project}/work-orders/{id}/lane-settings": {
    sent: post(/\/work-orders\/wo-hooked\/lane-settings$/, { lane: "lane-wo-hooked", digest: "sha256:hooked", choice: "base" }),
    run: decide("Lane lane-wo-hooked changed its own hooks/permissions", "Run with the repository's settings (base)"),
  },
  "POST /v1/projects/{project}/workers/{id}/personal-setup/acceptances": {
    sent: post(/\/workers\/worker-a\/personal-setup\/acceptances$/, { version: "2.1.330", kind: "mcp_server", name: "github" }),
    run: decide("Your MCP server github reached lanes on worker-a", "Accept it for this machine's lanes"),
  },
};

const missing = changing.filter((name) => !(name in paths));

describe("the keyboard paths", () => {
  it("cover each route that changes something and has a control", () => {
    expect(missing, "a changing route with no keyboard path").toEqual([]);
    expect(
      Object.keys(paths).filter((name) => !changing.includes(name)),
      "a path for a route that is not one of them",
    ).toEqual([]);
  });

  it("start from a palette action that the palette table names, wherever the table names one", () => {
    expect(Object.keys(paths).filter((name) => !(name in paletteActions))).toEqual([]);
  });

  it("use only the keyboard: no click, no field filled, no pointer, and no script that stands in for a key", () => {
    const pointer = /\.(click|dblclick|fill|check|uncheck|selectOption|hover|tap|dragTo|setInputFiles|dispatchEvent|focus|blur)\(|\bmouse\b|\btouchscreen\b/;
    for (const name of ["keyboard.test.ts", "keyboardTesting.ts"]) {
      const code = readFileSync(join(import.meta.dirname, name), "utf8")
        .split("\n")
        .filter((line) => !line.trim().startsWith("//"))
        .join("\n");
      expect(code.match(new RegExp(pointer, "g")) ?? [], name).toEqual([]);
    }
  });
});

const opened = await openBrowser();
const suite = suiteFor(opened);

let server: ViteDevServer;
let browser: Browser;
let base = "";

beforeAll(async () => {
  if (opened.kind !== "ready") {
    return;
  }
  browser = opened.browser;
  ({ server, base } = await serveApp());
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
}, 60_000);

suite("the repository creation card", () => {
  it(
    "is reached with J and K, and A and Tab reach Skip and Retry in order",
    async () => {
      const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: "light", reducedMotion: "reduce" });
      await context.route(/\/v1\//, serveChanges([]));
      const page = await context.newPage();
      await page.goto(`${base}/needs-you?kind=cards`);
      await ready(page);
      const keys = new Keys(page);
      const card = page.getByRole("article", { name: "ghost was not created" });
      // J and K move through the list, and select as they go: the item's card shows once the keyboard is on its row.
      const row = page.getByRole("link", { name: /^ghost was not created/ });
      await keys.activate(row);
      await card.waitFor();
      await keys.press("j", "k");
      await card.waitFor();
      // A moves to the card's primary button, Retry, and Shift+Tab goes back to Skip, the safe choice before it.
      await keys.press("a");
      await keys.landed(card.getByRole("button", { name: "Retry", exact: true }));
      await keys.press("Shift+Tab");
      await keys.landed(card.getByRole("button", { name: "Skip", exact: true }));
      await keys.press("Tab");
      await keys.landed(card.getByRole("button", { name: "Retry", exact: true }));
      await context.close();
    },
    60_000,
  );
});

suite("each keyboard path, from the default view,", () => {
  it.each(Object.entries(paths))(
    "reaches the control of %s and sends its request",
    async (_name, path) => {
      const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: "light", reducedMotion: "reduce" });
      const sent: Call[] = [];
      await context.route(/\/v1\//, serveChanges(sent, path.answers));
      const page = await context.newPage();
      await page.goto(`${base}/`);
      await ready(page);
      await path.run(new Keys(page), page);
      await expect.poll(() => sent.some((call) => matches(call, path.sent)), { timeout: 10_000, message: `${path.sent.method} ${String(path.sent.path)} among ${sent.map((call) => `${call.method} ${call.path} ${JSON.stringify(call.body)}`).join("; ") || "no change"}` }).toBe(true);
      await context.close();
    },
    60_000,
  );
});
