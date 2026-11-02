import { rm } from "node:fs/promises";

import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// The build writes into static, which the binary embeds, and static holds a page that is committed. So
// this removes only an earlier build's files, and an empty output directory never takes that page.
const cleanBuild = {
  name: "clean-build",
  apply: "build" as const,
  async buildStart() {
    await rm("static/assets", { recursive: true, force: true });
    await rm("static/index.html", { force: true });
    await rm("static/icon.svg", { force: true });
  },
};

export default defineConfig({
  plugins: [react(), cleanBuild],
  server: { host: "127.0.0.1" },
  build: {
    outDir: "static",
    emptyOutDir: false,
    // A font or image in a data URI would need a wider content policy.
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
  },
  test: {
    environment: "happy-dom",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["src/test-setup.ts"],
    restoreMocks: true,
  },
});
