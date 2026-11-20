import { Component, useEffect, useRef } from "react";
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

// unsentDraft says a person has written something on the page that no one has sent: a text box or area that holds more
// than white space. Needs you stays mounted behind every view, so a half-written answer is on the page whichever view
// shows. A reload would lose it.
export function unsentDraft(): boolean {
  const fields = document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("textarea, input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=password]):not([type=file])");
  return Array.from(fields).some((field) => field.value.trim() !== "");
}

// reloadOnceForUpdate reloads the page once for an update, and says whether it did. It leaves the page alone while a draft
// is unsent, because the person can still send it and then reload. A reload that was just made stays made, and where the
// browser keeps no session storage the page cannot know it already reloaded, so it never reloads by itself.
export function reloadOnceForUpdate(now = Date.now()): boolean {
  if (unsentDraft()) {
    return false;
  }
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

// kept says the page did not reload by itself because a draft is unsent.
type State = { error: Error | null; reset?: string; kept?: boolean };

// ErrorBoundary shows what failed and offers Reload, where a render that threw would leave an empty screen. It does not
// depend on any provider, so it works for a failure of the providers themselves.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, reset: this.props.reset };

  static getDerivedStateFromError(error: unknown): Partial<State> {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  static getDerivedStateFromProps(props: Props, state: State): Partial<State> | null {
    return props.reset !== state.reset ? { error: null, reset: props.reset, kept: false } : null;
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("ropeshift could not show a page", error, info.componentStack);
    if (isStaleChunk(error) && !reloadOnceForUpdate() && unsentDraft()) {
      this.setState({ kept: true });
    }
  }

  render() {
    const { error, kept } = this.state;
    if (!error) {
      return this.props.children;
    }
    const { scope, name } = this.props;
    const body = (
      <Failure
        error={error}
        name={name ?? "This page"}
        kept={kept === true}
        retry={scope === "page" ? () => this.setState({ error: null, kept: false }) : undefined}
      />
    );
    return scope === "app" ? (
      <main id="main" className="main" tabIndex={-1}>
        {body}
      </main>
    ) : (
      body
    );
  }
}

// Failure says what failed, under a heading that takes focus, so a keyboard or a screen reader lands on it.
function Failure({ error, name, kept, retry }: { error: Error; name: string; kept: boolean; retry?: () => void }) {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => heading.current?.focus(), []);
  const stale = isStaleChunk(error);
  return (
    <div className="screen" data-testid="error-boundary">
      <Banner
        tone="danger"
        action={
          <span className="row-tight">
            <Button small icon="restart" onClick={() => reloading.now()}>
              Reload
            </Button>
            {retry && !stale ? (
              <Button small onClick={retry}>
                Try again
              </Button>
            ) : null}
          </span>
        }
      >
        <h1 ref={heading} tabIndex={-1}>
          {stale ? "ropeshift was updated" : `${name} could not be shown`}
        </h1>
        {stale ? (
          <>
            <p>A newer version is running, and this page still holds the old one. Reload to load it.</p>
            <p>
              {kept
                ? "The page did not reload by itself, because something you wrote is not sent yet. Send it, then reload."
                : "What you wrote here and did not send is not kept."}
            </p>
          </>
        ) : (
          <>
            <p>{error.message || "The page failed with no message."}</p>
            <p>Reload the page. If it happens again, the details are in the browser console.</p>
          </>
        )}
      </Banner>
    </div>
  );
}
