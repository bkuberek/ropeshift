import { useEffect, useState } from "react";

export type Theme = "system" | "light" | "dark";

const key = "ropeshift-theme";
const order: Theme[] = ["system", "light", "dark"];

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

export function useTheme(): { theme: Theme; nextTheme: () => void } {
  const [theme, setTheme] = useState<Theme>(remembered);
  useEffect(() => {
    if (theme === "system") {
      delete document.documentElement.dataset.theme;
    } else {
      document.documentElement.dataset.theme = theme;
    }
    remember(theme);
  }, [theme]);
  const nextTheme = () => setTheme((was) => order[(order.indexOf(was) + 1) % order.length] ?? "system");
  return { theme, nextTheme };
}
