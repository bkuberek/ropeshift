import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { lazy, Suspense } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ErrorBoundary, isStaleChunk, reloadOnceForUpdate, reloading } from "./ErrorBoundary";

// A page that fails to draw leaves a person an empty screen with no way to tell why, so a boundary says what failed and
// offers Reload. A lazy chunk that no longer exists means the daemon was updated under an open page, which has its own words.

function Broken({ message }: { message: string }): never {
  throw new Error(message);
}

const staleMessage = "Failed to fetch dynamically imported module: http://127.0.0.1:7317/assets/Agents-3f2a.js";

beforeEach(() => {
  sessionStorage.clear();
  vi.spyOn(console, "error").mockImplementation(() => {});
});

afterEach(() => {
  // A test that stubs sessionStorage leaves its stub until the stubs go, so they go before the real one is cleared.
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  sessionStorage.clear();
  document.body.replaceChildren();
});

describe("isStaleChunk", () => {
  it("knows the words each browser gives a dynamic import that cannot load", () => {
    expect(isStaleChunk(new TypeError("Failed to fetch dynamically imported module: /assets/a.js"))).toBe(true);
    expect(isStaleChunk(new TypeError("error loading dynamically imported module: /assets/a.js"))).toBe(true);
    expect(isStaleChunk(new TypeError("Importing a module script failed."))).toBe(true);
    expect(isStaleChunk(Object.assign(new Error("Loading chunk 3 failed."), { name: "ChunkLoadError" }))).toBe(true);
  });

  it("does not take any other failure for one", () => {
    expect(isStaleChunk(new Error("Cannot read properties of undefined"))).toBe(false);
    expect(isStaleChunk("a string")).toBe(false);
    expect(isStaleChunk(undefined)).toBe(false);
  });
});

describe("the error boundary", () => {
  it("shows its children while nothing fails", () => {
    render(
      <ErrorBoundary scope="page" name="Overview">
        <p>all well</p>
      </ErrorBoundary>,
    );
    expect(screen.getByText("all well")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("says what failed and offers Reload, instead of an empty screen", async () => {
    const reload = vi.spyOn(reloading, "now").mockImplementation(() => {});
    render(
      <ErrorBoundary scope="page" name="Overview">
        <Broken message="Cannot read properties of undefined (reading 'name')" />
      </ErrorBoundary>,
    );
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("Overview could not be shown");
    expect(alert.textContent).toContain("Cannot read properties of undefined (reading 'name')");
    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("lets a page try again without a reload, and shows the page once it draws", async () => {
    let broken = true;
    function Sometimes() {
      if (broken) {
        throw new Error("not yet");
      }
      return <p>drawn</p>;
    }
    render(
      <ErrorBoundary scope="page" name="Overview">
        <Sometimes />
      </ErrorBoundary>,
    );
    broken = false;
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(screen.getByText("drawn")).toBeTruthy();
  });

  it("starts over when the page it holds changes", () => {
    const { rerender } = render(
      <ErrorBoundary scope="page" name="Overview" reset="overview">
        <Broken message="boom" />
      </ErrorBoundary>,
    );
    expect(screen.getByRole("alert")).toBeTruthy();
    rerender(
      <ErrorBoundary scope="page" name="Floor" reset="floor">
        <p>the floor</p>
      </ErrorBoundary>,
    );
    expect(screen.getByText("the floor")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("says ropeshift was updated when a lazy chunk cannot load, and reloads once by itself", async () => {
    const reload = vi.spyOn(reloading, "now").mockImplementation(() => {});
    const Page = lazy(() => Promise.reject(new TypeError(staleMessage)));
    render(
      <ErrorBoundary scope="page" name="Agents">
        <Suspense fallback={<p>reading</p>}>
          <Page />
        </Suspense>
      </ErrorBoundary>,
    );
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("ropeshift was updated");
    expect(alert.textContent).toContain("Reload");
    expect(reload).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
  });

  it("does not reload by itself a second time at once, so a chunk that stays missing cannot loop", async () => {
    const reload = vi.spyOn(reloading, "now").mockImplementation(() => {});
    const attempt = () => {
      const Page = lazy(() => Promise.reject(new TypeError(staleMessage)));
      return render(
        <ErrorBoundary scope="page" name="Agents">
          <Suspense fallback={null}>
            <Page />
          </Suspense>
        </ErrorBoundary>,
      );
    };
    const first = attempt();
    await screen.findByRole("alert");
    first.unmount();
    attempt();
    await screen.findByRole("alert");
    expect(reload).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("alert").textContent).toContain("ropeshift was updated");
  });
});

describe("the failure on screen", () => {
  it("names what failed in a heading and moves focus there, so a keyboard or a screen reader lands on it", async () => {
    render(
      <ErrorBoundary scope="page" name="Overview">
        <Broken message="boom" />
      </ErrorBoundary>,
    );
    const heading = screen.getByRole("heading", { level: 1, name: /Overview could not be shown/ });
    await waitFor(() => expect(document.activeElement).toBe(heading));
  });

  it("gives the whole-application failure the main landmark the skip link goes to", () => {
    render(
      <ErrorBoundary scope="app">
        <Broken message="boom" />
      </ErrorBoundary>,
    );
    const main = screen.getByRole("main");
    expect(main.id).toBe("main");
    expect(main.getAttribute("tabindex")).toBe("-1");
  });

  it("does not reload by itself while something a person wrote is not sent, and says so", async () => {
    const reload = vi.spyOn(reloading, "now").mockImplementation(() => {});
    const draft = document.createElement("textarea");
    draft.value = "Use the hosted runner, because";
    document.body.append(draft);
    const Page = lazy(() => Promise.reject(new TypeError(staleMessage)));
    render(
      <ErrorBoundary scope="page" name="Agents">
        <Suspense fallback={null}>
          <Page />
        </Suspense>
      </ErrorBoundary>,
    );
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("ropeshift was updated");
    expect(alert.textContent).toContain("did not reload by itself");
    expect(reload).not.toHaveBeenCalled();
    expect(sessionStorage.getItem("ropeshift:reloaded-for-update")).toBeNull();
  });

  it("reloads by itself when what is written is empty", async () => {
    const reload = vi.spyOn(reloading, "now").mockImplementation(() => {});
    const draft = document.createElement("textarea");
    draft.value = "   ";
    document.body.append(draft);
    const Page = lazy(() => Promise.reject(new TypeError(staleMessage)));
    render(
      <ErrorBoundary scope="page" name="Agents">
        <Suspense fallback={null}>
          <Page />
        </Suspense>
      </ErrorBoundary>,
    );
    await screen.findByRole("alert");
    expect(reload).toHaveBeenCalledTimes(1);
  });
});

describe("reloadOnceForUpdate", () => {
  it("reloads again once the guard is old, because a new update is a new reason", () => {
    const reload = vi.spyOn(reloading, "now").mockImplementation(() => {});
    expect(reloadOnceForUpdate(1_000)).toBe(true);
    expect(reloadOnceForUpdate(2_000)).toBe(false);
    expect(reloadOnceForUpdate(1_000 + 10 * 60_000)).toBe(true);
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it("does not reload where the guard cannot be kept, because it could never stop", () => {
    const reload = vi.spyOn(reloading, "now").mockImplementation(() => {});
    const blocked = () => {
      throw new Error("blocked");
    };
    vi.stubGlobal("sessionStorage", { getItem: blocked, setItem: blocked, clear: () => {} });
    expect(reloadOnceForUpdate(1_000)).toBe(false);
    expect(reload).not.toHaveBeenCalled();
  });
});
