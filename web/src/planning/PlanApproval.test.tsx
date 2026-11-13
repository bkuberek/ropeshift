import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { ApprovalRecord, Plans, PlanView } from "../api/types";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, goldenNeedsYou, here, refusal, serveViews } from "../testing";

type Answer = { status: number; body?: unknown };

const waiting = golden<PlanView>("plan");
const planId = waiting.plan.id;
const page = (version = 6) => here(`/plan?id=${planId}&version=${version}`);

// serve answers the plan the golden holds, with a change a test makes to it. The plans view names its
// generated IDs apart from this plan's, so the plan the page reads contracts of is aligned by title.
function serve(change: (view: PlanView) => void = () => {}, answer?: (sent: Sent) => Answer | undefined) {
  const view = golden<PlanView>("plan");
  change(view);
  const plans = golden<Plans>("plans");
  for (const row of plans.plans) {
    if (row.plan.title === view.plan.title) {
      row.plan.id = view.plan.id;
    }
  }
  return serveViews((call) => {
    const own = answer?.(call);
    if (own) {
      return own;
    }
    if (call.method === "GET" && call.path.startsWith(`/v1/plans/${planId}`)) {
      return { status: 200, body: view };
    }
    if (call.method === "GET" && call.path.startsWith("/v1/views/plans")) {
      return { status: 200, body: plans };
    }
    if (call.method === "POST" && call.path.endsWith("/decide")) {
      return { status: 200, body: {} };
    }
    return undefined;
  });
}

const heading = () => screen.findByRole("heading", { level: 1, name: "Approve plan: The factory floor, version 6" });
const decide = (sent: Sent[]) => sent.filter((call) => call.method === "POST" && call.path.endsWith("/decide"));

