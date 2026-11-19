import type { StateLook } from "../floor/states";
import type { PracticeFinding, PracticeRow, PracticesAcknowledgement, PracticesRequest, PracticesView, Severity } from "../api/practices";

// What a practices result reads as, in the words every view of it shares.

export const severities: Severity[] = ["high", "medium", "low"];

// The most bytes of the reason a member gives, which the server holds a reason to.
export const maxReasonBytes = 2048;

// pollMs is how often a request that is open is read, until it is reported or expired.
export const pollMs = 1000;

export const expiredWords = "The check did not finish within 30 minutes. A later result still appears here.";

export type Counts = { high: number; medium: number; low: number; open: number; acknowledged: number; unknown: number };

// orphansOf are a repository's acknowledgements of findings the latest result does not list: a check may not have run since
// the finding appeared, or the finding has since been met. They stay held until a member withdraws them.
export function orphansOf(view: PracticesView): PracticesAcknowledgement[] {
  const listed = new Set((view.result?.findings ?? []).map((finding) => finding.id));
  return (view.acknowledgement ?? []).filter((held) => !listed.has(held.finding));
}

// countsOf counts a repository's open findings by severity, its acknowledged findings, and its practices the check could
// not read. An acknowledged finding is no longer open, and stays in the list.
export function countsOf(view: PracticesView): Counts {
  const counts: Counts = { high: 0, medium: 0, low: 0, open: 0, acknowledged: orphansOf(view).length, unknown: 0 };
  for (const finding of view.result?.findings ?? []) {
    if (finding.acknowledged) {
      counts.acknowledged += 1;
    } else {
      counts[finding.severity] += 1;
      counts.open += 1;
    }
  }
  counts.unknown = (view.result?.practices ?? []).filter((row) => row.status === "unknown").length;
  return counts;
}

export const openFindings = (view: PracticesView): PracticeFinding[] => (view.result?.findings ?? []).filter((finding) => !finding.acknowledged);

// acknowledgedFindings are the findings of the result a member acknowledged, and an acknowledgement of a finding the result
// does not list, which has only its ID and the acknowledgement to show.
export const acknowledgedFindings = (view: PracticesView): PracticeFinding[] => [
  ...(view.result?.findings ?? []).filter((finding) => finding.acknowledged),
  ...orphansOf(view).map((held): PracticeFinding => ({ id: held.finding, practice: held.finding.split(":")[0] ?? held.finding, severity: "low", detail: "", why: "", fix: "", acknowledged: true, acknowledgement: held })),
];

export const severityLook: Record<Severity, StateLook> = {
  high: { label: "high", tone: "danger", glyph: "cross" },
  medium: { label: "medium", tone: "warning", glyph: "triangle" },
  low: { label: "low", tone: "neutral", glyph: "dot" },
};

// A practice with findings reads as the most serious of the open ones, and as Acknowledged when every one is
// acknowledged: its severity is then muted, and said beside the word.
export type PracticeStanding = { look: StateLook; muted?: Severity };

export function standingOf(row: PracticeRow, findings: PracticeFinding[]): PracticeStanding {
  switch (row.status) {
    case "met":
      return { look: { label: "Met", tone: "success", glyph: "check" } };
    case "unknown":
      return { look: { label: "Unknown", tone: "neutral", glyph: "question" } };
    case "not_applicable":
      return { look: { label: "Not applicable", tone: "neutral", glyph: "slash" } };
    case "finding": {
      const own = findings.filter((finding) => finding.practice === row.id);
      const open = own.filter((finding) => !finding.acknowledged);
      if (own.length > 0 && open.length === 0) {
        return { look: { label: "Acknowledged", tone: "neutral", glyph: "check" }, muted: row.severity };
      }
      const worst = severities.find((severity) => open.some((finding) => finding.severity === severity)) ?? row.severity;
      return { look: severityLook[worst] };
    }
  }
}

// checking says a request is being carried out: the list says it is open, and no read of it has said otherwise.
export const checking = (request: PracticesRequest | undefined, ended: Record<string, string>): request is PracticesRequest => request?.state === "offered" && !(request.id in ended);

// shortCommit is the start of a commit's ID.
export const shortCommit = (commit: string) => commit.slice(0, 7);

// reasonProblem says what is wrong with a reason before it is sent, or nothing.
export function reasonProblem(reason: string): string {
  if (reason.trim() === "") {
    return "Say why this finding stays open.";
  }
  if (new TextEncoder().encode(reason).length > maxReasonBytes) {
    return "The reason is over 2 KiB. Shorten it.";
  }
  return "";
}
