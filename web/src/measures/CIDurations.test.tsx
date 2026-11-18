import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { CIDurations } from "../api/types";
import { openPage } from "../opening";
import { golden, here, refusal, serveViews } from "../testing";

// The panel shows what `ropeshift measures ci` reads from the same records, which a Go test wrote from a real ledger: a
// repository with six heads, and one with none.

const open = () => openPage(here("/measures?tab=delivery"));

const answer = (ci: CIDurations | ReturnType<typeof refusal>) => (call: { path: string }) => {
  if (call.path === "/v1/measures/ci") {
    return "status" in ci ? ci : { status: 200, body: ci };
  }
  return call.path.startsWith("/v1/measures/delivery") ? { status: 200, body: golden("delivery-empty") } : undefined;
};

describe("the CI duration panel of the Delivery tab", () => {
  it("shows each repository's p50 and p85 of its recent heads, and that a repository with too few has no history", async () => {
    serveViews(answer(golden<CIDurations>("ci-durations")));
    open();
    const region = await screen.findByRole("region", { name: "CI duration by repository" });
    const [with_, without] = within(region).getAllByRole("row").slice(1);
    expect(with_?.textContent).toContain("app");
    expect(with_?.textContent).toContain("6");
    expect(with_?.textContent).toContain("p50 8m · p85 11m");
    expect(with_?.textContent).toContain("p50 1m · p85 1m");
    expect(without?.textContent).toContain("other");
    expect(without?.textContent).toContain("0 of 5");
    expect(without?.textContent).toContain("no history");
    expect(without?.textContent).toContain("expected p50 10m · p85 20m");
  });

  it("says why it could not read, in the API's words", async () => {
    serveViews(answer(refusal(500, "unavailable", "the ledger could not be read")));
    open();
    expect((await screen.findByRole("alert")).textContent).toContain("the ledger could not be read");
  });

  it("names an empty installation", async () => {
    serveViews(answer({ repositories: [] }));
    open();
    expect(await screen.findByText("No repository is registered.")).toBeTruthy();
  });
});
