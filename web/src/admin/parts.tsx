import { cloneElement, isValidElement, useCallback, useEffect, useId, useMemo, useReducer, useRef, useState } from "react";
import type { ReactElement, ReactNode } from "react";

import type { Download } from "../api/client";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { Button } from "../ui/controls";
import { Banner, SkeletonRows } from "../ui/display";
import { Icon } from "../ui/Icon";

// useRead follows one read, and reads again after a change this page made, as well as when the stream
// says the ledger moved.
export function useRead<T>(read: () => Promise<T>, key: string, live: Live) {
  const [version, again] = useReducer((count: number) => count + 1, 0);
  const state = useView<T>(read, `${key}#${version}`, live);
  return { ...state, again };
}

// Reading shows a read's failure in the API's words, and skeleton rows until its first answer. A page
// that has data keeps it under a banner that says it is not current.
export function Reading({ failure, ready, rows = 4, children }: { failure: string; ready: boolean; rows?: number; children: ReactNode }) {
  return (
    <>
      {failure ? <Banner tone="danger">{ready ? `What this page shows is not current. ${failure}` : failure}</Banner> : null}
      {ready ? children : null}
      {!ready && !failure ? (
        <div className="panel">
          <SkeletonRows rows={rows} />
        </div>
      ) : null}
    </>
  );
}

// useChange runs one change at a time and keeps what the server refused, for the control that asked.
export function useChange() {
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  const running = useRef(false);
  const run = useCallback(async <T,>(change: () => Promise<T>): Promise<T | undefined> => {
    if (running.current) {
      return undefined;
    }
    running.current = true;
    setBusy(true);
    setFailure("");
    try {
      return await change();
    } catch (error) {
      setFailure(error instanceof Error ? error.message : String(error));
      return undefined;
    } finally {
      running.current = false;
      setBusy(false);
    }
  }, []);
  return { run, busy, failure, setFailure };
}

// saveDownload hands a file the server answered to the browser, which saves it as a download.
export function saveDownload({ blob, name }: Download): void {
  const address = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = address;
  link.download = name;
  link.hidden = true;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(address), 1000);
}

// base64Of reads a file a person chose as base64, which the API reads from JSON.
export function base64Of(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).replace(/^data:[^,]*,/, ""));
    reader.onerror = () => reject(reader.error ?? new Error("the file could not be read"));
    reader.readAsDataURL(file);
  });
}

// SaveBar keeps a long form's unsaved changes in view, with Discard and Save, at the bottom of its panel.
export function SaveBar({ summary, onDiscard, onSave, saving = false, saveLabel = "Save", disabled = false }: {
  summary: string;
  onDiscard: () => void;
  onSave: () => void;
  saving?: boolean;
  saveLabel?: string;
  disabled?: boolean;
}) {
  const changed = summary !== "";
  return (
    <div className="save-bar">
      <span className="small muted" aria-live="polite">
        {changed ? summary : "No unsaved changes"}
      </span>
      <span className="spacer" />
      <Button small disabled={!changed || saving} onClick={onDiscard}>
        Discard
      </Button>
      <Button small variant="primary" loading={saving} disabled={!changed || saving || disabled} onClick={onSave}>
        {saveLabel}
      </Button>
    </div>
  );
}

// useLeaveGuard asks before a page with unsaved changes closes.
export function useLeaveGuard(dirty: boolean): void {
  useEffect(() => {
    if (!dirty) {
      return;
    }
    const ask = (event: BeforeUnloadEvent) => {
      event.preventDefault();
    };
    window.addEventListener("beforeunload", ask);
    return () => window.removeEventListener("beforeunload", ask);
  }, [dirty]);
}

// sourceWords names where a field's value comes from, as the roster and the settings name it.
export const sourceWords: Record<string, string> = { builtin: "Built-in", installation: "Installation", project: "Project", "": "Unset" };

export function dateOf(at: number): string {
  return at ? new Date(at).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) : "";
}

export function timeOf(at: number): string {
  return at ? new Date(at).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "";
}

// useDraft is a form's draft of a saved record. A read that brings the same record again leaves the
// draft alone, so a live update never undoes what a person is typing; a record that changed resets it.
export function useDraft<T>(saved: T): [T, (next: T | ((was: T) => T)) => void, T] {
  const content = JSON.stringify(saved);
  // The saved value is kept by its content, so a new object with the same content changes nothing.
  const stable = useMemo(() => saved, [content]);
  const [draft, setDraft] = useState<T>(stable);
  useEffect(() => setDraft(stable), [stable]);
  return [draft, setDraft, stable];
}

// GridField is a field in a form grid: its label in the left column, and the control with its help and
// message in the right, as a long record form lays them out.
export function GridField({ label, help, warning, error, children }: {
  label: ReactNode;
  help?: ReactNode;
  warning?: ReactNode;
  error?: ReactNode;
  children: ReactElement<{ id?: string; "aria-describedby"?: string; "aria-invalid"?: boolean }>;
}) {
  const id = useId();
  const described = [help ? `${id}-help` : "", error || warning ? `${id}-message` : ""].filter(Boolean).join(" ") || undefined;
  const control = isValidElement(children) ? cloneElement(children, { id, "aria-describedby": described, "aria-invalid": error ? true : undefined }) : children;
  return (
    <>
      <label className="label" htmlFor={id}>
        {label}
      </label>
      <div className={`field${error ? " has-error" : warning ? " has-warning" : ""}`}>
        {control}
        {help ? (
          <p className="help" id={`${id}-help`}>
            {help}
          </p>
        ) : null}
        {error || warning ? (
          <p className={`msg ${error ? "msg-error" : "msg-warning"}`} id={`${id}-message`}>
            <Icon name="warning" size="sm" />
            <span>{error ?? warning}</span>
          </p>
        ) : null}
      </div>
    </>
  );
}
