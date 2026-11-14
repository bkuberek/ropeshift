import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { cardStates } from "../floor/states";
import { clockOf, Resolved, whereWords, You } from "./resolution";

describe("where a decision was made", () => {
  it("says each place in its own words", () => {
    expect(whereWords("web")).toBe("in Needs you");
    expect(whereWords("needs_you")).toBe("in Needs you");
    expect(whereWords("conversation")).toBe("in the conversation");
    expect(whereWords("cli")).toBe("from the CLI");
    expect(whereWords("")).toBe("");
    expect(whereWords("a place this build does not know")).toBe("");
  });

  it("says the time of day on a 24-hour clock", () => {
    expect(clockOf(new Date(2026, 10, 13, 14, 36, 9).getTime())).toBe("14:36");
    expect(clockOf(new Date(2026, 10, 13, 9, 5, 0).getTime())).toBe("09:05");
  });
});

describe("a resolution", () => {
  const at = new Date(2026, 10, 13, 14, 36).getTime();

  it("says the outcome, who decided it, where, and when", () => {
    const { container } = render(<Resolved table={cardStates} state="used" by="member:member-1" channel="web" at={at} />);
    expect(container.textContent).toContain("Confirmed");
    expect(container.textContent).toContain("by member-1 in Needs you at 14:36");
  });

  it("names the current member you", () => {
    const { container } = render(
      <You.Provider value="member-1">
        <Resolved table={cardStates} state="used" by="member:member-1" channel="conversation" at={at} />
      </You.Provider>,
    );
    expect(container.textContent).toContain("Confirmed");
    expect(container.textContent).toContain("by you in the conversation at 14:36");
  });

  it("says nothing of who or where when the record does not say", () => {
    const { container } = render(<Resolved table={cardStates} state="void" />);
    expect(container.textContent).toBe("Void");
  });
});
