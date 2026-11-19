import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Questions } from "../api/types";
import { openPage } from "../opening";
import { golden, here, serveViews } from "../testing";

// A question is visible from the asking lane's page and its work order's page from the moment the lane asks it
// (FR-QD-13), with the fields Open questions shows and a link to the question there.

const budget = "Should the budget hold across restarts?";

// serveAsked answers the questions view with the golden's first question for a read that names a lane or a work
// order, or with nothing when none is asked, and records each path it was read at.
function serveAsked(asked = true) {
  return serveViews((call) => {
    if (!call.path.startsWith("/v1/views/questions")) {
      return undefined;
    }
    const view = golden<Questions>("questions");
    const query = new URLSearchParams(call.path.split("?")[1]);
    view.questions = asked && (query.get("lane") || query.get("work_order")) ? view.questions.slice(0, 1) : [];
    return { status: 200, body: view };
  });
}

const section = () => screen.findByRole("region", { name: "Questions" });

describe("a lane's questions", () => {
  it("lists every question the lane asked, with where it stands and a link to it", async () => {
    const sent = serveAsked();
    openPage("/lane?id=lane-wo-running");
    const questions = await section();
    const link = await within(questions).findByRole("link", { name: budget });
    expect(link.getAttribute("href")).toBe(here("/questions?id=question-1&status=all"));
    expect(questions.textContent).toContain("Needs a person");
    expect(questions.textContent).toContain("Rung 5");
    expect(within(questions).getByText("Held by", { selector: "dt" })).toBeTruthy();
    expect(within(questions).getByText("Batch", { selector: "dt" })).toBeTruthy();
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/views/questions?status=all&lane=lane-wo-running")).toBe(true));
  });

  it("says when the lane asked nothing", async () => {
    serveAsked(false);
    openPage("/lane?id=lane-wo-running");
    expect(await within(await section()).findByText("This lane has asked no question.")).toBeTruthy();
  });
});

describe("a work order's questions", () => {
  it("lists every question about the work order or asked by its lanes, with a link to it", async () => {
    const sent = serveAsked();
    openPage("/work-order?id=wo-running");
    const questions = await section();
    const link = await within(questions).findByRole("link", { name: budget });
    expect(link.getAttribute("href")).toBe(here("/questions?id=question-1&status=all"));
    expect(questions.textContent).toContain("Needs a person");
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/views/questions?status=all&work_order=wo-running")).toBe(true));
  });

  it("says when no question is about the work order", async () => {
    serveAsked(false);
    openPage("/work-order?id=wo-running");
    expect(await within(await section()).findByText("No question is about this work or was asked by its lanes.")).toBeTruthy();
  });
});
