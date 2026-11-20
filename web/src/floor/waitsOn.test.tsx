import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { InProgress, ShopFloor } from "../api/types";
import { openPage } from "../opening";
import { golden, serveViews } from "../testing";

// A lane that waits on a blocking question says whom it waits on and since when, on its work and on its own page,
// so a person sees that the orchestrator holds it, or that they do (FR-ORCH-1).
describe("a lane that waits on a blocking question", () => {
  it("says on In progress that it waits on the orchestrator", async () => {
    const running = golden<InProgress>("in-progress");
    const line = running.items.find((item) => item.lane?.id === "lane-wo-running");
    if (!line) {
      throw new Error("the golden holds no running lane-wo-running");
    }
    line.waits_on = { who: "orchestrator", question: "q-retry-budget", since: Date.now() - 90_000 };
    serveViews((call) => (call.path.startsWith("/v1/views/in-progress") ? { status: 200, body: running } : undefined));
    openPage("/in-progress");
    const note = await screen.findByText(/Waits on the orchestrator/);
    expect(note.textContent).toContain("q-retry-budget");
  });

  it("says on its lane page that it waits on you", async () => {
    const floor = golden<ShopFloor>("shop-floor");
    const lane = floor.lanes.find((item) => item.id === "lane-wo-running");
    if (!lane) {
      throw new Error("the golden holds no lane-wo-running");
    }
    lane.waits_on = { who: "member", question: "q-data-region", since: Date.now() - 60_000 };
    serveViews((call) => (call.path.startsWith("/v1/views/shop-floor") ? { status: 200, body: floor } : undefined));
    openPage(`/lane?id=${encodeURIComponent(lane.id)}`);
    const facts = await screen.findByRole("complementary", { name: "Lane facts" });
    expect((await within(facts).findByText(/Waits on you/)).textContent).toContain("q-data-region");
  });
});
