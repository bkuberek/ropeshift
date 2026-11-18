import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { CIKind, NeedItem, NeedsYou as View } from "../api/types";
import { CurrentProject } from "../app/route";
import { golden, here, refusal, serveApi, testProject } from "../testing";
import { cardKindOf, cardKinds, contextOf, filterOf, headingOf, keyOf, kindOf } from "./kinds";
import { NeedsYou } from "./NeedsYou";

type CIItem = Extract<NeedItem, { kind: "ci" }>;

// The items a Go test wrote from a real ledger: a head four hours into its CI with no history, a run queued for two
// hours, and each finding of the classification.
const items = (): CIItem[] => golden<View>("ci-needs-you").items.filter((item): item is CIItem => item.kind === "ci");

const itemOf = (kind: CIKind): CIItem => {
  const found = items().find((item) => item.ci.kind === kind);
  if (!found) {
    throw new Error(`the golden holds no ${kind}`);
  }
  return found;
};

function show(item: CIItem, after: () => Promise<void> = async () => {}) {
  const view: View = { ...golden<View>("ci-needs-you"), items: [item] };
  window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(keyOf(item))}`);
  return render(
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={after} />
    </CurrentProject.Provider>,
  );
}

const theCard = (name: string) => screen.getByRole("article", { name });

const names = (card: HTMLElement) => [...card.querySelectorAll("a.btn, button")].map((control) => control.textContent?.replace(/\(opens in a new tab\)/, "").trim());

describe("the kinds of CI that ask a person", () => {
  it("are every kind the server raises, each a card of its own that the accessibility checks hold to a state", () => {
    const served = items().map((item) => item.ci.kind);
    expect(new Set(served)).toEqual(new Set(["ci_not_finished", "ci_queue_stalled", "ci_infrastructure_failure", "ci_base_red", "ci_action_required"]));
    for (const kind of served) {
      expect(cardKinds).toContain(`ci-${kind}`);
    }
  });

  it("are stuck work, named by the work order and counted by its pull request", () => {
    const item = itemOf("ci_not_finished");
    expect(kindOf(item)).toEqual({ icon: "clock", name: "CI has not finished" });
    expect(headingOf(item)).toBe("Add the retry budget");
    expect(contextOf(item)).toBe("wo-ci · pull request #14");
    expect(cardKindOf(item)).toBe("ci-ci_not_finished");
    expect(filterOf(item)).toBe("stuck");
    expect(kindOf(itemOf("ci_queue_stalled")).name).toBe("No runner picked it up");
  });
});

describe("the card of CI that has not finished", () => {
  it("says how long the head has run against its limit, that CI has no history, and that no agent was started", () => {
    show(itemOf("ci_not_finished"));
    const card = theCard("Add the retry budget");
    expect(card.textContent).toContain("CI has not finished");
    expect(card.textContent).toContain("No agent was started");
    const facts = card.querySelector("dl")?.textContent ?? "";
    expect(facts).toContain("Running for4h");
    expect(facts).toContain("Time limit3h");
    expect(facts).toContain("Expected CI timeno history");
    expect(facts).toContain("Pull request#14");
    expect(names(card)).toEqual(["Open the run", "Rerun", "Wait longer"]);
  });

  it("opens the run's page in a new tab, and follows no address that is not a web address", () => {
    show(itemOf("ci_not_finished"));
    const link = within(theCard("Add the retry budget")).getByRole("link", { name: /Open the run/ });
    expect(link.getAttribute("href")).toBe("https://github.com/acme/app/actions/runs/9");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noreferrer noopener");
    for (const address of ["javascript:alert(1)", "http://forge.example/runs/9", "https://user:secret@forge.example/runs/9", "https://", "forge.example/runs/9"]) {
      const unsafe = itemOf("ci_not_finished");
      unsafe.ci.run_url = address;
      document.body.replaceChildren();
      show(unsafe);
      expect(within(theCard("Add the retry budget")).queryByRole("link", { name: /Open the run/ }), address).toBeNull();
    }
  });

  it("waits longer with one press, which adds one more p85, and reads the ledger again", async () => {
    const sent = serveApi(() => ({ status: 200, body: { work_order: "wo-ci", watch: {} } }));
    const after = vi.fn(async () => {});
    show(itemOf("ci_not_finished"), after);
    await userEvent.click(within(theCard("Add the retry budget")).getByRole("button", { name: "Wait longer" }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    const posts = sent.filter((call) => call.method === "POST");
    expect(posts.map((call) => `${call.path} ${JSON.stringify(call.body)}`)).toEqual(["/v1/work-orders/wo-ci/ci-wait {}"]);
  });

  it("waits for the time a person types", async () => {
    const sent = serveApi(() => ({ status: 200, body: { work_order: "wo-ci", watch: {} } }));
    show(itemOf("ci_not_finished"));
    await userEvent.type(within(theCard("Add the retry budget")).getByRole("textbox", { name: "Wait for (optional)" }), " 45m ");
    await userEvent.click(within(theCard("Add the retry budget")).getByRole("button", { name: "Wait longer" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST")).toBe(true));
    expect(sent.find((call) => call.method === "POST")?.body).toEqual({ by: "45m" });
  });

  it("offers Rerun, which ropeshift makes on the forge, and no longer says a person reruns the checks there", () => {
    show(itemOf("ci_not_finished"));
    const card = theCard("Add the retry budget");
    expect(within(card).queryByRole("button", { name: "Record a rerun" })).toBeNull();
    const button = within(card).getByRole("button", { name: "Rerun" });
    expect(button.getAttribute("aria-describedby")).toBeNull();
    expect(card.textContent).not.toMatch(/does not rerun|record it here|Record a rerun/);
  });

  it("offers Rerun when the forge gave no address for the run", () => {
    const item = itemOf("ci_not_finished");
    delete item.ci.run_url;
    show(item);
    const card = theCard("Add the retry budget");
    expect(names(card)).toEqual(["Rerun", "Wait longer"]);
    expect(card.textContent).not.toMatch(/does not rerun/);
  });

  it("reruns, and starts nothing else", async () => {
    const sent = serveApi(() => ({ status: 200, body: { work_order: "wo-ci", reruns: 1 } }));
    show(itemOf("ci_not_finished"));
    await userEvent.click(within(theCard("Add the retry budget")).getByRole("button", { name: "Rerun" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST")).toBe(true));
    expect(sent.filter((call) => call.method === "POST").map((call) => call.path)).toEqual(["/v1/work-orders/wo-ci/ci-rerun"]);
    expect(sent.filter((call) => /lanes|claims|rework|retry\b/.test(call.path))).toEqual([]);
  });

  it("says why the server refused, in its words", async () => {
    serveApi(() => refusal(400, "invalid", "invalid work order: wo-ci raises none of [ci_not_finished] about its CI now"));
    show(itemOf("ci_not_finished"));
    await userEvent.click(within(theCard("Add the retry budget")).getByRole("button", { name: "Wait longer" }));
    expect((await screen.findByText(/raises none of \[ci_not_finished\]/)).textContent).toContain("invalid work order");
  });
});

describe("the card of a run no runner picked up", () => {
  it("says how long it queued and offers only the run, because the fix is a runner", () => {
    show(itemOf("ci_queue_stalled"));
    const card = theCard("Trim the retry log");
    expect(card.textContent).toContain("The fix is a runner, not a rerun");
    const facts = card.querySelector("dl")?.textContent ?? "";
    expect(facts).toContain("Queued for2h");
    expect(facts).toContain("Queue limit30m");
    expect(names(card)).toEqual(["Open the run"]);
    expect(within(card).queryByRole("textbox")).toBeNull();
  });
});

describe("the cards of what the classification found", () => {
  it("offers a rerun and the run for an infrastructure failure, which names its check and conclusion", () => {
    show(itemOf("ci_infrastructure_failure"));
    const card = theCard("Add the retry budget");
    expect(card.textContent).toContain("CI failed, not the code");
    const facts = card.querySelector("dl")?.textContent ?? "";
    expect(facts).toContain("CheckCI");
    expect(facts).toContain("Conclusiontimed_out");
    expect(names(card)).toEqual(["Open the run", "Rerun"]);
  });

  it.each(["ci_base_red", "ci_action_required"] as const)("offers only the run for %s, which no rerun mends", (kind) => {
    show(itemOf(kind));
    const card = theCard("Add the retry budget");
    expect(names(card)).toEqual(["Open the run"]);
    expect(card.textContent).toContain("No agent was started");
  });
});
