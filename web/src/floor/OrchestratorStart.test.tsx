import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { OrchestratorHold, ScheduledStart, ShopFloor, WorkerLine } from "../api/types";
import { openPage } from "../opening";
import { golden, refusal, serveApi, serveViews } from "../testing";
import { countdownWords, OrchestratorStart, UsageLimit } from "./OrchestratorStart";

// What keeps an orchestrator from starting shows next to its Start control, from the same read model on Workers and in
// the notice that no orchestrator is judging: an offline worker, a tripped breaker with Reset, or a sign-in at its usage
// limit, with the window, its reset, and a live countdown, where Start schedules the start for the reset.

const minute = 60_000;
const hour = 60 * minute;
const fixedNow = Date.UTC(2026, 10, 18, 12, 0, 0);

afterEach(() => {
  vi.useRealTimers();
});

function lineWith(hold?: OrchestratorHold, start?: ScheduledStart): WorkerLine {
  const line = structuredClone(golden<ShopFloor>("shop-floor").workers.find((one) => one.worker.id === "worker-a")) as WorkerLine;
  line.orchestrator_hold = hold;
  line.scheduled_start = start;
  return line;
}

const limitedAt = (resets: number, more: Partial<OrchestratorHold> = {}): OrchestratorHold => ({ reason: "usage_limit", window: "five_hour", resets_at: resets, source: "session", ...more });

describe("the countdown to a reset", () => {
  it("counts in hours and minutes", () => {
    expect(countdownWords(2 * hour + 13 * minute, false)).toBe("2 h 13 min");
    expect(countdownWords(13 * minute + 59_000, false)).toBe("13 min");
    expect(countdownWords(59_000, false)).toBe("under a minute");
  });

  it("counts in days for a weekly window a day or more away, and in hours under a day", () => {
    expect(countdownWords(3 * 24 * hour + 5 * hour, true)).toBe("3 days");
    expect(countdownWords(24 * hour, true)).toBe("1 day");
    expect(countdownWords(5 * hour, true)).toBe("5 h 0 min");
  });
});

describe("the usage limit", () => {
  it("names the window and counts down to its reset from a fixed clock", () => {
    render(<UsageLimit hold={limitedAt(fixedNow + 2 * hour + 13 * minute)} now={fixedNow} />);
    expect(screen.getByText(/The usage limit of the five-hour session window is reached\. It resets at .* \(in 2 h 13 min\)\./)).toBeTruthy();
  });

  it("counts a weekly window down in days", () => {
    render(<UsageLimit hold={limitedAt(fixedNow + 3 * 24 * hour + hour, { window: "seven_day" })} now={fixedNow} />);
    expect(screen.getByText(/weekly window is reached\. It resets at .* \(in 3 days\)\./)).toBeTruthy();
  });

  it("says a reset time came from a /usage hint", () => {
    render(<UsageLimit hold={limitedAt(fixedNow + hour, { source: "usage_hint" })} now={fixedNow} />);
    expect(screen.getByText(/This reset time came from a \/usage hint, which can be out of date\./)).toBeTruthy();
  });

  it("says an old reset time may be out of date, and counts down to nothing", () => {
    render(<UsageLimit hold={limitedAt(fixedNow - 10 * minute, { stale: true })} now={fixedNow} />);
    const said = screen.getByText(/This reset time may be out of date/);
    expect(said.textContent).not.toContain("(in ");
  });

  it("says no reset time was reported, and shows no countdown", () => {
    render(<UsageLimit hold={{ reason: "usage_limit", window: "five_hour" }} now={fixedNow} />);
    const said = screen.getByText(/no reset time was reported, so no countdown shows/);
    expect(said.textContent).not.toContain("(in ");
  });

  it("runs the countdown on as the clock moves", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(fixedNow);
    serveApi(() => undefined);
    render(<OrchestratorStart line={lineWith(limitedAt(fixedNow + 2 * hour))} after={async () => {}} />);
    expect(screen.getByText(/\(in 2 h 0 min\)/)).toBeTruthy();
    await vi.advanceTimersByTimeAsync(31 * minute);
    await waitFor(() => expect(screen.getByText(/\(in 1 h 29 min\)/)).toBeTruthy());
  });
});

