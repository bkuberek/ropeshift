import { accessSync, constants } from "node:fs";
import { delimiter, join } from "node:path";

import type { Browser, LaunchOptions } from "playwright-core";
import { chromium } from "playwright-core";
import { describe, it } from "vitest";

// The browser the accessibility and keyboard suites run in. A suite tries, in order: the executable the variable
// names, Google Chrome through Playwright's chrome channel, a chromium or chromium-browser on the PATH, and
// Playwright's own Chromium. Continuous integration's runners carry Google Chrome. A machine with none of them fails
// the suites when CI is set and skips them otherwise, with a message that names the variable.

export const chromeVariable = "ROPESHIFT_TEST_CHROME";

type Env = Record<string, string | undefined>;

export type Candidate = { via: string; options: LaunchOptions };

export type Outcome = { kind: "ready"; browser: Browser; via: string } | { kind: "missing"; message: string; fail: boolean };

// findOnPath is where a program stands on the PATH, or null.
export function findOnPath(name: string, env: Env = process.env): string | null {
  for (const dir of (env.PATH ?? "").split(delimiter).filter(Boolean)) {
    const path = join(dir, name);
    try {
      accessSync(path, constants.X_OK);
      return path;
    } catch {
      // A directory that lacks the program, or cannot be read, holds nothing to run.
    }
  }
  return null;
}

// candidates lists the browsers to try, in order.
export function candidates(env: Env, onPath: (name: string) => string | null = findOnPath): Candidate[] {
  const tried: Candidate[] = [];
  const named = env[chromeVariable];
  if (named) {
    tried.push({ via: `${chromeVariable} (${named})`, options: { executablePath: named, headless: true } });
  }
  tried.push({ via: "Google Chrome", options: { channel: "chrome", headless: true } });
  for (const name of ["chromium", "chromium-browser"]) {
    const path = onPath(name);
    if (path) {
      tried.push({ via: `${name} on the PATH (${path})`, options: { executablePath: path, headless: true } });
    }
  }
  tried.push({ via: "Playwright's Chromium", options: { headless: true } });
  return tried;
}

// openBrowser starts the first candidate that starts. With none, it says what was tried and whether the run fails.
export async function openBrowser(
  env: Env = process.env,
  onPath: (name: string) => string | null = findOnPath,
  launch: (options: LaunchOptions) => Promise<Browser> = (options) => chromium.launch(options),
): Promise<Outcome> {
  const refused: string[] = [];
  for (const candidate of candidates(env, onPath)) {
    try {
      return { kind: "ready", browser: await launch(candidate.options), via: candidate.via };
    } catch (error) {
      refused.push(`${candidate.via}: ${String(error).split("\n")[0]}`);
    }
  }
  const message = [
    `No browser started for the accessibility and keyboard checks. Set ${chromeVariable} to a Chrome or Chromium executable, or install Google Chrome.`,
    ...refused.map((line) => `  ${line}`),
  ].join("\n");
  return { kind: "missing", message, fail: Boolean(env.CI) };
}

// suiteFor is the describe a browser suite registers under. With a browser it is describe. Without one, a run in
// continuous integration registers a failing test, and any other run skips the suite after saying why.
export function suiteFor(opened: Outcome): typeof describe {
  if (opened.kind === "ready") {
    return describe;
  }
  if (opened.fail) {
    it("has a browser to run in", () => {
      throw new Error(opened.message);
    });
  } else {
    // The reporter hides the console of a file whose tests all skip, so the message goes to the process's own stream.
    process.stderr.write(`\nSkipping the browser checks.\n${opened.message}\n\n`);
  }
  return describe.skip as typeof describe;
}
