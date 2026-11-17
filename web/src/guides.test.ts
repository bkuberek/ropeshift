// @vitest-environment node
import { mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import type { Browser, BrowserContext, Page } from "playwright-core";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { expand, runGuide, type Guide } from "../scripts/guides.mjs";
import { openBrowser, suiteFor } from "./browser";

// The guide walk's runner, on a small page the test serves in place of the daemon: each action, a clip to an
// element, a step that fails, and the private words. The walk against the real application is `make guide-shots`.

const base = "http://guide.test";

const fixture = `<!doctype html><html><head><meta charset="utf-8"><title>Fixture</title>
<style>body{margin:0;font:16px sans-serif} main{padding:24px} #card{width:200px;height:100px;background:#cde;margin:40px}</style></head>
<body><main><h1>Projects</h1>
<button id="open" type="button">Create project</button>
<button id="later" type="button">Arrive later</button>
<div id="card" role="region" aria-label="Summary">A summary</div>
<div id="dialog" role="dialog" aria-label="Create project" hidden>
  <label>Name <input id="name"></label>
  <button id="save" type="button">Save</button>
</div>
<p id="status"></p></main>
<script>
document.getElementById("open").onclick = () => { document.getElementById("dialog").hidden = false; };
document.getElementById("later").onclick = () => {
  setTimeout(() => { document.getElementById("status").textContent = "Arrived"; }, 1500);
};
document.getElementById("save").onclick = () => {
  document.getElementById("status").textContent = "Saved " + document.getElementById("name").value;
};
document.getElementById("name").onkeydown = (event) => {
  if (event.key === "Enter") { document.getElementById("status").textContent = "Entered " + event.target.value; }
};
</script></body></html>`;

const opened = await openBrowser();
const suite = suiteFor(opened);

let browser: Browser;
let context: BrowserContext;
let out: string;
const made: string[] = [];

beforeAll(async () => {
  if (opened.kind !== "ready") {
    return;
  }
  browser = opened.browser;
});

afterAll(async () => {
  await browser?.close();
  for (const dir of made) {
    rmSync(dir, { recursive: true, force: true });
  }
});

// Each test writes into a directory of its own.
async function fresh(): Promise<Page> {
  out = mkdtempSync(join(tmpdir(), "rs-guides-test-"));
  made.push(out);
  context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2, colorScheme: "light" });
  await context.route(`${base}/**`, (route) => route.fulfill({ contentType: "text/html", body: fixture }));
  return context.newPage();
}

async function done() {
  await context.close();
  const written = readdirSync(out).sort();
  return written;
}

const options = (extra = {}) => ({ base, out, theme: "light", vars: { name: "Harbor" }, private: [], capture: true, ...extra });

// The size a PNG file says its image has.
function size(name: string): [number, number] {
  const header = readFileSync(join(out, name));
  return [header.readUInt32BE(16), header.readUInt32BE(20)];
}

const create: Guide = {
  guide: "demo",
  steps: [
    {
      step: "open the dialog",
      route: "/",
      actions: [{ click: { role: "button", name: "Create project" } }, { waitFor: { text: "Name" } }],
      capture: { name: "demo-01", clip: "window" },
    },
    {
      step: "name the project",
      actions: [
        { fill: { label: "Name", value: "{{name}}", in: { role: "dialog", name: "Create project" } } },
        { click: { role: "button", name: "Save" } },
        { waitFor: { text: "Saved Harbor" } },
      ],
      capture: { name: "demo-02", clip: { role: "dialog", name: "Create project" } },
    },
    {
      step: "submit with the keyboard",
      actions: [{ fill: { label: "Name", value: "Quay" } }, { press: "Enter" }, { waitFor: { text: "Entered Quay" } }],
      capture: { name: "demo-03", clip: { selector: "#card" } },
    },
  ],
};

describe("expanding a value", () => {
  it("replaces each {{name}} with the variable it names", () => {
    expect(expand("{{src}}/{{api}}", { src: "/srv", api: "harbor-api" })).toBe("/srv/harbor-api");
    expect(expand("no variable", {})).toBe("no variable");
  });

  it("refuses a name no variable holds, so a typo never types the braces", () => {
    expect(() => expand("{{nope}}", { src: "/srv" })).toThrow(/nope/);
  });
});

