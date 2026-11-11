import { createContext, useContext, useEffect, useSyncExternalStore } from "react";
import type { AnchorHTMLAttributes, MouseEvent, ReactNode, RefObject } from "react";

// The application's own addresses. A path with no file is a view, which the listener serves as the
// application, so a link is an ordinary address and a person can send one. What a view selects stays
// in the query, because a value with a dot in a path would read as a file. Every view belongs to one
// project, whose name leads its address: /projects/{project}/{view}. Path is the view's part of it.
export type Route = { project: string; path: string; params: URLSearchParams; moves: number };

const projectAddress = /^\/projects\/([^/]+)(\/.*)?$/;

// splitProject reads the project an address names, and the view's part of the address after it. An
// address that names no project has an empty one.
export function splitProject(pathname: string): { project: string; path: string } {
  const found = projectAddress.exec(pathname);
  if (!found) {
    return { project: "", path: pathname };
  }
  return { project: decodeURIComponent(found[1] ?? ""), path: found[2] ?? "/" };
}

// inProject is the address of a view of one project. A view's address is the same in every project.
export function inProject(project: string, to: string): string {
  return project ? `/projects/${encodeURIComponent(project)}${to}` : to;
}

// placed is where a link to a view goes: into the project the page shows, unless it names a project.
function placed(to: string, project = splitProject(window.location.pathname).project): string {
  return projectAddress.test(to.split("?")[0] ?? "") ? to : inProject(project, to);
}

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

// go moves to a view of the project the page shows, or to an address that names a project. replace takes
// the place of the address the page is at, for a move no person asked for.
export function go(to: string, { stay = false, replace = false } = {}): void {
  if (replace) {
    window.history.replaceState(null, "", placed(to));
  } else {
    window.history.pushState(null, "", placed(to));
  }
  if (!stay) {
    window.scrollTo(0, 0);
  }
  arrive(!stay);
}

export function useRoute(): Route {
  const { address, moves } = useSyncExternalStore(subscribe, here);
  const url = new URL(address, window.location.origin);
  return { ...splitProject(url.pathname), params: url.searchParams, moves };
}

const Arrivals = createContext(0);

// ItemProject is the project an item of the combined Needs you belongs to: its changes and its links
// reach that project, whichever project the page shows.
export const ItemProject = createContext("");

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
export function Link({ to: view, stay = false, onClick, children, ...rest }: LinkProps) {
  const { project } = useRoute();
  const item = useContext(ItemProject);
  const to = placed(view, item || project);
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
