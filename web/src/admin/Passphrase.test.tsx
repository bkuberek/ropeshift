import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { openPage } from "../opening";
import type { Sent } from "../testing";
import { refusal, serveViews } from "../testing";

// The Change passphrase section of Settings: the form sends the current passphrase and the new one to the route
// the command line's `passphrase set` uses, the listener ends the member's other sessions, and the page says so.

const route = "/v1/passphrase";
const changes = (sent: Sent[]) => sent.filter((call) => call.method === "PUT" && call.path === route);

function serve(answer?: (sent: Sent) => { status: number; body?: unknown } | undefined) {
  return serveViews((call) => (call.method === "PUT" && call.path === route ? (answer?.(call) ?? { status: 200, body: { sessions_ended: 0 } }) : undefined));
}

const current = () => screen.getByLabelText("Current passphrase", { exact: true }) as HTMLInputElement;
const fresh = () => screen.getByLabelText("New passphrase", { exact: true }) as HTMLInputElement;
const again = () => screen.getByLabelText("New passphrase, again", { exact: true }) as HTMLInputElement;
const submit = () => screen.getByRole("button", { name: "Change passphrase" });
const fieldOf = (control: HTMLElement) => control.closest(".field") as HTMLElement;

async function open() {
  openPage("/settings?section=passphrase");
  await screen.findByLabelText("Current passphrase", { exact: true });
}

function fill(now: string, next: string, repeat = next) {
  fireEvent.change(current(), { target: { value: now } });
  fireEvent.change(fresh(), { target: { value: next } });
  fireEvent.change(again(), { target: { value: repeat } });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.localStorage.clear();
  window.sessionStorage.clear();
});

