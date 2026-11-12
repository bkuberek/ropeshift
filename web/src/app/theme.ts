import { useEffect, useState } from "react";

export type Theme = "system" | "light" | "dark";

const key = "ropeshift-theme";
const order: Theme[] = ["system", "light", "dark"];

// The surface color of each theme, which the browser's own chrome takes through the theme-color meta.
const surfaces = { light: "#ffffff", dark: "#13161a" };

export const themeWords: Record<Theme, string> = { system: "Theme: system", light: "Theme: light", dark: "Theme: dark" };

// This is a convenience of one browser, and storage can throw or come back empty, so nothing depends
// on it and a failure leaves the page on the system's theme.
function remembered(): Theme {
  try {
    const value = localStorage.getItem(key);
    return value === "light" || value === "dark" ? value : "system";
  } catch {
    return "system";
  }
}

function remember(theme: Theme): void {
  try {
    if (theme === "system") {
      localStorage.removeItem(key);
    } else {
      localStorage.setItem(key, theme);
    }
  } catch {
    return;
  }
}

function systemDark(): boolean {
  try {
    return window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false;
  } catch {
    return false;
  }
}

// paintChrome sets the theme-color meta to the surface of the theme in force, so the browser's bar
// matches the page's.
function paintChrome(theme: Theme): void {
  const dark = theme === "dark" || (theme === "system" && systemDark());
  for (const meta of document.querySelectorAll('meta[name="theme-color"]')) {
    meta.remove();
  }
  const meta = document.createElement("meta");
  meta.name = "theme-color";
  meta.content = dark ? surfaces.dark : surfaces.light;
  document.head.append(meta);
}

export function useTheme(): { theme: Theme; nextTheme: () => void } {
  const [theme, setTheme] = useState<Theme>(remembered);
  useEffect(() => {
    if (theme === "system") {
      delete document.documentElement.dataset.theme;
    } else {
      document.documentElement.dataset.theme = theme;
    }
    remember(theme);
    paintChrome(theme);
  }, [theme]);
  const nextTheme = () => setTheme((was) => order[(order.indexOf(was) + 1) % order.length] ?? "system");
  return { theme, nextTheme };
}
