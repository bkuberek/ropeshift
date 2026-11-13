import { fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import { here } from "../testing";
import { serveTalk, talking } from "../talk/serve";
import { clampDock, dockWidth } from "./prefs";

const open = async () => {
  serveTalk(talking());
  openPage(here("/roadmap"));
  await screen.findByRole("heading", { level: 1, name: "Roadmap" });
  await userEvent.keyboard(".");
  const dock = await screen.findByRole("complementary", { name: "Conversation" });
  return { dock, separator: within(dock).getByRole("separator", { name: "Resize the conversation panel" }) };
};

const wide = (separator: HTMLElement) => Number(separator.getAttribute("aria-valuenow"));

describe("the dock's width", () => {
  it("is a separator that says its width and its bounds", async () => {
    const { separator } = await open();
    expect(separator.getAttribute("aria-orientation")).toBe("vertical");
    expect(separator.getAttribute("aria-valuemin")).toBe("360");
    expect(separator.getAttribute("aria-valuemax")).toBe("640");
    expect(wide(separator)).toBe(420);
    expect(separator.tabIndex).toBe(0);
    expect((document.querySelector(".app") as HTMLElement).style.getPropertyValue("--dock-w")).toBe("420px");
  });

  it("steps 20 px with the arrow keys, a left key widening the dock that stands on the right", async () => {
    const { separator } = await open();
    separator.focus();
    await userEvent.keyboard("{ArrowLeft}{ArrowLeft}");
    expect(wide(separator)).toBe(460);
    await userEvent.keyboard("{ArrowRight}");
    expect(wide(separator)).toBe(440);
    expect((document.querySelector(".app") as HTMLElement).style.getPropertyValue("--dock-w")).toBe("440px");
  });

  it("stops at 360 and at 640", async () => {
    const { separator } = await open();
    separator.focus();
    await userEvent.keyboard("{End}");
    expect(wide(separator)).toBe(640);
    await userEvent.keyboard("{ArrowLeft}");
    expect(wide(separator)).toBe(640);
    await userEvent.keyboard("{Home}{ArrowRight}");
    expect(wide(separator)).toBe(360);
  });

  it("is dragged with a pointer, and no action needs the drag", async () => {
    const { separator } = await open();
    fireEvent.pointerDown(separator, { clientX: 900, pointerId: 1 });
    fireEvent.pointerMove(separator, { clientX: 850, pointerId: 1 });
    expect(wide(separator)).toBe(470);
    fireEvent.pointerUp(separator, { pointerId: 1 });
    fireEvent.pointerMove(separator, { clientX: 500, pointerId: 1 });
    expect(wide(separator)).toBe(470);
  });

  it("is remembered by the browser, and a stored value out of bounds is held to them", async () => {
    const { separator } = await open();
    separator.focus();
    await userEvent.keyboard("{ArrowLeft}");
    expect(localStorage.getItem("ropeshift-dock-width")).toBe("440");
    expect(clampDock(100)).toBe(dockWidth.min);
    expect(clampDock(9000)).toBe(dockWidth.max);
    expect(clampDock(500.4)).toBe(500);
  });
});
