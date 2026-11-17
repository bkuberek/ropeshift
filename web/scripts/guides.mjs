// Walks the guides' steps through the real web UI and captures the images the guides show, in one theme. The
// guide walk of test/screenshots runs it with the path of a listing it wrote:
//
//   {"base": "http://127.0.0.1:PORT", "passphrase": "...", "out": "DIR", "theme": "light",
//    "vars": {"src": "/tmp/ropeshift-guides/src"}, "private": ["..."], "capture": ["get-started"],
//    "guides": [{"guide": "get-started", "signed_out": true, "steps": [...]}]}
//
// A guide is a list of steps, and a step has an optional route to open, a list of actions, and an optional capture:
//
//   {"step": "sign in", "route": "/",
//    "actions": [{"fill": {"label": "Passphrase", "value": "{{passphrase}}"}},
//                {"click": {"role": "button", "name": "Sign in"}},
//                {"waitFor": {"text": "Overview"}},
//                {"press": "Escape"}],
//    "capture": {"name": "get-started-02", "clip": "window"}}
//
// An action presses a control by role and name (`click`), types into a field by label (`fill`), presses a key
// (`press`), or waits for text to show (`waitFor`), for 20 seconds or the `timeout` a wait sets in milliseconds. `in` limits a click, fill, or wait to the region, dialog, or
// other element named by a role and name. A value or a route may hold {{name}}, which a variable of the listing,
// or the passphrase, replaces. A capture is the window, or the element a selector or a role and name names,
// with `pad` pixels around it, written as <name>-<theme>.png at 1440 by 900 and twice the pixel density. A
// capture's `width` and `height` enlarge the window for that image alone, for a page that does not fit in 1440 by 900.
//
// A step that fails stops its guide and the walk, and the run names the guide and the step. Each capture also
// fails when the page shows one of the private words, and its image stays for a person to see why. A guide the
// listing does not name in `capture` is walked and takes no images, so a later guide starts from its state. The
// browser is Playwright's own Chromium, or the executable ROPESHIFT_TEST_CHROME names.

import { mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

import { chromium } from "playwright-core";

const viewport = { width: 1440, height: 900 };
const patience = 20_000;

// expand replaces each {{name}} with the variable it names, and refuses a name no variable holds.
export function expand(text, vars) {
  return text.replace(/\{\{\s*([\w-]+)\s*\}\}/g, (_, name) => {
    if (!Object.hasOwn(vars, name)) {
      throw new Error(`no variable {{${name}}} in the listing`);
    }
    return vars[name];
  });
}

// scoped is where a locator searches: the whole page, or the element a role and name name.
function scoped(page, within) {
  return within ? page.getByRole(within.role, { name: within.name, exact: within.exact ?? true }) : page;
}

// quiet waits until no skeleton holds the place of a body, no spinner turns or region is busy, and the fonts
// have loaded.
async function quiet(page, timeout) {
  await page.waitForFunction(
    () => !document.querySelector("main .skeleton-rows, main .skeleton, [aria-busy='true'], .spinner"),
    null,
    { timeout },
  );
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(400);
}

// arrive waits for a page's title as well.
async function arrive(page, timeout) {
  await page.locator("h1:visible").first().waitFor({ timeout });
  await quiet(page, timeout);
}

// describe says what an action does, for the failure that names it.
function describe(verb, spec) {
  switch (verb) {
    case "click":
      return `press the ${spec.role} "${spec.name}"`;
    case "fill":
      return `fill the field "${spec.label}"`;
    case "press":
      return `press the key ${spec}`;
    case "waitFor":
      return `wait for the text "${spec.text}"`;
    default:
      return `run ${verb}`;
  }
}

// perform runs one action. Each locator must name one element, so an ambiguous step fails instead of pressing
// whichever control came first.
async function perform(page, action, vars, timeout) {
  const verbs = Object.keys(action);
  if (verbs.length !== 1) {
    throw new Error(`an action has one verb, not ${verbs.length}: ${verbs.join(", ")}`);
  }
  const [verb] = verbs;
  try {
    await act(page, verb, action[verb], vars, timeout);
  } catch (error) {
    throw new Error(`could not ${describe(verb, action[verb])}: ${String(error.message ?? error).split("\n")[0]}`);
  }
}

async function act(page, verb, spec, vars, timeout) {
  switch (verb) {
    case "click": {
      const name = expand(spec.name, vars);
      await scoped(page, spec.in).getByRole(spec.role, { name, exact: spec.exact ?? true }).click({ timeout });
      return;
    }
    case "fill": {
      const label = expand(spec.label, vars);
      await scoped(page, spec.in).getByLabel(label, { exact: spec.exact ?? true }).fill(expand(spec.value, vars), { timeout });
      return;
    }
    case "press":
      await page.keyboard.press(expand(spec, vars));
      return;
    case "waitFor": {
      const text = expand(spec.text, vars);
      // A wait for what the daemon does in its own time, such as an orchestrator starting, may take longer.
      await scoped(page, spec.in).getByText(text, { exact: spec.exact ?? false }).first().waitFor({ state: "visible", timeout: spec.timeout ?? timeout });
      return;
    }
    default:
      throw new Error(`no action ${verb}; the actions are click, fill, press, and waitFor`);
  }
}

// capture takes a step's image after reading the page's text for private words.
async function capture(page, spec, options, timeout) {
  const file = join(options.out, `${spec.name}-${options.theme}.png`);
  const shown = await page.evaluate(() =>
    [document.body.innerText, ...[...document.querySelectorAll("input, textarea")].map((field) => field.value)].join("\n"),
  );
  // The page's content policy refuses an injected style, so the screenshot itself hides the caret and finishes
  // any animation.
  const shot = { path: file, caret: "hide", animations: "disabled" };
  const clip = spec.clip;
  if (clip === "window") {
    await page.screenshot(shot);
  } else {
    const target = clip.selector ? page.locator(clip.selector) : page.getByRole(clip.role, { name: clip.name, exact: clip.exact ?? true });
    await target.waitFor({ state: "visible", timeout });
    if (clip.pad) {
      await target.scrollIntoViewIfNeeded({ timeout });
      const box = await target.boundingBox();
      const frame = page.viewportSize() ?? viewport;
      const x = Math.max(0, box.x - clip.pad);
      const y = Math.max(0, box.y - clip.pad);
      await page.screenshot({
        ...shot,
        clip: {
          x,
          y,
          width: Math.min(frame.width - x, box.width + 2 * clip.pad),
          height: Math.min(frame.height - y, box.height + 2 * clip.pad),
        },
      });
    } else {
      await target.screenshot(shot);
    }
  }
  const leaked = (options.private ?? []).filter((word) => shown.includes(word));
  if (leaked.length > 0) {
    throw new Error(`the page shows ${leaked.join(", ")}; the image ${spec.name} stays for a person to see`);
  }
}

// runGuide walks a guide's steps on a page, and answers how many images it took and why it stopped, if it did.
// options: base, out, theme, vars, private, capture (whether to take the images), and timeout of each wait.
export async function runGuide(page, guide, options) {
  const timeout = options.timeout ?? patience;
  const vars = options.vars ?? {};
  let taken = 0;
  for (const [index, step] of guide.steps.entries()) {
    try {
      page.setDefaultTimeout(timeout);
      if (step.route) {
        await page.goto(`${options.base}${expand(step.route, vars)}`);
        await arrive(page, timeout);
      }
      for (const action of step.actions ?? []) {
        await perform(page, action, vars, timeout);
      }
      if (step.capture && options.capture) {
        // A larger window holds for this image alone, and the walk goes on at the size it had.
        const was = page.viewportSize() ?? viewport;
        const larger = Boolean(step.capture.width || step.capture.height);
        if (larger) {
          await page.setViewportSize({ width: step.capture.width ?? was.width, height: step.capture.height ?? was.height });
        }
        try {
          await quiet(page, timeout);
          await capture(page, step.capture, options, timeout);
        } finally {
          if (larger) {
            await page.setViewportSize(was);
          }
        }
        taken++;
      }
    } catch (error) {
      // What the page showed at the failure helps a person see why, so it is kept beside the images.
      await page.screenshot({ path: join(options.out, `failed-${guide.guide}-${index + 1}-${options.theme}.png`) }).catch(() => {});
      return { taken, failure: `${guide.guide} step ${index + 1} (${step.step}): ${String(error.message ?? error).split("\n")[0]}` };
    }
  }
  return { taken };
}

async function main() {
  const listing = JSON.parse(readFileSync(process.argv[2], "utf8"));
  mkdirSync(listing.out, { recursive: true });
  const executable = process.env.ROPESHIFT_TEST_CHROME;
  const browser = await chromium.launch({ headless: true, executablePath: executable || undefined });
  const vars = { ...listing.vars, passphrase: listing.passphrase };
  let taken = 0;
  let failure = "";
  try {
    for (const guide of listing.guides) {
      const context = await browser.newContext({
        viewport,
        deviceScaleFactor: 2,
        colorScheme: listing.theme,
        reducedMotion: "reduce",
        locale: "en-US",
        timezoneId: "UTC",
      });
      if (!guide.signed_out) {
        const signedIn = await context.request.post(`${listing.base}/v1/sessions`, { data: { passphrase: listing.passphrase } });
        if (!signedIn.ok()) {
          throw new Error(`the sign-in answered ${signedIn.status()}: ${await signedIn.text()}`);
        }
      }
      const page = await context.newPage();
      const result = await runGuide(page, guide, {
        base: listing.base,
        out: listing.out,
        theme: listing.theme,
        vars,
        private: listing.private,
        capture: (listing.capture ?? []).includes(guide.guide),
      });
      await context.close();
      taken += result.taken;
      if (result.failure) {
        failure = result.failure;
        break;
      }
    }
  } finally {
    await browser.close();
  }
  console.log(`captured ${taken} images in the ${listing.theme} theme`);
  if (failure) {
    console.error(`the walk stopped at ${failure}`);
    process.exit(1);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main();
}
