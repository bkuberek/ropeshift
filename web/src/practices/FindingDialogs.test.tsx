import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { PracticeFinding, PracticesView } from "../api/practices";
import type { Sent } from "../testing";
import { serveApi } from "../testing";
import { AcknowledgeDialog, WithdrawDialog } from "./FindingDialogs";

// The dialogs read the list the page re-reads on every ledger event and every poll, so a pair a member chose must not move
// when the list changes under it.

const finding = (id: string, over: Partial<PracticeFinding> = {}): PracticeFinding => ({ id, practice: id.split(":")[0] ?? id, severity: "low", detail: `detail of ${id}`, why: `why of ${id}`, fix: "", ...over });
const acknowledged = (id: string, reason = "because"): PracticeFinding => finding(id, { acknowledged: true, acknowledgement: { finding: id, reason, member: "member-1", acknowledged_at: "2026-11-19T10:00:00Z" } });

const viewOf = (repository: string, name: string, findings: PracticeFinding[], extra: PracticesView["acknowledgement"] = undefined): PracticesView => ({
  repository,
  name,
  result: { request: "r", commit: "abc", base_branch: "main", worker: "w", checked_at: "2026-11-19T00:00:00Z", table_version: 1, rules_version: 1, practices: [], findings },
  acknowledgement: extra,
});

const noop = () => undefined;
const serve = () => serveApi((call) => (call.method === "POST" ? { status: 200, body: {} } : undefined));
const posts = (sent: Sent[]) => sent.filter((call) => call.method === "POST");

