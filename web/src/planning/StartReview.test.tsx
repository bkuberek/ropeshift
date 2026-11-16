import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Plans } from "../api/types";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, refusal, serveViews } from "../testing";

const dialog = () => screen.findByRole("dialog", { name: "Start contract review" });
const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");
const anID = expect.stringMatching(/^[0-9A-Z]{26}$/);
const started = { id: "review-new", title: "Review Add the retry budget", kind: "contract_review", state: "ready" };

// The golden project's plans, as the plans route lists them.
const listed = () => golden<Plans>("plans").plans.map((detail) => detail.plan);

function serving(post?: (call: Sent) => { status: number; body?: unknown } | undefined, plans: ReturnType<typeof listed> = listed()) {
  return serveViews((call) => {
    if (call.method === "POST") {
      return post?.(call) ?? { status: 201, body: started };
    }
    if (call.method === "GET" && call.path === "/v1/plans") {
      return { status: 200, body: { plans } };
    }
    return undefined;
  });
}

const pageButton = () => screen.findByRole("button", { name: "Start contract review" });

describe("starting a contract review", () => {
  it("starts the review of the plan whose page it is on, once, and opens the review it made", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage("/plans?plan=generated-2");
    await screen.findByRole("heading", { level: 1, name: "Add the retry budget" });
    await userEvent.click(await pageButton());
    const box = await dialog();
    expect(within(box).getByText("Add the retry budget")).toBeTruthy();
    expect(within(box).queryByRole("combobox", { name: "Plan" })).toBeNull();
    expect(changes(sent)).toHaveLength(0);
    await userEvent.click(within(box).getByRole("button", { name: "Start contract review" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/plans/generated-2/reviews", project: "default", csrf: "token-from-cookie" });
    expect(changes(sent)[0]?.body).toEqual({ id: anID });
    expect((await screen.findAllByText("Started the contract review of Add the retry budget.")).length).toBeGreaterThan(0);
    await waitFor(() => expect(window.location.pathname + window.location.search).toBe("/projects/default/work-order?id=review-new"));
  });

  it("asks which plan on the list of plans, from the plans the route lists", async () => {
    const sent = serving();
    openPage("/plans");
    await userEvent.click(await pageButton());
    const box = await dialog();
    const choose = within(box).getByRole("combobox", { name: "Plan" });
    await waitFor(() => expect(within(choose).getAllByRole("option").length).toBeGreaterThan(2));
    expect(within(choose).getAllByRole("option").map((option) => option.textContent)).toEqual(["Choose a plan", ...listed().map((plan) => plan.title)]);
    await userEvent.click(within(box).getByRole("button", { name: "Start contract review" }));
    expect(within(box).getByText("Choose the plan to review.")).toBeTruthy();
    expect(choose.getAttribute("aria-invalid")).toBe("true");
    expect(changes(sent)).toHaveLength(0);
    await userEvent.selectOptions(choose, "generated-4");
    await userEvent.click(within(box).getByRole("button", { name: "Start contract review" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/plans/generated-4/reviews" });
  });

  it("chooses the plan itself when the project has the one", async () => {
    const sent = serving(undefined, listed().slice(0, 1));
    openPage("/plans");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await waitFor(() => expect((within(box).getByRole("combobox", { name: "Plan" }) as HTMLSelectElement).value).toBe("generated-2"));
    await userEvent.click(within(box).getByRole("button", { name: "Start contract review" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe("/v1/plans/generated-2/reviews");
  });

  it("says so when the project has no plan, and sends nothing", async () => {
    const sent = serving(undefined, []);
    openPage("/plans");
    await userEvent.click(await pageButton());
    const box = await dialog();
    expect(await within(box).findByText("This project has no plan to review yet.")).toBeTruthy();
    await userEvent.click(within(box).getByRole("button", { name: "Start contract review" }));
    expect(changes(sent)).toHaveLength(0);
  });

  it("shows the API's refusal in the dialog, and repeats under one ID", async () => {
    let calls = 0;
    const sent = serving(() => {
      calls += 1;
      return calls === 1 ? refusal(400, "invalid", "plan generated-2 has no work to review") : { status: 201, body: started };
    });
    openPage("/plans?plan=generated-2");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await userEvent.click(within(box).getByRole("button", { name: "Start contract review" }));
    expect((await within(box).findByRole("alert")).textContent).toContain("plan generated-2 has no work to review");
    expect(window.location.pathname).toBe("/projects/default/plans");
    await userEvent.click(within(box).getByRole("button", { name: "Start contract review" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    const ids = changes(sent).map((call) => (call.body as { id: string }).id);
    expect(ids[1]).toBe(ids[0]);
    await waitFor(() => expect(window.location.search).toBe("?id=review-new"));
  });

  it("shows the refusal when the plans cannot be read, and sends nothing", async () => {
    const sent = serveViews((call) => (call.method === "GET" && call.path === "/v1/plans" ? refusal(500, "internal", "read failed") : undefined));
    openPage("/plans");
    await userEvent.click(await pageButton());
    const box = await dialog();
    expect(await within(box).findByText(/The plans could not be read/)).toBeTruthy();
    await userEvent.click(within(box).getByRole("button", { name: "Start contract review" }));
    expect(changes(sent)).toHaveLength(0);
  });

  it("closes on Escape and sends nothing", async () => {
    const sent = serving();
    openPage("/plans?plan=generated-2");
    await userEvent.click(await pageButton());
    await dialog();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Start contract review" })).toBeNull());
    expect(changes(sent)).toHaveLength(0);
  });

  it("is in the command palette, which opens the dialog on the plans", async () => {
    serving();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), "contract review");
    await userEvent.click(await within(palette).findByRole("option", { name: /^Start contract review/ }));
    const box = await dialog();
    expect(window.location.pathname).toBe("/projects/default/plans");
    expect(box.contains(document.activeElement)).toBe(true);
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(window.location.search).toBe(""));
  });
});
