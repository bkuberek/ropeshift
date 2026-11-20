import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ApprovalDecided, CreationOutcome, ManifestRegistration, NeedsYou, PlanView, RepositoryOutcome } from "../api/types";
import { CurrentProject } from "../app/route";
import { golden, testProject } from "../testing";
import { isWaiting, WaitingCreations, waitingIn, waitingKey } from "../needs/waitingCreations";
import { createsLine, stepRows } from "./creation";
import { RegistrationRows } from "./PlanParts";

// The rows a version's manifest lists, as the plan page and the plan approval's card show them, from what the API served
// for a version that creates repositories: a preview, and the decision that created them.

const preview = () => golden<PlanView>("plan-created").approval?.registration as ManifestRegistration;
const decided = (): ManifestRegistration => ({ repositories: golden<ApprovalDecided>("approval-decided-created").repositories ?? [] });

// show renders the rows as the page does, where Needs you holds an item for each name in waiting.
function show(registration: ManifestRegistration, approval = "ap-1", waiting: string[] = []) {
  return render(
    <CurrentProject.Provider value={testProject}>
      <WaitingCreations.Provider value={new Set(waiting.map((name) => waitingKey(testProject, approval, name)))}>
        <RegistrationRows registration={registration} approval={approval} />
      </WaitingCreations.Provider>
    </CurrentProject.Provider>,
  );
}

const row = (name: string) => screen.getByRole("listitem", { name });

