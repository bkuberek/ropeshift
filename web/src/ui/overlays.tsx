import { createContext, useCallback, useContext, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent, ReactNode, RefObject } from "react";
import { createPortal } from "react-dom";

import type { IconName } from "./Icon";
import { Icon } from "./Icon";
import { IconButton } from "./controls";

const focusable = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

// trapTab keeps Tab and Shift+Tab inside a container, wrapping at either end.
export function trapTab(event: ReactKeyboardEvent<HTMLElement>, container: HTMLElement | null): void {
  if (event.key !== "Tab" || !container) {
    return;
  }
  const stops = [...container.querySelectorAll<HTMLElement>(focusable)].filter((element) => !element.closest("[hidden]"));
  const first = stops[0];
  const last = stops[stops.length - 1];
  if (!first || !last) {
    event.preventDefault();
    return;
  }
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

// useReturnFocus puts focus back on whatever held it before an overlay opened, when the overlay closes.
export function useReturnFocus(open: boolean): void {
  const before = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (!open) {
      return;
    }
    before.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    return () => {
      const back = before.current;
      if (back && document.contains(back)) {
        back.focus();
      }
    };
  }, [open]);
}

type DialogProps = {
  open: boolean;
  title: string;
  onClose: () => void;
  wide?: boolean;
  // initialFocus is the field or the safe button that takes focus, never the destructive one.
  initialFocus?: RefObject<HTMLElement | null>;
  footer?: ReactNode;
  children: ReactNode;
};

// Dialog holds a focused task over a scrim. Focus stays inside until it closes, Escape closes it, and
// focus returns to the control that opened it. It never opens on its own.
export function Dialog({ open, title, onClose, wide = false, initialFocus, footer, children }: DialogProps) {
  const box = useRef<HTMLDivElement>(null);
  const heading = useId();
  useReturnFocus(open);
  useEffect(() => {
    if (!open) {
      return;
    }
    const target = initialFocus?.current ?? box.current?.querySelector<HTMLElement>("input, select, textarea") ?? box.current?.querySelector<HTMLElement>(".dialog-foot button");
    (target ?? box.current)?.focus();
  }, [open, initialFocus]);
  if (!open) {
    return null;
  }
  const keys = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      onClose();
      return;
    }
    trapTab(event, box.current);
  };
  return createPortal(
    <div className="scrim" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <div className={`dialog${wide ? " dialog-wide" : ""}`} role="dialog" aria-modal="true" aria-labelledby={heading} ref={box} tabIndex={-1} onKeyDown={keys}>
        <div className="dialog-head">
          <h2 id={heading}>{title}</h2>
          <IconButton icon="x" label="Close" small onClick={onClose} />
        </div>
        <div className="dialog-body">{children}</div>
        {footer ? <div className="dialog-foot">{footer}</div> : null}
      </div>
    </div>,
    document.body,
  );
}

export type MenuItem = {
  label: string;
  icon?: IconName;
  onSelect: () => void;
  disabled?: boolean;
  // disabledWhy says why an item cannot run, in its tooltip.
  disabledWhy?: string;
  checked?: boolean;
  meta?: string;
};

// placeBelow sets a popover's place under the control that opened it, aligned to its right edge, and
// above it when it would run past the bottom of the window.
function placeBelow(popover: HTMLElement, anchor: HTMLElement, align: "start" | "end"): void {
  const rect = anchor.getBoundingClientRect();
  const height = popover.offsetHeight;
  const below = rect.bottom + 4 + height <= window.innerHeight;
  popover.style.top = `${Math.max(8, below ? rect.bottom + 4 : rect.top - 4 - height)}px`;
  if (align === "end") {
    popover.style.right = `${Math.max(8, window.innerWidth - rect.right)}px`;
    popover.style.left = "auto";
  } else {
    popover.style.left = `${Math.max(8, rect.left)}px`;
    popover.style.right = "auto";
  }
}

// usePopover opens and closes one popover anchored to a control. Escape and a press outside close it,
// and closing returns focus to the control.
export function usePopover(align: "start" | "end" = "end") {
  const [open, setOpen] = useState(false);
  const anchor = useRef<HTMLButtonElement>(null);
  const popover = useRef<HTMLDivElement>(null);
  const close = useCallback((refocus = true) => {
    setOpen(false);
    if (refocus) {
      anchor.current?.focus();
    }
  }, []);
  useLayoutEffect(() => {
    if (open && popover.current && anchor.current) {
      placeBelow(popover.current, anchor.current, align);
    }
  }, [open, align]);
  useEffect(() => {
    if (!open) {
      return;
    }
    const outside = (event: MouseEvent) => {
      const target = event.target as Node;
      if (!popover.current?.contains(target) && !anchor.current?.contains(target)) {
        close(false);
      }
    };
    document.addEventListener("mousedown", outside);
    return () => document.removeEventListener("mousedown", outside);
  }, [open, close]);
  return { open, setOpen, close, anchor, popover };
}

