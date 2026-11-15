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

const projects = JSON.stringify({ projects: [{ id: "default", name: "default", display_name: "Default", state: "active", created_at: 1, repositories: [] }] });

// answer stands where the daemon stands, as `serveViews` does for the other tests.
async function answer(route: Route): Promise<void> {
  const url = new URL(route.request().url());
  const path = url.pathname.replace(/^\/v1\/projects\/[^/]+/, "/v1");
  const json = (body: string, status = 200) => route.fulfill({ status, contentType: "application/json", body });
  if (url.pathname === "/v1/projects") {
    return json(projects);
  }
  if (path === "/v1/needs-you") {
    // Needs you with the confirmation cards a delegate prepared beside every other kind.
    const needs = JSON.parse(goldens["needs-you"] ?? "{}") as { items: unknown[] };
    const cards = JSON.parse(goldens["needs-you-cards"] ?? "{}") as { items: unknown[] };
    return json(JSON.stringify({ ...needs, items: [...needs.items, ...cards.items] }));
  }
  const view = /^\/v1\/views\/([a-z-]+)$/.exec(path)?.[1];
  if (view && goldens[view]) {
    return json(goldens[view]);
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
  "/needs-you?item=plan_approval%3Agenerated-11",
  "/needs-you?kind=cards&item=confirm%3Agenerated-1",
  "/needs-you?kind=cards&item=confirm%3Agenerated-2",
  "/projects/default/roadmap",
  "/projects/default/plans",
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
  "/projects/default/floor",
  "/projects/default/lane?id=lane-wo-running",
  "/projects/default/measures",
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
  });
});
