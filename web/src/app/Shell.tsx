import { useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode, RefObject } from "react";

import type { NeedsProject, Project, ProjectLabel, ShopFloor } from "../api/types";
import type { Connection } from "../api/useNeedsYou";
import type { IconName } from "../ui/Icon";
import { Icon } from "../ui/Icon";
import { IconButton, Kbd } from "../ui/controls";
import { Absent } from "../ui/display";
import { moveInMenu, trapTab, usePopover } from "../ui/overlays";
import { createPortal } from "react-dom";
import { dockWidth } from "./prefs";
import { go, inProject, Link } from "./route";
import type { View } from "./routes";
import { goKeys, titleOf } from "./routes";

export type NavItem = { view: View; icon: IconName };

// The sidebar: the current project's views by group, then the installation's. A view this build does
// not have yet is not listed.
export const navGroups: { name: string; items: NavItem[] }[] = [
  { name: "", items: [{ view: "needs-you", icon: "inbox" }] },
  {
    name: "Plan",
    items: [
      { view: "roadmap", icon: "roadmap" },
      { view: "plans", icon: "plan" },
      { view: "planning", icon: "flag" },
      { view: "documents", icon: "doc" },
      { view: "decisions", icon: "decision" },
      { view: "questions", icon: "question" },
    ],
  },
  {
    name: "Work",
    items: [
      { view: "work", icon: "stack" },
      { view: "floor", icon: "lanes" },
      { view: "measures", icon: "measures" },
    ],
  },
  { name: "Talk", items: [{ view: "conversations", icon: "chat" }] },
  {
    name: "Project",
    items: [
      { view: "repositories", icon: "repo" },
      { view: "agents", icon: "roster" },
      { view: "settings", icon: "sliders" },
      { view: "audit", icon: "history" },
    ],
  },
  {
    name: "Installation",
    items: [
      { view: "projects", icon: "projects" },
      { view: "workers", icon: "server" },
      { view: "members", icon: "users" },
      { view: "usage", icon: "gauge" },
    ],
  },
];

const installation = new Set<View>(["needs-you", "workers", "usage", "projects", "import", "members"]);

// addressOfView is where a view of the current project, or of the installation, lives.
export function addressOfView(view: View, project: string): string {
  return installation.has(view) ? `/${view}` : inProject(project, `/${view}`);
}

const keyOf = Object.fromEntries(Object.entries(goKeys).map(([key, view]) => [view, key.toUpperCase()]));

const connectionWords: Record<Connection, string> = { connecting: "Connecting", live: "Live", reconnecting: "Reconnecting" };

export function projectName(project: Project | ProjectLabel | undefined, fallback: string): string {
  return project?.display_name || project?.name || fallback;
}

type SwitcherProps = {
  project: string;
  projects: Project[];
  waiting: NeedsProject[];
  // path is the view's part of the address, which the same view of another project takes.
  path: string;
  anchor: RefObject<HTMLButtonElement | null>;
  open: boolean;
  setOpen: (open: boolean) => void;
  popover: RefObject<HTMLDivElement | null>;
  close: (refocus?: boolean) => void;
};