suite("the guide walk", () => {
  it("presses, fills, and waits, and captures the window and each clip at twice the density", async () => {
    const tab = await fresh();
    const result = await runGuide(tab, create, options());
    const written = await done();
    expect(result.failure).toBeUndefined();
    expect(result.taken).toBe(3);
    expect(written).toEqual(["demo-01-light.png", "demo-02-light.png", "demo-03-light.png"]);
    expect(size("demo-01-light.png")).toEqual([2880, 1800]);
    // The card is 200 by 100 pixels, so its clip is that doubled, to the pixel its position rounds outward.
    const [cardWidth, cardHeight] = size("demo-03-light.png");
    expect(cardWidth).toBe(400);
    expect(cardHeight).toBeGreaterThanOrEqual(200);
    expect(cardHeight).toBeLessThanOrEqual(204);
    // The dialog's clip is its own box.
    const [width, height] = size("demo-02-light.png");
    expect(width).toBeLessThan(2880);
    expect(height).toBeLessThan(1800);
  });

  it("names the guide and the step that failed, and stops there", async () => {
    const tab = await fresh();
    const broken: Guide = {
      guide: "demo",
      steps: [
        { step: "open the dialog", route: "/", actions: [{ click: { role: "button", name: "Create project" } }] },
        { step: "press a button that is not there", actions: [{ click: { role: "button", name: "Delete everything" } }] },
        { step: "never reached", actions: [{ waitFor: { text: "Entered" } }], capture: { name: "demo-09", clip: "window" } },
      ],
    };
    const result = await runGuide(tab, broken, options({ timeout: 800 }));
    const written = await done();
    expect(result.failure).toMatch(/^demo step 2 \(press a button that is not there\): /);
    expect(result.failure).toContain("Delete everything");
    expect(written).not.toContain("demo-09-light.png");
  });

  it("presses the controls of a guide it takes no images of", async () => {
    const tab = await fresh();
    const result = await runGuide(tab, create, options({ capture: false }));
    const written = await done();
    expect(result.failure).toBeUndefined();
    expect(result.taken).toBe(0);
    expect(written).toEqual([]);
  });

  it("waits past its patience only for a wait that sets a timeout", async () => {
    const arrive = (timeout?: number): Guide => ({
      guide: "late",
      steps: [{ step: "wait for it", route: "/", actions: [{ click: { role: "button", name: "Arrive later" } }, { waitFor: { text: "Arrived", timeout } }] }],
    });
    const patient = await runGuide(await fresh(), arrive(5000), options({ timeout: 500 }));
    await done();
    expect(patient.failure).toBeUndefined();
    const hasty = await runGuide(await fresh(), arrive(), options({ timeout: 500 }));
    await done();
    expect(hasty.failure).toMatch(/^late step 1 \(wait for it\): .*Arrived/);
  });

  it("enlarges the window for a capture that sets a size, and goes on at the size it had", async () => {
    const tab = await fresh();
    const wide: Guide = {
      guide: "wide",
      steps: [
        { step: "show the page wide", route: "/", actions: [{ waitFor: { text: "Projects" } }], capture: { name: "wide-01", clip: "window", width: 1600 } },
        { step: "show it tall", capture: { name: "wide-02", clip: "window", height: 1000 } },
        { step: "show it again", capture: { name: "wide-03", clip: "window" } },
      ],
    };
    const result = await runGuide(tab, wide, options());
    await done();
    expect(result.failure).toBeUndefined();
    expect(size("wide-01-light.png")).toEqual([3200, 1800]);
    expect(size("wide-02-light.png")).toEqual([2880, 2000]);
    expect(size("wide-03-light.png")).toEqual([2880, 1800]);
  });

  it("fails a capture of a page that shows a private word, and keeps the image", async () => {
    const tab = await fresh();
    const shows: Guide = {
      guide: "leak",
      steps: [{ step: "show the page", route: "/", actions: [{ waitFor: { text: "Projects" } }], capture: { name: "leak-01", clip: "window" } }],
    };
    const result = await runGuide(tab, shows, options({ private: ["Create project"] }));
    const written = await done();
    expect(result.failure).toMatch(/^leak step 1 \(show the page\): .*shows Create project/);
    expect(written).toContain("leak-01-light.png");
  });

  it("fails a step whose variable no listing holds", async () => {
    const tab = await fresh();
    const typo: Guide = {
      guide: "typo",
      steps: [{ step: "fill the field", route: "/", actions: [{ fill: { label: "Name", value: "{{folder}}" } }] }],
    };
    const result = await runGuide(tab, typo, options());
    await done();
    expect(result.failure).toMatch(/^typo step 1 \(fill the field\): .*folder/);
  });
});
