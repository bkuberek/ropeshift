import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { DocumentKind, Documents, RecordedRead } from "../api/types";
import { openPage } from "../opening";
import { golden, refusal, serveViews } from "../testing";

const specLink = "/document?path=docs%2Fspecs%2Fretry.md&instance=memory";

function serve(change: (view: Documents) => void) {
  const view = golden<Documents>("documents");
  change(view);
  serveViews((call) => (call.path.startsWith("/v1/views/documents") ? { status: 200, body: view } : undefined));
}

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

  it("keeps a document of a kind this build has no words for", async () => {
    const view = golden<Documents>("documents");
    const notes = view.documents.find((document) => document.path === "notes.md");
    if (notes) {
      notes.kind = "runbook" as DocumentKind;
    }
    serveViews((call) => (call.path.startsWith("/v1/views/documents") ? { status: 200, body: view } : undefined));
    openPage("/documents");
    expect(await screen.findByRole("link", { name: "Notes from the first run" })).toBeTruthy();
  });

  it("says where a document lives", async () => {
    serveViews();
    openPage("/specs");
    const row = (await screen.findByRole("link", { name: "The retry spec" })).closest("li");
    expect(row?.textContent).toContain("docs/specs/retry.md");
  });

  it("says so when a kind holds nothing", async () => {
    serveViews((call) =>
      call.path.startsWith("/v1/views/documents") ? { status: 200, body: { as_of: 1, revision: 1, documents: [], requirements: [], plans: [] } } : undefined,
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

  it("tells a read that failed from a document that is not there", async () => {
    serveViews((call) => (call.path.startsWith("/v1/documents") ? refusal(503, "unavailable", "The repository did not answer.") : undefined));
    openPage("/document?path=docs%2Fspecs%2Fretry.md");
    expect((await screen.findByRole("alert")).textContent).toContain("did not answer");
    expect(screen.queryByRole("heading", { name: "No such document" })).toBeNull();
    expect(screen.getByRole("heading", { level: 1, name: "Document" })).toBeTruthy();
  });

  it("says so when the address names no document", async () => {
    serveViews();
    openPage("/document");
    expect(await screen.findByRole("heading", { level: 1, name: "No such document" })).toBeTruthy();
  });
});

const recordedLink = "/document?id=spec%3Afactory-api";

describe("the documents a plan recorded", () => {
  it("lists a plan's specs from the ledger, each with its version and where it stands in review", async () => {
    serveViews();
    openPage("/specs");
    const plan = await screen.findByRole("region", { name: "The factory floor" });
    const link = within(plan).getByRole("link", { name: "The factory API" });
    expect(link.getAttribute("href")).toBe(recordedLink);
    const row = link.closest("li");
    expect(row?.textContent).toContain("Version 2");
    expect(row?.textContent).toContain("Waits for approval in plan version 6");
    expect(row?.textContent).toContain("plans/factory/spec-api.md");
  });

  it("lists a file a plan recorded once, and keeps every other file of the repository", async () => {
    serveViews();
    openPage("/specs");
    await screen.findByRole("region", { name: "The factory floor" });
    expect(screen.getAllByRole("link", { name: "The factory API" })).toHaveLength(1);
    const files = screen.getByRole("region", { name: "Files no plan records" });
    expect(within(files).getByRole("link", { name: "The retry spec" }).getAttribute("href")).toBe(specLink);
  });

  it("keeps the plain list when no plan recorded anything", async () => {
    serve((view) => {
      view.plans = [];
      for (const document of view.documents) {
        delete document.record;
      }
    });
    openPage("/specs");
    expect(await screen.findByRole("link", { name: "The retry spec" })).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Files no plan records" })).toBeNull();
  });

  it("puts architecture documents and experience designs with the designs", async () => {
    serveViews();
    openPage("/designs");
    const plan = await screen.findByRole("region", { name: "The factory floor" });
    const row = within(plan).getByRole("link", { name: "How the factory is built" }).closest("li");
    expect(row?.textContent).toContain("Architecture");
    expect(row?.textContent).toContain("Version 1");
    expect(screen.getByRole("link", { name: "The retry budget" })).toBeTruthy();
  });

  it("puts milestones with the other documents, and says an approved version is approved", async () => {
    serveViews();
    openPage("/documents");
    const plan = await screen.findByRole("region", { name: "The factory floor" });
    const row = within(plan).getByRole("link", { name: "Build the core" }).closest("li");
    expect(row?.textContent).toContain("Approved with plan version 5");
    expect(screen.getByRole("link", { name: "Notes from the first run" })).toBeTruthy();
  });

  it("says a version no plan approval lists stands in none", async () => {
    serve((view) => {
      const spec = view.plans[0]?.documents.find((document) => document.id === "spec:factory-api");
      if (spec?.versions[0]) {
        spec.versions[0] = { ...spec.versions[0], review: "none", plan_version: undefined };
      }
    });
    openPage("/specs");
    const row = (await screen.findByRole("link", { name: "The factory API" })).closest("li");
    expect(row?.textContent).toContain("In no plan approval");
  });

  it("lists the requirements documents a plan recorded", async () => {
    serveViews();
    openPage("/requirements");
    const holding = await screen.findByRole("region", { name: "Documents that hold them" });
    expect(within(holding).getByRole("link", { name: "What the factory must do" }).getAttribute("href")).toBe(
      "/document?id=requirements%3Afactory",
    );
    expect(within(holding).getByRole("link", { name: "What retries must do" })).toBeTruthy();
  });
});

describe("tracing requirements to work", () => {
  const traces = async () => {
    serveViews();
    openPage("/requirements");
    return screen.findByRole("list", { name: "Requirements of The factory floor, traced to work" });
  };
  const traceOf = (list: HTMLElement, key: string) => {
    const found = within(list)
      .getAllByRole("listitem")
      .find((item) => item.parentElement === list && item.querySelector(".trace-key")?.textContent === key);
    if (!found) {
      throw new Error(`no trace of ${key}`);
    }
    return found;
  };

  it("traces each requirement to the criteria that serve it and the work that carries them", async () => {
    const list = await traces();
    const trace = traceOf(list, "FAC-2");
    expect(trace.textContent).toContain("Shared work is built before the work that uses it.");
    const criteria = within(trace).getByRole("list", { name: "Criteria that serve FAC-2" });
    expect(criteria.textContent).toContain("Every call retries once");
    expect(within(criteria).getByRole("link", { name: "The API client, version 1" }).getAttribute("href")).toMatch(/^\/plans\?plan=/);
    const work = within(criteria).getByRole("list", { name: "Work orders" });
    expect(work.textContent).toContain("Write the API client");
    expect(work.textContent).toContain("Done");
  });

  it("lists every criterion that serves a requirement", async () => {
    const list = await traces();
    const criteria = within(traceOf(list, "FAC-1")).getByRole("list", { name: "Criteria that serve FAC-1" });
    expect(within(criteria).getAllByRole("listitem").filter((item) => item.parentElement === criteria)).toHaveLength(3);
  });

  it("says when no criterion serves a requirement", async () => {
    const list = await traces();
    expect(within(traceOf(list, "FAC-4")).getByText("No criterion serves it yet.")).toBeTruthy();
  });

  it("leaves a place for the review verdicts, which come with reviews", async () => {
    const list = await traces();
    const criteria = within(traceOf(list, "FAC-3")).getByRole("list", { name: "Criteria that serve FAC-3" });
    expect(within(criteria).getAllByText("No review verdict yet")).toHaveLength(2);
  });
});

describe("reading a version the ledger stored", () => {
  const open = async (path = recordedLink) => {
    const sent = serveViews();
    openPage(path);
    await screen.findByRole("heading", { level: 1, name: "The factory API" });
    return sent;
  };

  it("asks the ledger for the one document the address names", async () => {
    const sent = await open();
    expect(sent.some((call) => call.path === "/v1/documents/recorded?id=spec%3Afactory-api")).toBe(true);
    expect(sent.some((call) => call.path.startsWith("/v1/documents/read"))).toBe(false);
  });

  it("states its plan, version, source, and where it stands in review", async () => {
    await open();
    const read = golden<RecordedRead>("recorded-document");
    expect(screen.getByRole("link", { name: "The factory floor" }).getAttribute("href")).toBe(`/plans?plan=${encodeURIComponent(read.plan?.id ?? "")}`);
    const facts = screen.getAllByRole("term").map((term) => term.closest("div")?.textContent);
    expect(facts).toContain("Version2");
    expect(facts).toContain("ReviewWaits for approval in plan version 6");
    expect(facts).toContain("KindSpec");
    expect(facts).toContain(`Source${read.version.source ?? ""}`);
  });

  it("lays out the text the ledger stored, as text", async () => {
    await open();
    expect(screen.getByRole("note").textContent).toContain("repository");
    const body = screen.getByRole("region", { name: "The document" });
    expect(within(body).getByRole("heading", { name: "Approvals" })).toBeTruthy();
  });

  it("lists every version with its review, and reads an earlier one", async () => {
    const sent = await open();
    const versions = screen.getByRole("region", { name: "Versions" });
    expect(within(versions).getByText("Version 2 (this one)")).toBeTruthy();
    const earlier = within(versions).getByRole("link", { name: "Version 1" });
    expect(earlier.closest("li")?.textContent).toContain("Approved with plan version 5");
    await userEvent.click(earlier);
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/documents/recorded?id=spec%3Afactory-api&version=1")).toBe(true));
    expect(window.location.search).toContain("version=1");
  });

  it("keeps a stored body as text, whatever it says", async () => {
    const read = golden<RecordedRead>("recorded-document");
    read.body = "<img src=x onerror=alert(1)><script>alert(2)</script>";
    serveViews((call) => (call.path.startsWith("/v1/documents/recorded") ? { status: 200, body: read } : undefined));
    const { container } = openPage(recordedLink);
    await screen.findByRole("heading", { level: 1, name: "The factory API" });
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("script")).toBeNull();
    expect(container.textContent).toContain(read.body);
  });

  it("says when the ledger holds no such document", async () => {
    serveViews((call) => (call.path.startsWith("/v1/documents/recorded") ? refusal(404, "not_found", "document spec:absent: not found") : undefined));
    openPage("/document?id=spec%3Aabsent");
    expect(await screen.findByRole("heading", { level: 1, name: "No such document" })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("not found");
  });
});
