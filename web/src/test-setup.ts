import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  document.cookie = "ropeshift_csrf=; max-age=0";
  localStorage.clear();
});
