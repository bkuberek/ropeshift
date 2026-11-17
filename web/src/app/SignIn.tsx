import { useRef, useState } from "react";
import type { FormEvent } from "react";

import { ApiError, signIn } from "../api/client";
import { Button } from "../ui/controls";
import { Banner } from "../ui/display";
import { Field } from "../ui/forms";
import { Icon } from "../ui/Icon";
import { lockedOut, tooManyAttempts } from "./attempts";

// Where the passphrase comes from, in the guide that says how `ropeshift init` and `ropeshift passphrase set` ask
// for it. A test holds the address to a heading of that guide.
export const passphraseHelp = "https://github.com/bkuberek/ropeshift/blob/main/docs/onboarding.md#run-ropeshift-init";

// Every state the page can show, which the accessibility checks open one by one: a page nobody has asked anything
// of, one whose session ended, an empty try, a passphrase that does not match, an address a group server limits, a
// daemon that did not answer, and a daemon with no passphrase set.
export const signInStateIds = ["default", "ended", "empty", "wrong", "locked", "unreachable", "not-set"] as const;

export type SignInStateId = (typeof signInStateIds)[number];

type Outcome =
  | { kind: "wrong" | "empty" | "unreachable" | "not-set" }
  | { kind: "locked"; seconds: number | undefined }
  | { kind: "other"; message: string }
  | null;

// outcomeOf reads a refusal by its code, which is what the page decides on, and by the status for a limit.
function outcomeOf(error: unknown): Outcome {
  if (lockedOut(error)) {
    return { kind: "locked", seconds: error.retryAfter };
  }
  if (error instanceof ApiError) {
    if (error.code === "bad_passphrase") {
      return { kind: "wrong" };
    }
    if (error.code === "not_enrolled") {
      return { kind: "not-set" };
    }
    if (error.code === "unreachable") {
      return { kind: "unreachable" };
    }
  }
  return { kind: "other", message: error instanceof Error ? error.message : String(error) };
}

// SignIn asks for the passphrase, which goes to the daemon and nowhere else. The session comes back in a cookie this
// page cannot read, so nothing here holds a secret past this form. ended says the page had a session that is gone,
// which a sign-out in another tab or a change of the passphrase does, so the person is told why they are asked.
export function SignIn({ onSignedIn, ended = false }: { onSignedIn: () => Promise<void>; ended?: boolean }) {
  const [passphrase, setPassphrase] = useState("");
  const [outcome, setOutcome] = useState<Outcome>(null);
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const input = useRef<HTMLInputElement>(null);

  const send = (event: FormEvent) => {
    event.preventDefault();
    if (running.current) {
      return;
    }
    if (passphrase === "") {
      setOutcome({ kind: "empty" });
      input.current?.focus();
      return;
    }
    running.current = true;
    setBusy(true);
    setOutcome(null);
    signIn(passphrase)
      .then(onSignedIn)
      .catch((error: unknown) => {
        setOutcome(outcomeOf(error));
        input.current?.focus();
      })
      .finally(() => {
        running.current = false;
        setPassphrase("");
        setBusy(false);
      });
  };

  const fieldError =
    outcome?.kind === "wrong" ? "That passphrase does not match. Check it and try again." : outcome?.kind === "empty" ? "Enter your passphrase." : undefined;

  return (
    <div className="sign-in-page">
      <form className="sign-in" onSubmit={send} noValidate aria-labelledby="sign-in-title" aria-busy={busy || undefined}>
        <div className="sign-in-brand" aria-hidden="true">
          <span className="wordmark-mark">
            <Icon name="mark" />
          </span>
          <span>ropeshift</span>
        </div>
        <h1 id="sign-in-title" className="title">
          Sign in
        </h1>
        <p className="lead">Agents run as you on this machine, so a person proves they are one before deciding anything.</p>
        <div role="status">
          {outcome?.kind === "locked" ? <Banner tone="warning">{tooManyAttempts(outcome.seconds)}</Banner> : null}
          {ended && outcome?.kind !== "locked" ? <Banner icon="clock">Your session ended. Sign in again to carry on where you were.</Banner> : null}
        </div>
        {outcome?.kind === "unreachable" ? (
          <Banner tone="danger">The daemon did not answer, so nothing was checked. Check that it is running with ropeshift health, then try again.</Banner>
        ) : null}
        {outcome?.kind === "not-set" ? <Banner tone="danger">No passphrase is set yet. Run ropeshift passphrase set on a terminal, then sign in here.</Banner> : null}
        {outcome?.kind === "other" ? <Banner tone="danger">{outcome.message}</Banner> : null}
        <Field label="Passphrase" help="The one you set with ropeshift init or ropeshift passphrase set." error={fieldError}>
          <input
            ref={input}
            className="input"
            type="password"
            autoComplete="current-password"
            autoFocus
            value={passphrase}
            onChange={(event) => {
              setPassphrase(event.target.value);
              if (outcome?.kind === "wrong" || outcome?.kind === "empty") {
                setOutcome(null);
              }
            }}
          />
        </Field>
        <div className="actions">
          <Button type="submit" variant="primary" loading={busy}>
            Sign in
          </Button>
        </div>
        <p className="sign-in-help">
          <a href={passphraseHelp} target="_blank" rel="noreferrer noopener">
            Passphrase help<span className="visually-hidden"> (opens in a new tab)</span>
            <Icon name="external" size="sm" />
          </a>
        </p>
      </form>
    </div>
  );
}
