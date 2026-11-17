import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";

import { readView, setCurrentProject, signOut } from "../api/client";
import type { NeedItem, Project, ShopFloor } from "../api/types";
import { useHealth } from "../api/useHealth";
import { useNeedsYou } from "../api/useNeedsYou";
import { useProjects } from "../api/useProjects";
import { useView } from "../api/useView";
import { settingsList } from "../admin/Settings";
import { headingOf } from "../needs/kinds";
import { NeedsYou } from "../needs/NeedsYou";
import { useClock } from "../parts/parts";
import { TalkProvider, useTalk } from "../talk/state";
import type { Talk } from "../talk/state";
import { Surface } from "../talk/Surface";
import { Banner } from "../ui/display";
import { ToastProvider } from "../ui/overlays";
import { inField, idle, onMac, readKey } from "./keys";
import { NoSuchView, PageFor } from "./pages";
import type { Entry } from "./ranking";
import { useFocusParam } from "./focusParam";
import { PageActionsProvider } from "./pageActions";
import { Palette } from "./Palette";
import { recentRecords, useDockWidth, useFlag } from "./prefs";
import { reviewsTabs } from "../reviews/Reviews";
import { addressOf, ArrivalsProvider, CurrentProject, go, inProject, Link, useRoute } from "./route";
import type { View } from "./routes";
import { goKeys, resolve, titleOf } from "./routes";
import { addressOfView, Dock, navGroups, projectName, ProjectSwitcher, Sidebar, StatusBar, TopBar, useProjectSwitcher } from "./Shell";
import { ShortcutSheet } from "./ShortcutSheet";
import { SignIn } from "./SignIn";
import { themeWords, useTheme } from "./theme";

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

function useDocumentTitle(view: View | null): void {
  useEffect(() => {
    document.title = `${view ? titleOf(view) : "No such view"} - ropeshift`;
  }, [view]);
}

const keyOfView = Object.fromEntries(Object.entries(goKeys).map(([key, view]) => [view, `G ${key.toUpperCase()}`]));

// moveInList moves focus to the next or the previous item of the list or table the view shows. A view
// that selects as it moves, as Needs you does, takes the move itself.
function moveInList(step: 1 | -1): void {
  const handled = !window.dispatchEvent(new CustomEvent("ropeshift:move", { detail: step, cancelable: true }));
  if (handled) {
    return;
  }
  const items = [...document.querySelectorAll<HTMLElement>("#main [data-item]")].filter((item) => !item.closest("[hidden]"));
  const at = items.findIndex((item) => item === document.activeElement || item.contains(document.activeElement));
  items[at < 0 ? (step > 0 ? 0 : items.length - 1) : Math.min(items.length - 1, Math.max(0, at + step))]?.focus();
}

// focusPrimary moves focus to the open decision's primary button, which stands last among its buttons.
// It presses nothing.
function focusPrimary(): void {
  const shown = (selector: string) => [...document.querySelectorAll<HTMLElement>(selector)].filter((button) => !button.closest("[hidden]"));
  const buttons = shown("#main .decision .decide .btn");
  (shown("#main .decision .decide .btn-primary")[0] ?? buttons[buttons.length - 1])?.focus();
}

// TalkBridge hands the conversation surface's controls to the shell's key handler and palette, which stand
// outside the provider that holds them.
function TalkBridge({ into }: { into: { current: Talk | null } }) {
  into.current = useTalk();
  return null;
}

