import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { CIWatch, Standing } from "../api/types";
import { golden } from "../testing";
import { PullRequestPanel } from "./PullRequest";

// The work order's page names where the head's CI stands beside its pull request: the time CI is expected to take, or
// "no history", and the elapsed CI and queue time (FR-SPEND-23). The standing is what a Go test wrote from a real ledger.

const standing = () => golden<Standing>("acceptance-ci");

function show(ci: Partial<CIWatch> | null) {
  const read = standing();
  if (!read.pull_request) {
    throw new Error("the golden holds no pull request");
  }
  render(<PullRequestPanel pr={read.pull_request} ci={ci === null ? undefined : { ...(read.ci as CIWatch), ...ci }} />);
  return screen.getByRole("region", { name: /Pull request #/ });
}

const facts = (panel: HTMLElement) => Object.fromEntries([...panel.querySelectorAll("dt")].map((term) => [term.textContent, term.nextElementSibling?.textContent]));

describe("the pull request panel of a work order", () => {
  it("says the repository has no history, and how long the head's checks have run and waited", () => {
    const read = standing();
    expect(read.ci?.history).toBe(false);
    const panel = show({});
    expect(facts(panel)).toMatchObject({ "Expected CI time": "no history", "CI elapsed": "4h so far", "Queue time": "0s" });
  });

  it("says the expected time as the repository's own p50 and p85 once it has history", () => {
    const panel = show({ history: true, expected_p50_ms: 480_000, expected_ms: 660_000 });
    expect(facts(panel)["Expected CI time"]).toBe("p50 8m · p85 11m");
  });

  it("says a run still queued has waited so far and that no runner has picked it up", () => {
    const panel = show({ queued: true, queue_ms: 7_200_000 });
    expect(facts(panel)["Queue time"]).toBe("2h so far, no runner has picked it up");
  });

  it("says how long finished checks took", () => {
    const panel = show({ final: true, elapsed_ms: 540_000, queue_ms: 60_000 });
    expect(facts(panel)).toMatchObject({ "CI elapsed": "took 9m", "Queue time": "1m" });
  });

  it("shows no CI times for a head with no required check, or when the server read none", () => {
    expect(Object.keys(facts(show({ watching: false })))).not.toContain("Expected CI time");
    document.body.replaceChildren();
    const panel = show(null);
    expect(within(panel).queryByText("Expected CI time")).toBeNull();
  });
});