// moveInMenu moves focus among a menu's items with the arrow keys, Home, and End.
export function moveInMenu(event: ReactKeyboardEvent<HTMLElement>, container: HTMLElement | null, close: () => void): void {
  const items = [...(container?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"]), [role="menuitemradio"]') ?? [])];
  const at = items.indexOf(document.activeElement as HTMLElement);
  const to =
    event.key === "ArrowDown" ? (at + 1) % items.length : event.key === "ArrowUp" ? (at - 1 + items.length) % items.length : event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : -2;
  if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    close();
    return;
  }
  if (event.key === "Tab") {
    close();
    return;
  }
  if (to >= 0) {
    event.preventDefault();
    items[to]?.focus();
  }
}

// Menu is a button that opens a list of actions. The checked item, or else the first, takes focus.
export function Menu({ label, icon = "more", items, showLabel = false, small = true }: { label: string; icon?: IconName; items: MenuItem[]; showLabel?: boolean; small?: boolean }) {
  const { open, setOpen, close, anchor, popover } = usePopover();
  const menu = useId();
  useEffect(() => {
    if (open) {
      const items = popover.current?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"])');
      const checked = popover.current?.querySelector<HTMLElement>('[aria-checked="true"]');
      (checked ?? items?.[0])?.focus();
    }
  }, [open, popover]);
  return (
    <>
      <button
        ref={anchor}
        type="button"
        className={showLabel ? "btn btn-sm" : `icon-btn${small ? " icon-btn-sm" : ""}`}
        aria-label={showLabel ? undefined : label}
        title={showLabel ? undefined : label}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menu : undefined}
        onClick={() => setOpen(!open)}
      >
        <Icon name={icon} size={small ? "sm" : "md"} />
        {showLabel ? label : null}
      </button>
      {open
        ? createPortal(
            <div className="popover" role="menu" id={menu} aria-label={label} ref={popover} onKeyDown={(event) => moveInMenu(event, popover.current, close)}>
              {items.map((item) => (
                <button
                  key={item.label}
                  type="button"
                  role="menuitem"
                  className="menu-item"
                  tabIndex={-1}
                  aria-disabled={item.disabled || undefined}
                  aria-checked={item.checked === undefined ? undefined : item.checked}
                  title={item.disabled ? item.disabledWhy : undefined}
                  onClick={() => {
                    if (item.disabled) {
                      return;
                    }
                    close();
                    item.onSelect();
                  }}
                >
                  {item.icon ? <Icon name={item.icon} /> : null}
                  {item.label}
                  {item.meta ? <span className="meta">{item.meta}</span> : null}
                </button>
              ))}
            </div>,
            document.body,
          )
        : null}
    </>
  );
}

export type ToastTone = "neutral" | "success" | "warning" | "danger";

type Toast = { id: number; tone: ToastTone; text: string };

const ToastContext = createContext<(tone: ToastTone, text: string) => void>(() => {});

const toastIcons: Record<ToastTone, IconName> = { neutral: "info", success: "check", warning: "warning", danger: "warning" };
const toastMs = 6000;

// ToastProvider holds the toasts: bottom right, above the status bar, in a polite live region. A toast
// leaves after six seconds, never takes focus, and never carries the only path to anything.
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const next = useRef(0);
  const dismiss = useCallback((id: number) => setToasts((was) => was.filter((toast) => toast.id !== id)), []);
  const show = useCallback(
    (tone: ToastTone, text: string) => {
      next.current += 1;
      const id = next.current;
      setToasts((was) => [...was, { id, tone, text }]);
      setTimeout(() => dismiss(id), toastMs);
    },
    [dismiss],
  );
  return (
    <ToastContext.Provider value={show}>
      {children}
      <div className="toasts" aria-live="polite">
        {toasts.map((toast) => (
          <div key={toast.id} className={`toast tone-${toast.tone}`}>
            <Icon name={toastIcons[toast.tone]} />
            <p className="toast-body">{toast.text}</p>
            <IconButton icon="x" label="Dismiss" small onClick={() => dismiss(toast.id)} />
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): (tone: ToastTone, text: string) => void {
  return useContext(ToastContext);
}
