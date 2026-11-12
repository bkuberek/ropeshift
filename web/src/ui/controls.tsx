import { useRef } from "react";
import type { AnchorHTMLAttributes, ButtonHTMLAttributes, KeyboardEvent, ReactNode } from "react";

import { Link } from "../app/route";
import type { IconName } from "./Icon";
import { Icon } from "./Icon";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "danger-outline";

const variantClass: Record<ButtonVariant, string> = {
  primary: "btn btn-primary",
  secondary: "btn",
  ghost: "btn btn-ghost",
  danger: "btn btn-danger",
  "danger-outline": "btn btn-danger-outline",
};

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  small?: boolean;
  // loading hides the label behind a spinner and keeps the button's width, while an action it started runs.
  loading?: boolean;
  icon?: IconName;
  // shortcut is the key that does the same, which the tooltip names.
  shortcut?: string;
};

export function buttonClass(variant: ButtonVariant = "secondary", small = false, extra = ""): string {
  return [variantClass[variant], small ? "btn-sm" : "", extra].filter(Boolean).join(" ");
}

// Button is every pressable action. A disabled one keeps its label, and a loading one says it is busy.
export function Button({ variant = "secondary", small = false, loading = false, icon, shortcut, className = "", type = "button", title, children, ...rest }: ButtonProps) {
  return (
    <button
      type={type}
      className={buttonClass(variant, small, `${loading ? "is-loading" : ""} ${className}`.trim())}
      aria-busy={loading || undefined}
      aria-keyshortcuts={shortcut}
      title={title ?? (shortcut ? `Shortcut: ${shortcut}` : undefined)}
      {...rest}
    >
      {icon ? <Icon name={icon} size={small ? "sm" : "md"} /> : null}
      {children}
    </button>
  );
}

type LinkButtonProps = AnchorHTMLAttributes<HTMLAnchorElement> & { to: string; variant?: ButtonVariant; small?: boolean; icon?: IconName };

// LinkButton goes somewhere and looks like a button: an action that opens a view.
export function LinkButton({ to, variant = "secondary", small = false, icon, className = "", children, ...rest }: LinkButtonProps) {
  return (
    <Link to={to} className={buttonClass(variant, small, className)} {...rest}>
      {icon ? <Icon name={icon} size={small ? "sm" : "md"} /> : null}
      {children}
    </Link>
  );
}

type IconButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  icon: IconName;
  // label is the button's name, which a screen reader reads and the tooltip shows.
  label: string;
  shortcut?: string;
  small?: boolean;
};

// IconButton has a name of its own, and a tooltip with its shortcut, because an icon is never a name.
export function IconButton({ icon, label, shortcut, small = false, className = "", type = "button", ...rest }: IconButtonProps) {
  return (
    <button
      type={type}
      className={`icon-btn${small ? " icon-btn-sm" : ""}${className ? ` ${className}` : ""}`}
      aria-label={label}
      aria-keyshortcuts={shortcut}
      title={shortcut ? `${label} (${shortcut})` : label}
      {...rest}
    >
      <Icon name={icon} size={small ? "sm" : "md"} />
    </button>
  );
}

// Kbd draws a key as a cap.
export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="kbd">{children}</kbd>;
}

// FilterChip narrows a list. Pressed means the filter applies.
export function FilterChip({ pressed, count, onClick, children }: { pressed: boolean; count?: number; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" className="filter" aria-pressed={pressed} onClick={onClick}>
      {children}
      {count === undefined ? null : <span className="n">{count}</span>}
    </button>
  );
}

export type Choice<T extends string> = { value: T; label: string };

// Segmented picks one of a few values. Arrow keys move between them, as in a radio group.
export function Segmented<T extends string>({ label, value, choices, onChange }: { label: string; value: T; choices: Choice<T>[]; onChange: (value: T) => void }) {
  const group = useRef<HTMLDivElement>(null);
  const move = (event: KeyboardEvent<HTMLButtonElement>, at: number) => {
    const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    if (!step) {
      return;
    }
    event.preventDefault();
    const next = choices[(at + step + choices.length) % choices.length];
    if (next) {
      onChange(next.value);
      group.current?.querySelectorAll("button")[(at + step + choices.length) % choices.length]?.focus();
    }
  };
  return (
    <div className="segmented" role="group" aria-label={label} ref={group}>
      {choices.map((choice, at) => (
        <button key={choice.value} type="button" aria-pressed={choice.value === value} onClick={() => onChange(choice.value)} onKeyDown={(event) => move(event, at)}>
          {choice.label}
        </button>
      ))}
    </div>
  );
}

export type TabSpec<T extends string> = { id: T; label: string; count?: number };

// Tabs switch what one region shows. Arrow keys, Home, and End move between them, and the selected one
// is the only tab stop, so Tab moves on into the panel.
export function Tabs<T extends string>({ label, tabs, selected, onSelect, idBase }: { label: string; tabs: TabSpec<T>[]; selected: T; onSelect: (id: T) => void; idBase: string }) {
  const list = useRef<HTMLDivElement>(null);
  const at = Math.max(0, tabs.findIndex((tab) => tab.id === selected));
  const move = (event: KeyboardEvent<HTMLButtonElement>) => {
    const last = tabs.length - 1;
    const to = event.key === "ArrowRight" ? (at === last ? 0 : at + 1) : event.key === "ArrowLeft" ? (at === 0 ? last : at - 1) : event.key === "Home" ? 0 : event.key === "End" ? last : -1;
    const next = tabs[to];
    if (to < 0 || !next) {
      return;
    }
    event.preventDefault();
    onSelect(next.id);
    list.current?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[to]?.focus();
  };
  return (
    <div className="tabs" role="tablist" aria-label={label} ref={list}>
      {tabs.map((tab) => (
        <button
          key={tab.id}
          type="button"
          role="tab"
          className="tab"
          id={`${idBase}-tab-${tab.id}`}
          aria-controls={`${idBase}-panel`}
          aria-selected={tab.id === selected}
          tabIndex={tab.id === selected ? 0 : -1}
          onClick={() => onSelect(tab.id)}
          onKeyDown={move}
        >
          {tab.label}
          {tab.count === undefined ? null : <span className="count">{tab.count}</span>}
        </button>
      ))}
    </div>
  );
}

export function TabPanel({ idBase, selected, children }: { idBase: string; selected: string; children: ReactNode }) {
  return (
    <div role="tabpanel" id={`${idBase}-panel`} aria-labelledby={`${idBase}-tab-${selected}`} className="tab-panel">
      {children}
    </div>
  );
}

export type LinkTabSpec = { to: string; label: string; count?: number; current: boolean };

// LinkTabs are tabs whose choice lives in the address, so each is a link a person can share.
export function LinkTabs({ label, tabs }: { label: string; tabs: LinkTabSpec[] }) {
  return (
    <nav className="tabs" aria-label={label}>
      {tabs.map((tab) => (
        <Link key={tab.to} to={tab.to} stay className="tab" aria-current={tab.current ? "page" : undefined}>
          {tab.label}
          {tab.count === undefined ? null : <span className="count">{tab.count}</span>}
        </Link>
      ))}
    </nav>
  );
}
