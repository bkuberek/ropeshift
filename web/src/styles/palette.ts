// Reading the tokens file as it ships, so a test holds the colors a page draws and not a copy of them.

export type Palette = Record<string, string>;

// palette reads every six-digit color a block declares, by its name without the leading dashes.
export function palette(block: string): Palette {
  const out: Palette = {};
  for (const [, name, value] of block.matchAll(/--([\w-]+):\s*(#[0-9a-f]{6});/g)) {
    if (name && value) {
      out[name] = value;
    }
  }
  return out;
}

const darkChoice = ':root[data-theme="dark"]';
const darkSystem = "@media (prefers-color-scheme: dark)";

// themes splits the tokens file into its three declarations: light under :root, dark when the system
// asks for it, and dark when a person chose it. Each theme is complete, so the dark ones lay over light.
export function themes(css: string): { light: Palette; dark: Palette; darkBySystem: Palette } {
  const system = css.indexOf(darkSystem);
  const chosen = css.indexOf(darkChoice);
  const light = palette(css.slice(0, system));
  return {
    light,
    darkBySystem: { ...light, ...palette(css.slice(system, chosen)) },
    dark: { ...light, ...palette(css.slice(chosen, css.indexOf("}", chosen))) },
  };
}

function channel(value: number): number {
  return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
}

// luminance is WCAG's relative luminance of a six-digit color.
export function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((at) => channel(Number.parseInt(hex.slice(at, at + 2), 16) / 255)) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

// contrast is WCAG's contrast ratio between two colors, in either order.
export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}
