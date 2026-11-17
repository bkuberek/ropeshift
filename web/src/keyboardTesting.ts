import type { Locator, Page, Route } from "playwright-core";

import { answer } from "./browserApp";

// What the keyboard suite shares: the keys a path presses, and the daemon that records each change it is sent. A path
// reaches a control with the palette, the G keys, Tab, Enter, the arrows, and Esc, and never a pointer.

export type Call = { method: string; path: string; body: unknown };

// A change is answered with an empty success, so a path can go on to what follows it. Every read is the daemon's
// answer the accessibility suite reads. A path may answer a read or a change of its own first, with answers.
export function serveChanges(sent: Call[], answers: (route: Route) => Promise<boolean> | boolean = () => false) {
  return async (route: Route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() !== "GET") {
      let body: unknown = null;
      try {
        body = request.postDataJSON();
      } catch {
        body = request.postData();
      }
      sent.push({ method: request.method(), path: url.pathname, body });
      if (await answers(route)) {
        return;
      }
      if (/\/backup\/download$/.test(url.pathname)) {
        return route.fulfill({ status: 200, contentType: "application/octet-stream", headers: { "content-disposition": 'attachment; filename="backup.db"' }, body: "SQLite format 3\0" });
      }
      return route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
    }
    if (await answers(route)) {
      return;
    }
    return answer(route);
  };
}

const mod = process.platform === "darwin" ? "Meta" : "Control";

export class Keys {
  constructor(readonly page: Page) {}

  // press presses each key. Mod is Command on a Mac and Control elsewhere, as the application reads it.
  async press(...keys: string[]): Promise<void> {
    for (const key of keys) {
      await this.page.keyboard.press(key.replace(/^Mod\+/, `${mod}+`));
    }
  }

  // type types text into the field the keyboard is on, a key at a time with a short pause so a controlled field keeps up
  // on a slow runner, and waits until the field holds what was typed. Keys typed while the keyboard is on no field land
  // nowhere, so it first waits for one to take it. A field that never does fails the path, naming what it holds.
  async type(text: string): Promise<void> {
    try {
      await this.page.waitForFunction(() => document.activeElement instanceof HTMLInputElement || document.activeElement instanceof HTMLTextAreaElement, undefined, { timeout: 10_000 });
    } catch {
      throw new Error(`The keyboard never reached a field to type "${text}" into. It is on ${await this.where()}`);
    }
    await this.page.keyboard.type(text, { delay: 5 });
    try {
      await this.page.waitForFunction(
        (typed) => {
          const field = document.activeElement as HTMLInputElement | HTMLTextAreaElement | null;
          return !!field && "value" in field && field.value.endsWith(typed);
        },
        text,
        { timeout: 5000 },
      );
    } catch {
      const held = await this.page.evaluate(() => (document.activeElement as HTMLInputElement | null)?.value ?? "");
      throw new Error(`The field was to end with "${text}" and holds "${held}"`);
    }
  }

  // go presses G, and then the key of a view.
  async go(key: string): Promise<void> {
    await this.page.keyboard.press("g");
    await this.page.keyboard.press(key);
  }

  // palette opens the command palette, types a query, moves with the arrow keys to the action whose name starts with
  // title, and runs it with Enter. Without a query it types the first words of the title: the palette's own matching
  // finds the action from them, and the arrows reach it among the others that match.
  async palette(title: string, query = `>${title.slice(0, 22)}`): Promise<void> {
    // A view a person moved to takes focus on its title once it has read, so the palette opens after that.
    await this.page.locator("main h1:visible").first().waitFor();
    await this.page.waitForFunction(() => !document.querySelector("main .skeleton-rows"));
    await this.page.waitForTimeout(400);
    await this.press("Mod+k");
    await this.page.getByRole("dialog", { name: "Command palette" }).waitFor();
    await this.landed(this.page.getByRole("combobox"));
    // The field empties as the palette opens, which a key typed in the same moment can beat, so it is emptied here.
    await this.press("Mod+a", "Backspace");
    await this.type(query);
    const option = this.page.getByRole("option", { name: new RegExp(`^${title.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`) }).first();
    try {
      await option.waitFor({ timeout: 10_000 });
    } catch {
      const listed = await this.page.getByRole("option").allTextContents();
      const main = (await this.page.locator("#main").innerText().catch(() => "")).replace(/\s+/g, " ").slice(0, 300);
      const typed = await this.page.getByRole("combobox").inputValue().catch(() => "?");
      throw new Error(`The palette lists no action that starts with "${title}" for "${query}" (the field holds "${typed}") on ${await this.where()}. It lists ${listed.slice(0, 12).join(" | ")}. The page reads: ${main}`);
    }
    // A list settles as the page's own actions arrive, so the choice is made on the list that stays.
    await this.page.waitForTimeout(250);
    for (let at = 0; at < 60 && (await option.getAttribute("aria-selected")) !== "true"; at++) {
      await this.page.keyboard.press("ArrowDown");
    }
    await this.page.keyboard.press("Enter");
    try {
      await this.page.getByRole("dialog", { name: "Command palette" }).waitFor({ state: "detached", timeout: 5000 });
    } catch {
      const rows = await this.page.getByRole("option").evaluateAll((all) => all.map((one) => `${one.getAttribute("aria-selected") === "true" ? "*" : ""}${(one.textContent ?? "").slice(0, 40)}`));
      throw new Error(`Enter did not run "${title}" for "${query}". The keyboard is on ${await this.where()}. The list is ${rows.slice(0, 10).join(" | ")}`);
    }
  }

