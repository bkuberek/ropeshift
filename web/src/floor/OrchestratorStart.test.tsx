import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { OrchestratorHold, ScheduledStart, ShopFloor, WorkerLine } from "../api/types";
import { PageActionsProvider } from "../app/pageActions";
import type { Entry } from "../app/ranking";
import { openPage } from "../opening";
import { ViewTime } from "../parts/parts";
import { golden, refusal, serveApi, serveViews } from "../testing";
import { ToastProvider } from "../ui/overlays";
import { countdownWords, OrchestratorStart, UsageLimit, when } from "./OrchestratorStart";

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
  line.orchestrator = undefined;
  line.orchestrator_hold = hold;
  line.scheduled_start = start;
  return line;
}

const limitedAt = (resets: number, more: Partial<OrchestratorHold> = {}): OrchestratorHold => ({ reason: "usage_limit", window: "five_hour", resets_at: resets, ...more });

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

describe("a start of an orchestrator asked to run", () => {
  const askedToRun = (state: string | undefined, hold?: OrchestratorHold): WorkerLine => {
    const line = lineWith(hold);
    line.orchestrator = { id: "orchestrator-worker-a", worker_id: "worker-a", desired_state: "running", run: 2, resets: 0, actual_state: state };
    return line;
  };
  const answers = (run: number, resets = 0) =>
    serveApi((call) => (call.method === "PUT" ? { status: 200, body: { id: "orchestrator-worker-a", worker_id: "worker-a", desired_state: "running", run, resets } } : undefined));
  const shown = (line: WorkerLine) =>
    render(
      <ToastProvider>
        <OrchestratorStart line={line} after={async () => {}} />
      </ToastProvider>,
    );

  it("says a start of an ended run started the next", async () => {
    const sent = answers(3);
    shown(askedToRun("ended"));
    await userEvent.click(screen.getByRole("button", { name: "Start orchestrator on solo" }));
    expect(await screen.findByText("Asked solo to run its orchestrator.")).toBeTruthy();
    expect(sent.find((call) => call.method === "PUT")?.body).toEqual({ desired_state: "running" });
  });

  it("reports no success when the start changed nothing", async () => {
    answers(2);
    shown(askedToRun(undefined));
    await userEvent.click(screen.getByRole("button", { name: "Start orchestrator on solo" }));
    expect(await screen.findByText("The orchestrator on solo was already asked to run, so Start changed nothing.")).toBeTruthy();
    expect(screen.queryByText("Asked solo to run its orchestrator.")).toBeNull();
  });

  it("says a reset of a tripped breaker changed something", async () => {
    answers(2, 1);
    shown(askedToRun("tripped", { reason: "tripped" }));
    await userEvent.click(screen.getByRole("button", { name: "Reset the orchestrator on solo" }));
    expect(await screen.findByText("Asked solo to run its orchestrator.")).toBeTruthy();
  });

  // Nothing schedules a start of an orchestrator asked to run, so at a limit an ended run starts now, and its next
  // run waits the limit out.
  it("starts an ended run at a usage limit, and schedules nothing", async () => {
    const sent = answers(3);
    shown(askedToRun("ended", limitedAt(Date.now() + hour)));
    expect(screen.queryByText("Its orchestrator is asked to run, so it starts once the limit resets.")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Start orchestrator on solo" }));
    expect(await screen.findByText("Asked solo to run its orchestrator.")).toBeTruthy();
    expect(sent.some((call) => call.path.endsWith("/schedule"))).toBe(false);
  });
});

describe("Workers and resources", () => {
  const limitedFloor = (scheduled: boolean) => {
    const floor = { ...golden<ShopFloor>("shop-floor"), as_of: Date.now() };
    for (const line of floor.workers) {
      if (line.worker.id === "worker-a") {
        line.orchestrator = undefined;
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

describe("the start control on the view's clock", () => {
  // serverNow is the view's moment, years from the browser's, so a countdown that read the browser's clock would show.
  const serverNow = Date.UTC(2031, 2, 10, 9, 0, 0);
  const onServer = (line: WorkerLine) => (
    <ViewTime.Provider value={{ asOf: serverNow, elapsed: 0 }}>
      <OrchestratorStart line={line} after={async () => {}} />
    </ViewTime.Provider>
  );

  it("counts down from the view's server time, not the browser's", () => {
    serveApi(() => undefined);
    render(onServer(lineWith(limitedAt(serverNow + 2 * hour))));
    expect(screen.getByText(/\(in 2 h 0 min\)/)).toBeTruthy();
  });

  it("names the day of a scheduled start that is not today", () => {
    serveApi(() => undefined);
    const at = serverNow + 26 * hour;
    render(onServer(lineWith(limitedAt(at), { worker_id: "worker-a", starts_at: at, scheduled_by: "member-1", scheduled_at: serverNow })));
    const day = new Date(at).toLocaleString(undefined, { weekday: "short" });
    expect(screen.getByText(/^Starts at /).textContent).toContain(day);
    expect(screen.getByText(/^Starts at /).textContent).toContain("(in 1 day)");
  });

  it("gives a start today its time of day alone", () => {
    serveApi(() => undefined);
    const at = serverNow + 2 * hour;
    render(onServer(lineWith(limitedAt(at), { worker_id: "worker-a", starts_at: at, scheduled_by: "member-1", scheduled_at: serverNow })));
    expect(screen.getByText(/^Starts at /).textContent).toBe(`Starts at ${when(at, serverNow)} (in 2 h 0 min).`);
    expect(when(at, serverNow)).not.toContain(new Date(at).toLocaleString(undefined, { weekday: "short" }));
  });
});

describe("the start control's palette entry", () => {
  it("takes the keyboard to the button and sends nothing", () => {
    const sent = serveApi(() => undefined);
    let entries: Entry[] = [];
    render(
      <PageActionsProvider value={(listed) => (entries = listed)}>
        <OrchestratorStart line={lineWith(limitedAt(Date.now() + hour))} after={async () => {}} />
      </PageActionsProvider>,
    );
    const entry = entries.find((one) => one.title === "Start at reset on solo");
    act(() => {
      void entry?.run?.();
    });
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Start at reset on solo" }));
    expect(sent.some((call) => call.method !== "GET")).toBe(false);
  });

  it("offers nothing to schedule for an orchestrator already asked to run", () => {
    serveApi(() => undefined);
    const line = lineWith(limitedAt(Date.now() + hour));
    line.orchestrator = { id: "orchestrator-worker-a", worker_id: "worker-a", desired_state: "running", run: 1, resets: 0 };
    render(<OrchestratorStart line={line} after={async () => {}} />);
    expect(screen.getByText("Its orchestrator is asked to run, so it starts once the limit resets.")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