describe("the plan approval page", () => {
  it("names the plan and version, who wrote it, and what binds the approval", async () => {
    const sent = serve();
    openPage(page());
    await heading();
    expect(sent.some((call) => call.path === `/v1/plans/${planId}?version=6`)).toBe(true);
    expect(screen.getByText("Planning session")).toBeTruthy();
    const digest = waiting.approval?.approval.subject_digest.replace(/^sha256:/, "").slice(0, 12) ?? "";
    expect(screen.getAllByText(digest).length).toBeGreaterThan(1);
    expect(screen.getByText(/If the plan changes, this approval is void\./)).toBeTruthy();
  });

  it("lists how the version differs from the approved one: each record added, changed, or removed, with its kind", async () => {
    serve();
    openPage(page());
    await heading();
    const diff = screen.getByRole("region", { name: "Difference from version 5" });
    const added = within(diff).getByText("Added").parentElement as HTMLElement;
    expect(within(added).getByText("Architecture")).toBeTruthy();
    expect(within(added).getByText("factory")).toBeTruthy();
    const changed = within(diff).getByText("Changed").parentElement as HTMLElement;
    for (const name of ["core", "ui", "factory", "factory-api"]) {
      expect(within(changed).getAllByText(name).length).toBeGreaterThan(0);
    }
    expect(within(diff).getByText("Removed").parentElement?.textContent).toContain("Nothing.");
    expect(within(diff).getByRole("list", { name: "Inside contract:core" }).textContent).toContain("Criteria added: c2");
    expect(within(diff).getByRole("list", { name: "Inside contract:ui" }).textContent).toContain("Work orders added: ui-2");
  });

  it("says a first version differs from nothing, so every record is new", async () => {
    serve((view) => {
      if (view.approval) {
        view.approval.detail.difference = { added: ["plan:factory", "milestone:m1"] };
      }
    });
    openPage(page());
    await heading();
    const diff = screen.getByRole("region", { name: "Difference from the approved version" });
    expect(within(diff).getByText("Nothing was approved before, so every record is new.")).toBeTruthy();
    expect(within(diff).getByText("Milestone")).toBeTruthy();
  });

  it("shows each draft decision as text, with its question, answer, rationale, and who proposed it", async () => {
    serve();
    openPage(page());
    await heading();
    const drafts = screen.getByRole("list", { name: "Draft decisions" });
    const items = within(drafts).getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0]?.textContent).toContain("Which agent CLI runs the first lanes?");
    expect(items[0]?.textContent).toContain("It needs no new sign-in.");
    expect(items[0]?.textContent).toContain("Proposed by system:intake");
    expect(items[1]?.textContent).toContain("<img src=x onerror=alert(1)><script>alert(2)</script>");
    expect(document.querySelector("img[src='x']")).toBeNull();
  });

  it("marks a draft that a blocking question stood behind", async () => {
    serve((view) => {
      if (view.approval?.drafts?.[0]) {
        view.approval.drafts[0].blocking = true;
      }
    });
    openPage(page());
    await heading();
    expect(within(screen.getByRole("list", { name: "Draft decisions" })).getByText("Blocking")).toBeTruthy();
  });

  it("lists the contracts the version adds or changes, with criteria, requirement chips, and work orders", async () => {
    serve();
    openPage(page());
    await heading();
    const contract = await screen.findByRole("region", { name: "The planning core" });
    expect(within(contract).getByText("Changed")).toBeTruthy();
    expect(within(contract).getAllByText("c1").length).toBeGreaterThan(0);
    expect(within(contract).getByText(/Work orders:/).textContent).toContain("Build the planning core");
    expect(screen.queryByRole("region", { name: "The API client" })).toBeNull();
  });

  it("lists the documents the version covers, each linking to the difference between its versions", async () => {
    serve();
    openPage(page());
    await heading();
    const documents = screen.getByRole("region", { name: "Documents" });
    expect(within(documents).getAllByRole("link", { name: "factory-api" }).length).toBeGreaterThan(0);
    const link = (await within(documents).findAllByRole("link", { name: "Version 1 to 2" }))[0] as HTMLElement;
    expect(link.getAttribute("href")).toBe(here("/documents?id=architecture%3Afactory&version=2&against=1"));
  });

  it("lists the versions of the plan with where each request stands, and links to each", async () => {
    serve();
    openPage(page());
    await heading();
    const versions = screen.getByRole("list", { name: "Versions of the plan" });
    const rows = within(versions).getAllByRole("listitem");
    expect(rows).toHaveLength(6);
    expect(rows[0]?.textContent).toContain("Version 6 (this one)");
    expect(rows[1]?.textContent).toContain("Approved");
    expect(within(rows[1] as HTMLElement).getByRole("link", { name: "Version 5" }).getAttribute("href")).toBe(page(5));
    expect(rows[2]?.textContent).toContain("Void");
  });
});

