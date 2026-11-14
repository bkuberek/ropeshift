import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { DocumentRow, DocumentVersions, Documents, RecordedRead } from "../api/types";
import { openPage } from "../opening";
import { golden, here, refusal, serveViews, withStyles } from "../testing";

function serve(change: (view: Documents) => void, answer?: Parameters<typeof serveViews>[0]) {
  const view = golden<Documents>("documents");
  change(view);
  return serveViews((call) => answer?.(call) ?? (call.path.startsWith("/v1/views/documents") ? { status: 200, body: view } : undefined));
}

const recorded = here("/documents?id=spec%3Afactory-api");
const requirements = here("/documents?id=requirements%3Afactory");

const tree = () => screen.findByRole("navigation", { name: "Documents" });

// serveRequirements answers the ledger's read of the requirements document a plan recorded, which is the one
// document whose reader lists requirements.
function serveRequirements(change: (view: Documents) => void = () => {}) {
  const read = golden<RecordedRead>("recorded-document");
  return serve(change, (call) => {
    if (!call.path.startsWith("/v1/documents/recorded?id=requirements")) {
      return undefined;
    }
    read.document = { id: "requirements:factory", kind: "requirements", title: "What the factory must do", path: "plans/factory/requirements.md", versions: read.document.versions };
    read.body = "# What the factory must do\n\nA person plans and approves.\n";
    return { status: 200, body: read };
  });
}

