import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import type { Sent } from "../testing";
import { refusal, serveViews } from "../testing";

const dialog = () => screen.findByRole("dialog", { name: "Record decision" });
const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");
const anID = expect.stringMatching(/^[0-9A-Z]{26}$/);

function serving(post?: (call: Sent) => { status: number; body?: unknown } | undefined) {
  return serveViews((call) => (call.method === "POST" ? (post?.(call) ?? { status: 201, body: { id: "recorded", status: "decided", statement: "x" } }) : undefined));
}

// The page's own button, which opens the dialog.
const pageButton = () => screen.findByRole("button", { name: "Record decision" });

// write types the statement of a decision, as a member would.
async function write(box: HTMLElement, statement: string) {
  await userEvent.type(within(box).getByRole("textbox", { name: "Statement" }), statement);
}

describe("recording a decision", () => {
  it("asks a member to review it first, and then records it once, as theirs", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await write(box, "Charge in euros.");
    await userEvent.type(within(box).getByRole("textbox", { name: "Rationale" }), "Every customer is in the union.");
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    expect(await within(box).findByRole("heading", { name: "Review the decision" })).toBeTruthy();
    expect(within(box).getByText("Charge in euros.")).toBeTruthy();
    expect(within(box).getByText(/recorded once, as yours/)).toBeTruthy();
    expect(changes(sent)).toHaveLength(0);
    await userEvent.click(within(box).getByRole("button", { name: "Record decision" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/decisions", project: "default", csrf: "token-from-cookie" });
    expect(changes(sent)[0]?.body).toEqual({ id: anID, status: "decided", statement: "Charge in euros.", rationale: "Every customer is in the union." });
    expect((await screen.findAllByText("Recorded the decision.")).length).toBeGreaterThan(0);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Record decision" })).toBeNull());
  });

  it("sends every field the command takes, each as the command sends it", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await write(box, "Charge in euros.");
    await userEvent.type(within(box).getByRole("textbox", { name: "Rationale" }), "Every customer is in the union.");
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "Reversibility" }), "one_way");
    await userEvent.type(within(box).getByRole("textbox", { name: "Scope" }), "billing");
    await userEvent.click(within(box).getByRole("checkbox", { name: "Data" }));
    await userEvent.click(within(box).getByRole("checkbox", { name: "Security" }));
    await userEvent.type(within(box).getByRole("textbox", { name: "Citations" }), "requirement:FR-RETRY-1\n docs/billing.md ");
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "Replaces" }), "decision-3");
    await userEvent.click(within(box).getByText("More"));
    await userEvent.type(within(box).getByRole("textbox", { name: "Answers the question" }), "question-1");
    await userEvent.type(within(box).getByRole("textbox", { name: "Judged compatible with" }), "decision-1, decision-2\ndecision-1");
    await userEvent.type(within(box).getByRole("textbox", { name: "Decision ID (optional)" }), "decision-euros");
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    await userEvent.click(await within(box).findByRole("button", { name: "Record decision" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({
      id: "decision-euros",
      status: "decided",
      statement: "Charge in euros.",
      rationale: "Every customer is in the union.",
      citations: ["requirement:FR-RETRY-1", "docs/billing.md"],
      scope: "billing",
      reversibility: "one_way",
      categories: ["data", "security"],
      supersedes: "decision-3",
      question_id: "question-1",
      no_conflict: ["decision-1", "decision-2"],
    });
  });

  it("records an assumption as one, with the words that say what it is", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await write(box, "Prices are in euros until a customer says otherwise.");
    await userEvent.click(within(box).getByRole("checkbox", { name: "Record as an assumption" }));
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    expect(await within(box).findByRole("heading", { name: "Review the assumption" })).toBeTruthy();
    expect(within(box).getByText(/lets work go on/)).toBeTruthy();
    await userEvent.click(within(box).getByRole("button", { name: "Record assumption" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toMatchObject({ status: "assumed", statement: "Prices are in euros until a customer says otherwise." });
    expect((await screen.findAllByText("Recorded the assumption.")).length).toBeGreaterThan(0);
  });

  it("offers to replace only a decision still in force", async () => {
    serving();
    openPage("/decisions");
    await userEvent.click(await pageButton());
    const select = within(await dialog()).getByRole("combobox", { name: "Replaces" });
    await waitFor(() => expect(within(select).getAllByRole("option").length).toBeGreaterThan(1));
    expect(within(select).getAllByRole("option").map((option) => option.textContent)).toEqual(["Nothing", "decision-3: Retry until the budget runs out."]);
  });

  it("asks for the statement before it goes to the review", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    expect(within(box).getByText("State what was decided.")).toBeTruthy();
    expect(within(box).getByRole("textbox", { name: "Statement" }).getAttribute("aria-invalid")).toBe("true");
    expect(within(box).queryByRole("heading", { name: /^Review/ })).toBeNull();
    expect(changes(sent)).toHaveLength(0);
  });

  it("refuses an ID plan approval names its decisions by, and one the ledger would not take, before it reviews", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await write(box, "Charge in euros.");
    await userEvent.click(within(box).getByText("More"));
    const id = within(box).getByRole("textbox", { name: "Decision ID (optional)" });
    for (const [bad, said] of [
      ["pd-2f9a", /may not start with pd-, which only plan approval names decisions under/],
      ["has space", /1 to 64 letters, digits, - and _/],
      ["x".repeat(65), /1 to 64 letters, digits, - and _/],
    ] as const) {
      await userEvent.clear(id);
      await userEvent.type(id, bad);
      await userEvent.click(within(box).getByRole("button", { name: "Review" }));
      expect(within(box).getByText(said)).toBeTruthy();
      expect(id.getAttribute("aria-invalid")).toBe("true");
      expect(within(box).queryByRole("heading", { name: /^Review/ })).toBeNull();
    }
    expect(changes(sent)).toHaveLength(0);
    // PD- is another ID, which the ledger takes, because the prefix is read as written.
    await userEvent.clear(id);
    await userEvent.type(id, "PD-1");
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    expect(await within(box).findByRole("heading", { name: "Review the decision" })).toBeTruthy();
  });

  it("shows a refusal of the record in the review, keeps what was written, and repeats under one ID", async () => {
    let calls = 0;
    const sent = serving(() => {
      calls += 1;
      return calls === 1 ? refusal(409, "decision_ended", "decision decision-3 is overturned") : { status: 201, body: { id: "recorded", status: "decided", statement: "x" } };
    });
    openPage("/decisions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await write(box, "Charge in euros.");
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    await userEvent.click(await within(box).findByRole("button", { name: "Record decision" }));
    expect((await within(box).findByRole("alert")).textContent).toContain("decision-3 is overturned");
    await userEvent.click(within(box).getByRole("button", { name: "Back" }));
    expect((within(box).getByRole("textbox", { name: "Statement" }) as HTMLTextAreaElement).value).toBe("Charge in euros.");
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    await userEvent.click(await within(box).findByRole("button", { name: "Record decision" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent).map((call) => (call.body as { id: string }).id)).toEqual([(changes(sent)[0]?.body as { id: string }).id, (changes(sent)[0]?.body as { id: string }).id]);
  });

  it("says which decision an overlap names, when the ledger found one", async () => {
    serving(() => ({ status: 201, body: { id: "d-1", status: "decided", statement: "x", overlaps: ["decision-3"] } }));
    openPage("/decisions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await write(box, "Retry forever.");
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    await userEvent.click(await within(box).findByRole("button", { name: "Record decision" }));
    expect((await screen.findAllByText("Recorded the decision. Its scope overlaps decision-3.")).length).toBeGreaterThan(0);
  });

  it("goes back from the review to the form, with nothing sent", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await write(box, "Charge in euros.");
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    await userEvent.click(await within(box).findByRole("button", { name: "Back" }));
    expect(within(box).getByRole("textbox", { name: "Statement" })).toBeTruthy();
    expect(changes(sent)).toHaveLength(0);
  });

  it("closes on Escape and sends nothing", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await pageButton());
    await dialog();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Record decision" })).toBeNull());
    expect(changes(sent)).toHaveLength(0);
  });

  it("is in the command palette, which opens the dialog on the decisions", async () => {
    serving();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), "record decision");
    await userEvent.click(await within(palette).findByRole("option", { name: /^Record decision/ }));
    const box = await dialog();
    expect(window.location.pathname).toBe("/projects/default/decisions");
    expect(box.contains(document.activeElement)).toBe(true);
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(window.location.search).toBe(""));
  });
});
