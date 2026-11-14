import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ToastProvider } from "../ui/overlays";
import { MessageText } from "./MessageText";

const show = (text: string) => render(<ToastProvider><MessageText text={text} /></ToastProvider>);

describe("a message's text", () => {
  it("shows paragraphs, inline code, a list, and a fenced block", () => {
    const { container } = show("Run `go test`.\n\nThen:\n\n- one\n- two\n\n```sh\nmake check\n```");
    expect(container.querySelectorAll("p")).toHaveLength(2);
    expect(container.querySelector("p code")?.textContent).toBe("go test");
    expect(container.querySelectorAll("ul li")).toHaveLength(2);
    expect(container.querySelector("pre code")?.textContent).toBe("make check");
  });

  it("shows a citation of a record as a chip that opens its view", () => {
    show("The retry work is [[work_order:wo-a]].");
    const chip = screen.getByRole("link", { name: "Work order wo-a" });
    expect(chip.textContent).toBe("wo-a");
    expect(chip.getAttribute("href")).toBe("/projects/default/work-order?id=wo-a");
  });

  it("shows a citation of code as a chip that copies its location, because no view shows code", async () => {
    const copy = vi.fn(async () => {});
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText: copy } });
    show("See [[code:harbor-web:src/app.ts#L42@a1b2c3d4e5]].");
    const chip = screen.getByRole("button", { name: "Copy harbor-web/src/app.ts:42 @a1b2c3d" });
    expect(chip.textContent).toBe("harbor-web/src/app.ts:42 @a1b2c3d");
    expect(chip.closest("a")).toBeNull();
    await userEvent.click(chip);
    expect(copy).toHaveBeenCalledWith("harbor-web/src/app.ts:42 @a1b2c3d");
    expect(await screen.findByText("Copied harbor-web/src/app.ts:42 @a1b2c3d.")).toBeTruthy();
  });

  it("shows an unknown kind as its text", () => {
    const { container } = show("Nothing links [[wizard:abc]].");
    expect(container.textContent).toBe("Nothing links [[wizard:abc]].");
    expect(container.querySelector("a")).toBeNull();
  });

  it("never makes markup of what an agent wrote", () => {
    const { container } = show('<img src=x onerror="alert(1)"> <script>alert(1)</script> [x](javascript:alert(1)) **bold**');
    expect(container.querySelector("img, script, a")).toBeNull();
    expect(container.textContent).toContain("<script>alert(1)</script>");
    expect(container.textContent).toContain("**bold**");
  });
});