// ProjectSwitcher shows the current project and opens the list of projects: a filter, the active ones
// with how much waits in each, and the archived ones, marked read-only. Choosing one opens the same view
// there; a view's selection names records of one project, so it stays behind.
export function ProjectSwitcher({ project, projects, waiting, path, anchor, open, setOpen, popover, close }: SwitcherProps) {
  const [filter, setFilter] = useState("");
  const field = useRef<HTMLInputElement>(null);
  const listId = useId();
  const current = projects.find((row) => row.name === project);
  const shown = projectName(current, project || "No project");
  useEffect(() => {
    if (open) {
      setFilter("");
      field.current?.focus();
    }
  }, [open]);
  const matching = projects.filter((row) => `${row.name} ${row.display_name ?? ""}`.toLowerCase().includes(filter.trim().toLowerCase()));
  const choose = (name: string) => {
    close();
    go(inProject(name, path || "/roadmap"));
  };
  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "ArrowDown" && document.activeElement === field.current) {
      event.preventDefault();
      popover.current?.querySelector<HTMLElement>('[role="menuitemradio"]')?.focus();
      return;
    }
    moveInMenu(event, popover.current, close);
  };
  return (
    <>
      <button
        ref={anchor}
        type="button"
        className="project-button"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-keyshortcuts="P"
        title="Switch project (P)"
        onClick={() => setOpen(!open)}
      >
        <span className="project-mark" aria-hidden="true">
          {shown.charAt(0).toUpperCase()}
        </span>
        <span className="visually-hidden">Project: </span>
        <span className="truncate">{shown}</span>
        <Icon name="chevron-updown" size="sm" />
      </button>
      {open
        ? createPortal(
            <div className="popover project-popover" role="dialog" aria-label="Switch project" ref={popover} onKeyDown={keys}>
              <div className="menu-search">
                <label className="visually-hidden" htmlFor={`${listId}-filter`}>
                  Filter projects
                </label>
                <input
                  ref={field}
                  id={`${listId}-filter`}
                  className="input input-sm"
                  type="search"
                  autoComplete="off"
                  placeholder="Filter projects…"
                  value={filter}
                  onChange={(event) => setFilter(event.target.value)}
                />
              </div>
              <div role="menu" aria-label="Projects" id={listId}>
                {matching.length === 0 ? <p className="menu-label subtle small">No project matches.</p> : null}
                {matching.map((row) => {
                  const count = waiting.find((item) => item.name === row.name)?.waiting;
                  return (
                    <button
                      key={row.name}
                      type="button"
                      role="menuitemradio"
                      aria-checked={row.name === project}
                      className="menu-item"
                      tabIndex={-1}
                      onClick={() => choose(row.name)}
                    >
                      <span className="project-mark" aria-hidden="true">
                        {projectName(row, row.name).charAt(0).toUpperCase()}
                      </span>
                      <span className="truncate">{projectName(row, row.name)}</span>
                      <span className="meta">
                        {row.state === "archived" ? "archived, read-only" : count ? `${count} waiting` : ""}
                      </span>
                    </button>
                  );
                })}
                <div className="menu-sep" role="separator" />
                {switcherLinks.map((entry) => (
                  <button
                    key={entry.to}
                    type="button"
                    role="menuitem"
                    className="menu-item"
                    tabIndex={-1}
                    onClick={() => {
                      close(false);
                      go(entry.to);
                    }}
                  >
                    <Icon name={entry.icon} size="sm" />
                    <span className="truncate">{entry.label}</span>
                  </button>
                ))}
              </div>
            </div>,
            document.body,
          )
        : null}
    </>
  );
}

// The switcher's last entries: what makes a project, and the list that manages them.
const switcherLinks: { label: string; to: string; icon: IconName }[] = [
  { label: "Create project", to: "/projects?dialog=create", icon: "plus" },
  { label: "Import an existing project", to: "/import", icon: "import" },
  { label: "Manage projects", to: "/projects", icon: "projects" },
];

// useProjectSwitcher holds the switcher's popover, so the P key can open it from anywhere.
export function useProjectSwitcher() {
  return usePopover("start");
}

type TopBarProps = {
  switcher: ReactNode;
  waiting: number | null;
  dockOpen: boolean;
  onDock: () => void;
  onPalette: () => void;
  theme: string;
  onTheme: () => void;
  onSignOut?: () => void;
  navOpen: boolean;
  onNav: () => void;
  mac: boolean;
};

export function TopBar({ switcher, waiting, dockOpen, onDock, onPalette, theme, onTheme, onSignOut, navOpen, onNav, mac }: TopBarProps) {
  const account = usePopover("end");
  return (
    <header className="topbar">
      {onSignOut ? (
        <button className="icon-btn menu-btn" type="button" aria-label="Views" aria-expanded={navOpen} aria-controls="sidebar" onClick={onNav}>
          <Icon name="menu" />
        </button>
      ) : null}
      <Link to="/needs-you" className="wordmark" aria-label="ropeshift, Needs you">
        <span className="wordmark-mark" aria-hidden="true">
          <Icon name="mark" />
        </span>
        <span className="wordmark-text" aria-hidden="true">
          ropeshift
        </span>
      </Link>
      {onSignOut ? (
        <>
          <span className="topbar-sep" aria-hidden="true" />
          {switcher}
        </>
      ) : null}
      <div className="topbar-spacer" />
      {onSignOut ? (
        <button className="search-button" type="button" onClick={onPalette} aria-keyshortcuts={mac ? "Meta+K" : "Control+K"}>
          <Icon name="search" />
          <span className="hint-text">Search or run a command…</span>
          <span className="keys" aria-hidden="true">
            <Kbd>{mac ? "⌘" : "Ctrl"}</Kbd>
            <Kbd>K</Kbd>
          </span>
        </button>
      ) : null}
      <div className="top-actions">
        {onSignOut ? (
          <>
            <IconButton icon="search" label="Search or run a command" className="search-icon-btn" onClick={onPalette} />
            <Link to="/needs-you" className="needs-pill">
              <Icon name="inbox" />
              <span className="label-text">Needs you</span>
              {waiting ? (
                <span className="count">
                  {waiting}
                  <span className="visually-hidden"> waiting in every project</span>
                </span>
              ) : null}
            </Link>
            <IconButton icon="chat" label="Conversation panel" shortcut="." aria-pressed={dockOpen} aria-controls="dock" onClick={onDock} />
          </>
        ) : null}
        <button className="icon-btn theme-btn" type="button" aria-label={theme} title={theme} onClick={onTheme}>
          <Icon name="theme" />
        </button>
        {onSignOut ? (
          <>
            <button
              ref={account.anchor}
              type="button"
              className="icon-btn"
              aria-label="Account"
              title="Account"
              aria-haspopup="menu"
              aria-expanded={account.open}
              onClick={() => account.setOpen(!account.open)}
            >
              <Icon name="users" />
            </button>
            {account.open
              ? createPortal(
                  <div className="popover" role="menu" aria-label="Account" ref={account.popover} onKeyDown={(event) => moveInMenu(event, account.popover.current, account.close)}>
                    <button
                      type="button"
                      role="menuitem"
                      className="menu-item"
                      ref={(button) => button?.focus()}
                      onClick={() => {
                        account.close(false);
                        onSignOut();
                      }}
                    >
                      Sign out
                    </button>
                  </div>,
                  document.body,
                )
              : null}
          </>
        ) : null}
      </div>
    </header>
  );
}

