import { Component } from "react";
import type { ErrorInfo, ReactNode } from "react";

import { Button } from "../ui/controls";
import { Banner } from "../ui/display";

// reloading is the one place the page reloads itself, so a test can stand in for it.
export const reloading = { now: () => window.location.reload() };

// A dynamic import that cannot load a file says so in the words of its browser: Chrome and Edge, Firefox, Safari, and the
// bundler's own name for it.
const staleChunk = /dynamically imported module|importing a module script failed|loading chunk .* failed|unable to preload/i;

// isStaleChunk says a failure is a lazy chunk that could not load. After the daemon was updated, the open page still
// asks for file names the new build no longer holds.
export function isStaleChunk(error: unknown): boolean {
  if (!(error instanceof Error)) {
    return false;
  }
  return error.name === "ChunkLoadError" || staleChunk.test(error.message);
}

const guardKey = "ropeshift:reloaded-for-update";
// A second reload within this window is a loop, because a build that holds the file would have served it the first time.
const guardMs = 60_000;

// reloadOnceForUpdate reloads the page once for an update, and says whether it did. A reload that was just made stays
// made, and where the browser keeps no session storage the page cannot know it already reloaded, so it never reloads by itself.
export function reloadOnceForUpdate(now = Date.now()): boolean {
  try {
    const last = Number(sessionStorage.getItem(guardKey) ?? "0");
    if (last > 0 && now - last < guardMs) {
      return false;
    }
    sessionStorage.setItem(guardKey, String(now));
  } catch {
    return false;
  }
  reloading.now();
  return true;
}

type Props = {
  // scope says how much of the application the boundary holds: the whole of it, or the page in the shell.
  scope: "app" | "page";
  // name is what the person was looking at, in a person's words.
  name?: string;
  // reset starts the boundary over when it changes, so moving to another page does not keep a failure of the last.
  reset?: string;
  children: ReactNode;
};

type State = { error: Error | null; reset?: string };

// ErrorBoundary shows what failed and offers Reload, where a render that threw would leave an empty screen. It does not
// depend on any provider, so it works for a failure of the providers themselves.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, reset: this.props.reset };

  static getDerivedStateFromError(error: unknown): Partial<State> {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  static getDerivedStateFromProps(props: Props, state: State): Partial<State> | null {
    return props.reset !== state.reset ? { error: null, reset: props.reset } : null;
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("ropeshift could not show a page", error, info.componentStack);
    if (isStaleChunk(error)) {
      reloadOnceForUpdate();
    }
  }

  render() {
    const { error } = this.state;
    if (!error) {
      return this.props.children;
    }
    const { scope, name } = this.props;
    const stale = isStaleChunk(error);
    const what = name ?? "This page";
    const body = (
      <div className="screen" data-testid="error-boundary">
        <Banner
          tone="danger"
          action={
            <span className="row-tight">
              <Button small icon="restart" onClick={() => reloading.now()}>
                Reload
              </Button>
              {scope === "page" && !stale ? (
                <Button small onClick={() => this.setState({ error: null })}>
                  Try again
                </Button>
              ) : null}
            </span>
          }
        >
          {stale ? (
            <>
              <p>
                <strong>ropeshift was updated.</strong>
              </p>
              <p>A newer version is running, and this page still holds the old one. Reload to load it. What you wrote here and did not send is not kept.</p>
            </>
          ) : (
            <>
              <p>
                <strong>{what} could not be shown.</strong>
              </p>
              <p>{error.message || "The page failed with no message."}</p>
              <p>Reload the page. If it happens again, the details are in the browser console.</p>
            </>
          )}
        </Banner>
      </div>
    );
    return scope === "app" ? <main className="main">{body}</main> : body;
  }
}
