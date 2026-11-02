import { signOut } from "../api/client";
import { useNeedsYou } from "../api/useNeedsYou";
import { NeedsYou } from "../needs/NeedsYou";
import { SignIn } from "./SignIn";
import { useTheme } from "./theme";

const connectionWords = { connecting: "Connecting", live: "Live", reconnecting: "Reconnecting" };
const themeWords = { system: "Theme: system", light: "Theme: light", dark: "Theme: dark" };

export function App() {
  const { view, readAt, failure, needsSession, connection, reload, forget } = useNeedsYou();
  const { theme, nextTheme } = useTheme();
  return (
    <>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="masthead">
        <p className="wordmark">ropeshift</p>
        <div className="masthead-tools">
          {view && !needsSession ? (
            <p className={`status status-${connection}`} role="status">
              {connectionWords[connection]}
            </p>
          ) : null}
          <button className="link-button" type="button" onClick={nextTheme}>
            {themeWords[theme]}
          </button>
          {view && !needsSession ? (
            <button className="link-button" type="button" onClick={() => void signOut().then(forget, reload)}>
              Sign out
            </button>
          ) : null}
        </div>
      </header>
      <main id="main" className="page" tabIndex={-1}>
        {failure ? (
          <p className="refusal" role="alert">
            {failure}
          </p>
        ) : null}
        {needsSession ? <SignIn onSignedIn={reload} /> : null}
        {/* The queue stays mounted behind a sign-in, so answers in progress survive a session's end. */}
        <div hidden={needsSession}>{view ? <NeedsYou view={view} readAt={readAt} after={reload} /> : null}</div>
      </main>
    </>
  );
}
