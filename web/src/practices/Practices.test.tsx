import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { PracticesList, PracticesRequest, PracticesView } from "../api/practices";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, here, serveViews } from "../testing";
import { expiredWords } from "./logic";

// The practices check in the web UI reads what the API serves from a real ledger: the Overview's panel, the Repositories
// table's Findings column, and the Practices tab of a repository's record, with the dialogs that acknowledge a finding and
// withdraw the acknowledgement (FR-PRAC-6, FR-PRAC-10).

type Answer = { status: number; body?: unknown };

const envFinding = "env-ignored:ignore:.";
const secretFinding = "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f";

// listed is the golden list with its checked repository standing as the project's one registered repository, api.
function listed(change: (api: PracticesView, other: PracticesView) => void = () => undefined): PracticesList {
  const list = JSON.parse(JSON.stringify(golden<PracticesList>("practices")).replaceAll("repo-a", "id-api").replaceAll("widgets", "api")) as PracticesList;
  const [other, api] = list.repositories as [PracticesView, PracticesView];
  delete api.request;
  change(api, other);
  return list;
}

const openRequest = (over: Partial<PracticesRequest> = {}): PracticesRequest => ({
  id: "request-open",
  repository: "id-api",
  worker: "plan",
  worker_name: "plan",
  cause: "member",
  requested_by: "member-1",
  requested_at: 1790000000000,
  expires_at: 1790001800000,
  state: "offered",
  ...over,
});

function serving(list: PracticesList, own?: (call: Sent) => Answer | undefined) {
  return serveViews((call) => {
    const answered = own?.(call);
    if (answered) {
      return answered;
    }
    if (call.method === "GET" && /^\/v1\/practices(\?|$)/.test(call.path)) {
      return { status: 200, body: list };
    }
    return undefined;
  });
}

const posted = (sent: Sent[], path: string) => sent.filter((call) => call.method === "POST" && call.path === path);

