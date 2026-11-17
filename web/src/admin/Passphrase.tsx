import { useRef, useState } from "react";
import type { FormEvent, RefObject } from "react";

import { ApiError, changePassphrase } from "../api/client";
import { lockedOut, tooManyAttempts } from "../app/attempts";
import { Link } from "../app/route";
import { Button } from "../ui/controls";
import { Banner, Panel, Refusal } from "../ui/display";
import { Field } from "../ui/forms";

// The shortest and the longest passphrase the daemon takes, counted in bytes as it counts them. The longest is what a
// terminal can take a line of, and `ropeshift passphrase set` and a sign-in at a terminal read the passphrase there.
export const minPassphraseBytes = 8;
export const maxPassphraseBytes = 1024;

const bytesOf = (text: string) => new TextEncoder().encode(text).length;

type Problems = { current?: string; fresh?: string; again?: string };

// problemsOf names, field by field, what would stop the change before anything is sent. The daemon says the rest.
export function problemsOf(current: string, fresh: string, again: string): Problems {
  const found: Problems = {};
  if (current === "") {
    found.current = "Enter your current passphrase.";
  }
  const size = bytesOf(fresh);
  if (size === 0) {
    found.fresh = `Enter a new passphrase of at least ${minPassphraseBytes} bytes.`;
  } else if (size < minPassphraseBytes) {
    found.fresh = `Use at least ${minPassphraseBytes} bytes. This one has ${size}.`;
  } else if (size > maxPassphraseBytes) {
    found.fresh = `Use at most ${maxPassphraseBytes} bytes. This one has ${size}.`;
  }
  if (again !== fresh) {
    found.again = "The two new passphrases differ. Type the new passphrase again exactly.";
  }
  return found;
}

// noticeOf says what the change did to the member's other sessions, which the daemon counted.
export function noticeOf(ended: number): string {
  const others =
    ended === 0 ? "No other session was open." : `${ended === 1 ? "1 other session ended" : `${ended} other sessions ended`}; sign in again with the new passphrase there.`;
  return `Passphrase changed. ${others} This browser stays signed in.`;
}

// delegatesOf says how many delegate credentials still act, which a change leaves alone.
export function delegatesOf(count: number): string {
  return count === 1 ? "1 delegate credential of yours still acts." : `${count} delegate credentials of yours still act.`;
}

// PassphraseForm changes the passphrase through the route `ropeshift passphrase set` calls. The current passphrase
// proves the person at the page knows it, so a session left open on a screen cannot change it. No passphrase is kept
// past the request: the fields empty when it is sent, and nothing here writes one to the address or to storage.
export function PassphraseForm() {
  const [current, setCurrent] = useState("");
  const [fresh, setFresh] = useState("");
  const [again, setAgain] = useState("");
  const [problems, setProblems] = useState<Problems>({});
  const [failure, setFailure] = useState("");
  const [notice, setNotice] = useState<{ text: string; delegates: number } | null>(null);
  // locked holds the wait a group server's limit on wrong guesses gave, which the Sign in page shows too.
  const [locked, setLocked] = useState<{ seconds: number | undefined } | null>(null);
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const currentField = useRef<HTMLInputElement>(null);
  const freshField = useRef<HTMLInputElement>(null);
  const againField = useRef<HTMLInputElement>(null);

  // edit changes one field, and takes back what this page said of it, since it no longer describes what is typed.
  const edit = (set: (value: string) => void, field: keyof Problems) => (event: { target: { value: string } }) => {
    set(event.target.value);
    setNotice(null);
    setProblems((was) => ({ ...was, [field]: undefined }));
  };

  const focus = (field: RefObject<HTMLInputElement | null>) => field.current?.focus();

  const refused = (error: unknown) => {
    const code = error instanceof ApiError ? error.code : "";
    if (lockedOut(error)) {
      // Nothing was checked, so no field is marked wrong. The current passphrase is not kept for the wait.
      setCurrent("");
      setLocked({ seconds: error.retryAfter });
      focus(currentField);
    } else if (code === "bad_passphrase") {
      setCurrent("");
      setProblems({ current: "That is not your current passphrase. Check it and try again." });
      focus(currentField);
    } else if (code === "invalid" && error instanceof ApiError) {
      setProblems({ fresh: error.message });
      focus(freshField);
    } else if (code === "not_enrolled") {
      setFailure("No passphrase is set yet. Run ropeshift passphrase set on a terminal.");
    } else if (code === "passphrase_changed") {
      // The current passphrase was right when it was checked, so it is not marked wrong: another change got in first.
      setFailure("The passphrase changed while you were changing it. Sign in again.");
    } else if (code === "session_required") {
      setFailure("Your session ended. Sign in again to change the passphrase.");
    } else if (code === "unreachable") {
      setFailure("The daemon did not answer, so nothing changed. Check that it is running, then try again.");
    } else {
      setFailure(error instanceof Error ? error.message : String(error));
    }
  };

  const send = async (event: FormEvent) => {
    event.preventDefault();
    if (running.current) {
      return;
    }
    setNotice(null);
    setFailure("");
    setLocked(null);
    const found = problemsOf(current, fresh, again);
    setProblems(found);
    const first = found.current ? currentField : found.fresh ? freshField : found.again ? againField : null;
    if (first) {
      focus(first);
      return;
    }
    running.current = true;
    setBusy(true);
    try {
      const result = await changePassphrase(current, fresh);
      setCurrent("");
      setFresh("");
      setAgain("");
      setNotice({ text: noticeOf(result.sessions_ended), delegates: result.delegates ?? 0 });
    } catch (error) {
      refused(error);
    } finally {
      running.current = false;
      setBusy(false);
    }
  };

  return (
    <Panel>
      <form onSubmit={(event) => void send(event)} noValidate>
        <p className="small">Changing the passphrase ends your other sessions, in every browser and terminal. This browser stays signed in, and a delegate's credential is not touched.</p>
        <div className="field-stack">
          <Field label="Current passphrase" error={problems.current}>
            <input ref={currentField} className="input field-narrow" type="password" autoComplete="current-password" value={current} onChange={edit(setCurrent, "current")} />
          </Field>
          <Field
            label="New passphrase"
            help={`At least ${minPassphraseBytes} bytes and at most ${maxPassphraseBytes}: a terminal reads a passphrase a line at a time, so a longer one could not be typed there. A phrase of several words is easy to remember and hard to guess.`}
            error={problems.fresh}
          >
            <input ref={freshField} className="input field-narrow" type="password" autoComplete="new-password" value={fresh} onChange={edit(setFresh, "fresh")} />
          </Field>
          <Field label="New passphrase, again" error={problems.again}>
            <input ref={againField} className="input field-narrow" type="password" autoComplete="new-password" value={again} onChange={edit(setAgain, "again")} />
          </Field>
        </div>
        <div className="row-tight form-actions">
          <Button type="submit" variant="primary" loading={busy}>
            Change passphrase
          </Button>
        </div>
        {failure ? <Refusal>{failure}</Refusal> : null}
      </form>
      <div role="status">
        {locked ? <Banner tone="warning">{tooManyAttempts(locked.seconds)}</Banner> : null}
        {notice ? (
          <Banner icon="check">
            <p>{notice.text}</p>
            {notice.delegates > 0 ? (
              <p>
                {delegatesOf(notice.delegates)} A change leaves them alone.{" "}
                <Link to="/members?dialog=sign-out-all" installation>
                  Sign out all sessions
                </Link>{" "}
                ends them too.
              </p>
            ) : null}
          </Banner>
        ) : null}
      </div>
    </Panel>
  );
}
