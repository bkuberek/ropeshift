import { createContext, useContext, useEffect, useSyncExternalStore } from "react";
import type { AnchorHTMLAttributes, MouseEvent, ReactNode, RefObject } from "react";

// The application's own addresses. A path with no file is a view, which the listener serves as the
// application, so a link is an ordinary address and a person can send one. What a view selects stays
// in the query, because a value with a dot in a path would read as a file.
export type Route = { path: string; params: URLSearchParams; moves: number };

type Place = { address: string; moves: number };

const listeners = new Set<() => void>();

let place: Place = { address: "", moves: 0 };

function here(): Place {
  const address = `${window.location.pathname}${window.location.search}`;
  if (address !== place.address) {
    place = { ...place, address };
  }
  return place;
}

// A move a person made counts, so a view takes focus only when someone came to it.
function arrive(counts: boolean): void {
  place = { ...here(), moves: place.moves + (counts ? 1 : 0) };
  for (const listener of listeners) {
    listener();
  }
}

function popped(): void {
  arrive(true);
}

function subscribe(listener: () => void): () => void {
  if (listeners.size === 0) {
    window.addEventListener("popstate", popped);
  }
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      window.removeEventListener("popstate", popped);
    }
  };
}

export function go(to: string, { stay = false } = {}): void {
  window.history.pushState(null, "", to);
  if (!stay) {
    window.scrollTo(0, 0);
  }
  arrive(!stay);
}

export function useRoute(): Route {
  const { address, moves } = useSyncExternalStore(subscribe, here);
  const url = new URL(address, window.location.origin);
  return { path: url.pathname, params: url.searchParams, moves };
}

const Arrivals = createContext(0);

export function ArrivalsProvider({ value, children }: { value: number; children: ReactNode }) {
  return <Arrivals.Provider value={value}>{children}</Arrivals.Provider>;
}

// useArrivalFocus moves focus to a view's title when a person moves to it, which is how a screen
// reader learns the page changed. A page that just opened keeps its own start, so the skip link stays
// the first stop.
export function useArrivalFocus(title: RefObject<HTMLElement | null>, active = true): void {
  const moves = useContext(Arrivals);
  useEffect(() => {
    if (active && moves > 0) {
      title.current?.focus();
    }
  }, [moves, active, title]);
}

type LinkProps = AnchorHTMLAttributes<HTMLAnchorElement> & {
  to: string;
  // stay keeps the keyboard where it is, for a link that changes what a view shows and not which view.
  stay?: boolean;
};

// Link is an ordinary link the application follows itself. Anything but a plain press is the browser's,
// so a new tab still opens one. Every address here is the application's own, and no address is ever
// taken from a record's text.
export function Link({ to, stay = false, onClick, children, ...rest }: LinkProps) {
  const follow = (event: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(event);
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }
    event.preventDefault();
    go(to, { stay });
  };
  return (
    <a href={to} onClick={follow} {...rest}>
      {children}
    </a>
  );
}

// addressOf builds one of the application's addresses, so a value can never become part of its path.
export function addressOf(path: string, params: Record<string, string | undefined> = {}): string {
  const query = new URLSearchParams();
  for (const [name, value] of Object.entries(params)) {
    if (value) {
      query.set(name, value);
    }
  }
  const text = query.toString();
  return text ? `${path}?${text}` : path;
}
