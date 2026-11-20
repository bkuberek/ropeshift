import { useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent } from "react";

import { maxPaneChords } from "../api/client";
import { capsOf, chordOf, isRelease, releaseKey } from "./chords";

// KeyCaps shows a key sequence as key caps: each chord's keys joined, and the chords apart.
export function KeyCaps({ sequence }: { sequence: string }) {
  return (
    <span className="pane-caps">
      {capsOf(sequence).map((chord, at) => (
        <span key={at} className="pane-chord">
          {chord.map((cap, place) => (
            <kbd key={place} className="kbd">
              {cap}
            </kbd>
          ))}
        </span>
      ))}
    </span>
  );
}

// KeyComposer sends any key combination or sequence. A member types it, such as Ctrl-X Ctrl-S, or records
// it by pressing the keys, sees it as key caps, and sends it. While it records, every key a member presses
// is added, Tab and Escape included, and the release key stops the recording.
export function KeyComposer({ live, onSend, onClose }: { live: boolean; onSend: (chords: string[]) => void; onClose: () => void }) {
  const field = useRef<HTMLInputElement>(null);
  const hint = useId();
  const [keys, setKeys] = useState("");
  const [recording, setRecording] = useState(false);
  const chords = keys.split(/\s+/).filter(Boolean);
  const tooMany = chords.length > maxPaneChords;
  const ready = live && chords.length > 0 && !tooMany;
  useEffect(() => field.current?.focus(), []);

  const send = () => {
    if (!ready) {
      return;
    }
    onSend(chords);
    setKeys("");
    setRecording(false);
    field.current?.focus();
  };
  const keyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (recording) {
      event.preventDefault();
      event.stopPropagation();
      if (isRelease(event)) {
        setRecording(false);
        return;
      }
      const chord = chordOf(event);
      if (chord) {
        setKeys((was) => [...was.split(/\s+/).filter(Boolean), chord].join(" "));
      }
      return;
    }
    const plain = !event.ctrlKey && !event.altKey && !event.metaKey && !event.shiftKey;
    if (plain && event.key === "Enter") {
      event.preventDefault();
      send();
    } else if (plain && event.key === "Escape") {
      event.preventDefault();
      onClose();
    }
  };

  return (
    <div className="pane-composer" role="group" aria-label="Compose keys">
      <label className="field">
        <span className="label">Keys to send</span>
        <input
          ref={field}
          className="input mono"
          type="text"
          value={keys}
          placeholder="Ctrl-X Ctrl-S"
          autoComplete="off"
          spellCheck={false}
          aria-describedby={hint}
          onChange={(event) => setKeys(event.target.value)}
          onKeyDown={keyDown}
          onBlur={() => setRecording(false)}
        />
      </label>
      <p className="muted small" id={hint}>
        {recording
          ? `Recording: each key you press is added, Tab and Escape included. ${releaseKey} stops the recording.`
          : "Type chords separated by spaces, such as Shift-Tab or Ctrl-X Ctrl-S, or record them. A chord is any of Ctrl, Alt, and Shift, then a key."}
      </p>
      {chords.length > 0 ? (
        <p className="pane-composed" aria-hidden="true">
          <KeyCaps sequence={keys} />
        </p>
      ) : null}
      {tooMany ? (
        <p className="msg msg-error refusal" role="alert">
          A sequence holds at most {maxPaneChords} chords.
        </p>
      ) : null}
      <div className="row-tight">
        <button
          className="btn btn-sm"
          type="button"
          aria-pressed={recording}
          disabled={!live}
          // A press with the pointer leaves the keyboard in the field, which records.
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => {
            setRecording((was) => !was);
            field.current?.focus();
          }}
        >
          Record
        </button>
        <button className="btn btn-sm btn-primary" type="button" disabled={!ready} onMouseDown={(event) => event.preventDefault()} onClick={send}>
          Send keys
        </button>
        <button className="btn btn-sm btn-ghost" type="button" onClick={onClose}>
          Close
        </button>
      </div>
    </div>
  );
}
