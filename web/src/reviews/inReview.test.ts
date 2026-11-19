import { describe, expect, it } from "vitest";

import type { Completed, Plans, Standing, InProgress, NeedsYou, OnHold, Stuck } from "../api/types";
import { index } from "../floor/workIndex";
import { golden } from "../testing";
import { agreementOf, checksOf, enoughDecisions, mergeableWords, notesOf, pullRequestsOf, queueOf, verdictsOf, waitsOn } from "./inReview";

// The work in review as the API serves it from a real ledger: a change the review could not judge, which waits for
// the member's acceptance; a change whose review passed, whose pull request has a check running and whose merge
// waits for the member's approval; and a change whose merge the member approved.
const completed = golden<Completed>("reviews-completed");
const needs = golden<NeedsYou>("reviews-needs-you");
const rows = () => index(golden<Plans>("plans"), golden<InProgress>("in-progress"), golden<OnHold>("on-hold"), golden<Stuck>("stuck"), completed);

describe("the queue", () => {
  it("holds the work that waits for acceptance or a merge, the longest wait first", () => {
    expect(queueOf(rows(), needs.items).map((row) => [row.id, row.state])).toEqual([
      ["wo-accept", "acceptance"],
      ["wo-ruled", "integrating"],
      ["wo-merging", "integrating"],
    ]);
  });

  it("offers accept and rework where the read model lists them, and a merge where a merge request waits", () => {
    const [accept, ruled, merging] = queueOf(rows(), needs.items);
    expect(accept).toMatchObject({ accept: true, rework: true, merge: undefined });
    expect(ruled?.accept).toBe(false);
    expect(ruled?.rework).toBe(false);
    expect(ruled?.merge?.kind).toBe("merge");
    expect(ruled?.merge?.work_order_id).toBe("wo-ruled");
    expect(merging).toMatchObject({ accept: false, rework: false, merge: undefined });
  });

  it("offers nothing the read model does not list, whatever state the work stands in", () => {
    const without = needs.items.filter((item) => item.kind !== "work" && item.kind !== "approval");
    for (const row of queueOf(rows(), without)) {
      expect([row.accept, row.rework, row.merge]).toEqual([false, false, undefined]);
    }
  });

  it("offers only what a request of this project asks", () => {
    const elsewhere = needs.items.map((item) => ({ ...item, project: { id: "other", name: "other" } }));
    for (const row of queueOf(rows(), elsewhere, "default")) {
      expect([row.accept, row.rework, row.merge]).toEqual([false, false, undefined]);
    }
    const here = needs.items.map((item) => ({ ...item, project: { id: "default", name: "default" } }));
    expect(queueOf(rows(), here, "default").map((row) => row.accept || row.rework || row.merge !== undefined)).toEqual([true, true, false]);
  });

  it("leaves out a merge request that was decided", () => {
    const decided = structuredClone(needs.items).map((item) => (item.kind === "approval" ? { ...item, approval: { ...item.approval, state: "approved" } } : item));
    expect(queueOf(rows(), decided).find((row) => row.id === "wo-ruled")?.merge).toBeUndefined();
  });
});

const standingOf = (id: string): Standing => (completed.items.find((item) => item.work_order.id === id) as Completed["items"][number]).standing;

