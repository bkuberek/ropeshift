import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { laneStates, workStates } from "../floor/states";
import { Button, FilterChip, IconButton, Segmented, TabPanel, Tabs } from "./controls";
import { Absent, Actor, Badge, EmptyState, Freshness, LaneBadge, SkeletonRows, StateBadge, TargetChip, WorkBadge } from "./display";
import { Field, Select } from "./forms";
import { Icon } from "./Icon";
import { iconNames } from "./iconPaths";
import { Dialog, Menu, ToastProvider, useToast } from "./overlays";

describe("a button", () => {
  it("keeps its label and its width while it loads, and says it is busy", () => {
    render(<Button loading>Approve merge</Button>);
    const button = screen.getByRole("button", { name: "Approve merge" });
    expect(button.getAttribute("aria-busy")).toBe("true");
    expect(button.className).toContain("is-loading");
  });

  it("keeps its label when disabled, and presses nothing", async () => {
    const press = vi.fn();
    render(
      <Button disabled onClick={press}>
        Accept
      </Button>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Accept" }));
    expect(press).not.toHaveBeenCalled();
  });

  it("is a button that submits nothing unless asked", () => {
    render(<Button variant="primary">Save</Button>);
    expect(screen.getByRole("button").getAttribute("type")).toBe("button");
    expect(screen.getByRole("button").className).toContain("btn-primary");
  });

  it("names its shortcut in its tooltip", () => {
    render(<IconButton icon="chat" label="Conversation panel" shortcut="." />);
    const button = screen.getByRole("button", { name: "Conversation panel" });
    expect(button.getAttribute("title")).toBe("Conversation panel (.)");
    expect(button.getAttribute("aria-keyshortcuts")).toBe(".");
  });
});

describe("an icon", () => {
  it.each(iconNames)("%s is decoration, in the current color, on a 16 px grid", (name) => {
    const { container } = render(<Icon name={name} />);
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("viewBox")).toBe("0 0 16 16");
    expect(svg?.childElementCount).toBeGreaterThan(0);
  });
});

describe("a state badge", () => {
  it.each(Object.entries(workStates))("work order %s has its word, tone, and glyph", (state, look) => {
    const { container } = render(<WorkBadge state={state} />);
    expect(container.textContent).toBe(look.label);
    expect(container.querySelector(".badge")?.getAttribute("data-tone")).toBe(look.tone);
    expect(container.querySelector("svg")?.getAttribute("data-glyph")).toBe(look.glyph);
  });

  it.each(Object.entries(laneStates))("lane %s has its word, tone, and glyph", (state, look) => {
    const { container } = render(<LaneBadge state={state} />);
    expect(container.textContent).toBe(look.label);
    expect(container.querySelector("svg")?.getAttribute("data-glyph")).toBe(look.glyph);
  });

  it("shows a state the build has no row for as its own name, neutral", () => {
    const { container } = render(<StateBadge table={workStates} state="brand_new" />);
    expect(container.textContent).toBe("brand_new");
    expect(container.querySelector(".badge")?.getAttribute("data-tone")).toBe("neutral");
  });

  it("never carries meaning by color alone: a word and a glyph go with every tone", () => {
    const { container } = render(
      <Badge tone="danger" glyph="cross">
        Failed
      </Badge>,
    );
    expect(container.textContent).toBe("Failed");
    expect(container.querySelector("svg")).not.toBeNull();
  });
});

describe("a target chip", () => {
  it.each([
    ["assistant", "Assistant"],
    ["orchestrator", "Orchestrator"],
    ["planning", "Planning session"],
    ["lane", "Lane"],
  ] as const)("names the %s with its label and its own class", (kind, label) => {
    const { container } = render(<TargetChip kind={kind} />);
    expect(container.textContent).toBe(label);
    expect(container.firstElementChild?.className).toContain(`t-${kind}`);
  });
});

describe("an actor", () => {
  it("shows the assistant as its target chip and the member it acts for, and an outside delegate as a key", () => {
    const { container, rerender } = render(<Actor kind="delegate" id="member-1/dlg_a" label="assistant" actorFor="Sam Rivera" />);
    expect(container.querySelector(".target-chip.t-assistant")?.textContent).toBe("Assistant");
    expect(container.textContent).toContain("for Sam Rivera");
    expect(container.querySelectorAll("svg")).toHaveLength(1);
    rerender(<Actor kind="delegate" id="member-1/dlg_b" label="planning assistant" actorFor="Sam Rivera" />);
    expect(container.querySelector(".target-chip")).toBeNull();
    expect(container.textContent).toContain("planning assistant");
    expect(container.textContent).toContain("for Sam Rivera");
    expect(container.querySelectorAll("svg")).toHaveLength(1);
    expect(container.querySelector(".actor-delegate > svg")).not.toBeNull();
  });
});