  // focused says whether the control is the one the keyboard is on.
  async focused(control: Locator): Promise<boolean> {
    return control.evaluateAll((elements) => elements.some((element) => element === document.activeElement));
  }

  // landed waits until the keyboard is on the control, which an address that names a control to focus puts it on once
  // the view has read.
  async landed(control: Locator, timeoutMs = 10_000): Promise<void> {
    const until = Date.now() + timeoutMs;
    while (Date.now() < until) {
      if (await this.focused(control)) {
        return;
      }
      await this.page.waitForTimeout(100);
    }
    throw new Error(`The keyboard never landed on ${String(control)}. It is on ${await this.where()}`);
  }

  // where describes the control the keyboard is on.
  async where(): Promise<string> {
    return this.page.evaluate(() => {
      const element = document.activeElement as HTMLElement | null;
      return element ? `${element.tagName.toLowerCase()} "${(element.getAttribute("aria-label") || element.textContent || (element as HTMLInputElement).value || "").trim().slice(0, 60)}" in ${location.pathname}${location.search}` : "nothing";
    });
  }

  // rendered waits until the control is in the page. A view reads its data after the keys that moved to it, and a view's
  // code may load first, so a walk that starts at once cycles through the frame while the control is still to come, and
  // can spend every press before it arrives. A control that never renders fails here, by name and by address, which is
  // not the failure of a control that renders and cannot be reached with Tab. Waiting uses no pointer.
  async rendered(control: Locator, timeoutMs = 20_000): Promise<void> {
    try {
      await control.first().waitFor({ state: "attached", timeout: timeoutMs });
    } catch {
      throw new Error(`The control ${String(control)} never rendered on ${await this.page.evaluate(() => location.pathname + location.search)} in ${timeoutMs} ms`);
    }
  }

  // tabTo waits for the control to render, and then presses Tab, or Shift+Tab, until the keyboard is on the control, and
  // gives up after max presses.
  async tabTo(control: Locator, { back = false, max = 120 } = {}): Promise<void> {
    await this.rendered(control);
    const visited: string[] = [];
    for (let at = 0; at < max; at++) {
      if (await this.focused(control)) {
        return;
      }
      await this.page.keyboard.press(back ? "Shift+Tab" : "Tab");
      visited.push(await this.page.evaluate(() => {
        const element = document.activeElement as HTMLElement | null;
        return element ? `${element.tagName.toLowerCase()} "${(element.getAttribute("aria-label") || element.textContent || (element as HTMLInputElement).value || "").trim().slice(0, 40)}"` : "nothing";
      }));
    }
    throw new Error(`Tab never reached ${String(control)} in ${max} presses. It visited ${[...new Set(visited)].join(", ")}`);
  }

  // arrowTo presses the Down arrow until the keyboard is on the control, which moves within a menu.
  async arrowTo(control: Locator, max = 30): Promise<void> {
    for (let at = 0; at < max; at++) {
      if (await this.focused(control)) {
        return;
      }
      await this.page.keyboard.press("ArrowDown");
    }
    throw new Error(`The Down arrow never reached ${String(control)} in ${max} presses. The keyboard is on ${await this.where()}`);
  }

  // activate reaches a control with Tab and presses it with Enter.
  async activate(control: Locator, options?: { back?: boolean }): Promise<void> {
    await this.tabTo(control, options);
    await this.page.keyboard.press("Enter");
  }

  // replaceIn reaches a field with Tab, selects what it holds, and types over it.
  async replaceIn(control: Locator, text: string): Promise<void> {
    await this.tabTo(control);
    await this.press("Mod+a");
    await this.type(text);
  }

  // typeInto reaches a field with Tab and types into it.
  async typeInto(control: Locator, text: string): Promise<void> {
    await this.tabTo(control);
    await this.type(text);
  }
}