describe("what a row says", () => {
  it("counts the verdicts by kind", () => {
    expect(verdictsOf(standingOf("wo-accept"))).toBe("1 cannot judge");
    expect(verdictsOf(standingOf("wo-ruled"))).toBe("1 pass");
    expect(verdictsOf(standingOf("wo-canceled"))).toBe("No verdict");
    const mixed = structuredClone(standingOf("wo-ruled"));
    mixed.verdicts = [
      { ...(mixed.verdicts?.[0] as NonNullable<Standing["verdicts"]>[number]), criterion: "c1", verdict: "pass" },
      { ...(mixed.verdicts?.[0] as NonNullable<Standing["verdicts"]>[number]), criterion: "c2", verdict: "fail" },
      { ...(mixed.verdicts?.[0] as NonNullable<Standing["verdicts"]>[number]), criterion: "c3", verdict: "pass" },
    ];
    expect(verdictsOf(mixed)).toBe("2 pass, 1 fail");
  });

  it("counts the security notes apart from the verdicts, and says nothing when there are none", () => {
    expect(notesOf(standingOf("wo-accept"))).toBe("2 security notes");
    expect(notesOf(standingOf("wo-ruled"))).toBe("");
    const one = structuredClone(standingOf("wo-accept"));
    one.notes = one.notes?.slice(0, 1);
    expect(notesOf(one)).toBe("1 security note");
    expect(verdictsOf(one)).toBe("1 cannot judge");
  });

  it("counts the checks of the pull request when there is one, and the lane's otherwise", () => {
    expect(checksOf(standingOf("wo-ruled"))).toBe("1 passed, 1 running");
    expect(checksOf(standingOf("wo-merging"))).toBe("2 passed");
    expect(checksOf(standingOf("wo-accept"))).toBe("No check");
    expect(checksOf(standingOf("wo-failed"))).toBe("1 failed");
  });

  it("says what each row waits on, in the ledger's words when it has them", () => {
    const [accept, ruled, merging] = queueOf(rows(), needs.items);
    expect(waitsOn(accept as NonNullable<typeof accept>)).toBe("Your word: the review could not judge criterion c1");
    expect(waitsOn(ruled as NonNullable<typeof ruled>)).toBe("Your approval of the merge: required checks are still running on cab32100: e2e");
    expect(waitsOn(merging as NonNullable<typeof merging>)).toBe("The merge: the worker is merging fed98700 into main");
  });
});

describe("the pull requests", () => {
  it("lists the open ones with their head commit, whether a review read it, and their checks", () => {
    const found = pullRequestsOf(rows());
    expect(found.map((row) => [row.id, row.pr.number, row.pr.head_commit, row.reviewed, row.checks, row.mergeable])).toEqual([
      ["wo-ruled", 11, "cab3210000000000000000000000000000000000", true, "1 passed, 1 running", "Merges cleanly"],
      ["wo-merging", 12, "fed9870000000000000000000000000000000000", true, "2 passed", "Merges cleanly"],
    ]);
  });

  it("says a pull request whose head no verdict read is not reviewed", () => {
    const moved = rows();
    const ruled = moved.find((row) => row.order.id === "wo-ruled");
    const pr = ruled?.completed?.standing.pull_request;
    if (pr) {
      pr.head_commit = "0ddba11";
    }
    expect(pullRequestsOf(moved).find((row) => row.id === "wo-ruled")?.reviewed).toBe(false);
  });

  it("leaves out a pull request that is no longer open", () => {
    const merged = rows();
    const pr = merged.find((row) => row.order.id === "wo-merging")?.completed?.standing.pull_request;
    if (pr) {
      pr.state = "merged";
    }
    expect(pullRequestsOf(merged).map((row) => row.id)).toEqual(["wo-ruled"]);
  });

  it.each([
    ["MERGEABLE", "Merges cleanly"],
    ["CONFLICTING", "Conflicts with its base"],
    ["UNKNOWN", "Not known yet"],
    ["", "Not known yet"],
    [undefined, "Not known yet"],
  ])("reads the forge's %s as %s", (word, said) => {
    expect(mergeableWords(word)).toBe(said);
  });
});

describe("review agreement", () => {
  const measures = golden<{ measures: { review_agreement: { workspace: { last_30_days: Parameters<typeof agreementOf>[0] } } } }>("operations").measures;

  it("shows the counts and no rate under ten decisions", () => {
    const period = measures.review_agreement.workspace.last_30_days;
    expect(period.decisions).toBeLessThan(enoughDecisions);
    expect(agreementOf(period)).toMatchObject({ rate: "Not enough decisions for a rate yet", decisions: 1 });
  });

  it("shows the rate from ten decisions", () => {
    const period = { ...measures.review_agreement.workspace.last_30_days, decisions: 12, agreed: 9, disagreed: 3, agreement_rate: { numerator: 9, denominator: 12, value: 0.75 } };
    expect(agreementOf(period).rate).toBe("75% (9 of 12)");
  });

  it("says approved then rejected, rejected then approved, and later defects", () => {
    const period = { ...measures.review_agreement.workspace.last_30_days, approvals_overturned: 2, later_defects: 1, failures_overridden: 3 };
    expect(agreementOf(period)).toMatchObject({ approvedThenRejected: 2, rejectedThenApproved: 3, laterDefects: 1 });
  });
});
