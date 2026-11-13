import { useEffect, useId, useRef, useState } from "react";

import type { NeedItem, NeedsYou as View } from "../api/types";
import { addressOf, go, ItemProject, Link, useArrivalFocus, useRoute } from "../app/route";
import { useClock, ViewTime } from "../parts/parts";
import { Icon } from "../ui/Icon";
import { Button, FilterChip, Tabs, TabPanel } from "../ui/controls";
import { EmptyState, ProjectTag } from "../ui/display";
import { Select } from "../ui/forms";
import { ApprovalCard } from "./ApprovalCard";
import { ConfirmCard, ResolvedCard } from "./ConfirmCard";
import { ContractReviewCard } from "./ContractReviewCard";
import { EffectCard } from "./EffectCard";
import type { Frame } from "./frame";
import type { NeedFilter } from "./kinds";
import { contextOf, filterOf, filters, headingOf, keyOf, kindOf, preparerOf } from "./kinds";
import { PlanApprovalCard } from "./PlanApprovalCard";
import { RecoveryCard } from "./RecoveryCard";
import { QuestionBatch } from "./QuestionBatch";
import { waitLabel, waitWords } from "./wait";
import { WorkCard } from "./WorkCard";

const hourMs = 3_600_000;

function Card({ item, after, frame }: { item: NeedItem; after: () => Promise<void>; frame: Frame }) {
  switch (item.kind) {
    case "questions":
      return <QuestionBatch batch={item.batch} after={after} frame={frame} />;
    case "approval":
      return item.approval.kind === "recovery" ? <RecoveryCard approval={item.approval} after={after} frame={frame} /> : <ApprovalCard approval={item.approval} after={after} frame={frame} />;
    case "effect":
      return <EffectCard effect={item.effect} after={after} frame={frame} />;
    case "work":
      return <WorkCard id={item.id} work={item.work} after={after} frame={frame} />;
    case "plan_approval":
      return <PlanApprovalCard id={item.id} need={item.plan_approval} after={after} frame={frame} />;
    case "contract_review":
      return <ContractReviewCard review={item.contract_review} after={after} frame={frame} />;
    case "confirm":
      return <ConfirmCard need={item.card} after={after} frame={frame} />;
  }
}

function Wait({ ms }: { ms: number }) {
  return (
    <span className={`item-wait${ms >= hourMs ? " long" : ""}`}>
      <span aria-hidden="true">{waitLabel(ms)}</span>
      <span className="visually-hidden">{waitWords(ms)}</span>
    </span>
  );
}

// useNarrow says the page is as narrow as a phone, where a list and its detail take turns.
function useNarrow(): boolean {
  const query = "(max-width: 639px)";
  const [narrow, setNarrow] = useState(() => window.matchMedia?.(query).matches ?? false);
  useEffect(() => {
    const media = window.matchMedia?.(query);
    const change = () => setNarrow(media?.matches ?? false);
    media?.addEventListener?.("change", change);
    return () => media?.removeEventListener?.("change", change);
  }, []);
  return narrow;
}

type Props = { view: View; readAt: number; after: () => Promise<void>; active?: boolean };

