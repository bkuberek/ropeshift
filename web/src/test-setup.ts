import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

// A page shows one project, which its address names, so every test starts inside the one it serves.
beforeEach(() => {
  window.history.replaceState(null, "", "/projects/default/");
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  document.cookie = "ropeshift_csrf=; max-age=0";
  localStorage.clear();
  window.history.replaceState(null, "", "/");
});
