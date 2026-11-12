import { useCallback, useState } from "react";

// A preference of one browser: whether the conversation dock is open, whether single-key shortcuts are
// on, the records a person opened lately. Storage can throw or come back empty, so a failure leaves
// the default and nothing depends on it.
function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string | null): void {
  try {
    if (value === null) {
      localStorage.removeItem(key);
    } else {
      localStorage.setItem(key, value);
    }
  } catch {
    return;
  }
}

// useFlag is one on or off preference, remembered by this browser.
export function useFlag(key: string, initial: boolean): [boolean, (value: boolean) => void] {
  const [value, setValue] = useState(() => {
    const stored = read(key);
    return stored === null ? initial : stored === "on";
  });
  const set = useCallback(
    (next: boolean) => {
      setValue(next);
      write(key, next === initial ? null : next ? "on" : "off");
    },
    [key, initial],
  );
  return [value, set];
}

export type RecentRecord = { kind: string; id: string; title: string; project: string; to: string };

const recentKey = "ropeshift-recent";
const recentLimit = 6;

export function recentRecords(): RecentRecord[] {
  try {
    const parsed: unknown = JSON.parse(read(recentKey) ?? "[]");
    return Array.isArray(parsed) ? (parsed as RecentRecord[]).filter((row) => typeof row?.id === "string" && typeof row?.to === "string") : [];
  } catch {
    return [];
  }
}

// rememberRecord puts a record a person opened first among the recent ones.
export function rememberRecord(record: RecentRecord): void {
  const rest = recentRecords().filter((row) => !(row.id === record.id && row.project === record.project));
  write(recentKey, JSON.stringify([record, ...rest].slice(0, recentLimit)));
}