describe("deciding a version", () => {
  it("approves with the digest the page shows, bound to what it read", async () => {
    const sent = serve();
    openPage(page());
    await heading();
    await userEvent.click(screen.getByRole("button", { name: "Approve version 6" }));
    await waitFor(() => expect(decide(sent)).toHaveLength(1));
    expect(decide(sent)[0]?.path).toBe(`/v1/approvals/${waiting.approval?.approval.id}/decide`);
    expect(decide(sent)[0]?.body).toEqual({ digest: waiting.approval?.approval.subject_digest, allow: true, reason: "" });
    expect(decide(sent)[0]?.csrf).not.toBeNull();
  });

  it("says what approving does, and what still reaches the person later", async () => {
    serve();
    openPage(page());
    await heading();
    const card = screen.getByRole("complementary", { name: "Your decision" });
    expect(within(card).getByText(/Approving decides 2 draft decisions as yours and passes contract review for the work of 4 contracts\./)).toBeTruthy();
    expect(within(card).getByText(/still reaches you later/)).toBeTruthy();
  });

  it("shows a refused approval in the API's words", async () => {
    serve(undefined, (call) => (call.path.endsWith("/decide") ? refusal(409, "digest_mismatch", "The plan changed since you read it.") : undefined));
    openPage(page());
    await heading();
    await userEvent.click(screen.getByRole("button", { name: "Approve version 6" }));
    expect(await screen.findByText("The plan changed since you read it.")).toBeTruthy();
  });

  it("declines only with a reason, which it asks for and focuses", async () => {
    const sent = serve();
    openPage(page());
    await heading();
    await userEvent.click(screen.getByRole("button", { name: "Decline…" }));
    const dialog = await screen.findByRole("dialog", { name: "Decline version 6" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Decline version 6" }));
    expect(within(dialog).getByText("Say why you decline, in a sentence.")).toBeTruthy();
    expect(document.activeElement).toBe(within(dialog).getByLabelText("Reason (required)"));
    expect(decide(sent)).toHaveLength(0);
    fireEvent.change(within(dialog).getByLabelText("Reason (required)"), { target: { value: "The retry budget should be per key." } });
    await userEvent.click(within(dialog).getByRole("button", { name: "Decline version 6" }));
    await waitFor(() => expect(decide(sent)).toHaveLength(1));
    expect(decide(sent)[0]?.body).toEqual({ digest: waiting.approval?.approval.subject_digest, allow: false, reason: "The retry budget should be per key." });
  });
});

describe("a version that is not waiting", () => {
  it("is void when a newer version replaced it, with a banner linking the newer one and no button to approve", async () => {
    serve((view) => {
      if (view.approval) {
        view.approval.approval.state = "superseded";
        view.approval.approval.plan_version = 4;
      }
    }, undefined);
    openPage(page(4));
    await screen.findByRole("heading", { level: 1, name: /The factory floor, version 4/ });
    const banner = screen.getByText("Version 6 replaced this version.").closest(".banner") as HTMLElement;
    expect(within(banner).getByRole("link", { name: "Open version 6" }).getAttribute("href")).toBe(page(6));
    expect(screen.queryByRole("button", { name: /Approve version/ })).toBeNull();
    expect(within(screen.getByRole("status", { name: "Resolution" })).getByText("Void")).toBeTruthy();
  });

  it("is read-only once approved, with who decided it and when", async () => {
    const approved = golden<PlanView>("plan-approved");
    serve(
      () => {},
      (call) => {
        if (call.method === "GET" && call.path === `/v1/plans/${planId}?version=5`) {
          const view = structuredClone(approved);
          if (view.approval) {
            Object.assign(view.approval.approval, { decided_by: "member:sam", decided_at: 1790000100000, reason: "Looks right." });
          }
          return { status: 200, body: view };
        }
        return undefined;
      },
    );
    openPage(page(5));
    await screen.findByRole("heading", { level: 1, name: /The factory floor, version 5/ });
    const status = screen.getByRole("status", { name: "Resolution" });
    expect(status.textContent).toContain("Approved by member:sam");
    expect(status.textContent).toContain("Looks right.");
    expect(screen.queryByRole("button", { name: /Approve version|Decline/ })).toBeNull();
  });

  it("is read-only once declined, with the reason", async () => {
    serve((view) => {
      if (view.approval) {
        Object.assign(view.approval.approval, { state: "denied", decided_by: "member:sam", decided_at: 1790000100000, reason: "Not yet." });
      }
    });
    openPage(page());
    await screen.findByRole("heading", { level: 1, name: /The factory floor, version 6/ });
    expect(screen.getByRole("status", { name: "Resolution" }).textContent).toContain("Declined by member:sam");
    expect(screen.getByRole("status", { name: "Resolution" }).textContent).toContain("Not yet.");
  });

  it("says when no request asks about a version", async () => {
    serve((view) => {
      view.approval = undefined;
    });
    openPage(page(3));
    expect(await screen.findByText("No request asks about version 3")).toBeTruthy();
    expect(screen.getByRole("list", { name: "Versions of the plan" })).toBeTruthy();
  });

  it("says when there is no such plan", async () => {
    serveViews((call) => (call.path.startsWith("/v1/plans/") || call.path.startsWith("/v1/approvals/") ? refusal(404, "not_found", "plan nope: not found") : undefined));
    openPage(here("/plan?id=nope&version=1"));
    expect(await screen.findByRole("heading", { level: 1, name: "No such plan" })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("not found");
  });
});

describe("an adopted plan", () => {
  const adopted = golden<ApprovalRecord>("approval-adopted");
  const adoptedPage = here(`/plan?id=${adopted.id}&version=1`);

  function serveAdopted(change: (approval: ApprovalRecord, detail: Record<string, unknown>) => void = () => {}) {
    const approval = structuredClone(adopted);
    const detail = JSON.parse(approval.detail ?? "{}") as Record<string, unknown>;
    change(approval, detail);
    approval.detail = JSON.stringify(detail);
    return serveViews((call) => {
      if (call.method === "GET" && call.path.startsWith("/v1/plans/")) {
        return refusal(404, "not_found", "plan: not found");
      }
      if (call.method === "GET" && call.path === `/v1/approvals/${adopted.id}`) {
        return { status: 200, body: approval };
      }
      return undefined;
    });
  }

  it("reads the request when the ledger holds no plan for it yet, and decides it", async () => {
    const sent = serveAdopted();
    openPage(adoptedPage);
    await screen.findByRole("heading", { level: 1, name: "Approve plan: Adopt shop, version 1" });
    expect(sent.some((call) => call.path === `/v1/approvals/${adopted.id}`)).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Approve version 1" }));
    await waitFor(() => expect(decide(sent)).toHaveLength(1));
    expect(decide(sent)[0]?.path).toBe(`/v1/approvals/${adopted.id}/decide`);
  });

  it("groups what exists with each claim's evidence, what continues, what is new, and what is left out with its reason", async () => {
    serveAdopted();
    openPage(adoptedPage);
    const panel = await screen.findByRole("region", { name: "What the adoption does" });
    const exists = within(panel).getByText("What exists").parentElement as HTMLElement;
    expect(exists.textContent).toContain("requirement:SHOP-1");
    expect(within(exists).getByRole("list", { name: "Evidence for requirement:SHOP-1" }).textContent).toBe("shop:client.goshop:client_test.go");
    expect(exists.textContent).toContain("Rests on requirement:SHOP-2");
    const continues = within(panel).getByText("What continues").parentElement as HTMLElement;
    expect(continues.textContent).toContain("retry-finish");
    expect(continues.textContent).toContain("feature/retry in shop");
    expect(within(panel).getByText("What is new").parentElement?.textContent).toContain("milestone:m2");
    expect(within(panel).getByText("What is left out").parentElement?.textContent).toContain("Abandoned in favor of the new checkout");
  });

  it("shows the adoption branch's changed files as one change, a moved file with where it came from", async () => {
    serveAdopted();
    openPage(adoptedPage);
    const files = await screen.findByRole("list", { name: "Files the branch changes" });
    const rows = within(files).getAllByRole("listitem");
    expect(rows.map((row) => row.textContent)).toEqual([
      expect.stringContaining("Addedplans/plan.md"),
      expect.stringMatching(/Moveddocs\/payments\.md.*from notes\/payments\.md/),
      expect.stringContaining("Changeddocs/requirements.md"),
    ]);
  });

  it("lists no contract of another plan, because the ledger holds none of this plan's yet", async () => {
    serveAdopted();
    openPage(adoptedPage);
    const contracts = await screen.findByRole("region", { name: "New and changed contracts" });
    expect(within(contracts).getByText(/The ledger holds this plan's contracts once it is approved\./)).toBeTruthy();
    expect(within(contracts).queryByRole("region")).toBeNull();
  });

  it("gives the reason when the branch's files could not be read", async () => {
    serveAdopted((_, detail) => {
      const adoption = detail.adoption as Record<string, unknown>;
      adoption.changes = [];
      adoption.changes_unread = "this control plane reads no repository's history";
    });
    openPage(adoptedPage);
    expect(await screen.findByText(/The changes could not be read: this control plane reads no repository's history/)).toBeTruthy();
    expect(screen.queryByRole("list", { name: "Files the branch changes" })).toBeNull();
  });

  it("reaches the page from the plan approval card in Needs you", async () => {
    serveViews();
    const item = goldenNeedsYou().items.find((row) => row.kind === "plan_approval");
    openPage(`/needs-you?item=${encodeURIComponent(`plan_approval:${item?.id}`)}`);
    const link = await screen.findByRole("link", { name: /Open the full plan approval/ });
    expect(link.getAttribute("href")).toMatch(/^\/projects\/default\/plan\?id=.+&version=\d+$/);
  });
});