type SidebarProps = {
  view: View | null;
  project: string;
  waiting: number | null;
  theme: string;
  onTheme: () => void;
  onSheet: () => void;
  open: boolean;
  onClose: () => void;
};

// Sidebar lists the views: the current one has a selected fill, an ink bar, and aria-current. On a phone
// it is a drawer over a scrim, which Escape closes.
export function Sidebar({ view, project, waiting, theme, onTheme, onSheet, open, onClose }: SidebarProps) {
  const nav = useRef<HTMLElement>(null);
  useEffect(() => {
    if (open) {
      nav.current?.querySelector<HTMLElement>("a")?.focus();
    }
  }, [open]);
  return (
    <>
      <nav
        className="sidebar"
        id="sidebar"
        aria-label="Views"
        ref={nav}
        onKeyDown={(event) => {
          if (!open) {
            return;
          }
          if (event.key === "Escape") {
            onClose();
            return;
          }
          trapTab(event, nav.current);
        }}
      >
        <div className="sidebar-scroll">
          {navGroups.map((group) => (
            <div key={group.name || "top"} className="nav-group">
              {group.name ? (
                <p className="overline" id={`nav-${group.name}`}>
                  {group.name}
                </p>
              ) : null}
              <ul aria-labelledby={group.name ? `nav-${group.name}` : undefined}>
                {group.items.map((item) => {
                  const count = item.view === "needs-you" ? waiting : null;
                  return (
                    <li key={item.view}>
                      <Link
                        to={addressOfView(item.view, project)}
                        className={`nav-link${count ? " has-count" : ""}`}
                        aria-current={item.view === view ? "page" : undefined}
                        title={keyOf[item.view] ? `${titleOf(item.view)} (G then ${keyOf[item.view]})` : titleOf(item.view)}
                        onClick={onClose}
                      >
                        <Icon name={item.icon} />
                        <span className="nav-label">{titleOf(item.view)}</span>
                        {count ? (
                          <span className="count">
                            {count}
                            <span className="visually-hidden"> waiting</span>
                          </span>
                        ) : null}
                      </Link>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </div>
        <div className="sidebar-foot">
          <button type="button" className="btn btn-ghost btn-sm sidebar-theme" onClick={onTheme} aria-label={theme}>
            <Icon name="theme" size="sm" />
            <span className="nav-label">{theme}</span>
          </button>
          <span className="spacer" />
          <IconButton icon="keyboard" label="Keyboard shortcuts" shortcut="?" small onClick={onSheet} />
        </div>
      </nav>
      {open ? <div className="drawer-scrim" aria-hidden="true" onClick={onClose} /> : null}
    </>
  );
}

type StatusProps = {
  connection: Connection;
  floor: ShopFloor | null;
  floorAgeMs: number;
  project: string;
};

const runningLanes = new Set(["provisioning", "starting", "briefing", "working", "needs_decision", "compacting", "handing_off", "idle", "backoff"]);

// StatusBar is the status line on every page: the connection, the project's lanes against its
// machines' caps, each machine, and what no source has reported yet, which says so and is never zero.
export function StatusBar({ connection, floor, floorAgeMs, project }: StatusProps) {
  const workers = floor?.workers ?? [];
  const lanes = (floor?.lanes ?? []).filter((lane) => runningLanes.has(lane.actual_state)).length;
  const caps = workers.map((line) => line.worker.manifest?.capacity?.max_lanes).filter((cap): cap is number => typeof cap === "number" && cap > 0);
  const cap = caps.length === workers.length && caps.length ? caps.reduce((sum, value) => sum + value, 0) : null;
  return (
    <footer className="statusbar" aria-label="Status">
      <span className="sb-item live" role="status">
        <span className={connection === "live" ? "live-dot" : "live-dot is-off"} aria-hidden="true" />
        {connectionWords[connection]}
      </span>
      {floor ? (
        <Link to={inProject(project, "/floor")} className="sb-item" title={`Lanes of ${project} running, against the caps of the machines that serve it`}>
          <span className="sb-label">Lanes</span>
          <strong>{lanes}</strong>
          {cap === null ? null : <span>/ {cap}</span>}
        </Link>
      ) : null}
      {workers.map((line) => (
        <Link
          key={line.worker.id}
          to={`/workers?worker=${encodeURIComponent(line.worker.id)}`}
          className="sb-item"
          title={`${line.worker.name || line.worker.id}: ${line.freshness === "observed" ? "observed" : "last reported"}, ${Math.round(floorAgeMs / 1000)}s since this page read it. No source reports its CPU, memory, or disk to this page yet.`}
        >
          <span className={line.freshness === "observed" ? "fresh-dot" : "fresh-dot reported"} aria-hidden="true" />
          <span>{line.worker.name || line.worker.id}</span>
          <Absent>resources unavailable</Absent>
        </Link>
      ))}
      <Link to="/usage" className="sb-item sb-right" title="No source has reported a sign-in's windows or cost yet">
        <span className="sb-label">Est. cost</span>
        <Absent>unavailable</Absent>
      </Link>
      <Link to="/usage" className="sb-compact" title="Open Usage">
        {floor ? (
          <span>
            Lanes {lanes}
            {cap === null ? "" : `/${cap}`}
          </span>
        ) : null}
        <span aria-hidden="true">·</span>
        <span>
          Est. cost <Absent>unavailable</Absent>
        </span>
      </Link>
    </footer>
  );
}

// Dock is the conversation surface's frame, open or closed per browser, on every view. It holds the surface
// only while open, so a closed dock reads nothing, and its width is the person's: dragged, or stepped 20 px
// with the arrow keys, between 360 and 640 px. Below 1024 px it is an overlay, and on a phone it fills the screen.
export function Dock({ open, width, onWidth, children }: { open: boolean; width: number; onWidth: (width: number) => void; children: ReactNode }) {
  return (
    <aside className="dock" id="dock" aria-label="Conversation" hidden={!open}>
      {open ? (
        <>
          <DockResize width={width} onWidth={onWidth} />
          {children}
        </>
      ) : null}
    </aside>
  );
}

// DockResize is the dock's separator: a handle that is dragged, or stepped with the arrow keys, so no resize
// needs a drag (WCAG 2.5.7). The dock stands on the right, so a left key widens it.
function DockResize({ width, onWidth }: { width: number; onWidth: (width: number) => void }) {
  const [dragging, setDragging] = useState(false);
  const start = useRef<{ x: number; width: number } | null>(null);
  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.key === "ArrowLeft" || event.key === "ArrowUp" ? dockWidth.step : event.key === "ArrowRight" || event.key === "ArrowDown" ? -dockWidth.step : 0;
    if (step) {
      event.preventDefault();
      onWidth(width + step);
    } else if (event.key === "Home") {
      event.preventDefault();
      onWidth(dockWidth.min);
    } else if (event.key === "End") {
      event.preventDefault();
      onWidth(dockWidth.max);
    }
  };
  return (
    <div
      className={`dock-resize${dragging ? " is-dragging" : ""}`}
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize the conversation panel"
      aria-valuenow={width}
      aria-valuemin={dockWidth.min}
      aria-valuemax={dockWidth.max}
      aria-valuetext={`${width} pixels wide`}
      tabIndex={0}
      onKeyDown={keys}
      onPointerDown={(event) => {
        start.current = { x: event.clientX, width };
        event.currentTarget.setPointerCapture?.(event.pointerId);
        setDragging(true);
      }}
      onPointerMove={(event) => {
        if (start.current) {
          onWidth(start.current.width + (start.current.x - event.clientX));
        }
      }}
      onPointerUp={() => {
        start.current = null;
        setDragging(false);
      }}
      onPointerCancel={() => {
        start.current = null;
        setDragging(false);
      }}
    />
  );
}