describe("the Practices panel of the Overview", () => {
  it("reads Not checked yet for a repository no check reported", async () => {
    serving(listed());
    openPage(here(""));
    const panel = await screen.findByRole("region", { name: "Practices" });
    const row = (await within(panel).findByRole("link", { name: "gadgets" })).closest("li") as HTMLElement;
    expect(within(row).getByText("Not checked yet")).toBeTruthy();
  });

  it("shows a result's open findings by severity, its acknowledged and unknown counts, its commit, and a link to the tab", async () => {
    serving(listed());
    openPage(here(""));
    const panel = await screen.findByRole("region", { name: "Practices" });
    const link = await within(panel).findByRole("link", { name: "api" });
    expect(link.getAttribute("href")).toBe("/projects/default/repositories?repo=id-api&tab=practices");
    const row = link.closest("li") as HTMLElement;
    expect(within(row).getByText("high 1")).toBeTruthy();
    expect(within(row).queryByText(/^medium/)).toBeNull();
    expect(within(row).getByText("1 acknowledged")).toBeTruthy();
    expect(within(row).getByText("1 unknown")).toBeTruthy();
    expect(within(row).getByText("1111111")).toBeTruthy();
  });

  it("counts a finding that was acknowledged as no longer open, and keeps it in the count of what was", async () => {
    serving(
      listed((api) => {
        const result = api.result;
        if (result) {
          result.findings = result.findings.map((finding) => ({ ...finding, acknowledged: true }));
        }
      }),
    );
    openPage(here(""));
    const panel = await screen.findByRole("region", { name: "Practices" });
    const row = (await within(panel).findByRole("link", { name: "api" })).closest("li") as HTMLElement;
    expect(within(row).getByText("No open findings")).toBeTruthy();
    expect(within(row).getByText("2 acknowledged")).toBeTruthy();
  });

  it("asks for a check of every repository with Run check", async () => {
    const sent = serving(listed(), (call) => (call.method === "POST" && call.path === "/v1/practices/checks" ? { status: 202, body: { checks: [] } } : undefined));
    openPage(here(""));
    const panel = await screen.findByRole("region", { name: "Practices" });
    await userEvent.click(await within(panel).findByRole("button", { name: "Run check" }));
    await waitFor(() => expect(posted(sent, "/v1/practices/checks")).toHaveLength(1));
    expect(posted(sent, "/v1/practices/checks")[0]?.body).toEqual({ repositories: [] });
    expect(posted(sent, "/v1/practices/checks")[0]?.csrf).not.toBeNull();
  });

  it("says why a repository was not asked, in the server's words", async () => {
    serving(listed(), (call) =>
      call.method === "POST" && call.path === "/v1/practices/checks"
        ? { status: 202, body: { checks: [{ repository: "repo-b", name: "gadgets", reason: "no connected worker keeps a checkout of gadgets" }] } }
        : undefined,
    );
    openPage(here(""));
    const panel = await screen.findByRole("region", { name: "Practices" });
    await userEvent.click(await within(panel).findByRole("button", { name: "Run check" }));
    expect(await within(panel).findByText(/no connected worker keeps a checkout of gadgets/)).toBeTruthy();
  });

  it("shows Checking on the worker while a request is open, and reads the request once a second until it is reported", async () => {
    let reads = 0;
    const sent = serving(
      listed((api) => {
        api.request = openRequest();
      }),
      (call) => {
        if (call.method === "GET" && call.path === "/v1/practices/checks/request-open") {
          reads += 1;
          return { status: 200, body: openRequest({ state: reads < 2 ? "offered" : "reported" }) };
        }
        return undefined;
      },
    );
    openPage(here(""));
    const panel = await screen.findByRole("region", { name: "Practices" });
    expect(await within(panel).findByText("Checking on plan")).toBeTruthy();
    await waitFor(() => expect(reads).toBe(2), { timeout: 5000 });
    await waitFor(() => expect(within(panel).queryByText("Checking on plan")).toBeNull(), { timeout: 5000 });
    expect(sent.filter((call) => call.path === "/v1/practices" && call.method === "GET").length).toBeGreaterThan(1);
  });

  it("says a request expired, and that a later result still appears", async () => {
    serving(
      listed((api) => {
        api.request = openRequest();
      }),
      (call) => (call.method === "GET" && call.path === "/v1/practices/checks/request-open" ? { status: 200, body: openRequest({ state: "expired" }) } : undefined),
    );
    openPage(here(""));
    const panel = await screen.findByRole("region", { name: "Practices" });
    expect(await within(panel).findByText(expiredWords, {}, { timeout: 5000 })).toBeTruthy();
  });

  it("shows what a read refused, and leaves the rest of the page standing", async () => {
    serveViews((call) => (call.method === "GET" && call.path.startsWith("/v1/practices") ? { status: 500, body: { error: { code: "unavailable", message: "the practices could not be read" } } } : undefined));
    openPage(here(""));
    expect(await screen.findByText("the practices could not be read")).toBeTruthy();
    expect(screen.getByRole("heading", { level: 1, name: "Overview" })).toBeTruthy();
  });
});

describe("the Findings column of the Repositories table", () => {
  it("counts a repository's open findings by severity, and reads Not checked yet for one with no result", async () => {
    serving(listed());
    openPage(here("/repositories"));
    const table = await screen.findByRole("region", { name: "Repositories" });
    const row = (await within(table).findByRole("link", { name: "api" })).closest("tr") as HTMLElement;
    expect(within(row).getByText("high 1")).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "Findings" })).toBeTruthy();
    // The link names its repository, so a screen reader's list of links tells the rows apart.
    expect(within(row).getByRole("link", { name: "Findings of api: high 1" }).getAttribute("href")).toBe("/projects/default/repositories?repo=id-api&tab=practices");
  });
});

