import { cleanup, configure } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

// A view reads several read models before it shows, and a busy machine runs the suite slower, so a query
// that waits for one waits this long before it fails.
configure({ asyncUtilTimeout: 4000 });

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