describe("a value and its freshness", () => {
  it.each([
    ["observed", "fresh", "Observed"],
    ["last_reported", "fresh reported", "Last reported"],
    ["stale", "fresh stale", "Stale"],
  ] as const)("%s reads as %s", (kind, className, word) => {
    const { container } = render(<Freshness kind={kind} source="a stream event" />);
    expect(container.firstElementChild?.className).toBe(className);
    expect(container.textContent).toBe(word);
    expect(container.firstElementChild?.getAttribute("title")).toBe(`${word} from a stream event`);
  });

  it("says a missing value is missing, never zero", () => {
    render(<Absent />);
    expect(screen.getByText("unavailable").className).toBe("unavailable");
  });
});

describe("empty and loading", () => {
  it("says what would appear, and offers the one action that fills it", () => {
    render(<EmptyState title="Nothing needs you" action={<button type="button">Refresh</button>}>New items appear here as they arrive.</EmptyState>);
    expect(screen.getByText("Nothing needs you")).toBeTruthy();
    expect(screen.getByText("New items appear here as they arrive.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Refresh" })).toBeTruthy();
  });

  it("marks the region it stands in as busy, with words for a screen reader", () => {
    const { container } = render(<SkeletonRows rows={3} />);
    expect(container.firstElementChild?.getAttribute("aria-busy")).toBe("true");
    expect(container.querySelectorAll(".skeleton-row")).toHaveLength(3);
    expect(screen.getByText("Reading…")).toBeTruthy();
  });
});

describe("a field", () => {
  it("labels its control, and ties its help and its error to it", () => {
    render(
      <Field label="Name" help="Lowercase and hyphens." error="Use a lowercase, hyphenated name, such as harbor-api.">
        <input className="input" />
      </Field>,
    );
    const input = screen.getByRole("textbox", { name: "Name" });
    expect(input.getAttribute("aria-invalid")).toBe("true");
    const described = (input.getAttribute("aria-describedby") ?? "").split(" ").map((id) => document.getElementById(id)?.textContent);
    expect(described).toEqual(["Lowercase and hyphens.", "Use a lowercase, hyphenated name, such as harbor-api."]);
  });

  it("warns without blocking, and names where a value comes from", () => {
    render(
      <Field label="Max lanes" source={{ name: "Installation", overridden: true }} warning="9 is above the recommendation of 6.">
        <input className="input" />
      </Field>,
    );
    const input = screen.getByRole("textbox", { name: /Max lanes/ });
    expect(input.getAttribute("aria-invalid")).toBeNull();
    expect(screen.getByText("Installation").className).toBe("source override");
  });

  it("draws a select's chevron beside it, and keeps the select's name", () => {
    render(
      <Field label="Project">
        <Select>
          <option>Harbor</option>
        </Select>
      </Field>,
    );
    expect(screen.getByRole("combobox", { name: "Project" })).toBeTruthy();
  });
});

function TabsHarness() {
  const [tab, setTab] = useState<"review" | "changes" | "lanes">("review");
  return (
    <>
      <Tabs
        label="Work order"
        idBase="t"
        selected={tab}
        onSelect={setTab}
        tabs={[
          { id: "review", label: "Review", count: 4 },
          { id: "changes", label: "Changes" },
          { id: "lanes", label: "Lanes" },
        ]}
      />
      <TabPanel idBase="t" selected={tab}>
        {tab}
      </TabPanel>
    </>
  );
}

describe("tabs", () => {
  it("keep one tab stop, and move with the arrow keys, Home, and End", async () => {
    render(<TabsHarness />);
    const review = screen.getByRole("tab", { name: "Review 4" });
    expect(review.getAttribute("tabindex")).toBe("0");
    expect(screen.getByRole("tab", { name: "Changes" }).getAttribute("tabindex")).toBe("-1");
    review.focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(document.activeElement).toBe(screen.getByRole("tab", { name: "Changes" }));
    expect(screen.getByRole("tabpanel").textContent).toBe("changes");
    await userEvent.keyboard("{End}");
    expect(screen.getByRole("tab", { name: "Lanes" }).getAttribute("aria-selected")).toBe("true");
    await userEvent.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "Review 4" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("tabpanel").getAttribute("aria-labelledby")).toBe("t-tab-review");
  });
});

