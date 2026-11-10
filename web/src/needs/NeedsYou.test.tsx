import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { goldenNeedsYou, refusal, serveApi } from "../testing";
import type { NeedItem, NeedsYou as View, QuestionItem } from "../api/types";
import { NeedsYou } from "./NeedsYou";
import { waitLabel, waitWords } from "./wait";

const hostile = `<img src=x onerror=alert(1)><script>alert(2)</script>`;

function show(view: View = goldenNeedsYou(), after: () => Promise<void> = async () => {}) {
  return render(<NeedsYou view={view} readAt={Date.now()} after={after} />);
}

function itemOf<K extends NeedItem["kind"]>(view: View, kind: K): Extract<NeedItem, { kind: K }> {
  const found = view.items.find((item) => item.kind === kind);
  if (!found) {
    throw new Error(`no ${kind} in the golden view`);
  }
  return found as Extract<NeedItem, { kind: K }>;
}

// item matches the label that leads a line of a difference, such as "Added".
const item = (label: string) => (_: string, element: Element | null) => element?.tagName === "LI" && (element.firstChild?.textContent ?? "") === label;

function cardNamed(name: RegExp) {
  return screen.getByRole("article", { name });
}

describe("the queue", () => {
  it("keeps the server's order, the longest wait first", () => {
    const view = goldenNeedsYou();
    show(view);
    const headings = screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent);
    expect(headings).toHaveLength(view.items.length);
    expect(headings[0]).toBe("Rotate the keys");
    expect(headings[1]).toBe("Move the archive");
    const since = view.items.map((item) => item.since);
    expect(since).toEqual([...since].sort((a, b) => a - b));
  });

  it("states how long each thing has waited, for eyes and for a screen reader", () => {
    const view = goldenNeedsYou();
    show(view);
    const first = view.items[0];
    if (!first) {
      throw new Error("empty view");
    }
    const waited = view.as_of - first.since;
    const article = screen.getAllByRole("article")[0];
    if (!article) {
      throw new Error("no article");
    }
    expect(within(article).getByText(waitLabel(waited))).toBeTruthy();
    expect(within(article).getByText(waitWords(waited))).toBeTruthy();
  });

  it("says plainly when nothing waits", () => {
    show({ ...goldenNeedsYou(), items: [] });
    expect(screen.getByText("Nothing is waiting.")).toBeTruthy();
    expect(screen.queryByRole("list")).toBeNull();
  });

  it("keeps keyboard focus in the page when an answered item leaves", () => {
    const view = goldenNeedsYou();
    const { rerender } = show(view);
    rerender(<NeedsYou view={{ ...view, items: view.items.slice(1) }} readAt={Date.now()} after={async () => {}} />);
    expect(document.activeElement).toBe(screen.getByRole("heading", { level: 1 }));
  });

  it("leaves a page that just opened at its start, so the skip link comes first", () => {
    show();
    expect(document.activeElement).toBe(document.body);
  });

  it("leaves focus alone when a person is somewhere else in the page", async () => {
    const view = goldenNeedsYou();
    const { rerender } = show(view);
    const field = screen.getAllByRole("radio")[0];
    field?.focus();
    rerender(<NeedsYou view={{ ...view, items: view.items.slice(1) }} readAt={Date.now()} after={async () => {}} />);
    expect(document.activeElement).toBe(field);
  });

  it("gives every item a heading it is labelled by", () => {
    show();
    for (const article of screen.getAllByRole("article")) {
      expect(article.getAttribute("aria-labelledby")).toBeTruthy();
    }
  });
});

describe("waits", () => {
  it.each([
    [59_000, "now"],
    [60_000, "1m"],
    [3_599_000, "59m"],
    [3_600_000, "1h"],
    [3_660_000, "1h 1m"],
    [86_400_000, "1d"],
    [90_000_000, "1d 1h"],
  ])("%d ms reads %s", (ms, label) => {
    expect(waitLabel(ms)).toBe(label);
  });

  it("reads in words", () => {
    expect(waitWords(3_660_000)).toBe("waiting 1 hour 1 minute");
    expect(waitWords(90_000_000)).toBe("waiting 1 day 1 hour");
    expect(waitWords(1_000)).toBe("waiting since just now");
  });
});

