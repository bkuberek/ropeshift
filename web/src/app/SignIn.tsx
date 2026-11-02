import { useState } from "react";

import { ApiError, signIn } from "../api/client";

const refusals: Record<string, string> = {
  bad_passphrase: "That passphrase does not match.",
  not_enrolled: "No passphrase is set yet. Run ropeshift passphrase set on a terminal.",
};

// The passphrase goes to the daemon and nowhere else. The session comes back in a cookie this page
// cannot read, so nothing here holds a secret past this form.
export function SignIn({ onSignedIn }: { onSignedIn: () => Promise<void> }) {
  const [passphrase, setPassphrase] = useState("");
  const [failure, setFailure] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <form
      className="sign-in"
      onSubmit={(event) => {
        event.preventDefault();
        setBusy(true);
        setFailure("");
        signIn(passphrase)
          .then(onSignedIn)
          .catch((error: unknown) => {
            const code = error instanceof ApiError ? error.code : "";
            setFailure(refusals[code] ?? (error instanceof Error ? error.message : String(error)));
          })
          .finally(() => {
            setPassphrase("");
            setBusy(false);
          });
      }}
    >
      <h1 className="title">Sign in</h1>
      <p className="lead">Agents run as you on this machine, so a person proves they are one before deciding anything.</p>
      <label className="field">
        <span className="field-label">Passphrase</span>
        <input
          type="password"
          autoComplete="current-password"
          autoFocus
          required
          value={passphrase}
          disabled={busy}
          onChange={(event) => setPassphrase(event.target.value)}
        />
      </label>
      {failure ? (
        <p className="refusal" role="alert">
          {failure}
        </p>
      ) : null}
      <div className="actions">
        <button className="button button-strong" type="submit" disabled={busy || passphrase === ""}>
          Sign in
        </button>
      </div>
    </form>
  );
}