describe("the Practices tab of a repository's record", () => {
  const tab = async () => {
    openPage(here("/repositories?repo=id-api&tab=practices"));
    return screen.findByRole("region", { name: "Practices of api" });
  };

  it("names the commit, base branch, worker, and age of the result, and lists every practice in table order with its status", async () => {
    serving(listed());
    const record = await tab();
    expect(within(record).getByText("1111111")).toBeTruthy();
    expect(within(record).getByText("main")).toBeTruthy();
    expect(within(record).getByText("plan")).toBeTruthy();
    const table = within(record).getByRole("table", { name: /Practices/ });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((row) => within(row).getAllByRole("cell")[0]?.textContent)).toEqual([
      expect.stringContaining("env-ignored"),
      expect.stringContaining("secrets"),
      expect.stringContaining("ci"),
      expect.stringContaining("pre-commit"),
      expect.stringContaining("lockfile"),
      expect.stringContaining("license"),
    ]);
    const statusOf = (id: string) => within(rows.find((row) => row.textContent?.startsWith(id)) as HTMLElement).getAllByRole("cell")[1]?.textContent;
    expect(statusOf("env-ignored")).toContain("high");
    expect(statusOf("secrets")).toContain("Acknowledged");
    expect(statusOf("secrets")).toContain("high");
    expect(statusOf("ci")).toContain("Met");
    expect(statusOf("lockfile")).toContain("Not applicable");
    expect(statusOf("license")).toContain("Unknown");
    expect(within(table).getByText("the forge could not be read")).toBeTruthy();
  });

  it("links each template to its page and copies the command that prints it", async () => {
    const copy = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    serving(listed());
    const record = await tab();
    const table = within(record).getByRole("table", { name: /Practices/ });
    const row = within(table).getByText("env-ignored").closest("tr") as HTMLElement;
    expect(within(row).getByRole("link", { name: "repository/gitignore" }).getAttribute("href")).toBe("/templates?name=repository%2Fgitignore");
    await userEvent.click(within(row).getByRole("button", { name: "Copy ropeshift template show repository/gitignore" }));
    expect(copy).toHaveBeenCalledWith("ropeshift template show repository/gitignore");
  });

  it("shows a finding's ID and detail, and for a secret only its rule, file, line, and commit", async () => {
    serving(listed());
    const record = await tab();
    const findings = within(record).getByRole("table", { name: /Findings/ });
    const env = within(findings).getByText(envFinding).closest("tr") as HTMLElement;
    expect(within(env).getByText("`.env` is not ignored")).toBeTruthy();
    const secret = within(findings).getByText(secretFinding).closest("tr") as HTMLElement;
    for (const text of ["github_token", "config/dev.yaml", "3", "aaaaaaaaaaaa"]) {
      expect(within(secret).getByText(text)).toBeTruthy();
    }
    expect(secret.textContent).not.toContain("1a2b3c4d5e6f1a2b3c4d5e6f");
  });

  it("opens a dialog with the finding's ID and why, refuses an empty reason, and sends the acknowledgement with one", async () => {
    const sent = serving(listed(), (call) =>
      call.method === "POST" && call.path === "/v1/practices/acknowledgements"
        ? { status: 201, body: { finding: envFinding, reason: "the folder is not a project root", member: "member-1", acknowledged_at: "2026-11-19T10:00:00Z" } }
        : undefined,
    );
    const record = await tab();
    const findings = within(record).getByRole("table", { name: /Findings/ });
    const env = within(findings).getByText(envFinding).closest("tr") as HTMLElement;
    await userEvent.click(within(env).getByRole("button", { name: "Acknowledge" }));
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    expect(within(dialog).getByText(envFinding)).toBeTruthy();
    expect(within(dialog).getByText(/An env file holds credentials/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Acknowledge" }));
    expect(within(dialog).getByText("Say why this finding stays open.")).toBeTruthy();
    expect(posted(sent, "/v1/practices/acknowledgements")).toHaveLength(0);
    await userEvent.type(within(dialog).getByLabelText("Reason"), "the folder is not a project root");
    await userEvent.click(within(dialog).getByRole("button", { name: "Acknowledge" }));
    await waitFor(() => expect(posted(sent, "/v1/practices/acknowledgements")).toHaveLength(1));
    expect(posted(sent, "/v1/practices/acknowledgements")[0]?.body).toEqual({ repository: "id-api", finding: envFinding, reason: "the folder is not a project root" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("refuses a reason over 2 KiB before it is sent", async () => {
    const sent = serving(listed());
    const record = await tab();
    const env = within(within(record).getByRole("table", { name: /Findings/ })).getByText(envFinding).closest("tr") as HTMLElement;
    await userEvent.click(within(env).getByRole("button", { name: "Acknowledge" }));
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    await userEvent.click(within(dialog).getByLabelText("Reason"));
    await userEvent.paste("x".repeat(2049));
    await userEvent.click(within(dialog).getByRole("button", { name: "Acknowledge" }));
    expect(within(dialog).getByText("The reason is over 2 KiB. Shorten it.")).toBeTruthy();
    expect(posted(sent, "/v1/practices/acknowledgements")).toHaveLength(0);
  });

  it("keeps the server's refusal in the dialog", async () => {
    serving(listed(), (call) =>
      call.method === "POST" && call.path === "/v1/practices/acknowledgements" ? { status: 409, body: { error: { code: "conflict", message: "the finding is already acknowledged" } } } : undefined,
    );
    const record = await tab();
    const env = within(within(record).getByRole("table", { name: /Findings/ })).getByText(envFinding).closest("tr") as HTMLElement;
    await userEvent.click(within(env).getByRole("button", { name: "Acknowledge" }));
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    await userEvent.type(within(dialog).getByLabelText("Reason"), "because");
    await userEvent.click(within(dialog).getByRole("button", { name: "Acknowledge" }));
    expect(await within(dialog).findByText("the finding is already acknowledged")).toBeTruthy();
  });

  it("shows an acknowledged finding with its reason, member, and time, and withdraws it from a dialog", async () => {
    const sent = serving(listed(), (call) =>
      call.method === "POST" && call.path === "/v1/practices/acknowledgements/withdraw"
        ? { status: 200, body: { finding: secretFinding, reason: "a token the parser tests read", member: "member-1", acknowledged_at: "2026-09-21T14:13:20Z" } }
        : undefined,
    );
    const record = await tab();
    const secret = within(within(record).getByRole("table", { name: /Findings/ })).getByText(secretFinding).closest("tr") as HTMLElement;
    expect(within(secret).getByText("Acknowledged")).toBeTruthy();
    expect(within(secret).getByText("a token the parser tests read")).toBeTruthy();
    expect(within(secret).getByText(/member-1/)).toBeTruthy();
    await userEvent.click(within(secret).getByRole("button", { name: "Withdraw" }));
    const dialog = await screen.findByRole("dialog", { name: "Withdraw acknowledgement" });
    expect(within(dialog).getByText(secretFinding)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Withdraw acknowledgement" }));
    await waitFor(() => expect(posted(sent, "/v1/practices/acknowledgements/withdraw")).toHaveLength(1));
    expect(posted(sent, "/v1/practices/acknowledgements/withdraw")[0]?.body).toEqual({ repository: "id-api", finding: secretFinding });
  });

  it("asks for a check of this repository only, and reads Checking on the worker until it is reported", async () => {
    let reads = 0;
    const list = listed();
    const sent = serving(list, (call) => {
      if (call.method === "POST" && call.path === "/v1/practices/checks") {
        const api = list.repositories[1];
        if (api) {
          api.request = openRequest();
        }
        return { status: 202, body: { checks: [{ repository: "id-api", name: "api", request: openRequest() }] } };
      }
      if (call.method === "GET" && call.path === "/v1/practices/checks/request-open") {
        reads += 1;
        return { status: 200, body: openRequest({ state: reads < 2 ? "offered" : "reported" }) };
      }
      return undefined;
    });
    const record = await tab();
    await userEvent.click(within(record).getByRole("button", { name: "Run check" }));
    await waitFor(() => expect(posted(sent, "/v1/practices/checks")).toHaveLength(1));
    expect(posted(sent, "/v1/practices/checks")[0]?.body).toEqual({ repositories: ["id-api"] });
    expect(await within(record).findByText("Checking on plan")).toBeTruthy();
    await waitFor(() => expect(reads).toBeGreaterThan(1), { timeout: 5000 });
    await waitFor(() => expect(within(record).queryByText("Checking on plan")).toBeNull(), { timeout: 5000 });
  });

  it("shows Checking on the worker while a request is open, and the expired words when it expired", async () => {
    serving(
      listed((api) => {
        api.request = openRequest();
      }),
      (call) => (call.method === "GET" && call.path === "/v1/practices/checks/request-open" ? { status: 200, body: openRequest({ state: "expired" }) } : undefined),
    );
    const record = await tab();
    expect(await within(record).findByText("Checking on plan")).toBeTruthy();
    expect(await within(record).findByText(expiredWords, {}, { timeout: 5000 })).toBeTruthy();
  });

  it("reads Not checked yet for a repository with no result, and still offers Run check", async () => {
    serving(
      listed((api) => {
        delete api.result;
      }),
    );
    const record = await tab();
    expect(within(record).getByText("Not checked yet")).toBeTruthy();
    expect(within(record).getByRole("button", { name: "Run check" })).toBeTruthy();
  });

  it("keeps focus on the finding's button when its acknowledgement is made, and again when it is withdrawn", async () => {
    const list = listed();
    const api = list.repositories[1] as PracticesView;
    serving(list, (call) => {
      const result = api.result;
      if (call.method === "POST" && call.path === "/v1/practices/acknowledgements" && result) {
        const body = call.body as { finding: string; reason: string };
        const held = { finding: body.finding, reason: body.reason, member: "member-1", acknowledged_at: "2026-11-19T10:00:00Z" };
        result.findings = result.findings.map((one) => (one.id === body.finding ? { ...one, acknowledged: true, acknowledgement: held } : one));
        return { status: 201, body: held };
      }
      if (call.method === "POST" && call.path === "/v1/practices/acknowledgements/withdraw" && result) {
        const body = call.body as { finding: string };
        result.findings = result.findings.map((one) => (one.id === body.finding ? { ...one, acknowledged: undefined, acknowledgement: undefined } : one));
        return { status: 200, body: {} };
      }
      return undefined;
    });
    const record = await tab();
    const rowOf = () => within(within(record).getByRole("table", { name: /Findings/ })).getByText(envFinding).closest("tr") as HTMLElement;
    await userEvent.click(within(rowOf()).getByRole("button", { name: "Acknowledge" }));
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    await userEvent.type(within(dialog).getByLabelText("Reason"), "because");
    await userEvent.click(within(dialog).getByRole("button", { name: "Acknowledge" }));
    await waitFor(() => expect(within(rowOf()).getByRole("button", { name: "Withdraw" })).toBe(document.activeElement));
    await userEvent.click(within(rowOf()).getByRole("button", { name: "Withdraw" }));
    await userEvent.click(await screen.findByRole("button", { name: "Withdraw acknowledgement" }));
    await waitFor(() => expect(within(rowOf()).getByRole("button", { name: "Acknowledge" })).toBe(document.activeElement));
  });

  it("lists an acknowledgement of a finding the latest result does not hold, and withdraws it", async () => {
    const orphan = { finding: "lockfile:npm", reason: "the lockfile is generated", member: "member-2", acknowledged_at: "2026-11-19T10:00:00Z" };
    const list = listed((api) => {
      api.acknowledgement = [...(api.acknowledgement ?? []), orphan];
    });
    const api = list.repositories[1] as PracticesView;
    const sent = serving(list, (call) => {
      if (call.method === "POST" && call.path === "/v1/practices/acknowledgements/withdraw") {
        api.acknowledgement = (api.acknowledgement ?? []).filter((one) => one.finding !== (call.body as { finding: string }).finding);
        return { status: 200, body: orphan };
      }
      return undefined;
    });
    const record = await tab();
    const held = await within(record).findByRole("table", { name: /not in the latest result/ });
    const row = within(held).getByText("lockfile:npm").closest("tr") as HTMLElement;
    expect(within(row).getByText("the lockfile is generated")).toBeTruthy();
    expect(within(row).getByText(/member-2/)).toBeTruthy();
    expect(within(held).queryByText(secretFinding)).toBeNull();
    await userEvent.click(within(row).getByRole("button", { name: "Withdraw" }));
    const dialog = await screen.findByRole("dialog", { name: "Withdraw acknowledgement" });
    expect(within(dialog).getByText("lockfile:npm")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Withdraw acknowledgement" }));
    await waitFor(() => expect(posted(sent, "/v1/practices/acknowledgements/withdraw")).toHaveLength(1));
    expect(posted(sent, "/v1/practices/acknowledgements/withdraw")[0]?.body).toEqual({ repository: "id-api", finding: "lockfile:npm" });
    await waitFor(() => expect(within(record).queryByText("lockfile:npm")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(within(record).getByRole("heading", { name: "Practices of api" })));
  });

  it("lists an acknowledgement for a repository no check has reported", async () => {
    serving(
      listed((api) => {
        delete api.result;
        api.acknowledgement = [{ finding: "codeowners", reason: "the whole team owns it", member: "member-1", acknowledged_at: "2026-11-19T10:00:00Z" }];
      }),
    );
    const record = await tab();
    expect(await within(record).findByText("the whole team owns it")).toBeTruthy();
    expect(within(record).getByText("Not checked yet")).toBeTruthy();
  });

  it("is reached by the Practices tab of the record, and the Settings tab returns to the form", async () => {
    serving(listed());
    openPage(here("/repositories?repo=id-api"));
    await screen.findByRole("region", { name: "api" });
    const tabs = screen.getByRole("navigation", { name: "Record" });
    await userEvent.click(within(tabs).getByRole("link", { name: "Practices" }));
    expect(await screen.findByRole("region", { name: "Practices of api" })).toBeTruthy();
    expect(window.location.search).toContain("tab=practices");
    await userEvent.click(within(screen.getByRole("navigation", { name: "Record" })).getByRole("link", { name: "Settings" }));
    expect(await screen.findByRole("region", { name: "api" })).toBeTruthy();
  });
});

describe("the dialogs the command palette opens", () => {
  it("offers a picker over the repositories and findings that are open, and acknowledges the one chosen", async () => {
    const sent = serving(listed(), (call) =>
      call.method === "POST" && call.path === "/v1/practices/acknowledgements" ? { status: 201, body: { finding: envFinding, reason: "r", member: "m", acknowledged_at: "2026-11-19T10:00:00Z" } } : undefined,
    );
    openPage(here("/repositories?dialog=acknowledge"));
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    const repository = within(dialog).getByLabelText("Repository") as HTMLSelectElement;
    expect([...repository.options].map((option) => option.textContent)).toEqual(["api"]);
    const finding = within(dialog).getByLabelText("Finding") as HTMLSelectElement;
    expect([...finding.options].map((option) => option.value)).toEqual([envFinding]);
    await userEvent.type(within(dialog).getByLabelText("Reason"), "because");
    await userEvent.click(within(dialog).getByRole("button", { name: "Acknowledge" }));
    await waitFor(() => expect(posted(sent, "/v1/practices/acknowledgements")).toHaveLength(1));
    expect(posted(sent, "/v1/practices/acknowledgements")[0]?.body).toEqual({ repository: "id-api", finding: envFinding, reason: "because" });
  });

  it("offers a picker over the acknowledged findings to withdraw", async () => {
    const sent = serving(listed(), (call) =>
      call.method === "POST" && call.path === "/v1/practices/acknowledgements/withdraw" ? { status: 200, body: { finding: secretFinding, reason: "r", member: "m", acknowledged_at: "2026-11-19T10:00:00Z" } } : undefined,
    );
    openPage(here("/repositories?dialog=withdraw"));
    const dialog = await screen.findByRole("dialog", { name: "Withdraw acknowledgement" });
    const finding = within(dialog).getByLabelText("Finding") as HTMLSelectElement;
    expect([...finding.options].map((option) => option.value)).toEqual([secretFinding]);
    await userEvent.click(within(dialog).getByRole("button", { name: "Withdraw acknowledgement" }));
    await waitFor(() => expect(posted(sent, "/v1/practices/acknowledgements/withdraw")).toHaveLength(1));
  });

  it("says there is nothing to acknowledge when no finding is open", async () => {
    serving(
      listed((api, other) => {
        if (api.result) {
          api.result.findings = [];
        }
        delete other.result;
      }),
    );
    openPage(here("/repositories?dialog=acknowledge"));
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    expect(within(dialog).getByText("No repository has an open finding.")).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "Acknowledge" })).toBeNull();
  });
});
