import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import { refusal, serveViews } from "../testing";

const specLink = "/document?path=docs%2Fspecs%2Fretry.md&instance=memory";

describe("the document lists", () => {
  it("lists specs on their own, each linked to its reading", async () => {
    serveViews();
    openPage("/specs");
    expect(await screen.findByRole("heading", { level: 1, name: "Specs" })).toBeTruthy();
    const link = await screen.findByRole("link", { name: "The retry spec" });
    expect(link.getAttribute("href")).toBe(specLink);
    expect(screen.queryByRole("link", { name: "The retry budget" })).toBeNull();
  });

  it("lists designs on their own", async () => {
    serveViews();
    openPage("/designs");
    expect(await screen.findByRole("link", { name: "The retry budget" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "The retry spec" })).toBeNull();
  });

  it("lists every other document, so none goes missing", async () => {
    serveViews();
    openPage("/documents");
    expect(await screen.findByRole("link", { name: "Notes from the first run" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "The retry spec" })).toBeNull();
  });

  it("says where a document lives", async () => {
    serveViews();
    openPage("/specs");
    const row = (await screen.findByRole("link", { name: "The retry spec" })).closest("li");
    expect(row?.textContent).toContain("docs/specs/retry.md");
  });

  it("says so when a kind holds nothing", async () => {
    serveViews((call) =>
      call.path.startsWith("/v1/views/documents") ? { status: 200, body: { as_of: 1, revision: 1, documents: [], requirements: [] } } : undefined,
    );
    openPage("/designs");
    expect(await screen.findByText("No designs yet.")).toBeTruthy();
  });
});

describe("requirements", () => {
  it("lists each requirement by its key, with the documents that hold them", async () => {
    serveViews();
    openPage("/requirements");
    const row = (await screen.findByText("FR-RETRY-1")).closest("li");
    expect(row?.textContent).toContain("A lane must stop retrying at its budget.");
    expect(row?.textContent).toContain("Version 1");
    expect(screen.getByRole("link", { name: "What retries must do" })).toBeTruthy();
  });

  it("opens to one requirement from a contract's link", async () => {
    serveViews();
    openPage("/requirements?key=FR-RETRY-1");
    expect(await screen.findByRole("heading", { level: 1, name: "FR-RETRY-1" })).toBeTruthy();
    expect(await screen.findByText("A lane must stop retrying at its budget.")).toBeTruthy();
    expect(screen.queryByText("FR-RETRY-2")).toBeNull();
    expect(screen.getByRole("link", { name: "All requirements" }).getAttribute("href")).toBe("/requirements");
  });

  it("says so when a contract names a requirement nobody recorded", async () => {
    serveViews();
    openPage("/requirements?key=FR-MISSING-9");
    expect(await screen.findByRole("heading", { level: 1, name: "No such requirement" })).toBeTruthy();
  });
});

describe("reading a document", () => {
  const open = async () => {
    const sent = serveViews();
    openPage(specLink);
    await screen.findByRole("heading", { level: 1, name: "The retry spec" });
    return sent;
  };

  it("asks for the one document the address names", async () => {
    const sent = await open();
    expect(sent.some((call) => call.path === "/v1/documents/read?path=docs%2Fspecs%2Fretry.md&instance=memory")).toBe(true);
  });

  it("says plainly that the text came from outside", async () => {
    await open();
    expect(screen.getByRole("note").textContent).toContain("repository");
  });

  it("states where the text came from and which version it is", async () => {
    await open();
    expect(screen.getByText("docs/specs/retry.md")).toBeTruthy();
    expect(screen.getByText("docs/specs/retry.md@4")).toBeTruthy();
    expect(screen.getByText("Spec")).toBeTruthy();
  });

  it("lays the text out as a reader expects", async () => {
    await open();
    const body = screen.getByRole("region", { name: "The document" });
    expect(within(body).getByRole("heading", { name: "Links and pictures" })).toBeTruthy();
    expect(within(body).getByText("The first rule").closest("ul")).not.toBeNull();
    expect(within(body).getByText("code").tagName).toBe("CODE");
  });

  it("lists versions and reads an earlier one", async () => {
    const sent = await open();
    const versions = screen.getByRole("region", { name: "Versions" });
    await waitFor(() => expect(within(versions).getByRole("link", { name: /^1\b/ })).toBeTruthy());
    await userEvent.click(within(versions).getByRole("link", { name: /^1\b/ }));
    await waitFor(() =>
      expect(sent.some((call) => call.path === "/v1/documents/read?path=docs%2Fspecs%2Fretry.md&instance=memory&version=1")).toBe(true),
    );
    expect(window.location.search).toContain("version=1");
  });

  it("says when a document cannot be read", async () => {
    serveViews((call) => (call.path.startsWith("/v1/documents") ? refusal(404, "not_found", "document absent.md: not found") : undefined));
    openPage("/document?path=absent.md");
    expect(await screen.findByRole("heading", { level: 1, name: "No such document" })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("not found");
  });

  it("says so when the address names no document", async () => {
    serveViews();
    openPage("/document");
    expect(await screen.findByRole("heading", { level: 1, name: "No such document" })).toBeTruthy();
  });
});
