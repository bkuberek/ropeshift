import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

import type { ReactElement } from "react";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";

import { CurrentProject } from "../app/route";
import { golden, goldenNeedsYou, here, mergeRequest, refusal, reviewedCommit, serveApi, testProject } from "../testing";
import type { ManifestRegistration, MergeDetail, NeedItem, NeedsYou as View, PlanView, Plans, QuestionItem, WorkOrder } from "../api/types";
import { NeedsYou } from "./NeedsYou";
import { waitLabel, waitWords } from "./wait";

const hostile = `<img src=x onerror=alert(1)><script>alert(2)</script>`;

type Pick = NeedItem["kind"] | ((item: NeedItem) => boolean);

// show renders Needs you. pick opens one item, as its address does, so its card is the one shown.
function show(view: View = goldenNeedsYou(), after: () => Promise<void> = async () => {}, pick?: Pick) {
  const item = pick ? view.items.find(typeof pick === "function" ? pick : (row) => row.kind === pick) : undefined;
  if (item) {
    window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(`${item.kind}:${item.id}`)}`);
  }
  return render(
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={after} />
    </CurrentProject.Provider>,
  );
}

const plainApproval = (item: NeedItem) => item.kind === "approval" && item.approval.kind !== "recovery";

function itemOf<K extends NeedItem["kind"]>(view: View, kind: K): Extract<NeedItem, { kind: K }> {
  const found = view.items.find((item) => item.kind === kind);
  if (!found) {
    throw new Error(`no ${kind} in the golden view`);
  }
  return found as Extract<NeedItem, { kind: K }>;
}

// item matches the label that leads a line of a difference, such as "Added".
const item = (label: string) => (_: string, element: Element | null) => element?.tagName === "LI" && (element.firstChild?.textContent ?? "") === label;

// cardNamed opens the item whose title matches, as a person does from the list, and returns its card.
async function cardNamed(name: RegExp) {
  const link = screen.getAllByRole("link").find((element) => element.classList.contains("item") && name.test(element.querySelector(".item-title")?.textContent ?? ""));
  if (link && link.getAttribute("aria-current") !== "true") {
    await userEvent.click(link);
  }
  return screen.getByRole("article", { name });
}

// titlesOf reads the list's titles in the order it shows them.
const titlesOf = () =>
  within(screen.getByRole("list", { name: "Waiting items" }))
    .getAllByRole("link")
    .filter((link) => link.classList.contains("item"))
    .map((link) => link.querySelector(".item-title")?.textContent);

describe("the queue", () => {
  it("puts what holds a running session first, then the longest wait of each tier", async () => {
    const view = goldenNeedsYou();
    show(view);
    const titles = titlesOf();
    expect(titles).toHaveLength(view.items.length);
    expect(titles.slice(0, 4)).toEqual(["Allow Bash?", "2 questions", "Rotate the keys", "Move the archive"]);
    expect(screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent)).toEqual(
      expect.arrayContaining(["Holding a running session, 2", "Work is stopped, 7", "Waiting for your decision, 3"]),
    );
  });

  it("orders by wait alone on request, and keeps the order in the address", async () => {
    const view = goldenNeedsYou();
    show(view);
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Order" }), "oldest");
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("order")).toBe("oldest"));
    expect(titlesOf().slice(0, 2)).toEqual(["Rotate the keys", "Move the archive"]);
    expect(screen.queryByRole("heading", { name: /^Holding a running session/ })).toBeNull();
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Order" }), "newest");
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("order")).toBe("newest"));
    expect(titlesOf().at(-1)).toBe("Rotate the keys");
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Order" }), "importance");
    await waitFor(() => expect(new URLSearchParams(window.location.search).has("order")).toBe(false));
  });

  it("opens the first item's decision card beside the list", async () => {
    show();
    const first = within(screen.getByRole("list", { name: "Waiting items" })).getAllByRole("link")[0];
    expect(first?.getAttribute("aria-current")).toBe("true");
    expect(screen.getAllByRole("article")).toHaveLength(1);
    expect(screen.getByRole("article", { name: "Allow Bash?" })).toBeTruthy();
  });

  it("opens the card a person chooses, and keeps the others' drafts", async () => {
    show();
    await userEvent.click(await cardNamed(/questions/));
    const [unfold] = screen.getAllByRole("button", { name: "Choose another" });
    if (unfold) {
      await userEvent.click(unfold);
    }
    const somethingElse = screen.getAllByRole("radio", { name: "Something else" })[0];
    if (!somethingElse) {
      throw new Error("no such choice");
    }
    await userEvent.click(somethingElse);
    await userEvent.type(screen.getByRole("textbox", { name: "Your answer" }), "Half of a thought");
    await cardNamed(/Rotate the keys/);
    expect(screen.queryByRole("textbox", { name: "Your answer" })).toBeNull();
    await cardNamed(/questions/);
    expect((screen.getByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement).value).toBe("Half of a thought");
  });

  it("narrows the list to one kind, with a count for each", async () => {
    show();
    const stuck = screen.getByRole("button", { name: /^Stuck/ });
    await userEvent.click(stuck);
    await waitFor(() => expect(stuck.getAttribute("aria-pressed")).toBe("true"));
    const titles = within(screen.getByRole("list", { name: "Waiting items" }))
      .getAllByRole("link")
      .map((link) => link.querySelector(".item-title")?.textContent);
    expect(titles).toContain("Rotate the keys");
    expect(titles).not.toContain("Did it happen?");
    expect(stuck.textContent).toBe(`Stuck${titles.length}`);
  });

  it("says the decided tab has no source yet, and never draws an empty list as done", async () => {
    show();
    await userEvent.click(screen.getByRole("tab", { name: "Decided" }));
    expect(await screen.findByText("Decided requests are not shown yet")).toBeTruthy();
  });

  it("states how long each thing has waited, for eyes and for a screen reader", async () => {
    const view = goldenNeedsYou();
    const first = view.items[0];
    if (!first) {
      throw new Error("empty view");
    }
    show(view, undefined, (item) => item === first);
    const waited = view.as_of - first.since;
    const article = screen.getAllByRole("article")[0];
    if (!article) {
      throw new Error("no article");
    }
    expect(within(article).getByText(waitLabel(waited))).toBeTruthy();
    expect(within(article).getByText(waitWords(waited))).toBeTruthy();
  });

  it("says plainly when nothing waits", async () => {
    show({ ...goldenNeedsYou(), items: [] });
    expect(screen.getByText("Nothing is waiting.")).toBeTruthy();
    expect(screen.queryByRole("list")).toBeNull();
  });

  it("keeps an item resolved elsewhere in place, marked resolved, with focus on its resolution", async () => {
    const view = goldenNeedsYou();
    const { rerender } = show(view, undefined, (item) => item.id === "wo-lapsed");
    rerender(
      <CurrentProject.Provider value={testProject}>
        <NeedsYou view={{ ...view, items: view.items.filter((item) => item.id !== "wo-lapsed") }} readAt={Date.now()} after={async () => {}} />
      </CurrentProject.Provider>,
    );
    await waitFor(() => expect(document.activeElement?.textContent).toContain("no longer waits"));
    expect(document.activeElement?.textContent).toContain("Press J for the next item.");
    const row = screen.getAllByRole("link").find((link) => link.textContent?.includes("Rotate the keys"));
    expect(row?.textContent).toContain("Resolved elsewhere");
    expect(titlesOf()[2]).toBe("Rotate the keys (resolved)");
  });

  it("drops an item resolved elsewhere once the person moves on", async () => {
    const view = goldenNeedsYou();
    const { rerender } = show(view, undefined, (item) => item.id === "wo-lapsed");
    rerender(
      <CurrentProject.Provider value={testProject}>
        <NeedsYou view={{ ...view, items: view.items.filter((item) => item.id !== "wo-lapsed") }} readAt={Date.now()} after={async () => {}} />
      </CurrentProject.Provider>,
    );
    await screen.findByText(/no longer waits/);
    await cardNamed(/Move the archive/);
    expect(titlesOf()).not.toContain("Rotate the keys");
  });

  it("never points to a next item when none waits, and says what happens next instead", async () => {
    const view = goldenNeedsYou();
    const only = view.items.filter((item) => item.kind === "approval");
    const { rerender } = show({ ...view, items: only }, undefined, "approval");
    rerender(
      <CurrentProject.Provider value={testProject}>
        <NeedsYou view={{ ...view, items: [] }} readAt={Date.now()} after={async () => {}} />
      </CurrentProject.Provider>,
    );
    const note = await screen.findByText(/no longer waits/);
    expect(note.textContent).toContain("Nothing else waits.");
    expect(note.textContent).not.toContain("Press J");
    expect(screen.getByRole("link", { name: "Watch it on Floor" }).getAttribute("href")).toContain("/floor");
  });

  it("leaves a page that just opened at its start, so the skip link comes first", async () => {
    show();
    expect(document.activeElement).toBe(document.body);
  });

  it("leaves focus alone when a person is somewhere else in the page", async () => {
    const view = goldenNeedsYou();
    const { rerender } = show(view);
    const chip = screen.getByRole("button", { name: /^Decisions/ });
    chip.focus();
    rerender(
      <CurrentProject.Provider value={testProject}>
        <NeedsYou view={{ ...view, items: view.items.slice(1) }} readAt={Date.now()} after={async () => {}} />
      </CurrentProject.Provider>,
    );
    expect(document.activeElement).toBe(chip);
  });

  it("gives every item a heading it is labelled by", async () => {
    show();
    for (const article of screen.getAllByRole("article")) {
      expect(article.getAttribute("aria-labelledby")).toBeTruthy();
    }
  });
});

describe("the flow from one decision to the next", () => {
  type Request = Extract<NeedItem, { kind: "approval" }>;
  // request is a call a lane asks to run, made from the golden's, under its own ID, lane, subject, and digest.
  const request = (base: Request, id: string, lane: string, subject: string, since: number): Request => ({
    ...structuredClone(base),
    id,
    since,
    approval: { ...structuredClone(base.approval), id, lane_id: lane, subject, subject_digest: `digest-${id}` },
  });
  const requests = (lanes: [string, string][]) => {
    const view = goldenNeedsYou();
    const base = itemOf(view, "approval");
    const items = lanes.map(([lane, subject], index) => request(base, `approval-${index + 1}`, lane, subject, base.since + index * 1000));
    return { view: { ...view, items }, items };
  };
  const posts = (sent: ReturnType<typeof serveApi>) => sent.filter((call) => call.method === "POST");
  const page = (view: View, after: () => Promise<void> = async () => {}) => (
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={after} />
    </CurrentProject.Provider>
  );

  it("opens the next item after a decision, with focus on its primary button, and says what happened and what is next", async () => {
    const { view, items } = requests([
      ["lane-a", "Bash"],
      ["lane-b", "Read"],
    ]);
    serveApi(() => ({ status: 200 }));
    let rerender: (ui: ReactElement) => void = () => {};
    ({ rerender } = show(view, async () => rerender(page({ ...view, items: items.slice(1) })), "approval"));
    await userEvent.click(screen.getByRole("button", { name: "Allow this one call" }));
    await waitFor(() => expect(document.activeElement?.closest("[data-card]")?.getAttribute("data-card")).toBe("approval:approval-2"));
    expect(document.activeElement?.textContent).toBe("Allow this one call");
    expect(document.querySelector("[data-said]")?.textContent).toBe("Approved: Allow Bash. Next: Allow Read?");
    expect(document.querySelector("[data-said]")?.getAttribute("aria-live")).toBe("polite");
    expect(titlesOf()).toEqual(["Allow Read?"]);
  });

  it("never decides the next item with a press that lands as the page moves, or with a key held down", async () => {
    const { view, items } = requests([
      ["lane-a", "Bash"],
      ["lane-b", "Read"],
    ]);
    const sent = serveApi(() => ({ status: 200 }));
    let clock = Date.now();
    vi.spyOn(Date, "now").mockImplementation(() => clock);
    onTestFinished(() => {
      vi.restoreAllMocks();
    });
    let rerender: (ui: ReactElement) => void = () => {};
    ({ rerender } = show(view, async () => rerender(page({ ...view, items: items.slice(1) })), "approval"));
    await userEvent.click(screen.getByRole("button", { name: "Allow this one call" }));
    await waitFor(() => expect(document.activeElement?.closest("[data-card]")?.getAttribute("data-card")).toBe("approval:approval-2"));
    const next = document.activeElement as HTMLElement;
    await userEvent.click(next);
    expect(posts(sent)).toHaveLength(1);
    clock += 400;
    next.focus();
    await userEvent.keyboard("{Enter>3/}");
    await waitFor(() => expect(posts(sent)).toHaveLength(2));
    expect(posts(sent)[1]?.body).toMatchObject({ digest: "digest-approval-2", allow: true });
  });

  it("moves to the next item and leaves focus where a person types outside the page", async () => {
    const { view, items } = requests([
      ["lane-a", "Bash"],
      ["lane-b", "Read"],
    ]);
    serveApi(() => ({ status: 200 }));
    const elsewhere = document.body.appendChild(document.createElement("textarea"));
    try {
      let rerender: (ui: ReactElement) => void = () => {};
      ({ rerender } = show(
        view,
        async () => {
          elsewhere.focus();
          rerender(page({ ...view, items: items.slice(1) }));
        },
        "approval",
      ));
      await userEvent.click(screen.getByRole("button", { name: "Allow this one call" }));
      await waitFor(() => expect(document.querySelector("[data-said]")?.textContent).toBe("Approved: Allow Bash. Next: Allow Read?"));
      await new Promise((settle) => requestAnimationFrame(settle));
      expect(document.activeElement).toBe(elsewhere);
    } finally {
      elsewhere.remove();
    }
  });

  it("says nothing else needs you once the last item is decided, and links what happens next", async () => {
    const { view } = requests([["lane-a", "Bash"]]);
    serveApi(() => ({ status: 200 }));
    let rerender: (ui: ReactElement) => void = () => {};
    ({ rerender } = show(view, async () => rerender(page({ ...view, items: [] })), "approval"));
    await userEvent.click(screen.getByRole("button", { name: "Allow this one call" }));
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Nothing else needs you"));
    expect(document.activeElement?.textContent).toContain("Lane lane-a goes on.");
    expect(document.activeElement?.textContent).not.toContain("Press J");
    expect(screen.getByRole("link", { name: "Watch it on Floor" }).getAttribute("href")).toContain("/floor");
  });

  it("leaves a decided item in place when a person turned advancing off", async () => {
    localStorage.setItem("ropeshift-advance", "off");
    const { view, items } = requests([
      ["lane-a", "Bash"],
      ["lane-b", "Read"],
    ]);
    serveApi(() => ({ status: 200 }));
    let rerender: (ui: ReactElement) => void = () => {};
    ({ rerender } = show(view, async () => rerender(page({ ...view, items: items.slice(1) })), "approval"));
    expect((screen.getByRole("checkbox", { name: "Advance after each decision" }) as HTMLInputElement).checked).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Allow this one call" }));
    const note = await screen.findByText(/no longer waits/);
    expect(note.textContent).toContain("you decided it");
    expect(note.textContent).toContain("Press J for the next item.");
  });

  it("groups a lane's requests under the lane, and approves them all, each bound to its own digest", async () => {
    const { view } = requests([
      ["L-88", "npm install"],
      ["L-90", "Read"],
      ["L-88", "npx vitest run"],
      ["L-88", "curl localhost"],
    ]);
    const sent = serveApi((call) => (call.path === "/v1/approvals/approval-3/decide" ? refusal(409, "digest_mismatch", "the call changed since it was read") : { status: 200 }));
    const after = vi.fn(async () => {});
    show(view, after);
    const group = screen.getByRole("list", { name: "Lane L-88" });
    expect(within(group).getAllByRole("link").map((link) => link.querySelector(".item-title")?.textContent)).toEqual(["Allow npm install?", "Allow npx vitest run?", "Allow curl localhost?"]);
    expect(titlesOf()).toEqual(["Allow npm install?", "Allow npx vitest run?", "Allow curl localhost?", "Allow Read?"]);
    await userEvent.click(screen.getByRole("button", { name: "Approve all 3" }));
    await waitFor(() => expect(posts(sent)).toHaveLength(3));
    expect(posts(sent).map((call) => [call.path, (call.body as { digest: string }).digest])).toEqual([
      ["/v1/approvals/approval-1/decide", "digest-approval-1"],
      ["/v1/approvals/approval-3/decide", "digest-approval-3"],
      ["/v1/approvals/approval-4/decide", "digest-approval-4"],
    ]);
    expect((await screen.findByText(/One request was skipped/)).textContent).toContain("npx vitest run: the call changed since it was read");
    expect(after).toHaveBeenCalled();
  });

  it("leaves a gate out of Approve all, and says how many wait to be reviewed one by one", async () => {
    const { view, items } = requests([
      ["L-88", "npm install"],
      ["L-88", "git push origin feature"],
      ["L-88", "npx vitest run"],
      ["L-88", "git tag v1"],
    ]);
    for (const gated of [items[1], items[3]]) {
      if (gated) {
        gated.approval.gate = true;
      }
    }
    const sent = serveApi(() => ({ status: 200 }));
    show(view);
    expect(screen.getByText(/2 gates wait to be reviewed one by one/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Approve all 2" }));
    await waitFor(() => expect(posts(sent)).toHaveLength(2));
    expect(posts(sent).map((call) => call.path)).toEqual(["/v1/approvals/approval-1/decide", "/v1/approvals/approval-3/decide"]);
  });

  it("offers no Approve all when what the lane asks is gates and one other request", async () => {
    const { view, items } = requests([
      ["L-88", "npm install"],
      ["L-88", "git push origin feature"],
    ]);
    const push = items[1];
    if (push) {
      push.approval.gate = true;
    }
    show(view);
    expect(screen.getByRole("list", { name: "Lane L-88" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Approve all/ })).toBeNull();
    expect(screen.getByText(/1 gate waits to be reviewed one by one/)).toBeTruthy();
  });

  it("lists each gate Approve all leaves out by what it asks, above Approve all", async () => {
    const { view, items } = requests([
      ["L-88", "npm install"],
      ["L-88", "git push origin feature"],
      ["L-88", "npx vitest run"],
      ["L-88", "git tag v1"],
    ]);
    for (const gated of [items[1], items[3]]) {
      if (gated) {
        gated.approval.gate = true;
      }
    }
    show(view);
    const gates = screen.getByRole("list", { name: /^2 gates wait to be reviewed one by one/ });
    expect(within(gates).getAllByRole("listitem").map((row) => row.textContent)).toEqual(["Allow git push origin feature?", "Allow git tag v1?"]);
    const approveAll = screen.getByRole("button", { name: "Approve all 2" });
    expect(gates.compareDocumentPosition(approveAll) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("says on a gate's card that it is approved one call at a time", async () => {
    const { view, items } = requests([["L-88", "git push origin feature"]]);
    const push = items[0];
    if (push) {
      push.approval.gate = true;
    }
    show(view, undefined, "approval");
    expect(within(screen.getByRole("article")).getByText(/^Gate:/).textContent).toBe(
      "Gate: the lane's profile gates this call, or it is a push, so it asks in every mode and is approved one call at a time, never with Approve all.",
    );
  });

  it("keeps grouping off in the address once a person turns it off", async () => {
    const { view } = requests([
      ["L-88", "npm install"],
      ["L-88", "npx vitest run"],
    ]);
    show(view);
    await userEvent.click(screen.getByRole("checkbox", { name: "Group by lane" }));
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("group")).toBe("off"));
    expect(screen.queryByRole("list", { name: "Lane L-88" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Approve all/ })).toBeNull();
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
  ])("%d ms reads %s", async (ms, label) => {
    expect(waitLabel(ms)).toBe(label);
  });

  it("reads in words", async () => {
    expect(waitWords(3_660_000)).toBe("waiting 1 hour 1 minute");
    expect(waitWords(90_000_000)).toBe("waiting 1 day 1 hour");
    expect(waitWords(1_000)).toBe("waiting since just now");
  });
});

describe("the title of a single question", () => {
  const longText =
    "Should the importer keep the original column names when the source file has duplicates, or rename each duplicate with a numeric suffix?";

  // oneQuestion is the golden view with its batch cut to one question holding the given text.
  const oneQuestion = (text: string) => {
    const view = goldenNeedsYou();
    const item = itemOf(view, "questions");
    const [first] = item.batch.items;
    if (!first) {
      throw new Error("empty batch");
    }
    first.text = text;
    item.batch.items = [first];
    return view;
  };

  const titleOfQuestion = () => {
    const link = screen.getAllByRole("link").find((element) => element.id.startsWith("need-questions:"));
    const title = link?.querySelector(".item-title");
    if (!title) {
      throw new Error("no question in the list");
    }
    return title;
  };

  it("is the question's own text in the list, not A question", () => {
    show(oneQuestion("Which store holds the index?"));
    expect(titleOfQuestion().textContent).toBe("Which store holds the index?");
    expect(screen.queryByText("A question")).toBeNull();
  });

  it("is cut at a word boundary when long, with the whole question in the title attribute and in the card", async () => {
    show(oneQuestion(longText), undefined, "questions");
    const title = titleOfQuestion();
    expect(title.textContent?.endsWith("…")).toBe(true);
    expect(title.textContent?.length).toBeLessThan(longText.length);
    expect(longText.startsWith(title.textContent?.slice(0, -1) ?? "?")).toBe(true);
    expect(title.getAttribute("title")).toBe(longText);
    expect(screen.getByRole("group", { name: longText })).toBeTruthy();
    expect(screen.getByRole("article", { name: title.textContent ?? "" })).toBeTruthy();
  });

  it("has no title attribute when nothing was cut", () => {
    show(oneQuestion("Which store holds the index?"));
    expect(titleOfQuestion().getAttribute("title")).toBeNull();
  });

  it("is A question when the text is empty", () => {
    show(oneQuestion("   "));
    expect(titleOfQuestion().textContent).toBe("A question");
  });

  it("is a count for several questions", () => {
    show(goldenNeedsYou());
    expect(titleOfQuestion().textContent).toBe("2 questions");
  });
});

type Posted = { answers: { question: string; id: string; accepted?: boolean; answer?: string; rationale?: string }[] };

// decided answers a batch request with the decisions it would record, as the daemon does.
const decided = (call: { body?: unknown }) => ({
  status: 201,
  body: { decisions: (call.body as Posted).answers.map((answer) => ({ id: answer.id, status: "decided", statement: answer.answer ?? "", decided_by: "member-ada" })) },
});

describe("a batch of questions", () => {
  // unfold shows a question's answers, which its card keeps behind Choose another while it leads with the
  // recommendation.
  const unfold = async (group: HTMLElement) => {
    const choose = within(group).queryByRole("button", { name: "Choose another" });
    if (choose && choose.getAttribute("aria-expanded") !== "true") {
      await userEvent.click(choose);
    }
  };
  // A batch that needs only agreement: both of the golden's questions have answers that can be taken back, which
  // the tests of the send read, and the one-way item the golden holds is the subject of the tests below them.
  const batchView = () => {
    const view = goldenNeedsYou();
    const item = itemOf(view, "questions");
    for (const question of item.batch.items) {
      question.reversibility = "reversible";
    }
    return { view, item };
  };

  it("needs one press when a member agrees with every recommendation", async () => {
    const { view, item } = batchView();
    const sent = serveApi(() => ({ status: 201 }));
    const after = vi.fn(async () => {});
    show(view, after, "questions");
    await userEvent.click(screen.getByRole("button", { name: "Agree with 2 recommendations" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: `/v1/batches/${item.batch.batch.id}/answers` });
    expect((sent[0]?.body as Posted).answers).toEqual(item.batch.items.map((question) => ({ question: question.id, id: `answer-${question.id}`, accepted: true, expect_reversibility: "reversible" })));
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
    show(view, after, "questions");
    const button = screen.getByRole("button", { name: "Agree with 2 recommendations" });
    await userEvent.dblClick(button);
    await userEvent.click(button);
    await waitFor(() => expect(sent).toHaveLength(1));
    release();
    expect(sent).toHaveLength(1);
  });

  it("repeats an answer under one ID, so a retry after a lost reply is one decision", async () => {
    const { view } = batchView();
    let calls = 0;
    const sent = serveApi(() => {
      calls += 1;
      return calls === 1 ? refusal(503, "unavailable", "The store is busy.") : { status: 201 };
    });
    show(view, undefined, "questions");
    await userEvent.click(screen.getByRole("button", { name: "Agree with 2 recommendations" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Agree with 2 recommendations" }));
    await waitFor(() => expect(sent.length).toBeGreaterThanOrEqual(2));
    expect((sent[0]?.body as Posted).answers.map((answer) => answer.id)).toEqual((sent[1]?.body as Posted).answers.map((answer) => answer.id));
  });

  it("says a batch the person answered here was answered, by whom, and with what, once it no longer waits", async () => {
    localStorage.setItem("ropeshift-advance", "off");
    const { view, item } = batchView();
    serveApi((call) => (call.method === "POST" ? decided(call) : undefined));
    const after = vi.fn(async () => {});
    const { rerender } = show(view, after, "questions");
    await userEvent.click(screen.getByRole("button", { name: "Agree with 2 recommendations" }));
    await waitFor(() => expect(after).toHaveBeenCalledOnce());
    rerender(
      <CurrentProject.Provider value={testProject}>
        <NeedsYou view={{ ...view, items: view.items.filter((row) => row.kind !== "questions") }} readAt={Date.now()} after={async () => {}} />
      </CurrentProject.Provider>,
    );
    const note = await screen.findByText(/no longer waits/);
    expect(note.textContent).toContain("you answered it, as member-ada");
    expect(note.textContent).not.toContain("another place");
    for (const question of item.batch.items) {
      expect(note.parentElement?.textContent).toContain(`${question.text}${question.recommendation}`);
    }
    const row = screen.getAllByRole("link").find((link) => link.getAttribute("aria-current") === "true");
    expect(row?.textContent).toContain("Answered by you");
    expect(row?.textContent).not.toContain("Resolved elsewhere");
  });

  it("lists each answer by its question, so two questions with the same text keep an answer each", async () => {
    const { view, item } = batchView();
    const text = item.batch.items[0]?.text ?? "";
    for (const question of item.batch.items) {
      question.text = text;
    }
    localStorage.setItem("ropeshift-advance", "off");
    const keys = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      serveApi((call) => (call.method === "POST" ? decided(call) : undefined));
      const after = vi.fn(async () => {});
      const { rerender } = show(view, after, "questions");
      await userEvent.click(screen.getByRole("button", { name: "Agree with 2 recommendations" }));
      await waitFor(() => expect(after).toHaveBeenCalledOnce());
      rerender(
        <CurrentProject.Provider value={testProject}>
          <NeedsYou view={{ ...view, items: view.items.filter((row) => row.kind !== "questions") }} readAt={Date.now()} after={async () => {}} />
        </CurrentProject.Provider>,
      );
      const answers = await screen.findByRole("list", { name: "Your answers" });
      expect(within(answers).getAllByRole("listitem")).toHaveLength(2);
      expect(keys.mock.calls.filter((call) => call.some((part) => String(part).includes("same key")))).toEqual([]);
    } finally {
      keys.mockRestore();
    }
  });

  it("says a batch nobody answered here was decided or resolved in another place", async () => {
    const { view } = batchView();
    const { rerender } = show(view, undefined, "questions");
    rerender(
      <CurrentProject.Provider value={testProject}>
        <NeedsYou view={{ ...view, items: view.items.filter((row) => row.kind !== "questions") }} readAt={Date.now()} after={async () => {}} />
      </CurrentProject.Provider>,
    );
    expect((await screen.findByText(/no longer waits/)).textContent).toContain("it was decided or resolved in another place");
    expect(screen.queryByText(/you answered it/i)).toBeNull();
  });

  it("refuses the whole batch when someone else answered a question first, and says which", async () => {
    const { view } = batchView();
    const after = vi.fn(async () => {});
    const sent = serveApi(() => refusal(409, "resolved", "answer 2, for question question-2: this question already has its answer"));
    show(view, after, "questions");
    await userEvent.click(screen.getByRole("button", { name: "Agree with 2 recommendations" }));
    expect((await screen.findByRole("alert")).textContent).toContain("answer 2, for question question-2");
    expect(sent).toHaveLength(1);
    expect(after).toHaveBeenCalled();
  });

  it("lets a member answer in their own words, with the reason for it", async () => {
    const { view, item } = batchView();
    const sent = serveApi(() => ({ status: 201 }));
    show(view, undefined, "questions");
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    const group = screen.getByRole("group", { name: first.text });
    await unfold(group);
    await userEvent.click(within(group).getByRole("radio", { name: "Something else" }));
    await userEvent.type(within(group).getByRole("textbox", { name: "Your answer" }), "Hold it for a day.");
    expect(screen.getByRole("button", { name: "Agree with 1 recommendation" })).toBeTruthy();
    await userEvent.type(within(group).getByRole("textbox", { name: "Why" }), "A day covers the longest outage.");
    await userEvent.click(screen.getByRole("button", { name: "Send 2 answers" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect((sent[0]?.body as Posted).answers[0]).toMatchObject({ question: first.id, answer: "Hold it for a day.", rationale: "A day covers the longest outage." });
    expect((sent[0]?.body as Posted).answers[0]).not.toHaveProperty("accepted");
  });

  it("leaves a one-way choice for a person to make, and says how many wait", async () => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    first.reversibility = "one_way";
    const sent = serveApi(() => ({ status: 201 }));
    show(view, undefined, "questions");
    const group = screen.getByRole("group", { name: first.text });
    await unfold(group);
    for (const radio of within(group).getAllByRole("radio")) {
      expect((radio as HTMLInputElement).checked).toBe(false);
    }
    expect(screen.getByRole("status").textContent).toBe("1 one-way question waits for your own choice, and is recorded on its own once you confirm it.");
    await userEvent.click(screen.getByRole("button", { name: "Agree with 1 recommendation" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect((sent[0]?.body as Posted).answers.map((answer) => answer.question)).not.toContain(first.id);
  });

  // The golden's batch holds a question whose category the decision policy keeps from an orchestrator, so its
  // answer is one-way though its reason says nothing of it.
  it("holds the golden's one-way question back from the card's send, and records it through its confirm once chosen", async () => {
    const view = goldenNeedsYou();
    const { batch } = itemOf(view, "questions");
    const oneWay = batch.items.find((question) => question.reversibility === "one_way");
    expect(oneWay?.escalation_reason).not.toBe("one_way");
    const sent = serveApi(() => ({ status: 201 }));
    show(view, undefined, "questions");
    expect(screen.getByRole("button", { name: "Agree with 1 recommendation" })).toBeTruthy();
    const group = screen.getByRole("group", { name: oneWay?.text });
    await unfold(group);
    await userEvent.click(within(group).getByRole("radio", { name: /^Keep the budget/ }));
    expect(screen.getByRole("button", { name: "Agree with 1 recommendation" })).toBeTruthy();
    await userEvent.click(within(group).getByRole("button", { name: "Agree with recommendation" }));
    expect(sent).toHaveLength(0);
    await userEvent.click(screen.getByRole("button", { name: "Record one-way decision" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect((sent[0]?.body as Posted).answers).toEqual([expect.objectContaining({ question: oneWay?.id, accepted: true, expect_reversibility: "one_way" })]);
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
  ])("counts only what a person sees chosen, given %s", async (_name, change: Partial<QuestionItem>) => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    Object.assign(first, change);
    show(view, undefined, "questions");
    for (const question of item.batch.items) {
      await unfold(screen.getByRole("group", { name: question.text }));
    }
    const counted = item.batch.items.filter((question) => countedAsShown(screen.getByRole("group", { name: question.text }), question)).length;
    const button = screen.getByRole("button", { name: /^Agree with \d+ recommendations?$/ });
    expect(button.textContent).toBe(counted === 1 ? "Agree with 1 recommendation" : `Agree with ${counted} recommendations`);
  });

  it("shows a recommendation that is no option as chosen, in the box for a person's own words", async () => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    first.recommendation = "Keep the budget, capped at three";
    const sent = serveApi(() => ({ status: 201 }));
    show(view, undefined, "questions");
    const group = screen.getByRole("group", { name: first.text });
    await unfold(group);
    const own = within(group).getByRole("radio", { name: /^Something else/ }) as HTMLInputElement;
    expect(own.checked).toBe(true);
    expect(own.closest("label")?.textContent).toContain("Recommended");
    expect((within(group).getByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement).value).toBe("Keep the budget, capped at three");
    await userEvent.click(screen.getByRole("button", { name: "Agree with 2 recommendations" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect((sent[0]?.body as Posted).answers[0]).toMatchObject({ question: first.id, accepted: true });
  });

  it("keeps a one-way choice unchosen, with a recommendation that is no option ready in the box", async () => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    first.reversibility = "one_way";
    first.recommendation = "Keep the budget, capped at three";
    show(view, undefined, "questions");
    const group = screen.getByRole("group", { name: first.text });
    await unfold(group);
    for (const radio of within(group).getAllByRole("radio")) {
      expect((radio as HTMLInputElement).checked).toBe(false);
    }
    expect(screen.getByRole("button", { name: "Agree with 1 recommendation" })).toBeTruthy();
    await userEvent.click(within(group).getByRole("radio", { name: /^Something else/ }));
    expect((within(group).getByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement).value).toBe("Keep the budget, capped at three");
    expect(screen.getByRole("button", { name: "Agree with 1 recommendation" })).toBeTruthy();
    expect(within(group).getByRole("button", { name: "Agree with recommendation" })).toBeTruthy();
  });

  it("says one answer in the singular", async () => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    first.reversibility = "reversible";
    first.escalation_reason = undefined;
    first.recommendation = undefined;
    item.batch.items = [first];
    show(view, undefined, "questions");
    expect(screen.getByRole("button", { name: "Send 0 of 1 answer" })).toBeTruthy();
  });

  it("offers a one-way question alone with no recommendation nothing to agree with, and its own send once it is chosen", async () => {
    const { view, item } = batchView();
    const first = item.batch.items[0];
    if (!first) {
      throw new Error("empty batch");
    }
    first.reversibility = "one_way";
    first.recommendation = undefined;
    item.batch.items = [first];
    show(view, undefined, "questions");
    expect(screen.queryByRole("button", { name: "Agree with recommendation" })).toBeNull();
    expect((screen.getByRole("button", { name: "Send this answer" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("marks what is recommended and what blocks work", async () => {
    const { view, item } = batchView();
    show(view, undefined, "questions");
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
    show(view, undefined, plainApproval);
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
    show(view, undefined, plainApproval);
    const card = await cardNamed(/^Allow /);
    await userEvent.type(within(card).getByRole("textbox", { name: /Reason/ }), "Tag it by hand.");
    await userEvent.click(within(card).getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toMatchObject({ allow: false, reason: "Tag it by hand." });
  });

  it("says so when the call changed since this read", async () => {
    const view = goldenNeedsYou();
    const after = vi.fn(async () => {});
    serveApi(() => refusal(409, "stale_digest", "This request no longer matches what you read."));
    show(view, after, plainApproval);
    await userEvent.click(screen.getByRole("button", { name: "Allow this one call" }));
    expect((await screen.findByRole("alert")).textContent).toContain("no longer matches");
    expect(after).toHaveBeenCalled();
  });

  it("shows what will run as text", async () => {
    const view = goldenNeedsYou();
    const { approval } = itemOf(view, "approval");
    show(view, undefined, plainApproval);
    expect(screen.getAllByText(approval.command ?? approval.summary).length).toBeGreaterThan(0);
  });
});

describe("a recovery request", () => {
  function recoveryView(): View {
    const view = goldenNeedsYou();
    const { approval, ...rest } = itemOf(view, "approval");
    const request = {
      ...approval,
      id: "recovery-1",
      kind: "recovery",
      lane_id: "",
      work_order_id: "wo-failed",
      subject: "work_retry",
      subject_digest: "sha256:subject",
      input: '{"reason":"the network came back","work_order":"wo-failed"}',
      command: "",
      summary: "work_retry on work order wo-failed",
      requested_by: "orchestrator:w1",
      detail: JSON.stringify({
        act: "work_retry",
        work_order_id: "wo-failed",
        reason: `the network came back ${hostile}`,
        check: "max_attempts",
        limit: "the work order used 1 of max_attempts 1",
        policy_version: 1,
      }),
    };
    return { ...view, items: [{ ...rest, id: request.id, approval: request }] };
  }

  it("says what the orchestrator asks, why, and the limit it went past, as text", async () => {
    show(recoveryView());
    const card = await cardNamed(/^Let the orchestrator retry wo-failed\?/);
    expect(within(card).getByText(`the network came back ${hostile}`)).toBeTruthy();
    expect(within(card).getByText("the work order used 1 of max_attempts 1")).toBeTruthy();
    expect(within(card).getByText("Recovery request")).toBeTruthy();
    expect(document.querySelector("img")).toBeNull();
  });

  it("runs the act on approval, and declines it with the digest it read", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show(recoveryView());
    await userEvent.click(screen.getByRole("button", { name: "Approve" }));
    await userEvent.click(screen.getByRole("button", { name: "Decline" }));
    await waitFor(() => expect(sent).toHaveLength(2));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/approvals/recovery-1/decide", body: { digest: "sha256:subject", allow: true } });
    expect(sent[1]?.body).toMatchObject({ digest: "sha256:subject", allow: false });
  });
});

describe("a merge request", () => {
  function mergeView(subject: "approve" | "held" = "approve", over: Partial<MergeDetail> = {}): View {
    const view = goldenNeedsYou();
    const request = mergeRequest(subject, over);
    return { ...view, items: [{ ...itemOf(view, "approval"), id: request.id, approval: request }] };
  }
  const approveCard = () => cardNamed(/^Approve the merge of wo-merge\?/);
  const heldCard = () => cardNamed(/^Retry the held merge of wo-merge\?/);

  it("is a card of its own, named for the merge, with none of a tool call's controls", async () => {
    show(mergeView());
    const card = await approveCard();
    expect(within(card).getByText("Merge approval")).toBeTruthy();
    expect(within(card).queryByText("Approval request")).toBeNull();
    expect(within(card).queryByRole("button", { name: "Allow this one call" })).toBeNull();
    expect(within(card).queryByText("Lane")).toBeNull();
    expect(within(card).queryByRole("textbox", { name: /Reason, which the lane reads/ })).toBeNull();
    const row = within(screen.getByRole("list", { name: "Waiting items" })).getByRole("link");
    expect(row.textContent).toContain("Approve the merge of wo-merge?");
    expect(row.textContent).toContain("wo-merge · harbor-web");
  });

  // A request names the work by its title, and its ID stays in the line under it and in the card's facts.
  it("is named by the work order's title, and still names the work order's ID", async () => {
    show(mergeView("approve", { title: "Add the retry budget" }));
    const card = await cardNamed(/^Approve the merge of Add the retry budget\?/);
    expect(within(card).getByRole("link", { name: "wo-merge" }).getAttribute("href")).toBe(here("/work-order?id=wo-merge"));
    const row = within(screen.getByRole("list", { name: "Waiting items" })).getByRole("link");
    expect(row.textContent).toContain("Approve the merge of Add the retry budget?");
    expect(row.textContent).toContain("wo-merge · harbor-web");
  });

  it("names a held merge by the work order's title too", async () => {
    show(mergeView("held", { title: "Add the retry budget" }));
    expect(await cardNamed(/^Retry the held merge of Add the retry budget\?/)).toBeTruthy();
  });

  it("names the repository, the branch it lands on, and the reviewed commit the approval is bound to", async () => {
    show(mergeView());
    const card = await approveCard();
    const facts = card.querySelector('dl[aria-label="The merge"]') as HTMLElement;
    const fact = (name: string) => within(facts).getByText(name, { selector: "dt" }).nextElementSibling as HTMLElement;
    expect(fact("Repository").textContent).toBe("harbor-web");
    expect(fact("Branch").textContent).toBe("ropeshift/wo-merge into main");
    expect(fact("Reviewed commit").textContent).toBe(reviewedCommit.slice(0, 12));
    expect(fact("Reviewed commit").querySelector("[title]")?.getAttribute("title")).toBe(reviewedCommit);
    expect(fact("Merge method").textContent).toBe("squash");
    expect(fact("Merge authority").textContent).toContain("person_approves");
    expect(within(facts).getByRole("link", { name: "wo-merge" }).getAttribute("href")).toBe(here("/work-order?id=wo-merge"));
    expect(card.querySelector(".binding")?.textContent).toContain(reviewedCommit.slice(0, 12));
  });

  it.each(["https://github.com/acme/harbor-web/pull/12", "javascript:alert(1)"])("names the pull request and the address %j a forge reported as text, and links nothing", async (url) => {
    show(mergeView("approve", { pull_request: 12, url }));
    const card = await approveCard();
    const facts = card.querySelector('dl[aria-label="The merge"]') as HTMLElement;
    expect(within(facts).getByText("Pull request", { selector: "dt" }).nextElementSibling?.textContent).toBe(`#12 · ${url}`);
    expect(within(card).queryByRole("link", { name: /#12|^https?:|^javascript:/ })).toBeNull();
    expect(card.querySelector('a[href^="javascript:"], a[href^="http"]')).toBeNull();
  });

  it("says what approving and denying do at person_approves", async () => {
    show(mergeView());
    const choices = within(await approveCard()).getByRole("region", { name: "What each choice does" });
    const approve = within(choices).getByText("Approve merge", { selector: "dt" }).nextElementSibling?.textContent ?? "";
    expect(approve).toContain("9f8e7d6c5b4a");
    expect(approve).toContain("main");
    expect(approve).toContain("squash");
    const deny = within(choices).getByText("Deny", { selector: "dt" }).nextElementSibling?.textContent ?? "";
    expect(deny).toContain("back to rework");
    expect(deny).toContain("your reason");
  });

  it("says a held merge stopped and why, that approving tries it again, and that denying sends the work back", async () => {
    show(mergeView("held"));
    const card = await heldCard();
    expect(within(card).getByText("Held merge")).toBeTruthy();
    expect(within(card).getByText("Why it stopped").nextElementSibling?.textContent).toBe("the base branch requires 2 approving reviews");
    const choices = within(card).getByRole("region", { name: "What each choice does" });
    expect(within(choices).getByText("Retry merge", { selector: "dt" }).nextElementSibling?.textContent).toContain("Tries the merge again");
    expect(within(choices).getByText("Deny", { selector: "dt" }).nextElementSibling?.textContent).toContain("back to rework");
    expect(within(card).queryByRole("button", { name: "Approve merge" })).toBeNull();
    expect(within(card).getByRole("button", { name: "Retry merge" })).toBeTruthy();
  });

  it("approves the commit it read, and no other", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show(mergeView());
    await userEvent.click(within(await approveCard()).getByRole("button", { name: "Approve merge" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/approvals/merge-approve/decide", body: { digest: reviewedCommit, allow: true, reason: "" } });
  });

  it("tries a held merge again on the commit it read", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show(mergeView("held"));
    await userEvent.click(within(await heldCard()).getByRole("button", { name: "Retry merge" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/approvals/merge-held/decide", body: { digest: reviewedCommit, allow: true } });
  });

  it("denies with the reason the work's next attempt reads", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show(mergeView());
    const card = await approveCard();
    await userEvent.type(within(card).getByRole("textbox", { name: /Reason/ }), "Split the migration first.");
    await userEvent.click(within(card).getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/approvals/merge-approve/decide", body: { digest: reviewedCommit, allow: false, reason: "Split the migration first." } });
  });

  it("says so when the reviewed commit changed since this read", async () => {
    const after = vi.fn(async () => {});
    serveApi(() => refusal(409, "stale_digest", "This request no longer matches what you read."));
    show(mergeView(), after);
    await userEvent.click(within(await approveCard()).getByRole("button", { name: "Approve merge" }));
    expect((await screen.findByRole("alert")).textContent).toContain("no longer matches");
    expect(after).toHaveBeenCalled();
  });

  it("reads a request raised before the repository was recorded, and keeps what a forge said as text", async () => {
    show(mergeView("held", { repository: undefined, reason: `refused ${hostile}`, branch: `feature/${hostile}` }));
    const card = await heldCard();
    expect(within(card).queryByText("Repository")).toBeNull();
    expect(within(card).getByText(`refused ${hostile}`)).toBeTruthy();
    expect(document.querySelector("img")).toBeNull();
  });
});

describe("an unknown effect", () => {
  it("offers what the server offers, and sends what a person says", async () => {
    const view = goldenNeedsYou();
    const { effect } = itemOf(view, "effect");
    const sent = serveApi(() => ({ status: 200 }));
    show(view, undefined, "effect");
    const card = await cardNamed(/Did it happen/);
    expect(within(card).getByText(effect.key)).toBeTruthy();
    await userEvent.click(within(card).getByRole("button", { name: "Run it again" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ path: `/v1/effects/${encodeURIComponent(effect.id)}/decide`, body: { outcome: "retry" } });
  });

  it("warns that a retry may repeat it", async () => {
    show(undefined, undefined, "effect");
    expect(within(await cardNamed(/Did it happen/)).getByText(/may repeat it/)).toBeTruthy();
  });

  it("drops what the server no longer offers", async () => {
    const view = goldenNeedsYou();
    itemOf(view, "effect").effect.offers = ["happened", "not_happened"];
    show(view, undefined, "effect");
    const card = await cardNamed(/Did it happen/);
    expect(within(card).queryByRole("button", { name: "Run it again" })).toBeNull();
    expect(within(card).queryByText(/may repeat it/)).toBeNull();
    expect(within(card).getByRole("button", { name: "It did not happen" })).toBeTruthy();
  });
});

describe("waiting work", () => {
  it("runs an action that needs no words in one press", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show();
    await userEvent.click(within(await cardNamed(/Fix the flaky test/)).getByRole("button", { name: "Reset the breaker" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-tripped/lane-reset" });
  });

  it("asks why before an action that changes who owns work", async () => {
    const sent = serveApi(() => ({ status: 204 }));
    show();
    const card = await cardNamed(/Rotate the keys/);
    await userEvent.click(within(card).getByRole("button", { name: "End this claim" }));
    expect(sent).toHaveLength(0);
    const confirm = within(card).getByRole("button", { name: "End this claim" });
    expect((confirm as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(within(card).getByRole("textbox", { name: "Why (required)" }), "The worker is gone.");
    await userEvent.click(confirm);
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ path: "/v1/work-orders/wo-lapsed/reassign", body: { reason: "The worker is gone." } });
  });

  it("cancels work only once a person says why", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show();
    const card = await cardNamed(/Move the archive/);
    await userEvent.click(within(card).getByRole("button", { name: "Cancel this work" }));
    expect(sent).toHaveLength(0);
    await userEvent.type(within(card).getByRole("textbox", { name: "Why (required)" }), "The plan changed.");
    await userEvent.click(within(card).getByRole("button", { name: "Cancel this work" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-blocked/cancel", body: { reason: "The plan changed." } });
  });

  // Failed work stays failed until a member retries it, so it waits here from the time it failed. The domain
  // refuses to cancel work that has failed, so the card offers a retry and nothing the server would refuse.
  it("asks a member to retry failed work, and names a retry that adds an attempt", async () => {
    const sent = serveApi(() => ({ status: 200, body: { work_order: "wo-failed", state: "ready" } }));
    show();
    const card = await cardNamed(/Try the impossible/);
    expect(within(card).getAllByText("Failed").length).toBeGreaterThan(0);
    expect(within(card).getByText("Its lane failed, and nothing retries failed work on its own.")).toBeTruthy();
    expect(within(card).getByText("It used every attempt it was given, so a retry adds one, which only a member can do.")).toBeTruthy();
    expect(within(card).getByText(/^It gave up on <img src=x onerror=alert\(1\)>/)).toBeTruthy();
    expect(within(card).queryByRole("button", { name: "Cancel this work" })).toBeNull();
    expect(within(card).getByRole("link", { name: "Open work order wo-failed" }).getAttribute("href")).toContain("/work-order?id=wo-failed");
    await userEvent.click(within(card).getByRole("button", { name: "Retry with an extra attempt" }));
    expect(sent).toHaveLength(0);
    // The note is the member's own and starts empty. What the lane recorded stands above it, quoted as the lane's.
    const why = within(card).getByRole("textbox", { name: "Why (optional)" }) as HTMLTextAreaElement;
    expect(why.value).toBe("");
    const quoted = within(card).getByRole("figure", { name: "The lane recorded" });
    expect(quoted.textContent).toContain("It gave up on <img src=x onerror=alert(1)><script>alert(2)</script>");
    const confirm = within(card).getByRole("button", { name: "Retry with an extra attempt" });
    expect((confirm as HTMLButtonElement).disabled).toBe(false);
    await userEvent.type(why, "The network came back.");
    await userEvent.click(confirm);
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-failed/retry", body: { reason: "The network came back." } });
  });

  // An empty note sends the member's fixed words, and the server keeps the failed lane with the retry, so what the lane
  // wrote never reaches the request as the member's words. The attack fixture stays text.
  it("retries failed work with an empty note as the member's fixed words, never the lane's", async () => {
    const sent = serveApi(() => ({ status: 200, body: { work_order: "wo-failed", state: "ready" } }));
    show();
    const card = await cardNamed(/Try the impossible/);
    await userEvent.click(within(card).getByRole("button", { name: "Retry with an extra attempt" }));
    expect(card.querySelector("img, script")).toBeNull();
    expect(within(card).getByText("Leave it empty and the retry says: Retried after the recorded failure")).toBeTruthy();
    await userEvent.click(within(card).getByRole("button", { name: "Retry with an extra attempt" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ path: "/v1/work-orders/wo-failed/retry", body: { reason: "Retried after the recorded failure" } });
    expect(JSON.stringify(sent[0]?.body)).not.toContain("It gave up on");
  });

  // After a retry, what the plans view says the work allows is what the page offers: Start with the hold that keeps
  // the work from a lane, or a link to the work order when the work is not claimable.
  describe("after a retry", () => {
    const failed = (row: NeedItem) => row.kind === "work" && row.id === "wo-failed";

    // retried retries wo-failed, and answers the plans view with the work as the change leaves it.
    async function retried(change: (order: WorkOrder, allowed: { actions: string[] }) => void) {
      const view = goldenNeedsYou();
      const plans = golden<Plans>("plans");
      const order = plans.plans.flatMap((plan) => plan.work_orders).find((row) => row.id === "wo-failed") as WorkOrder;
      const allowed = { actions: ["retry"] };
      change(order, allowed);
      plans.work_actions["wo-failed"] = allowed as Plans["work_actions"][string];
      const without = (
        <CurrentProject.Provider value={testProject}>
          <NeedsYou view={{ ...view, items: view.items.filter((row) => !failed(row)) }} readAt={Date.now()} after={async () => {}} />
        </CurrentProject.Provider>
      );
      let rerender: ReturnType<typeof show>["rerender"] = () => {};
      ({ rerender } = show(view, async () => rerender(without), failed));
      const sent = serveApi((call) => {
        if (call.method === "POST") {
          return { status: 200, body: { work_order: "wo-failed", state: "ready" } };
        }
        return call.path.startsWith("/v1/views/plans") ? { status: 200, body: plans } : undefined;
      });
      await userEvent.click(await screen.findByRole("button", { name: "Retry with an extra attempt" }));
      await userEvent.type(screen.getByRole("textbox", { name: "Why (optional)" }), "The network came back.");
      await userEvent.click(screen.getByRole("button", { name: "Retry with an extra attempt" }));
      await waitFor(() => expect(sent.some((call) => call.path === "/v1/work-orders/wo-failed/retry")).toBe(true));
      return screen.findByText(/no longer waits/);
    }

    it("says the work is ready and no lane has started, and offers Start with the hold the ledger records", async () => {
      const note = await retried((order, allowed) => {
        order.state = "ready";
        order.start_held = { reason: "no_checkout" };
        allowed.actions = ["claim", "cancel"];
      });
      expect(note.textContent).toContain("you retried it");
      expect(note.textContent).toContain("no lane has started");
      expect(await screen.findByText("Held: No clone on the worker")).toBeTruthy();
      await userEvent.click(screen.getByRole("button", { name: "Start" }));
      expect((await screen.findByRole("dialog")).textContent).toContain("Start Try the impossible");
    });

    it("offers no Start for work the plans view does not allow a claim of, and leads to the work order", async () => {
      await retried(() => {});
      expect(await screen.findByRole("link", { name: "Open work order wo-failed" })).toBeTruthy();
      expect(screen.queryByRole("button", { name: "Start" })).toBeNull();
    });
  });

  it("names a plain retry when the failed work has an attempt left", async () => {
    const view = goldenNeedsYou();
    for (const item of view.items) {
      if (item.kind === "work" && item.id === "wo-failed") {
        delete item.work.extra_attempt;
      }
    }
    show(view);
    const card = await cardNamed(/Try the impossible/);
    expect(within(card).getByRole("button", { name: "Retry" })).toBeTruthy();
    expect(within(card).queryByText(/used every attempt/)).toBeNull();
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

  // The worker holds the claim of a lane it is too old to read, and renews it, so no lease lapses and nothing else
  // says the lane never starts. The card names the worker and offers only what a member can do: cancel the work.
  it("says a claim waits for its worker to be upgraded, and names the worker", async () => {
    const view = goldenNeedsYou();
    const detail = 'the worker "atlas" (worker-atlas) must be upgraded to start this lane: it runs protocol 3, and this lane needs protocol 4';
    view.items = [
      {
        kind: "work",
        id: "wo-behind",
        since: view.as_of,
        work: { reason: "worker_behind", detail, work_order: { id: "wo-behind", kind: "change", title: "Add the retry budget", state: "claimed" }, actions: ["cancel"] },
      },
    ];
    show(view);
    const card = await cardNamed(/Add the retry budget/);
    expect(within(card).getByText("Waiting for upgrade")).toBeTruthy();
    expect(within(card).getByText("Its worker must be upgraded before it can start its lane.")).toBeTruthy();
    expect(within(card).getByText(detail)).toBeTruthy();
    expect(within(card).getByRole("button", { name: "Cancel this work" })).toBeTruthy();
    expect(within(card).queryByRole("button", { name: "End this claim" })).toBeNull();
  });

  // A dialog no request covers holds its call open, so the lane never reads stalled, and no key of the worker's answers
  // it. The card says so, offers nothing to press, and leads to the lane's pane, where a person answers it.
  it("says a lane waits in its pane, and leads to the pane", async () => {
    const view = goldenNeedsYou();
    const stalled = view.items.find((item) => item.kind === "work" && item.id === "wo-stalled") as Extract<NeedItem, { kind: "work" }>;
    const lane = { ...stalled.work.lane!, conditions: ["waiting_external", "waiting_in_pane"] };
    view.items = [
      {
        kind: "work",
        id: "wo-dialog",
        since: view.as_of,
        work: { reason: "waiting_in_pane", lane, work_order: { id: "wo-dialog", kind: "change", title: "Read the configuration", state: "running" }, actions: [] },
      },
    ];
    show(view);
    const card = await cardNamed(/Read the configuration/);
    expect(within(card).getByText("Waiting in its pane")).toBeTruthy();
    expect(within(card).getByText(/only a person can answer, such as a dialog/)).toBeTruthy();
    expect(within(card).getByRole("link", { name: "Open its pane" }).getAttribute("href")).toContain(`/lane?id=${lane.id}`);
    expect(within(card).getByText("Waiting for you in its pane")).toBeTruthy();
    expect(within(card).queryAllByRole("button")).toHaveLength(0);
  });

  it("says an orchestrator waits in its pane, and offers no start that would replace it", async () => {
    const view = goldenNeedsYou();
    const orchestrator = { id: "orchestrator-worker-a", worker_id: "worker-a", desired_state: "running", run: 1, resets: 0, actual_state: "working", conditions: ["waiting_in_pane"] };
    view.items = [{ kind: "work", id: orchestrator.id, since: view.as_of, work: { reason: "waiting_in_pane", orchestrator, actions: [] } }];
    show(view);
    const card = screen.getByRole("article");
    expect(within(card).getByText(/Talk to the orchestrator to answer it in its pane/)).toBeTruthy();
    expect(within(card).queryAllByRole("button")).toHaveLength(0);
  });

  // A merge outside ropeshift dropped the design history an adoption imported. The card names the branch and the
  // missing commit, and offers nothing to press: merging the commit into the base clears it at the next check.
  it("says a merged adoption lost the design history it imported, naming the branch and the missing commit", async () => {
    const view = goldenNeedsYou();
    const detail = "Branch lane/adopt merged into main, and main at base-squashed does not contain design-tip, the design repository's tip that the adoption imported.";
    view.items = [
      {
        kind: "work",
        id: "adopt-1",
        since: view.as_of,
        work: { reason: "history_missing", detail, work_order: { id: "adopt-1", kind: "plan", title: "Adopt shop", state: "done" }, actions: [] },
      },
    ];
    show(view);
    const card = await cardNamed(/Adopt shop/);
    expect(within(card).getByText("Design history missing")).toBeTruthy();
    expect(within(card).getByText("Its adoption branch merged without the design history it imported.")).toBeTruthy();
    expect(within(card).getByText(detail)).toBeTruthy();
    expect(within(card).queryAllByRole("button")).toHaveLength(0);
  });

  it("says a merged adoption's base could not be checked for the design history, and why", async () => {
    const view = goldenNeedsYou();
    const detail = "Branch lane/adopt merged into main, and ropeshift could not check that main contains the design history the adoption imported: the plan approval names no design tip.";
    view.items = [
      {
        kind: "work",
        id: "adopt-1",
        since: view.as_of,
        work: { reason: "history_unread", detail, work_order: { id: "adopt-1", kind: "plan", title: "Adopt shop", state: "done" }, actions: [] },
      },
    ];
    show(view);
    const card = await cardNamed(/Adopt shop/);
    expect(within(card).getByText("Design history unchecked")).toBeTruthy();
    expect(within(card).getByText("Its adoption branch merged, and its base could not be checked for the design history it imported.")).toBeTruthy();
    expect(within(card).getByText(detail)).toBeTruthy();
    expect(within(card).queryAllByRole("button")).toHaveLength(0);
  });

  it("names the question a parked lane waits for, and keeps resume as the way past it", async () => {
    const sent = serveApi(() => ({ status: 200 }));
    show();
    const card = await cardNamed(/Wait for the check/);
    const lead = within(card).getByText(/^Its lane is parked/);
    expect(lead.textContent).toBe("Its lane is parked until question question-1 is answered, or until a person resumes it.");
    expect(within(lead).getByRole("link", { name: "question-1" }).getAttribute("href")).toBe(here("/questions?id=question-1"));
    expect(within(card).getByText("Resume lets it go on now, without waiting.")).toBeTruthy();
    await userEvent.click(within(card).getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-parked/lane-resume" });
  });

  it("says a lane only a person can move waits for a person", async () => {
    const view = goldenNeedsYou();
    const parked = view.items.find((item) => item.kind === "work" && item.work.reason === "human_resume");
    if (parked?.kind !== "work" || !parked.work.wakes?.[0]) {
      throw new Error("no parked lane in the golden view");
    }
    parked.work.wakes[0].expression = `{"human_resume":true}`;
    show(view);
    const card = await cardNamed(/Wait for the check/);
    expect(within(card).getByText("Its lane is parked until a person says go.")).toBeTruthy();
    expect(within(card).queryByText(/without waiting/)).toBeNull();
  });

  it("shows the review's verdict for each criterion, in words, with its reasons and citations", async () => {
    show();
    const card = await cardNamed(/Trim the log/);
    const review = within(card).getByRole("region", { name: "What the review found" });
    expect(within(review).getByText("c1")).toBeTruthy();
    expect(within(review).getByText("Cannot judge")).toBeTruthy();
    expect(within(review).getByText(/nothing I can run shows it keeps the last day/)).toBeTruthy();
    expect(within(review).getByText("trim.go:30")).toBeTruthy();
    expect(within(review).getByText("FR-LOG-2")).toBeTruthy();
    const notes = within(review).getByRole("list", { name: "Security notes" });
    expect(within(notes).getByText("Injection")).toBeTruthy();
    expect(within(notes).getByText("Dependencies")).toBeTruthy();
    expect(within(notes).getByText("go.mod adds a module at a branch and not at a version.")).toBeTruthy();
    expect(within(card).getByText(/the review could not judge criterion c1/)).toBeTruthy();
    // The person still decides, and the card still offers both.
    expect(within(card).getByRole("button", { name: "Accept" })).toBeTruthy();
    expect(within(card).getByRole("button", { name: "Send back for rework" })).toBeTruthy();
  });

  // Ropeshift sends an unfinished plan back once itself. When the next version is still unfinished, the card names what is
  // missing and leads with Send back, which offers that list as its reason, so a member is called only when needed.
  it("lists what an unfinished plan lacks, leads with Send back, and prefills its reason", async () => {
    const sent = serveApi(() => ({ status: 200, body: { work_order: "wo-trim", state: "ready" } }));
    const view = goldenNeedsYou();
    const accept = view.items.find((item) => item.kind === "work" && item.work.reason === "acceptance");
    if (accept?.kind !== "work") {
      throw new Error("no acceptance item in the golden view");
    }
    const lines = ["The plan's title is still the template's, \"State the plan in one line\".", "The goal's text is still the template's, which says why the goal matters."];
    accept.work.unfinished = lines;
    accept.work.send_back_reason = `The plan is not finished:\n- ${lines.join("\n- ")}`;
    show(view);
    const card = await cardNamed(/Trim the log/);
    expect(within(card).getByText(/Ropeshift did not offer this plan for approval/)).toBeTruthy();
    for (const line of lines) {
      expect(within(card).getByText(line)).toBeTruthy();
    }
    const buttons = within(card).getAllByRole("button");
    expect(buttons[buttons.length - 1]?.textContent).toBe("Send back for rework");
    expect(buttons[buttons.length - 1]?.className).toContain("btn-primary");
    expect(within(card).getByRole("button", { name: "Accept" }).className).not.toContain("btn-primary");
    await userEvent.click(within(card).getByRole("button", { name: "Send back for rework" }));
    const why = within(card).getByRole("textbox", { name: "Why (required)" }) as HTMLTextAreaElement;
    expect(why.value).toBe(accept.work.send_back_reason);
    await userEvent.click(within(card).getByRole("button", { name: "Send back for rework" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", body: { reason: accept.work.send_back_reason } });
  });

  it("leads with Accept on finished work whose plan nothing held back", async () => {
    show();
    const card = await cardNamed(/Trim the log/);
    const buttons = within(card).getAllByRole("button");
    expect(buttons[buttons.length - 1]?.textContent).toBe("Accept");
  });

  it("says a verdict in text for every kind, never by colour alone", async () => {
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
    delete accept.work.standing.notes;
    show(view);
    const review = within(await cardNamed(/Trim the log/)).getByRole("region", { name: "What the review found" });
    for (const word of ["Pass", "Fail", "Cannot judge"]) {
      expect(within(review).getByText(word)).toBeTruthy();
    }
    expect(within(review).getAllByRole("listitem")).toHaveLength(3 + 1);
    expect(within(review).getByText("nothing is logged")).toBeTruthy();
  });

  it("keeps a review's words from becoming markup", async () => {
    const view = goldenNeedsYou();
    const accept = view.items.find((item) => item.kind === "work" && item.work.reason === "acceptance");
    const verdict = accept?.kind === "work" ? accept.work.standing?.verdicts?.[0] : undefined;
    if (!verdict) {
      throw new Error("no verdict in the golden view");
    }
    verdict.reasons = hostile;
    verdict.citations = [hostile];
    show(view);
    const card = await cardNamed(/Trim the log/);
    expect(card.querySelector("img")).toBeNull();
    expect(card.querySelector("script")).toBeNull();
    expect(within(card).getAllByText(hostile).length).toBeGreaterThan(0);
  });

  it("says a review that failed or is still open gave no verdict, and that the person decides without it", async () => {
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
      expect(within(within(await cardNamed(/Trim the log/)).getByRole("region", { name: "What the review found" })).getByText(words)).toBeTruthy();
      unmount();
    }
  });

  it("says nothing of a review when work was never reviewed", async () => {
    const view = goldenNeedsYou();
    const accept = view.items.find((item) => item.kind === "work" && item.work.reason === "acceptance");
    if (accept?.kind !== "work" || !accept.work.standing) {
      throw new Error("no acceptance item in the golden view");
    }
    delete accept.work.standing.verdicts;
    delete accept.work.standing.notes;
    delete accept.work.standing.why;
    show(view);
    expect(within(await cardNamed(/Trim the log/)).queryByRole("region", { name: "What the review found" })).toBeNull();
  });

  it("offers both ways out of blocked", async () => {
    show();
    const card = await cardNamed(/Move the archive/);
    expect(within(card).getByRole("button", { name: "Back to ready" })).toBeTruthy();
    expect(within(card).getByRole("button", { name: "Back to planning" })).toBeTruthy();
    expect(within(card).getByText("no_outcome")).toBeTruthy();
  });

  it("keeps each ID on one line, whole in its title", async () => {
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
      const shown = within(await cardNamed(card)).getAllByText(id);
      for (const element of shown) {
        expect(element.classList.contains("id"), id).toBe(true);
        expect(element.getAttribute("title")).toBe(id);
      }
    }
  });

  it("keeps a last reported value apart from an observed one", async () => {
    const view = goldenNeedsYou();
    show(view);
    const card = await cardNamed(/Rotate the keys/);
    expect(within(card).queryAllByText(/^Last reported/).length + within(card).queryAllByText(/^Observed/).length).toBeGreaterThan(0);
  });
});

describe("a plan approval", () => {
  const planView = () => {
    const view = goldenNeedsYou();
    return { view, item: itemOf(view, "plan_approval") };
  };
  const card = () => cardNamed(/^Approve The factory floor/);

  it("names the plan, its version, who asked, and what it holds", async () => {
    const { view, item } = planView();
    show(view, undefined, "plan_approval");
    const shown = (await card());
    expect(within(shown).getByRole("heading", { level: 2 }).textContent).toBe("Approve The factory floor, version 6?");
    expect(within(shown).getByText("system:intake")).toBeTruthy();
    const holds = within(shown).getByRole("list", { name: "What it holds" });
    expect(holds.textContent).toContain("4 contracts");
    expect(holds.textContent).toContain("3 milestones");
    expect(holds.textContent).toContain("1 spec");
    expect(holds.textContent).toContain("1 requirements document");
    expect(holds.textContent).toContain("1 architecture document");
    expect(within(shown).getByRole("link", { name: "Read the plan" }).getAttribute("href")).toBe(
      here(`/plans?plan=${encodeURIComponent(item.plan_approval.approval.plan_id ?? "")}`),
    );
  });

  it("shows how it differs from the version a member approved", async () => {
    show(undefined, undefined, "plan_approval");
    const difference = within((await card())).getByRole("region", { name: "Since version 5" });
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

  it("shows the decisions made since the approved version beside the difference, with the work that waits for them", async () => {
    const revised = golden<NeedItem>("needs-you-revised-approval");
    show({ ...goldenNeedsYou(), items: [revised] }, undefined, "plan_approval");
    const shown = await cardNamed(/^Approve Retry budget/);
    const since = within(shown).getByRole("list", { name: "Decisions since version 1" });
    expect(within(since).getAllByRole("listitem")).toHaveLength(1);
    expect(since.textContent).toContain("Use the wordmark.");
    expect(since.textContent).toContain("Reversible, binds plan:");
    expect(within(shown).getByText(/1 work order waits in contract review until a member confirms version 1 or approves a later one/)).toBeTruthy();
    expect(within(shown).getByRole("button", { name: "Approve this plan" })).toBeTruthy();
  });

  it("lists what a placeholder plan lacks and offers no Approve, because ropeshift refuses to approve it", async () => {
    const view = goldenNeedsYou();
    const item = view.items.find((row) => row.kind === "plan_approval");
    if (item?.kind !== "plan_approval") {
      throw new Error("no plan approval in the golden view");
    }
    item.plan_approval.unfinished = ["The plan's title is still the template's, \"State the plan in one line\"."];
    show(view, undefined, "plan_approval");
    const shown = await card();
    expect(within(shown).getByText(/Ropeshift did not offer this plan version for approval/)).toBeTruthy();
    expect(within(shown).getByText(/title is still the template's/)).toBeTruthy();
    expect(within(shown).queryByRole("button", { name: "Approve this plan" })).toBeNull();
    expect(within(shown).getByRole("button", { name: "Deny" })).toBeTruthy();
  });

  it("shows no decisions since when none moved past the approved version", async () => {
    show(undefined, undefined, "plan_approval");
    expect(within(await card()).queryByRole("list", { name: /^Decisions since version/ })).toBeNull();
  });

  it("says a first version has nothing to differ from", async () => {
    const { view, item: need } = planView();
    need.plan_approval.difference = { base: 0, added: ["plan:factory", "spec:factory-api"] };
    show(view, undefined, "plan_approval");
    const difference = within((await card())).getByRole("region", { name: "A first version" });
    expect(difference.textContent).toContain("Nothing was approved before");
    expect(within(difference).getByText(item("Added")).textContent).toContain("spec:factory-api");
  });

  it("says when a version changes nothing a member approved", async () => {
    const { view, item: need } = planView();
    need.plan_approval.difference = { base: 5 };
    show(view, undefined, "plan_approval");
    expect(within((await card())).getByText("No record differs from version 5.")).toBeTruthy();
  });

  it("lists each answer it drafts, which approving it decides", async () => {
    show(undefined, undefined, "plan_approval");
    const drafts = within((await card())).getByRole("list", { name: "Draft decisions" });
    const rows = within(drafts).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]?.textContent).toContain("Which agent CLI runs the first lanes?");
    expect(rows[0]?.textContent).toContain("The one already installed on the machine.");
    expect(rows[0]?.textContent).toContain("It needs no new sign-in.");
    expect(within((await card())).getByText(/decides each draft decision as yours/)).toBeTruthy();
  });

  it("lists a draft a decision outside the plan replaced, which approving does not decide again, as the ledger wrote it", async () => {
    show({ ...goldenNeedsYou(), items: [golden<NeedItem>("needs-you-replaced-draft-approval")] }, undefined, "plan_approval");
    const shown = await cardNamed(/^Approve Retry budget/);
    const row = within(within(shown).getByRole("list", { name: "Replaced draft decisions" })).getByRole("listitem");
    expect(row.textContent).toContain("d1");
    expect(row.textContent).toContain("replaced");
    expect(row.textContent).toContain("d-adyen");
    expect(row.textContent).toContain("Q: Which provider first? A: Adyen");
  });

  it("names a draft a decision outside the plan overturned, and says when none stands for it", async () => {
    const { view, item: need } = planView();
    need.plan_approval.replaced = [
      { draft: "d1", decision: "pd-1", status: "overturned", in_force: "d-wordmark", statement: "Q: Which logo set? A: Wordmark" },
      { draft: "d2", decision: "pd-2", status: "replaced" },
    ];
    show(view, undefined, "plan_approval");
    const rows = within(within(await card()).getByRole("list", { name: "Replaced draft decisions" })).getAllByRole("listitem");
    expect(rows[0]?.textContent).toContain("overturned");
    expect(rows[0]?.textContent).toContain("d-wordmark");
    expect(rows[1]?.textContent).toContain("No decision stands for it.");
  });

  it("lists no replaced draft when a plan has none", async () => {
    show(undefined, undefined, "plan_approval");
    expect(within(await card()).queryByRole("list", { name: "Replaced draft decisions" })).toBeNull();
  });

  it("approves with the digest this page read", async () => {
    const { view, item: need } = planView();
    const sent = serveApi(() => ({ status: 200 }));
    const after = vi.fn(async () => {});
    show(view, after, "plan_approval");
    await userEvent.click(within((await card())).getByRole("button", { name: "Approve this plan" }));
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
    show(undefined, undefined, "plan_approval");
    await userEvent.type(within((await card())).getByRole("textbox", { name: /Reason/ }), "Split the core first.");
    await userEvent.click(within((await card())).getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toMatchObject({ allow: false, reason: "Split the core first." });
  });

  it("says so when the plan changed since this read", async () => {
    serveApi(() => refusal(409, "stale_digest", "This request no longer matches what you read."));
    show(undefined, undefined, "plan_approval");
    await userEvent.click(within((await card())).getByRole("button", { name: "Approve this plan" }));
    expect((await within((await card())).findByRole("alert")).textContent).toContain("no longer matches");
  });

  // withManifest is a plan version that lists a repository manifest, and the plan the full page reads for it,
  // whose approval holds the registration a read of it previews.
  const withManifest = (registration: ManifestRegistration | undefined) => {
    const { view, item: need } = planView();
    need.plan_approval.counts = { ...need.plan_approval.counts, repositories: 1 };
    const planId = need.plan_approval.approval.plan_id ?? "";
    const plan = golden<PlanView>("plan");
    if (plan.approval) {
      plan.approval.registration = registration;
    }
    return { view, need, planId, plan };
  };
  const preview: ManifestRegistration = {
    preview: true,
    repositories: [
      { name: "billing", manifest_path: "../billing", path: "/work/billing", remote: "git@example.invalid:acme/billing.git", outcome: "registered" },
      { name: "ghost", manifest_path: "../ghost", path: "/work/ghost", outcome: "refused", reason: "ghost is not registered: no git clone is at /work/ghost" },
    ],
  };
  const approveButton = async () => within(await card()).getByRole("button", { name: "Approve this plan" }) as HTMLButtonElement;

  it("shows each repository its manifest lists, where its path leads, and what approving does, before it can approve", async () => {
    const { view, need, planId, plan } = withManifest(preview);
    const sent = serveApi((call) => {
      if (call.method === "GET" && call.path.startsWith(`/v1/plans/${planId}`)) {
        return { status: 200, body: plan };
      }
      return { status: 200 };
    });
    show(view, undefined, "plan_approval");
    // Nothing is approved on a preview nobody has read.
    expect((await approveButton()).disabled).toBe(true);
    const panel = await within(await card()).findByRole("region", { name: "Repositories approving registers" });
    const billing = within(panel).getByRole("listitem", { name: "billing" });
    expect(billing.textContent).toContain("Will register");
    expect(billing.textContent).toContain("../billing");
    expect(billing.textContent).toContain("/work/billing");
    expect(billing.textContent).toContain("git@example.invalid:acme/billing.git");
    expect(within(panel).getByRole("listitem", { name: "ghost" }).textContent).toContain("no git clone is at /work/ghost");
    expect(within(panel).getByText(/Reading it registers nothing/)).toBeTruthy();
    await waitFor(async () => expect((await approveButton()).disabled).toBe(false));
    expect(sent[0]).toMatchObject({ method: "GET", path: `/v1/plans/${planId}?version=${need.plan_approval.version}` });
    await userEvent.click(await approveButton());
    await waitFor(() => expect(sent.some((call) => call.method === "POST" && call.path === `/v1/approvals/${need.id}/decide`)).toBe(true));
  });

  it("says what approving creates, with a public repository's warning, while it holds the approval until the preview is read", async () => {
    const created = golden<PlanView>("plan-created").approval?.registration;
    const { view, need, planId, plan } = withManifest(created);
    const sent = serveApi((call) => (call.method === "GET" && call.path.startsWith(`/v1/plans/${planId}`) ? { status: 200, body: plan } : { status: 200 }));
    show(view, undefined, "plan_approval");
    expect((await approveButton()).disabled).toBe(true);
    const panel = await within(await card()).findByRole("region", { name: "Repositories approving registers" });
    expect(within(panel).getByRole("listitem", { name: "web" }).textContent).toContain("Will create");
    expect(within(panel).getByRole("listitem", { name: "docs" }).textContent).toContain("Public: anyone can read it");
    expect((await card()).textContent).toContain("Approving it also creates 1 repository on GitHub, acme/web, private, and registers it.");
    await waitFor(async () => expect((await approveButton()).disabled).toBe(false));
    expect(sent.filter((call) => call.method === "POST")).toEqual([]);
    expect(need.id).toBeTruthy();
  });

  it("does not approve when it cannot read what approving registers, and leads to the page that can", async () => {
    const { view, planId } = withManifest(preview);
    const sent = serveApi((call) =>
      call.method === "GET" && call.path.startsWith(`/v1/plans/${planId}`) ? refusal(500, "internal", "the daemon could not read the clones") : { status: 200 },
    );
    show(view, undefined, "plan_approval");
    expect((await within(await card()).findByRole("alert")).textContent).toContain("the daemon could not read the clones");
    expect((await approveButton()).disabled).toBe(true);
    expect(within(await card()).getByRole("link", { name: "Open the full plan approval" })).toBeTruthy();
    expect(sent.some((call) => call.method === "POST")).toBe(false);
  });

  it("asks nothing of a plan whose version lists no manifest", async () => {
    const { view } = planView();
    const sent = serveApi(() => ({ status: 200 }));
    show(view, undefined, "plan_approval");
    expect((await approveButton()).disabled).toBe(false);
    expect(within(await card()).queryByRole("region", { name: "Repositories approving registers" })).toBeNull();
    expect(sent).toHaveLength(0);
  });

  it.each([
    [1, "1 repository manifest"],
    [2, "2 repository manifests"],
  ])("names %d repository manifest(s) as manifests, not as repositories", async (count, words) => {
    const { view, need } = withManifest(undefined);
    need.plan_approval.counts = { ...need.plan_approval.counts, repositories: count };
    serveApi(() => ({ status: 200 }));
    show(view, undefined, "plan_approval");
    const holds = within(await card()).getByRole("list", { name: "What it holds" });
    expect(holds.textContent).toContain(words);
    expect(holds.textContent).not.toMatch(/\d repositories/);
  });

  // approvedWith is what the decide route answers for a plan whose manifest lists two repositories, one registered and one refused.
  const approvedWith = (more: Record<string, unknown> = {}) => ({
    id: "generated-13",
    state: "approved",
    repositories: [
      { name: "billing", manifest_path: "../billing", path: "/work/billing", remote: "git@example.invalid:acme/billing.git", outcome: "registered" },
      { name: "ghost", manifest_path: "../ghost", path: "/work/ghost", outcome: "refused", reason: "ghost is not registered: no git clone is at /work/ghost" },
    ],
    ...more,
  });

  it("shows what approving registered of each manifest entry, once the item no longer waits", async () => {
    localStorage.setItem("ropeshift-advance", "off");
    const { view, need, planId, plan } = withManifest(preview);
    const sent = serveApi((call) => {
      if (call.method === "GET" && call.path.startsWith(`/v1/plans/${planId}`)) {
        return { status: 200, body: plan };
      }
      return { status: 200, body: approvedWith() };
    });
    let rerender: ReturnType<typeof show>["rerender"] = () => {};
    // The page reads Needs you again after a decision, and the plan is no longer there.
    const after = async () =>
      rerender(
        <CurrentProject.Provider value={testProject}>
          <NeedsYou view={{ ...view, items: view.items.filter((item) => item.id !== need.id) }} readAt={Date.now()} after={async () => {}} />
        </CurrentProject.Provider>,
      );
    ({ rerender } = show(view, after, "plan_approval"));
    await waitFor(async () => expect((await approveButton()).disabled).toBe(false));
    await userEvent.click(await approveButton());
    await waitFor(() => expect(sent.some((call) => call.method === "POST" && call.path === `/v1/approvals/${need.id}/decide`)).toBe(true));
    const panel = await screen.findByRole("region", { name: "Repositories this approval registered" });
    expect(within(panel).getByRole("listitem", { name: "billing" }).textContent).toContain("Registered");
    expect(within(panel).getByRole("listitem", { name: "billing" }).textContent).toContain("/work/billing");
    const ghost = within(panel).getByRole("listitem", { name: "ghost" });
    expect(ghost.textContent).toContain("Refused");
    expect(ghost.textContent).toContain("no git clone is at /work/ghost");
    expect(screen.getByText(/no longer waits/)).toBeTruthy();
    expect(within(panel).queryByText(/Reading it registers nothing/)).toBeNull();
  });

  it("says why registration could not run, when the approval stands without it", async () => {
    const { view, need, planId, plan } = withManifest(preview);
    serveApi((call) => (call.method === "GET" && call.path.startsWith(`/v1/plans/${planId}`) ? { status: 200, body: plan } : { status: 200, body: approvedWith({ repositories: [], repositories_problem: "no worker on this machine keeps the planning repository" }) }));
    show(view, undefined, "plan_approval");
    await waitFor(async () => expect((await approveButton()).disabled).toBe(false));
    await userEvent.click(await approveButton());
    const panel = await screen.findByRole("region", { name: "Repositories this approval registered" });
    expect(within(panel).getByRole("alert").textContent).toContain("no worker on this machine keeps the planning repository");
    expect(need.id).toBeTruthy();
  });

  it("shows no registration when a plan is denied", async () => {
    const { view } = planView();
    const sent = serveApi(() => ({ status: 200, body: { id: "generated-13", state: "denied" } }));
    show(view, undefined, "plan_approval");
    await userEvent.click(within(await card()).getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    await waitFor(() => expect((within(screen.getAllByRole("article")[0] as HTMLElement).getByRole("button", { name: "Deny" }) as HTMLButtonElement).disabled).toBe(false));
    expect(screen.queryByRole("region", { name: "Repositories this approval registered" })).toBeNull();
  });

  it("groups an adopted plan, with each claim's evidence and every file its branch changed", async () => {
    const { view, item: need } = planView();
    need.plan_approval.adoption = {
      exists: [
        { ref: "requirement:SHOP-1", title: "One client", evidence: ["shop:client.go", "shop:client_test.go"] },
        { ref: "contract:client", title: "Payments client", cites: ["requirement:SHOP-1"] },
      ],
      continues: [{ contract: "contract:retry", work_order: "retry-finish", title: "Finish the retry branch", repo: ".", branch: "feature/retry" }],
      new: [{ ref: "contract:retry", title: "Retry failed calls", work_orders: ["retry-docs"] }],
      left_out: [{ ref: "branch:spike/old-ui", reason: "An abandoned experiment" }],
      changes: [
        { status: "changed", path: "docs/requirements.md" },
        { status: "moved", path: "docs/payments.md", from: "notes/payments.md" },
        { status: "added", path: "plans/plan.md" },
      ],
    };
    show(view, undefined, "plan_approval");
    const exists = within((await card())).getByRole("region", { name: "What exists" });
    expect(within(exists).getByRole("listitem", { name: "requirement:SHOP-1" }).textContent).toContain("shop:client_test.go");
    expect(within(exists).getByRole("listitem", { name: "contract:client" }).textContent).toContain("requirement:SHOP-1");
    const continues = within((await card())).getByRole("region", { name: "What continues" });
    expect(continues.textContent).toContain("feature/retry");
    expect(continues.textContent).toContain("retry-finish");
    expect(within((await card())).getByRole("region", { name: "What is new" }).textContent).toContain("retry-docs");
    expect(within((await card())).getByRole("region", { name: "What is left out" }).textContent).toContain("An abandoned experiment");
    const changes = within((await card())).getByRole("region", { name: "What the branch changes" });
    const files = within(changes).getAllByRole("listitem");
    expect(files.map((file) => file.textContent)).toEqual([
      "changed docs/requirements.md",
      "moved notes/payments.md to docs/payments.md",
      "added plans/plan.md",
    ]);
  });

  it("says a group of an adopted plan holds nothing, and why its changes could not be read", async () => {
    const { view, item: need } = planView();
    need.plan_approval.adoption = { exists: [], continues: [], new: [], left_out: [], changes: [], changes_unread: "the lane recorded no commit its branch began at" };
    show(view, undefined, "plan_approval");
    expect(within((await card())).getByRole("region", { name: "What is left out" }).textContent).toContain("Nothing.");
    expect(within((await card())).getByRole("region", { name: "What the branch changes" }).textContent).toContain("the lane recorded no commit its branch began at");
  });

  it("lists what a consolidation imported, the old design repository approving removes, and how the person merges it", async () => {
    const { view, item: need } = planView();
    need.plan_approval.adoption = {
      exists: [],
      continues: [],
      new: [],
      left_out: [],
      changes: [],
      consolidation: {
        design: "design",
        merge: "1111111",
        design_tip: "2222222",
        collisions: [
          { path: ".gitignore", kept: "union" },
          { path: "README.md", kept: "workspace" },
        ],
        left_out: ["poc"],
        removes: { id: "design-root", name: "design" },
        person_merges: true,
      },
    };
    show(view, undefined, "plan_approval");
    const imported = within(await card()).getByRole("region", { name: "What the consolidation imports" });
    expect(imported.textContent).toContain("The adoption imports the history of design in merge commit 1111111, whose second parent 2222222 is the design repository's tip.");
    expect(within(imported).getByRole("list", { name: "Paths both repositories held" }).textContent).toBe(".gitignore: the lines of bothREADME.md: the workspace's version");
    expect(within(imported).getByRole("list", { name: "Code trees left out" }).textContent).toBe("poc");
    expect(imported.textContent).toContain("Approving removes repository design from the project, and keeps its record as history.");
    expect(imported.textContent).toContain("At person_merges you merge this branch yourself: merge it with a merge commit. Only a merge commit keeps the design history");
  });

  it("says why a consolidation's merge could not be read, and lists the trees left out all the same", async () => {
    const { view, item: need } = planView();
    need.plan_approval.adoption = {
      exists: [],
      continues: [],
      new: [],
      left_out: [],
      changes: [],
      consolidation: { design: "design", collisions: [], left_out: [], unread: "git could not read the adoption branch" },
    };
    show(view, undefined, "plan_approval");
    const imported = within(await card()).getByRole("region", { name: "What the consolidation imports" });
    expect(imported.textContent).toContain("The adoption imports the history of design, and its merge could not be read: git could not read the adoption branch");
    expect(imported.textContent).toContain("No code tree is left out.");
    expect(within(imported).queryByRole("list", { name: "Paths both repositories held" })).toBeNull();
    expect(imported.textContent).not.toContain("person_merges");
    expect(imported.textContent).not.toContain("Approving removes");
  });

  it("says why a consolidation removes no repository, and that its list of colliding paths is cut short", async () => {
    const { view, item: need } = planView();
    need.plan_approval.adoption = {
      exists: [],
      continues: [],
      new: [],
      left_out: [],
      changes: [],
      consolidation: {
        design: "design",
        merge: "1111111",
        design_tip: "2222222",
        collisions: [{ path: "README.md", kept: "workspace" }],
        collisions_truncated: true,
        left_out: [],
        removal_unread: "nothing shows repository starter is the clone the adoption imported",
      },
    };
    show(view, undefined, "plan_approval");
    const imported = within(await card()).getByRole("region", { name: "What the consolidation imports" });
    expect(imported.textContent).toContain("The list is cut short: more paths were held by both.");
    expect(imported.textContent).toContain("Approving removes no repository: nothing shows repository starter is the clone the adoption imported");
    expect(imported.textContent).not.toContain("Approving removes repository");
  });

  it("shows no groups for a plan that adopts nothing", async () => {
    show(undefined, undefined, "plan_approval");
    expect(within((await card())).queryByRole("region", { name: "What exists" })).toBeNull();
  });
});

describe("a contract review", () => {
  const card = () => cardNamed(/^Document the retry budget/);

  it("names the work and its contract, and leads to the contract", async () => {
    const view = goldenNeedsYou();
    const { contract_review: review } = itemOf(view, "contract_review");
    show(view, undefined, "contract_review");
    const shown = (await card());
    expect(within(shown).getByText(/no plan approval covers it/)).toBeTruthy();
    expect(within(shown).getByText("Document the retry budget, version 1")).toBeTruthy();
    expect(within(shown).getAllByText("wo-review")[0]?.classList.contains("id")).toBe(true);
    expect(within(shown).getByRole("link", { name: "Read the contract" }).getAttribute("href")).toBe(here(`/plans?plan=${encodeURIComponent(review.plan)}`));
  });

  it("passes the review in one press", async () => {
    const sent = serveApi(() => ({ status: 200, body: { state: "ready" } }));
    show(undefined, undefined, "contract_review");
    await userEvent.click(within((await card())).getByRole("button", { name: "Pass the review" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-review/review" });
  });

  it("sends work back to planning only once a person says why", async () => {
    const sent = serveApi(() => ({ status: 204 }));
    show(undefined, undefined, "contract_review");
    await userEvent.click(within((await card())).getByRole("button", { name: "Send back to planning" }));
    expect(sent).toHaveLength(0);
    const confirm = within((await card())).getByRole("button", { name: "Send back to planning" });
    expect((confirm as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(within((await card())).getByRole("textbox", { name: "Why (required)" }), "The criteria are vague.");
    await userEvent.click(confirm);
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-review/send-back", body: { reason: "The criteria are vague." } });
  });

  it("says what the server refused", async () => {
    serveApi(() => refusal(409, "conflict", "The work order moved on."));
    show(undefined, undefined, "contract_review");
    await userEvent.click(within((await card())).getByRole("button", { name: "Pass the review" }));
    expect((await within((await card())).findByRole("alert")).textContent).toContain("moved on");
  });

  it("names no contract a plan does not hold", async () => {
    const view = goldenNeedsYou();
    itemOf(view, "contract_review").contract_review.plan = "";
    show(view, undefined, "contract_review");
    expect(within((await card())).queryByRole("link", { name: "Read the contract" })).toBeNull();
  });
});

describe("text from outside", () => {
  it("stays text, whatever it says", async () => {
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

  it.each(["summary", "command", "path", "lane_id"] as const)("stays text in an approval's %s", async (field) => {
    const view = goldenNeedsYou();
    itemOf(view, "approval").approval[field] = hostile;
    const { container } = show(view);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain(hostile);
  });

  it("stays text in a plan's draft decisions", async () => {
    const view = goldenNeedsYou();
    const drafts = itemOf(view, "plan_approval").plan_approval.drafts;
    expect(drafts.some((draft) => draft.answer.includes(hostile))).toBe(true);
    const { container } = show(view);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("script")).toBeNull();
    expect((await cardNamed(/^Approve The factory floor/)).textContent).toContain(hostile);
  });

  it.each(["key", "target", "lane_id", "worker_id"] as const)("stays text in an effect's %s", async (field) => {
    const view = goldenNeedsYou();
    itemOf(view, "effect").effect[field] = hostile;
    const { container } = show(view);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain(hostile);
  });

  it("is a rule of the source: no page builds HTML from a string", async () => {
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

describe("the combined view", () => {
  // twoProjects labels the golden view's items with two projects, as the installation's Needs you does.
  function twoProjects(): View {
    const view = goldenNeedsYou();
    view.items = view.items.map((item, i) => ({
      ...item,
      project: i % 2 === 0 ? { id: "default", name: "default" } : { id: "beta-id", name: "beta", display_name: "Beta product" },
    }));
    return view;
  }

  it("labels each item with its project", async () => {
    const view = twoProjects();
    show(view);
    const items = within(screen.getByRole("list", { name: "Waiting items" })).getAllByRole("link");
    expect(items).toHaveLength(view.items.length);
    expect(within(items[0] as HTMLElement).getByText("default")).toBeTruthy();
    expect(within(items[1] as HTMLElement).getByText("Beta product")).toBeTruthy();
    expect(screen.getByRole("article").textContent).toContain("default");
  });

  it("acts on an item in its own project, whichever project the page shows", async () => {
    const view = twoProjects();
    const { approval } = itemOf(view, "approval");
    const project = view.items.find((item) => item.kind === "approval")?.project?.name;
    const sent = serveApi(() => ({ status: 200 }));
    show(view, undefined, plainApproval);
    await userEvent.click(screen.getByRole("button", { name: "Allow this one call" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", project, path: `/v1/approvals/${approval.id}/decide` });
  });
});

// A request a person decides from this page says so once it no longer waits, as questions they answered do, and a
// request settled somewhere else says it was settled in another place. The page moves to the next item after a
// decision unless a person turned that off, which these tests do, so the request stays to say how it ended.
describe("a request decided from this page", () => {
  beforeEach(() => localStorage.setItem("ropeshift-advance", "off"));
  type Kind = { name: string; view: () => View; pick: Pick; approve: string; decline: string; reasoned: boolean };
  const recovery = (): View => {
    const view = goldenNeedsYou();
    const { approval, ...rest } = itemOf(view, "approval");
    const request = { ...approval, id: "recovery-1", kind: "recovery", subject_digest: "sha256:subject", detail: JSON.stringify({ act: "work_retry", work_order_id: "wo-failed" }) };
    return { ...view, items: [{ ...rest, id: request.id, approval: request }] };
  };
  const merge = (): View => {
    const view = goldenNeedsYou();
    const request = mergeRequest("approve");
    return { ...view, items: [{ ...itemOf(view, "approval"), id: request.id, approval: request }] };
  };
  const kinds: Kind[] = [
    { name: "an approval", view: goldenNeedsYou, pick: plainApproval, approve: "Allow this one call", decline: "Deny", reasoned: true },
    { name: "a recovery request", view: recovery, pick: "approval", approve: "Approve", decline: "Decline", reasoned: false },
    { name: "a merge request", view: merge, pick: "approval", approve: "Approve merge", decline: "Deny", reasoned: true },
    { name: "a plan approval", view: goldenNeedsYou, pick: "plan_approval", approve: "Approve this plan", decline: "Deny", reasoned: true },
  ];

  // settle renders Needs you on a view, and answers the next read of it with the view minus the picked item.
  function settle(kind: Kind) {
    const view = kind.view();
    const item = view.items.find(typeof kind.pick === "function" ? kind.pick : (row) => row.kind === kind.pick) as NeedItem;
    const without = (
      <CurrentProject.Provider value={testProject}>
        <NeedsYou view={{ ...view, items: view.items.filter((row) => row !== item) }} readAt={Date.now()} after={async () => {}} />
      </CurrentProject.Provider>
    );
    let rerender: ReturnType<typeof show>["rerender"] = () => {};
    ({ rerender } = show(view, async () => rerender(without), kind.pick));
    return { item, rerender: () => rerender(without) };
  }

  const resolvedRow = () => screen.getAllByRole("link").find((link) => link.getAttribute("aria-current") === "true");

  for (const kind of kinds) {
    describe(kind.name, () => {
      it("says the person approved it, as the member the ledger recorded", async () => {
        const { item } = settle(kind);
        const sent = serveApi((call) => (call.method === "POST" ? { status: 200, body: { id: item.id, state: "approved", decided_by: "member-ada" } } : undefined));
        await userEvent.click(await screen.findByRole("button", { name: kind.approve }));
        await waitFor(() => expect(sent.some((call) => call.method === "POST")).toBe(true));
        const note = await screen.findByText(/no longer waits/);
        expect(note.textContent).toContain("you decided it, as member-ada");
        expect(note.textContent).not.toContain("another place");
        expect(within(screen.getByRole("list", { name: "Your decision" })).getByText("Approved")).toBeTruthy();
        expect(resolvedRow()?.textContent).toContain("Decided by you");
        expect(resolvedRow()?.textContent).not.toContain("Resolved elsewhere");
      });

      it("says the person declined it, with the reason they gave", async () => {
        const { item } = settle(kind);
        const reason = "Split it first.";
        const sent = serveApi((call) => (call.method === "POST" ? { status: 200, body: { id: item.id, state: "denied", decided_by: "member-ada", ...(kind.reasoned ? { reason } : {}) } } : undefined));
        if (kind.reasoned) {
          await userEvent.type(await screen.findByRole("textbox", { name: /Reason/ }), reason);
        }
        await userEvent.click(await screen.findByRole("button", { name: kind.decline }));
        await waitFor(() => expect(sent.some((call) => call.method === "POST")).toBe(true));
        const note = await screen.findByText(/no longer waits/);
        expect(note.textContent).toContain("you decided it, as member-ada");
        const decision = within(screen.getByRole("list", { name: "Your decision" }));
        expect(decision.getByText("Declined")).toBeTruthy();
        if (kind.reasoned) {
          expect(decision.getByText(reason, { exact: false })).toBeTruthy();
        }
      });

      it("still says another place decided it when nobody decided it here", async () => {
        const { rerender } = settle(kind);
        rerender();
        const note = await screen.findByText(/no longer waits/);
        expect(note.textContent).toContain("it was decided or resolved in another place");
        expect(note.textContent).not.toContain("you decided it");
        expect(screen.queryByRole("list", { name: "Your decision" })).toBeNull();
        expect(resolvedRow()?.textContent).toContain("Resolved elsewhere");
      });
    });
  }
});
