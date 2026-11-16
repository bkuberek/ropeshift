import type { Page } from "playwright-core";

// The types of web/scripts/guides.mjs, for the test that drives it. A guide file in test/screenshots/guides holds
// a Guide as JSON.

// A control or region, by its role and accessible name. A name matches whole, unless exact is false.
export type Locate = { role: string; name: string; exact?: boolean };

export type Action =
  | { click: Locate & { in?: Locate } }
  | { fill: { label: string; value: string; exact?: boolean; in?: Locate } }
  | { press: string }
  | { waitFor: { text: string; exact?: boolean; in?: Locate } };

// A clip is the window, or an element named by a selector or by a role and name, with pad pixels around it.
export type Clip = "window" | ({ selector: string; pad?: number } | (Locate & { pad?: number }));

export type Step = {
  step: string;
  route?: string;
  actions?: Action[];
  capture?: { name: string; clip: Clip };
};

export type Guide = {
  guide: string;
  title?: string;
  world?: string;
  order?: number;
  signed_out?: boolean;
  steps: Step[];
};

export type RunOptions = {
  base: string;
  out: string;
  theme: string;
  vars?: Record<string, string>;
  private?: string[];
  capture: boolean;
  timeout?: number;
};

export function expand(text: string, vars: Record<string, string>): string;

export function runGuide(page: Page, guide: Guide, options: RunOptions): Promise<{ taken: number; failure?: string }>;
