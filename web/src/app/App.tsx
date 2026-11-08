import { useRef } from "react";

import { signOut } from "../api/client";
import { useNeedsYou } from "../api/useNeedsYou";
import { NeedsYou } from "../needs/NeedsYou";
import { Nav } from "./Nav";
import { PageFor, useDocumentTitle } from "./pages";
import { ArrivalsProvider, useRoute } from "./route";
import { SignIn } from "./SignIn";
import { useTheme } from "./theme";

const connectionWords = { connecting: "Connecting", live: "Live", reconnecting: "Reconnecting" };
const themeWords = { system: "Theme: system", light: "Theme: light", dark: "Theme: dark" };

export function App() {
  const { view, readAt, failure, needsSession, connection, heard, reload, checkSession, forget } = useNeedsYou();
  const { theme, nextTheme } = useTheme();
  const route = useRoute();
  const opened = useRef(route.moves);
  const signedIn = view !== null && !needsSession;
  const atNeeds = route.path === "/";
  useDocumentTitle(route.path);
  return (
    <ArrivalsProvider value={route.moves - opened.current}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="masthead">
        <p className="wordmark">ropeshift</p>
        <div className="masthead-tools">
          {signedIn ? (
            <p className={`status status-${connection}`} role="status">
              {connectionWords[connection]}
            </p>
          ) : null}
          <button className="link-button" type="button" onClick={nextTheme}>
            {themeWords[theme]}
          </button>
          {signedIn ? (
            <button className="link-button" type="button" onClick={() => void signOut().then(forget, reload)}>
              Sign out
            </button>
          ) : null}
        </div>
      </header>
      <div className="frame">
        {signedIn ? <Nav path={route.path} waiting={view.items.length} /> : null}
        <main id="main" className="page" tabIndex={-1}>
          {failure ? (
            <p className="refusal" role="alert">
              {failure}
            </p>
          ) : null}
          {needsSession ? <SignIn onSignedIn={reload} /> : null}
          {/* The page stays mounted behind a sign-in, and the queue behind any other view, so answers in progress survive both. */}
          <div hidden={needsSession}>
            {view ? (
              <>
                <div hidden={!atNeeds}>
                  <NeedsYou view={view} readAt={readAt} after={reload} active={atNeeds} />
                </div>
                {atNeeds ? null : <PageFor key={route.path} route={route} live={{ heard, checkSession }} after={reload} />}
              </>
            ) : null}
          </div>
        </main>
      </div>
    </ArrivalsProvider>
  );
}
