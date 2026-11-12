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
    return json(goldens["needs-you"] ?? "{}");
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
  if (path === "/v1/events") {
    return route.fulfill({ status: 200, contentType: "text/event-stream", body: ": open\n\n" });
  }
  return json(JSON.stringify({ error: { code: "not_found", message: "not served in this test" } }), 404);
}

// The address of every view, each with the records a person would open.
const views = [
  "/needs-you",
  "/needs-you?item=questions%3Agenerated-7",
  "/needs-you?item=plan_approval%3Agenerated-11",
  "/projects/default/roadmap",
  "/projects/default/plans",
  "/projects/default/documents?kind=requirements",
  "/projects/default/documents?kind=spec",
  "/projects/default/documents?path=docs%2Fspecs%2Fretry.md",
  "/projects/default/decisions",
  "/projects/default/questions",
  "/projects/default/work?group=all",
  "/projects/default/work-order?id=wo-accept",
  "/projects/default/floor",
  "/projects/default/lane?id=lane-wo-running",
  "/projects/default/measures",
  "/usage",
  "/workers",
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
      "the command palette and the shortcut sheet meet WCAG 2.2 A and AA",
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
