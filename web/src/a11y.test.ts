// @vitest-environment node
import { readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";

import type { Browser, Page, Route } from "playwright-core";
import { chromium } from "playwright-core";
import type { ViteDevServer } from "vite";
import { createServer } from "vite";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

// The accessibility checks: axe-core in a real browser, over every view of the application, in both
// themes, at desktop and at phone width, on what the Go tests wrote from a real ledger. They run with
// the WCAG 2.0, 2.1, and 2.2 A and AA rules, and any violation fails the build. The browser is the
// installed Google Chrome, which continuous integration's runners carry.

const require = createRequire(import.meta.url);
const axeSource = readFileSync(require.resolve("axe-core/axe.min.js"), "utf8");
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22a", "wcag22aa"];

const goldens = Object.fromEntries(
  readdirSync("testdata")
    .filter((name) => name.endsWith(".json"))
    .map((name) => [name.replace(/\.json$/, ""), readFileSync(join("testdata", name), "utf8")]),
);

// What the conversation surface reads: what the member can talk to, with a planning session on a pane, a
// headless lane, and an interactive lane beside the stopped orchestrator the ledger wrote.
const talkTargets = JSON.stringify({
  ...JSON.parse(goldens["conversation-targets"] ?? "{}"),
  planning: [
    { lane: "lane-wo-running", work_order: "wo-plan", title: "Plan alerts", kind: "plan", worker: "worker-a", worker_name: "solo", generation: 1, run_mode: "tmux", state: "working" },
  ],
  lanes: [
    { lane: "lane-wo-1", work_order: "wo-1", title: "Add the retry budget", kind: "change", worker: "worker-a", worker_name: "solo", generation: 1, run_mode: "headless", state: "working" },
    { lane: "lane-wo-pane", work_order: "wo-2", title: "Highlight spans", kind: "change", worker: "worker-a", worker_name: "solo", generation: 1, run_mode: "tmux", state: "working" },
  ],
});

// Two merge requests, as the ledger holds them: the merge of a reviewed commit to approve at person_approves, whose
// work order has a page, and a merge the forge refused, held until a member decides it.
function mergeRequest(subject: "approve" | "held", workOrder: string) {
  const commit = "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432";
  const detail = {
    work_order_id: workOrder,
    repository: "harbor-web",
    commit,
    branch: "ropeshift/" + workOrder,
    base: "main",
    pull_request: 12,
    url: "https://github.com/acme/harbor-web/pull/12",
    method: "squash",
    merge_authority: "person_approves",
    ...(subject === "held" ? { reason: "the base branch requires 2 approving reviews", request: "merge:" + workOrder + ":1" } : {}),
  };
  const id = `merge-${subject}`;
  return {
    kind: "approval",
    id,
    since: 1790000500000,
    approval: {
      id,
      lane_id: "",
      work_order_id: workOrder,
      generation: 0,
      kind: "merge",
      subject,
      subject_digest: commit,
      summary:
        subject === "held"
          ? `The merge of pull request #12, commit 9f8e7d6 of work order ${workOrder} (Add the retry budget), cannot go on: ${detail.reason}. Approve to try it again, or deny to send the work back for rework.`
          : `Approve the merge of pull request #12, commit 9f8e7d6 of work order ${workOrder} (Add the retry budget), at person_approves. The review's verdicts are on the work order.`,
      requested_by: "system:integration",
      state: "pending",
      raised_at: 1790000500000,
      detail: JSON.stringify(detail),
    },
  };
}

const projects = JSON.stringify({ projects: [{ id: "default", name: "default", display_name: "Default", state: "active", created_at: 1, repositories: [] }] });

// Two projects the ledger holds nothing in, for the views that have an empty state: "quiet" has no repository
// either, and "fresh" has one. Every read model of either lists nothing and counts nothing.
function emptyViews(): Record<string, unknown> {
  const stamp = { as_of: 1790000964002, revision: 1 };
  const measured = JSON.parse(goldens.operations ?? "{}") as Record<string, any>;
  const nothing = { numerator: 0, denominator: 0, value: null };
  const agreement = { decisions: 0, agreed: 0, disagreed: 0, unknown: 0, agreement_rate: nothing, approvals_overturned: 0, later_defects: 0, failures_overridden: 0 };
  Object.assign(measured, { work_orders: {}, lanes: {}, workers: 0, workers_fresh: 0, waiting_for_a_person: 0 });
  Object.assign(measured.measures, {
    interruptions: 0,
    completed: 0,
    interruptions_per_completed: nothing,
    overturn_rate: nothing,
    rework: nothing,
    cost_per_outcome: {},
    review_agreement: { workspace: { last_30_days: agreement, all_time: agreement }, repositories: [] },
  });
  return {
    "needs-you": { ...stamp, items: [] },
    completed: { ...stamp, items: [] },
    "in-progress": { ...stamp, items: [] },
    "on-hold": { ...stamp, items: [] },
    stuck: { ...stamp, items: [] },
    plans: { ...stamp, plans: [], unplanned: [], work_actions: {} },
    roadmap: { ...stamp, goals: [] },
    "shop-floor": { ...stamp, workers: [], lanes: [] },
    operations: measured,
  };
}
const empty = emptyViews();

// answer stands where the daemon stands, as `serveViews` does for the other tests.
async function answer(route: Route): Promise<void> {
  const url = new URL(route.request().url());
  const path = url.pathname.replace(/^\/v1\/projects\/[^/]+/, "/v1");
  const json = (body: string, status = 200) => route.fulfill({ status, contentType: "application/json", body });
  if (url.pathname === "/v1/projects") {
    return json(projects);
  }
  if (path === "/v1/needs-you") {
    // Needs you with the confirmation cards a delegate prepared, and the merge requests, beside every other kind.
    const needs = JSON.parse(goldens["needs-you"] ?? "{}") as { items: unknown[] };
    const cards = JSON.parse(goldens["needs-you-cards"] ?? "{}") as { items: unknown[] };
    return json(JSON.stringify({ ...needs, items: [...needs.items, ...cards.items, mergeRequest("approve", "wo-running"), mergeRequest("held", "wo-held")] }));
  }
  const view = /^\/v1\/views\/([a-z-]+)$/.exec(path)?.[1];
  const project = /^\/v1\/projects\/([^/]+)\//.exec(url.pathname)?.[1];
  // The projects with nothing in them answer every read model, the repositories, and the audit with nothing.
  if (project === "quiet" || project === "fresh") {
    if (view && view in empty) {
      return json(JSON.stringify(empty[view]));
    }
    if (path === "/v1/repositories") {
      return json(project === "quiet" ? JSON.stringify({ repositories: [] }) : (goldens.repositories ?? "{}"));
    }
    if (path === "/v1/audit") {
      return json(JSON.stringify({ scope: "project", as_of: 1790000964002, head: 0, next: 0, events: [] }));
    }
  }
  // The project that has work in review: a change the review could not judge, one whose merge waits for the
  // member's approval with a check running, and one whose merge the member approved.
  if (project === "reviewing" && (view === "completed" || view === "needs-you") && goldens[`reviews-${view}`]) {
    return json(goldens[`reviews-${view}`] ?? "{}");
  }
  // Its member has decided twelve times after a review, so its agreement shows a rate.
  if (project === "reviewing" && view === "operations") {
    const measured = JSON.parse(goldens.operations ?? "{}") as Record<string, any>;
    const { workspace } = measured.measures.review_agreement;
    workspace.last_30_days = { decisions: 12, agreed: 9, disagreed: 3, unknown: 0, agreement_rate: { numerator: 9, denominator: 12, value: 0.75 }, approvals_overturned: 2, later_defects: 1, failures_overridden: 1 };
    return json(JSON.stringify(measured));
  }
  if (view && goldens[view]) {
    return json(goldens[view]);
  }
  // What a work order's Changes and Activity tabs read: work that is running, work whose review ruled and waits
  // for a member, work that ended with a delivery record, and work that never reached ready.
  const evidence = /^\/v1\/work-orders\/([^/]+)\/(acceptance|timeline)$/.exec(path);
  if (evidence) {
    const read = ({ "wo-running": "running", "wo-accept": "reviewed", "wo-done": "ended", "wo-loose": "empty" } as Record<string, string | undefined>)[evidence[1] ?? ""];
    const body = read ? goldens[`${evidence[2]}-${read}`] : undefined;
    return body ? json(body) : json(JSON.stringify({ error: { code: "not_found", message: "no such work order" } }), 404);
  }
  if (path === "/v1/documents/read") {
    return json(goldens.document ?? "{}");
  }
  if (path === "/v1/documents/versions") {
    return json(goldens["document-versions"] ?? "{}");
  }
  if (path === "/v1/documents/recorded") {
    return json(goldens["recorded-document"] ?? "{}");
  }
  // What the projects, repositories, settings, agents, members, and audit pages read.
  const admin: Record<string, string | undefined> = {
    "/v1/repositories": goldens.repositories,
    "/v1/fleet": goldens.fleet,
    "/v1/policies": goldens["policies-recovery"],
    "/v1/roster": goldens.roster,
    "/v1/roster/history": goldens["roster-history"],
    "/v1/members": goldens.members,
    "/v1/audit": goldens.audit,
    "/v1/workers": JSON.stringify({ workers: [{ id: "worker-1", name: "atlas", manifest: { capacity: { max_lanes: 6 } } }] }),
    "/v1/workers/worker-1/caps": goldens["worker-caps"],
    "/v1/categories": JSON.stringify({ categories: ["security", "data"] }),
  };
  // What the delegates panel, the delegate policy, and the audit's delegate rows read.
  if (path === "/v1/policies" && url.searchParams.get("kind") === "delegate") {
    return json(goldens["policies-delegate"] ?? "{}");
  }
  if (path === "/v1/policies" && url.searchParams.get("kind") === "estimate") {
    return json(goldens["policies-estimate"] ?? "{}");
  }
  if (path === "/v1/audit" && url.searchParams.get("actor") === "delegate") {
    return json(goldens["audit-delegates"] ?? "{}");
  }
  const delegates: Record<string, string | undefined> = { "/v1/delegates": goldens.delegates, "/v1/operations": goldens["operation-classes"] };
  if (delegates[path]) {
    return json(delegates[path] ?? "{}");
  }
  if (admin[path]) {
    return json(admin[path] ?? "{}");
  }
  // A plan, a request to approve one that has no plan yet, the outline of a planning lane, and the sources the
  // daemon reads.
  if (/^\/v1\/plans\/[^/]+\/forecast$/.test(path) && goldens.forecast) {
    return json(goldens.forecast);
  }
  if (path === "/v1/measures/delivery") {
    return json((url.searchParams.get("by") === "size" ? goldens["delivery-size"] : goldens.delivery) ?? "{}");
  }
  if (path === "/v1/measures/estimates") {
    return json(goldens.calibration ?? "{}");
  }
  if (path.startsWith("/v1/plans/adopt-approval")) {
    return json(JSON.stringify({ error: { code: "not_found", message: "no such plan" } }), 404);
  }
  if (path.startsWith("/v1/plans/") && goldens.plan) {
    return json(goldens.plan);
  }
  if (path === "/v1/approvals/adopt-approval" && goldens["approval-adopted"]) {
    return json(goldens["approval-adopted"]);
  }
  if (/^\/v1\/lanes\/[^/]+\/outline$/.test(path) && goldens.outline) {
    return json(goldens.outline);
  }
  if (path === "/v1/sources") {
    return json(JSON.stringify({ sources: [] }));
  }
  // What each channel was told, what was sent to a lane, and what each lane reported of its tokens and cost.
  if (path === "/v1/notices" && goldens.notices) {
    return json(goldens.notices);
  }
  if (/^\/v1\/lanes\/[^/]+\/messages$/.test(path) && goldens["lane-messages"]) {
    return json(goldens["lane-messages"]);
  }
  if (path === "/v1/telemetry/lanes" && goldens["telemetry-lanes"]) {
    return json(goldens["telemetry-lanes"]);
  }
  if (/^\/v1\/workers\/[^/]+\/orchestrator$/.test(path)) {
    return json(
      JSON.stringify({ id: "orchestrator-worker-a", worker_id: "worker-a", desired_state: "running", run: 4, resets: 2, actual_state: "tripped", current_generation: 3, conditions: ["day_capped"], reported_at: 1790000000000 }),
    );
  }
  if (path === "/v1/conversation-targets") {
    return json(talkTargets);
  }
  if (path === "/v1/conversations" && goldens.conversations) {
    return json(goldens.conversations);
  }
  if (/^\/v1\/conversations\/[^/]+\/entries$/.test(path) && goldens["conversation-entries"]) {
    return json(goldens["conversation-entries"]);
  }
  if (/^\/v1\/lanes\/[^/]+\/stream$/.test(path) && goldens["lane-stream"]) {
    return json(goldens["lane-stream"]);
  }
  if (/^\/v1\/orchestrators\/[^/]+\/stream$/.test(path) && goldens["orchestrator-stream"]) {
    return json(goldens["orchestrator-stream"]);
  }
  if (path === "/v1/events" || path.startsWith("/v1/panes/")) {
    return route.fulfill({ status: 200, contentType: "text/event-stream", body: ": open\n\n" });
  }
  return json(JSON.stringify({ error: { code: "not_found", message: "not served in this test" } }), 404);
}

// The address of every view, each with the records a person would open.
const views = [
  "/needs-you",
  "/needs-you?item=questions%3Agenerated-7",
  "/needs-you?item=plan_approval%3Agenerated-13",
  "/needs-you?item=work%3Awo-failed",
  "/needs-you?kind=cards&item=confirm%3Agenerated-1",
  "/needs-you?kind=cards&item=confirm%3Agenerated-2",
  "/needs-you?item=approval%3Amerge-approve",
  "/needs-you?item=approval%3Amerge-held",
  "/projects/default",
  "/projects/quiet",
  "/projects/fresh",
  "/projects/default/reviews",
  "/projects/default/reviews?tab=pull-requests",
  "/projects/default/reviews?tab=agreement",
  "/projects/reviewing/reviews",
  "/projects/reviewing/reviews?tab=pull-requests",
  "/projects/reviewing/reviews?tab=agreement",
  "/projects/fresh/reviews",
  "/projects/fresh/reviews?tab=pull-requests",
  "/projects/fresh/reviews?tab=agreement",
  "/projects/default/roadmap",
  "/projects/default/plans",
  "/projects/default/plans?plan=generated-2",
  "/projects/default/plan?id=generated-1&version=6",
  "/projects/default/plan?id=generated-1&version=5",
  "/projects/default/plan?id=adopt-approval&version=1",
  "/projects/default/planning",
  "/projects/default/planning?lane=lane-wo-running",
  "/projects/default/documents?kind=requirements",
  "/projects/default/documents?kind=spec",
  "/projects/default/documents?id=spec%3Afactory-api",
  "/projects/default/documents?id=spec%3Afactory-api&version=2&against=1",
  "/projects/default/documents?kind=requirements&key=FAC-2",
  "/projects/default/documents?path=docs%2Fspecs%2Fretry.md",
  "/projects/default/traceability",
  "/projects/default/traceability?key=FAC-2",
  "/projects/default/decisions",
  "/projects/default/questions",
  "/projects/default/work?group=all",
  "/projects/default/work-order?id=wo-accept",
  "/projects/default/work-order?id=generated-28",
  "/projects/default/work-order?id=generated-26",
  "/projects/default/work-order?id=wo-failed",
  "/projects/default/work-order?id=wo-running",
  "/projects/default/work-order?id=wo-done&tab=lanes",
  "/projects/default/work-order?id=wo-running&tab=changes",
  "/projects/default/work-order?id=wo-running&tab=activity",
  "/projects/default/work-order?id=wo-accept&tab=changes",
  "/projects/default/work-order?id=wo-accept&tab=activity",
  "/projects/default/work-order?id=wo-done&tab=changes",
  "/projects/default/work-order?id=wo-done&tab=activity",
  "/projects/default/work-order?id=wo-loose&tab=changes",
  "/projects/default/work-order?id=wo-loose&tab=activity",
  "/projects/default/floor",
  "/projects/default/lane?id=lane-wo-running",
  "/projects/default/measures",
  "/projects/default/measures?tab=delivery",
  "/projects/default/measures?tab=delivery&window=7d&by=size",
  "/projects/default/measures?tab=delivery&scope=all",
  "/projects/default/measures?tab=estimates",
  "/projects/default/measures?tab=estimates&window=7d&repository=harbor-web",
  "/projects/default/conversations",
  "/usage",
  "/workers",
  "/projects",
  "/import",
  "/members",
  "/settings?section=admission",
  "/settings?section=assistant",
  "/projects/default/settings?section=recovery",
  "/projects/default/settings?section=fleet",
  "/projects/default/settings?section=delegate-policy",
  "/projects/default/settings?section=estimate",
  "/agents",
  "/projects/default/agents?agent=heavy-implementation",
  "/projects/default/agents?tab=history",
  "/projects/default/repositories?repo=id-api",
  "/projects/default/audit",
  "/projects/default/audit?actor=delegate",
  "/projects/default/audit?tab=notices",
  "/audit",
];

const sizes = { desktop: { width: 1440, height: 900 }, phone: { width: 390, height: 844 } } as const;
const themes = ["light", "dark"] as const;

type Violation = { id: string; help: string; nodes: { target: string[] }[] };

async function audit(page: Page): Promise<string[]> {
  await page.evaluate(axeSource);
  const violations = (await page.evaluate(
    (runOnly) =>
      (window as unknown as { axe: { run: (context: Document, options: object) => Promise<{ violations: unknown[] }> } }).axe
        .run(document, { runOnly: { type: "tag", values: runOnly }, resultTypes: ["violations"] })
        .then((result) => result.violations),
    tags,
  )) as Violation[];
  return violations.map((violation) => `${violation.id}: ${violation.help} at ${violation.nodes.map((node) => node.target.join(" ")).join(", ")}`);
}

// ready waits until the view's title stands and no skeleton holds the place of its body.
async function ready(page: Page): Promise<void> {
  await page.locator("main h1:visible").first().waitFor({ timeout: 20_000 });
  await page.waitForFunction(() => !document.querySelector("main .skeleton-rows"), null, { timeout: 20_000 });
  await page.waitForTimeout(150);
}

let server: ViteDevServer;
let browser: Browser;
let base = "";

beforeAll(async () => {
  server = await createServer({ configFile: "vite.config.ts", logLevel: "error", server: { host: "127.0.0.1", port: 0, strictPort: false, hmr: false } });
  await server.listen();
  base = (server.resolvedUrls?.local[0] ?? "").replace(/\/$/, "");
  try {
    browser = await chromium.launch({ channel: "chrome", headless: true });
  } catch (error) {
    throw new Error(`The accessibility checks run in the installed Google Chrome, which did not start: ${String(error)}`);
  }
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
}, 60_000);

// Each kind of target, as the switcher names it, and what its surface holds that the audit should see.
const targetKinds = [
  { name: /Assistant/, ready: { role: "log", name: "Messages with the assistant" } },
  { name: /Orchestrator.*solo|Orchestrator.*atlas/, ready: { role: "log", name: /Event stream of the orchestrator/ } },
  { name: /Planning session.*lane-wo-running/, ready: { role: "region", name: "Pane of lane lane-wo-running" } },
  { name: /Lane.*lane-wo-1/, ready: { role: "log", name: "Event stream of lane lane-wo-1" } },
  { name: /Lane.*lane-wo-pane/, ready: { role: "region", name: "Pane of lane lane-wo-pane" } },
] as const;

describe.each(Object.entries(sizes))("at %s width", (size, viewport) => {
  describe.each(themes)("in the %s theme", (theme) => {
    it(
      "every view meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        for (const view of views) {
          // A view that never finishes reading is named, with why, rather than failing the whole run unnamed.
          const opened = await page
            .goto(`${base}${view}`)
            .then(() => ready(page))
            .then(
              () => "",
              (error: unknown) => String(error).split("\n")[0] ?? "",
            );
          if (opened) {
            found.push(`${size} ${theme} ${view} did not finish reading: ${opened}`);
            continue;
          }
          for (const violation of await audit(page)) {
            found.push(`${size} ${theme} ${view} ${violation}`);
          }
        }
        await context.close();
        expect(found).toEqual([]);
      },
      300_000,
    );

    it(
      "the conversation dock, open on each kind of target and on its menus and dialogs, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        await page.goto(`${base}/projects/default/roadmap`);
        await ready(page);
        await page.getByRole("button", { name: "Conversation panel" }).click();
        const dock = page.getByRole("complementary", { name: "Conversation" });
        await dock.getByRole("log", { name: "Messages with the assistant" }).waitFor();
        const found: string[] = [];
        for (const kind of targetKinds) {
          await dock.getByRole("button", { name: "Switch target" }).click();
          const menu = page.getByRole("menu", { name: "Switch conversation target" });
          if (kind.name === targetKinds[0].name) {
            found.push(...(await audit(page)).map((violation) => `target menu ${violation}`));
          }
          await menu.getByRole("menuitemradio", { name: kind.name }).first().click();
          await dock.getByRole(kind.ready.role, { name: kind.ready.name }).waitFor();
          await page.waitForTimeout(200);
          found.push(...(await audit(page)).map((violation) => `${String(kind.name)} ${violation}`));
        }
        await dock.getByRole("button", { name: "Switch target" }).click();
        await page.getByRole("menuitemradio", { name: /Assistant/ }).click();
        await dock.getByRole("log", { name: "Messages with the assistant" }).waitFor();
        await dock.getByRole("button", { name: "Conversation actions" }).click();
        found.push(...(await audit(page)).map((violation) => `conversation menu ${violation}`));
        // A control that changes state as a dialog opens must not show a color on its way: axe reads whatever
        // a transition has reached, so the audit runs with transitions held slow.
        const slow = await page.addStyleTag({ content: "*, *::before, *::after { transition-duration: 5s !important; }" });
        await page.getByRole("menuitem", { name: "Rename…" }).click();
        await page.getByRole("dialog", { name: "Rename conversation" }).waitFor();
        found.push(...(await audit(page)).map((violation) => `rename dialog ${violation}`));
        await slow.evaluate((style) => (style as HTMLElement).remove());
        await page.keyboard.press("Escape");
        await dock.getByRole("button", { name: "Conversation actions" }).click();
        await page.getByRole("menuitem", { name: "Delete conversation…" }).click();
        await page.getByRole("dialog", { name: /^Delete/ }).waitFor();
        found.push(...(await audit(page)).map((violation) => `delete dialog ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      180_000,
    );

    it(
      "the start dialog, with workers, with a refusal, and with no worker, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        // The ready work order of the plans the Go tests wrote, which has a page and a row on the floor.
        await page.goto(`${base}/projects/default/work-order?id=generated-28`);
        await ready(page);
        await page.getByRole("button", { name: "Start", exact: true }).click();
        const dialog = page.getByRole("dialog", { name: "Start Write the guide" });
        await dialog.waitFor();
        await page.waitForFunction(() => document.querySelectorAll("[role=dialog] select option").length > 1, null, { timeout: 20_000 });
        // The Start button turns from disabled to enabled as the workers arrive, and axe reads a color a
        // transition has reached, so each audit waits for the transition to end.
        await page.mouse.move(0, 0);
        await page.waitForTimeout(600);
        found.push(...(await audit(page)).map((violation) => `start dialog with workers ${violation}`));
        // The answer to a claim the test does not serve is a refusal, which the dialog shows at its foot.
        await dialog.getByRole("button", { name: "Start", exact: true }).click();
        await dialog.getByRole("alert").waitFor();
        await page.mouse.move(0, 0);
        await page.waitForTimeout(600);
        found.push(...(await audit(page)).map((violation) => `start dialog with a refusal ${violation}`));
        await page.keyboard.press("Escape");
        await page.route(/\/v1\/workers$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ workers: [] }) }));
        await page.getByRole("button", { name: "Start", exact: true }).click();
        await page.getByRole("dialog", { name: "Start Write the guide" }).getByText("No worker has joined.").first().waitFor();
        await page.mouse.move(0, 0);
        await page.waitForTimeout(600);
        found.push(...(await audit(page)).map((violation) => `start dialog with no worker ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the lane caps form, with a number above the recommendation, with a refusal, with no recommendation, and for a worker on another machine, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        // Save turns from disabled to enabled as the form changes, and axe reads a color a transition has
        // reached, so each audit waits for the transitions to end.
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/settings?section=admission`);
        await ready(page);
        await page.getByRole("button", { name: "A number" }).click();
        await page.getByLabel("Number of lanes").fill("9");
        await page.getByText(/9 is above the recommendation of 6/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `lane caps above the recommendation ${violation}`));
        // The daemon refuses the change, which the form shows at its foot.
        await page.route(/\/v1\/workers\/worker-1\/caps$/, (route) =>
          route.request().method() === "PUT"
            ? route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: { code: "invalid", message: "max_lanes_per_sign_in is not a number of lanes" } }) })
            : route.fallback(),
        );
        await page.getByRole("button", { name: "Save", exact: true }).click();
        await page.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `lane caps with a refusal ${violation}`));
        const caps = JSON.parse(goldens["worker-caps"] ?? "{}") as Record<string, unknown>;
        await page.route(/\/v1\/workers\/worker-1\/caps$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ...caps, recommendation: undefined }) }));
        await page.goto(`${base}/settings?section=admission`);
        await ready(page);
        await page.getByText(/has not reported a recommendation/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `lane caps with no recommendation ${violation}`));
        await page.route(/\/v1\/workers\/worker-1\/caps$/, (route) =>
          route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: { code: "invalid", message: "worker worker-1 runs on another machine, so its lane caps are set in the user's profile on that machine" } }) }),
        );
        await page.goto(`${base}/settings?section=admission`);
        await page.getByText(/runs on another machine/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `lane caps of a worker on another machine ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the estimate policy, with a number the API would refuse, in review, with a refusal, with a notice, and its history, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/projects/default/settings?section=estimate`);
        await ready(page);
        const minimum = page.getByLabel(/Comparable outcomes a confident estimate needs/);
        await minimum.fill("4");
        await page.getByText("Enter a whole number, 5 or more.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimate policy with an error ${violation}`));
        await minimum.fill("15");
        await page.getByRole("button", { name: "Review change" }).click();
        const dialog = page.getByRole("dialog", { name: "Review the estimate policy change" });
        await dialog.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimate policy review ${violation}`));
        const published = (status: number, body: unknown) => (route: Route) => (route.request().method() === "POST" ? route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) }) : route.fallback());
        await page.route(/\/v1\/projects\/default\/policies$/, published(400, { error: { code: "invalid", message: "invalid estimate policy: version 3 is not newer than version 3" } }));
        await dialog.getByRole("button", { name: "Save version 3" }).click();
        await dialog.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimate policy review with a refusal ${violation}`));
        await page.route(/\/v1\/projects\/default\/policies$/, published(201, { version: 3, notice: "The policy is in force, and the project's delivery records are still in the installation's pool: the next delivery tick takes them out." }));
        await dialog.getByRole("button", { name: "Save version 3" }).click();
        await page.getByText(/the next delivery tick takes them out/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimate policy with a notice ${violation}`));
        await page.getByRole("tab", { name: /History/ }).click();
        await page.getByRole("heading", { name: "Version 1" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimate policy history ${violation}`));
        await page.getByRole("button", { name: "Revert" }).click();
        await page.getByRole("dialog", { name: "Revert to version 1" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimate policy revert ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the add agent dialog, as a copy, with pasted TOML, with text that stops it, and with a refusal, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/projects/default/agents`);
        await ready(page);
        await page.getByRole("button", { name: "Add agent" }).click();
        const dialog = page.getByRole("dialog", { name: "Add agent" });
        await dialog.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `add agent as a copy ${violation}`));
        await dialog.getByRole("button", { name: "Paste TOML" }).click();
        const text = dialog.getByLabel(/Agent definition/);
        await text.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `add agent with no text yet ${violation}`));
        await text.fill('[[agent]]\ncolour = "red"\n');
        await dialog.getByText("Unknown key colour.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `add agent with text that stops it ${violation}`));
        await text.fill('[[agent]]\nname = "scout"\npurpose = "Finds the code a change touches."\nkind = "lookup"\naccess = "read_only"\nclass = "small"\neffort = "low"\n\n[agent.tools]\nallow = ["read", "search"]\n');
        await dialog.getByRole("table", { name: "Parsed agent" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `add agent with a parsed agent ${violation}`));
        await page.route(/\/v1\/projects\/default\/roster\/changes$/, (route) =>
          route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: { code: "invalid", message: 'invalid roster: agent "scout" allows no tools' } }) }),
        );
        await dialog.getByRole("button", { name: /Save version/ }).click();
        await dialog.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `add agent with a refusal ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the recovery actions of a work order, their menu and each dialog, meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        // A change the test does not serve is refused, which each dialog shows at its foot.
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        // Work that runs: its menu holds Cancel, whose dialog asks why and sends nothing until a person says.
        await page.goto(`${base}/projects/default/work-order?id=wo-running`);
        await ready(page);
        await page.getByRole("button", { name: "More actions" }).click();
        await page.getByRole("menu", { name: "More actions" }).waitFor();
        found.push(...(await audit(page)).map((violation) => `work actions menu ${violation}`));
        await page.getByRole("menuitem", { name: "Cancel this work…" }).click();
        const cancel = page.getByRole("dialog", { name: "Cancel this work: Write the docs" });
        await cancel.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `cancel dialog ${violation}`));
        await cancel.getByRole("textbox", { name: "Reason (required)" }).fill("The plan changed.");
        await cancel.getByRole("button", { name: "Cancel this work" }).click();
        await cancel.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `cancel dialog with a refusal ${violation}`));
        await page.keyboard.press("Escape");
        // Failed work: the card asks for a retry, with the reason form open.
        await page.goto(`${base}/projects/default/work-order?id=wo-failed`);
        await ready(page);
        const card = page.getByRole("article", { name: /Try the impossible/ });
        await card.getByRole("button", { name: "Retry with an extra attempt" }).click();
        await card.getByRole("textbox", { name: "Why (required)" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `failed work card asking why ${violation}`));
        // Work a worker holds: giving the claim back names the worker and asks nothing else.
        const plans = JSON.parse(goldens.plans ?? "{}") as { plans: { work_orders: { id: string; state: string; claim_id?: string; fence?: number; lease_worker?: string }[] }[]; work_actions: Record<string, unknown> };
        for (const order of plans.plans.flatMap((plan) => plan.work_orders)) {
          if (order.id === "generated-28") {
            Object.assign(order, { state: "claimed", claim_id: "claim-held", fence: 3, lease_worker: "worker-a" });
            plans.work_actions["generated-28"] = { actions: ["start_lane", "release", "cancel"] };
          }
        }
        await page.route(/\/views\/plans$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(plans) }));
        await page.goto(`${base}/projects/default/work-order?id=generated-28`);
        await ready(page);
        await page.getByRole("button", { name: "More actions" }).click();
        await page.getByRole("menuitem", { name: "Release the claim…" }).click();
        const release = page.getByRole("dialog", { name: "Release the claim: Write the guide" });
        await release.getByText("holds the claim.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `release dialog ${violation}`));
        await page.keyboard.press("Escape");
        await page.unroute(/\/views\/plans$/);
        // A worktree: the worker's refusal comes first, and a second press may force it.
        await page.goto(`${base}/projects/default/work-order?id=wo-done&tab=lanes`);
        await ready(page);
        await page.getByRole("button", { name: "Archive the worktree of lane lane-wo-done" }).click();
        const archive = page.getByRole("dialog", { name: "Archive the worktree: lane lane-wo-done" });
        await archive.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `archive dialog ${violation}`));
        await page.route(/\/lane-archive$/, (route) =>
          route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { code: "unpushed_work", message: "the worktree holds work no remote has: lane lane-wo-done" } }) }),
        );
        await archive.getByRole("button", { name: "Archive the worktree" }).click();
        await archive.getByRole("button", { name: "Archive anyway" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `archive dialog with the worker's refusal ${violation}`));
        await page.keyboard.press("Escape");
        // A lane on the floor offers the same cancel as its work order's page.
        await page.goto(`${base}/projects/default/floor`);
        await ready(page);
        await page.getByRole("button", { name: "Actions for lane lane-wo-running" }).click();
        await page.getByRole("menuitem", { name: "Cancel this work…" }).click();
        await page.getByRole("dialog", { name: "Cancel this work: Write the docs" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `cancel dialog from a lane ${violation}`));
        await page.keyboard.press("Escape");
        // A worker's orchestrator, read as the ledger holds it.
        await page.goto(`${base}/workers`);
        await ready(page);
        await page.getByRole("button", { name: /^Details of the orchestrator on / }).first().click();
        const details = page.getByRole("dialog", { name: /^Orchestrator on / });
        await details.getByText("Breaker resets").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `orchestrator details ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      180_000,
    );

    it(
      "the add category dialog, empty, with a name the ledger would refuse, and with a refusal, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/projects/default/questions`);
        await ready(page);
        await page.getByRole("button", { name: "Add category" }).click();
        const dialog = page.getByRole("dialog", { name: "Add category" });
        await dialog.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `add category dialog ${violation}`));
        await dialog.getByLabel("Name").fill("Release Planning");
        await dialog.getByRole("button", { name: "Add category" }).click();
        await dialog.getByText(/lowercase words joined by hyphens/).last().waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `add category dialog with an error ${violation}`));
        await page.route(/\/v1\/projects\/default\/categories$/, (route) =>
          route.request().method() === "POST"
            ? route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: { code: "invalid", message: "invalid question or decision: category is taken" } }) })
            : route.fallback(),
        );
        await dialog.getByLabel("Name").fill("billing");
        await dialog.getByRole("button", { name: "Add category" }).click();
        await dialog.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `add category dialog with a refusal ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the record question dialog, empty, with its errors, about a work order, and with a refusal, and the dismiss dialog meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        const refuse = (pattern: RegExp, message: string) =>
          page.route(pattern, (route) =>
            route.request().method() === "POST" ? route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: { code: "invalid", message } }) }) : route.fallback(),
          );
        await page.goto(`${base}/projects/default/questions`);
        await ready(page);
        await page.getByRole("button", { name: "Record question" }).click();
        const dialog = page.getByRole("dialog", { name: "Record question" });
        await dialog.waitFor();
        await dialog.getByRole("checkbox", { name: "Security" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record question dialog ${violation}`));
        await dialog.getByRole("button", { name: "Record question" }).click();
        await dialog.getByText("Write the question.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record question dialog with an error ${violation}`));
        await dialog.getByRole("textbox", { name: "Question" }).fill("Which currency do invoices use?");
        await dialog.getByRole("checkbox", { name: "Security" }).check();
        await dialog.getByRole("combobox", { name: "About" }).selectOption("work_order");
        await dialog.getByRole("checkbox", { name: "Work on it waits for the answer" }).check();
        await dialog.getByRole("button", { name: "Record question" }).click();
        await dialog.getByText("Name the work order, or choose nothing in particular.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record question dialog about a work order ${violation}`));
        await dialog.getByRole("textbox", { name: "Work order ID" }).fill("wo-invoices");
        await refuse(/\/v1\/projects\/default\/questions$/, "invalid question or decision: unknown category");
        await dialog.getByRole("button", { name: "Record question" }).click();
        await dialog.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record question dialog with a refusal ${violation}`));
        await page.keyboard.press("Escape");
        await page.getByRole("button", { name: "Dismiss Does the plan cover a restart?" }).click();
        const confirm = page.getByRole("dialog", { name: "Dismiss this question?" });
        await confirm.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `dismiss question dialog ${violation}`));
        await refuse(/\/v1\/projects\/default\/questions\/question-review\/dismiss$/, "invalid question or decision: question question-review is answered");
        await confirm.getByRole("button", { name: "Dismiss question" }).click();
        await confirm.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `dismiss question dialog with a refusal ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the answer form of an open question, with a recommendation, with a member's own words, and with a refusal, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/projects/default/questions`);
        await ready(page);
        await page.getByRole("button", { name: "Answer Should the budget hold across restarts?" }).click();
        const group = page.getByRole("group", { name: "Should the budget hold across restarts?" });
        await group.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `answer form with a recommendation ${violation}`));
        await group.getByRole("radio", { name: "Something else" }).check();
        await group.getByRole("textbox", { name: "Your answer" }).fill("Hold it for a day.");
        await settle();
        found.push(...(await audit(page)).map((violation) => `answer form with a member's own words ${violation}`));
        await page.route(/\/v1\/projects\/default\/questions\/question-1\/answer$/, (route) =>
          route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { code: "resolved", message: "This question already has its answer." } }) }),
        );
        await page.getByRole("button", { name: "Send this answer" }).click();
        await page.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `answer form with a refusal ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the record decision dialog, empty, with its errors, in its review, and with a refusal, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/projects/default/decisions`);
        await ready(page);
        await page.getByRole("button", { name: "Record decision" }).click();
        const dialog = page.getByRole("dialog", { name: "Record decision" });
        await dialog.waitFor();
        await dialog.getByRole("checkbox", { name: "Security" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record decision dialog ${violation}`));
        await dialog.getByText("More").click();
        await dialog.getByRole("textbox", { name: "Decision ID (optional)" }).fill("pd-2f9a");
        await dialog.getByRole("button", { name: "Review" }).click();
        await dialog.getByText("State what was decided.").waitFor();
        await dialog.getByText(/may not start with pd-/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record decision dialog with errors ${violation}`));
        await dialog.getByRole("textbox", { name: "Statement" }).fill("Charge in euros.");
        await dialog.getByRole("textbox", { name: "Rationale" }).fill("Every customer is in the union.");
        await dialog.getByRole("textbox", { name: "Decision ID (optional)" }).fill("");
        await dialog.getByRole("checkbox", { name: "Security" }).check();
        await page.route(/\/v1\/projects\/default\/candidates\?/, (route) =>
          route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ candidates: [{ kind: "requirement", record_id: "FR-RETRY-1", score: 0.9 }, { kind: "decision", record_id: "decision-3", score: 0.7 }] }),
          }),
        );
        await dialog.getByRole("textbox", { name: "Find a record to cite" }).fill("retry");
        await dialog.getByRole("button", { name: "Search" }).click();
        await dialog.getByRole("button", { name: "Cite requirement:FR-RETRY-1" }).click();
        await dialog.getByRole("button", { name: "Cited requirement:FR-RETRY-1" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record decision dialog with search results ${violation}`));
        await dialog.getByRole("button", { name: "Review" }).click();
        await dialog.getByRole("heading", { name: "Review the decision" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record decision review ${violation}`));
        await page.route(/\/v1\/projects\/default\/decisions$/, (route) =>
          route.request().method() === "POST"
            ? route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { code: "decision_ended", message: "decision decision-3 is overturned" } }) })
            : route.fallback(),
        );
        await dialog.getByRole("button", { name: "Record decision" }).click();
        await dialog.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record decision review with a refusal ${violation}`));
        await dialog.getByRole("button", { name: "Back" }).click();
        await dialog.getByRole("checkbox", { name: "Record as an assumption" }).check();
        await dialog.getByRole("button", { name: "Review" }).click();
        await dialog.getByRole("heading", { name: "Review the assumption" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `record assumption review ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the overturn dialog, empty, with its errors, with a refusal, and with what it made, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/projects/default/decisions`);
        await ready(page);
        await page.getByRole("button", { name: "Overturn decision-3" }).click();
        const dialog = page.getByRole("dialog", { name: "Overturn decision-3" });
        await dialog.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `overturn dialog ${violation}`));
        await dialog.getByText("More").click();
        await dialog.getByRole("textbox", { name: "ID of the new decision (optional)" }).fill("pd-2f9a");
        await dialog.getByRole("button", { name: "Overturn decision" }).click();
        await dialog.getByText("State what stands instead.").waitFor();
        await dialog.getByText(/may not start with pd-/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `overturn dialog with errors ${violation}`));
        await dialog.getByRole("textbox", { name: "New statement" }).fill("Retry twice, and no more.");
        await dialog.getByRole("textbox", { name: "ID of the new decision (optional)" }).fill("");
        const overturn = (status: number, body: unknown) => (route: Route) =>
          route.request().method() === "POST" ? route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) }) : route.fallback();
        await page.route(/\/v1\/projects\/default\/decisions\/decision-3\/overturn$/, overturn(409, { error: { code: "decision_ended", message: "decision decision-3 is overturned" } }));
        await dialog.getByRole("button", { name: "Overturn decision" }).click();
        await dialog.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `overturn dialog with a refusal ${violation}`));
        await page.route(
          /\/v1\/projects\/default\/decisions\/decision-3\/overturn$/,
          overturn(201, {
            old: { id: "decision-3", status: "overturned", statement: "Retry until the budget runs out." },
            new: { id: "decision-new", status: "decided", statement: "Retry twice, and no more.", supersedes: "decision-3" },
            rework: ["generated-26", "generated-28"],
            noticed: ["lane-wo-running"],
          }),
        );
        await dialog.getByRole("button", { name: "Overturn decision" }).click();
        await dialog.getByRole("heading", { name: "Overturned decision-3" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `overturn dialog with what it made ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the merge request cards, to approve and held, with a reason typed and with a refusal, meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        // A change the test does not serve is refused, which each card shows at its foot.
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        for (const [address, name, button, state] of [
          ["/needs-you?item=approval%3Amerge-approve", /^Approve the merge of wo-running/, "Approve merge", "merge approval"],
          ["/needs-you?item=approval%3Amerge-held", /^Retry the held merge of wo-held/, "Retry merge", "held merge"],
        ] as const) {
          await page.goto(`${base}${address}`);
          await ready(page);
          const card = page.getByRole("article", { name });
          await card.getByRole("textbox", { name: /^Reason/ }).fill("Split the migration first.");
          await settle();
          found.push(...(await audit(page)).map((violation) => `${state} with a reason ${violation}`));
          await card.getByRole("button", { name: button }).click();
          await card.getByRole("alert").waitFor();
          await settle();
          found.push(...(await audit(page)).map((violation) => `${state} with a refusal ${violation}`));
        }
        // The same card stands on the page of the work it merges.
        await page.goto(`${base}/projects/default/work-order?id=wo-running`);
        await ready(page);
        await page.getByRole("article", { name: /^Approve the merge of wo-running/ }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `merge approval on the work order ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the new work order dialog, empty, with every field, with its errors, and with a refusal, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/projects/default/work`);
        await ready(page);
        await page.getByRole("button", { name: "New work order" }).click();
        const dialog = page.getByRole("dialog", { name: "New work order" });
        await dialog.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `new work order dialog ${violation}`));
        await dialog.getByText("More").click();
        await page.waitForFunction(() => document.querySelectorAll("[role=dialog] select option").length > 4, null, { timeout: 20_000 });
        await settle();
        found.push(...(await audit(page)).map((violation) => `new work order dialog with every field ${violation}`));
        await dialog.getByRole("textbox", { name: "Work order ID (optional)" }).fill("has space");
        await dialog.getByRole("textbox", { name: "Priority" }).fill("high");
        await dialog.getByRole("textbox", { name: "Depends on" }).fill("not/an/id");
        await dialog.getByRole("textbox", { name: "Requirements" }).fill("FR@1");
        await dialog.getByRole("button", { name: "Create work order" }).click();
        await dialog.getByText("Give the work order a title.").waitFor();
        await dialog.getByText("A priority is a whole number, such as 2.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `new work order dialog with errors ${violation}`));
        await dialog.getByRole("textbox", { name: "Title" }).fill("Export the invoices");
        await dialog.getByRole("textbox", { name: "Criteria" }).fill("An export downloads.");
        await dialog.getByRole("textbox", { name: "Work order ID (optional)" }).fill("");
        await dialog.getByRole("textbox", { name: "Priority" }).fill("");
        await dialog.getByRole("textbox", { name: "Depends on" }).fill("");
        await dialog.getByRole("textbox", { name: "Requirements" }).fill("");
        // The answer to a create the test does not serve is a refusal, which the dialog shows at its foot.
        await dialog.getByRole("button", { name: "Create work order" }).click();
        await dialog.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `new work order dialog with a refusal ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the start contract review dialog, on a plan, with a refusal, asking which plan, with no plan read, and with none, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        const json = (status: number, body: unknown) => (route: Route) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
        await page.goto(`${base}/projects/default/plans?plan=generated-2`);
        await ready(page);
        await page.getByRole("button", { name: "Start contract review" }).click();
        const dialog = page.getByRole("dialog", { name: "Start contract review" });
        await dialog.waitFor();
        await dialog.getByText("Add the retry budget").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `start contract review dialog ${violation}`));
        await page.route(/\/v1\/projects\/default\/plans\/generated-2\/reviews$/, json(400, { error: { code: "invalid", message: "plan generated-2 has no work to review" } }));
        await dialog.getByRole("button", { name: "Start contract review" }).click();
        await dialog.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `start contract review dialog with a refusal ${violation}`));
        await page.keyboard.press("Escape");
        await page.goto(`${base}/projects/default/plans`);
        await ready(page);
        await page.getByRole("button", { name: "Start contract review" }).click();
        await dialog.getByText(/The plans could not be read/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `start contract review dialog with no plan read ${violation}`));
        await page.keyboard.press("Escape");
        await page.route(
          /\/v1\/projects\/default\/plans$/,
          json(200, {
            plans: [
              { id: "generated-2", title: "Add the retry budget", status: "active" },
              { id: "generated-4", title: "Document the retry budget", status: "active" },
            ],
          }),
        );
        await page.getByRole("button", { name: "Start contract review" }).click();
        await dialog.getByRole("combobox", { name: "Plan" }).waitFor();
        await page.waitForFunction(() => document.querySelectorAll("[role=dialog] select option").length > 2, null, { timeout: 20_000 });
        await settle();
        found.push(...(await audit(page)).map((violation) => `start contract review dialog asking which plan ${violation}`));
        await dialog.getByRole("button", { name: "Start contract review" }).click();
        await dialog.getByText("Choose the plan to review.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `start contract review dialog with an error ${violation}`));
        await page.keyboard.press("Escape");
        await page.route(/\/v1\/projects\/default\/plans$/, json(200, { plans: [] }));
        await page.getByRole("button", { name: "Start contract review" }).click();
        await dialog.getByText("This project has no plan to review yet.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `start contract review dialog with no plan ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the Delivery tab, with no finished work, with a failure, and its controls, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        const delivery = /\/v1\/projects\/default\/measures\/delivery/;
        await page.goto(`${base}/projects/default/measures?tab=delivery`);
        await ready(page);
        await page.getByRole("region", { name: "Where the time went" }).waitFor();
        await page.getByRole("combobox", { name: "Group by" }).selectOption("agent");
        await settle();
        found.push(...(await audit(page)).map((violation) => `delivery by roster agent ${violation}`));
        await page.getByRole("button", { name: "24 hours" }).click();
        await page.getByRole("region", { name: "Delivery by agent" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `delivery over 24 hours ${violation}`));
        await page.route(delivery, (route) => route.fulfill({ status: 200, contentType: "application/json", body: goldens["delivery-empty"] ?? "{}" }));
        await page.goto(`${base}/projects/default/measures?tab=delivery`);
        await ready(page);
        await page.getByText("Delivery measures appear when work orders finish.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `delivery with no finished work ${violation}`));
        await page.route(delivery, (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "the ledger could not be read" } }) }));
        await page.goto(`${base}/projects/default/measures?tab=delivery`);
        await page.getByRole("alert").getByText(/the ledger could not be read/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `delivery with a failure ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the Estimates tab, with calibration, with no finished work, with a failure, and its controls, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        const estimates = /\/v1\/projects\/default\/measures\/estimates/;
        await page.goto(`${base}/projects/default/measures?tab=estimates`);
        await ready(page);
        await page.getByRole("region", { name: "Confident estimates, last 30 days" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimates ${violation}`));
        await page.getByRole("combobox", { name: "Repository" }).selectOption("harbor-web");
        await page.getByRole("button", { name: "7 days" }).click();
        await page.getByRole("region", { name: "Confident estimates, last 7 days" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimates of one repository over 7 days ${violation}`));
        await page.route(estimates, (route) => route.fulfill({ status: 200, contentType: "application/json", body: goldens["calibration-empty"] ?? "{}" }));
        await page.goto(`${base}/projects/default/measures?tab=estimates`);
        await ready(page);
        await page.getByText("Estimate calibration appears when work orders finish.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimates with no finished work ${violation}`));
        await page.route(estimates, (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "the ledger could not be read" } }) }));
        await page.goto(`${base}/projects/default/measures?tab=estimates`);
        await page.getByRole("alert").getByText(/the ledger could not be read/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `estimates with a failure ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "a plan's forecast, on its page and on the Roadmap, confident, low, pending, replaced, and refused, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        const forecast = /\/v1\/projects\/default\/plans\/[^/]+\/forecast$/;
        const served = (body: string | undefined) => (route: Route) => route.fulfill({ status: 200, contentType: "application/json", body: body ?? "{}" });
        await page.goto(`${base}/projects/default/roadmap`);
        await ready(page);
        await page.getByRole("group", { name: "Forecast of The factory floor" }).first().waitFor();
        await page.getByText("What-ifs and how it was made").first().click();
        await settle();
        found.push(...(await audit(page)).map((violation) => `Roadmap with forecasts and one open ${violation}`));
        await page.goto(`${base}/projects/default/plans?plan=generated-2`);
        await ready(page);
        await page.getByRole("heading", { level: 2, name: "Forecast" }).waitFor();
        await page.getByText("Low confidence").first().waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `plan page with a forecast ${violation}`));
        await page.route(forecast, served(goldens["forecast-pending"]));
        await page.goto(`${base}/projects/default/plans?plan=generated-2`);
        await page.getByText(/Forecast pending/).first().waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `plan page with a forecast pending ${violation}`));
        await page.goto(`${base}/projects/default/roadmap`);
        await page.getByRole("group", { name: "Forecast of The factory floor" }).first().waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `Roadmap with forecasts pending ${violation}`));
        await page.route(forecast, served(goldens["forecast-superseded"]));
        await page.goto(`${base}/projects/default/plans?plan=generated-2`);
        await page.getByText(/replaced version 1/).first().waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `plan page with a forecast a newer version replaced ${violation}`));
        await page.route(forecast, (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "the ledger could not be read" } }) }));
        await page.goto(`${base}/projects/default/plans?plan=generated-2`);
        await page.getByRole("alert").getByText(/the ledger could not be read/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `plan page with a forecast it could not read ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the palette, the shortcut sheet, the project switcher, a menu, and a dialog meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        await page.goto(`${base}/projects/default/floor`);
        await ready(page);
        const found: string[] = [];
        await page.keyboard.press(process.platform === "darwin" ? "Meta+K" : "Control+K");
        await page.getByRole("combobox").fill("wo");
        await page.waitForTimeout(400);
        found.push(...(await audit(page)).map((violation) => `palette ${violation}`));
        await page.keyboard.press("Escape");
        await page.locator("main h1:visible").first().click();
        await page.keyboard.press("?");
        await page.getByRole("dialog", { name: "Keyboard shortcuts" }).waitFor();
        found.push(...(await audit(page)).map((violation) => `sheet ${violation}`));
        await page.keyboard.press("Escape");
        await page.keyboard.press("p");
        await page.getByRole("dialog", { name: "Switch project" }).waitFor();
        found.push(...(await audit(page)).map((violation) => `project switcher ${violation}`));
        await page.keyboard.press("Escape");
        await page.getByRole("button", { name: "Actions for lane lane-wo-tripped" }).click();
        await page.getByRole("menu").waitFor();
        found.push(...(await audit(page)).map((violation) => `lane menu ${violation}`));
        await page.getByRole("menuitem").first().click();
        await page.getByRole("dialog").waitFor();
        found.push(...(await audit(page)).map((violation) => `lane act dialog ${violation}`));
        await page.keyboard.press("Escape");
        await page.goto(`${base}/projects/default/roadmap`);
        await ready(page);
        await page.getByRole("button", { name: "Start planning session" }).first().click();
        await page.getByRole("dialog", { name: "Start planning session" }).waitFor();
        found.push(...(await audit(page)).map((violation) => `start planning session dialog ${violation}`));
        await page.keyboard.press("Escape");
        await page.goto(`${base}/projects/default/plan?id=generated-1&version=6`);
        await ready(page);
        await page.getByRole("button", { name: "Decline…" }).click();
        await page.getByRole("dialog", { name: "Decline version 6" }).waitFor();
        found.push(...(await audit(page)).map((violation) => `decline dialog ${violation}`));
        await page.keyboard.press("Escape");
        if (size === "phone") {
          await page.getByRole("button", { name: "Views" }).click();
          await page.getByRole("navigation", { name: "Views" }).getByRole("link", { name: "Roadmap" }).waitFor();
          found.push(...(await audit(page)).map((violation) => `drawer ${violation}`));
        }
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the rework dialog and the merge dialog of the Reviews queue meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        await page.goto(`${base}/projects/reviewing/reviews`);
        await ready(page);
        await page.getByRole("button", { name: "Rework wo-accept" }).click();
        const rework = page.getByRole("dialog", { name: "Send wo-accept back for rework" });
        await rework.waitFor();
        await page.mouse.move(0, 0);
        await page.waitForTimeout(600);
        found.push(...(await audit(page)).map((violation) => `rework dialog ${violation}`));
        await rework.getByRole("textbox").fill("The log still drops the last day.");
        await rework.getByRole("button", { name: "Send back for rework" }).click();
        // The test serves no change, so the server's refusal stands at the foot of the dialog.
        await rework.getByRole("alert").waitFor();
        await page.mouse.move(0, 0);
        await page.waitForTimeout(600);
        found.push(...(await audit(page)).map((violation) => `rework dialog with a refusal ${violation}`));
        await rework.getByRole("button", { name: "Cancel" }).click();
        await rework.waitFor({ state: "detached" });
        await page.getByRole("button", { name: "Approve merge of wo-ruled" }).click();
        const merge = page.getByRole("dialog", { name: "Merge request for wo-ruled" });
        await merge.waitFor();
        await page.mouse.move(0, 0);
        await page.waitForTimeout(600);
        found.push(...(await audit(page)).map((violation) => `merge dialog ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    // The Notices tab, a lane's messages, Usage's cost by lane, and the Workers page's drivers, in the states their
    // full goldens do not reach: nothing to list, a store the control plane lacks, a read refused, and workers that
    // reported each kind of doctor result.
    it(
      "the notices, lane messages, lane costs, and drivers, empty, refused, and with every doctor result, meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const serve = async (pattern: RegExp, body: string, status = 200) => {
          await page.unroute(pattern);
          await page.route(pattern, (route) => route.fulfill({ status, contentType: "application/json", body }));
        };
        const refused = JSON.stringify({ error: { code: "internal", message: "the store could not be read" } });
        const check = async (name: string, address: string) => {
          await page.goto(`${base}${address}`);
          await ready(page);
          await page.waitForTimeout(300);
          found.push(...(await audit(page)).map((violation) => `${name} ${violation}`));
        };
        await serve(/\/notices$/, goldens["notices-empty"] ?? "{}");
        await check("notices with none","/projects/default/audit?tab=notices");
        await serve(/\/notices$/, refused, 500);
        await check("notices refused", "/projects/default/audit?tab=notices");
        await serve(/\/lanes\/[^/]+\/messages$/, goldens["lane-messages-empty"] ?? "{}");
        await check("lane messages with none", "/projects/default/lane?id=lane-wo-running");
        await serve(/\/lanes\/[^/]+\/messages$/, refused, 500);
        await check("lane messages refused", "/projects/default/lane?id=lane-wo-running");
        await serve(/\/v1\/telemetry\/lanes$/, goldens["telemetry-lanes-empty"] ?? "{}");
        await check("lane costs with none", "/usage");
        await serve(/\/v1\/telemetry\/lanes$/, goldens["telemetry-lanes-unavailable"] ?? "{}");
        await check("lane costs with no store", "/usage");
        await serve(/\/v1\/telemetry\/lanes$/, refused, 500);
        await check("lane costs refused", "/usage");
        // The workers each reported a different doctor result: usable, an unqualified version, no CLI, a CLI that failed
        // its version check, a signed-out CLI, and none reported.
        await serve(/\/views\/shop-floor$/, goldens["shop-floor-drivers"] ?? "{}");
        await check("drivers of every doctor result", "/workers");
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    // The top bar gives way in the project's name, with an ellipsis, before any button shrinks: a button smaller
    // than 24 px is a target a finger or a pointer misses (WCAG 2.5.8).
    if (size === "phone") {
      it(
        "the top bar keeps its buttons their size under a long project name",
        async () => {
          const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
          await context.route(/\/v1\//, answer);
          const page = await context.newPage();
          await page.goto(`${base}/projects/a-very-long-project-name-indeed/reviews`);
          await ready(page);
          const menu = await page.locator(".menu-btn").boundingBox();
          const switcher = await page.locator(".project-button").boundingBox();
          expect(menu?.width).toBe(40);
          expect(menu?.height).toBe(40);
          // The name stays inside the bar: no part of the page scrolls sideways.
          expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
          expect(switcher?.width).toBeLessThan(180);
          await context.close();
        },
        60_000,
      );
    }
  });
});
