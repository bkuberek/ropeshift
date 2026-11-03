import { render } from "@testing-library/react";
import { vi } from "vitest";

import { App } from "./app/App";
import { stubEvents } from "./testing";

// openPage stands for a person who follows a link: the whole application, at one address, on whatever
// a test serves.
export function openPage(path: string) {
  window.history.replaceState(null, "", path);
  stubEvents();
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  return render(<App />);
}
