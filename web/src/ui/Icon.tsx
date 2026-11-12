import type { ReactNode } from "react";

import type { IconName } from "./iconPaths";
import { paths } from "./iconPaths";

export type { IconName } from "./iconPaths";

type IconSize = "sm" | "md" | "lg";

const sizeClass: Record<IconSize, string> = { sm: "i i-sm", md: "i", lg: "i i-lg" };

// Icon is decoration: the control or the words beside it carry the name.
export function Icon({ name, size = "md", className = "" }: { name: IconName; size?: IconSize; className?: string }) {
  return (
    <svg className={`${sizeClass[size]}${className ? ` ${className}` : ""}`} viewBox="0 0 16 16" aria-hidden="true" focusable="false">
      {paths[name]}
    </svg>
  );
}

// The glyph shapes a state badge carries, so a state reads without its color.
export type GlyphName = "ring" | "target" | "dot" | "half" | "pause" | "check" | "cross" | "triangle" | "question" | "slash" | "person" | "arrow";

const ring = <circle cx="5" cy="5" r="3.6" fill="none" stroke="currentColor" strokeWidth="1.6" />;

const glyphs: Record<GlyphName, ReactNode> = {
  ring,
  target: (
    <>
      {ring}
      <circle cx="5" cy="5" r="1.5" fill="currentColor" />
    </>
  ),
  dot: <circle cx="5" cy="5" r="4" fill="currentColor" />,
  half: (
    <>
      {ring}
      <path d="M5 1.4a3.6 3.6 0 0 1 0 7.2z" fill="currentColor" />
    </>
  ),
  pause: (
    <>
      <rect x="2" y="1.5" width="2.2" height="7" rx=".6" fill="currentColor" />
      <rect x="5.8" y="1.5" width="2.2" height="7" rx=".6" fill="currentColor" />
    </>
  ),
  check: <path d="M1.8 5.3 4 7.4l4.2-4.8" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />,
  cross: <path d="M2.2 2.2l5.6 5.6M7.8 2.2 2.2 7.8" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />,
  triangle: <path d="M5 .8 9.4 8.8H.6z" fill="currentColor" />,
  question: (
    <>
      <path d="M3.2 3.6a1.9 1.9 0 1 1 2.6 1.8c-.5.2-.8.6-.8 1.1v.4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      <circle cx="5" cy="8.6" r=".9" fill="currentColor" />
    </>
  ),
  slash: (
    <>
      {ring}
      <path d="M2.5 7.5l5-5" stroke="currentColor" strokeWidth="1.6" />
    </>
  ),
  person: (
    <>
      <circle cx="5" cy="3" r="2.1" fill="currentColor" />
      <path d="M1 9.6c.4-2.3 2-3.4 4-3.4s3.6 1.1 4 3.4z" fill="currentColor" />
    </>
  ),
  arrow: <path d="M1.5 5h6M5 2.2 7.8 5 5 7.8" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" />,
};

export function Glyph({ name, className = "glyph" }: { name: GlyphName; className?: string }) {
  return (
    <svg className={className} viewBox="0 0 10 10" aria-hidden="true" focusable="false" data-glyph={name}>
      {glyphs[name]}
    </svg>
  );
}
