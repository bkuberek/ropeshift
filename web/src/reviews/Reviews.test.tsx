import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Completed, NeedsYou, Operations } from "../api/types";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, here, refusal, serveViews } from "../testing";

// The Reviews view reads what the API serves from a real ledger: work waiting for acceptance, work whose merge
// waits for a member, and work whose merge a member approved, with the pull requests they land through.

type Served = {
  completed?: (view: Completed) => void;
  needs?: (view: NeedsYou) => void;
  operations?: (view: Operations) => void;
  post?: (call: Sent) => { status: number; body?: unknown } | undefined;
};

function serving({ completed, needs, operations, post }: Served = {}) {
  const done = golden<Completed>("reviews-completed");
  completed?.(done);
  const waiting = golden<NeedsYou>("reviews-needs-you");
  needs?.(waiting);
  const measured = golden<Operations>("operations");
  operations?.(measured);
  return serveViews((call) => {
    if (call.method === "POST") {
      return post?.(call) ?? { status: 200, body: {} };
    }
    if (call.path.startsWith("/v1/views/completed")) {
      return { status: 200, body: done };
    }
    if (call.path.startsWith("/v1/views/needs-you") || call.path === "/v1/needs-you") {
      return { status: 200, body: waiting };
    }
    if (call.path.startsWith("/v1/views/operations")) {
      return { status: 200, body: measured };
    }
    return undefined;
  });
}

const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");

const queue = async () => within(await screen.findByRole("region", { name: "Work in review" }));

// rowOf finds the row of one work order in the queue.
async function rowOf(id: string) {
  const row = (await queue()).getAllByRole("row").find((found) => found.textContent?.includes(id));
  expect(row, `a row for ${id}`).toBeTruthy();
  return within(row as HTMLElement);
}

