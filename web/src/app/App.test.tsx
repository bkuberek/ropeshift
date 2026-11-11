import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { FakeEvents, goldenNeedsYou, refusal, serveApi, stubEvents } from "../testing";
import { App } from "./App";

const needsYou = "/v1/needs-you";

beforeEach(() => {
  stubEvents();
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
});

describe("signing in", () => {
  it("asks for a passphrase when the server asks for a session", async () => {
    serveApi(() => refusal(401, "session_required"));
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
    expect(screen.getByLabelText("Passphrase").getAttribute("type")).toBe("password");
  });

  it("shows the queue once a session stands", async () => {
    let signedIn = false;
    const sent = serveApi((call) => {
      if (call.path === "/v1/sessions") {
        signedIn = true;
        return { status: 201, body: { member: "local", expires_at: 1 } };
      }
      return signedIn ? { status: 200, body: goldenNeedsYou() } : refusal(401, "session_required");
    });
    render(<App />);
    await userEvent.type(await screen.findByLabelText("Passphrase"), "a passphrase");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("heading", { name: "Needs you" })).toBeTruthy();
    expect(sent.find((call) => call.method === "POST")?.body).toEqual({ passphrase: "a passphrase" });
  });

  it("holds no secret anywhere a script could read it", async () => {
    let signedIn = false;
    serveApi((call) => {
      if (call.path === "/v1/sessions") {
        signedIn = true;
        return { status: 201, body: { member: "local", expires_at: 1 } };
      }
      return signedIn ? { status: 200, body: goldenNeedsYou() } : refusal(401, "session_required");
    });
    render(<App />);
    await userEvent.type(await screen.findByLabelText("Passphrase"), "a passphrase");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await screen.findByRole("heading", { name: "Needs you" });
    expect(JSON.stringify({ ...localStorage })).not.toContain("a passphrase");
    expect(JSON.stringify({ ...sessionStorage })).not.toContain("a passphrase");
    expect(document.body.innerHTML).not.toContain("a passphrase");
  });

  it("says what went wrong with a wrong passphrase", async () => {
    serveApi((call) => (call.path === "/v1/sessions" ? refusal(401, "bad_passphrase") : refusal(401, "session_required")));
    render(<App />);
    await userEvent.type(await screen.findByLabelText("Passphrase"), "a guess");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect((await screen.findByRole("alert")).textContent).toBe("That passphrase does not match.");
  });

  it("says how to set a passphrase when none is set, which this listener cannot do", async () => {
    serveApi((call) => (call.path === "/v1/sessions" ? refusal(401, "not_enrolled") : refusal(401, "session_required")));
    render(<App />);
    await userEvent.type(await screen.findByLabelText("Passphrase"), "a guess");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect((await screen.findByRole("alert")).textContent).toContain("ropeshift passphrase set");
  });

  it("keeps a half-written answer through a session's end", async () => {
    let live = true;
    serveApi((call) => {
      if (call.path === "/v1/sessions") {
        live = true;
        return { status: 201 };
      }
      if (call.path === needsYou) {
        return live ? { status: 200, body: goldenNeedsYou() } : refusal(401, "session_required");
      }
      return undefined;
    });
    render(<App />);
    const somethingElse = (await screen.findAllByRole("radio", { name: "Something else" }))[0];
    if (!somethingElse) {
      throw new Error("no such choice");
    }
    await userEvent.click(somethingElse);
    await userEvent.type(screen.getByRole("textbox", { name: "Your answer" }), "Half of a thought");
    live = false;
    FakeEvents.opened[0]?.emit();
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
    await userEvent.type(screen.getByLabelText("Passphrase"), "a passphrase");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await screen.findByRole("heading", { name: "Needs you" });
    expect((screen.getByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement).value).toBe("Half of a thought");
  });
});

describe("a session's token", () => {
  it("goes with every change, from the cookie a page can read", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serveApi((call) => (call.method === "GET" ? { status: 200, body: goldenNeedsYou() } : { status: 200 }));
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "Allow this one call" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST")).toBe(true));
    expect(sent.find((call) => call.method === "POST")?.csrf).toBe("token-from-cookie");
    expect(sent.find((call) => call.method === "GET")?.csrf).toBeNull();
  });
});

describe("signing out", () => {
  it("ends the session and returns to the sign-in", async () => {
    let live = true;
    const sent = serveApi((call) => {
      if (call.path === "/v1/sessions/end") {
        live = false;
        return { status: 204 };
      }
      return live ? { status: 200, body: goldenNeedsYou() } : refusal(401, "session_required");
    });
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "Sign out" }));
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
    expect(sent.some((call) => call.path === "/v1/sessions/end")).toBe(true);
    await waitFor(() => expect(FakeEvents.opened.every((source) => source.closed)).toBe(true));
    expect(document.body.textContent).not.toContain("Rotate the keys");
  });
});

describe("the stream", () => {
  const live = () => serveApi(() => ({ status: 200, body: goldenNeedsYou() }));

  it("follows from the revision the page read", async () => {
    live();
    render(<App />);
    await screen.findByRole("heading", { name: "Needs you" });
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    expect(FakeEvents.opened[0]?.path).toBe(`/v1/events?after=${goldenNeedsYou().revision}`);
  });

  it("reads again when something happens, once for a burst", async () => {
    const sent = live();
    render(<App />);
    await screen.findByRole("heading", { name: "Needs you" });
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    const reads = sent.length;
    const stream = FakeEvents.opened[0];
    stream?.emit();
    stream?.emit();
    stream?.emit();
    await waitFor(() => expect(sent.length).toBe(reads + 1));
  });

  it("says when it is live and when it is not", async () => {
    live();
    render(<App />);
    await screen.findByRole("heading", { name: "Needs you" });
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    const stream = FakeEvents.opened[0];
    stream?.onopen?.();
    expect((await screen.findByRole("status")).textContent).toBe("Live");
    stream?.onerror?.();
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("Reconnecting"));
  });
});

describe("the shell", () => {
  it("offers a way past the masthead", async () => {
    serveApi(() => ({ status: 200, body: goldenNeedsYou() }));
    render(<App />);
    expect(screen.getByRole("link", { name: "Skip to content" }).getAttribute("href")).toBe("#main");
    expect(screen.getByRole("main").id).toBe("main");
    await screen.findByRole("heading", { name: "Needs you" });
  });

  it("says when the daemon is away", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("connection refused"))));
    render(<App />);
    expect((await screen.findByRole("alert")).textContent).toContain("did not answer");
  });

  it("walks the themes and remembers the choice", async () => {
    serveApi(() => ({ status: 200, body: goldenNeedsYou() }));
    render(<App />);
    const toggle = screen.getByRole("button", { name: "Theme: system" });
    await userEvent.click(toggle);
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(localStorage.getItem("ropeshift-theme")).toBe("light");
    await userEvent.click(toggle);
    expect(document.documentElement.dataset.theme).toBe("dark");
    await userEvent.click(toggle);
    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(localStorage.getItem("ropeshift-theme")).toBeNull();
  });

  it("runs with no storage at all", async () => {
    serveApi(() => ({ status: 200, body: goldenNeedsYou() }));
    vi.stubGlobal("localStorage", {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
      removeItem: () => {
        throw new Error("blocked");
      },
    });
    render(<App />);
    const toggle = await screen.findByRole("button", { name: "Theme: system" });
    await userEvent.click(toggle);
    expect(document.documentElement.dataset.theme).toBe("light");
    await userEvent.click(toggle);
    await userEvent.click(toggle);
    vi.unstubAllGlobals();
  });
});
