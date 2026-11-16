// @vitest-environment node
import type { Browser, LaunchOptions } from "playwright-core";
import { describe, expect, it } from "vitest";

import { candidates, chromeVariable, openBrowser } from "./browser";

// The browser the accessibility and keyboard suites run in is the first of four that starts: the executable
// ROPESHIFT_TEST_CHROME names, Google Chrome through Playwright's chrome channel, a chromium or chromium-browser on the
// PATH, and Playwright's own Chromium. A machine with none fails the suites in continuous integration and skips them
// elsewhere, with a message that names the variable.

const fake = { close: async () => {} } as unknown as Browser;
const none = () => null;

describe("the browsers a suite tries", () => {
  it("name ROPESHIFT_TEST_CHROME first, then Chrome, then a chromium on the PATH, then Playwright's own", () => {
    const onPath = (name: string) => (name === "chromium-browser" ? "/usr/bin/chromium-browser" : name === "chromium" ? "/usr/bin/chromium" : null);
    const tried = candidates({ [chromeVariable]: "/opt/chrome/chrome" }, onPath);
    expect(tried.map((row) => row.via)).toEqual([
      `${chromeVariable} (/opt/chrome/chrome)`,
      "Google Chrome",
      "chromium on the PATH (/usr/bin/chromium)",
      "chromium-browser on the PATH (/usr/bin/chromium-browser)",
      "Playwright's Chromium",
    ]);
    expect(tried[0]?.options).toEqual({ executablePath: "/opt/chrome/chrome", headless: true });
    expect(tried[1]?.options).toEqual({ channel: "chrome", headless: true });
    expect(tried[2]?.options).toEqual({ executablePath: "/usr/bin/chromium", headless: true });
    expect(tried[4]?.options).toEqual({ headless: true });
  });

  it("leave out the variable when it is unset or empty, and a name the PATH does not hold", () => {
    expect(candidates({}, none).map((row) => row.via)).toEqual(["Google Chrome", "Playwright's Chromium"]);
    expect(candidates({ [chromeVariable]: "" }, none).map((row) => row.via)).toEqual(["Google Chrome", "Playwright's Chromium"]);
  });
});

describe("opening a browser", () => {
  it("uses the first candidate that starts, and says which", async () => {
    const started: LaunchOptions[] = [];
    const launch = async (options: LaunchOptions) => {
      started.push(options);
      if (options.channel === "chrome") {
        throw new Error("Chromium distribution 'chrome' is not found");
      }
      return fake;
    };
    const opened = await openBrowser({ [chromeVariable]: "/missing/chrome" }, none, async (options) => {
      if (options.executablePath === "/missing/chrome") {
        throw new Error("no such file");
      }
      return launch(options);
    });
    expect(opened.kind).toBe("ready");
    expect(opened.kind === "ready" ? opened.via : "").toBe("Playwright's Chromium");
    expect(started.map((options) => options.channel ?? options.executablePath ?? "bundled")).toEqual(["chrome", "bundled"]);
  });

  it("starts the variable's executable before any other", async () => {
    const opened = await openBrowser({ [chromeVariable]: "/opt/chrome/chrome" }, none, async () => fake);
    expect(opened.kind === "ready" ? opened.via : "").toBe(`${chromeVariable} (/opt/chrome/chrome)`);
  });

  const refuse = async () => {
    throw new Error("would not start");
  };

  it("finds none, and a run outside continuous integration skips with a message that names the variable", async () => {
    const opened = await openBrowser({}, none, refuse);
    expect(opened.kind).toBe("missing");
    if (opened.kind === "missing") {
      expect(opened.fail).toBe(false);
      expect(opened.message).toContain(chromeVariable);
      expect(opened.message).toContain("Google Chrome");
      expect(opened.message).toContain("would not start");
    }
  });

  it("finds none, and a run in continuous integration fails", async () => {
    const opened = await openBrowser({ CI: "true" }, none, refuse);
    expect(opened.kind === "missing" && opened.fail).toBe(true);
    expect(opened.kind === "missing" ? opened.message : "").toContain(chromeVariable);
  });

  it("reads an empty CI as unset", async () => {
    const opened = await openBrowser({ CI: "" }, none, refuse);
    expect(opened.kind === "missing" && opened.fail).toBe(false);
  });
});