// NeedsYou lists what waits on a member in every active project, the longest wait first (FR-UI-2,
// FR-PROJ-8), beside the selected item's decision card. An item another place resolved stays where it
// stood, struck through, until the person moves on, so nothing moves under the cursor.
export function NeedsYou({ view, readAt, after, active = true }: Props) {
  const now = useClock();
  const elapsed = Math.max(0, now - readAt);
  const route = useRoute();
  const narrow = useNarrow();
  const title = useRef<HTMLHeadingElement>(null);
  const resolvedNote = useRef<HTMLParagraphElement>(null);
  const tabsId = useId();
  useArrivalFocus(title, active);
  const params = active ? route.params : new URLSearchParams();
  const filter = (filters.find((row) => row.id === params.get("kind"))?.id ?? "all") as NeedFilter;
  const project = params.get("project") ?? "";
  const tab = params.get("tab") === "decided" ? "decided" : "open";
  const chosenKey = params.get("item") ?? "";

  // gone holds the selected item after the server stopped listing it, at the place it stood.
  const [gone, setGone] = useState<{ item: NeedItem; at: number } | null>(null);
  const shown = useRef<NeedItem[]>(view.items);
  // lastSelected is the item the person had selected at the last render, before this read arrived.
  const lastSelected = useRef("");
  useEffect(() => {
    const was = shown.current;
    const key = lastSelected.current;
    if (key && !view.items.some((item) => keyOf(item) === key)) {
      const at = was.findIndex((item) => keyOf(item) === key);
      const item = was[at];
      if (item) {
        setGone({ item, at });
      }
    }
    shown.current = view.items;
  }, [view.items]);

  // An item the server labeled with no project is of the only project there is.
  const inProject = (item: NeedItem) => !project || !item.project || item.project.name === project;
  const counts = Object.fromEntries(filters.map((row) => [row.id, view.items.filter((item) => inProject(item) && (row.id === "all" || filterOf(item) === row.id)).length]));
  let listed = view.items.filter((item) => inProject(item) && (filter === "all" || filterOf(item) === filter));
  if (gone && !listed.some((item) => keyOf(item) === keyOf(gone.item))) {
    listed = [...listed.slice(0, gone.at), gone.item, ...listed.slice(gone.at)];
  }
  // A person who looks at another view and comes back finds the item they had open.
  const remembered = useRef("");
  if (chosenKey) {
    remembered.current = chosenKey;
  }
  const selected =
    listed.find((item) => keyOf(item) === chosenKey) ?? (chosenKey ? undefined : (listed.find((item) => keyOf(item) === remembered.current) ?? (narrow ? undefined : listed[0])));
  const selectedKey = selected ? keyOf(selected) : "";
  useEffect(() => {
    lastSelected.current = selectedKey;
  });
  const isGone = (item: NeedItem) => gone !== null && keyOf(gone.item) === keyOf(item);

  const address = (changes: Record<string, string>) => addressOf("/needs-you", { project, kind: filter === "all" ? "" : filter, tab: tab === "open" ? "" : tab, item: chosenKey, ...changes });

  const select = (item: NeedItem | undefined, focus = true) => {
    if (!item) {
      return;
    }
    if (gone && keyOf(gone.item) !== keyOf(item)) {
      setGone(null);
    }
    go(address({ item: keyOf(item) }), { stay: true });
    if (focus) {
      requestAnimationFrame(() => document.getElementById(`need-${keyOf(item)}`)?.focus());
    }
  };

  // J and K move through the list and select as they go.
  useEffect(() => {
    if (!active) {
      return;
    }
    const move = (event: Event) => {
      const step = (event as CustomEvent<number>).detail;
      const at = listed.findIndex((item) => selected && keyOf(item) === keyOf(selected));
      const next = listed[Math.min(listed.length - 1, Math.max(0, at + step))];
      if (next) {
        event.preventDefault();
        select(next);
      }
    };
    window.addEventListener("ropeshift:move", move);
    return () => window.removeEventListener("ropeshift:move", move);
  });

  // The palette opens a decision with its primary button focused. It presses nothing.
  const focusAsked = active && params.get("focus") === "primary";
  useEffect(() => {
    if (focusAsked) {
      [...document.querySelectorAll<HTMLElement>("#main .split-detail .decide .btn-primary")].find((button) => !button.closest("[hidden]"))?.focus();
      go(address({}), { stay: true, replace: true });
    }
  });

  // A resolved decision moves focus to its resolution line.
  const selectedGone = selected !== undefined && isGone(selected);
  useEffect(() => {
    if (selectedGone && active && document.activeElement === document.body) {
      resolvedNote.current?.focus();
    }
  }, [selectedGone, active]);

  const projectsCount = new Set(view.items.map((item) => item.project?.name ?? "")).size;
  const waitOf = (item: NeedItem) => view.as_of - item.since + elapsed;
  const frameOf = (item: NeedItem): Frame => ({
    project: item.project,
    title: headingOf(item),
    provenance:
      item.kind === "confirm" ? (
        <Wait ms={waitOf(item)} />
      ) : (
        <>
          {contextOf(item)} · <Wait ms={waitOf(item)} />
        </>
      ),
  });

  return (
    <ViewTime.Provider value={{ asOf: view.as_of, elapsed }}>
      <div className={`split${selected && (narrow || chosenKey) ? " show-detail" : ""}`}>
        <section className="split-list" aria-labelledby={`${tabsId}-title`}>
          <div className="split-list-head">
            <div className="split-title">
              <h1 id={`${tabsId}-title`} ref={title} tabIndex={-1}>
                Needs you
              </h1>
              <p className="subtle small" aria-live="polite">
                {view.items.length === 0 ? "Nothing is waiting." : `${view.items.length} waiting in ${projectsCount === 1 ? "1 project" : `${projectsCount} projects`} · oldest first`}
              </p>
            </div>
            <Tabs
              label="Requests"
              idBase={tabsId}
              selected={tab}
              tabs={[
                { id: "open", label: "Open", count: view.items.length },
                { id: "decided", label: "Decided" },
              ]}
              onSelect={(id) => go(address({ tab: id === "open" ? "" : id, item: "" }), { stay: true })}
            />
            {tab === "open" ? (
              <>
                <div className="filter-row" role="group" aria-label="Kinds">
                  {filters.map((row) => (
                    <FilterChip key={row.id} pressed={filter === row.id} count={counts[row.id]} onClick={() => go(address({ kind: row.id === "all" ? "" : row.id, item: "" }), { stay: true })}>
                      {row.label}
                    </FilterChip>
                  ))}
                </div>
                {(view.projects?.length ?? 0) > 1 ? (
                  <Select small aria-label="Project" value={project} onChange={(event) => go(address({ project: event.target.value, item: "" }), { stay: true })}>
                    <option value="">All active projects</option>
                    {view.projects?.map((row) => (
                      <option key={row.name} value={row.name}>
                        {row.display_name || row.name}
                      </option>
                    ))}
                  </Select>
                ) : null}
              </>
            ) : null}
          </div>
          <TabPanel idBase={tabsId} selected={tab}>
            {tab === "decided" ? (
              <EmptyState icon="history" title="Decided requests are not shown yet">
                This build's API lists only what still waits. Each decision stays in the project's ledger, and the last 30 days of them arrive here in a later build.
              </EmptyState>
            ) : listed.length === 0 ? (
              <EmptyState icon="inbox" title={view.items.length === 0 ? "Nothing needs you" : "Nothing of this kind waits"}>
                {view.items.length === 0
                  ? "Every lane has what it needs from you. New items appear here as they arrive: plan approvals, questions, approval requests, uncertain effects, and stuck work."
                  : "Choose All to see everything that waits."}
              </EmptyState>
            ) : (
              <ol className="split-list-body" aria-label="Waiting items">
                {listed.map((item) => {
                  const key = keyOf(item);
                  const done = isGone(item);
                  return (
                    <li key={key}>
                      <Link
                        id={`need-${key}`}
                        to={address({ item: key })}
                        stay
                        data-item
                        className={`item${done ? " done" : ""}`}
                        aria-current={selected && keyOf(selected) === key ? "true" : undefined}
                        onClick={() => {
                          if (gone && keyOf(gone.item) !== key) {
                            setGone(null);
                          }
                        }}
                      >
                        <Icon name={kindOf(item).icon} />
                        <span className="item-title">
                          {headingOf(item)}
                          {done ? <span className="visually-hidden"> (resolved)</span> : null}
                        </span>
                        <Wait ms={waitOf(item)} />
                        <span className="item-meta">
                          {item.project ? <ProjectTag name={item.project.name} display={item.project.display_name} /> : null}
                          {item.kind === "confirm" && !done ? (
                            <span className="truncate">
                              <Icon name="key" size="sm" /> {preparerOf(item.card)}
                            </span>
                          ) : (
                            <span className="truncate">{done ? "Resolved elsewhere" : contextOf(item)}</span>
                          )}
                        </span>
                      </Link>
                    </li>
                  );
                })}
              </ol>
            )}
          </TabPanel>
        </section>
        <section className="split-detail" aria-label="The selected item">
          {narrow && selected ? (
            <Button small icon="chevron-left" className="back-to-list" onClick={() => go(address({ item: "" }), { stay: true })}>
              All items
            </Button>
          ) : null}
          {selected && tab === "open" && isGone(selected) && selected.kind !== "confirm" ? (
            <div className="decision is-resolved">
              <div className="decision-body">
                <p className="resolved-note" ref={resolvedNote} tabIndex={-1}>
                  <Icon name="check" />
                  {headingOf(selected)} no longer waits: it was decided or resolved in another place. Press J for the next item.
                </p>
              </div>
            </div>
          ) : null}
          {selected && tab === "open" && isGone(selected) && selected.kind === "confirm" ? (
            <ResolvedCard need={selected.card} project={selected.project?.name ?? ""} after={after} frame={frameOf(selected)} />
          ) : null}
          {/* Every card stays mounted, and only the selected one shows, so an answer in progress survives a look at another item. */}
          {view.items.map((item) => (
            <div key={keyOf(item)} hidden={tab !== "open" || !selected || keyOf(selected) !== keyOf(item)}>
              <ItemProject.Provider value={item.project?.name ?? ""}>
                <Card item={item} after={after} frame={frameOf(item)} />
              </ItemProject.Provider>
            </div>
          ))}
        </section>
      </div>
    </ViewTime.Provider>
  );
}
