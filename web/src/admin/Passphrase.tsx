import { useRef, useState } from "react";
import type { FormEvent, RefObject } from "react";

import { ApiError, changePassphrase } from "../api/client";
import { Button } from "../ui/controls";
import { Banner, Panel, Refusal } from "../ui/display";
import { Field } from "../ui/forms";

// The shortest passphrase the daemon takes, counted in bytes as it counts them (`ropeshift passphrase set`).
export const minPassphraseBytes = 8;

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

// PassphraseForm changes the passphrase through the route `ropeshift passphrase set` calls. The current passphrase
// proves the person at the page knows it, so a session left open on a screen cannot change it. No passphrase is kept
// past the request: the fields empty when it is sent, and nothing here writes one to the address or to storage.
export function PassphraseForm() {
  const [current, setCurrent] = useState("");
  const [fresh, setFresh] = useState("");
  const [again, setAgain] = useState("");
  const [problems, setProblems] = useState<Problems>({});
  const [failure, setFailure] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const currentField = useRef<HTMLInputElement>(null);
  const freshField = useRef<HTMLInputElement>(null);
  const againField = useRef<HTMLInputElement>(null);

  // edit changes one field, and takes back what this page said of it, since it no longer describes what is typed.
  const edit = (set: (value: string) => void, field: keyof Problems) => (event: { target: { value: string } }) => {
    set(event.target.value);
    setNotice("");
    setProblems((was) => ({ ...was, [field]: undefined }));
  };

  const focus = (field: RefObject<HTMLInputElement | null>) => field.current?.focus();

  const refused = (error: unknown) => {
    const code = error instanceof ApiError ? error.code : "";
    if (code === "bad_passphrase") {
      setCurrent("");
      setProblems({ current: "That is not your current passphrase. Check it and try again." });
      focus(currentField);
    } else if (code === "invalid" && error instanceof ApiError) {
      setProblems({ fresh: error.message });
      focus(freshField);
    } else if (code === "not_enrolled") {
      setFailure("No passphrase is set yet. Run ropeshift passphrase set on a terminal.");
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
    setNotice("");
    setFailure("");
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
      setNotice(noticeOf(result.sessions_ended));
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
            help={`At least ${minPassphraseBytes} bytes. A phrase of several words is easy to remember and hard to guess.`}
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
      <div role="status">{notice ? <Banner icon="check">{notice}</Banner> : null}</div>
    </Panel>
  );
}
