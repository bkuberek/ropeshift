import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { NeedItem, NeedsYou as View, SpendLane, SpendNeed } from "../api/types";
import { CurrentProject } from "../app/route";
import { golden, here, refusal, serveApi, testProject } from "../testing";
import { cardKindOf, cardKinds, contextOf, filterOf, headingOf, keyOf, kindOf } from "./kinds";
import { NeedsYou } from "./NeedsYou";
import { amountError } from "./spendWords";

type SpendItem = Extract<NeedItem, { kind: "spend" }>;

// The item a Go test wrote from a real ledger: a work order charged $30.00 against a hard limit of $25.00, parked by
// its one lane, whose last digest and handoff narrative it holds.
const stop = (): SpendItem => {
  const found = golden<View>("spend-needs-you").items.find((item): item is SpendItem => item.kind === "spend");
  if (!found) {
    throw new Error("the golden holds no spend stop");
  }
  return found;
};

function show(item: SpendItem, after: () => Promise<void> = async () => {}) {
  const view: View = { ...golden<View>("spend-needs-you"), items: [item] };
  window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(keyOf(item))}`);
  return render(
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={after} />
    </CurrentProject.Provider>,
  );
}

const theCard = () => screen.getByRole("article", { name: "Add the retry budget" });

const names = (card: HTMLElement) => [...card.querySelectorAll("a.btn, button")].map((control) => control.textContent?.trim()).filter((name) => name !== "Open lane lane-wo-stop");

const facts = (card: HTMLElement) => card.querySelector("dl")?.textContent ?? "";

const posts = (sent: { method: string; path: string; body?: unknown }[]) => sent.filter((call) => call.method === "POST").map((call) => `${call.path} ${JSON.stringify(call.body)}`);

describe("a spend stop in Needs you", () => {
  it("is a card kind of its own, stuck work named by the work order and counted by what it is charged", () => {
    const item = stop();
    expect(cardKinds).toContain("spend");
    expect(cardKindOf(item)).toBe("spend");
    expect(filterOf(item)).toBe("stuck");
    expect(kindOf(item)).toEqual({ icon: "gauge", name: "Spend limit reached" });
    expect(headingOf(item)).toBe("Add the retry budget");
    expect(contextOf(item)).toBe("wo-stop · $30.00 of $25.00");
    expect(keyOf(item)).toBe("spend:wo-stop:spend_limit");
  });

  it("says what the work order is charged against its limits, what remains, where the limits came from, and what is not known", () => {
    show(stop());
    const card = theCard();
    expect(card.textContent).toContain("Spend limit reached");
    const read = facts(card);
    expect(read).toContain("Charged$30.00 estimated, at list price, with 0 turns estimated by average");
    expect(read).toContain("Soft limit$12.50");
    expect(read).toContain("Hard limit$25.00");
    expect(read).toContain("Remaining$0.00");
    expect(read).toContain("Cost bandno estimate");
    expect(read).toContain("Limits fromthe limit for its size, because it has no trusted cost band");
    expect(read).toContain("Waiting sharenot attributed yet");
  });

  it("says the band with how far it can be trusted, and the waiting share once there is one", () => {
    const item = stop();
    item.spend.band = { p50: 12, p85: 30, outcomes: 8, confidence: "confident", baselined: true };
    item.spend.waiting_share = 0.4;
    item.spend.spend.basis = "baselined_band";
    item.spend.spend.raised_to = 25;
    show(item);
    const read = facts(theCard());
    expect(read).toContain("Cost bandp50 $12.00 · p85 $30.00 · confident, from 8 finished work orders");
    expect(read).toContain("Limits fromthe cost band recorded when its plan was approved");
    expect(read).toContain("Waiting share40 percent of its spend");
    expect(read).toContain("Hard limit$25.00 (raised by a member)");
    item.spend.band = { p50: 12, p85: 12, outcomes: 1, confidence: "low", open: true };
    document.body.replaceChildren();
    show(item);
    expect(facts(theCard())).toContain("Cost bandp50 $12.00 · p85 $12.00 or more · low confidence, from 1 finished work order");
  });

  it("offers the five ways out, each named for what it does, and starts no agent by being shown", () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    show(stop());
    expect(names(theCard())).toEqual(["Raise to this amount", "Block", "Send to replanning", "Raise by 50 percent", "Raise by 100 percent", "Resume once"]);
    expect(sent.filter((call) => call.method !== "GET")).toEqual([]);
  });

  it("says what Resume once would grant, and by what it is sized", () => {
    show(stop());
    expect(theCard().textContent).toContain("Resume once lets one more generation spend $30.00, sized by the mean charge of the generations that recorded spend, and sets the hard limit to $60.00.");
    const item = stop();
    item.spend.resume = { allowance_usd: 12, basis: "estimate", limit_usd: 42 };
    document.body.replaceChildren();
    show(item);
    expect(theCard().textContent).toContain("sized by the work order's estimate, because no generation recorded spend, and sets the hard limit to $42.00.");
  });

  it("offers no Resume once where nothing sizes a generation, and says to raise the limit", () => {
    const item = stop();
    delete item.spend.resume;
    item.spend.actions = item.spend.actions.filter((action) => action !== "spend_resume");
    show(item);
    expect(names(theCard())).not.toContain("Resume once");
    expect(theCard().textContent).toContain("Nothing sizes one more generation for this work order, so Resume once is not offered. Raise its limit instead.");
  });

  it("offers a block and no replanning for a review, which has no contract to plan again", () => {
    const item = stop();
    item.spend.actions = item.spend.actions.filter((action) => action !== "spend_replan");
    show(item);
    expect(names(theCard())).toContain("Block");
    expect(names(theCard())).not.toContain("Send to replanning");
  });
});

// summaryOf is the line that names a lane and opens what it left.
const summaryOf = (lanes: HTMLElement, lane: string): HTMLElement => {
  const found = [...lanes.querySelectorAll("summary")].find((summary) => summary.textContent?.includes(lane));
  if (!found) {
    throw new Error(`no summary names ${lane}`);
  }
  return found;
};

describe("what each lane left", () => {
  const lanesOf = (card: HTMLElement) => within(card).getByRole("region", { name: "What the lanes left" });

  it("names each lane with its last digest, and opens to the narrative its agent wrote", async () => {
    show(stop());
    const lanes = lanesOf(theCard());
    expect(lanes.textContent).toContain("1 lane served this work order");
    const summary = summaryOf(lanes, "lane-wo-stop");
    expect(summary.textContent).toBe("Lane lane-wo-stop, generation 1: 9 turns · 6 tool calls · 1 file written");
    await userEvent.click(summary);
    const text = lanes.textContent ?? "";
    for (const line of ["the budget type", "the retry loop", "wire the loop to the budget", "a flaky test"]) {
      expect(text).toContain(line);
    }
    expect(within(lanes).getByRole("link", { name: "Open lane lane-wo-stop" }).getAttribute("href")).toContain("/lane?id=lane-wo-stop");
  });

  it("renders what an agent left out of a narrative or a digest as nothing, and does not fall over on a null", async () => {
    const item = stop();
    const lane = item.spend.lanes[0]!;
    // A narrative may name only some of its lists, and a stored digest may lack fields, so each reads null.
    const sparse = { done: ["the budget type"], in_progress: null, next_steps: null, risks: null, carries_next_step: false };
    lane.handoff = { generation: 1, reason: "spend_limit", narrative: sparse as unknown as NonNullable<SpendLane["handoff"]>["narrative"] };
    lane.digest = { ...lane.digest!, tool_calls: null, files_written: null, files_written_more: undefined } as unknown as SpendLane["digest"];
    show(item);
    const lanes = within(theCard()).getByRole("region", { name: "What the lanes left" });
    const summary = summaryOf(lanes, "lane-wo-stop");
    expect(summary.textContent).toBe("Lane lane-wo-stop, generation 1: 9 turns · 0 tool calls · 0 files written");
    await userEvent.click(summary);
    expect(lanes.textContent).toContain("the budget type");
    expect(within(lanes).getAllByText("nothing")).toHaveLength(3);
  });

  it("is one item however many lanes served the work order", () => {
    const item = stop();
    const more: SpendLane[] = [{ lane: "lane-earlier", generation: 2, digest: { source: "hooks", turns: 1, tool_calls: {}, files_written: [], started_at: 1, complete: false } }, { lane: "lane-first" }];
    item.spend.lanes = [...item.spend.lanes, ...more];
    show(item);
    expect(screen.getAllByRole("article")).toHaveLength(1);
    const lanes = within(theCard()).getByRole("region", { name: "What the lanes left" });
    expect(lanes.textContent).toContain("3 lanes served this work order");
    expect(lanes.textContent).toContain("1 turn · 0 tool calls · 0 files written · may be incomplete");
    expect(lanes.textContent).toContain("Lane lane-first: no digest");
  });

  it("says a handoff degraded, and that a lane left none", async () => {
    const item = stop();
    const [first] = item.spend.lanes as [SpendLane];
    item.spend.lanes = [{ ...first, handoff: { generation: 1, degraded: true } }, { lane: "lane-bare" }];
    show(item);
    const lanes = within(theCard()).getByRole("region", { name: "What the lanes left" });
    await userEvent.click(summaryOf(lanes, "lane-wo-stop"));
    await userEvent.click(summaryOf(lanes, "lane-bare"));
    expect(lanes.textContent).toContain("Its handoff in generation 1 degraded, so it has no narrative: only what the ledger knew was kept.");
    expect(lanes.textContent).toContain("It left no handoff.");
  });
});

describe("the ways out", () => {
  it("raise the limit by half with one press, and read the ledger again", async () => {
    const sent = serveApi(() => ({ status: 200, body: { work_order: "wo-stop", act: "raise" } }));
    const after = vi.fn(async () => {});
    show(stop(), after);
    await userEvent.click(within(theCard()).getByRole("button", { name: "Raise by 50 percent" }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(posts(sent)).toEqual(['/v1/work-orders/wo-stop/spend/raise {"by_percent":50}']);
  });

  it("raise the limit by all of it", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    show(stop());
    await userEvent.click(within(theCard()).getByRole("button", { name: "Raise by 100 percent" }));
    await waitFor(() => expect(posts(sent)).toHaveLength(1));
    expect(posts(sent)).toEqual(['/v1/work-orders/wo-stop/spend/raise {"by_percent":100}']);
  });

  it("raise the limit to an amount a person types, by the button and by Enter", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    show(stop());
    const field = within(theCard()).getByRole("textbox", { name: "Raise the hard limit to (US dollars)" });
    const button = within(theCard()).getByRole("button", { name: "Raise to this amount" }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    await userEvent.type(field, "80.50");
    expect(button.disabled).toBe(false);
    await userEvent.click(button);
    await waitFor(() => expect(posts(sent)).toHaveLength(1));
    await userEvent.type(field, "{Enter}");
    await waitFor(() => expect(posts(sent)).toHaveLength(2));
    expect(posts(sent)).toEqual(Array(2).fill('/v1/work-orders/wo-stop/spend/raise {"to_usd":80.5}'));
  });

  it("refuse an amount that lifts nothing before it is sent, and say why", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    show(stop());
    const field = within(theCard()).getByRole("textbox", { name: "Raise the hard limit to (US dollars)" });
    const button = within(theCard()).getByRole("button", { name: "Raise to this amount" }) as HTMLButtonElement;
    for (const [typed, said] of [
      ["28", "A limit of $28.00 would stay below the $30.00 this work order is charged."],
      ["30", "A limit of $30.00 would stay below the $30.00 this work order is charged."],
      ["eighty", "Write an amount in dollars, such as 80 or 80.50."],
      ["1000001", "A limit is at most $1000000.00."],
    ] as const) {
      await userEvent.clear(field);
      await userEvent.type(field, typed);
      expect(within(theCard()).getByText(said), typed).toBeTruthy();
      expect(button.disabled, typed).toBe(true);
      expect(field.getAttribute("aria-invalid")).toBe("true");
    }
    await userEvent.type(field, "{Enter}");
    expect(posts(sent)).toEqual([]);
  });

  it("resume once with one press", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    show(stop());
    await userEvent.click(within(theCard()).getByRole("button", { name: "Resume once" }));
    await waitFor(() => expect(posts(sent)).toHaveLength(1));
    expect(posts(sent)).toEqual(["/v1/work-orders/wo-stop/spend/resume {}"]);
  });

  it("block the work order, which names the limit as its reason", async () => {
    const sent = serveApi(() => ({ status: 200, body: { work_order: "wo-stop", state: "blocked", reason: "spend_limit" } }));
    show(stop());
    await userEvent.click(within(theCard()).getByRole("button", { name: "Block" }));
    await waitFor(() => expect(posts(sent)).toHaveLength(1));
    expect(posts(sent)).toEqual(["/v1/work-orders/wo-stop/spend/block {}"]);
  });

  it("send the work order to replanning", async () => {
    const sent = serveApi(() => ({ status: 200, body: { work_order: "wo-stop", state: "planning", reason: "spend_limit" } }));
    show(stop());
    await userEvent.click(within(theCard()).getByRole("button", { name: "Send to replanning" }));
    await waitFor(() => expect(posts(sent)).toHaveLength(1));
    expect(posts(sent)).toEqual(["/v1/work-orders/wo-stop/spend/replan {}"]);
  });

  it("start no lane, claim, or retry of their own", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    show(stop());
    for (const name of ["Raise by 50 percent", "Block"]) {
      await userEvent.click(within(theCard()).getByRole("button", { name }));
    }
    await waitFor(() => expect(posts(sent).length).toBeGreaterThan(0));
    expect(sent.filter((call) => /\/lanes|claims|retry|lane-/.test(call.path))).toEqual([]);
  });

  it("say why the server refused, in its words, and leave the card for another try", async () => {
    serveApi(() => refusal(403, "forbidden", "only a maintainer may raise a limit in group mode"));
    show(stop());
    await userEvent.click(within(theCard()).getByRole("button", { name: "Resume once" }));
    expect((await screen.findByText(/only a maintainer may raise a limit/)).textContent).toContain("maintainer");
    expect((within(theCard()).getByRole("button", { name: "Resume once" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("say a work order a raise cannot lift is refused with the figure it fell short of", async () => {
    serveApi(() => refusal(400, "invalid", "invalid work order: a hard limit of $25.00 would stay below charged spend of $30.00 on work order wo-stop, so raise it to more than that"));
    show(stop());
    await userEvent.click(within(theCard()).getByRole("button", { name: "Raise by 50 percent" }));
    expect((await screen.findByText(/would stay below charged spend of \$30.00/)).textContent).toContain("raise it to more than that");
  });
});

describe("an amount to raise to", () => {
  const spend = (): SpendNeed["spend"] => stop().spend.spend;

  it("is any figure above both the limit and what is charged, with at most two places", () => {
    expect(amountError("", spend())).toBe("");
    expect(amountError("30.01", spend())).toBe("");
    expect(amountError(" 80 ", spend())).toBe("");
    expect(amountError("30", spend())).toContain("stay below");
    expect(amountError("-5", spend())).toContain("Write an amount");
    expect(amountError("1e3", spend())).toContain("Write an amount");
    expect(amountError("80.555", spend())).toContain("Write an amount");
    expect(amountError("1000000", spend())).toBe("");
    expect(amountError("1000000.01", spend())).toContain("at most");
  });

  it("must also clear the limit in force when that is above what is charged", () => {
    const limited = spend();
    limited.charged.charged_usd = 20;
    limited.hard_limit = 25;
    expect(amountError("25", limited)).toBe("A raise must be above the $25.00 limit in force.");
    expect(amountError("25.01", limited)).toBe("");
  });
});
