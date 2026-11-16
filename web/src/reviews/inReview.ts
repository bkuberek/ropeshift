import type { Agreement, Approval, NeedItem, PullRequest, Standing } from "../api/types";
import type { WorkRow } from "../floor/workIndex";
import { groupOf } from "../floor/workIndex";
import { isMerge } from "../needs/merge";

// What Reviews says of work in review is what the read models say: the work index holds where each work order
// stands and how its review went, and Needs you holds what a member can do about it. Nothing here decides which
// act a state allows.

// QueueRow is one work order that waits for acceptance or a merge. accept and rework are the acts its request in
// Needs you lists, and merge is the merge request that waits for the member's decision.
export type QueueRow = {
  id: string;
  title: string;
  state: string;
  since: number;
  standing: Standing;
  row: WorkRow;
  accept: boolean;
  rework: boolean;
  merge?: Approval;
};

const standingOf = (row: WorkRow): Standing | undefined => row.completed?.standing ?? row.stuck?.standing;

// byWait puts the longest wait first, and work with no known time last.
const byWait = (a: { since: number }, b: { since: number }) => (a.since || Number.MAX_SAFE_INTEGER) - (b.since || Number.MAX_SAFE_INTEGER);

// queueOf lists the work that waits for acceptance or for a merge, the longest wait first. A request counts for a
// work order when it is of this project: one the server labeled with no project is of the only project there is.
export function queueOf(rows: WorkRow[], needs: NeedItem[], project = ""): QueueRow[] {
  const mine = needs.filter((item) => !project || !item.project || item.project.name === project);
  return rows
    .filter((row) => groupOf(row) === "review" && row.completed)
    .map((row): QueueRow => {
      const id = row.order.id;
      const work = mine.find((item) => item.kind === "work" && item.work.work_order?.id === id);
      const actions = work?.kind === "work" ? work.work.actions : [];
      const merge = mine.find((item) => item.kind === "approval" && isMerge(item.approval) && item.approval.work_order_id === id && item.approval.state === "pending");
      return {
        id,
        title: row.order.title || id,
        state: row.order.state,
        since: row.completed?.since ?? 0,
        standing: row.completed?.standing as Standing,
        row,
        accept: actions.includes("accept"),
        rework: actions.includes("rework"),
        merge: merge?.kind === "approval" ? merge.approval : undefined,
      };
    })
    .sort(byWait);
}

const count = (amount: number, word: string) => `${amount} ${word}`;

// verdictsOf counts the verdicts that speak for the work's current attempt, by kind.
export function verdictsOf(standing: Standing): string {
  const verdicts = standing.verdicts ?? [];
  if (verdicts.length === 0) {
    return "No verdict";
  }
  const parts: [string, string][] = [
    ["pass", "pass"],
    ["fail", "fail"],
    ["cannot_judge", "cannot judge"],
  ];
  return parts
    .map(([kind, word]) => count(verdicts.filter((verdict) => verdict.verdict === kind).length, word))
    .filter((said) => !said.startsWith("0 "))
    .join(", ");
}

// checksOf counts the checks on the work's commit by where each stands: the pull request's checks when the work
// lands through one, and the checks on record otherwise.
export function checksOf(standing: Standing): string {
  const forge = standing.pull_request?.checks ?? [];
  const states = forge.length ? forge.map((check) => check.state as string) : standing.checks.map((check) => check.status);
  if (states.length === 0) {
    return "No check";
  }
  const of = (...names: string[]) => states.filter((state) => names.includes(state)).length;
  const parts: [number, string][] = [
    [of("passed", "success"), "passed"],
    [of("failed", "failure"), "failed"],
    [of("pending"), "running"],
    [of("skipped"), "skipped"],
  ];
  return parts
    .filter(([amount]) => amount > 0)
    .map(([amount, word]) => count(amount, word))
    .join(", ");
}

// waitsOn says what the work in review waits for, with the ledger's words for why when it has them.
export function waitsOn(row: QueueRow): string {
  const why = row.standing.why;
  const lead = row.state === "acceptance" ? "Your word" : row.merge ? "Your approval of the merge" : "The merge";
  return why ? `${lead}: ${why}` : lead;
}

// PullRequestRow is one open pull request ropeshift opened for work in review.
export type PullRequestRow = { id: string; title: string; since: number; row: WorkRow; pr: PullRequest; reviewed: boolean; checks: string; mergeable: string };

const mergeableWord: Record<string, string> = { MERGEABLE: "Merges cleanly", CONFLICTING: "Conflicts with its base" };

// mergeableWords says what the forge reports of whether a pull request merges cleanly.
export function mergeableWords(word: string | undefined): string {
  return (word && mergeableWord[word]) || "Not known yet";
}

// pullRequestsOf lists the open pull requests the work lands through, the longest wait first. A pull request is
// reviewed when a verdict read its head commit.
export function pullRequestsOf(rows: WorkRow[]): PullRequestRow[] {
  return rows
    .flatMap((row): PullRequestRow[] => {
      const standing = standingOf(row);
      const pr = standing?.pull_request;
      if (!standing || !pr || pr.state !== "open") {
        return [];
      }
      return [
        {
          id: row.order.id,
          title: row.order.title || row.order.id,
          since: row.since ?? 0,
          row,
          pr,
          reviewed: (standing.verdicts ?? []).some((verdict) => verdict.commit === pr.head_commit),
          checks: checksOf(standing),
          mergeable: mergeableWords(pr.mergeable),
        },
      ];
    })
    .sort(byWait);
}

// A rate over fewer decisions than this says little, so Reviews shows the counts and no percentage.
export const enoughDecisions = 10;

export type AgreementWords = {
  decisions: number;
  agreed: number;
  disagreed: number;
  unknown: number;
  rate: string;
  approvedThenRejected: number;
  rejectedThenApproved: number;
  laterDefects: number;
};

// agreementOf says how a member's decisions stood against the review agent's verdicts over one period. The
// approvals the person overturned are work the review passed and the person sent back, the failures they overrode
// are work the review failed and the person accepted, and a later defect is an approval overturned after the work
// was accepted.
export function agreementOf(period: Agreement): AgreementWords {
  const { value, numerator, denominator } = period.agreement_rate;
  let rate = "Not enough decisions for a rate yet";
  if (period.decisions >= enoughDecisions) {
    rate = value === null ? "No decision to compare" : `${Math.round(value * 1000) / 10}% (${numerator} of ${denominator})`;
  }
  return {
    decisions: period.decisions,
    agreed: period.agreed,
    disagreed: period.disagreed,
    unknown: period.unknown,
    rate,
    approvedThenRejected: period.approvals_overturned,
    rejectedThenApproved: period.failures_overridden,
    laterDefects: period.later_defects,
  };
}