describe("the queue", () => {
  it("lists work in acceptance or integrating, the longest wait first, with its review, checks, authority, and what it waits on", async () => {
    serving();
    openPage(here("/reviews"));
    expect(await screen.findByRole("heading", { level: 1, name: "Reviews" })).toBeTruthy();
    const rows = (await queue()).getAllByRole("row").slice(1);
    expect(rows.map((row) => within(row).getAllByRole("cell")[0]?.textContent)).toEqual(["Trim the logwo-accept", "Trim the retry logwo-ruled", "Add the retry budgetwo-merging"]);
    const accept = within(rows[0] as HTMLElement);
    expect(accept.getByText("Waiting for acceptance")).toBeTruthy();
    expect(accept.getByText("1 cannot judge")).toBeTruthy();
    expect(accept.getByText("2 security notes")).toBeTruthy();
    expect(accept.getByText("No check")).toBeTruthy();
    expect(accept.getByText("person_approves")).toBeTruthy();
    expect(accept.getByText("Your word: the review could not judge criterion c1")).toBeTruthy();
    const ruled = within(rows[1] as HTMLElement);
    expect(ruled.getByText("Integrating")).toBeTruthy();
    expect(ruled.queryByText(/security note/)).toBeNull();
    expect(ruled.getByText("1 passed, 1 running")).toBeTruthy();
    expect(ruled.getByText("Your approval of the merge: required checks are still running on cab321: e2e")).toBeTruthy();
  });

  it("links each work order to its page, and counts the work in its tab", async () => {
    serving();
    openPage(here("/reviews"));
    const link = (await (await rowOf("wo-accept")).findByRole("link", { name: "Trim the log" })) as HTMLAnchorElement;
    expect(link.getAttribute("href")).toBe("/projects/default/work-order?id=wo-accept");
    const tabs = screen.getByRole("navigation", { name: "Reviews" });
    expect(within(tabs).getByRole("link", { name: /^Queue/ }).textContent).toBe("Queue3");
    expect(within(tabs).getByRole("link", { name: /^Pull requests/ }).textContent).toBe("Pull requests2");
    expect(within(tabs).getByRole("link", { name: /^Queue/ }).getAttribute("aria-current")).toBe("page");
  });

  it("offers each row the acts the read model lists for it, and nothing else", async () => {
    serving();
    openPage(here("/reviews"));
    const names = async (id: string) => (await rowOf(id)).queryAllByRole("button").map((button) => button.textContent);
    expect(await names("wo-accept")).toEqual(["Accept", "Rework"]);
    expect(await names("wo-ruled")).toEqual(["Approve merge"]);
    expect(await names("wo-merging")).toEqual([]);
    expect((await rowOf("wo-merging")).getByText("Nothing for you to do")).toBeTruthy();
  });

  it("offers no act when the read model lists none, whatever state the work stands in", async () => {
    serving({ needs: (view) => void (view.items = view.items.filter((item) => item.kind !== "work" && item.kind !== "approval")) });
    openPage(here("/reviews"));
    expect((await queue()).queryAllByRole("button")).toEqual([]);
  });

  it("accepts work in one press, with the same request the work order's card sends", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage(here("/reviews"));
    await userEvent.click((await rowOf("wo-accept")).getByRole("button", { name: "Accept wo-accept" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-accept/accept", project: "default", body: {}, csrf: "token-from-cookie" });
  });

  it("reads the work in review again after an act, so the row shows what the ledger says", async () => {
    const sent = serving();
    openPage(here("/reviews"));
    const button = (await rowOf("wo-accept")).getByRole("button", { name: "Accept wo-accept" });
    const reads = () => sent.filter((call) => call.method === "GET" && call.path === "/v1/views/completed").length;
    const before = reads();
    await userEvent.click(button);
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    await waitFor(() => expect(reads()).toBeGreaterThan(before));
  });

  it("asks why before it sends work back for rework, and sends the reason", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage(here("/reviews"));
    await userEvent.click((await rowOf("wo-accept")).getByRole("button", { name: "Rework wo-accept" }));
    const dialog = await screen.findByRole("dialog", { name: "Send wo-accept back for rework" });
    const send = within(dialog).getByRole("button", { name: "Send back for rework" }) as HTMLButtonElement;
    expect(send.disabled).toBe(true);
    await userEvent.type(within(dialog).getByRole("textbox", { name: /Why/ }), "The log still drops the last day.");
    expect(send.disabled).toBe(false);
    await userEvent.click(send);
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/work-orders/wo-accept/rework", body: { reason: "The log still drops the last day." }, csrf: "token-from-cookie" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("opens the merge card for a merge request, and approves the commit the review read", async () => {
    const sent = serving();
    openPage(here("/reviews"));
    await userEvent.click((await rowOf("wo-ruled")).getByRole("button", { name: "Approve merge of wo-ruled" }));
    const dialog = await screen.findByRole("dialog", { name: "Merge request for wo-ruled" });
    expect(within(dialog).getByText("Reviewed commit")).toBeTruthy();
    expect(dialog.textContent).toContain("#11");
    await userEvent.click(within(dialog).getByRole("button", { name: "Approve merge" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/approvals/generated-13/decide", body: { digest: "cab321", allow: true, reason: "" } });
  });

  it("denies a merge with a reason through the same card", async () => {
    const sent = serving();
    openPage(here("/reviews"));
    await userEvent.click((await rowOf("wo-ruled")).getByRole("button", { name: "Approve merge of wo-ruled" }));
    const dialog = await screen.findByRole("dialog", { name: "Merge request for wo-ruled" });
    await userEvent.type(within(dialog).getByRole("textbox"), "Wait for the e2e check.");
    await userEvent.click(within(dialog).getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ body: { digest: "cab321", allow: false, reason: "Wait for the e2e check." } });
  });

  it("names a held merge by what it asks", async () => {
    serving({
      needs: (view) => {
        const item = view.items.find((found) => found.kind === "approval" && found.approval.kind === "merge");
        if (item?.kind === "approval") {
          item.approval.subject = "held";
        }
      },
    });
    openPage(here("/reviews"));
    expect((await rowOf("wo-ruled")).getByRole("button", { name: "Retry merge of wo-ruled" })).toBeTruthy();
  });

  it("shows what the server refused, at the row, and leaves the act available", async () => {
    serving({ post: () => refusal(409, "not_in_acceptance", "wo-accept is not waiting for acceptance.") });
    openPage(here("/reviews"));
    const row = await rowOf("wo-accept");
    await userEvent.click(row.getByRole("button", { name: "Accept wo-accept" }));
    expect((await rowOf("wo-accept")).getByRole("alert").textContent).toContain("wo-accept is not waiting for acceptance.");
    expect(((await rowOf("wo-accept")).getByRole("button", { name: "Accept wo-accept" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("says nothing is in review when no work waits", async () => {
    serving({ completed: (view) => void (view.items = view.items.filter((item) => !["acceptance", "integrating"].includes(item.work_order.state))) });
    openPage(here("/reviews"));
    expect(await screen.findByText("Nothing is in review.")).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Work in review" })).toBeNull();
  });

  it("shows a model's words as text, so markup in a verdict never becomes the page's", async () => {
    serving({
      completed: (view) => {
        const item = view.items.find((found) => found.work_order.id === "wo-accept");
        if (item) {
          item.standing.why = "<img src=x onerror=alert(1)>";
        }
      },
    });
    openPage(here("/reviews"));
    const row = await rowOf("wo-accept");
    expect(row.getByText(/<img src=x onerror=alert\(1\)>/)).toBeTruthy();
    expect(document.querySelector("img[src='x']")).toBeNull();
  });
});

describe("the pull requests tab", () => {
  it("lists the open pull requests with their head commit, whether a review read it, checks, and mergeability", async () => {
    serving();
    openPage(here("/reviews?tab=pull-requests"));
    const table = within(await screen.findByRole("region", { name: "Open pull requests" }));
    const rows = table.getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    const ruled = within(rows[0] as HTMLElement);
    expect(ruled.getByText("#11")).toBeTruthy();
    expect(ruled.getByText("lane/lane-wo-ruled into main")).toBeTruthy();
    expect(ruled.getByText("cab321").getAttribute("title")).toBe("cab321");
    expect(ruled.getByText("Read by the review")).toBeTruthy();
    expect(ruled.getByText("1 passed, 1 running")).toBeTruthy();
    expect(ruled.getByText("Merges cleanly")).toBeTruthy();
    expect(ruled.getByRole("link", { name: "wo-ruled" }).getAttribute("href")).toBe("/projects/default/work-order?id=wo-ruled&tab=checks");
    expect(screen.getByRole("navigation", { name: "Reviews" }).querySelector("[aria-current='page']")?.textContent).toBe("Pull requests2");
  });

  it("says a pull request whose head no verdict read is not reviewed, and one that conflicts", async () => {
    serving({
      completed: (view) => {
        const pr = view.items.find((found) => found.work_order.id === "wo-ruled")?.standing.pull_request;
        if (pr) {
          pr.head_commit = "0ddba11";
          pr.mergeable = "CONFLICTING";
        }
      },
    });
    openPage(here("/reviews?tab=pull-requests"));
    const table = within(await screen.findByRole("region", { name: "Open pull requests" }));
    const ruled = within(table.getAllByRole("row").slice(1)[0] as HTMLElement);
    expect(ruled.getByText("Not read by a review")).toBeTruthy();
    expect(ruled.getByText("Conflicts with its base")).toBeTruthy();
  });

  it("shows the address of a pull request as text, never as a link taken from the ledger", async () => {
    serving();
    openPage(here("/reviews?tab=pull-requests"));
    const table = within(await screen.findByRole("region", { name: "Open pull requests" }));
    expect(table.getByText("https://forge.example/acme/app/pull/wo-ruled")).toBeTruthy();
    expect(document.querySelector("a[href^='https://forge.example']")).toBeNull();
  });

  it("says no pull request is open when none is", async () => {
    serving({ completed: (view) => void view.items.forEach((item) => delete item.standing.pull_request) });
    openPage(here("/reviews?tab=pull-requests"));
    expect(await screen.findByText("No pull request is open.")).toBeTruthy();
  });
});

describe("the agreement tab", () => {
  it("shows the counts and no rate while fewer than ten decisions stand, for the project and each repository", async () => {
    serving();
    openPage(here("/reviews?tab=agreement"));
    const project = within(await screen.findByRole("group", { name: "Review agreement of this project" }));
    const last = within(project.getByRole("group", { name: "Last 30 days" }));
    expect(last.getByText("Not enough decisions for a rate yet")).toBeTruthy();
    expect(last.getByText("Decisions", { selector: "dt" }).closest(".kv-row")?.querySelector("dd")?.textContent).toBe("1");
    expect(project.queryByText(/%/)).toBeNull();
    const repository = within(screen.getByRole("group", { name: /Review agreement of repository-1/ }));
    expect(repository.getByText(/person_approves/)).toBeTruthy();
  });

  it("shows the rate from ten decisions, with approved then rejected, rejected then approved, and later defects", async () => {
    serving({
      operations: (view) => {
        view.measures.review_agreement.workspace.last_30_days = {
          decisions: 12,
          agreed: 9,
          disagreed: 3,
          unknown: 0,
          agreement_rate: { numerator: 9, denominator: 12, value: 0.75 },
          approvals_overturned: 2,
          later_defects: 1,
          failures_overridden: 1,
        };
      },
    });
    openPage(here("/reviews?tab=agreement"));
    const project = within(await screen.findByRole("group", { name: "Review agreement of this project" }));
    const period = within(project.getByRole("group", { name: "Last 30 days" }));
    expect(period.getByText("75% (9 of 12)")).toBeTruthy();
    const fact = (name: string) => period.getByText(name, { selector: "dt" }).closest(".kv-row")?.querySelector("dd")?.textContent;
    expect(fact("Approved, then rejected")).toBe("2");
    expect(fact("Rejected, then approved")).toBe("1");
    expect(fact("Later defects")).toBe("1");
  });

  // The ledger lists the repositories by ID, which a fresh installation draws at random, so the tab orders them
  // by the name it shows, and by ID among repositories of one name.
  it("lists the repositories by name, then by ID, whatever their IDs", async () => {
    const measured = golden<Operations>("operations");
    const [first] = measured.measures.review_agreement.repositories;
    if (!first) {
      throw new Error("the golden has no repository's agreement");
    }
    const row = (id: string, level: string) => ({ ...first, repository_id: id, merge_authority: level });
    measured.measures.review_agreement.repositories = [row("id-a", "person_merges"), row("id-b", "person_approves"), row("id-c", "person_merges"), row("id-d", "review_approves")];
    const named = (id: string, name: string) => ({ id, name, path: `/src/${id}`, state: "active" });
    serveViews((call) => {
      if (call.path.startsWith("/v1/views/operations")) {
        return { status: 200, body: measured };
      }
      if (call.method === "GET" && call.path === "/v1/repositories") {
        return { status: 200, body: { repositories: [named("id-a", "web"), named("id-b", "harbor"), named("id-c", "api"), named("id-d", "api")] } };
      }
      return undefined;
    });
    openPage(here("/reviews?tab=agreement"));
    await screen.findByRole("group", { name: "Review agreement of web" });
    const shown = screen.getAllByRole("group", { name: /^Review agreement of (?!this project)/ }).map((group) => `${group.getAttribute("aria-label")} ${group.querySelector(".mono")?.textContent}`);
    expect(shown).toEqual(["Review agreement of api person_merges", "Review agreement of api review_approves", "Review agreement of harbor person_approves", "Review agreement of web person_merges"]);
  });

  it("states that the merge authority level never rises by itself", async () => {
    serving();
    openPage(here("/reviews?tab=agreement"));
    expect((await screen.findByText(/never changes a merge authority level by itself/)).textContent).toContain("Repositories");
  });

  it("says no decision followed a review when none did", async () => {
    serving({
      operations: (view) => {
        view.measures.review_agreement = { workspace: { last_30_days: { ...view.measures.review_agreement.workspace.last_30_days, decisions: 0, agreed: 0, agreement_rate: { numerator: 0, denominator: 0, value: null } }, all_time: { ...view.measures.review_agreement.workspace.all_time, decisions: 0, agreed: 0, agreement_rate: { numerator: 0, denominator: 0, value: null } } }, repositories: [] };
      },
    });
    openPage(here("/reviews?tab=agreement"));
    expect(await screen.findByText("No decision has followed a review yet.")).toBeTruthy();
  });
});

describe("the tabs", () => {
  it("move between the queue, the pull requests, and the agreement by link", async () => {
    serving();
    openPage(here("/reviews"));
    await queue();
    await userEvent.click(within(screen.getByRole("navigation", { name: "Reviews" })).getByRole("link", { name: /^Agreement/ }));
    expect(`${window.location.pathname}${window.location.search}`).toBe("/projects/default/reviews?tab=agreement");
    expect(await screen.findByRole("group", { name: "Review agreement of this project" })).toBeTruthy();
    await userEvent.click(within(screen.getByRole("navigation", { name: "Reviews" })).getByRole("link", { name: /^Queue/ }));
    expect(window.location.search).toBe("");
    expect(await queue()).toBeTruthy();
  });

  it("open the queue for a tab no one knows", async () => {
    serving();
    openPage(here("/reviews?tab=nowhere"));
    expect(await queue()).toBeTruthy();
  });
});
