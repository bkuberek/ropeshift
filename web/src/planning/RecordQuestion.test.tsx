import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import type { Sent } from "../testing";
import { refusal, serveViews } from "../testing";

const dialog = () => screen.findByRole("dialog", { name: "Record question" });
const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");
const anID = expect.stringMatching(/^[0-9A-Z]{26}$/);

// serving answers the views from their goldens, and a change with what a test says: by default, the record
// the daemon would send back.
function serving(post?: (call: Sent) => { status: number; body?: unknown } | undefined) {
  return serveViews((call) => (call.method === "POST" ? (post?.(call) ?? { status: 201, body: {} }) : undefined));
}

// The page's own button, which opens the dialog.
const pageButton = () => screen.findByRole("button", { name: "Record question" });

describe("recording a question", () => {
  it("sends the question with its categories and what it is about", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "Question" }), "Which currency do invoices use?");
    await userEvent.click(within(box).getByRole("checkbox", { name: "Security" }));
    await userEvent.click(within(box).getByRole("checkbox", { name: "Data" }));
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "About" }), "work_order");
    await userEvent.type(within(box).getByRole("textbox", { name: "Work order ID" }), "wo-invoices");
    await userEvent.click(within(box).getByRole("checkbox", { name: "Work on it waits for the answer" }));
    await userEvent.click(within(box).getByRole("button", { name: "Record question" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/questions", project: "default", csrf: "token-from-cookie" });
    expect(changes(sent)[0]?.body).toEqual({
      id: anID,
      text: "Which currency do invoices use?",
      categories: ["data", "security"],
      subject_kind: "work_order",
      subject_id: "wo-invoices",
      blocking: true,
    });
    expect((await screen.findAllByText("Recorded the question.")).length).toBeGreaterThan(0);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Record question" })).toBeNull());
  });

  it("sends only the text when that is all a member gives", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "Question" }), "  Is the cache shared?  ");
    await userEvent.click(within(box).getByRole("button", { name: "Record question" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({ id: anID, text: "Is the cache shared?" });
  });

  it("offers every category the project has, built-in and added", async () => {
    serving();
    openPage("/questions");
    await userEvent.click(await pageButton());
    const group = within(await dialog()).getByRole("group", { name: "Categories" });
    await waitFor(() => expect(within(group).getAllByRole("checkbox").length).toBe(9));
    expect(within(group).getAllByRole("checkbox").map((box) => (box as HTMLInputElement).labels?.[0]?.textContent)).toEqual([
      "Architecture", "Business", "Compliance", "Data", "Design", "Files", "Operations", "Product", "Security",
    ]);
  });

  it("asks for the question before it sends one", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await userEvent.click(within(box).getByRole("button", { name: "Record question" }));
    expect(within(box).getByText("Write the question.")).toBeTruthy();
    expect(within(box).getByRole("textbox", { name: "Question" }).getAttribute("aria-invalid")).toBe("true");
    expect(changes(sent)).toHaveLength(0);
  });

  it("holds what it is about to a kind and a plain ID, which the ledger reads", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "Question" }), "Which currency?");
    expect(within(box).queryByRole("textbox", { name: /ID$/ })).toBeNull();
    expect((within(box).getByRole("checkbox", { name: "Work on it waits for the answer" }) as HTMLInputElement).disabled).toBe(true);
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "About" }), "plan");
    await userEvent.click(within(box).getByRole("button", { name: "Record question" }));
    expect(within(box).getByText("Name the plan, or choose nothing in particular.")).toBeTruthy();
    await userEvent.type(within(box).getByRole("textbox", { name: "Plan ID" }), "has space");
    await userEvent.click(within(box).getByRole("button", { name: "Record question" }));
    expect(within(box).getByText(/1 to 64 letters, digits, - and _/)).toBeTruthy();
    expect(changes(sent)).toHaveLength(0);
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "About" }), "");
    expect(within(box).queryByRole("textbox", { name: /ID$/ })).toBeNull();
    await userEvent.click(within(box).getByRole("button", { name: "Record question" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({ id: anID, text: "Which currency?" });
  });

  it("shows the daemon's refusal, keeps what was written, and repeats under one ID", async () => {
    let calls = 0;
    const sent = serving(() => {
      calls += 1;
      return calls === 1 ? refusal(400, "invalid", "invalid question or decision: unknown category") : { status: 201, body: {} };
    });
    openPage("/questions");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "Question" }), "Which currency?");
    await userEvent.click(within(box).getByRole("button", { name: "Record question" }));
    expect((await within(box).findByRole("alert")).textContent).toContain("unknown category");
    expect((within(box).getByRole("textbox", { name: "Question" }) as HTMLTextAreaElement).value).toBe("Which currency?");
    await userEvent.click(within(box).getByRole("button", { name: "Record question" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    const [first, second] = changes(sent);
    expect((first?.body as { id: string }).id).toBe((second?.body as { id: string }).id);
  });

  it("closes on Escape and sends nothing", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await pageButton());
    await dialog();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Record question" })).toBeNull());
    expect(changes(sent)).toHaveLength(0);
  });

  it("is in the command palette, which opens the dialog on the open questions", async () => {
    serving();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), "record question");
    await userEvent.click(await within(palette).findByRole("option", { name: /^Record question/ }));
    const box = await dialog();
    expect(window.location.pathname).toBe("/projects/default/questions");
    expect(box.contains(document.activeElement)).toBe(true);
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(window.location.search).toBe(""));
  });
});