describe("Change passphrase", () => {
  it("is a section of Settings under You, and shows the minimum length and what a change ends before anything is typed", async () => {
    serve();
    await open();
    const tree = screen.getByRole("navigation", { name: "Settings sections" });
    expect(within(tree).getByRole("link", { name: "Change passphrase" }).getAttribute("href")).toBe("/settings?section=passphrase");
    expect(screen.getByRole("heading", { level: 1, name: "Change passphrase" })).toBeTruthy();
    expect(within(fieldOf(fresh())).getByText(/at least 8 bytes/i)).toBeTruthy();
    expect(screen.getByText(/ends your other sessions, in every browser and terminal/)).toBeTruthy();
    for (const field of [current(), fresh(), again()]) {
      expect(field.type).toBe("password");
      expect(field.value).toBe("");
    }
    expect(current().autocomplete).toBe("current-password");
    expect(fresh().autocomplete).toBe("new-password");
    expect(again().autocomplete).toBe("new-password");
    expect(document.querySelector(".msg-error")).toBeNull();
  });

  it("sends the current and the new passphrase once, with the page's token, and says how many other sessions ended", async () => {
    const sent = serve(() => ({ status: 200, body: { sessions_ended: 2 } }));
    await open();
    fill("tin-roof-anvil-1", "copper-kettle-moss-2");
    await userEvent.click(submit());
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({ current: "tin-roof-anvil-1", passphrase: "copper-kettle-moss-2" });
    expect(changes(sent)[0]?.csrf).not.toBeNull();
    const notice = await screen.findByText(/Passphrase changed\. 2 other sessions ended/);
    expect(notice.closest("[role=status]")).toBeTruthy();
    // The form holds no passphrase once it is sent, and the notice repeats none.
    for (const field of [current(), fresh(), again()]) {
      expect(field.value).toBe("");
    }
    expect(document.body.textContent).not.toContain("copper-kettle-moss-2");
    expect(document.body.textContent).not.toContain("tin-roof-anvil-1");
  });

  it("says when no other session was open, and when one was", async () => {
    let ended = 0;
    serve(() => ({ status: 200, body: { sessions_ended: ended } }));
    await open();
    fill("the old passphrase", "the new passphrase");
    await userEvent.click(submit());
    expect(await screen.findByText(/Passphrase changed\. No other session was open/)).toBeTruthy();
    ended = 1;
    fill("the new passphrase", "a third passphrase");
    await userEvent.click(submit());
    expect(await screen.findByText(/Passphrase changed\. 1 other session ended/)).toBeTruthy();
  });

  it("names the fix at each field and sends nothing, and puts the keyboard on the first invalid field", async () => {
    const sent = serve();
    await open();
    fill("", "short", "different");
    await userEvent.click(submit());
    expect(within(fieldOf(current())).getByText("Enter your current passphrase.")).toBeTruthy();
    expect(within(fieldOf(fresh())).getByText("Use at least 8 bytes. This one has 5.")).toBeTruthy();
    expect(within(fieldOf(again())).getByText("The two new passphrases differ. Type the new passphrase again exactly.")).toBeTruthy();
    expect(current().getAttribute("aria-invalid")).toBe("true");
    expect(fresh().getAttribute("aria-invalid")).toBe("true");
    expect(document.activeElement).toBe(current());
    expect(changes(sent)).toHaveLength(0);
    // With the current passphrase given, the next invalid field is the new one.
    fireEvent.change(current(), { target: { value: "the old passphrase" } });
    await userEvent.click(submit());
    expect(document.activeElement).toBe(fresh());
    expect(changes(sent)).toHaveLength(0);
  });

  it("counts bytes and not characters, as the daemon does", async () => {
    const sent = serve();
    await open();
    // Three characters of two bytes each are six bytes, and four are eight.
    fill("the old passphrase", "ééé");
    await userEvent.click(submit());
    expect(within(fieldOf(fresh())).getByText("Use at least 8 bytes. This one has 6.")).toBeTruthy();
    expect(changes(sent)).toHaveLength(0);
    fill("the old passphrase", "éééé");
    await userEvent.click(submit());
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
  });

  it("shows a wrong current passphrase at its field, empties it, and keeps the new ones", async () => {
    serve(() => refusal(401, "bad_passphrase", "the passphrase does not match."));
    await open();
    fill("a wrong passphrase", "the new passphrase");
    await userEvent.click(submit());
    expect(await within(fieldOf(current())).findByText("That is not your current passphrase. Check it and try again.")).toBeTruthy();
    expect(current().value).toBe("");
    expect(fresh().value).toBe("the new passphrase");
    expect(again().value).toBe("the new passphrase");
    expect(document.activeElement).toBe(current());
    expect(screen.queryByText(/Passphrase changed/)).toBeNull();
  });

  it("shows the daemon's refusal of a new passphrase at its field, in the daemon's words", async () => {
    serve(() => refusal(400, "invalid", "a passphrase has at least 8 bytes"));
    await open();
    fill("the old passphrase", "the new passphrase");
    await userEvent.click(submit());
    expect(await within(fieldOf(fresh())).findByText("a passphrase has at least 8 bytes")).toBeTruthy();
    expect(current().value).toBe("the old passphrase");
  });

  it("says what to do when no passphrase is set, since the first one is set on a terminal", async () => {
    serve(() => refusal(401, "not_enrolled", "no passphrase is set: run ropeshift passphrase set on a terminal"));
    await open();
    fill("the old passphrase", "the new passphrase");
    await userEvent.click(submit());
    expect((await screen.findByRole("alert")).textContent).toContain("No passphrase is set yet. Run ropeshift passphrase set on a terminal.");
  });

  it("says when the session ended, and when the daemon did not answer, and changes nothing on either", async () => {
    serve(() => refusal(401, "session_required", "this session expired"));
    await open();
    fill("the old passphrase", "the new passphrase");
    await userEvent.click(submit());
    expect((await screen.findByRole("alert")).textContent).toContain("Your session ended. Sign in again to change the passphrase.");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("network")));
    fill("the old passphrase", "the new passphrase");
    await userEvent.click(submit());
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("The daemon did not answer, so nothing changed."));
    expect(fresh().value).toBe("the new passphrase");
  });

  it("sends the change from the keyboard, with Enter in a field", async () => {
    const sent = serve();
    await open();
    fireEvent.change(current(), { target: { value: "the old passphrase" } });
    fireEvent.change(fresh(), { target: { value: "the new passphrase" } });
    await userEvent.type(again(), "the new passphrase{Enter}");
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
  });

  it("sends one request for a double press while the change is in flight", async () => {
    const sent = serve();
    await open();
    const answering = globalThis.fetch;
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => (release = resolve));
    vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
      if (init?.method === "PUT") {
        await held;
      }
      return answering(url, init);
    });
    fill("the old passphrase", "the new passphrase");
    fireEvent.click(submit());
    fireEvent.click(submit());
    release();
    await screen.findByText(/Passphrase changed/);
    expect(changes(sent)).toHaveLength(1);
  });

  it("keeps a passphrase out of the address, the page's text, and the browser's storage", async () => {
    serve();
    await open();
    fill("tin-roof-anvil-1", "copper-kettle-moss-2");
    await userEvent.click(submit());
    await screen.findByText(/Passphrase changed/);
    expect(window.location.href).not.toMatch(/tin-roof-anvil-1|copper-kettle-moss-2/);
    for (const store of [window.localStorage, window.sessionStorage]) {
      for (let at = 0; at < store.length; at++) {
        const name = store.key(at) ?? "";
        expect(`${name}=${store.getItem(name)}`).not.toMatch(/tin-roof-anvil-1|copper-kettle-moss-2/);
      }
    }
  });
});
