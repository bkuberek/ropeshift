import type { Difference, PlanVersionLine } from "../api/types";

// A reference names a record as `<kind>:<id>`, the ID being the one inside the record's file.
export const refKind = (ref: string) => (ref.includes(":") ? ref.slice(0, ref.indexOf(":")) : "");
export const refName = (ref: string) => (ref.includes(":") ? ref.slice(ref.indexOf(":") + 1) : ref);

export type Change = "added" | "changed" | "removed";

export type ChangeLine = { change: Change; ref: string; kind: string; name: string; inside: string[] };

const partWords: [string, "criteria" | "work_orders"][] = [
  ["Criteria", "criteria"],
  ["Work orders", "work_orders"],
];

// changeLines lists a version's difference from the one a member approved last: each record added, changed,
// or removed, in that order, and for a contract that changed, which criteria and work orders did.
export function changeLines(difference: Difference): ChangeLine[] {
  const lines: ChangeLine[] = [];
  for (const [change, refs] of [
    ["added", difference.added],
    ["changed", difference.changed],
    ["removed", difference.removed],
  ] as const) {
    for (const ref of refs ?? []) {
      const inside: string[] = [];
      const contract = difference.contracts?.find((row) => row.ref === ref);
      for (const [name, part] of partWords) {
        for (const verb of ["added", "changed", "removed"] as const) {
          const ids = contract?.[part][verb];
          if (ids?.length) {
            inside.push(`${name} ${verb}: ${ids.join(", ")}`);
          }
        }
      }
      lines.push({ change, ref, kind: refKind(ref), name: refName(ref), inside });
    }
  }
  return lines;
}

export type VersionLinks = { newer?: number; approved?: number; waiting?: number };

// versionLinks says where a version stands among its plan's: the newest version that replaced it, the one a
// member approved last, and the one that waits for a member's word. A version the plan never had has none.
export function versionLinks(versions: PlanVersionLine[], version: number): VersionLinks | undefined {
  if (!versions.some((line) => line.version === version)) {
    return undefined;
  }
  const newest = Math.max(...versions.map((line) => line.version));
  return {
    newer: newest > version ? newest : undefined,
    approved: versions.find((line) => line.state === "approved")?.version,
    waiting: versions.find((line) => line.state === "pending")?.version,
  };
}