describe("dismissing a question", () => {
  const card = (name: string) => screen.findByRole("article", { name });
  const text = "Does the plan cover a restart?";

  it("asks first, and then closes the question with no answer", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving((call) => (call.path.endsWith("/dismiss") ? { status: 204 } : undefined));
    openPage("/questions");
    await userEvent.click(within(await card(text)).getByRole("button", { name: `Dismiss ${text}` }));
    const box = await screen.findByRole("dialog", { name: "Dismiss this question?" });
    expect(within(box).getByText(text)).toBeTruthy();
    expect(within(box).getByText(/closes with no answer/)).toBeTruthy();
    expect(changes(sent)).toHaveLength(0);
    await userEvent.click(within(box).getByRole("button", { name: "Dismiss question" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/questions/question-review/dismiss", project: "default", body: {}, csrf: "token-from-cookie" });
    expect((await screen.findAllByText("Dismissed the question.")).length).toBeGreaterThan(0);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Dismiss this question?" })).toBeNull());
  });

  it("starts with focus on the safe choice, and Cancel sends nothing", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(within(await card(text)).getByRole("button", { name: `Dismiss ${text}` }));
    const box = await screen.findByRole("dialog", { name: "Dismiss this question?" });
    expect(document.activeElement).toBe(within(box).getByRole("button", { name: "Cancel" }));
    await userEvent.click(within(box).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Dismiss this question?" })).toBeNull());
    expect(changes(sent)).toHaveLength(0);
  });

  it("shows the daemon's refusal in the dialog", async () => {
    serving(() => refusal(400, "invalid", "invalid question or decision: question question-review is answered"));
    openPage("/questions");
    await userEvent.click(within(await card(text)).getByRole("button", { name: `Dismiss ${text}` }));
    const box = await screen.findByRole("dialog", { name: "Dismiss this question?" });
    await userEvent.click(within(box).getByRole("button", { name: "Dismiss question" }));
    expect((await within(box).findByRole("alert")).textContent).toContain("is answered");
  });

  it("names the question it dismisses, whatever its text holds", async () => {
    serving();
    openPage("/questions");
    const hostile = "May the <img src=x onerror=alert(1)><script>alert(2)</script> stay?";
    await userEvent.click(within(await card(hostile)).getByRole("button", { name: `Dismiss ${hostile}` }));
    const box = await screen.findByRole("dialog", { name: "Dismiss this question?" });
    expect(within(box).getByText(hostile)).toBeTruthy();
    expect(box.querySelector("img, script")).toBeNull();
  });
});