// App is the shell around every view: the top bar, the sidebar, the content, the conversation dock, and
// the status bar. Needs you stays mounted behind every other view, so an answer in progress survives a
// look elsewhere, and every view stays mounted behind a sign-in.
export function App() {
  const route = useRoute();
  const resolved = resolve(route.pathname, route.search);
  const projects = useProjects();
  const list = projects.list ?? [];
  const project = route.project || route.params.get("project") || chosen(list);
  setCurrentProject(project);
  const needs = useNeedsYou(project);
  const { theme, nextTheme, setTheme } = useTheme();
  const opened = useRef(route.moves);
  const mac = useMemo(onMac, []);
  const [singleKeys, setSingleKeys] = useFlag("ropeshift-single-keys", true);
  const [dockOpen, setDockOpen] = useFlag("ropeshift-dock", false);
  const [dockPx, setDockPx] = useDockWidth();
  const talkRef = useRef<Talk | null>(null);
  const openDock = useCallback(() => setDockOpen(true), [setDockOpen]);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [pageEntries, setPageEntries] = useState<Entry[]>([]);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [navOpen, setNavOpen] = useState(false);
  const switcher = useProjectSwitcher();
  const keyState = useRef(idle);
  const view = resolved.kind === "view" ? resolved.view : null;
  // A project's view at an address with no project is where a person is already, while the page chooses
  // the project, so the sidebar marks it and the tab names it from the first render.
  const marked = resolved.kind === "pick-project" ? resolved.view : view;
  const needsSession = projects.needsSession || needs.needsSession;
  const signedIn = needs.view !== null && !needsSession;
  // The Conversations view holds the surface in full, so the dock stands aside there and the keys go to it.
  const onConversations = view === "conversations";
  const dockVisible = dockOpen && signedIn && !onConversations;
  const live = useMemo(() => ({ heard: needs.heard, checkSession: needs.checkSession }), [needs.heard, needs.checkSession]);
  useDocumentTitle(resolved.kind === "view" || resolved.kind === "pick-project" || resolved.kind === "missing" ? marked : "needs-you");
  useFocusParam(route, signedIn);

  useEffect(() => {
    if (route.project) {
      remember(route.project);
    }
  }, [route.project]);

  const query = route.search;
  useEffect(() => {
    if (resolved.kind === "redirect") {
      go(resolved.to, { replace: true, stay: true });
    } else if (resolved.kind === "pick-project" && project) {
      go(inProject(project, `${resolved.path}${query}`), { replace: true, stay: true });
    }
  }, [resolved.kind, resolved.kind === "redirect" ? resolved.to : "", resolved.kind === "pick-project" ? resolved.path : "", project, query]);

  const floor = useView<ShopFloor | null>(() => (project && signedIn ? readView<ShopFloor>("shop-floor") : Promise.resolve(null)), `status:${project}:${signedIn}`, live);
  const health = useHealth(live, signedIn).view;
  // A join token is for a worker on another machine, which only an installation in group mode takes in.
  const group = health?.mode === "group";
  const now = useClock();

  const signOutNow = useCallback(() => void signOut().then(needs.forget, needs.reload), [needs.forget, needs.reload]);
  const signedInAgain = async () => {
    await needs.reload();
    await projects.reload();
  };

  // The dock opens with the cursor in its message box, and on the Conversations view the key goes there.
  const toggleDock = () => {
    if (onConversations || !dockOpen) {
      if (!onConversations) {
        setDockOpen(true);
      }
      talkRef.current?.requestFocus();
      return;
    }
    setDockOpen(false);
  };

  useEffect(() => {
    const listen = (event: KeyboardEvent) => {
      if (event.defaultPrevented || !signedIn) {
        return;
      }
      const read = readKey(
        { key: event.key, metaKey: event.metaKey, ctrlKey: event.ctrlKey, altKey: event.altKey, shiftKey: event.shiftKey, inField: inField(event.target), mac },
        keyState.current,
        { singleKeys, now: Date.now() },
      );
      keyState.current = read.state;
      const action = read.action;
      if (read.handled) {
        event.preventDefault();
      }
      if (!action) {
        return;
      }
      const overlay = document.querySelector('[aria-modal="true"], .popover');
      if (action.type === "palette") {
        setPaletteOpen((was) => !was);
        return;
      }
      if (overlay) {
        return;
      }
      switch (action.type) {
        case "sheet":
          setSheetOpen(true);
          break;
        case "go":
          go(addressOfView(action.view, project));
          break;
        case "projects":
          switcher.setOpen(true);
          break;
        case "dock":
          toggleDock();
          break;
        case "target":
          if (!onConversations) {
            setDockOpen(true);
          }
          talkRef.current?.requestMenu();
          break;
        case "next":
          moveInList(1);
          break;
        case "previous":
          moveInList(-1);
          break;
        case "primary":
          focusPrimary();
          break;
      }
    };
    window.addEventListener("keydown", listen);
    return () => window.removeEventListener("keydown", listen);
  }, [signedIn, singleKeys, mac, project, dockOpen, onConversations, setDockOpen, switcher]);

  const current = list.find((row) => row.name === project);
  const label = projectName(current, project);
  const items = needs.view?.items ?? [];
  const sources = useMemo(() => {
    // Each card that waits has a Decide action, and the first few stand in the list with nothing typed.
    const decide = items.map(
      (item: NeedItem): Entry => ({
        key: `decide:${item.kind}:${item.id}`,
        group: "Actions",
        title: `Decide: ${headingOf(item)}`,
        icon: "inbox",
        meta: projectName(item.project, item.project?.name ?? "") || "Needs you",
        to: `/needs-you?item=${encodeURIComponent(`${item.kind}:${item.id}`)}&focus=primary`,
      }),
    );
    const actions: Entry[] = [
      ...decide.slice(0, 5),
      ...pageEntries,
      { key: "action:projects", group: "Actions", title: "Switch project", icon: "projects", meta: "P", run: () => switcher.setOpen(true) },
      { key: "action:create-project", group: "Actions", title: "Create project", icon: "plus", to: "/projects?dialog=create" },
      { key: "action:import", group: "Actions", title: "Import an existing project", icon: "import", to: "/import" },
      { key: "action:import-backup", group: "Actions", title: "Import a project from a backup", icon: "upload", to: "/projects?focus=Import backup", keywords: "restore upload" },
      { key: "action:start-orchestrator", group: "Actions", title: "Start orchestrator", icon: "server", to: "/workers?focus=Start orchestrator", keywords: "worker run" },
      ...list.flatMap((row): Entry[] => {
        const shown = projectName(row, row.name);
        const entry = (action: string, title: string, icon: Entry["icon"], params: Record<string, string>): Entry => ({
          key: `action:${action}:${row.name}`,
          group: "Actions",
          id: row.name,
          title: `${title}: ${shown}`,
          icon,
          to: addressOf("/projects", params),
        });
        return row.state === "archived"
          ? [entry("restore-project", "Restore project", "restart", { focus: `Restore ${shown}` }), entry("remove-project", "Remove project", "trash", { dialog: "remove", project: row.name })]
          : [
              entry("rename-project", "Rename project", "pencil", { dialog: "rename", project: row.name }),
              entry("archive-project", "Archive project", "archive", { dialog: "archive", project: row.name }),
              entry("backup-project", "Back up project", "download", { focus: `Back up ${shown}` }),
            ];
      }),
      ...settingsList
        .filter((section) => !section.project || project)
        .map(
          (section): Entry => ({
            key: `action:settings:${section.id}`,
            group: "Actions",
            title: `Settings: ${section.title}`,
            icon: "sliders",
            keywords: "change edit setting policy",
            to: inProject(section.project ? project : "", addressOf("/settings", { section: section.id })),
          }),
        ),
      { key: "action:fleet-caps", group: "Actions", title: "Set the fleet's caps", icon: "sliders", to: inProject(project, "/settings?section=fleet&focus=Work in progress limit"), keywords: "wip limit lanes per repository worker" },
      { key: "action:revoke-delegate", group: "Actions", title: "Revoke a delegate", icon: "key", to: "/members?focus=Revoke", keywords: "credential" },
      { key: "action:remove-delegate", group: "Actions", title: "Remove a delegate", icon: "key", to: "/members?focus=Remove", keywords: "credential" },
      { key: "action:installation-add-agent", group: "Actions", title: "Installation agents: add an agent", icon: "roster", run: () => go("/agents?dialog=add", { installation: true }), keywords: "roster" },
      { key: "action:installation-agent-history", group: "Actions", title: "Installation agents: restore a version", icon: "history", run: () => go("/agents?tab=history", { installation: true }), keywords: "roster revert" },
      ...(project
        ? [
            { key: "action:add-agent", group: "Actions" as const, title: "Add agent", icon: "roster" as const, to: inProject(project, "/agents?dialog=add"), keywords: "roster" },
            { key: "action:agent-history", group: "Actions" as const, title: "Agents: restore a version", icon: "history" as const, to: inProject(project, "/agents?tab=history"), keywords: "roster revert" },
          ]
        : []),
      ...(project
        ? [
            { key: "action:register", group: "Actions" as const, title: "Register repositories", icon: "repo" as const, to: inProject(project, "/repositories?dialog=register") },
            { key: "action:new-work", group: "Actions" as const, title: "New work order", icon: "plus" as const, to: inProject(project, "/work?dialog=new"), keywords: "create add work" },
            { key: "action:start-review", group: "Actions" as const, title: "Start contract review", icon: "play" as const, to: inProject(project, "/plans?dialog=review"), keywords: "plan review lane questions" },
            { key: "action:fleet", group: "Actions" as const, title: "Pause or drain the fleet", icon: "pause" as const, to: inProject(project, "/settings?section=fleet") },
            { key: "action:record-question", group: "Actions" as const, title: "Record question", icon: "question" as const, to: inProject(project, "/questions?dialog=record"), keywords: "ask open" },
            { key: "action:record-decision", group: "Actions" as const, title: "Record decision", icon: "decision" as const, to: inProject(project, "/decisions?dialog=record"), keywords: "decide assume assumption" },
            { key: "action:add-category", group: "Actions" as const, title: "Add category", icon: "plus" as const, to: inProject(project, `/${view === "decisions" ? "decisions" : "questions"}?dialog=category`), keywords: "question decision filter" },
          ]
        : []),
      ...(group ? [{ key: "action:join-token", group: "Actions" as const, title: "Create join token", icon: "key" as const, to: "/workers?dialog=token" }] : []),
      { key: "action:backup-installation", group: "Actions", title: "Back up the installation", icon: "download", to: inProject(project, "/settings?section=backup") },
      { key: "action:dock", group: "Actions", title: dockOpen ? "Close the conversation panel" : "Open the conversation panel", icon: "chat", meta: ".", run: toggleDock },
      ...(project
        ? [
            {
              key: "action:new-conversation",
              group: "Actions" as const,
              title: "New assistant conversation",
              icon: "plus" as const,
              keywords: "chat talk assistant",
              run: () => {
                const talk = talkRef.current;
                talk?.select("assistant", "", { open: !onConversations, focus: true });
                talk?.requestNew();
              },
            },
            {
              key: "action:target",
              group: "Actions" as const,
              title: "Switch conversation target",
              icon: "chat" as const,
              meta: "T",
              keywords: "talk assistant orchestrator lane planning",
              run: () => {
                if (!onConversations) {
                  setDockOpen(true);
                }
                talkRef.current?.requestMenu();
              },
            },
            {
              key: "action:message-assistant",
              group: "Actions" as const,
              title: "Message the assistant",
              icon: "send" as const,
              keywords: "chat talk assistant send",
              run: () => talkRef.current?.select("assistant", "", { open: !onConversations, focus: true }),
            },
            { key: "action:conversations", group: "Actions" as const, title: "Go to Conversations", icon: "chat" as const, meta: "G C", to: inProject(project, "/conversations") },
          ]
        : []),
      { key: "action:theme", group: "Actions", title: `Switch theme (${themeWords[theme].replace("Theme: ", "now ")})`, icon: "theme", run: nextTheme },
      { key: "action:sheet", group: "Actions", title: "Show keyboard shortcuts", icon: "keyboard", meta: "?", run: () => setSheetOpen(true) },
      { key: "action:sign-out", group: "Actions", title: "Sign out", icon: "lock", run: signOutNow },
      { key: "action:create-delegate", group: "Actions", title: "Create delegate", icon: "key", to: "/members?dialog=delegate", keywords: "credential agent" },
      { key: "action:sign-out-all", group: "Actions", title: "Sign out all sessions", icon: "lock", to: "/members?dialog=sign-out-all", keywords: "revoke delegates" },
    ];
    const views: Entry[] = navGroups.flatMap((group) =>
      group.items.map((item) => ({
        key: `view:${item.view}`,
        group: "Views" as const,
        title: titleOf(item.view),
        icon: item.icon,
        meta: keyOfView[item.view],
        to: addressOfView(item.view, project),
        keywords: item.view === "floor" ? "shop floor lanes machines" : item.view === "measures" ? "operations" : item.view === "work" ? "in progress on hold stuck completed" : "",
      })),
    );
    // Each tab of Reviews is a view of its own in the palette, beside the view itself.
    const tabs: Entry[] = project
      ? reviewsTabs.map((tab) => ({
          key: `view:reviews:${tab.id}`,
          group: "Views" as const,
          title: `Reviews: ${tab.label}`,
          icon: "review" as const,
          to: inProject(project, addressOf("/reviews", { tab: tab.id === "queue" ? "" : tab.id })),
          keywords: tab.id === "queue" ? "review accept rework merge approve" : tab.id === "pull-requests" ? "review pull request checks" : "review agreement calibration",
        }))
      : [];
    const recent: Entry[] = recentRecords()
      .filter((row) => row.project === project)
      .map((row) => ({ key: `recent:${row.kind}:${row.id}`, group: "Records", id: row.id, title: row.title, icon: "clock", to: row.to, meta: label }));
    const others: Entry[] = list
      .filter((row) => row.state === "active" && row.name !== project)
      .map((row) => ({ key: `project:${row.name}`, group: "Projects", id: row.name, title: `Switch to ${projectName(row, row.name)}`, icon: "projects", to: inProject(row.name, resolved.kind === "view" && route.project ? route.path : "/roadmap") }));
    return { actions, views: [...views, ...tabs], recent, projects: others, more: decide.slice(5) };
  }, [items, pageEntries, dockOpen, onConversations, theme, nextTheme, signOutNow, project, label, list, switcher, setDockOpen, resolved.kind, route.project, route.path, view, group]);

  const waiting = needs.view ? needs.view.items.length : null;
  const shell = `app${navOpen ? " nav-open" : ""}${dockVisible ? " dock-open" : ""}`;
  const chooseConversation = (id: string) => {
    talkRef.current?.select("assistant", id, { start: true });
    go(addressOfView("conversations", project));
  };

  return (
    <ToastProvider>
      <PageActionsProvider value={setPageEntries}>
      <CurrentProject.Provider value={project}>
        <ArrivalsProvider value={route.moves - opened.current}>
          <TalkProvider project={project} openDock={openDock}>
          <TalkBridge into={talkRef} />
          <div className={shell} style={dockVisible ? ({ "--dock-w": `${dockPx}px` } as CSSProperties) : undefined}>
            <a className="skip-link" href="#main">
              Skip to content
            </a>
            <TopBar
              switcher={
                <ProjectSwitcher
                  project={project}
                  projects={list}
                  waiting={needs.view?.projects ?? []}
                  path={route.project ? route.path : "/roadmap"}
                  {...switcher}
                />
              }
              waiting={waiting}
              dockOpen={dockVisible || (dockOpen && onConversations)}
              onDock={toggleDock}
              onPalette={() => setPaletteOpen(true)}
              theme={themeWords[theme]}
              onTheme={nextTheme}
              onSignOut={signedIn ? signOutNow : undefined}
              navOpen={navOpen}
              onNav={() => setNavOpen(!navOpen)}
              mac={mac}
            />
            {signedIn ? (
              <Sidebar
                view={marked}
                project={project}
                waiting={waiting}
                theme={themeWords[theme]}
                onTheme={nextTheme}
                onSheet={() => setSheetOpen(true)}
                open={navOpen}
                onClose={() => setNavOpen(false)}
              />
            ) : null}
            <main id="main" className="main" tabIndex={-1}>
              {projects.failure || needs.failure ? (
                <div className="screen-banner">
                  <Banner tone="danger">{projects.failure || needs.failure}</Banner>
                </div>
              ) : null}
              {needsSession ? <SignIn onSignedIn={signedInAgain} ended={needs.view !== null} /> : null}
              {projects.list && projects.list.length === 0 && !needsSession && view !== "projects" && view !== "import" ? (
                <div className="screen">
                  <h1 tabIndex={-1}>No project yet</h1>
                  <p className="muted">
                    This installation holds no project. <Link to="/projects?dialog=create">Create a project</Link> or <Link to="/import">import an existing one</Link>.
                  </p>
                </div>
              ) : null}
              <div hidden={needsSession} className={onConversations ? "fill" : undefined}>
                {needs.view ? (
                  <>
                    <div hidden={view !== "needs-you"} className="screen-flush">
                      <NeedsYou view={needs.view} readAt={needs.readAt} after={needs.reload} active={view === "needs-you"} />
                    </div>
                    {view && view !== "needs-you" ? <PageFor
                        key={`${view}:${project}:${route.project ? "project" : "installation"}`}
                        view={view}
                        project={project}
                        scoped={route.project !== ""}
                        params={route.params}
                        live={live}
                        after={needs.reload}
                        needs={needs.view}
                        projects={list}
                        reloadProjects={projects.reload}
                        prefs={{ theme, setTheme, singleKeys, setSingleKeys }}
                        onSignedOut={() => void needs.forget()}
                      /> : null}
                    {resolved.kind === "missing" ? <NoSuchView /> : null}
                  </>
                ) : null}
              </div>
            </main>
            {signedIn ? (
              <Dock open={dockVisible} width={dockPx} onWidth={setDockPx}>
                <Surface live={live} variant="dock" onClose={() => setDockOpen(false)} projectLabel={label} after={needs.reload} />
              </Dock>
            ) : null}
            {signedIn ? <StatusBar connection={needs.connection} floor={floor.view} floorAgeMs={Math.max(0, now - floor.readAt)} project={project} warning={health?.warning} version={health?.version} /> : <footer className="statusbar" aria-label="Status" />}
          </div>
          <Palette open={paletteOpen} onClose={() => setPaletteOpen(false)} project={project} projectLabel={label} sources={sources} onConversation={chooseConversation} />
          <ShortcutSheet open={sheetOpen} onClose={() => setSheetOpen(false)} mac={mac} singleKeys={singleKeys} onSingleKeys={setSingleKeys} />
          </TalkProvider>
        </ArrivalsProvider>
      </CurrentProject.Provider>
      </PageActionsProvider>
    </ToastProvider>
  );
}
