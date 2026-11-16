import { useEffect } from "react";

import type { Route } from "./route";
import { go } from "./route";

// The command palette takes a member to a control that has no dialog of its own with `?focus=Name`: the view opens,
// and the control whose name starts with Name takes focus. The palette never presses it. `primary` is a decision's
// own parameter, which Needs you reads.

const controls = "button, a[href], input, select, textarea, [role=tab], [role=switch], [role=menuitem]";

// nameOf is the name a control shows or announces: its label, or the words in it. A control that stands once for each
// record of a list, and shows the same words on each, names itself with data-focus.
function nameOf(element: HTMLElement): string {
  const own = element.dataset.focus;
  if (own) {
    return own;
  }
  const label = element.getAttribute("aria-label");
  if (label) {
    return label;
  }
  const by = element.getAttribute("aria-labelledby");
  if (by) {
    return by
      .split(/\s+/)
      .map((id) => document.getElementById(id)?.textContent ?? "")
      .join(" ");
  }
  if (element instanceof HTMLInputElement || element instanceof HTMLSelectElement || element instanceof HTMLTextAreaElement) {
    const named = [...(element.labels ?? [])].map((one) => one.textContent ?? "").join(" ");
    if (named) {
      return named;
    }
  }
  return element.textContent ?? "";
}

const plain = (text: string) => text.replace(/\s+/g, " ").trim().toLowerCase();

function find(wanted: string): HTMLElement | undefined {
  return [...document.querySelectorAll<HTMLElement>(`#main ${controls}`)].find(
    (element) => !element.closest("[hidden]") && !element.hasAttribute("disabled") && plain(nameOf(element)).startsWith(wanted),
  );
}

// A view reads before its controls stand, so the search waits for them, and gives up after this long.
const waitMs = 6000;

// useFocusParam focuses the control an address names, once the page is signed in, and takes the name off the
// address whether or not it found one.
export function useFocusParam(route: Route, signedIn: boolean): void {
  const asked = route.params.get("focus") ?? "";
  useEffect(() => {
    if (!signedIn || !asked || asked === "primary") {
      return;
    }
    const wanted = plain(asked);
    const leave = () => {
      const rest = new URLSearchParams(route.params);
      rest.delete("focus");
      const query = rest.toString();
      go(`${route.pathname}${query ? `?${query}` : ""}`, { stay: true, replace: true });
    };
    const found = find(wanted);
    if (found) {
      found.focus();
      leave();
      return;
    }
    const main = document.getElementById("main");
    if (!main) {
      return;
    }
    const watch = new MutationObserver(() => {
      const control = find(wanted);
      if (control) {
        watch.disconnect();
        clearTimeout(giveUp);
        control.focus();
        leave();
      }
    });
    const giveUp = setTimeout(() => {
      watch.disconnect();
      leave();
    }, waitMs);
    watch.observe(main, { childList: true, subtree: true, attributes: true, attributeFilter: ["hidden", "disabled"] });
    return () => {
      watch.disconnect();
      clearTimeout(giveUp);
    };
  }, [signedIn, asked, route.pathname]);
}