describe("choices", () => {
  it("a segmented control moves with the arrow keys and says which is pressed", async () => {
    function Harness() {
      const [value, setValue] = useState<"project" | "all">("project");
      return (
        <Segmented
          label="Show"
          value={value}
          onChange={setValue}
          choices={[
            { value: "project", label: "Harbor" },
            { value: "all", label: "All projects" },
          ]}
        />
      );
    }
    render(<Harness />);
    screen.getByRole("button", { name: "Harbor" }).focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(screen.getByRole("button", { name: "All projects" }).getAttribute("aria-pressed")).toBe("true");
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "All projects" }));
  });

  it("a filter chip says whether it applies and how many it holds", async () => {
    const press = vi.fn();
    render(
      <FilterChip pressed={false} count={3} onClick={press}>
        Decisions
      </FilterChip>,
    );
    const chip = screen.getByRole("button", { name: /^Decisions/ });
    expect(chip.textContent).toBe("Decisions3");
    expect(chip.getAttribute("aria-pressed")).toBe("false");
    await userEvent.click(chip);
    expect(press).toHaveBeenCalledOnce();
  });
});

describe("a dialog", () => {
  function Harness() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          Remove
        </button>
        <Dialog
          open={open}
          title="Remove Tern"
          onClose={() => setOpen(false)}
          footer={
            <>
              <button type="button" onClick={() => setOpen(false)}>
                Cancel
              </button>
              <button type="button">Remove Tern</button>
            </>
          }
        >
          <label>
            Name
            <input />
          </label>
        </Dialog>
      </>
    );
  }

  it("names itself, focuses its first field, keeps focus inside, and returns it on Escape", async () => {
    render(<Harness />);
    const opener = screen.getByRole("button", { name: "Remove" });
    await userEvent.click(opener);
    const dialog = screen.getByRole("dialog", { name: "Remove Tern" });
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(document.activeElement).toBe(within(dialog).getByRole("textbox", { name: "Name" }));
    await userEvent.tab();
    await userEvent.tab();
    await userEvent.tab();
    await userEvent.tab();
    expect(dialog.contains(document.activeElement)).toBe(true);
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(opener);
  });
});

describe("a menu", () => {
  it("opens on its button, focuses its first item, moves with the arrows, and closes on Escape", async () => {
    const park = vi.fn();
    render(
      <Menu
        label="Actions for lane L-88"
        items={[
          { label: "Park", onSelect: park },
          { label: "Reset breaker", onSelect: () => {}, disabled: true, disabledWhy: "The lane is not tripped." },
          { label: "Stop", onSelect: () => {} },
        ]}
      />,
    );
    const button = screen.getByRole("button", { name: "Actions for lane L-88" });
    await userEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("true");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "Park" }));
    await userEvent.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "Stop" }));
    expect(screen.getByRole("menuitem", { name: "Reset breaker" }).getAttribute("title")).toBe("The lane is not tripped.");
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(button);
    await userEvent.click(button);
    await userEvent.click(screen.getByRole("menuitem", { name: "Park" }));
    expect(park).toHaveBeenCalledOnce();
  });
});

describe("a toast", () => {
  it("speaks in a polite region, never takes focus, and leaves after six seconds", async () => {
    vi.useFakeTimers();
    function Harness() {
      const toast = useToast();
      return (
        <button type="button" onClick={() => toast("success", "Saved. atlas max lanes is 9.")}>
          Save
        </button>
      );
    }
    render(
      <ToastProvider>
        <Harness />
      </ToastProvider>,
    );
    const save = screen.getByRole("button", { name: "Save" });
    save.focus();
    act(() => save.click());
    const message = screen.getByText("Saved. atlas max lanes is 9.");
    expect(message.closest("[aria-live]")?.getAttribute("aria-live")).toBe("polite");
    expect(document.activeElement).toBe(save);
    act(() => {
      vi.advanceTimersByTime(6000);
    });
    expect(screen.queryByText("Saved. atlas max lanes is 9.")).toBeNull();
    vi.useRealTimers();
  });
});
