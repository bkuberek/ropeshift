import { splitProject } from "./route";

// The application's views. Needs you, Workers, and Usage belong to the installation; the rest to one
// project, whose name leads the address: /projects/{project}/{view}.
export const installationViews = ["needs-you", "workers", "usage"] as const;
export const projectViews = ["roadmap", "plans", "documents", "decisions", "questions", "work", "work-order", "floor", "lane", "measures"] as const;

export type InstallationView = (typeof installationViews)[number];
export type ProjectView = (typeof projectViews)[number];
export type View = InstallationView | ProjectView;

export const views: View[] = [...installationViews, ...projectViews];

export type Resolved =
  | { kind: "view"; view: View; project: string }
  | { kind: "redirect"; to: string }
  // pick-project is a project's view at an address that names no project; the page chooses one.
  | { kind: "pick-project"; path: string }
  | { kind: "missing" };

// A page's name for the browser. It names the view and never a record, so a tab title never carries an
// agent's words.
const titles: Record<View, string> = {
  "needs-you": "Needs you",
  workers: "Workers and resources",
  usage: "Usage",
  roadmap: "Roadmap",
  plans: "Plans",
  documents: "Documents",
  decisions: "Decisions",
  questions: "Open questions",
  work: "Work orders",
  "work-order": "Work order",
  floor: "Floor",
  lane: "Lane",
  measures: "Measures",
};

export function titleOf(view: View): string {
  return titles[view];
}

// The keys that follow G to go to a view.
export const goKeys: Record<string, View> = {
  n: "needs-you",
  r: "roadmap",
  d: "documents",
  w: "work",
  f: "floor",
  u: "usage",
};

// The earlier UI's addresses, and where each view moved. A query the old address held comes along.
const moved: Record<string, { path: string; set?: Record<string, string> }> = {
  "/in-progress": { path: "/work", set: { group: "in-progress" } },
  "/on-hold": { path: "/work", set: { group: "on-hold" } },
  "/stuck": { path: "/work", set: { group: "stuck" } },
  "/completed": { path: "/work", set: { group: "completed" } },
  "/shop-floor": { path: "/floor" },
  "/operations": { path: "/measures" },
  "/requirements": { path: "/documents", set: { kind: "requirements" } },
  "/specs": { path: "/documents", set: { kind: "spec" } },
  "/designs": { path: "/documents", set: { kind: "design" } },
  "/document": { path: "/documents" },
};

function isProjectView(name: string): name is ProjectView {
  return (projectViews as readonly string[]).includes(name);
}

function isInstallationView(name: string): name is InstallationView {
  return (installationViews as readonly string[]).includes(name);
}

function withQuery(path: string, search: string, set: Record<string, string> = {}): string {
  const query = new URLSearchParams();
  for (const [name, value] of Object.entries(set)) {
    query.set(name, value);
  }
  for (const [name, value] of new URLSearchParams(search)) {
    if (!query.has(name)) {
      query.set(name, value);
    }
  }
  const text = query.toString();
  return text ? `${path}?${text}` : path;
}

// resolve reads an address: a view, a move to the address that took an old one's place, a project
// view that waits for the page to choose its project, or nothing.
export function resolve(pathname: string, search: string): Resolved {
  const { project, path } = splitProject(pathname);
  const name = path.replace(/^\/+|\/+$/g, "");
  if (!project) {
    if (pathname === "/") {
      return { kind: "redirect", to: "/needs-you" };
    }
    if (isInstallationView(name)) {
      return { kind: "view", view: name, project: "" };
    }
    if (isProjectView(name) || moved[path]) {
      return { kind: "pick-project", path };
    }
    return { kind: "missing" };
  }
  const base = `/projects/${encodeURIComponent(project)}`;
  // A project's own address is its Overview, which shows Needs you for that project.
  if (name === "") {
    return { kind: "redirect", to: `/needs-you?project=${encodeURIComponent(project)}` };
  }
  const old = moved[path];
  if (old) {
    return { kind: "redirect", to: withQuery(`${base}${old.path}`, search, old.set) };
  }
  if (isProjectView(name)) {
    return { kind: "view", view: name, project };
  }
  return { kind: "missing" };
}
