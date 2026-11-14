// Captures the views a screenshot run lists, from a running daemon's web UI, at desktop and phone width in
// both themes, and renders `ropeshift top --once` as an image. test/screenshots runs it with the path of a
// listing it wrote:
//
//   {"base": "http://127.0.0.1:PORT", "passphrase": "...", "out": "DIR", "top": "DIR/top.txt", "private": ["..."],
//    "views": [{"name": "floor", "path": "/projects/orders/floor", "dock": "^Lane", "dock_ready": "^Event stream"}]}
//
// Each view becomes <name>-<theme>-<width>.png in out. A view whose text holds one of the private words fails
// the run, and its image stays for a person to see why. The browser is Playwright's own Chromium, which
// `npx playwright-core install chromium-headless-shell` puts in place.

import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { chromium } from "playwright-core";

const listing = JSON.parse(readFileSync(process.argv[2], "utf8"));
const sizes = [
  { width: 1440, height: 900 },
  { width: 390, height: 844 },
];
const themes = ["light", "dark"];

// ready waits until the view's title stands, no skeleton holds the place of its body, no spinner turns, and
// the fonts have loaded.
async function ready(page) {
  await page.locator("main h1:visible").first().waitFor({ timeout: 20_000 });
  await page.waitForFunction(
    () => !document.querySelector("main .skeleton-rows, main .skeleton, [aria-busy='true'], .spinner"),
    null,
    { timeout: 20_000 },
  );
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(400);
}

// dock opens the conversation panel and switches it to the target whose name matches the listing's pattern.
async function dock(page, target, readyLog) {
  await page.getByRole("button", { name: "Conversation panel" }).first().click();
  const panel = page.getByRole("complementary", { name: "Conversation" });
  await panel.getByRole("log", { name: "Messages with the assistant" }).waitFor({ timeout: 20_000 });
  if (readyLog) {
    await panel.getByRole("button", { name: "Switch target" }).click();
    await page.getByRole("menu", { name: "Switch conversation target" }).getByRole("menuitemradio", { name: new RegExp(target) }).first().click();
    await panel.getByRole("log", { name: new RegExp(readyLog) }).waitFor({ timeout: 20_000 });
  }
  await page.waitForTimeout(400);
}

function escapeHTML(text) {
  return text.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
}

// top renders the text `ropeshift top --once` printed as a terminal shows it.
async function top(browser) {
  if (!listing.top || !existsSync(listing.top)) {
    return;
  }
  const text = readFileSync(listing.top, "utf8").replace(/\s+$/, "");
  const page = await browser.newPage({ viewport: { width: 1600, height: 400 }, deviceScaleFactor: 2 });
  await page.setContent(
    `<!doctype html><html><body style="margin:0;background:#000"><pre id="top" style="display:inline-block;margin:0;padding:20px 24px;` +
      `background:#000;color:#e6e6e6;font:15px/1.6 'DejaVu Sans Mono',Menlo,Consolas,monospace">${escapeHTML(text)}</pre></body></html>`,
  );
  await page.evaluate(() => document.fonts.ready);
  await page.locator("#top").screenshot({ path: join(listing.out, "top.png") });
  await page.close();
}

// capture takes one view's image, and answers why it could not, or nothing.
async function capture(page, view, name) {
  try {
    await page.goto(`${listing.base}${view.path}`);
    await ready(page);
    if (view.dock) {
      await dock(page, view.dock, view.dock_ready);
    }
    const shown = await page.evaluate(() =>
      [document.body.innerText, ...[...document.querySelectorAll("input, textarea")].map((field) => field.value)].join("\n"),
    );
    // The page's content policy refuses an injected style, so the screenshot itself hides the caret and
    // finishes any animation.
    await page.screenshot({ path: join(listing.out, name), caret: "hide", animations: "disabled" });
    taken++;
    const leaked = (listing.private ?? []).filter((word) => shown.includes(word));
    return leaked.length > 0 ? `shows ${leaked.join(", ")}` : "";
  } catch (error) {
    return String(error).split("\n")[0];
  }
}

const browser = await chromium.launch({ headless: true });
const failed = [];
let taken = 0;
try {
  for (const theme of themes) {
    for (const viewport of sizes) {
      const context = await browser.newContext({
        viewport,
        deviceScaleFactor: 2,
        colorScheme: theme,
        reducedMotion: "reduce",
        locale: "en-US",
        timezoneId: "UTC",
      });
      const signedIn = await context.request.post(`${listing.base}/v1/sessions`, { data: { passphrase: listing.passphrase } });
      if (!signedIn.ok()) {
        throw new Error(`the sign-in answered ${signedIn.status()}: ${await signedIn.text()}`);
      }
      // Each view starts from what a new browser holds, so a panel one view opened is closed on the next.
      await context.addInitScript(() => {
        try {
          localStorage.clear();
          sessionStorage.clear();
        } catch {
          // A page with no storage starts clean anyway.
        }
      });
      let page = await context.newPage();
      for (const view of listing.views) {
        const name = `${view.name}-${theme}-${viewport.width}.png`;
        // A navigation the browser lost, such as to a change in the machine's network, can leave a page
        // that interrupts every later one, so a view that fails gets one more try on a new page.
        let error = await capture(page, view, name);
        if (error && !error.startsWith("shows ")) {
          await page.close().catch(() => {});
          page = await context.newPage();
          error = await capture(page, view, name);
        }
        if (error) {
          failed.push(`${name} (${view.path}): ${error}`);
        }
      }
      await context.close();
    }
  }
  await top(browser);
} finally {
  await browser.close();
}
console.log(`captured ${taken} views`);
if (failed.length > 0) {
  console.error(`${failed.length} views were not captured:\n${failed.join("\n")}`);
  process.exit(1);
}
