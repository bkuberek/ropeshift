// @vitest-environment node
import type { Browser, BrowserContext, Locator, Page, Route } from "playwright-core";
import type { ViteDevServer } from "vite";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { dialogStates, signInStates, views } from "./a11yStates";
import type { SignInStateId } from "./app/SignIn";
import { openBrowser, suiteFor } from "./browser";
import { answer, audit, dialogsSeen, goldens, ready, serveApp } from "./browserApp";

// The accessibility checks: axe-core in a real browser, over every view of the application, in both
// themes, at desktop and at phone width, on what the Go tests wrote from a real ledger. They run with
// the WCAG 2.0, 2.1, and 2.2 A and AA rules, and any violation fails the build. The browser is the first
// that starts of the executable in ROPESHIFT_TEST_CHROME, Google Chrome, a chromium on the PATH, and
// Playwright's Chromium; continuous integration's runners carry Google Chrome.

const sizes = { desktop: { width: 1440, height: 900 }, phone: { width: 390, height: 844 } } as const;
const themes = ["light", "dark"] as const;

// ROPESHIFT_A11Y_VIEWS is a pattern of the addresses to check, so a change to one view runs only its own states. It is
// unset in continuous integration, which checks every view.
const only = process.env.ROPESHIFT_A11Y_VIEWS;
const checked = only ? views.filter((view) => new RegExp(only).test(view)) : views;

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

// wholeLadder is the questions golden with a question at each point the full golden does not reach: one an orchestrator
// holds, with a rung it judged, one an assumption answered, and one merged into another.
function wholeLadder(): string {
  const view = JSON.parse(goldens["questions"] ?? "{}") as { questions: Record<string, unknown>[] };
  const [held, answered, merged] = view.questions;
  Object.assign(held ?? {}, {
    holder: { id: "worker-a", orchestrator: "orchestrator-worker-a", machine: "solo", until: 1790000999001 },
    ladder_trace: [
      { rung: 1, outcome: "no match", candidates: ["decision:decision-1"], by: "worker-a" },
      { rung: 3, outcome: "decided", candidates: ["PAY-12"], by: "worker-a", decision: "decision-7", rationale: "The budget is already approved." },
    ],
  });
  Object.assign(answered ?? {}, {
    status: "answered",
    closed_by: { id: "decision-9", status: "assumed", statement: "Keep the budget", rationale: "The budget is already approved.", decided_by: "worker-a" },
  });
  Object.assign(merged ?? {}, { status: "dismissed", merged_into: "question-1" });
  return JSON.stringify(view);
}

// Each kind of target, as the switcher names it, and what its surface holds that the audit should see.
const targetKinds = [
  { name: /Assistant/, ready: { role: "log", name: "Messages with the assistant" } },
  { name: /Orchestrator.*solo|Orchestrator.*atlas/, ready: { role: "log", name: /Event stream of the orchestrator/ } },
  { name: /Planning session.*lane-wo-running/, ready: { role: "region", name: "Pane of lane lane-wo-running" } },
  { name: /Lane.*lane-wo-1/, ready: { role: "log", name: "Event stream of lane lane-wo-1" } },
  { name: /Lane.*lane-wo-pane/, ready: { role: "region", name: "Pane of lane lane-wo-pane" } },
] as const;