describe("a repository approving creates, before the decision", () => {
  it("reads Will create, where the path leads, and where on GitHub it is made, in the words of the design", () => {
    show(preview());
    const web = row("web");
    expect(web.textContent).toContain("Will create");
    expect(web.textContent).toContain("Path ../web leads to /work/web");
    expect(web.textContent).toContain("GitHub acme/web, private, branch main, as octo");
    expect(web.textContent).not.toContain("Public: anyone can read it");
    expect(web.querySelector("ol")).toBeNull();
  });

  it("warns that a public repository is one anyone can read, as text on a badge", () => {
    show(preview());
    const docs = row("docs");
    expect(docs.textContent).toContain("GitHub acme/docs, public, branch trunk");
    expect(docs.querySelector(".badge.tone-warning")?.textContent).toBe("Public: anyone can read it");
  });

  it("gives a refusal its reason, its remedy command with Copy, and says to fix it and reload", async () => {
    const copy = vi.fn(async (_text: string) => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    show(preview());
    const docs = row("docs");
    expect(docs.textContent).toContain("Refused");
    expect(docs.textContent).toContain("gh_signed_out: Run `gh auth login --hostname github.com` on this machine");
    expect(within(docs).getByText("gh auth login --hostname github.com").tagName).toBe("CODE");
    expect(docs.textContent).toContain("Fix this and reload before you approve, or approve and retry it from Needs you.");
    await userEvent.click(within(docs).getByRole("button", { name: "Copy gh auth login --hostname github.com" }));
    expect(copy).toHaveBeenCalledWith("gh auth login --hostname github.com");
  });

  it("gives a forge repository that exists the record's own sentence, as the API wrote it, with no command to copy", () => {
    const sentence = "clone acme/web at ../web and write `remote` in place of `create` in the manifest entry for web";
    show({
      preview: true,
      repositories: [{ name: "web", manifest_path: "../web", path: "/work/web", outcome: "refused", refusal_code: "remote_exists", reason: "acme/web exists", creation: { forge: "github", owner: "acme", forge_name: "web", visibility: "private", remedy: sentence } }],
    });
    const web = row("web");
    expect(web.textContent).toContain(sentence);
    expect(web.textContent).not.toContain(`${sentence}, or skip it`);
    expect(within(web).queryByRole("button", { name: /^Copy/ })).toBeNull();
  });

  it("shows a busy path as refused, with its remedy, and does not tell the member to fix it and reload", () => {
    show({
      preview: true,
      repositories: [
        {
          name: "api",
          manifest_path: "repos/api",
          path: "/work/repos/api",
          outcome: "refused",
          refusal_code: "path_busy",
          reason: "api is not registered: path_busy",
          creation: { forge: "github", owner: "acme", forge_name: "api", visibility: "private", remedy: "another creation at this path is running; preview again when it finishes" },
        },
      ],
    });
    const api = row("api");
    expect(api.textContent).toContain("Refused");
    expect(api.textContent).toContain("another creation at this path is running; preview again when it finishes");
    expect(api.textContent).not.toContain("Fix this and reload");
    expect(within(api).queryByRole("button", { name: /^Copy/ })).toBeNull();
  });

  it("says a planned entry is created when a version that gives it work is approved", () => {
    show(golden<PlanView>("plan-planned").approval?.registration as ManifestRegistration);
    const web = row("web");
    expect(web.textContent).toContain("Planned");
    expect(web.textContent).toContain("Created when a version that gives it work is approved. No work order of this version targets web.");
    expect(web.textContent).not.toContain("leads to");
  });

  it("says in one line what approving creates", () => {
    expect(createsLine([{ name: "api", path: "/w/api", outcome: "created", creation: { forge: "github", owner: "acme", forge_name: "api", visibility: "private" } }])).toBe(
      "creates 1 repository on GitHub, acme/api, private, and registers it",
    );
    expect(createsLine(preview().repositories)).toBe("creates 1 repository on GitHub, acme/web, private, and registers it");
    expect(
      createsLine([
        { name: "api", path: "", outcome: "created", creation: { forge: "github", owner: "acme", forge_name: "api", visibility: "private" } },
        { name: "web", path: "", outcome: "created", creation: { forge: "github", owner: "acme", forge_name: "web", visibility: "public" } },
      ]),
    ).toBe("creates 2 repositories on GitHub, acme/api (private) and acme/web (public), and registers them");
    expect(createsLine(golden<PlanView>("plan-planned").approval?.registration?.repositories)).toBe("");
  });
});

describe("a repository approving created or tried to create", () => {
  it("lists a created repository's steps with each outcome as a word, and the Makefile entry with Copy", async () => {
    show(decided(), "ap-1");
    const web = row("web");
    expect(web.textContent).toContain("Created");
    const steps = within(web).getByRole("list", { name: "Steps of web" });
    expect(within(steps).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
      "Files written: Done",
      "First commit: Done1a2b3c4",
      "Create: Done",
      "Origin: Done",
      "Push: Done",
    ]);
    expect(web.textContent).toContain("Add to REPOSITORIES in the workspace Makefile:");
    expect(within(web).getByText("web=git@github.com:acme/web.git=../web").tagName).toBe("CODE");
    expect(within(web).getByRole("button", { name: "Copy web=git@github.com:acme/web.git=../web" })).toBeTruthy();
  });

  it("says a creation that stopped is not created, at which step and why, and links its item in Needs you", () => {
    show(decided(), "ap-1", ["docs"]);
    const docs = row("docs");
    expect(docs.textContent).toContain("Not created");
    expect(docs.textContent).toContain("the push step failed: remote: Permission denied");
    const steps = within(docs).getByRole("list", { name: "Steps of docs" });
    expect(within(steps).getAllByRole("listitem").map((item) => item.textContent)).toContain("Push: Failedremote: Permission denied");
    expect(within(docs).getByRole("link", { name: "Retry or skip it in Needs you" }).getAttribute("href")).toContain("/needs-you?item=repository_creation%3Aap-1%2Fdocs");
  });

  it("links the item in Needs you of each row that has one, whatever the row says of its creation", () => {
    const creation: CreationOutcome = { forge: "github", owner: "acme", forge_name: "web", visibility: "private" };
    show(
      {
        repositories: [
          { name: "web", manifest_path: "../web", path: "/work/web", outcome: "refused", refusal_code: "remote_exists", reason: "acme/web exists", creation },
          // A run an error or its limit stopped, which the row reads as refused with no creation.
          { name: "stopped", manifest_path: "../stopped", path: "/work/stopped", outcome: "refused", reason: "stopped is not registered: the run passed its limit of 10 minutes" },
          // Two entries that create one forge repository: the second is refused before the creator is asked.
          { name: "copy", manifest_path: "../copy", path: "/work/copy", outcome: "refused", reason: "copy is not registered: it would create acme/web, which the entry web of this version creates too" },
          { name: "after", manifest_path: "../after", path: "/work/after", outcome: "not_attempted", reason: "after was not attempted: the run stopped at stopped" },
          { name: "held", manifest_path: "../held", path: "/work/held", outcome: "refused", reason: "held is not registered: its name is held by another repository" },
          { name: "api", manifest_path: "../api", path: "/work/api", outcome: "known" },
        ],
      },
      "ap-1",
      ["web", "stopped", "copy", "after"],
    );
    for (const name of ["web", "stopped", "copy", "after"]) {
      expect(within(row(name)).getByRole("link", { name: "Retry or skip it in Needs you" }).getAttribute("href")).toContain(`/needs-you?item=repository_creation%3Aap-1%2F${name}`);
    }
    // Needs you holds none for a refusal of an entry that creates nothing, or for an entry it holds nothing of.
    expect(within(row("held")).queryByRole("link")).toBeNull();
    expect(within(row("api")).queryByRole("link")).toBeNull();
  });

  it("finds a creation among the items Needs you holds by its project, its approval, and its name", () => {
    const view = golden<NeedsYou>("repository-creation-needs-you");
    const item = view.items.find((one) => one.kind === "repository_creation");
    if (item?.kind !== "repository_creation") {
      throw new Error("the golden holds no repository creation");
    }
    const { approval, name } = item.repository_creation;
    const here = waitingIn([{ ...item, project: { id: "p1", name: "shop" } }]);
    expect(isWaiting(here, "shop", approval, name)).toBe(true);
    expect(isWaiting(here, "other", approval, name)).toBe(false);
    expect(isWaiting(here, "shop", approval, "another")).toBe(false);
    const unlabelled = waitingIn([{ ...item, project: undefined }]);
    expect(isWaiting(unlabelled, "shop", approval, name)).toBe(true);
  });

  it("links no row that Needs you holds no item for, whatever the row says", () => {
    const rows: ManifestRegistration = { repositories: [{ name: "web", manifest_path: "../web", path: "/work/web", outcome: "create_failed", reason: "the push step failed" }] };
    show(rows, "ap-1", []);
    expect(within(row("web")).queryByRole("link")).toBeNull();
  });

  it("links no item before the decision, which has created nothing", () => {
    render(
      <CurrentProject.Provider value={testProject}>
        <WaitingCreations.Provider value={new Set([waitingKey(testProject, "ap-1", "web")])}>
          <RegistrationRows
            registration={{ preview: true, repositories: [{ name: "web", manifest_path: "../web", path: "/work/web", outcome: "refused", refusal_code: "remote_exists", creation: { forge: "github", owner: "acme", forge_name: "web", visibility: "private" } }] }}
            approval="ap-1"
          />
        </WaitingCreations.Provider>
      </CurrentProject.Provider>,
    );
    expect(within(row("web")).queryByRole("link")).toBeNull();
  });

  it("reads Skipped for an entry a member skipped, with the member's reason, and no refusal line", () => {
    show({ repositories: [{ name: "api", manifest_path: "repos/api", path: "/work/repos/api", outcome: "skipped", reason: "built by hand" }] });
    const api = row("api");
    expect(api.textContent).toContain("Skipped");
    expect(api.textContent).toContain("built by hand");
    expect(api.textContent).not.toContain("Fix this and reload");
    expect(within(api).queryByRole("link")).toBeNull();
  });

  it("lists no step of a creation that recorded none", () => {
    expect(stepRows(undefined)).toEqual([]);
    expect(stepRows({ forge: "github", owner: "acme", forge_name: "api", visibility: "private" })).toEqual([]);
  });

  it("names a step the creation never reached", () => {
    const rows = stepRows({ forge: "github", owner: "acme", forge_name: "api", visibility: "private", commit_error: "no identity", steps: [] });
    expect(rows.map((one) => `${one.name}: ${one.outcome}`)).toEqual(["Files written: Done", "First commit: Failed"]);
    const partway = stepRows({ forge: "github", owner: "acme", forge_name: "api", visibility: "private", commit: "abcdef0123", steps: [{ step: "create", outcome: "done" }] });
    expect(partway.map((one) => `${one.name}: ${one.outcome}`)).toEqual(["Files written: Done", "First commit: Done", "Create: Done", "Origin: Not reached", "Push: Not reached"]);
  });
});

