import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import type { Sent } from "../testing";
import { refusal, serveViews } from "../testing";

const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");
const searches = (sent: Sent[]) => sent.filter((call) => call.path.startsWith("/v1/candidates"));

const found = [
  { kind: "requirement", record_id: "FR-RETRY-1", score: 0.9 },
  { kind: "decision", record_id: "decision-3", score: 0.7 },
];

function serving(candidates: (call: Sent) => { status: number; body?: unknown } = () => ({ status: 200, body: { candidates: found } })) {
  return serveViews((call) => {
    if (call.path.startsWith("/v1/candidates")) {
      return candidates(call);
    }
    return call.method === "POST" ? { status: 201, body: { old: { id: "decision-3", status: "overturned", statement: "x" }, new: { id: "n", status: "decided", statement: "y" } } } : undefined;
  });
}

async function openRecord() {
  openPage("/decisions");
  await userEvent.click(await screen.findByRole("button", { name: "Record decision" }));
  const box = await screen.findByRole("dialog", { name: "Record decision" });
  await userEvent.type(within(box).getByRole("textbox", { name: "Statement" }), "Retry twice.");
  return box;
}

describe("finding a record to cite", () => {
  it("searches the project's records for the words, and cites one into the citations", async () => {
    const sent = serving();
    const box = await openRecord();
    await userEvent.type(within(box).getByRole("textbox", { name: "Find a record to cite" }), "retry budget");
    await userEvent.click(within(box).getByRole("button", { name: "Search" }));
    expect(await within(box).findByText("requirement:FR-RETRY-1")).toBeTruthy();
    expect(searches(sent)).toHaveLength(1);
    expect(searches(sent)[0]).toMatchObject({ method: "GET", project: "default" });
    const query = new URLSearchParams(searches(sent)[0]?.path.split("?")[1]);
    expect(query.get("text")).toBe("retry budget");
    expect(query.get("kinds")).toBe("decision,requirement,contract,question");
    expect(query.get("limit")).toBe("8");
    await userEvent.click(within(box).getByRole("button", { name: "Cite requirement:FR-RETRY-1" }));
    await userEvent.click(within(box).getByRole("button", { name: "Cite decision:decision-3" }));
    expect((within(box).getByRole("textbox", { name: "Citations" }) as HTMLTextAreaElement).value).toBe("requirement:FR-RETRY-1\ndecision:decision-3");
    // A record cited is shown as cited, and cannot be cited twice.
    expect((within(box).getByRole("button", { name: "Cited requirement:FR-RETRY-1" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.click(within(box).getByRole("button", { name: "Review" }));
    await userEvent.click(await within(box).findByRole("button", { name: "Record decision" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toMatchObject({ citations: ["requirement:FR-RETRY-1", "decision:decision-3"] });
  });

  it("searches on Enter, and Enter does not go on to the review", async () => {
    const sent = serving();
    const box = await openRecord();
    await userEvent.type(within(box).getByRole("textbox", { name: "Find a record to cite" }), "retry{Enter}");
    expect(await within(box).findByText("decision:decision-3")).toBeTruthy();
    expect(within(box).queryByRole("heading", { name: /^Review/ })).toBeNull();
    expect(searches(sent)).toHaveLength(1);
  });

  it("searches for nothing no more than it must: no words, no search", async () => {
    const sent = serving();
    const box = await openRecord();
    expect((within(box).getByRole("button", { name: "Search" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(within(box).getByRole("textbox", { name: "Find a record to cite" }), "   ");
    expect((within(box).getByRole("button", { name: "Search" }) as HTMLButtonElement).disabled).toBe(true);
    expect(searches(sent)).toHaveLength(0);
  });

  it("says when nothing matches", async () => {
    serving(() => ({ status: 200, body: { candidates: [] } }));
    const box = await openRecord();
    await userEvent.type(within(box).getByRole("textbox", { name: "Find a record to cite" }), "zebra{Enter}");
    expect(await within(box).findByText("Nothing matches those words.")).toBeTruthy();
  });

  it("shows the daemon's refusal", async () => {
    serving(() => refusal(400, "invalid", "candidates need the kinds to read"));
    const box = await openRecord();
    await userEvent.type(within(box).getByRole("textbox", { name: "Find a record to cite" }), "retry{Enter}");
    expect((await within(box).findByText(/candidates need the kinds to read/)).closest("[role=alert]")).toBeTruthy();
  });

  it("is in the overturn dialog too", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await screen.findByRole("button", { name: "Overturn decision-3" }));
    const box = await screen.findByRole("dialog", { name: "Overturn decision-3" });
    await userEvent.type(within(box).getByRole("textbox", { name: "New statement" }), "Retry twice.");
    await userEvent.type(within(box).getByRole("textbox", { name: "Find a record to cite" }), "retry{Enter}");
    await userEvent.click(await within(box).findByRole("button", { name: "Cite requirement:FR-RETRY-1" }));
    await userEvent.click(within(box).getByRole("button", { name: "Overturn decision" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toMatchObject({ statement: "Retry twice.", citations: ["requirement:FR-RETRY-1"] });
  });
});
