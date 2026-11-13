import { readdirSync } from "node:fs";
import { join, relative } from "node:path";
import { expect, test } from "vitest";

// A case-insensitive filesystem (macOS, Windows) cannot hold two files whose
// paths differ only in case, and TypeScript resolves "./Diff" and "./diff"
// to the same module there. Compare paths without their extensions.
const webRoot = join(import.meta.dirname, "..");
const skipped = new Set(["node_modules", "static", "dist", "coverage", ".vite"]);

function files(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    if (skipped.has(entry.name)) return [];
    const path = join(dir, entry.name);
    return entry.isDirectory() ? files(path) : [path];
  });
}

test("no two web files share a path ignoring case and extension", () => {
  const seen = new Map<string, string>();
  const clashes: string[] = [];
  for (const file of files(webRoot)) {
    const path = relative(webRoot, file);
    const key = path.replace(/\.[^./]+$/, "").toLowerCase();
    const other = seen.get(key);
    if (other !== undefined) clashes.push(`${other} and ${path}`);
    else seen.set(key, path);
  }
  expect(clashes).toEqual([]);
});