// ATTACK: what a worker, a forge, or a manifest wrote reaches the page as text and nothing else.
describe("a row built from hostile text", () => {
  const hostile = "\u001b[31mred\u001b[0m\u0007<script>alert(1)</script><img src=x onerror=alert(2)>\u202Eevil\nsecond line";
  const entry: RepositoryOutcome = {
    name: `api${"\u202E"}`,
    manifest_path: `../api\n<script>alert(3)</script>`,
    path: `/work/api\u001b]0;title\u0007`,
    remote: `git@github.com:acme/api.git\u202E`,
    outcome: "create_failed",
    reason: hostile,
    creation: {
      forge: "github",
      owner: "acme",
      forge_name: "api",
      visibility: "public",
      commit_error: hostile,
      steps: [{ step: "push", outcome: "failed", detail: hostile }],
      remedy: hostile,
      makefile: `api=${hostile}`,
    },
  };

  it("renders no element from it, and drops its escapes, bells, bidirectional marks, and line breaks", () => {
    const { container } = show({ repositories: [entry, { ...entry, name: "web", outcome: "refused", refusal_code: "gh_signed_out" }] });
    expect(container.querySelectorAll("script, img, iframe, object, style, link")).toHaveLength(0);
    const text = container.textContent ?? "";
    expect(text).toContain("<script>alert(1)</script>");
    expect(text).toContain("<img src=x onerror=alert(2)>");
    expect(text).not.toMatch(/[\u001b\u0007\u202E]/);
    expect(text).not.toContain("\n");
    expect(text).toContain("evil second line");
    expect([...container.querySelectorAll("a")].every((link) => (link.getAttribute("href") ?? "").startsWith("/"))).toBe(true);
  });

  it("copies a command on one line", async () => {
    const copy = vi.fn(async (_text: string) => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    show({ repositories: [{ ...entry, name: "web", outcome: "refused", refusal_code: "gh_signed_out" }] });
    await userEvent.click(screen.getAllByRole("button", { name: /^Copy/ })[0] as HTMLElement);
    const copied = String(copy.mock.calls[0]?.[0] ?? "");
    expect(copied).not.toMatch(/[\u001b\u0007\u202E\n]/);
    expect(copied).toContain("second line");
  });
});
