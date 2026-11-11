import { useEffect, useRef } from "react";

import { signOut } from "../api/client";
import type { Project } from "../api/types";
import { useNeedsYou } from "../api/useNeedsYou";
import { useProjects } from "../api/useProjects";
import { NeedsYou } from "../needs/NeedsYou";
import { Nav } from "./Nav";
import { PageFor, useDocumentTitle } from "./pages";
import { ArrivalsProvider, go, inProject, useRoute } from "./route";
import { SignIn } from "./SignIn";
import { useTheme } from "./theme";

const connectionWords = { connecting: "Connecting", live: "Live", reconnecting: "Reconnecting" };
const themeWords = { system: "Theme: system", light: "Theme: light", dark: "Theme: dark" };

// The project a browser showed last, so an address that names none opens on it again. Storage can be
// missing or refused, and then the first active project opens.
const lastProject = "ropeshift-project";

function remembered(): string {
  try {
    return localStorage.getItem(lastProject) ?? "";
  } catch {
    return "";
  }
}

function remember(name: string): void {
  try {
    localStorage.setItem(lastProject, name);
  } catch {
    // A browser that keeps nothing opens on the first active project.
  }
}

// chosen is the project an address with none opens on: the one shown last, then the first active one.
function chosen(projects: Project[]): string {
  const last = projects.find((project) => project.name === remembered());
  return (last ?? projects.find((project) => project.state === "active") ?? projects[0])?.name ?? "";
}

// App shows one project at a time, and its address names it. An address that names none moves to the
// project the browser showed last, at the same view.
export function App() {
  const route = useRoute();
  const projects = useProjects();
  const { theme, nextTheme } = useTheme();
  const choice = route.project ? "" : chosen(projects.list ?? []);
  const query = route.params.toString();
  useEffect(() => {
    if (choice) {
      go(inProject(choice, `${route.path}${query ? `?${query}` : ""}`), { replace: true, stay: true });
    }
  }, [choice, route.path, query]);
  if (route.project) {
    return (
      <Workspace
        key={route.project}
        project={route.project}
        projects={projects.list ?? []}
        reloadProjects={projects.reload}
        theme={themeWords[theme]}
        nextTheme={nextTheme}
      />
    );
  }
  return (
    <>
      <header className="masthead">
        <p className="wordmark">ropeshift</p>
        <div className="masthead-tools">
          <button className="link-button" type="button" onClick={nextTheme}>
            {themeWords[theme]}
          </button>
        </div>
      </header>
      <main id="main" className="page" tabIndex={-1}>
        {projects.failure ? (
          <p className="refusal" role="alert">
            {projects.failure}
          </p>
        ) : null}
        {projects.needsSession ? <SignIn onSignedIn={projects.reload} /> : null}
        {projects.list && projects.list.length === 0 ? <p className="quiet">This installation holds no project. Run ropeshift project create on a terminal.</p> : null}
        {!projects.list && !projects.needsSession && !projects.failure ? <p className="quiet">Reading.</p> : null}
      </main>
    </>
  );
}

// ProjectSwitch moves to the same view of another project. A view's selection names records of one
// project, so it stays behind.
function ProjectSwitch({ project, projects, path }: { project: string; projects: Project[]; path: string }) {
  const known = projects.some((row) => row.name === project);
  return (
    <label className="project-switch">
      <span className="visually-hidden">Project</span>
      <select value={project} onChange={(event) => go(inProject(event.target.value, path))}>
        {known ? null : <option value={project}>{project}</option>}
        {projects.map((row) => (
          <option key={row.name} value={row.name}>
            {row.display_name || row.name}
            {row.state === "archived" ? " (archived)" : ""}
          </option>
        ))}
      </select>
    </label>
  );
}

type WorkspaceProps = {
  project: string;
  projects: Project[];
  reloadProjects: () => Promise<void>;
  theme: string;
  nextTheme: () => void;
};

// Workspace is one project's views, which an address names, behind the project's switch.
function Workspace({ project, projects, reloadProjects, theme, nextTheme }: WorkspaceProps) {
  const { view, readAt, failure, needsSession, connection, heard, reload, checkSession, forget } = useNeedsYou();
  const route = useRoute();
  const opened = useRef(route.moves);
  const signedIn = view !== null && !needsSession;
  const atNeeds = route.path === "/";
  useDocumentTitle(route.path);
  useEffect(() => remember(project), [project]);
  const signedInAgain = async () => {
    await reload();
    await reloadProjects();
  };
  return (
    <ArrivalsProvider value={route.moves - opened.current}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="masthead">
        <p className="wordmark">ropeshift</p>
        <div className="masthead-tools">
          {signedIn ? <ProjectSwitch project={project} projects={projects} path={route.path} /> : null}
          {signedIn ? (
            <p className={`status status-${connection}`} role="status">
              {connectionWords[connection]}
            </p>
          ) : null}
          <button className="link-button" type="button" onClick={nextTheme}>
            {theme}
          </button>
          {signedIn ? (
            <button className="link-button" type="button" onClick={() => void signOut().then(forget, reload)}>
              Sign out
            </button>
          ) : null}
        </div>
      </header>
      <div className="frame">
        {signedIn ? <Nav path={route.path} waiting={view.items.length} /> : null}
        <main id="main" className="page" tabIndex={-1}>
          {failure ? (
            <p className="refusal" role="alert">
              {failure}
            </p>
          ) : null}
          {needsSession ? <SignIn onSignedIn={signedInAgain} /> : null}
          {/* The page stays mounted behind a sign-in, and the queue behind any other view, so answers in progress survive both. */}
          <div hidden={needsSession}>
            {view ? (
              <>
                <div hidden={!atNeeds}>
                  <NeedsYou view={view} readAt={readAt} after={reload} active={atNeeds} />
                </div>
                {atNeeds ? null : <PageFor key={route.path} route={route} live={{ heard, checkSession }} after={reload} />}
              </>
            ) : null}
          </div>
        </main>
      </div>
    </ArrivalsProvider>
  );
}