describe("the document tree", () => {
  it("lists the documents by kind, each linked to its reading", async () => {
    serveViews();
    openPage(here("/documents"));
    const nav = await tree();
    for (const kind of ["Requirements", "Specs", "Designs", "Other"]) {
      expect(within(nav).getByText(kind)).toBeTruthy();
    }
    expect(within(nav).getByRole("link", { name: "The factory API" }).getAttribute("href")).toBe(here("/documents?id=spec%3Afactory-api"));
    expect(within(nav).getByRole("link", { name: "The retry spec" }).getAttribute("href")).toBe(here("/documents?path=docs%2Fspecs%2Fretry.md&instance=memory"));
  });

  it("lists a file a plan recorded once, and the files of the repository beside it", async () => {
    serveViews();
    openPage(here("/documents"));
    const nav = await tree();
    expect(within(nav).getAllByRole("link", { name: "The factory API" })).toHaveLength(1);
    expect(within(nav).getByRole("link", { name: "The retry budget" })).toBeTruthy();
    expect(within(nav).getByRole("link", { name: "How the factory is built" })).toBeTruthy();
    expect(within(nav).getByRole("link", { name: "Build the core" })).toBeTruthy();
  });

  it("marks the document that is open, and opens the first requirements document with no address to say", async () => {
    serveRequirements();
    openPage(here("/documents"));
    const nav = await tree();
    const current = within(nav).getAllByRole("link").filter((link) => link.getAttribute("aria-current") === "page");
    expect(current.map((link) => link.textContent)).toEqual(["What the factory must do"]);
    expect(await screen.findByRole("heading", { level: 2, name: "What the factory must do" })).toBeTruthy();
  });

  it("opens the specs from the old address that named them", async () => {
    serveViews();
    openPage("/specs");
    await tree();
    expect(window.location.pathname).toBe(here("/documents"));
    expect(window.location.search).toBe("?kind=spec");
    const current = within(await tree()).getAllByRole("link").filter((link) => link.getAttribute("aria-current") === "page");
    expect(current.map((link) => link.textContent)).toEqual(["The factory API"]);
  });

  it("says so when no document is held, and offers to start a planning session", async () => {
    serve((view) => {
      view.documents = [];
      view.plans = [];
      view.requirements = [];
    });
    openPage(here("/documents"));
    expect(await screen.findByText("No documents yet")).toBeTruthy();
    expect(screen.getByText("Documents appear when a plan or an adoption adds them.")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Start planning session" }).length).toBeGreaterThan(0);
  });

  it("opens the list from the old address that names no document", async () => {
    serveViews();
    openPage("/document");
    expect(await screen.findByRole("heading", { level: 1, name: "Documents" })).toBeTruthy();
    expect(window.location.pathname).toBe(here("/documents"));
  });
});

describe("reading a version the ledger stored", () => {
  const open = async (path = recorded) => {
    const sent = serveViews();
    openPage(path);
    await screen.findByRole("heading", { level: 2, name: "The factory API" });
    await screen.findByRole("combobox", { name: "Version" });
    return sent;
  };

  it("asks the ledger for the one document the address names", async () => {
    const sent = await open();
    expect(sent.some((call) => call.path === "/v1/documents/recorded?id=spec%3Afactory-api")).toBe(true);
    expect(sent.some((call) => call.path.startsWith("/v1/documents/read"))).toBe(false);
  });

  it("says where the version came from, its digest, and where it stands in review", async () => {
    await open();
    const read = golden<RecordedRead>("recorded-document");
    expect(screen.getByText(read.document.path ?? "")).toBeTruthy();
    expect(screen.getByTitle(read.version.digest)).toBeTruthy();
    expect(screen.getByTitle(read.version.source ?? "").textContent).toBe(read.version.source);
    const facts = screen.getAllByRole("term").map((term) => term.closest("div")?.textContent);
    expect(facts).toContain("ReviewWaits for approval in plan version 6");
    expect(facts).toContain("KindSpec");
    expect(screen.getByRole("link", { name: "The factory floor" }).getAttribute("href")).toBe(here(`/plans?plan=${encodeURIComponent(read.plan?.id ?? "")}`));
    expect(screen.getByRole("link", { name: "Open in plan" }).getAttribute("href")).toBe(here(`/plan?id=${encodeURIComponent(read.plan?.id ?? "")}&version=6`));
  });

  // A path and a source are words with no space to break at, wider than a phone.
  it("keeps where the version came from inside the reader, however long its path and source", async () => {
    withStyles();
    await open();
    const read = golden<RecordedRead>("recorded-document");
    const source = screen.getByTitle(read.version.source ?? "");
    const style = getComputedStyle(source);
    expect([style.display, style.maxWidth, style.overflow, style.textOverflow, style.whiteSpace]).toEqual(["inline-block", "100%", "hidden", "ellipsis", "nowrap"]);
    expect(getComputedStyle(source.closest("p") as HTMLElement).overflowWrap).toBe("anywhere");
  });

  it("says plainly that the text came from outside, and shows it as text", async () => {
    const read = golden<RecordedRead>("recorded-document");
    read.body = "<img src=x onerror=alert(1)><script>alert(2)</script>";
    serveViews((call) => (call.path.startsWith("/v1/documents/recorded") ? { status: 200, body: read } : undefined));
    const { container } = openPage(recorded);
    await screen.findByRole("heading", { level: 2, name: "The factory API" });
    await screen.findByRole("combobox", { name: "Version" });
    expect(screen.getByRole("note").textContent).toContain("repository");
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("script")).toBeNull();
    expect(container.textContent).toContain(read.body);
  });

  it("lays out the text the ledger stored", async () => {
    await open();
    expect(screen.getByRole("heading", { name: "Approvals" })).toBeTruthy();
  });

  it("selects a version by its number and where it stands, and reads it", async () => {
    const sent = await open();
    const select = screen.getByRole("combobox", { name: "Version" });
    expect(within(select).getAllByRole("option").map((option) => option.textContent)).toEqual(["v2 · in pending plan v6", "v1 · approved in plan v5"]);
    fireEvent.change(select, { target: { value: "1" } });
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/documents/recorded?id=spec%3Afactory-api&version=1")).toBe(true));
    expect(window.location.search).toContain("version=1");
  });

  it("shows how two versions differ, by line", async () => {
    const newest = golden<RecordedRead>("recorded-document");
    const first = golden<RecordedRead>("recorded-document");
    first.version = newest.document.versions[1] as RecordedRead["version"];
    first.body = "# The factory API\n\nA client reads the roadmap.\n";
    const sent = serveViews((call) => {
      if (call.path === "/v1/documents/recorded?id=spec%3Afactory-api&version=1") {
        return { status: 200, body: first };
      }
      return undefined;
    });
    openPage(recorded);
    await screen.findByRole("heading", { level: 2, name: "The factory API" });
    fireEvent.change(await screen.findByRole("combobox", { name: "Compare with" }), { target: { value: "1" } });
    const diff = await screen.findByRole("table", { name: "Difference between version 1 and version 2" });
    expect(within(diff).getByText("A client reads the roadmap and approves a plan.")).toBeTruthy();
    expect(window.location.search).toContain("against=1");
    expect(sent.some((call) => call.path === "/v1/documents/recorded?id=spec%3Afactory-api&version=1")).toBe(true);
  });

  it("opens a difference from an address that names it", async () => {
    const first = golden<RecordedRead>("recorded-document");
    first.body = "Old text.";
    first.version = first.document.versions[1] as RecordedRead["version"];
    serveViews((call) => (call.path === "/v1/documents/recorded?id=spec%3Afactory-api&version=1" ? { status: 200, body: first } : undefined));
    openPage(here("/documents?id=spec%3Afactory-api&version=2&against=1"));
    expect(await screen.findByRole("table", { name: "Difference between version 1 and version 2" })).toBeTruthy();
  });

  it("says when the ledger holds no such document", async () => {
    serveViews((call) => (call.path.startsWith("/v1/documents/recorded") ? refusal(404, "not_found", "document spec:absent: not found") : undefined));
    openPage(here("/documents?id=spec%3Afactory-api"));
    expect(await screen.findByRole("heading", { level: 2, name: "No such document" })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("not found");
  });

  it("copies a link to the version", async () => {
    await open();
    const written: string[] = [];
    Object.defineProperty(navigator, "clipboard", { value: { writeText: async (text: string) => void written.push(text) }, configurable: true });
    await userEvent.click(screen.getByRole("button", { name: "Copy link" }));
    await waitFor(() => expect(written).toEqual([window.location.href]));
  });
});

describe("reading a file of the repository", () => {
  const path = here("/documents?path=docs%2Fspecs%2Fretry.md&instance=memory");

  it("asks for the one file the address names and says where it came from", async () => {
    const sent = serveViews();
    openPage(path);
    await screen.findByRole("heading", { level: 2, name: "The retry spec" });
    const shown = golden<DocumentRow>("document");
    expect((await screen.findByTitle(shown.source)).textContent).toBe(shown.source);
    expect(sent.some((call) => call.path === "/v1/documents/read?path=docs%2Fspecs%2Fretry.md&instance=memory")).toBe(true);
    expect(screen.getByRole("note").textContent).toContain("repository");
    expect(screen.getByRole("heading", { name: "Links and pictures" })).toBeTruthy();
  });

  it("lists its versions and reads an earlier one", async () => {
    const sent = serveViews();
    openPage(path);
    await screen.findByRole("heading", { level: 2, name: "The retry spec" });
    const select = await screen.findByRole("combobox", { name: "Version" });
    await waitFor(() => expect(within(select).getAllByRole("option")).toHaveLength(golden<DocumentVersions>("document-versions").versions.length));
    fireEvent.change(select, { target: { value: "1" } });
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/documents/read?path=docs%2Fspecs%2Fretry.md&instance=memory&version=1")).toBe(true));
    expect(window.location.search).toContain("version=1");
  });

  it("says why git cannot read it, and keeps the list of its versions", async () => {
    serveViews((call) => (call.path.startsWith("/v1/documents/read") ? refusal(503, "unavailable", "git could not read docs/specs/retry.md.") : undefined));
    openPage(path);
    expect((await screen.findByRole("alert")).textContent).toContain("git could not read docs/specs/retry.md");
    expect(await screen.findByRole("combobox", { name: "Version" })).toBeTruthy();
  });
});

describe("requirements", () => {
  it("lists each requirement of a requirements document with where it stands", async () => {
    serveRequirements();
    openPage(requirements);
    const list = await screen.findByRole("list", { name: "Requirements" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows.map((row) => row.querySelector(".id")?.textContent)).toEqual(["FAC-1", "FAC-2", "FAC-3", "FAC-4"]);
    expect(rows[0]?.textContent).toContain("Planned");
    expect(rows[1]?.textContent).toContain("Met");
    expect(rows[3]?.textContent).toContain("Planned");
  });

  it("selects a requirement for the trace beside the reader, which the address names", async () => {
    serveRequirements();
    openPage(requirements);
    await userEvent.click(await screen.findByRole("link", { name: "Trace FAC-1" }));
    expect(window.location.search).toContain("key=FAC-1");
    const panel = await screen.findByRole("region", { name: "Trace · FAC-1" });
    expect(within(panel).getByText("A person approves a whole plan in one step.")).toBeTruthy();
    expect(screen.getByText("FAC-1").closest("li")?.getAttribute("aria-current")).toBe("true");
  });

  it("follows a requirement to its criteria with their contract and version, the work orders with their state, and the verdicts", async () => {
    serve((view) => {
      const order = view.plans[0]?.requirements[1]?.criteria[0]?.work_orders[0];
      if (order) {
        order.verdict = { verdict: "pass", commit: "a1b2c3d4e5f6" };
      }
    });
    openPage(here("/documents?key=FAC-2"));
    const panel = await screen.findByRole("region", { name: "Trace · FAC-2" });
    expect(within(panel).getByText("Requirement")).toBeTruthy();
    expect(within(panel).getByText("c1", { selector: ".chip" })).toBeTruthy();
    expect(within(panel).getByText(/Contract/).textContent).toContain("v1 The API client");
    expect(within(panel).getByRole("link", { name: "Write the API client" }).getAttribute("href")).toMatch(/\/work-order\?id=/);
    expect(within(panel).getByText("Done")).toBeTruthy();
    expect(within(panel).getByText("Pass")).toBeTruthy();
    expect(within(panel).getByTitle("a1b2c3d4e5f6").textContent).toBe("a1b2c3d");
  });

  it("says there is no verdict yet for work no review has judged", async () => {
    serveViews();
    openPage(here("/documents?key=FAC-1"));
    const panel = await screen.findByRole("region", { name: "Trace · FAC-1" });
    expect(within(panel).getByText("none yet")).toBeTruthy();
  });

  it("shows the evidence where the verdicts would be for a requirement existing code meets", async () => {
    serve((view) => {
      const trace = view.plans[0]?.requirements[3];
      if (trace) {
        trace.status = "met";
        trace.evidence = ["shop:client.go", "shop:client_test.go"];
      }
    });
    openPage(here("/documents?key=FAC-4"));
    const panel = await screen.findByRole("region", { name: "Trace · FAC-4" });
    const evidence = within(panel).getByRole("list", { name: "Evidence that the code meets FAC-4" });
    expect(within(evidence).getAllByRole("listitem").map((item) => item.textContent)).toEqual(["shop:client.go", "shop:client_test.go"]);
    expect(within(panel).queryByText("Verdicts")).toBeNull();
    expect(within(panel).getByText("Met by existing code")).toBeTruthy();
  });

  it("says when no criterion traces to a requirement, which is a gap in the plan", async () => {
    serveViews();
    openPage(here("/documents?key=FAC-4"));
    const panel = await screen.findByRole("region", { name: "Trace · FAC-4" });
    expect(within(panel).getByText("No criteria trace to this requirement.")).toBeTruthy();
  });

  it("says when a requirement is not recorded", async () => {
    serveViews();
    openPage(here("/documents?key=FAC-99"));
    expect(await screen.findByText("No requirement FAC-99 is recorded.")).toBeTruthy();
  });

  it("opens the requirements document that holds a requirement the address selects", async () => {
    serveRequirements();
    openPage(here("/documents?key=FAC-3"));
    expect(await screen.findByRole("list", { name: "Requirements" })).toBeTruthy();
    expect(screen.getByText("FAC-3").closest("li")?.getAttribute("aria-current")).toBe("true");
  });
});

describe("the traceability tab", () => {
  const matrix = async (path = here("/traceability")) => {
    serveViews();
    openPage(path);
    return screen.findByRole("region", { name: "Traceability matrix" });
  };

  it("is the address of the second tab, and the tabs move between two paths", async () => {
    serveViews();
    openPage(here("/documents?kind=requirements&key=FAC-2"));
    const tabs = await screen.findByRole("navigation", { name: "Documents view" });
    expect(within(tabs).getByRole("link", { name: "Reader" }).getAttribute("aria-current")).toBe("page");
    await userEvent.click(within(tabs).getByRole("link", { name: "Traceability" }));
    expect(window.location.pathname).toBe(here("/traceability"));
    expect(window.location.search).toBe("?key=FAC-2");
    expect(within(await screen.findByRole("navigation", { name: "Documents view" })).getByRole("link", { name: "Traceability" }).getAttribute("aria-current")).toBe("page");
    await userEvent.click(within(screen.getByRole("navigation", { name: "Documents view" })).getByRole("link", { name: "Reader" }));
    expect(window.location.pathname).toBe(here("/documents"));
    expect(window.location.search).toBe("?kind=requirements&key=FAC-2");
  });

  it("has a row for each requirement with its status, criteria, work orders, and latest verdict or evidence", async () => {
    const region = await matrix();
    const rows = within(region).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(4);
    const cells = (row: HTMLElement) => within(row).getAllByRole("cell").map((cell) => cell.textContent);
    expect(cells(rows[0] as HTMLElement)[0]).toContain("FAC-1");
    expect(cells(rows[1] as HTMLElement)[1]).toBe("Met");
    expect(cells(rows[1] as HTMLElement)[2]).toBe("c1");
    expect(cells(rows[1] as HTMLElement)[3]).toBe("Write the API client");
    expect(cells(rows[1] as HTMLElement)[4]).toBe("none yet");
    expect(cells(rows[3] as HTMLElement)[2]).toBe("none");
  });

  it("filters the rows by status", async () => {
    const region = await matrix();
    await userEvent.click(within(screen.getByRole("group", { name: "Status" })).getByRole("button", { name: /^Met 1$/ }));
    expect(within(region).getAllByRole("row").slice(1)).toHaveLength(1);
    expect(within(region).getByText("FAC-2")).toBeTruthy();
    await userEvent.click(within(screen.getByRole("group", { name: "Status" })).getByRole("button", { name: /^Met 1$/ }));
    expect(within(region).getAllByRole("row").slice(1)).toHaveLength(4);
  });

  it("filters the rows by plan when the project has more than one", async () => {
    serve((view) => {
      const second = structuredClone(view.plans[0]!);
      second.plan = { id: "plan-2", title: "A second plan", status: "active" };
      second.requirements = second.requirements.slice(0, 1).map((trace) => ({ ...trace, key: "SEC-1", document: "requirements:second" }));
      view.plans.push(second);
    });
    openPage(here("/traceability"));
    const region = await screen.findByRole("region", { name: "Traceability matrix" });
    expect(within(region).getAllByRole("row").slice(1)).toHaveLength(5);
    fireEvent.change(screen.getByRole("combobox", { name: "Plan" }), { target: { value: "plan-2" } });
    expect(within(region).getAllByRole("row").slice(1)).toHaveLength(1);
    expect(within(region).getByText("SEC-1")).toBeTruthy();
  });

  it("selects a row by the key in the address, and shows its trace", async () => {
    const region = await matrix(here("/traceability?key=FAC-2"));
    const selected = within(region).getAllByRole("row").filter((row) => row.getAttribute("aria-selected") === "true");
    expect(selected).toHaveLength(1);
    expect(selected[0]?.textContent).toContain("FAC-2");
    expect(await screen.findByRole("region", { name: "Trace · FAC-2" })).toBeTruthy();
    await userEvent.click(within(region).getByRole("link", { name: "FAC-3" }));
    expect(window.location.search).toBe("?key=FAC-3");
  });

  it("says so when no requirement is traced yet", async () => {
    serve((view) => {
      view.plans = [];
    });
    openPage(here("/traceability"));
    expect(await screen.findByText("No requirement is traced yet")).toBeTruthy();
  });

  it("shows the latest verdict of a requirement's work in its row", async () => {
    serve((view) => {
      const order = view.plans[0]?.requirements[1]?.criteria[0]?.work_orders[0];
      if (order) {
        order.verdict = { verdict: "fail", commit: "abc1234" };
      }
    });
    openPage(here("/traceability"));
    const region = await screen.findByRole("region", { name: "Traceability matrix" });
    expect(within(within(region).getByText("FAC-2").closest("tr") as HTMLElement).getByText("Fail")).toBeTruthy();
  });
});
