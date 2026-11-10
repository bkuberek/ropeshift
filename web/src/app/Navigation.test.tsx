import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { openPage } from "../opening";
import { FakeEvents, golden, refusal, serveViews } from "../testing";

const views = () => screen.findByRole("navigation", { name: "Views" });
const go = async (name: string | RegExp) => userEvent.click(within(await views()).getByRole("link", { name }));

describe("the views", () => {
  it("offers every view of the design", async () => {
    serveViews();
    openPage("/");
    const names = within(await views()).getAllByRole("link").map((link) => link.textContent);
    expect(names).toEqual([
      "Needs you 11 waiting", "Roadmap", "Plans", "Requirements", "Specs", "Designs", "Other documents", "Decisions", "Open questions",
      "In progress", "On hold", "Stuck", "Completed", "Shop floor", "Operations",
    ]);
  });

  it("moves to a view with no reload", async () => {
    const sent = serveViews();
    openPage("/");
    await go("Roadmap");
    expect(await screen.findByRole("heading", { level: 1, name: "Roadmap" })).toBeTruthy();
    expect(window.location.pathname).toBe("/roadmap");
    expect(sent.some((call) => call.path === "/v1/views/roadmap")).toBe(true);
  });

  it("marks where a person is", async () => {
    serveViews();
    openPage("/roadmap");
    expect(within(await views()).getByRole("link", { name: "Roadmap" }).getAttribute("aria-current")).toBe("page");
    expect(within(await views()).getByRole("link", { name: "Plans" }).getAttribute("aria-current")).toBeNull();
  });

  it("marks a view from any address inside it", async () => {
    serveViews();
    openPage("/plans?plan=no-such-plan");
    expect(within(await views()).getByRole("link", { name: "Plans" }).getAttribute("aria-current")).toBe("page");
  });

  it("follows the back button", async () => {
    serveViews();
    openPage("/");
    await go("Roadmap");
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    window.history.back();
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(await screen.findByRole("heading", { level: 1, name: "Needs you" })).toBeTruthy();
  });

  it("opens straight on a view from its address", async () => {
    serveViews();
    openPage("/decisions");
    expect(await screen.findByRole("heading", { level: 1, name: "Decisions" })).toBeTruthy();
  });

  it("names the page for the browser and for a screen reader", async () => {
    serveViews();
    openPage("/roadmap");
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    await waitFor(() => expect(document.title).toBe("Roadmap - ropeshift"));
    await go(/^Needs you/);
    await waitFor(() => expect(document.title).toBe("Needs you - ropeshift"));
  });

  it("puts keyboard focus on the new view's title, so a screen reader hears it", async () => {
    serveViews();
    openPage("/");
    await go("Decisions");
    const title = await screen.findByRole("heading", { level: 1, name: "Decisions" });
    await waitFor(() => expect(document.activeElement).toBe(title));
  });

  it("leaves focus alone on a page that just opened, so the skip link stays first", async () => {
    serveViews();
    openPage("/decisions");
    await screen.findByRole("heading", { level: 1, name: "Decisions" });
    expect(document.activeElement).toBe(document.body);
  });

  it("says when an address holds no view", async () => {
    serveViews();
    openPage("/no-such-view");
    expect(await screen.findByRole("heading", { level: 1, name: "No such view" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Back to Needs you" }).getAttribute("href")).toBe("/");
  });

  it("leaves a modified click to the browser, which opens a new tab", async () => {
    serveViews();
    openPage("/");
    const link = within(await views()).getByRole("link", { name: "Roadmap" });
    let left = false;
    const watch = (event: Event) => {
      left = !event.defaultPrevented;
      event.preventDefault();
    };
    document.addEventListener("click", watch);
    fireEvent.click(link, { ctrlKey: true });
    document.removeEventListener("click", watch);
    expect(left).toBe(true);
    expect(window.location.pathname).toBe("/");
  });

  it("folds the list behind a button a phone can reach", async () => {
    serveViews();
    openPage("/");
    const toggle = await screen.findByRole("button", { name: /^Views/ });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    await userEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(document.getElementById(toggle.getAttribute("aria-controls") ?? "")).not.toBeNull();
    await go("Roadmap");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
  });
});

describe("what waits, from anywhere", () => {
  it("shows the count on every view", async () => {
    serveViews();
    openPage("/roadmap");
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    expect(within(await views()).getByRole("link", { name: /^Needs you/ }).textContent).toContain("11 waiting");
  });

  it("drops the count when nothing waits", async () => {
    serveViews((call) => (call.path === "/v1/views/needs-you" ? { status: 200, body: { ...golden<object>("needs-you"), items: [] } } : undefined));
    openPage("/roadmap");
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    expect(within(await views()).getByRole("link", { name: "Needs you" })).toBeTruthy();
  });

  it("keeps a half-written answer when a person looks elsewhere and comes back", async () => {
    serveViews();
    openPage("/");
    const somethingElse = (await screen.findAllByRole("radio", { name: "Something else" }))[0];
    if (!somethingElse) {
      throw new Error("no such choice");
    }
    await userEvent.click(somethingElse);
    await userEvent.type(screen.getByRole("textbox", { name: "Your answer" }), "Half of a thought");
    await go("Decisions");
    await screen.findByRole("heading", { level: 1, name: "Decisions" });
    await go(/^Needs you/);
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    expect((screen.getByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement).value).toBe("Half of a thought");
  });
});

describe("a view on the stream", () => {
  it("reads again when something happens", async () => {
    const sent = serveViews();
    openPage("/roadmap");
    await screen.findByRole("region", { name: "Ship solo mode" });
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    const reads = sent.filter((call) => call.path === "/v1/views/roadmap").length;
    FakeEvents.opened[0]?.emit();
    await waitFor(() => expect(sent.filter((call) => call.path === "/v1/views/roadmap").length).toBe(reads + 1));
  });

  it("states the revision and age of what it shows", async () => {
    serveViews();
    openPage("/roadmap");
    await screen.findByRole("region", { name: "Ship solo mode" });
    expect(screen.getByText(/^As of revision \d+/)).toBeTruthy();
  });

  it("keeps showing a view while it reads again", async () => {
    let held = false;
    let answer: (() => void) | undefined;
    const body = golden<object>("roadmap");
    serveViews((call) => {
      if (call.path !== "/v1/views/roadmap") {
        return undefined;
      }
      if (held) {
        return { status: 503, body: { error: { code: "unavailable", message: "The ledger is busy." } } };
      }
      return { status: 200, body };
    });
    openPage("/roadmap");
    await screen.findByRole("region", { name: "Ship solo mode" });
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    held = true;
    answer = () => FakeEvents.opened[0]?.emit();
    answer();
    expect((await screen.findByRole("alert")).textContent).toContain("The ledger is busy.");
    expect(screen.getByRole("region", { name: "Ship solo mode" })).toBeTruthy();
  });

  it("holds a costly list to one read in its gap, however much a person hears", async () => {
    const sent = serveViews();
    openPage("/specs");
    await screen.findByRole("link", { name: "The retry spec" });
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    const reads = () => sent.filter((call) => call.path === "/v1/views/documents").length;
    const first = reads();
    for (let event = 0; event < 3; event += 1) {
      FakeEvents.opened[0]?.emit();
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    expect(reads()).toBe(first);
    expect(screen.getByRole("main").querySelector("[aria-busy='true']")).toBeNull();
  });

  it("reads a document once, because nothing on the stream changes it", async () => {
    const sent = serveViews();
    openPage("/document?path=docs%2Fspecs%2Fretry.md");
    await screen.findByRole("heading", { level: 1, name: "The retry spec" });
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    FakeEvents.opened[0]?.emit();
    await new Promise((resolve) => setTimeout(resolve, 150));
    expect(sent.filter((call) => call.path.startsWith("/v1/documents/read"))).toHaveLength(1);
  });

  it("reads through a burst that never settles, at a bound", async () => {
    const sent = serveViews();
    openPage("/roadmap");
    await screen.findByRole("region", { name: "Ship solo mode" });
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    const reads = () => sent.filter((call) => call.path === "/v1/views/needs-you").length;
    const first = reads();
    vi.useFakeTimers();
    for (let event = 0; event < 45; event += 1) {
      FakeEvents.opened[0]?.emit();
      vi.advanceTimersByTime(50);
    }
    vi.useRealTimers();
    await waitFor(() => expect(reads()).toBeGreaterThan(first));
  });

  it("never lets an older answer replace a newer one", async () => {
    const answers: ((value: Response) => void)[] = [];
    const roadmap = golden<{ goals: { goal?: { title: string } }[] }>("roadmap");
    const later = structuredClone(roadmap);
    if (later.goals[0]?.goal) {
      later.goals[0].goal.title = "The newer goal";
    }
    serveViews();
    const ordinary = globalThis.fetch;
    globalThis.fetch = ((path: string, init?: RequestInit) => {
      if (path === "/v1/views/roadmap") {
        return new Promise<Response>((resolve) => answers.push(resolve));
      }
      return ordinary(path, init);
    }) as typeof fetch;
    openPage("/roadmap");
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1)).catch(() => undefined);
    await waitFor(() => expect(answers.length).toBeGreaterThanOrEqual(1));
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    FakeEvents.opened[0]?.emit();
    await waitFor(() => expect(answers.length).toBeGreaterThanOrEqual(2));
    answers[1]?.(new Response(JSON.stringify(later), { status: 200 }));
    expect(await screen.findByRole("region", { name: "The newer goal" })).toBeTruthy();
    answers[0]?.(new Response(JSON.stringify(roadmap), { status: 200 }));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.getByRole("region", { name: "The newer goal" })).toBeTruthy();
  });
});

describe("a view and a session", () => {
  it("asks for a passphrase when a view finds the session gone, and keeps the page", async () => {
    let live = true;
    serveViews((call) => (live || call.path === "/v1/sessions" ? undefined : refusal(401, "session_required")));
    openPage("/roadmap");
    await screen.findByRole("region", { name: "Ship solo mode" });
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    live = false;
    FakeEvents.opened[0]?.emit();
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
  });

  it("learns from a view's own refusal that the session ended", async () => {
    let live = true;
    serveViews((call) => {
      if (call.path === "/v1/views/decisions") {
        live = false;
      }
      return live ? undefined : refusal(401, "session_required");
    });
    openPage("/");
    await go("Decisions");
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
  });

  it("returns to the view a person asked for after signing in", async () => {
    let live = false;
    serveViews((call) => {
      if (call.path === "/v1/sessions") {
        live = true;
        return { status: 201 };
      }
      return live ? undefined : refusal(401, "session_required");
    });
    openPage("/decisions");
    await userEvent.type(await screen.findByLabelText("Passphrase"), "a passphrase");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Decisions" })).toBeTruthy();
    expect(await screen.findByRole("article", { name: "Retry twice." })).toBeTruthy();
  });

  it("leaves nothing of a view in the page after signing out", async () => {
    let live = true;
    serveViews((call) => {
      if (call.path === "/v1/sessions/end") {
        live = false;
        return { status: 204 };
      }
      return live ? undefined : refusal(401, "session_required");
    });
    openPage("/decisions");
    await screen.findByRole("article", { name: "Retry twice." });
    await userEvent.click(screen.getByRole("button", { name: "Sign out" }));
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
    expect(document.body.textContent).not.toContain("Retry twice.");
    expect(screen.queryByRole("navigation", { name: "Views" })).toBeNull();
  });

  it("offers no view to a page with no session", async () => {
    serveViews(() => refusal(401, "session_required"));
    openPage("/roadmap");
    await screen.findByRole("heading", { name: "Sign in" });
    expect(screen.queryByRole("navigation", { name: "Views" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "Roadmap" })).toBeNull();
  });
});