describe("a batch of questions", () => {
  const batchView = () => {
    const view = goldenNeedsYou();
    return { view, item: itemOf(view, "questions") };
  };

  it("needs one press when a member agrees with every recommendation", async () => {
    const { view, item } = batchView();
    const sent = serveApi(() => ({ status: 201 }));
    const after = vi.fn(async () => {});
    show(view, after);
    await userEvent.click(screen.getByRole("button", { name: "Send 2 answers" }));
    await waitFor(() => expect(sent).toHaveLength(2));
    expect(sent.map((call) => call.path)).toEqual(item.batch.items.map((question) => `/v1/questions/${question.id}/answer`));
    for (const [index, question] of item.batch.items.entries()) {
      expect(sent[index]?.body).toMatchObject({ id: `answer-${question.id}`, answer: question.recommendation });
    }
    await waitFor(() => expect(after).toHaveBeenCalledOnce());
  });

  it("sends each answer once, however hard a person presses", async () => {
    const { view } = batchView();
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const sent = serveApi(() => ({ status: 201 }));
    const after = vi.fn(() => held);
    show(view, after);
    const button = screen.getByRole("button", { name: "Send 2 answers" });
    await userEvent.dblClick(button);
    await userEvent.click(button);
    await waitFor(() => expect(sent).toHaveLength(2));
    release();
    expect(sent).toHaveLength(2);
  });

  it("repeats an answer under one ID, so a retry after a lost reply is one decision", async () => {
    const { view } = batchView();
    let calls = 0;
    const sent = serveApi(() => {
      calls += 1;
      return calls === 1 ? refusal(503, "unavailable", "The store is busy.") : { status: 201 };
    });
    show(view);
    await userEvent.click(screen.getByRole("button", { name: "Send 2 answers" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Send 2 answers" }));
    await waitFor(() => expect(sent.length).toBeGreaterThanOrEqual(3));
    const first = sent[0]?.body as { id: string };
    expect(sent.filter((call) => (call.body as { id: string }).id === first.id).length).toBe(2);
  });

  it("takes a question someone else answered first as answered", async () => {
    const { view } = batchView();
    serveApi(() => refusal(409, "resolved", "This question already has its answer."));
    show(view);
    await userEvent.click(screen.getByRole("button", { name: "Send 2 answers" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });

  it("lets a member answer in their own words", async () => {
    const { view, item } = batchView();
    const sent = serveApi(() => ({ status: 201 }));
    show(view);
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    const group = screen.getByRole("group", { name: first.text });
    await userEvent.click(within(group).getByRole("radio", { name: "Something else" }));
    await userEvent.type(within(group).getByRole("textbox", { name: "Your answer" }), "Hold it for a day.");
    await userEvent.click(screen.getByRole("button", { name: "Send 2 answers" }));
    await waitFor(() => expect(sent).toHaveLength(2));
    expect(sent[0]?.body).toMatchObject({ answer: "Hold it for a day." });
  });

  it("leaves a one-way choice for a person to make", async () => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    first.escalation_reason = "one_way";
    const sent = serveApi(() => ({ status: 201 }));
    show(view);
    const group = screen.getByRole("group", { name: first.text });
    for (const radio of within(group).getAllByRole("radio")) {
      expect((radio as HTMLInputElement).checked).toBe(false);
    }
    await userEvent.click(screen.getByRole("button", { name: "Send 1 of 2 answers" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.path).not.toContain(first.id);
  });

  // A send counts what it sends, so each question it counts must show what it will send.
  function countedAsShown(group: HTMLElement, item: QuestionItem): boolean {
    const chosen = within(group)
      .queryAllByRole("radio")
      .find((radio) => (radio as HTMLInputElement).checked);
    const box = within(group).queryByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement | null;
    const ownWords = chosen ? (chosen as HTMLInputElement).value === "__own_words__" : (item.options ?? []).length === 0;
    return ownWords ? (box?.value.trim() ?? "") !== "" : chosen !== undefined;
  }

  it.each([
    ["a recommendation that is no option", { recommendation: "Keep the budget, capped at three" }],
    ["a recommendation with no options at all", { recommendation: "Keep the budget", options: [] }],
    ["no recommendation", { recommendation: undefined }],
  ])("counts only what a person sees chosen, given %s", (_name, change: Partial<QuestionItem>) => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    Object.assign(first, change);
    show(view);
    const counted = item.batch.items.filter((question) => countedAsShown(screen.getByRole("group", { name: question.text }), question)).length;
    const button = screen.getByRole("button", { name: /^Send \d.* answers$/ });
    const count = item.batch.items.length;
    const want = counted === count ? `Send ${count} answers` : `Send ${counted} of ${count} answers`;
    expect(button.textContent).toBe(want);
  });

  it("shows a recommendation that is no option as chosen, in the box for a person's own words", async () => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    first.recommendation = "Keep the budget, capped at three";
    const sent = serveApi(() => ({ status: 201 }));
    show(view);
    const group = screen.getByRole("group", { name: first.text });
    const own = within(group).getByRole("radio", { name: /^Something else/ }) as HTMLInputElement;
    expect(own.checked).toBe(true);
    expect(own.closest("label")?.textContent).toContain("Recommended");
    expect((within(group).getByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement).value).toBe("Keep the budget, capped at three");
    await userEvent.click(screen.getByRole("button", { name: "Send 2 answers" }));
    await waitFor(() => expect(sent).toHaveLength(2));
    expect(sent[0]?.body).toMatchObject({ answer: "Keep the budget, capped at three" });
  });

  it("keeps a one-way choice unchosen, with a recommendation that is no option ready in the box", async () => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    first.escalation_reason = "one_way";
    first.recommendation = "Keep the budget, capped at three";
    show(view);
    const group = screen.getByRole("group", { name: first.text });
    for (const radio of within(group).getAllByRole("radio")) {
      expect((radio as HTMLInputElement).checked).toBe(false);
    }
    expect(screen.getByRole("button", { name: "Send 1 of 2 answers" })).toBeTruthy();
    await userEvent.click(within(group).getByRole("radio", { name: /^Something else/ }));
    expect((within(group).getByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement).value).toBe("Keep the budget, capped at three");
    expect(screen.getByRole("button", { name: "Send 2 answers" })).toBeTruthy();
  });

  it("says one answer in the singular", () => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    first.escalation_reason = "one_way";
    item.batch.items = [first];
    show(view);
    expect(screen.getByRole("button", { name: "Send 0 of 1 answer" })).toBeTruthy();
  });

  it("marks what is recommended and what blocks work", () => {
    const { view, item } = batchView();
    show(view);
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    const group = screen.getByRole("group", { name: first.text });
    expect(within(group).getByText("Blocks work")).toBeTruthy();
    expect(within(group).getByText("Recommended")).toBeTruthy();
  });
});

describe("an approval", () => {
  it("binds to the digest this page read", async () => {
    const view = goldenNeedsYou();
    const { approval } = itemOf(view, "approval");
    const sent = serveApi(() => ({ status: 200 }));
    show(view);
    await userEvent.click(screen.getByRole("button", { name: "Allow this one call" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(approval.subject_digest).not.toBe("");
    expect(sent[0]).toMatchObject({
      method: "POST",
      path: `/v1/approvals/${approval.id}/decide`,
      body: { digest: approval.subject_digest, allow: true },
    });
  });

  it("sends a denial with its reason", async () => {
    const view = goldenNeedsYou();
    const sent = serveApi(() => ({ status: 200 }));
    show(view);
    const card = cardNamed(/^Allow /);
    await userEvent.type(within(card).getByRole("textbox", { name: /Reason/ }), "Tag it by hand.");
    await userEvent.click(within(card).getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toMatchObject({ allow: false, reason: "Tag it by hand." });
  });

  it("says so when the call changed since this read", async () => {
    const view = goldenNeedsYou();
    const after = vi.fn(async () => {});
    serveApi(() => refusal(409, "stale_digest", "This request no longer matches what you read."));
    show(view, after);
    await userEvent.click(screen.getByRole("button", { name: "Allow this one call" }));
    expect((await screen.findByRole("alert")).textContent).toContain("no longer matches");
    expect(after).toHaveBeenCalled();
  });

  it("shows what will run as text", () => {
    const view = goldenNeedsYou();
    const { approval } = itemOf(view, "approval");
    show(view);
    expect(screen.getAllByText(approval.command ?? approval.summary).length).toBeGreaterThan(0);
  });
});

describe("an unknown effect", () => {
  it("offers what the server offers, and sends what a person says", async () => {
    const view = goldenNeedsYou();
    const { effect } = itemOf(view, "effect");
    const sent = serveApi(() => ({ status: 200 }));
    show(view);
    const card = cardNamed(/Did it happen/);
    expect(within(card).getByText(effect.key)).toBeTruthy();
    await userEvent.click(within(card).getByRole("button", { name: "Run it again" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ path: `/v1/effects/${encodeURIComponent(effect.id)}/decide`, body: { outcome: "retry" } });
  });

  it("warns that a retry may repeat it", () => {
    show();
    expect(within(cardNamed(/Did it happen/)).getByText(/may repeat it/)).toBeTruthy();
  });

  it("drops what the server no longer offers", () => {
    const view = goldenNeedsYou();
    itemOf(view, "effect").effect.offers = ["happened", "not_happened"];
    show(view);
    const card = cardNamed(/Did it happen/);
    expect(within(card).queryByRole("button", { name: "Run it again" })).toBeNull();
    expect(within(card).queryByText(/may repeat it/)).toBeNull();
    expect(within(card).getByRole("button", { name: "It did not happen" })).toBeTruthy();
  });
});

describe("waiting work", () => {
  it("runs an action that needs no words in one press", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show();
    await userEvent.click(within(cardNamed(/Fix the flaky test/)).getByRole("button", { name: "Reset the breaker" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-tripped/lane-reset" });
  });

  it("asks why before an action that changes who owns work", async () => {
    const sent = serveApi(() => ({ status: 204 }));
    show();
    const card = cardNamed(/Rotate the keys/);
    await userEvent.click(within(card).getByRole("button", { name: "End this claim" }));
    expect(sent).toHaveLength(0);
    const confirm = within(card).getByRole("button", { name: "End this claim" });
    expect((confirm as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(within(card).getByRole("textbox", { name: "Why" }), "The worker is gone.");
    await userEvent.click(confirm);
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ path: "/v1/work-orders/wo-lapsed/reassign", body: { reason: "The worker is gone." } });
  });

  it("cancels work only once a person says why", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show();
    const card = cardNamed(/Move the archive/);
    await userEvent.click(within(card).getByRole("button", { name: "Cancel this work" }));
    expect(sent).toHaveLength(0);
    await userEvent.type(within(card).getByRole("textbox", { name: "Why" }), "The plan changed.");
    await userEvent.click(within(card).getByRole("button", { name: "Cancel this work" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-blocked/cancel", body: { reason: "The plan changed." } });
  });

  it("starts a stuck orchestrator through its worker, which has no work order", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    const view = goldenNeedsYou();
    const orchestrator = { id: "orchestrator-worker-a", worker_id: "worker-a", desired_state: "running", run: 1, resets: 0, actual_state: "tripped" };
    view.items = [
      { kind: "work", id: orchestrator.id, since: view.as_of, work: { reason: "tripped", orchestrator, actions: ["start_orchestrator"] } },
    ];
    show(view);
    const card = screen.getByRole("article");
    expect(within(card).getByText(/breaker stopped it/)).toBeTruthy();
    await userEvent.click(within(card).getByRole("button", { name: "Start it again" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "PUT", path: "/v1/workers/worker-a/orchestrator", body: { desired_state: "running" } });
  });

  it("names the question a parked lane waits for, and keeps resume as the way past it", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show();
    const card = cardNamed(/Wait for the check/);
    const lead = within(card).getByText(/^Its lane is parked/);
    expect(lead.textContent).toBe("Its lane is parked until question question-1 is answered, or until a person resumes it.");
    expect(within(lead).getByRole("link", { name: "question-1" }).getAttribute("href")).toBe("/questions?id=question-1");
    expect(within(card).getByText("Resume lets it go on now, without waiting.")).toBeTruthy();
    await userEvent.click(within(card).getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-parked/lane-resume" });
  });

  it("says a lane only a person can move waits for a person", () => {
    const view = goldenNeedsYou();
    const parked = view.items.find((item) => item.kind === "work" && item.work.reason === "human_resume");
    if (parked?.kind !== "work" || !parked.work.wakes?.[0]) {
      throw new Error("no parked lane in the golden view");
    }
    parked.work.wakes[0].expression = `{"human_resume":true}`;
    show(view);
    const card = cardNamed(/Wait for the check/);
    expect(within(card).getByText("Its lane is parked until a person says go.")).toBeTruthy();
    expect(within(card).queryByText(/without waiting/)).toBeNull();
  });

  it("shows the review's verdict for each criterion, in words, with its reasons and citations", () => {
    show();
    const card = cardNamed(/Trim the log/);
    const review = within(card).getByRole("region", { name: "What the review found" });
    expect(within(review).getByText("c1")).toBeTruthy();
    expect(within(review).getByText("Cannot judge")).toBeTruthy();
    expect(within(review).getByText(/nothing I can run shows it keeps the last day/)).toBeTruthy();
    expect(within(review).getByText("trim.go:30")).toBeTruthy();
    expect(within(review).getByText("FR-LOG-2")).toBeTruthy();
    expect(within(card).getByText(/the review could not judge criterion c1/)).toBeTruthy();
    // The person still decides, and the card still offers both.
    expect(within(card).getByRole("button", { name: "Accept" })).toBeTruthy();
    expect(within(card).getByRole("button", { name: "Send back for rework" })).toBeTruthy();
  });

  it("says a verdict in text for every kind, never by colour alone", () => {
    const view = goldenNeedsYou();
    const accept = view.items.find((item) => item.kind === "work" && item.work.reason === "acceptance");
    if (accept?.kind !== "work" || !accept.work.standing) {
      throw new Error("no acceptance item in the golden view");
    }
    const base = accept.work.standing.verdicts?.[0];
    if (!base) {
      throw new Error("no verdict in the golden view");
    }
    accept.work.standing.verdicts = [
      { ...base, criterion: "c1", verdict: "pass", reasons: "retry.go retries twice", citations: ["retry.go:12"] },
      { ...base, criterion: "c2", verdict: "fail", reasons: "nothing is logged", citations: [] },
      { ...base, criterion: "c3", verdict: "cannot_judge", reasons: "no test runs here", citations: undefined },
    ];
    show(view);
    const review = within(cardNamed(/Trim the log/)).getByRole("region", { name: "What the review found" });
    for (const word of ["Pass", "Fail", "Cannot judge"]) {
      expect(within(review).getByText(word)).toBeTruthy();
    }
    expect(within(review).getAllByRole("listitem")).toHaveLength(3 + 1);
    expect(within(review).getByText("nothing is logged")).toBeTruthy();
  });

  it("keeps a review's words from becoming markup", () => {
    const view = goldenNeedsYou();
    const accept = view.items.find((item) => item.kind === "work" && item.work.reason === "acceptance");
    const verdict = accept?.kind === "work" ? accept.work.standing?.verdicts?.[0] : undefined;
    if (!verdict) {
      throw new Error("no verdict in the golden view");
    }
    verdict.reasons = hostile;
    verdict.citations = [hostile];
    show(view);
    const card = cardNamed(/Trim the log/);
    expect(card.querySelector("img")).toBeNull();
    expect(card.querySelector("script")).toBeNull();
    expect(within(card).getAllByText(hostile).length).toBeGreaterThan(0);
  });

  it("says a review that failed or is still open gave no verdict, and that the person decides without it", () => {
    for (const [state, words] of [
      ["failed", /The review of this work failed/],
      ["running", /A review of this work has not ended/],
    ] as const) {
      const view = goldenNeedsYou();
      const accept = view.items.find((item) => item.kind === "work" && item.work.reason === "acceptance");
      const standing = accept?.kind === "work" ? accept.work.standing : undefined;
      if (!standing?.reviews?.[0]) {
        throw new Error("no review in the golden view");
      }
      delete standing.verdicts;
      standing.reviews[0].state = state;
      const { unmount } = show(view);
      expect(within(within(cardNamed(/Trim the log/)).getByRole("region", { name: "What the review found" })).getByText(words)).toBeTruthy();
      unmount();
    }
  });

  it("says nothing of a review when work was never reviewed", () => {
    const view = goldenNeedsYou();
    const accept = view.items.find((item) => item.kind === "work" && item.work.reason === "acceptance");
    if (accept?.kind !== "work" || !accept.work.standing) {
      throw new Error("no acceptance item in the golden view");
    }
    delete accept.work.standing.verdicts;
    delete accept.work.standing.why;
    show(view);
    expect(within(cardNamed(/Trim the log/)).queryByRole("region", { name: "What the review found" })).toBeNull();
  });

  it("offers both ways out of blocked", () => {
    show();
    const card = cardNamed(/Move the archive/);
    expect(within(card).getByRole("button", { name: "Back to ready" })).toBeTruthy();
    expect(within(card).getByRole("button", { name: "Back to planning" })).toBeTruthy();
    expect(within(card).getByText("no_outcome")).toBeTruthy();
  });

  it("keeps each ID on one line, whole in its title", () => {
    const view = goldenNeedsYou();
    const { approval } = itemOf(view, "approval");
    const { effect } = itemOf(view, "effect");
    show(view);
    const ids = [
      [/Rotate the keys/, "lane-wo-lapsed"],
      [/^Allow /, approval.lane_id],
      [/^Allow /, approval.work_order_id],
      [/Did it happen/, effect.key],
      [/Did it happen/, effect.worker_id],
    ] as const;
    for (const [card, id] of ids) {
      const shown = within(cardNamed(card)).getAllByText(id);
      for (const element of shown) {
        expect(element.classList.contains("id"), id).toBe(true);
        expect(element.getAttribute("title")).toBe(id);
      }
    }
  });

  it("keeps a last reported value apart from an observed one", () => {
    const view = goldenNeedsYou();
    show(view);
    expect(screen.getAllByText(/^Last reported/).length + screen.getAllByText(/^Observed/).length).toBeGreaterThan(0);
  });
});

describe("a plan approval", () => {
  const planView = () => {
    const view = goldenNeedsYou();
    return { view, item: itemOf(view, "plan_approval") };
  };
  const card = () => cardNamed(/^Approve The factory floor/);

  it("names the plan, its version, who asked, and what it holds", () => {
    const { view, item } = planView();
    show(view);
    const shown = card();
    expect(within(shown).getByRole("heading", { level: 2 }).textContent).toBe("Approve The factory floor, version 6?");
    expect(within(shown).getByText("system:intake")).toBeTruthy();
    const holds = within(shown).getByRole("list", { name: "What it holds" });
    expect(holds.textContent).toContain("4 contracts");
    expect(holds.textContent).toContain("3 milestones");
    expect(holds.textContent).toContain("1 spec");
    expect(holds.textContent).toContain("1 requirements document");
    expect(holds.textContent).toContain("1 architecture document");
    expect(within(shown).getByRole("link", { name: "Read the plan" }).getAttribute("href")).toBe(
      `/plans?plan=${encodeURIComponent(item.plan_approval.approval.plan_id ?? "")}`,
    );
  });

  it("shows how it differs from the version a member approved", () => {
    show();
    const difference = within(card()).getByRole("region", { name: "Since version 5" });
    expect(within(difference).getByText(item("Added")).textContent).toContain("architecture:factory");
    const changed = within(difference).getByText(item("Changed")).textContent;
    for (const ref of ["contract:core", "contract:ui", "plan:factory", "spec:factory-api"]) {
      expect(changed).toContain(ref);
    }
    expect(within(difference).queryByText(item("Removed"))).toBeNull();
    const core = within(difference).getByRole("listitem", { name: "contract:core" });
    expect(core.textContent).toContain("Criteria added: c2");
    expect(core.textContent).toContain("Criteria changed: c1");
    expect(within(difference).getByRole("listitem", { name: "contract:ui" }).textContent).toContain("Work orders added: ui-2");
  });

  it("says a first version has nothing to differ from", () => {
    const { view, item: need } = planView();
    need.plan_approval.difference = { base: 0, added: ["plan:factory", "spec:factory-api"] };
    show(view);
    const difference = within(card()).getByRole("region", { name: "A first version" });
    expect(difference.textContent).toContain("Nothing was approved before");
    expect(within(difference).getByText(item("Added")).textContent).toContain("spec:factory-api");
  });

  it("says when a version changes nothing a member approved", () => {
    const { view, item: need } = planView();
    need.plan_approval.difference = { base: 5 };
    show(view);
    expect(within(card()).getByText("No record differs from version 5.")).toBeTruthy();
  });

  it("lists each answer it drafts, which approving it decides", () => {
    show();
    const drafts = within(card()).getByRole("list", { name: "Draft decisions" });
    const rows = within(drafts).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]?.textContent).toContain("Which agent CLI runs the first lanes?");
    expect(rows[0]?.textContent).toContain("The one already installed on the machine.");
    expect(rows[0]?.textContent).toContain("It needs no new sign-in.");
    expect(within(card()).getByText(/decides each draft decision as yours/)).toBeTruthy();
  });

  it("approves with the digest this page read", async () => {
    const { view, item: need } = planView();
    const sent = serveApi(() => ({ status: 200 }));
    const after = vi.fn(async () => {});
    show(view, after);
    await userEvent.click(within(card()).getByRole("button", { name: "Approve this plan" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({
      method: "POST",
      path: `/v1/approvals/${need.id}/decide`,
      body: { digest: need.plan_approval.approval.subject_digest, allow: true },
    });
    await waitFor(() => expect(after).toHaveBeenCalledOnce());
  });

  it("denies with a reason", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show();
    await userEvent.type(within(card()).getByRole("textbox", { name: /Reason/ }), "Split the core first.");
    await userEvent.click(within(card()).getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toMatchObject({ allow: false, reason: "Split the core first." });
  });

  it("says so when the plan changed since this read", async () => {
    serveApi(() => refusal(409, "stale_digest", "This request no longer matches what you read."));
    show();
    await userEvent.click(within(card()).getByRole("button", { name: "Approve this plan" }));
    expect((await within(card()).findByRole("alert")).textContent).toContain("no longer matches");
  });
});

describe("a contract review", () => {
  const card = () => cardNamed(/^Document the retry budget/);

  it("names the work and its contract, and leads to the contract", () => {
    const view = goldenNeedsYou();
    const { contract_review: review } = itemOf(view, "contract_review");
    show(view);
    const shown = card();
    expect(within(shown).getByText(/no plan approval covers it/)).toBeTruthy();
    expect(within(shown).getByText("Document the retry budget, version 1")).toBeTruthy();
    expect(within(shown).getAllByText("wo-review")[0]?.classList.contains("id")).toBe(true);
    expect(within(shown).getByRole("link", { name: "Read the contract" }).getAttribute("href")).toBe(`/plans?plan=${encodeURIComponent(review.plan)}`);
  });

  it("passes the review in one press", async () => {
    const sent = serveApi(() => ({ status: 200, body: { state: "ready" } }));
    show();
    await userEvent.click(within(card()).getByRole("button", { name: "Pass the review" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-review/review" });
  });

  it("sends work back to planning only once a person says why", async () => {
    const sent = serveApi(() => ({ status: 204 }));
    show();
    await userEvent.click(within(card()).getByRole("button", { name: "Send back to planning" }));
    expect(sent).toHaveLength(0);
    const confirm = within(card()).getByRole("button", { name: "Send back to planning" });
    expect((confirm as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(within(card()).getByRole("textbox", { name: "Why" }), "The criteria are vague.");
    await userEvent.click(confirm);
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-review/send-back", body: { reason: "The criteria are vague." } });
  });

  it("says what the server refused", async () => {
    serveApi(() => refusal(409, "conflict", "The work order moved on."));
    show();
    await userEvent.click(within(card()).getByRole("button", { name: "Pass the review" }));
    expect((await within(card()).findByRole("alert")).textContent).toContain("moved on");
  });

  it("names no contract a plan does not hold", () => {
    const view = goldenNeedsYou();
    itemOf(view, "contract_review").contract_review.plan = "";
    show(view);
    expect(within(card()).queryByRole("link", { name: "Read the contract" })).toBeNull();
  });
});

describe("text from outside", () => {
  it("stays text, whatever it says", () => {
    const view = goldenNeedsYou();
    const { batch } = itemOf(view, "questions");
    const question = batch.items.find((item) => item.text.includes(hostile));
    expect(question).toBeTruthy();
    const { container } = show(view);
    expect(container.textContent).toContain(hostile);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("script")).toBeNull();
    expect(container.innerHTML).not.toContain("<img");
  });

  it.each(["summary", "command", "path", "lane_id"] as const)("stays text in an approval's %s", (field) => {
    const view = goldenNeedsYou();
    itemOf(view, "approval").approval[field] = hostile;
    const { container } = show(view);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain(hostile);
  });

  it("stays text in a plan's draft decisions", () => {
    const view = goldenNeedsYou();
    const drafts = itemOf(view, "plan_approval").plan_approval.drafts;
    expect(drafts.some((draft) => draft.answer.includes(hostile))).toBe(true);
    const { container } = show(view);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("script")).toBeNull();
    expect(cardNamed(/^Approve The factory floor/).textContent).toContain(hostile);
  });

  it.each(["key", "target", "lane_id", "worker_id"] as const)("stays text in an effect's %s", (field) => {
    const view = goldenNeedsYou();
    itemOf(view, "effect").effect[field] = hostile;
    const { container } = show(view);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain(hostile);
  });

  it("is a rule of the source: no page builds HTML from a string", () => {
    const source = (dir: string): string[] =>
      readdirSync(dir).flatMap((name: string) => {
        const path = join(dir, name);
        return statSync(path).isDirectory() ? source(path) : /\.tsx?$/.test(name) && !/\.test\.|testing/.test(name) ? [path] : [];
      });
    for (const path of source("src")) {
      expect(readFileSync(path, "utf8"), path).not.toMatch(/dangerouslySetInnerHTML|innerHTML|outerHTML|insertAdjacentHTML|document\.write|\beval\(/);
    }
  });
});