describe("the start control", () => {
  const answering = (status = 201) =>
    serveApi((call) => {
      if (/^\/v1\/workers\/worker-a\/orchestrator(\/schedule)?$/.test(call.path) && call.method !== "GET") {
        return status === 201 ? { status, body: { worker_id: "worker-a", starts_at: fixedNow, scheduled_by: "member-1", scheduled_at: fixedNow } } : refusal(status, "invalid", "the sign-in of worker worker-a is not at its usage limit, so start its orchestrator now");
      }
      return undefined;
    });

  it("schedules the start for the reset while the sign-in is at its limit", async () => {
    const sent = answering();
    render(<OrchestratorStart line={lineWith(limitedAt(Date.now() + hour))} after={async () => {}} />);
    await userEvent.click(screen.getByRole("button", { name: "Start at reset on solo" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST" && call.path === "/v1/workers/worker-a/orchestrator/schedule")).toBe(true));
  });

  it("shows a refusal to schedule next to the control", async () => {
    answering(400);
    render(<OrchestratorStart line={lineWith(limitedAt(Date.now() + hour))} after={async () => {}} />);
    await userEvent.click(screen.getByRole("button", { name: "Start at reset on solo" }));
    expect(await screen.findByText(/is not at its usage limit/)).toBeTruthy();
  });

  it("says when a scheduled start starts, and cancels it", async () => {
    const sent = serveApi((call) => (call.method === "DELETE" ? { status: 204 } : undefined));
    const at = Date.now() + 90 * minute;
    render(<OrchestratorStart line={lineWith(limitedAt(at), { worker_id: "worker-a", starts_at: at, scheduled_by: "member-1", scheduled_at: Date.now() })} after={async () => {}} />);
    expect(screen.getByText(/^Starts at .* \(in 1 h (29|30) min\)\.$/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Cancel the scheduled start on solo" }));
    await waitFor(() => expect(sent.some((call) => call.method === "DELETE" && call.path === "/v1/workers/worker-a/orchestrator/schedule")).toBe(true));
  });

  it("says a start whose reset was stale waits for the next reported reset", () => {
    serveApi(() => undefined);
    render(<OrchestratorStart line={lineWith(limitedAt(0), { worker_id: "worker-a", scheduled_by: "member-1", scheduled_at: 1, missed: 1 })} after={async () => {}} />);
    expect(screen.getByText("The reset passed with the limit still holding, so the orchestrator starts at the next reset its worker reports.")).toBeTruthy();
  });

  it("offers Reset for a tripped breaker, and says the worker is offline", async () => {
    const sent = serveApi((call) => (call.method === "PUT" ? { status: 200, body: {} } : undefined));
    const { unmount } = render(<OrchestratorStart line={lineWith({ reason: "tripped" })} after={async () => {}} />);
    expect(screen.getByText(/Its breaker tripped/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Reset the orchestrator on solo" }));
    await waitFor(() => expect(sent.find((call) => call.method === "PUT")?.body).toEqual({ desired_state: "running" }));
    unmount();
    render(<OrchestratorStart line={lineWith({ reason: "offline" })} after={async () => {}} />);
    expect(screen.getByText("The worker is offline, so its orchestrator starts once it connects.")).toBeTruthy();
  });
});

describe("Workers and resources", () => {
  const limitedFloor = (scheduled: boolean) => {
    const floor = golden<ShopFloor>("shop-floor");
    for (const line of floor.workers) {
      if (line.worker.id === "worker-a") {
        line.orchestrator_hold = limitedAt(Date.now() + hour);
        line.scheduled_start = scheduled ? { worker_id: "worker-a", starts_at: Date.now() + hour, scheduled_by: "member-1", scheduled_at: Date.now() } : undefined;
      }
    }
    return floor;
  };

  it("shows the orchestrator card's hold and countdown from the same read model", async () => {
    serveViews((call) => (call.path.startsWith("/v1/views/shop-floor") ? { status: 200, body: limitedFloor(false) } : undefined));
    openPage("/workers");
    expect(await screen.findByText(/The usage limit of the five-hour session window is reached\. It resets at .* \(in (59 min|1 h 0 min)\)\./)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Start at reset on solo" })).toBeTruthy();
  });

  it("lists Start at reset on and Cancel the scheduled start on in the command palette", async () => {
    for (const [scheduled, title] of [[false, "Start at reset on solo"], [true, "Cancel the scheduled start on solo"]] as const) {
      serveViews((call) => (call.path.startsWith("/v1/views/shop-floor") ? { status: 200, body: limitedFloor(scheduled) } : undefined));
      const page = openPage("/workers");
      await screen.findByRole("button", { name: title });
      await userEvent.keyboard("{Control>}k{/Control}");
      await userEvent.type(await screen.findByRole("combobox"), scheduled ? ">Cancel the scheduled" : ">Start at reset");
      expect(await screen.findByRole("option", { name: new RegExp(title) })).toBeTruthy();
      page.unmount();
    }
  });
});
