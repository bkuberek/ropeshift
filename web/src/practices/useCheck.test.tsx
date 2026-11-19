import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { PracticesRequest, PracticesView } from "../api/practices";
import type { Sent } from "../testing";
import { refusal, serveApi } from "../testing";
import { pollMs } from "./logic";
import { useCheck } from "./useCheck";

// The follow of an open request reads once a second while the tab is shown, one read at a time, and stops with the
// request, the page, or a refused session.

const request = (id: string, repository: string): PracticesRequest => ({ id, repository, worker: "plan", cause: "member", requested_by: "m", requested_at: 1, expires_at: 2, state: "offered" });
const views = (...ids: string[]): PracticesView[] => ids.map((id) => ({ repository: `repo-${id}`, name: id, request: request(`request-${id}`, `repo-${id}`) }));

const reads = (sent: Sent[]) => sent.filter((call) => call.method === "GET" && call.path.startsWith("/v1/practices/checks/"));

function visibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { value: state, configurable: true });
  document.dispatchEvent(new Event("visibilitychange"));
}

const tick = (count = 1) => act(async () => void (await vi.advanceTimersByTimeAsync(pollMs * count)));

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
});

const follow = (list: PracticesView[], checkSession = vi.fn(async (): Promise<void> => undefined)) => {
  const again = vi.fn();
  const hook = renderHook(() => useCheck(list, again, { heard: 0, checkSession }));
  return { ...hook, again, checkSession };
};

describe("following an open request", () => {
  it("reads each open request once a second", async () => {
    const sent = serveApi(() => ({ status: 200, body: request("request-a", "repo-a") }));
    follow(views("a"));
    await tick(3);
    expect(reads(sent)).toHaveLength(3);
  });

  it("reads nothing while the tab is hidden", async () => {
    const sent = serveApi(() => ({ status: 200, body: request("request-a", "repo-a") }));
    visibility("hidden");
    follow(views("a"));
    await tick(5);
    expect(reads(sent)).toHaveLength(0);
  });

  it("stops reading when the tab is hidden, and reads once when it is shown again", async () => {
    const sent = serveApi(() => ({ status: 200, body: request("request-a", "repo-a") }));
    follow(views("a"));
    await tick(2);
    expect(reads(sent)).toHaveLength(2);
    act(() => visibility("hidden"));
    await tick(5);
    expect(reads(sent)).toHaveLength(2);
    await act(async () => visibility("visible"));
    expect(reads(sent)).toHaveLength(3);
    await tick(1);
    expect(reads(sent)).toHaveLength(4);
  });

  it("keeps one read in flight, however many requests are open and however slow the daemon is", async () => {
    let release: () => void = () => undefined;
    const sent = serveApi(() => ({ status: 200, body: request("request-a", "repo-a") }));
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        sent.push({ method: "GET", url, path: url.replace("/v1/projects/default", "/v1"), project: "default", body: undefined, csrf: null });
        await new Promise<void>((resolve) => {
          release = resolve;
        });
        return new Response(JSON.stringify(request("request-a", "repo-a")), { status: 200 });
      }),
    );
    follow(views("a", "b", "c"));
    await tick(4);
    expect(reads(sent)).toHaveLength(1);
    await act(async () => release());
    await tick(1);
    expect(reads(sent).length).toBeGreaterThan(1);
  });

  it("stops reading when the page goes away", async () => {
    const sent = serveApi(() => ({ status: 200, body: request("request-a", "repo-a") }));
    const { unmount } = follow(views("a"));
    await tick(2);
    unmount();
    await tick(5);
    expect(reads(sent)).toHaveLength(2);
  });

  it("reads nothing more of a request once it is reported, and reads the list again", async () => {
    const sent = serveApi(() => ({ status: 200, body: { ...request("request-a", "repo-a"), state: "reported" } }));
    const { again, result } = follow(views("a"));
    await tick(1);
    expect(again).toHaveBeenCalledTimes(1);
    expect(result.current.ended).toEqual({ "request-a": "reported" });
    await tick(5);
    expect(reads(sent)).toHaveLength(1);
  });

  it("says a repository's request expired, and reads nothing more of it", async () => {
    const sent = serveApi(() => ({ status: 200, body: { ...request("request-a", "repo-a"), state: "expired" } }));
    const { result } = follow(views("a"));
    await tick(1);
    await tick(3);
    expect(result.current.expired).toEqual({ "repo-a": true });
    expect(reads(sent)).toHaveLength(1);
  });

  it("asks the shell whether the session is still held when a read is refused for it, once, and reads no more until that answers", async () => {
    const sent = serveApi(() => refusal(401, "session_required", "sign in"));
    let answer: () => void = () => undefined;
    const checkSession = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          answer = resolve;
        }),
    );
    follow(views("a"), checkSession);
    await tick(4);
    expect(checkSession).toHaveBeenCalledTimes(1);
    expect(reads(sent)).toHaveLength(1);
    await act(async () => answer());
    await tick(1);
    expect(reads(sent)).toHaveLength(2);
  });

  it("keeps reading after a failure that is not the session", async () => {
    const sent = serveApi(() => refusal(500, "unavailable", "down"));
    const { checkSession } = follow(views("a"));
    await tick(3);
    expect(reads(sent)).toHaveLength(3);
    expect(checkSession).not.toHaveBeenCalled();
  });
});