describe("the Acknowledge finding dialog", () => {
  it("keeps the finding the member chose when a read leaves it out, says so, and sends nothing", async () => {
    const sent = serve();
    const first = [viewOf("repo-1", "api", [finding("codeowners"), finding("security-policy")])];
    const { rerender } = render(<AcknowledgeDialog views={first} onClose={noop} after={noop} />);
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    expect((within(dialog).getByLabelText("Finding") as HTMLSelectElement).value).toBe("codeowners");
    await userEvent.type(within(dialog).getByLabelText("Reason"), "because");
    rerender(<AcknowledgeDialog views={[viewOf("repo-1", "api", [finding("security-policy")])]} onClose={noop} after={noop} />);
    expect(await within(dialog).findByText("This finding is no longer open.")).toBeTruthy();
    expect((within(dialog).getByLabelText("Finding") as HTMLSelectElement).value).toBe("codeowners");
    const send = within(dialog).getByRole("button", { name: "Acknowledge" }) as HTMLButtonElement;
    expect(send.disabled).toBe(true);
    await userEvent.click(send);
    expect(posts(sent)).toHaveLength(0);
  });

  it("keeps the chosen repository when it leaves the list altogether", async () => {
    serve();
    const both = [viewOf("repo-1", "api", [finding("codeowners")]), viewOf("repo-2", "web", [finding("security-policy")])];
    const { rerender } = render(<AcknowledgeDialog views={both} onClose={noop} after={noop} />);
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    expect((within(dialog).getByLabelText("Repository") as HTMLSelectElement).value).toBe("repo-1");
    rerender(<AcknowledgeDialog views={[viewOf("repo-2", "web", [finding("security-policy")])]} onClose={noop} after={noop} />);
    expect(await within(dialog).findByText("This finding is no longer open.")).toBeTruthy();
    expect((within(dialog).getByLabelText("Repository") as HTMLSelectElement).value).toBe("repo-1");
    expect((within(dialog).getByRole("button", { name: "Acknowledge" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("lets the member pick another finding once the chosen one is gone, and send that one", async () => {
    const sent = serve();
    const { rerender } = render(<AcknowledgeDialog views={[viewOf("repo-1", "api", [finding("codeowners"), finding("security-policy")])]} onClose={noop} after={noop} />);
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    rerender(<AcknowledgeDialog views={[viewOf("repo-1", "api", [finding("security-policy")])]} onClose={noop} after={noop} />);
    await within(dialog).findByText("This finding is no longer open.");
    await userEvent.selectOptions(within(dialog).getByLabelText("Finding"), "security-policy");
    expect(within(dialog).queryByText("This finding is no longer open.")).toBeNull();
    await userEvent.type(within(dialog).getByLabelText("Reason"), "because");
    await userEvent.click(within(dialog).getByRole("button", { name: "Acknowledge" }));
    await waitFor(() => expect(posts(sent)).toHaveLength(1));
    expect(posts(sent)[0]?.body).toEqual({ repository: "repo-1", finding: "security-policy", reason: "because" });
  });

  it("keeps a finding a row named when the list no longer holds it", async () => {
    serve();
    const { rerender } = render(<AcknowledgeDialog views={[viewOf("repo-1", "api", [finding("codeowners"), finding("security-policy")])]} repository="repo-1" finding="security-policy" onClose={noop} after={noop} />);
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    expect(within(dialog).getByText("security-policy")).toBeTruthy();
    rerender(<AcknowledgeDialog views={[viewOf("repo-1", "api", [finding("codeowners")])]} repository="repo-1" finding="security-policy" onClose={noop} after={noop} />);
    expect(await within(dialog).findByText("This finding is no longer open.")).toBeTruthy();
    expect((within(dialog).getByRole("button", { name: "Acknowledge" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("opens on its first field, the Repository picker, when the palette opened it", async () => {
    serve();
    render(<AcknowledgeDialog views={[viewOf("repo-1", "api", [finding("codeowners")])]} onClose={noop} after={noop} />);
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    expect(document.activeElement).toBe(within(dialog).getByLabelText("Repository"));
  });

  it("opens on its Reason when a row named the finding", async () => {
    serve();
    render(<AcknowledgeDialog views={[viewOf("repo-1", "api", [finding("codeowners")])]} repository="repo-1" finding="codeowners" onClose={noop} after={noop} />);
    const dialog = await screen.findByRole("dialog", { name: "Acknowledge finding" });
    expect(document.activeElement).toBe(within(dialog).getByLabelText("Reason"));
  });
});

describe("the Withdraw acknowledgement dialog", () => {
  it("keeps the acknowledgement the member chose when a read leaves it out, says so, and sends nothing", async () => {
    const sent = serve();
    const { rerender } = render(<WithdrawDialog views={[viewOf("repo-1", "api", [acknowledged("codeowners"), acknowledged("security-policy")])]} onClose={noop} after={noop} />);
    const dialog = await screen.findByRole("dialog", { name: "Withdraw acknowledgement" });
    expect((within(dialog).getByLabelText("Finding") as HTMLSelectElement).value).toBe("codeowners");
    rerender(<WithdrawDialog views={[viewOf("repo-1", "api", [finding("codeowners"), acknowledged("security-policy")])]} onClose={noop} after={noop} />);
    expect(await within(dialog).findByText("This acknowledgement was already withdrawn.")).toBeTruthy();
    expect((within(dialog).getByLabelText("Finding") as HTMLSelectElement).value).toBe("codeowners");
    const send = within(dialog).getByRole("button", { name: "Withdraw acknowledgement" }) as HTMLButtonElement;
    expect(send.disabled).toBe(true);
    await userEvent.click(send);
    expect(posts(sent)).toHaveLength(0);
  });

  it("offers an acknowledgement whose finding the latest result does not hold", async () => {
    const sent = serve();
    const orphan = { finding: "lockfile:npm", reason: "the lockfile is generated", member: "member-1", acknowledged_at: "2026-11-19T10:00:00Z" };
    render(<WithdrawDialog views={[viewOf("repo-1", "api", [], [orphan])]} onClose={noop} after={noop} />);
    const dialog = await screen.findByRole("dialog", { name: "Withdraw acknowledgement" });
    expect((within(dialog).getByLabelText("Finding") as HTMLSelectElement).value).toBe("lockfile:npm");
    expect(within(dialog).getByText(/the lockfile is generated/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Withdraw acknowledgement" }));
    await waitFor(() => expect(posts(sent)).toHaveLength(1));
    expect(posts(sent)[0]?.body).toEqual({ repository: "repo-1", finding: "lockfile:npm" });
  });
});
