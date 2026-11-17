import { render } from "@testing-library/react";
import { vi } from "vitest";

import { App } from "./app/App";
import { stubEvents } from "./testing";

// openPage stands for a person who follows a link: the whole application, at one address, on whatever
// a test serves. state is the history entry's state, which a move the application made itself may carry.
export function openPage(path: string, state: unknown = null) {
  window.history.replaceState(state, "", path);
  stubEvents();
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  return render(<App />);
}