suite.each(Object.entries(sizes))("at %s width", (size, viewport) => {
  describe.each(themes)("in the %s theme", (theme) => {
    it(
      "every view meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        for (const view of checked) {
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
      600_000,
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
      "Import, on the dry run, on the Restructure choice with a code tree left out, and on the refusal of a plan that changed, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        // Buttons and checks turn from disabled to enabled as the plan is read, and axe reads a color a transition
        // has reached, so each audit waits for the transitions to end.
        const check = async (name: string) => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
          found.push(...(await audit(page)).map((violation) => `${name} ${violation}`));
        };
        // The plan route answers the consolidation, with the code tree left out when the request names it, and the
        // layout that replaced it once a test says the layout changed.
        let layout: "read" | "changed" = "read";
        await page.route(/\/v1\/adoption\/plan$/, (route) => {
          const asked = route.request().postDataJSON() as { leave_out?: string[] };
          const name = layout === "changed" ? "single" : asked.leave_out?.includes("tools") ? "consolidate-left-out" : "consolidate";
          return route.fulfill({ status: 200, contentType: "application/json", body: goldens[`adoption-plan-${name}`] ?? "{}" });
        });
        await page.goto(`${base}/import`);
        await ready(page);
        await page.getByLabel("Folders", { exact: true }).fill("/work/ws");
        await page.waitForFunction(() => document.querySelector("select")?.value === "worker-1");
        await page.getByRole("button", { name: "Run dry run" }).click();
        await page.getByRole("region", { name: "Clones found" }).waitFor();
        await check("import dry run");
        await page.getByRole("checkbox", { name: "Restructure the planning repository as listed" }).check();
        // A tree left out checks once the plan is read again, so the check waits for the answer.
        await page.getByRole("checkbox", { name: /Leave out tools/ }).click();
        await page.waitForFunction(() => document.querySelector<HTMLInputElement>("input[type=checkbox]:checked + span code")?.textContent === "tools");
        await check("import restructure choice");
        await page.getByRole("button", { name: "Continue" }).click();
        await page.getByRole("button", { name: "Continue" }).click();
        layout = "changed";
        await page.getByRole("button", { name: "Start adopt session" }).click();
        await page.getByRole("alert").filter({ hasText: "The layout changed since you read the plan" }).waitFor();
        await check("import refusal of a plan that changed");
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      180_000,
    );

    it(
      "Import, on the dry run with each repository's practices closed and then opened, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const check = async (name: string) => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
          found.push(...(await audit(page)).map((violation) => `${name} ${violation}`));
        };
        await page.route(/\/v1\/adoption\/plan$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: goldens["adoption-plan-practices"] ?? "{}" }));
        await page.goto(`${base}/import`);
        await ready(page);
        await page.getByLabel("Folders", { exact: true }).fill("/work/ws");
        await page.waitForFunction(() => document.querySelector("select")?.value === "worker-1");
        await page.getByRole("button", { name: "Run dry run" }).click();
        await page.getByRole("region", { name: "Practices found" }).waitFor();
        await check("import dry run practices closed");
        for (const summary of await page.getByRole("region", { name: "Practices found" }).locator("summary").all()) {
          await summary.click();
        }
        await check("import dry run practices opened");
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      180_000,
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
      "the Sign in page, in each of its states, meets WCAG 2.2 A and AA",
      async () => {
        const found: string[] = [];
        const gone = (route: Route) => route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: { code: "session_required", message: "this session is not known" } }) });
        const refusing = (status: number, code: string) => (route: Route) =>
          route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error: { code, message: code } }) });
        // How the page is made to show each state: what the daemon answers, and what a person types and presses.
        const reach: Record<SignInStateId, (page: Page, context: BrowserContext) => Promise<void>> = {
          default: async (page, context) => {
            await context.route(/\/v1\//, gone);
            await page.goto(`${base}/needs-you`);
            await ready(page);
          },
          ended: async (page, context) => {
            await page.goto(`${base}/projects/default/roadmap`);
            await ready(page);
            // The session is gone from the daemon's side, which the page learns at its next read.
            await context.route(/\/v1\//, gone);
            await page.getByText("Your session ended.").waitFor({ timeout: 20_000 });
          },
          empty: async (page, context) => {
            await reach.default(page, context);
            await page.getByRole("button", { name: "Sign in", exact: true }).click();
            await page.getByText("Enter your passphrase.").waitFor();
          },
          wrong: async (page, context) => {
            await reach.default(page, context);
            await context.route(/\/v1\/sessions$/, refusing(401, "bad_passphrase"));
            await page.getByLabel("Passphrase", { exact: true }).fill("a guess at it");
            await page.getByRole("button", { name: "Sign in", exact: true }).click();
            await page.getByText("That passphrase does not match. Check it and try again.").waitFor();
          },
          locked: async (page, context) => {
            await reach.default(page, context);
            await context.route(/\/v1\/sessions$/, (route) =>
              route.fulfill({
                status: 429,
                contentType: "application/json",
                headers: { "Retry-After": "900" },
                body: JSON.stringify({ error: { code: "too_many_attempts", message: "too_many_attempts" } }),
              }),
            );
            await page.getByLabel("Passphrase", { exact: true }).fill("a guess at it");
            await page.getByRole("button", { name: "Sign in", exact: true }).click();
            await page.getByText("Too many sign-in attempts. Wait 15 minutes, then try again.").waitFor();
          },
          unreachable: async (page, context) => {
            await reach.default(page, context);
            await context.route(/\/v1\/sessions$/, (route) => route.abort());
            await page.getByLabel("Passphrase", { exact: true }).fill("a guess at it");
            await page.getByRole("button", { name: "Sign in", exact: true }).click();
            await page.getByText(/The daemon did not answer, so nothing was checked/).waitFor();
          },
          "not-set": async (page, context) => {
            await reach.default(page, context);
            await context.route(/\/v1\/sessions$/, refusing(401, "not_enrolled"));
            await page.getByLabel("Passphrase", { exact: true }).fill("a guess at it");
            await page.getByRole("button", { name: "Sign in", exact: true }).click();
            await page.getByText(/No passphrase is set yet/).waitFor();
          },
        };
        for (const state of signInStates) {
          const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
          await context.route(/\/v1\//, answer);
          const page = await context.newPage();
          await reach[state](page, context);
          // The button and the field change as the page settles, and axe reads a color a transition has reached.
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
          found.push(...(await audit(page)).map((violation) => `sign in, ${state} ${violation}`));
          await context.close();
        }
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      240_000,
    );

    it(
      "the Change passphrase form, empty, with errors at its fields, with a wrong current passphrase, with a refusal, and after a change, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        const answering = (status: number, body: unknown) =>
          page.route(/\/v1\/passphrase$/, (route) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) }));
        const fillIn = async (now: string) => {
          await page.getByLabel("Current passphrase", { exact: true }).fill(now);
          await page.getByLabel("New passphrase", { exact: true }).fill("copper-kettle-moss-2");
          await page.getByLabel("New passphrase, again", { exact: true }).fill("copper-kettle-moss-2");
        };
        const submit = () => page.getByRole("button", { name: "Change passphrase" }).click();
        await page.goto(`${base}/settings?section=passphrase`);
        await ready(page);
        await settle();
        found.push(...(await audit(page)).map((violation) => `change passphrase, empty ${violation}`));
        await submit();
        await page.getByText("Enter your current passphrase.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `change passphrase with errors at its fields ${violation}`));
        await answering(401, { error: { code: "bad_passphrase", message: "the passphrase does not match." } });
        await fillIn("not the passphrase");
        await submit();
        await page.getByText("That is not your current passphrase. Check it and try again.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `change passphrase with a wrong current passphrase ${violation}`));
        await answering(401, { error: { code: "not_enrolled", message: "no passphrase is set" } });
        await fillIn("tin-roof-anvil-1");
        await submit();
        await page.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `change passphrase with a refusal ${violation}`));
        await answering(200, { sessions_ended: 2, delegates: 1 });
        await fillIn("tin-roof-anvil-1");
        await submit();
        await page.getByText(/Passphrase changed\. 2 other sessions ended/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `change passphrase after a change ${violation}`));
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
      "the spend section, with a value the API would refuse, in review, with a refusal, read only for a member the API refuses, and its history, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/projects/default/settings?section=spend`);
        await ready(page);
        const daily = page.getByLabel(/^Project daily limit/);
        await daily.fill("-1");
        await page.getByText("Enter an amount in dollars from 0 to 1,000,000. 0 is none.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `spend with an error ${violation}`));
        await daily.fill("30");
        await page.getByRole("button", { name: "Review change" }).click();
        const dialog = page.getByRole("dialog", { name: "Review the spend change" });
        await dialog.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `spend review ${violation}`));
        const refused = (status: number, code: string, message: string) => (route: Route) =>
          route.request().method() === "POST" ? route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error: { code, message } }) }) : route.fallback();
        await page.route(/\/v1\/projects\/default\/policies$/, refused(400, "invalid", "invalid spend policy: project_daily_limit is 30, and the policy refuses it"));
        await dialog.getByRole("button", { name: "Save version 3" }).click();
        await dialog.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `spend review with a refusal ${violation}`));
        await page.route(/\/v1\/projects\/default\/policies$/, refused(403, "forbidden", "only a maintainer changes the spend policy"));
        await dialog.getByRole("button", { name: "Save version 3" }).click();
        await dialog.getByText(/only a maintainer changes the spend policy/).waitFor();
        await dialog.getByRole("button", { name: "Keep editing" }).click();
        await page.getByText(/you may read this policy, and not publish a version of it/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `spend read only ${violation}`));
        await page.getByRole("tab", { name: /History/ }).click();
        await page.getByRole("heading", { name: "Version 1" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `spend history ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the channel choice of each spend alert, chosen, and chosen for a channel the policy turned off, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/projects/default/settings?section=notify`);
        await ready(page);
        await page.getByRole("table", { name: "Channels for each spend alert" }).waitFor();
        await page.getByRole("checkbox", { name: /was reached, and work parked.*send to the webhook/ }).check();
        await page.getByText(/spend_channels spend_hard_limit to desktop → /).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `channel choice chosen ${violation}`));
        await page.getByRole("checkbox", { name: /Desktop notifications/ }).uncheck();
        await page.getByText("An alert is sent to the desktop, and desktop notifications are off. Turn them on, or clear the choice.").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `channel choice with an error ${violation}`));
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
      "the orchestrator's start at a usage limit, with its countdown, a start scheduled for the reset, and one that waits for the next reset meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const resets = Date.now() + 2 * 60 * 60 * 1000;
        const floorWith = (scheduled?: object) => {
          const floor = JSON.parse(goldens["shop-floor"] ?? "{}") as { workers: { worker: { id: string }; orchestrator?: object; orchestrator_hold?: object; scheduled_start?: object }[] };
          for (const line of floor.workers) {
            if (line.worker.id === "worker-a") {
              line.orchestrator = undefined;
              line.orchestrator_hold = { reason: "usage_limit", window: "five_hour", resets_at: resets };
              line.scheduled_start = scheduled;
            }
          }
          return floor;
        };
        const states: [string, object | undefined, RegExp][] = [
          ["at a usage limit", undefined, /The usage limit of the five-hour session window is reached/],
          ["with a start scheduled for the reset", { worker_id: "worker-a", starts_at: resets, scheduled_by: "member-1", scheduled_at: Date.now() }, /^Starts at /],
          ["with a start that waits for the next reset", { worker_id: "worker-a", scheduled_by: "member-1", scheduled_at: Date.now(), missed: 1 }, /starts at the next reset its worker reports/],
        ];
        for (const [name, scheduled, shows] of states) {
          await page.unroute(/\/views\/shop-floor/);
          await page.route(/\/views\/shop-floor/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(floorWith(scheduled)) }));
          await page.goto(`${base}/workers`);
          await ready(page);
          await page.getByText(shows).first().waitFor();
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
          found.push(...(await audit(page)).map((violation) => `orchestrator start ${name} ${violation}`));
        }
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    it(
      "the merge dialog, with a refusal, the assumption form, its refusal of a one-way question, and the notice that no orchestrator is judging meet WCAG 2.2 A and AA",
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
        await page.getByText(/No orchestrator is judging the questions that wait/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `open questions with no orchestrator ${violation}`));
        await page.getByRole("button", { name: "Merge Should the budget hold across restarts?" }).click();
        const merge = page.getByRole("dialog", { name: "Merge this question?" });
        await merge.waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `merge question dialog ${violation}`));
        await page.route(/\/v1\/projects\/default\/questions\/question-1\/merge$/, (route) =>
          route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { code: "resolved", message: "this question is resolved: answered" } }) }),
        );
        await merge.getByRole("radio").first().check();
        await merge.getByRole("button", { name: "Merge question" }).click();
        await merge.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `merge question dialog with a refusal ${violation}`));
        await merge.getByRole("button", { name: "Cancel" }).click();
        await merge.waitFor({ state: "hidden" });
        await page.getByRole("button", { name: "Assume an answer to Should the budget hold across restarts?" }).click();
        await page.getByRole("button", { name: "Record this assumption" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `assumption form ${violation}`));
        await page.getByRole("button", { name: "Assume an answer to Does the plan cover a restart?" }).click();
        await page.getByText(/A one-way question is answered by a person, never assumed/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `assumption refused for a one-way question ${violation}`));
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
      "the batch of questions in Needs you, with a one-way question waiting, with an answer of a member's own that asks why, and with a refusal, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        await page.goto(`${base}/needs-you?item=questions%3Agenerated-7`);
        await ready(page);
        const group = page.getByRole("group", { name: "Should the budget hold across restarts?" });
        await group.waitFor();
        await page.getByRole("status").filter({ hasText: "one-way" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `batch with a one-way question waiting ${violation}`));
        await page.getByRole("button", { name: "Accept all", exact: true }).click();
        await settle();
        found.push(...(await audit(page)).map((violation) => `batch after Accept all ${violation}`));
        await group.getByRole("button", { name: "Choose another" }).click();
        await group.getByRole("radio", { name: "Drop it" }).check();
        await group.getByRole("textbox", { name: "Why" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `batch with an answer that asks why ${violation}`));
        await group.getByRole("textbox", { name: "Why" }).fill("It costs a restart to keep.");
        await page.route(/\/v1\/projects\/default\/batches\/[^/]+\/answers$/, (route) =>
          route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { code: "resolved", message: "answer 1, for question question-1: this question already has its answer" } }) }),
        );
        await page.getByRole("button", { name: "Send 1 of 2 answers" }).click();
        await page.getByRole("alert").waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `batch with a refusal ${violation}`));
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
      "estimates, on a work order, on plan approval, and in the planning outline, confident, low, open, absent, pending, and refused, meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        const served = (body: string | undefined) => (route: Route) => route.fulfill({ status: 200, contentType: "application/json", body: body ?? "{}" });
        const estimate = /\/v1\/projects\/default\/work-orders\/wo-running\/estimate$/;
        // A work order whose estimate is confident beside a low-confidence baseline, with the explanations open.
        await page.goto(`${base}/projects/default/work-order?id=wo-running&tab=activity`);
        await ready(page);
        await page.getByRole("region", { name: "Estimate" }).first().waitFor();
        for (const control of await page.getByText("How it was made").all()) {
          await control.click();
        }
        await settle();
        found.push(...(await audit(page)).map((violation) => `work order estimate beside its baseline ${violation}`));
        for (const name of ["work-estimate-low", "work-estimate-open", "work-estimate-none", "work-estimate-absent"]) {
          await page.route(estimate, served(goldens[name]));
          await page.goto(`${base}/projects/default/work-order?id=wo-running&tab=activity`);
          await page.getByRole("region", { name: "Estimate" }).first().waitFor();
          await settle();
          found.push(...(await audit(page)).map((violation) => `work order ${name} ${violation}`));
        }
        await page.route(estimate, (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "the ledger could not be read" } }) }));
        await page.goto(`${base}/projects/default/work-order?id=wo-running`);
        await page.getByRole("alert").getByText(/the ledger could not be read/).first().waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `work order with an estimate it could not read ${violation}`));
        // Plan approval: the Estimates group of a version the forecast tick has not forecast, with estimates that
        // have no history, then of one it has forecast, with every kind of estimate and forecast.
        await page.goto(`${base}/projects/default/plan?id=generated-1&version=6`);
        await page.getByRole("region", { name: "Estimates" }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `plan approval with a forecast pending ${violation}`));
        await page.route(/\/v1\/projects\/default\/plans\/generated-1(\?.*)?$/, served(goldens["plan-estimates"]));
        await page.goto(`${base}/projects/default/plan?id=generated-1&version=6`);
        await page.getByRole("region", { name: "Estimates" }).getByText("Low confidence").first().waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `plan approval with every kind of estimate and forecast ${violation}`));
        // The planning outline, once a request stands for the tip of a branch whose plan the ledger holds.
        const outline = JSON.parse(goldens.outline ?? "{}") as Record<string, unknown>;
        outline.not_submitted = undefined;
        outline.approval = { id: "generated-13", state: "pending", plan_id: "generated-1", version: 6 };
        await page.route(/\/v1\/projects\/default\/lanes\/[^/]+\/outline$/, served(JSON.stringify(outline)));
        await page.goto(`${base}/projects/default/planning?lane=lane-wo-running`);
        await page.getByRole("heading", { level: 3, name: "Estimates" }).waitFor();
        await page.getByText("Low confidence").first().waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `planning outline with estimates and forecasts ${violation}`));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      180_000,
    );

    it(
      "the planning pane's History tab, with groups of every kind, with no finished work, and with a failure, meets WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        const history = /\/v1\/projects\/default\/measures\/history$/;
        const open = async () => {
          await page.goto(`${base}/projects/default/planning?lane=lane-wo-running`);
          await ready(page);
          await page.getByRole("tab", { name: "History" }).click();
        };
        await open();
        await page.getByRole("heading", { level: 3, name: "What the planning session sees" }).waitFor();
        await page.getByText("Low confidence").first().waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `history with confident and low groups ${violation}`));
        await page.route(history, (route) => route.fulfill({ status: 200, contentType: "application/json", body: goldens["history-empty"] ?? "{}" }));
        await open();
        await page.getByText("No finished work yet.", { exact: false }).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `history with no finished work ${violation}`));
        await page.route(history, (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "the ledger could not be read" } }) }));
        await open();
        await page.getByRole("alert").getByText(/the ledger could not be read/).waitFor();
        await settle();
        found.push(...(await audit(page)).map((violation) => `history with a failure ${violation}`));
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
        await page.goto(`${base}/needs-you?kind=cards&item=repository_creation%3Agenerated-1%2Fghost`);
        await ready(page);
        await page.getByRole("button", { name: "Skip", exact: true }).click();
        await page.getByRole("dialog", { name: "Leave ghost uncreated?" }).waitFor();
        found.push(...(await audit(page)).map((violation) => `skip repository dialog ${violation}`));
        await page.getByRole("button", { name: "Skip repository" }).click();
        await page.getByText("Say why you skip it.").waitFor();
        found.push(...(await audit(page)).map((violation) => `skip repository dialog with its error ${violation}`));
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

    // The dialogs of the installation's pages, which the coverage test holds to an axe state each: the project
    // dialogs, the join token, the delegate dialogs, the sign-out of every session, the repository dialogs, the
    // risk a member reads, a roster change in review, and the choice of a worker for the assistant.
    it(
      "the dialogs of Projects, Workers, Members, Repositories, Settings, Agents, and the assistant meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        const found: string[] = [];
        const settle = async () => {
          await page.mouse.move(0, 0);
          await page.waitForTimeout(600);
        };
        const check = async (name: string, dialog: Locator) => {
          await dialog.waitFor();
          await settle();
          found.push(...(await audit(page)).map((violation) => `${name} ${violation}`));
        };
        const served = (body: unknown, status = 200) => (route: Route) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
        // The project dialogs, with an archived project beside the active one.
        const listed = [
          { id: "default", name: "default", display_name: "Default", state: "active", created_at: 1, repositories: [] },
          { id: "old", name: "old", display_name: "Old", state: "archived", created_at: 1, repositories: [] },
        ];
        await page.route(/\/v1\/projects$/, (route) => (route.request().method() === "GET" ? served({ projects: listed })(route) : route.fallback()));
        await page.goto(`${base}/projects`);
        await ready(page);
        await page.getByRole("row", { name: /Old/ }).waitFor();
        await page.getByRole("button", { name: "Create project" }).first().click();
        await check("create project dialog", page.getByRole("dialog", { name: "Create project" }));
        await page.keyboard.press("Escape");
        await page.getByRole("button", { name: "Archive…" }).click();
        await check("archive project dialog", page.getByRole("dialog", { name: /^Archive / }));
        await page.keyboard.press("Escape");
        await page.getByRole("button", { name: "Actions for Default" }).click();
        await page.getByRole("menuitem", { name: "Rename…" }).click();
        await check("rename project dialog", page.getByRole("dialog", { name: /^Rename / }));
        await page.keyboard.press("Escape");
        await page.getByRole("button", { name: "Remove…" }).click();
        await check("remove project dialog", page.getByRole("dialog", { name: /^Remove / }));
        await page.keyboard.press("Escape");
        await page.locator("input[type=file]").setInputFiles({ name: "tidewater.db", mimeType: "application/octet-stream", buffer: Buffer.from("SQLite format 3\0") });
        await check("import backup dialog", page.getByRole("dialog", { name: "Import backup" }));
        await page.unroute(/\/v1\/projects$/);
        // The join token, the delegate dialogs, and the sign-out of every session.
        // Only an installation in group mode offers it.
        await page.route(/\/v1\/health$/, served({ status: "ok", revision: 1, mode: "group", version: "v0.1.0-alpha.16" }));
        await page.goto(`${base}/workers?dialog=token`);
        await check("join token dialog", page.getByRole("dialog", { name: "Create join token" }));
        await page.goto(`${base}/workers?dialog=revoke-token`);
        await check("revoke join token dialog", page.getByRole("dialog", { name: "Revoke join token" }));
        await page.unroute(/\/v1\/health$/);
        await page.goto(`${base}/members?dialog=delegate`);
        const create = page.getByRole("dialog", { name: "Create delegate" });
        await check("create delegate dialog", create);
        await page.route(/\/v1\/delegates$/, (route) =>
          route.request().method() === "POST"
            ? served({ id: "delegate-1", label: "ci", project: "default", kind: "delegate", created_at: 1790000000000, expires_at: 1790003600000, last_used_at: null, revoked_at: null, state: "active", token: "rsd_0123456789abcdef" })(route)
            : route.fallback(),
        );
        await create.getByRole("textbox", { name: "Label" }).fill("ci");
        await create.getByRole("button", { name: "Create", exact: true }).click();
        await check("delegate created dialog", page.getByRole("dialog", { name: "Delegate created" }));
        await page.goto(`${base}/members?dialog=sign-out-all`);
        await check("sign out all sessions dialog", page.getByRole("dialog", { name: "Sign out all sessions" }));
        // The repository dialogs.
        await page.goto(`${base}/projects/default/repositories?dialog=register`);
        await check("register repositories dialog", page.getByRole("dialog", { name: /^Register repositories in / }));
        // Start a project from a template: the first question unanswered, several repositories with rows, the wait for gh, gh
        // signed out, the owners loaded, the wait for the worker, the worker's refusal brought back to the form, and what was
        // written, with the hand-off to registration and without it, and with a failed push.
        const asked = { id: "request-1", worker: "worker-1", requested_by: "member", requested_at: Date.now(), expires_at: Date.now() + 120_000, shape: "workspace", path: "/srv/code/harbor", first_commit: true, state: "offered", registrable: false };
        let reading: Record<string, unknown> = asked;
        await page.route(/\/v1\/workers$/, served({ workers: [{ id: "worker-1", name: "atlas", epoch: 1, protocol: 7 }, { id: "worker-2", name: "elder", epoch: 1, protocol: 4 }] }));
        await page.route(/\/v1\/workers\/worker-1\/template-writes$/, (route) => served(asked, 202)(route));
        await page.route(/\/v1\/workers\/worker-1\/template-writes\/request-1$/, (route) => served(reading)(route));
        // The forge account read: asked with a POST, and read until the worker reports what its gh can do.
        const readOf = (state: string, report?: Record<string, unknown>) => ({ id: "read-1", worker: "worker-1", requested_by: "member", requested_at: Date.now(), expires_at: Date.now() + 120_000, forge: "github", state, report });
        let account: Record<string, unknown> = readOf("offered");
        await page.route(/\/v1\/workers\/worker-1\/forge-account-reads$/, (route) => served(readOf("offered"), 202)(route));
        await page.route(/\/v1\/workers\/worker-1\/forge-account-reads\/read-1$/, (route) => served(account)(route));
        const report = {
          request: "request-1",
          outcome: "written",
          project: "harbor",
          files: ["AGENTS.md", "Makefile", "plan/goal.md"],
          commit: "3f9c2ab",
          clone: true,
          commands: ["make -C /srv/code/harbor setup", "ropeshift project create harbor", "make -C /srv/code/harbor register"],
        };
        const start = page.getByRole("dialog", { name: "Start a project from a template" });
        const send = async () => {
          await start.getByRole("radio", { name: /^One repository/ }).check();
          await start.getByRole("textbox", { name: "Folder" }).fill("/srv/code/harbor");
          await start.getByRole("button", { name: "Write files" }).click();
        };
        await page.goto(`${base}/projects?dialog=template`);
        await check("start a project from a template dialog, the first question unanswered", start);
        await start.getByRole("radio", { name: /^Several repositories/ }).check();
        await start.getByRole("button", { name: "Add", exact: true }).click();
        await start.getByRole("group", { name: "Code repository 1" }).getByRole("textbox", { name: "Name" }).fill("api");
        await check("start a project from a template dialog, several repositories with rows", start);
        await start.getByRole("textbox", { name: "Folder" }).fill("/srv/code/harbor");
        await start.getByRole("textbox", { name: "Project name" }).fill("Tide Water");
        await start.getByRole("button", { name: "Write files" }).click();
        await start.getByText(/lowercase letters and digits in words joined by single hyphens/).waitFor();
        await check("start a project from a template dialog, a project name that is refused", start);
        await start.getByRole("textbox", { name: "Project name" }).fill("");
        await start.getByRole("radio", { name: "Create on GitHub" }).check();
        await start.getByText("Checking gh on atlas").waitFor();
        await check("start a project from a template dialog, checking gh", start);
        account = readOf("reported", { request: "read-1", state: "gh_signed_out", detail: "Run `gh auth login --hostname github.com` on atlas", remedy: "gh auth login --hostname github.com" });
        await start.getByRole("button", { name: "Check again" }).waitFor();
        await check("start a project from a template dialog, gh signed out", start);
        account = readOf("reported", { request: "read-1", state: "ok", login: "ana", git_protocol: "ssh", owners: ["ana", "acme"] });
        await start.getByRole("button", { name: "Check again" }).click();
        await start.getByRole("group", { name: "Remote" }).getByRole("combobox", { name: "Owner" }).waitFor();
        await check("start a project from a template dialog, owners loaded", start);
        await page.goto(`${base}/projects?dialog=template`);
        await send();
        await start.getByText("Waiting for atlas").waitFor();
        await check("start a project from a template dialog, waiting", start);
        reading = { ...asked, state: "refused", report: { request: "request-1", outcome: "refused", reason: "path_not_empty", detail: "the folder holds files" } };
        await start.getByText(/the folder holds files \(path_not_empty\)/).waitFor();
        await check("start a project from a template dialog, refused", start);
        reading = { ...asked, state: "written", registrable: true, report };
        await start.getByRole("button", { name: "Write files" }).click();
        await start.getByText(/atlas wrote 3 files/).waitFor();
        await check("start a project from a template dialog, written with Register", start);
        reading = { ...asked, state: "written", registrable: false, report };
        await page.goto(`${base}/projects?dialog=template`);
        await send();
        await start.getByText(/atlas wrote 3 files/).waitFor();
        await check("start a project from a template dialog, written without Register", start);
        const failedPush = {
          ...report,
          repositories: [
            { name: "harbor", path: ".", commit: "3f9c2ab", remote: "git@github.com:acme/harbor.git", steps: [{ step: "create", outcome: "done" }, { step: "origin", outcome: "done" }, { step: "push", outcome: "failed", detail: "remote: Permission denied" }] },
            { name: "api", path: "repos/api", commit: "9d1e4f0", steps: [{ step: "create", outcome: "skipped" }, { step: "origin", outcome: "skipped" }, { step: "push", outcome: "skipped" }] },
          ],
          remaining: ["git -C /srv/code/harbor push --set-upstream origin main", "gh auth setup-git"],
        };
        reading = { ...asked, state: "written", registrable: true, report: failedPush };
        await page.goto(`${base}/projects?dialog=template`);
        await send();
        await start.getByText(/atlas wrote 3 files/).waitFor();
        await check("start a project from a template dialog, written with a failed push", start);
        await page.unroute(/\/v1\/workers$/);
        await page.goto(`${base}/projects/default/repositories?repo=id-api`);
        await ready(page);
        await page.getByRole("button", { name: "Remove…" }).click();
        await check("remove repository dialog", page.getByRole("dialog", { name: /^Remove / }));
        // The dialogs of the practices check: acknowledging a finding, empty and with a refusal of its reason, and withdrawing
        // the acknowledgement.
        await page.goto(`${base}/projects/default/repositories?dialog=acknowledge`);
        const acknowledge = page.getByRole("dialog", { name: "Acknowledge finding" });
        await check("acknowledge finding dialog", acknowledge);
        await acknowledge.getByRole("button", { name: "Acknowledge", exact: true }).click();
        await acknowledge.getByText("Say why this finding stays open.").waitFor();
        await check("acknowledge finding dialog with a reason asked for", acknowledge);
        await page.goto(`${base}/projects/default/repositories?dialog=withdraw`);
        await check("withdraw acknowledgement dialog", page.getByRole("dialog", { name: "Withdraw acknowledgement" }));
        // The risk a member reads before standing behind a worker's level.
        await page.route(/\/v1\/workers\/worker-1\/autonomy$/, served({ as_of: 1790000000000, worker_id: "worker-1", sign_in: "subscription", level: "attended", chosen_level: "assisted", risk_acknowledged: false, may_claim: false }));
        await page.route(/\/v1\/autonomy\/risk$/, served({ statement: "A worker at this level runs the agent's commands without asking.", digest: "sha256:abc" }));
        await page.goto(`${base}/settings?section=autonomy`);
        await ready(page);
        await page.getByRole("button", { name: "Read the stated risk" }).click();
        await check("stated risk dialog", page.getByRole("dialog", { name: "The stated risk" }));
        // A roster change in review.
        await page.goto(`${base}/projects/default/agents?tab=guide`);
        await ready(page);
        await page.getByRole("textbox", { name: "Routing guide" }).fill("Pick the lightest agent that can do the work.");
        await page.getByRole("button", { name: "Review change" }).click();
        await check("roster review dialog", page.getByRole("dialog", { name: "Review the routing guide change" }));
        // The assistant asks which worker runs a stopped conversation when several can.
        const targets = JSON.parse(goldens["conversation-targets"] ?? "{}") as { assistant: { conversation: Record<string, unknown>; workers: unknown[] } };
        const stopped = { ...targets.assistant.conversation, actual_state: "stopped", desired_state: "stopped", worker_id: undefined };
        const workers = [
          { id: "worker-atlas", name: "atlas", available: true },
          { id: "worker-basalt", name: "basalt", available: true },
        ];
        await page.route(/\/conversation-targets$/, served({ ...targets, assistant: { ...targets.assistant, conversation: stopped, workers } }));
        await page.route(/\/conversations$/, (route) => (route.request().method() === "GET" ? served({ as_of: 1790000010000, conversations: [stopped], workers })(route) : route.fallback()));
        await page.goto(`${base}/projects/default/roadmap`);
        await ready(page);
        await page.getByRole("button", { name: "Conversation panel" }).click();
        const dock = page.getByRole("complementary", { name: "Conversation" });
        // Which worker runs the session is asked from the workers the dock has read, and the dock reads them once it
        // opens. Until the stopped conversation shows its Resume button, the dock knows no worker and the first key or
        // the send starts a session with no question, so the dialog never opens.
        await dock.getByRole("button", { name: "Resume", exact: true }).waitFor();
        await dock.getByRole("textbox", { name: "Message to the assistant" }).fill("Hello");
        await page.keyboard.press("Control+Enter");
        await check("choose a worker dialog", page.getByRole("dialog", { name: "Choose a worker" }));
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      240_000,
    );

    // The Notices tab, a lane's messages, Usage's cost by lane, the Workers page's drivers, and the Templates view, in
    // the states their full goldens do not reach: nothing to list, a store the control plane lacks, a read refused, and
    // workers that reported each kind of doctor result.
    it(
      "the notices, lane messages, lane costs, drivers, and templates, empty, refused, and with every doctor result, meet WCAG 2.2 A and AA",
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
        await serve(/\/v1\/templates$/, refused, 500);
        await check("templates refused", "/templates");
        await context.close();
        expect(found.map((line) => `${size} ${theme} ${line}`)).toEqual([]);
      },
      120_000,
    );

    // The whole ladder (FR-QD-13), in the states the full goldens do not reach: a question an orchestrator holds with a
    // judged rung, one answered by an assumption, and one merged, each with its trace open, on Open questions and on a
    // lane's and a work order's page.
    it(
      "the questions, held, judged, answered, and merged, with their traces open, meet WCAG 2.2 A and AA",
      async () => {
        const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
        await context.route(/\/v1\//, answer);
        const page = await context.newPage();
        await page.route(/\/views\/questions(\?.*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: wholeLadder() }));
        const found: string[] = [];
        for (const address of ["/projects/default/questions?status=all", "/projects/default/questions?status=answered", "/projects/default/lane?id=lane-wo-running", "/projects/default/work-order?id=wo-running"]) {
          await page.goto(`${base}${address}`);
          await ready(page);
          await page.getByText("How it got here").first().waitFor({ state: "attached" });
          await page.evaluate(() => {
            for (const trace of document.querySelectorAll("details")) {
              trace.open = true;
            }
          });
          await page.waitForTimeout(300);
          found.push(...(await audit(page)).map((violation) => `${address} ${violation}`));
        }
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

    // At desktop width the Machines table of a worker that reported every measure, under its whole 32-character
    // ID, fits its panel: its last column is in view, and none waits behind a sideways scroll.
    if (size === "desktop") {
      it(
        "the Machines table keeps its last column in view",
        async () => {
          const gib = 1024 ** 3;
          const floor = JSON.parse(goldens["shop-floor"] ?? "{}");
          for (const line of floor.workers) {
            line.worker.id = "3f9a2c7e5b1d4086a4e2c8f06d9b1e57";
            line.worker.reported_at = line.worker.last_handshake_at;
            line.report_freshness = "last_reported";
            line.worker.report = {
              interval_ms: 15000,
              machine: {
                cores: 8,
                cpu_percent: 42.4,
                load_1: 1,
                load_5: 2.5,
                load_15: 3,
                memory_total_bytes: 32 * gib,
                memory_used_bytes: 12 * gib,
                memory_available_bytes: 20 * gib,
                memory_pressure_some: 1.5,
                volumes: [
                  { roles: ["state"], free_bytes: 5 * gib, total_bytes: 100 * gib },
                  { roles: ["worktrees"], free_bytes: 120 * gib, total_bytes: 500 * gib },
                ],
              },
              caps: { max_lanes: 7, max_lanes_set: false, max_lanes_per_sign_in: 0 },
              sessions: [],
              warnings: [],
            };
          }
          const context = await browser.newContext({ viewport, colorScheme: theme, reducedMotion: "reduce" });
          await context.route(/\/v1\//, answer);
          const page = await context.newPage();
          await page.route(/\/views\/shop-floor$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(floor) }));
          await page.goto(`${base}/workers`);
          await ready(page);
          const region = page.locator('.table-wrap[aria-label="Machines"]');
          await region.locator('td[data-label="Freshness"]', { hasText: "Resources measured" }).waitFor();
          const [table, panel] = await region.evaluate((wrap) => [wrap.scrollWidth, wrap.clientWidth]);
          expect(table, "the Machines table's width, against its panel's").toBeLessThanOrEqual(panel ?? 0);
          await context.close();
        },
        60_000,
      );
    }
  });
});

// The coverage test holds the dialogs of the source to the list of states. This holds the list to the browser: each
// dialog it names was open during an audit of the tests above, which it reads, so it runs with all of them.
suite("the dialogs the states list", () => {
  it("were each open during an audit", () => {
    const unseen = dialogStates.filter((row) => ![...dialogsSeen].some((name) => row.title.test(name))).map((row) => `${row.file}: ${String(row.title)}`);
    expect(unseen).toEqual([]);
  });
});
