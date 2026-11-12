import type { ReactNode } from "react";

// The icon set, drawn for ropeshift on a 16 px grid with a 1.5 px stroke, round caps and joins, and no
// fill except for dots. Each renders as inline SVG in currentColor, hidden from assistive technology,
// and never stands alone as a control's only name.
export const iconNames = ["mark", "inbox", "grid", "roadmap", "plan", "doc", "trace", "decision", "question", "stack", "lanes", "review", "pr", "chat", "repo", "roster", "sliders", "history", "gauge", "server", "users", "projects", "measures", "search", "chevron-down", "chevron-right", "chevron-left", "chevron-updown", "plus", "x", "more", "check", "warning", "info", "external", "copy", "play", "pause", "stop", "restart", "retry", "replan", "power", "terminal", "hub", "flag", "delegate", "branch", "commit", "theme", "menu", "panel", "filter", "clock", "cpu", "memory", "disk", "key", "lock", "archive", "download", "upload", "trash", "link", "send", "folder", "import", "attach", "card", "bolt", "expand", "keyboard", "pencil"] as const;

export type IconName = (typeof iconNames)[number];

export const paths: Record<IconName, ReactNode> = {
  "mark": <path d="M3 4.5h6M5 8h8M3 11.5h7" />,
  "inbox": <>
    <path d="M2 9.5h3.2l1 1.8h3.6l1-1.8H14" />
    <path d="M3.6 3.5h8.8L14 9.5V13a.5.5 0 0 1-.5.5h-11A.5.5 0 0 1 2 13V9.5z" />
  </>,
  "grid": <>
    <rect x="2.5" y="2.5" width="4.5" height="4.5" rx="1" />
    <rect x="9" y="2.5" width="4.5" height="4.5" rx="1" />
    <rect x="2.5" y="9" width="4.5" height="4.5" rx="1" />
    <rect x="9" y="9" width="4.5" height="4.5" rx="1" />
  </>,
  "roadmap": <path d="M2.5 4h6M5.5 8h7.5M4 12h5" />,
  "plan": <>
    <rect x="3.5" y="2.5" width="9" height="11.5" rx="1.5" />
    <path d="M6 2.5h4v2H6z" />
    <path d="M6 8h4M6 11h2.5" />
  </>,
  "doc": <>
    <path d="M4 1.8h5.2L12.5 5v9.2H4z" />
    <path d="M9 2v3.3h3.3" />
    <path d="M6 8.5h4.5M6 11h4.5" />
  </>,
  "trace": <>
    <path d="M2.5 4l1.2 1.2L6 3" />
    <path d="M8 4.2h5.5" />
    <path d="M2.5 10l1.2 1.2L6 9" />
    <path d="M8 10.2h5.5" />
  </>,
  "decision": <>
    <path d="M8 1.8 14.2 8 8 14.2 1.8 8z" />
    <path d="M5.6 8h4.8" />
  </>,
  "question": <>
    <circle cx="8" cy="8" r="6" />
    <path d="M6.3 6.3a1.8 1.8 0 1 1 2.5 1.6c-.5.2-.8.6-.8 1.1v.4" />
    <path d="M8 11.4v.1" />
  </>,
  "stack": <>
    <path d="M8 2 14 5 8 8 2 5z" />
    <path d="M2 8l6 3 6-3" />
    <path d="M2 11l6 3 6-3" />
  </>,
  "lanes": <>
    <path d="M2 4h12M2 8h12M2 12h12" />
    <circle cx="5" cy="4" r="1.3" fill="currentColor" />
    <circle cx="10.5" cy="8" r="1.3" fill="currentColor" />
    <circle cx="7.5" cy="12" r="1.3" fill="currentColor" />
  </>,
  "review": <>
    <path d="M8 1.8 13 3.8v4c0 3-2.2 5.2-5 6.4-2.8-1.2-5-3.4-5-6.4v-4z" />
    <path d="M5.7 8.1 7.3 9.7l3-3.2" />
  </>,
  "pr": <>
    <circle cx="4" cy="3.5" r="1.5" />
    <circle cx="4" cy="12.5" r="1.5" />
    <circle cx="12" cy="12.5" r="1.5" />
    <path d="M4 5v6" />
    <path d="M12 11V6.5a2 2 0 0 0-2-2H7" />
    <path d="M8.5 3 7 4.5 8.5 6" />
  </>,
  "chat": <path d="M2.5 3.5h11v7.5H7l-3 2.5V11H2.5z" />,
  "repo": <>
    <path d="M3 3a1.5 1.5 0 0 1 1.5-1.5h8v10h-8A1.5 1.5 0 0 0 3 13z" />
    <path d="M3 13a1.5 1.5 0 0 0 1.5 1.5h8" />
    <path d="M6 5h4" />
  </>,
  "roster": <>
    <circle cx="4" cy="4.5" r="1.6" />
    <circle cx="4" cy="11.5" r="1.6" />
    <path d="M7.5 4.5h6M7.5 11.5h6" />
  </>,
  "sliders": <>
    <path d="M2.5 4.5h7M12.5 4.5h1M2.5 11.5h1M6.5 11.5h7" />
    <circle cx="11" cy="4.5" r="1.5" />
    <circle cx="5" cy="11.5" r="1.5" />
  </>,
  "history": <>
    <path d="M2.6 8a5.4 5.4 0 1 0 1.6-3.8" />
    <path d="M2.5 2.5v2.4h2.4" />
    <path d="M8 5v3l2 1.3" />
  </>,
  "gauge": <>
    <path d="M2.5 11a5.5 5.5 0 1 1 11 0" />
    <path d="M8 11l2.5-3.5" />
    <path d="M2.5 13.5h11" />
  </>,
  "server": <>
    <rect x="2.5" y="2.5" width="11" height="4.5" rx="1" />
    <rect x="2.5" y="9" width="11" height="4.5" rx="1" />
    <path d="M5 4.75h.1M5 11.25h.1" />
  </>,
  "users": <>
    <circle cx="5.5" cy="5.5" r="2" />
    <circle cx="11" cy="6" r="1.6" />
    <path d="M1.8 13c.3-2.2 1.7-3.4 3.7-3.4s3.4 1.2 3.7 3.4" />
    <path d="M9.6 9.8c.4-.1.9-.2 1.4-.2 1.7 0 2.9 1 3.2 3" />
  </>,
  "projects": <>
    <rect x="2" y="5" width="9" height="9" rx="1.5" />
    <path d="M5 5V3.5A1.5 1.5 0 0 1 6.5 2h6A1.5 1.5 0 0 1 14 3.5v6a1.5 1.5 0 0 1-1.5 1.5H11" />
  </>,
  "measures": <>
    <path d="M2.5 13.5h11" />
    <path d="M4.5 11V8M8 11V4.5M11.5 11V6.5" />
  </>,
  "search": <>
    <circle cx="7" cy="7" r="4.5" />
    <path d="M10.5 10.5 14 14" />
  </>,
  "chevron-down": <path d="M4.5 6.5 8 10l3.5-3.5" />,
  "chevron-right": <path d="M6.5 4.5 10 8l-3.5 3.5" />,
  "chevron-left": <path d="M9.5 4.5 6 8l3.5 3.5" />,
  "chevron-updown": <path d="M5 6l3-3 3 3M5 10l3 3 3-3" />,
  "plus": <path d="M8 3v10M3 8h10" />,
  "x": <path d="M4 4l8 8M12 4l-8 8" />,
  "more": <>
    <circle cx="3.5" cy="8" r="1.1" fill="currentColor" />
    <circle cx="8" cy="8" r="1.1" fill="currentColor" />
    <circle cx="12.5" cy="8" r="1.1" fill="currentColor" />
  </>,
  "check": <path d="M3 8.5 6.5 12 13 4.5" />,
  "warning": <>
    <path d="M8 2.2 14.3 13.3H1.7z" />
    <path d="M8 6.5v3" />
    <path d="M8 11.5v.1" />
  </>,
  "info": <>
    <circle cx="8" cy="8" r="6" />
    <path d="M8 7.2V11" />
    <path d="M8 5v.1" />
  </>,
  "external": <>
    <path d="M9 2.5h4.5V7" />
    <path d="M13.5 2.5 7.5 8.5" />
    <path d="M11.5 9.5v3a1 1 0 0 1-1 1h-7a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1h3" />
  </>,
  "copy": <>
    <rect x="5" y="5" width="8.5" height="8.5" rx="1.5" />
    <path d="M11 5V3.5a1 1 0 0 0-1-1H3.5a1 1 0 0 0-1 1V10a1 1 0 0 0 1 1H5" />
  </>,
  "play": <path d="M5 3.2v9.6L12.5 8z" />,
  "pause": <path d="M5.5 3.5v9M10.5 3.5v9" />,
  "stop": <rect x="4" y="4" width="8" height="8" rx="1" />,
  "restart": <>
    <path d="M13 8a5 5 0 1 1-1.5-3.6" />
    <path d="M13 2.5v3h-3" />
  </>,
  "retry": <>
    <path d="M3 8a5 5 0 0 1 8.6-3.5L13 6" />
    <path d="M13 3v3h-3" />
    <path d="M13 8a5 5 0 0 1-8.6 3.5L3 10" />
    <path d="M3 13v-3h3" />
  </>,
  "replan": <>
    <path d="M2.5 4h7M2.5 8h4M2.5 12h7" />
    <path d="M11 9.5l2.5 2.5-2.5 2.5" />
    <path d="M13.5 12H12a3 3 0 0 1-3-3V8" />
  </>,
  "power": <>
    <path d="M8 2v5.5" />
    <path d="M4.4 4.6a5 5 0 1 0 7.2 0" />
  </>,
  "terminal": <>
    <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" />
    <path d="M4.5 6.2 6.5 8l-2 1.8" />
    <path d="M8 10.2h3.5" />
  </>,
  "hub": <>
    <circle cx="8" cy="8" r="2" />
    <circle cx="8" cy="2.5" r="1.2" />
    <circle cx="13.5" cy="8" r="1.2" />
    <circle cx="8" cy="13.5" r="1.2" />
    <circle cx="2.5" cy="8" r="1.2" />
    <path d="M8 3.7V6M12.3 8H10M8 12.3V10M3.7 8H6" />
  </>,
  "flag": <>
    <path d="M3.5 14.5V2" />
    <path d="M3.5 2.5h8.5l-1.8 3 1.8 3H3.5" />
  </>,
  "delegate": <>
    <circle cx="6.5" cy="5" r="2.5" />
    <path d="M2 13.5c.3-2.6 2-4 4.5-4s4.2 1.4 4.5 4" />
    <path d="M11 2.5h3v3" />
    <path d="M14 2.5l-3.2 3.2" />
  </>,
  "branch": <>
    <circle cx="4.5" cy="3.5" r="1.5" />
    <circle cx="4.5" cy="12.5" r="1.5" />
    <circle cx="11.5" cy="5" r="1.5" />
    <path d="M4.5 5v6" />
    <path d="M11.5 6.5c0 3-7 2.5-7 4.5" />
  </>,
  "commit": <>
    <circle cx="8" cy="8" r="2.5" />
    <path d="M1.5 8h4M10.5 8h4" />
  </>,
  "theme": <>
    <circle cx="8" cy="8" r="5.5" />
    <path d="M8 2.5v11a5.5 5.5 0 0 0 0-11z" fill="currentColor" />
  </>,
  "menu": <path d="M2.5 4h11M2.5 8h11M2.5 12h11" />,
  "panel": <>
    <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" />
    <path d="M10 2.5v11" />
  </>,
  "filter": <path d="M2 3.5h12L9.5 9v4l-3 1.5V9z" />,
  "clock": <>
    <circle cx="8" cy="8" r="6" />
    <path d="M8 4.8V8l2.2 1.5" />
  </>,
  "cpu": <>
    <rect x="4" y="4" width="8" height="8" rx="1" />
    <path d="M6 1.5V4M10 1.5V4M6 12v2.5M10 12v2.5M1.5 6H4M1.5 10H4M12 6h2.5M12 10h2.5" />
  </>,
  "memory": <>
    <rect x="1.5" y="4.5" width="13" height="7" rx="1" />
    <path d="M4.5 7v2M7 7v2M9.5 7v2M12 7v2M4 11.5v2M12 11.5v2" />
  </>,
  "disk": <>
    <ellipse cx="8" cy="4" rx="5.5" ry="2" />
    <path d="M2.5 4v8c0 1.1 2.5 2 5.5 2s5.5-.9 5.5-2V4" />
    <path d="M2.5 8c0 1.1 2.5 2 5.5 2s5.5-.9 5.5-2" />
  </>,
  "key": <>
    <circle cx="5" cy="11" r="2.5" />
    <path d="M6.8 9.2 13 3M11 5l1.5 1.5M9.5 6.5 11 8" />
  </>,
  "lock": <>
    <rect x="3" y="7" width="10" height="7" rx="1.5" />
    <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
  </>,
  "archive": <>
    <rect x="2" y="3" width="12" height="3" rx="1" />
    <path d="M3 6v6.5a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V6" />
    <path d="M6.5 9h3" />
  </>,
  "download": <>
    <path d="M8 2.5v8M4.5 7 8 10.5 11.5 7" />
    <path d="M2.5 13.5h11" />
  </>,
  "upload": <>
    <path d="M8 10.5v-8M4.5 6 8 2.5 11.5 6" />
    <path d="M2.5 13.5h11" />
  </>,
  "trash": <>
    <path d="M2.5 4.5h11M6 4.5V3h4v1.5" />
    <path d="M4 4.5l.6 9h6.8l.6-9" />
  </>,
  "link": <>
    <path d="M6.8 9.2a2.8 2.8 0 0 0 4 0l2-2a2.8 2.8 0 0 0-4-4l-.6.6" />
    <path d="M9.2 6.8a2.8 2.8 0 0 0-4 0l-2 2a2.8 2.8 0 0 0 4 4l.6-.6" />
  </>,
  "send": <path d="M8 13V3M3.5 7.5 8 3l4.5 4.5" />,
  "folder": <path d="M2 4a1 1 0 0 1 1-1h3l1.5 1.5H13a1 1 0 0 1 1 1V12a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1z" />,
  "import": <>
    <path d="M8 1.5v8M5 6.5l3 3 3-3" />
    <path d="M2.5 9.5v3a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1v-3" />
  </>,
  "attach": <>
    <path d="M2.5 8h7.5M7 5l3 3-3 3" />
    <path d="M10.5 2.5h2a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1h-2" />
  </>,
  "card": <>
    <rect x="2" y="3" width="12" height="10" rx="1.5" />
    <path d="M5 8l2 2 4-4" />
  </>,
  "bolt": <path d="M9 1.8 3.5 9h4L7 14.2 12.5 7h-4z" />,
  "expand": <path d="M9.5 2.5h4v4M13.5 2.5 9 7M6.5 13.5h-4v-4M2.5 13.5 7 9" />,
  "keyboard": <>
    <rect x="1.5" y="4" width="13" height="8" rx="1.5" />
    <path d="M4 6.5h.1M6.5 6.5h.1M9 6.5h.1M11.5 6.5h.1M5 9.5h6" />
  </>,
  "pencil": <>
    <path d="M10.5 2.5l3 3L6 13H3v-3z" />
    <path d="M9 4l3 3" />
  </>,
};
