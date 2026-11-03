import { useId, useState } from "react";

import { Link } from "./route";

const groups = [
  { name: "Now", pages: [{ path: "/", label: "Needs you" }] },
  {
    name: "Planning",
    pages: [
      { path: "/roadmap", label: "Roadmap" },
      { path: "/plans", label: "Plans" },
      { path: "/requirements", label: "Requirements" },
      { path: "/specs", label: "Specs" },
      { path: "/designs", label: "Designs" },
      { path: "/documents", label: "Other documents" },
      { path: "/decisions", label: "Decisions" },
      { path: "/questions", label: "Open questions" },
    ],
  },
];

// The list is always there on a wide screen, and folds behind one button on a phone, where a long list
// would push every view off the first screen.
export function Nav({ path, waiting }: { path: string; waiting: number }) {
  const [open, setOpen] = useState(false);
  const lists = useId();
  return (
    <nav className={open ? "rail rail-open" : "rail"} aria-label="Views">
      <button className="rail-toggle" type="button" aria-expanded={open} aria-controls={lists} onClick={() => setOpen(!open)}>
        Views
        {waiting ? <span className="rail-count"> {waiting} waiting</span> : null}
      </button>
      <div className="rail-lists" id={lists}>
        {groups.map((group) => (
          <div key={group.name} className="rail-group">
            <p className="rail-name" id={`${lists}-${group.name}`}>
              {group.name}
            </p>
            <ul aria-labelledby={`${lists}-${group.name}`}>
              {group.pages.map((page) => (
                <li key={page.path}>
                  <Link to={page.path} aria-current={page.path === path ? "page" : undefined} onClick={() => setOpen(false)}>
                    {page.label}
                    {page.path === "/" && waiting ? <span className="rail-count"> {waiting} waiting</span> : null}
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </nav>
  );
}
