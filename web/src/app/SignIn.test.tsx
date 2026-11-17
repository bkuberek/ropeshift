import { readFileSync } from "node:fs";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { openPage } from "../opening";
import { openLedgerStream, refusal, serveApi, serveViews, stubEvents } from "../testing";
import { App } from "./App";
import { passphraseHelp, signInStateIds } from "./SignIn";

// The Sign in page: the product's mark and name, the passphrase field and its errors, and one state for each way
// the page can find itself: a wrong passphrase, a session that ended, a daemon that does not
// answer, and a daemon with no passphrase set.

beforeEach(() => {
  window.history.replaceState(null, "", "/needs-you");
  stubEvents();
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const signedOut = (answerSignIn?: () => { status: number; body?: unknown }) =>
  serveApi((call) => (call.path === "/v1/sessions" && answerSignIn ? answerSignIn() : refusal(401, "session_required")));

const field = () => screen.getByLabelText("Passphrase", { exact: true }) as HTMLInputElement;
const press = () => userEvent.click(screen.getByRole("button", { name: "Sign in" }));

async function attempt(typed = "a guess at it") {
  render(<App />);
  await userEvent.type(await screen.findByLabelText("Passphrase", { exact: true }), typed);
  await press();
}

describe("the Sign in page", () => {
  it("shows the product's mark and name, the one heading, and the passphrase field a password manager and a paste can fill", async () => {
    signedOut();
    render(<App />);
    expect(await screen.findByRole("heading", { level: 1, name: "Sign in" })).toBeTruthy();
    const page = document.querySelector(".sign-in") as HTMLElement;
    expect(page.querySelector(".wordmark-mark svg")).toBeTruthy();
    expect(within(page).getByText("ropeshift")).toBeTruthy();
    expect(field().type).toBe("password");
    expect(field().autocomplete).toBe("current-password");
    expect(document.activeElement).toBe(field());
    await userEvent.paste("pasted-in-whole");
    expect(field().value).toBe("pasted-in-whole");
    expect(screen.getByText(/ropeshift init/)).toBeTruthy();
    // A page that has not been asked anything says nothing is wrong.
    expect(page.querySelector(".msg-error, .banner")).toBeNull();
    expect(field().getAttribute("aria-invalid")).toBeNull();
  });

  it("links the passphrase help to a heading of a guide this repository holds", () => {
    const link = new URL(passphraseHelp);
    expect(link.origin + link.pathname.split("/docs/")[0]).toBe("https://github.com/bkuberek/ropeshift/blob/main");
    const file = decodeURIComponent(link.pathname.split("/docs/")[1] ?? "");
    const guide = readFileSync(`../docs/${file}`, "utf8");
    const slugs = [...guide.matchAll(/^#{1,6} (.+)$/gm)].map((match) => (match[1] ?? "").toLowerCase().replace(/[^\p{L}\p{N} -]/gu, "").replace(/ /g, "-"));
    expect(slugs, `${file} has the heading ${link.hash}`).toContain(link.hash.slice(1));
  });

  it("offers the help in a new tab, with no referrer, and says so", async () => {
    signedOut();
    render(<App />);
    const link = await screen.findByRole("link", { name: /Passphrase help/ });
    expect(link.getAttribute("href")).toBe(passphraseHelp);
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toContain("noreferrer");
    expect(link.textContent).toContain("opens in a new tab");
  });

  it("names the fix when nothing was typed, sends nothing, and keeps the keyboard on the field", async () => {
    const sent = signedOut();
    render(<App />);
    await screen.findByRole("heading", { name: "Sign in" });
    await press();
    const message = within(field().closest(".field") as HTMLElement).getByText("Enter your passphrase.");
    expect(message).toBeTruthy();
    expect(field().getAttribute("aria-invalid")).toBe("true");
    expect(field().getAttribute("aria-describedby")).toContain(message.closest("p")?.id ?? "none");
    expect(document.activeElement).toBe(field());
    expect(sent.filter((call) => call.method === "POST")).toHaveLength(0);
  });

  it("shows a wrong passphrase at its field, empties it, and puts the keyboard back on it", async () => {
    signedOut(() => refusal(401, "bad_passphrase", "the passphrase does not match."));
    await attempt();
    const message = await screen.findByText("That passphrase does not match. Check it and try again.");
    expect(message.closest(".field")).toBe(field().closest(".field"));
    expect(field().value).toBe("");
    expect(field().getAttribute("aria-invalid")).toBe("true");
    expect(document.activeElement).toBe(field());
    // Typing again takes the message back, since it no longer describes what is typed.
    await userEvent.type(field(), "x");
    expect(screen.queryByText(/does not match/)).toBeNull();
  });

  it("says when the daemon did not answer, as an alert, and leaves nothing checked", async () => {
    signedOut();
    render(<App />);
    await screen.findByRole("heading", { name: "Sign in" });
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("network")));
    await userEvent.type(field(), "a guess at it");
    await press();
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("The daemon did not answer, so nothing was checked.");
    expect(alert.textContent).toContain("ropeshift health");
    expect(field().getAttribute("aria-invalid")).toBeNull();
  });

  it("says how to set a passphrase when none is set, which this listener cannot do", async () => {
    signedOut(() => refusal(401, "not_enrolled", "no passphrase is set"));
    await attempt();
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("No passphrase is set yet. Run ropeshift passphrase set on a terminal, then sign in here.");
  });

  it("shows any other refusal in the daemon's words", async () => {
    signedOut(() => refusal(503, "unavailable", "the store is busy"));
    await attempt();
    expect((await screen.findByRole("alert")).textContent).toContain("the store is busy");
  });

  it("says a session ended when one was open, and says nothing of one on a first visit or after signing out", async () => {
    let live = true;
    serveViews((call) => (live || call.path === "/v1/sessions" ? undefined : refusal(401, "session_required")));
    openPage("/roadmap");
    await screen.findByRole("region", { name: "Ship solo mode" });
    const stream = await openLedgerStream();
    live = false;
    stream.emit();
    await screen.findByRole("heading", { name: "Sign in" });
    const notice = await screen.findByText("Your session ended. Sign in again to carry on where you were.");
    expect(notice.closest("[role=status]")).toBeTruthy();
    cleanup();

    // A first visit has had no session to end.
    serveViews(() => refusal(401, "session_required"));
    openPage("/roadmap");
    await screen.findByRole("heading", { name: "Sign in" });
    expect(screen.queryByText(/Your session ended/)).toBeNull();
    cleanup();

    // Signing out is a choice, so it is not announced as a loss.
    live = true;
    serveViews((call) => {
      if (call.path === "/v1/sessions/end") {
        live = false;
        return { status: 204 };
      }
      return live ? undefined : refusal(401, "session_required");
    });
    openPage("/decisions");
    await screen.findByRole("article", { name: "Retry twice." });
    await userEvent.click(screen.getByRole("button", { name: "Account" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Sign out" }));
    await screen.findByRole("heading", { name: "Sign in" });
    expect(screen.queryByText(/Your session ended/)).toBeNull();
  });

  it("keeps the notice that a session ended through a wrong passphrase", async () => {
    let live = true;
    serveViews((call) => {
      if (call.path === "/v1/sessions") {
        return refusal(401, "bad_passphrase");
      }
      return live ? undefined : refusal(401, "session_required");
    });
    openPage("/roadmap");
    await screen.findByRole("region", { name: "Ship solo mode" });
    const stream = await openLedgerStream();
    live = false;
    stream.emit();
    await userEvent.type(await screen.findByLabelText("Passphrase", { exact: true }), "a guess at it");
    await press();
    expect(await screen.findByText("That passphrase does not match. Check it and try again.")).toBeTruthy();
    expect(screen.getByText(/Your session ended/)).toBeTruthy();
  });

  it("sends one request for a form submitted twice while the first is out", async () => {
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => (release = resolve));
    const sent = signedOut(() => ({ status: 201, body: { member: "local", expires_at: 1 } }));
    render(<App />);
    await userEvent.type(await screen.findByLabelText("Passphrase", { exact: true }), "a guess at it");
    const answering = globalThis.fetch;
    vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        await held;
      }
      return answering(url, init);
    });
    fireEvent.submit(field().closest("form") as HTMLFormElement);
    fireEvent.submit(field().closest("form") as HTMLFormElement);
    release();
    await waitFor(() => expect(sent.filter((call) => call.method === "POST")).toHaveLength(1));
  });

  it("names the states it can show, one for each the accessibility checks open", () => {
    expect([...signInStateIds]).toEqual(["default", "ended", "empty", "wrong", "unreachable", "not-set"]);
  });
});
