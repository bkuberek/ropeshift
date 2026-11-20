import { readFileSync } from "node:fs";
import { join } from "node:path";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { NeedItem, NeedsYou as View, RepositoryCreationNeed } from "../api/types";
import { openPage } from "../opening";
import { CurrentProject } from "../app/route";
import { golden, here, refusal, serveApi, serveViews, testProject } from "../testing";
import { cardKindOf, cardKinds, contextOf, filterOf, headingOf, keyOf, kindOf } from "./kinds";
import { NeedsYou } from "./NeedsYou";
import { reasonProblem } from "./RepositoryCreationCard";

type CreationItem = Extract<NeedItem, { kind: "repository_creation" }>;

// The item a Go test wrote from a real ledger: ghost, whose push the forge denied, with its steps and the command that finishes it.
const stopped = (): CreationItem => {
  const found = golden<View>("repository-creation-needs-you").items.find((item): item is CreationItem => item.kind === "repository_creation");
  if (!found) {
    throw new Error("the golden holds no repository creation");
  }
  return found;
};

// with gives the item's need a change, as another state of the same creation would read.
const with_ = (change: (need: RepositoryCreationNeed) => void): CreationItem => {
  const item = stopped();
  change(item.repository_creation);
  return item;
};

function show(item: CreationItem, after: () => Promise<void> = async () => {}) {
  const view: View = { ...golden<View>("repository-creation-needs-you"), items: [item] };
  window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(keyOf(item))}`);
  const ui = (shown: View, later: () => Promise<void>) => (
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={shown} readAt={Date.now()} after={later} />
    </CurrentProject.Provider>
  );
  const rendered = render(ui(view, after));
  return { ...rendered, gone: () => rendered.rerender(ui({ ...view, items: [] }, after)) };
}

const theCard = (name = "ghost was not created") => screen.getByRole("article", { name });

const buttons = (card: HTMLElement) => [...card.querySelectorAll("a.btn, button")].map((control) => control.textContent?.trim());

const posts = (sent: { method: string; path: string; body?: unknown }[]) => sent.filter((call) => call.method === "POST").map((call) => `${call.path} ${JSON.stringify(call.body)}`);

describe("a repository a plan approval did not create, in Needs you", () => {
  it("is a card kind of its own, under Cards, titled by the repository", () => {
    const item = stopped();
    expect(cardKinds).toContain("repository-creation");
    expect(cardKindOf(item)).toBe("repository-creation");
    expect(filterOf(item)).toBe("cards");
    expect(kindOf(item)).toEqual({ icon: "repo", name: "Repository creation" });
    expect(headingOf(item)).toBe("ghost was not created");
    expect(contextOf(item)).toBe("Plan generated-2 · version 1");
    expect(keyOf(item)).toBe("repository_creation:generated-1/ghost");
  });

  it("lists the steps as an ordered list, each outcome as a word, with the reason and the command that finishes it, copied as shown", async () => {
    const copy = vi.fn(async (_text: string) => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    show(stopped());
    const card = theCard();
    expect(card.textContent).toContain("Repository creation");
    expect(card.textContent).toContain("Retry goes on from the last step done and registers ghost once every step succeeds.");
    const read = card.querySelector("dl")?.textContent ?? "";
    expect(read).toContain("On the forgeGitHub acme/ghost, private, branch main, as octo");
    expect(read).toContain("Path../ghost");
    expect(read).toContain("Leads to/work/ghost");
    expect(read).toContain("Remotegit@github.com:acme/ghost.git");
    const steps = within(card).getByRole("list", { name: "Steps of ghost" });
    expect(steps.tagName).toBe("OL");
    expect(within(steps).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
      "Files written: Done",
      "First commit: Done5d6e7f8",
      "Create: Done",
      "Origin: Done",
      "Push: Failedremote: Permission denied",
    ]);
    expect(card.textContent).toContain("the push step failed: remote: Permission denied");
    const commands = within(card).getByRole("list", { name: "Commands that finish it by hand" });
    expect(within(commands).getByText("git -C /work/ghost push --set-upstream origin main").tagName).toBe("CODE");
    await userEvent.click(within(commands).getByRole("button", { name: "Copy git -C /work/ghost push --set-upstream origin main" }));
    expect(copy).toHaveBeenCalledWith("git -C /work/ghost push --set-upstream origin main");
  });

  it("offers Skip and then Retry as the primary, and sends nothing by being shown", () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    show(stopped());
    const card = theCard();
    expect(buttons(card).filter((name) => name === "Skip" || name === "Retry")).toEqual(["Skip", "Retry"]);
    expect(within(card).getByRole("button", { name: "Retry" }).className).toContain("btn-primary");
    expect(sent.filter((call) => call.method !== "GET")).toEqual([]);
  });

  it("says a refusal's remedy as the API wrote it, and offers its command to copy", () => {
    show(
      with_((need) => {
        need.state = "refused";
        need.reason = "docs is not registered: gh_signed_out";
        need.remedy = "run `gh auth login --hostname github.com`";
        need.creation = { forge: "github", owner: "acme", forge_name: "ghost", visibility: "public", default_branch: "main", remedy: "gh auth login --hostname github.com" };
      }),
    );
    const card = theCard();
    expect(card.textContent).toContain("Refused");
    expect(card.textContent).toContain("No step ran. Nothing was written.");
    expect(card.textContent).toContain("run `gh auth login --hostname github.com`");
    expect(within(card).getByText("gh auth login --hostname github.com").tagName).toBe("CODE");
    expect(card.querySelector(".badge.tone-warning")?.textContent).toBe("Public: anyone can read it");
    expect(within(card).queryByRole("list", { name: "Steps of ghost" })).toBeNull();
  });

  it("gives a forge repository that exists the record's sentence and the words to skip it, as the API wrote them", () => {
    const sentence = "clone acme/ghost at /work/ghost and write `remote` in place of `create` in the manifest entry for ghost, or skip it";
    show(
      with_((need) => {
        need.state = "refused";
        need.remedy = sentence;
      }),
    );
    expect(theCard().textContent).toContain(sentence);
    expect(within(theCard()).queryByRole("button", { name: /^Copy gh/ })).toBeNull();
  });

  it("says an interrupted creation is interrupted, and offers both", () => {
    show(with_((need) => (need.state = "interrupted")));
    expect(theCard().textContent).toContain("Interrupted");
    expect(buttons(theCard()).filter((name) => name === "Skip" || name === "Retry")).toEqual(["Skip", "Retry"]);
  });

  it("shows a creation another request runs as creating now, with no button", () => {
    show(
      with_((need) => {
        need.state = "running";
        need.actions = [];
        need.reason = "";
      }),
    );
    const card = theCard("ghost is being created");
    expect(card.textContent).toContain("Creating now");
    expect(buttons(card).filter((name) => name === "Skip" || name === "Retry")).toEqual([]);
  });
});

describe("Retry", () => {
  const created = {
    id: "generated-1",
    repositories: [{ name: "ghost", manifest_path: "../ghost", path: "/work/ghost", outcome: "created" }],
  };

  it("runs the creation in the item's project, shows Creating ghost while it runs, and says who created it once the item is gone", async () => {
    let finish: () => void = () => {};
    const pending = new Promise<void>((resolve) => (finish = resolve));
    const sent = serveApi((call) => (call.method === "POST" ? { status: 200, body: created } : { status: 200, body: {} }));
    const answered = fetch;
    vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        await pending;
      }
      return answered(url, init);
    });
    const later = vi.fn(async () => {});
    const view = show(stopped(), later);
    await userEvent.click(within(theCard()).getByRole("button", { name: "Retry" }));
    expect((await within(theCard()).findByRole("status")).textContent).toBe("Creating ghost");
    expect((within(theCard()).getByRole("button", { name: "Retry" }) as HTMLButtonElement).disabled).toBe(true);
    expect((within(theCard()).getByRole("button", { name: "Skip" }) as HTMLButtonElement).disabled).toBe(true);
    finish();
    await waitFor(() => expect(later).toHaveBeenCalled());
    expect(sent.filter((call) => call.method === "POST").map((call) => `${call.path} ${JSON.stringify(call.body)}`)).toEqual([
      "/v1/approvals/generated-1/repositories/ghost/retry {}",
    ]);
    view.gone();
    await waitFor(() => expect(document.activeElement?.textContent).toMatch(/ghost was not created no longer waits: created by you at \d/));
    expect(screen.getByRole("list", { name: "Waiting items" }).textContent).toContain("Settled by you");
  });

  it("goes back to the card with the new outcome when the creation stops again", async () => {
    const sent = serveApi((call) => (call.method === "POST" ? { status: 200, body: { id: "generated-1", repositories: [{ name: "ghost", path: "/work/ghost", outcome: "create_failed" }] } } : { status: 200, body: {} }));
    const later = vi.fn(async () => {});
    show(stopped(), later);
    await userEvent.click(within(theCard()).getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(later).toHaveBeenCalled());
    expect(posts(sent)).toHaveLength(1);
    expect(within(theCard()).getByRole("status").textContent).toBe("");
    expect((within(theCard()).getByRole("button", { name: "Retry" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("shows why a run that met an error could not finish, when the answer carries it", async () => {
    const problem = "registering the repositories the manifest adds failed: disk full";
    serveApi((call) => (call.method === "POST" ? { status: 200, body: { id: "generated-1", repositories: [{ name: "ghost", path: "/work/ghost", outcome: "create_failed" }], repositories_problem: problem } } : { status: 200, body: {} }));
    show(stopped());
    await userEvent.click(within(theCard()).getByRole("button", { name: "Retry" }));
    expect((await within(theCard()).findByRole("alert")).textContent).toBe(problem);
    await userEvent.click(within(theCard()).getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(within(theCard()).getAllByRole("alert")).toHaveLength(1));
  });

  it("shows the server's message when it refuses, as it does for an entry a member skipped", async () => {
    const message = "ghost was skipped; a later approved version that targets it creates it";
    serveApi((call) => (call.method === "POST" ? refusal(409, "illegal_move", message) : { status: 200, body: {} }));
    show(stopped());
    await userEvent.click(within(theCard()).getByRole("button", { name: "Retry" }));
    expect((await within(theCard()).findByRole("alert")).textContent).toContain(message);
  });
});

describe("the live region", () => {
  it("stands in the card empty, and holds Creating ghost while a retry runs, so a screen reader announces it", async () => {
    let finish: () => void = () => {};
    const pending = new Promise<void>((resolve) => (finish = resolve));
    serveApi(() => ({ status: 200, body: { id: "generated-1", repositories: [] } }));
    const answered = fetch;
    vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        await pending;
      }
      return answered(url, init);
    });
    show(stopped());
    const region = within(theCard()).getByRole("status");
    expect(region.textContent).toBe("");
    await userEvent.click(within(theCard()).getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(region.textContent).toBe("Creating ghost"));
    expect(within(theCard()).getByRole("status")).toBe(region);
    finish();
    await waitFor(() => expect(region.textContent).toBe(""));
  });

  it("announces a change of state that arrives by a read, in the same region", async () => {
    const running = with_((need) => {
      need.state = "running";
      need.actions = [];
    });
    const view = show(running);
    const region = within(theCard("ghost is being created")).getByRole("status");
    expect(region.textContent).toBe("");
    const failed = stopped();
    view.rerender(
      <CurrentProject.Provider value={testProject}>
        <NeedsYou view={{ ...golden<View>("repository-creation-needs-you"), items: [failed] }} readAt={Date.now()} after={async () => {}} />
      </CurrentProject.Provider>,
    );
    await waitFor(() => expect(within(theCard()).getByRole("status").textContent).toBe("ghost is now failed"));
    expect(within(theCard()).getByRole("status")).toBe(region);
  });
});

describe("Skip", () => {
  const open = async () => {
    await userEvent.click(within(theCard()).getByRole("button", { name: "Skip" }));
    return screen.findByRole("dialog", { name: "Leave ghost uncreated?" });
  };

  it("asks why, in a dialog that says what happens to its work, and sends the reason trimmed", async () => {
    const sent = serveApi((call) => (call.method === "POST" ? { status: 200, body: { id: "generated-1", repositories: [] } } : { status: 200, body: {} }));
    const later = vi.fn(async () => {});
    const view = show(stopped(), later);
    const dialog = await open();
    expect(dialog.textContent).toContain("Its work stays in contract review until a later approved version creates it.");
    expect(document.activeElement).toBe(within(dialog).getByLabelText(/^Reason/));
    await userEvent.type(within(dialog).getByLabelText(/^Reason/), "  built by hand  ");
    await userEvent.click(within(dialog).getByRole("button", { name: "Skip repository" }));
    await waitFor(() => expect(later).toHaveBeenCalled());
    expect(posts(sent)).toEqual(['/v1/approvals/generated-1/repositories/ghost/skip {"reason":"built by hand"}']);
    expect(screen.queryByRole("dialog")).toBeNull();
    view.gone();
    await waitFor(() => expect(document.activeElement?.textContent).toContain("no longer waits: you skipped it"));
  });

  it("sends nothing without a reason, and says so at the field", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    show(stopped());
    const dialog = await open();
    await userEvent.click(within(dialog).getByRole("button", { name: "Skip repository" }));
    expect(dialog.textContent).toContain("Say why you skip it.");
    expect(posts(sent)).toEqual([]);
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("shows what the server refused with, and keeps the dialog open", async () => {
    serveApi((call) => (call.method === "POST" ? refusal(409, "illegal_move", "ghost is being created now") : { status: 200, body: {} }));
    show(stopped());
    const dialog = await open();
    await userEvent.type(within(dialog).getByLabelText(/^Reason/), "later");
    await userEvent.click(within(dialog).getByRole("button", { name: "Skip repository" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("ghost is being created now");
    expect(screen.getByRole("dialog")).toBeTruthy();
  });
});

describe("the reason for a skip", () => {
  it("is checked as the server checks it: 1 to 2000 characters once trimmed, on one line, with no control character", () => {
    expect(reasonProblem("")).toBe("Say why you skip it.");
    expect(reasonProblem("   ")).toBe("Say why you skip it.");
    expect(reasonProblem("built by hand")).toBe("");
    expect(reasonProblem(` ${"a".repeat(2000)} `)).toBe("");
    expect(reasonProblem("a".repeat(2001))).toBe("The reason is over 2000 characters. Shorten it.");
    expect(reasonProblem("é".repeat(2000))).toBe("");
    expect(reasonProblem("one\ntwo")).toBe("The reason is one line, with no control character or line break.");
    expect(reasonProblem("bell\u0007")).toBe("The reason is one line, with no control character or line break.");
    expect(reasonProblem("escape\u001b[31m")).toBe("The reason is one line, with no control character or line break.");
    expect(reasonProblem("flip\u202Ethis")).toBe("The reason is one line, with no control character or line break.");
    expect(reasonProblem("line\u2028break")).toBe("The reason is one line, with no control character or line break.");
  });
});

describe("the palette", () => {
  const served = () => {
    const needs = { ...golden<View>("repository-creation-needs-you") };
    const sent = serveViews((call) => (call.path === "/v1/needs-you" ? { status: 200, body: needs } : undefined));
    return sent;
  };
  const search = async (words: string) => {
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), words);
    return palette;
  };

  it("offers Retry creating and Skip creating for the item, and each goes to its button without pressing it", async () => {
    const sent = served();
    const palette = await search("retry creating");
    expect(within(palette).getAllByRole("option").map((option) => option.textContent)).toEqual(expect.arrayContaining([expect.stringContaining("Retry creating ghost")]));
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(document.activeElement?.textContent).toBe("Retry"));
    expect(sent.some((call) => call.method === "POST")).toBe(false);
  });

  it("goes to Skip, which opens its dialog only when pressed", async () => {
    const sent = served();
    const palette = await search("skip creating");
    expect(within(palette).getAllByRole("option").map((option) => option.textContent)).toEqual(expect.arrayContaining([expect.stringContaining("Skip creating ghost")]));
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(document.activeElement?.textContent).toBe("Skip"));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(sent.some((call) => call.method === "POST")).toBe(false);
  });
});

// ATTACK: what the API gives is text, wherever it stands in the card.
describe("a card built from hostile text", () => {
  it("names the repository in the palette's Decide entry as plain text", async () => {
    const item = with_((need) => {
      need.name = "ghost\u202E<b>x</b>";
    });
    serveViews((call) => (call.path === "/v1/needs-you" ? { status: 200, body: { ...golden<View>("repository-creation-needs-you"), items: [item] } } : undefined));
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), "decide ghost");
    const titles = within(palette).getAllByRole("option").map((option) => option.textContent ?? "");
    expect(titles.some((text) => text.includes("Decide: ghost<b>x</b> was not created"))).toBe(true);
    expect(titles.join("")).not.toMatch(/\u202E/);
    expect(palette.querySelector("b")).toBeNull();
  });

  const hostile = "\u001b[31mred\u001b[0m\u0007<script>alert(1)</script><img src=x onerror=alert(2)>\u202Eevil\nsecond line";

  it("renders no element from it, drops escapes, bells, bidirectional marks, and line breaks, and copies one line", async () => {
    const copy = vi.fn(async (_text: string) => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    const item = with_((need) => {
      need.name = "ghost\u202E<script>alert(9)</script>";
      need.plan = "plan\u202E\u0007x";
      need.manifest_path = `../ghost\n${hostile}`;
      need.path = `/work/ghost${hostile}`;
      need.reason = hostile;
      need.remedy = `run \`${hostile}\``;
      need.creation = {
        forge: "github",
        owner: `acme${"\u202E"}`,
        forge_name: "ghost",
        visibility: "public",
        url: `git@github.com:acme/ghost.git${hostile}`,
        commit_error: hostile,
        steps: [{ step: "push", outcome: "failed", detail: hostile }],
        remaining: [`git push\n${hostile}`],
      };
    });
    show(item);
    const card = theCard("ghost<script>alert(9)</script> was not created");
    // The list's title, the card's title, and the plan in their context are the name and the plan as plain text.
    const listed = screen.getByRole("list", { name: "Waiting items" });
    expect(listed.textContent).toContain("ghost<script>alert(9)</script> was not created");
    expect(listed.textContent).toContain("Plan planx · version 1");
    expect(within(card).getByRole("heading", { level: 2 }).textContent).toBe("ghost<script>alert(9)</script> was not created");
    expect(listed.textContent).not.toMatch(/[\u202E\u0007]/);
    expect(document.querySelectorAll("script")).toHaveLength(0);
    expect(card.querySelectorAll("script, img, iframe, object, style, link")).toHaveLength(0);
    const text = card.textContent ?? "";
    expect(text).toContain("<script>alert(1)</script>");
    expect(text).not.toMatch(/[\u001b\u0007\u202E]/);
    expect(text).not.toContain("\n");
    for (const copyButton of within(card).getAllByRole("button", { name: /^Copy/ })) {
      await userEvent.click(copyButton);
    }
    expect(copy).toHaveBeenCalled();
    for (const [text] of copy.mock.calls) {
      expect(text).not.toMatch(/[\u001b\u0007\u202E\n]/);
    }
  });
});

describe("the layout on a phone", () => {
  it("stacks each step, its outcome under its name", () => {
    const css = readFileSync(join(import.meta.dirname, "..", "styles", "responsive.css"), "utf8");
    expect(css).toMatch(/@media \(max-width: 639px\) \{\s*\.creation-steps > li \{\s*grid-template-columns: 1fr;/);
  });
});
