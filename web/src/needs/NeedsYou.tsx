import { useContext, useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent, MouseEvent, ReactNode, Ref } from "react";

import { ApiError, decideApproval, readView } from "../api/client";
import type { Allowance, ManifestRegistration, NeedItem, NeedsYou as View, Plans, WorkOrder } from "../api/types";
import { useFlag } from "../app/prefs";
import { addressOf, CurrentProject, go, inProject, ItemProject, Link, useArrivalFocus, useRoute } from "../app/route";
import { useTalk } from "../talk/state";
import { useClock, ViewTime } from "../parts/parts";
import { Icon } from "../ui/Icon";
import { Button, FilterChip, Tabs, TabPanel } from "../ui/controls";
import { EmptyState, ProjectTag, Refusal } from "../ui/display";
import { HeldTag } from "../floor/StartHeld";
import { StartWork, startOf } from "../floor/StartWork";
import { Select } from "../ui/forms";
import { useNarrow } from "../ui/narrow";
import { ApprovalDecision } from "./ApprovalDecision";
import { CICard } from "./CICard";
import { ConfirmCard, ResolvedCard, voidAsk } from "./ConfirmCard";
import { ConflictCard } from "./ConflictCard";
import { ContractReviewCard } from "./ContractReviewCard";
import type { Decided } from "./decided";
import { EffectCard } from "./EffectCard";
import type { Frame } from "./frame";
import type { NeedFilter } from "./kinds";
import { contextOf, filterOf, filters, fullHeadingOf, headingOf, keyOf, kindOf, preparerOf } from "./kinds";
import { nextStepOf } from "./next";
import type { NextStep } from "./next";
import { arrange, flatten, gateRequests, orderOf, orders, permissionRequests, tierNames } from "./order";
import type { Entry, Order } from "./order";
import { PlanApprovalCard, RegisteredRepositories } from "./PlanApprovalCard";
import { PlanRevisionCard } from "./PlanRevisionCard";
import { QuestionBatch } from "./QuestionBatch";
import { RepositoryCreationCard } from "./RepositoryCreationCard";
import type { Answered } from "./QuestionBatch";
import { SpendCard } from "./SpendCard";
import { useAct } from "./useAct";
import { waitLabel, waitWords } from "./wait";
import { WorkCard } from "./WorkCard";

const hourMs = 3_600_000;

// A press this soon after the page moved to the next item is taken as the end of the last one, and presses nothing.
const settleMoveMs = 300;

// useAssistantCard is what an assistant's card offers beyond a delegate's: going to the conversation that
// prepared it, and asking the assistant again for one that went void. Both belong to the project the page shows.
function useAssistantCard(item: NeedItem | undefined) {
  const talk = useTalk();
  const current = useContext(CurrentProject);
  if (!item || item.kind !== "confirm" || !item.card.conversation || item.card.conversation.deleted || (item.project && item.project.name !== current)) {
    return {};
  }
  const conversation = item.card.conversation.id;
  return {
    openConversation: () => talk.select("assistant", conversation, { open: true, focus: true }),
    askAgain: () => talk.askAgain(conversation, voidAsk(item.card.approval.summary)),
  };
}

type Registered = { registration?: ManifestRegistration; onRegistered: (registration: ManifestRegistration) => void };

type Settled = { onAnswered: (answered: Answered) => void; onDecided: (decided: Decided) => void; onResolved: (words: string) => void; onRetried: (order: WorkOrder) => void };

function Card({ item, after, frame, registered, settled }: { item: NeedItem; after: () => Promise<void>; frame: Frame; registered: Registered; settled: Settled }) {
  const assistant = useAssistantCard(item);
  switch (item.kind) {
    case "questions":
      return <QuestionBatch batch={item.batch} after={after} frame={frame} onAnswered={settled.onAnswered} />;
    case "approval":
      return <ApprovalDecision approval={item.approval} after={after} frame={frame} onDecided={settled.onDecided} />;
    case "effect":
      return <EffectCard effect={item.effect} after={after} frame={frame} />;
    case "work":
      return <WorkCard id={item.id} work={item.work} after={after} frame={frame} onRetried={settled.onRetried} />;
    case "plan_approval":
      return <PlanApprovalCard id={item.id} need={item.plan_approval} after={after} frame={frame} registered={registered.registration} onRegistered={registered.onRegistered} onDecided={settled.onDecided} />;
    case "plan_revision":
      return <PlanRevisionCard need={item.plan_revision} after={after} frame={frame} />;
    case "contract_review":
      return <ContractReviewCard review={item.contract_review} after={after} frame={frame} />;
    case "ci":
      return <CICard need={item.ci} after={after} frame={frame} />;
    case "spend":
      return <SpendCard need={item.spend} after={after} frame={frame} />;
    case "repository_creation":
      return <RepositoryCreationCard need={item.repository_creation} after={after} frame={frame} onResolved={settled.onResolved} />;
    case "confirm":
      return <ConfirmCard need={item.card} after={after} frame={frame} {...assistant} />;
    case "conflict":
      return <ConflictCard need={item.conflict} frame={frame} />;
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

// NextStepLine says what a decision set going, and links where to watch it.
function NextStepLine({ step, project }: { step: NextStep; project: string }) {
  return (
    <p>
      {step.words} <Link to={project ? inProject(project, step.to) : step.to}>{step.link}</Link>
    </p>
  );
}

// ResolvedNote says why the selected item no longer waits: the person answered or decided it on this page,
// with who the ledger recorded it for and what it was, or else it was settled somewhere else. It points to the next
// item only when one waits, and otherwise says what happens next.
function ResolvedNote({ heading, answered, decided, resolved, noteRef, more, next }: {
  heading: string;
  answered?: Answered;
  decided?: Decided;
  resolved?: string;
  noteRef: Ref<HTMLParagraphElement>;
  more: boolean;
  next: ReactNode;
}) {
  const by = (answered ?? decided)?.by;
  const done = answered ? "answered" : decided ? "decided" : "";
  const then = more ? " Press J for the next item." : " Nothing else waits.";
  return (
    <>
      <p className="resolved-note" ref={noteRef} tabIndex={-1}>
        <Icon name="check" />
        {done
          ? `${heading} no longer waits: you ${done} it${by ? `, as ${by}` : ""}.${then}`
          : resolved
            ? `${heading} no longer waits: ${resolved}.${then}`
            : `${heading} no longer waits: it was decided or resolved in another place.${then}`}
      </p>
      {more ? null : next}
      {decided ? (
        <ul className="links" aria-label="Your decision">
          <li>
            <p className="question-text">{decided.approved ? "Approved" : "Declined"}</p>
            {decided.reason ? <p>Reason: {decided.reason}</p> : null}
          </li>
        </ul>
      ) : null}
      {answered ? (
        <ul className="links" aria-label="Your answers">
          {answered.answers.map((row) => (
            <li key={row.id}>
              <p className="question-text">{row.question}</p>
              <p>{row.answer}</p>
            </li>
          ))}
        </ul>
      ) : null}
    </>
  );
}

// StartRetried offers Start for work a retry returned to ready, as the plans view says the work stands now: its
// state, the hold the ledger keeps it in, and what a member may do to it. Work the view does not offer a claim or a
// lane start of, or a view that cannot be read, leads to the work order's page instead. The dialog works in the
// project the page shows, so work of another project leads to its page as well.
function StartRetried({ order, project, after }: { order: WorkOrder; project?: string; after: () => Promise<void> }) {
  const current = useContext(CurrentProject);
  const elsewhere = Boolean(project && project !== current);
  const [now, setNow] = useState<{ order: WorkOrder; allowed?: Allowance } | null>(null);
  useEffect(() => {
    if (elsewhere) {
      return;
    }
    let live = true;
    readView<Plans>("plans")
      .then((plans) => {
        const found = [...plans.plans.flatMap((plan) => plan.work_orders), ...plans.unplanned].find((row) => row.id === order.id);
        if (live && found) {
          setNow({ order: found, allowed: plans.work_actions?.[found.id] });
        }
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [order.id, elsewhere]);
  if (!elsewhere && now && ["claim", "lane"].includes(startOf(now.order, now.allowed)?.kind ?? "")) {
    return (
      <p className="row-tight">
        <StartWork order={now.order} allowed={now.allowed} after={after} />
        <HeldTag hold={now.order.start_held} />
      </p>
    );
  }
  return (
    <ItemProject.Provider value={project ?? ""}>
      <p className="small">
        <Link to={addressOf("/work-order", { id: order.id })}>Open work order {order.id}</Link>
      </p>
    </ItemProject.Provider>
  );
}

// LaneGroupHead stands over a lane's items: the lane, its work order, how many wait, and Approve all for its
// permission requests. Approve all decides each request bound to the digest this page read, so a request that
// changed or went void is refused, skipped, and named, and the rest are approved.
function LaneGroupHead({ entry, after, onActed }: { entry: Entry; after: () => Promise<void>; onActed: (keys: string[]) => void }) {
  const { act, busy, failure } = useAct(after);
  const [skipped, setSkipped] = useState<string[]>([]);
  const requests = permissionRequests(entry.items);
  const gates = gateRequests(entry.items).length;
  const [first] = entry.items;
  const project = first?.project?.name ?? "";
  const workOrder = requests[0]?.work_order_id ?? "";
  const approveAll = () =>
    act(async () => {
      const missed: string[] = [];
      onActed(entry.items.filter((item) => item.kind === "approval" && !item.approval.gate).map(keyOf));
      for (const request of requests) {
        try {
          await decideApproval(request.id, request.subject_digest, true, "", "needs_you");
        } catch (error) {
          missed.push(`${request.subject}: ${error instanceof ApiError ? error.message : String(error)}`);
        }
      }
      setSkipped(missed);
    });
  const lane = addressOf("/lane", { id: entry.lane });
  return (
    <div className="lane-group-head">
      <div className="lane-group-title">
        <strong>Lane {entry.lane}</strong>
        {requests.length > 1 ? (
          <Button small loading={busy} disabled={busy} onClick={() => void approveAll()} aria-describedby={`group-${entry.lane}-says`}>
            Approve all {requests.length}
          </Button>
        ) : null}
      </div>
      <p className="subtle" id={`group-${entry.lane}-says`}>
        {[workOrder, `${entry.items.length} waiting`].filter(Boolean).join(" · ")}
        {requests.length > 1 ? `. Approve all allows each of its ${requests.length} requests as its card shows it, bound to the digest this page read.` : ""}
        {gates > 0 ? ` ${gates === 1 ? "1 gate waits" : `${gates} gates wait`} to be reviewed one by one, and Approve all leaves ${gates === 1 ? "it" : "them"} out.` : ""}{" "}
        <Link to={project ? inProject(project, lane) : lane}>Open lane</Link>
      </p>
      {skipped.length > 0 ? (
        <Refusal>
          {skipped.length === 1 ? "One request was skipped" : `${skipped.length} requests were skipped`}, because it changed or went void: {skipped.join("; ")}
        </Refusal>
      ) : null}
      {failure ? <Refusal>{failure}</Refusal> : null}
    </div>
  );
}

// guardPress keeps a press from deciding the next item when the page just moved there, or when a key is held
// down, so a double press never decides two items.
function guardPress(movedAt: { current: number }) {
  return (event: KeyboardEvent | MouseEvent) => {
    if (!(event.target instanceof Element) || !event.target.closest("button")) {
      return;
    }
    const held = "repeat" in event && event.repeat && (event.key === "Enter" || event.key === " ");
    const soon = !("key" in event) && Date.now() - movedAt.current < settleMoveMs;
    if (held || soon) {
      event.preventDefault();
      event.stopPropagation();
    }
  };
}

type Props = { view: View; readAt: number; after: () => Promise<void>; active?: boolean };

// NeedsYou lists what waits on a member in every active project, the most important first and then the longest
// wait (FR-UI-2, FR-PROJ-8), each lane's items together, beside the selected item's decision card. After a
// decision on its card, the next item opens with focus on its primary button. An item another place resolved
// stays where it stood, struck through, until the person moves on, so nothing moves under the cursor.
export function NeedsYou({ view, readAt, after, active = true }: Props) {
  const now = useClock();
  const elapsed = Math.max(0, now - readAt);
  const route = useRoute();
  const narrow = useNarrow();
  const current = useContext(CurrentProject);
  const title = useRef<HTMLHeadingElement>(null);
  const resolvedNote = useRef<HTMLParagraphElement>(null);
  const doneNote = useRef<HTMLDivElement>(null);
  const tabsId = useId();
  useArrivalFocus(title, active);
  const params = active ? route.params : new URLSearchParams();
  const filter = (filters.find((row) => row.id === params.get("kind"))?.id ?? "all") as NeedFilter;
  const project = params.get("project") ?? "";
  const tab = params.get("tab") === "decided" ? "decided" : "open";
  const chosenKey = params.get("item") ?? "";
  const order: Order = orderOf(params.get("order"));
  const grouped = params.get("group") !== "off";
  const [advance, setAdvance] = useFlag("ropeshift-advance", true);
  // acted holds the items a person decided on this page, whose leaving the list moves the page to the next item.
  const acted = useRef(new Set<string>());
  // movedAt is when the page last moved to the next item on its own, focusNext the item whose primary button takes
  // focus once it shows, and said the polite line that says what happened and what is next.
  const movedAt = useRef(0);
  const focusNext = useRef("");
  const [said, setSaid] = useState("");
  // finished is the last item a person decided here, once nothing else waits, which says what happens next.
  const [finished, setFinished] = useState<NeedItem | null>(null);

  // registered holds what approving a plan registered of its manifest's repositories, by item, so a member reads
  // it after the plan stops waiting and its card goes.
  const [registered, setRegistered] = useState<Record<string, ManifestRegistration>>({});
  // answered holds what the person answered on this page, by item, so an item that stops waiting because of it
  // says so, and not that another place settled it.
  const [answered, setAnswered] = useState<Record<string, Answered>>({});
  // decided holds the same for a request the person approved or declined on this page.
  const [decided, setDecided] = useState<Record<string, Decided>>({});
  // resolved holds, in words, how the person settled an item that has no answer or decision of its own, such as a repository they
  // retried or skipped.
  const [resolved, setResolved] = useState<Record<string, string>>({});
  // retried holds the work a member returned to ready from its card, by item, which has no lane until someone starts one.
  const [retried, setRetried] = useState<Record<string, WorkOrder>>({});

  const address = (changes: Record<string, string>) =>
    addressOf("/needs-you", {
      project,
      kind: filter === "all" ? "" : filter,
      tab: tab === "open" ? "" : tab,
      order: order === "importance" ? "" : order,
      group: grouped ? "" : "off",
      item: chosenKey,
      ...changes,
    });

  // outcomeOf says what a person did to an item on this page, for the line that says what is next.
  const outcomeOf = (key: string, item: NeedItem) => {
    const heading = headingOf(item).replace(/[.?!]+$/, "");
    const done = decided[key];
    if (answered[key]) {
      return `Answered: ${heading}`;
    }
    if (done) {
      return `${done.approved ? "Approved" : "Declined"}: ${heading}`;
    }
    return resolved[key] ? `${heading}: ${resolved[key]}` : `Done: ${heading}`;
  };

  // gone holds the selected item after the server stopped listing it, which stays at the place it stood.
  const [gone, setGone] = useState<NeedItem | null>(null);
  const shown = useRef<NeedItem[]>(view.items);
  // lastSelected is the item the person had selected at the last render, before this read arrived, and lastListed
  // the order the list showed then, which says what came after it.
  const lastSelected = useRef("");
  const lastListed = useRef<string[]>([]);
  // An item a person decided here moves the page to the next item in the list's order, and says so. One that
  // another place settled stays, struck through, until the person moves on.
  useEffect(() => {
    const was = shown.current;
    const key = lastSelected.current;
    shown.current = view.items;
    const still = (wanted: string | undefined) => view.items.some((item) => keyOf(item) === wanted);
    const item = key && !still(key) ? was.find((row) => keyOf(row) === key) : undefined;
    const decidedHere = item !== undefined && acted.current.has(key);
    for (const wanted of [...acted.current]) {
      if (still(wanted) || wanted === key) {
        acted.current.delete(wanted);
      }
    }
    // A page the person left moves nowhere, and keeps nothing struck through for them to come back to.
    if (!item || !active) {
      return;
    }
    // Work a member retried here waits for them to start it, so the page stays on it to offer Start.
    if (!decidedHere || !advance || retried[key] !== undefined) {
      setGone(item);
      return;
    }
    const at = lastListed.current.indexOf(key);
    const following = at < 0 ? [] : [...lastListed.current.slice(at + 1), ...lastListed.current.slice(0, at).reverse()];
    const next = view.items.find((row) => keyOf(row) === following.find(still));
    if (next) {
      movedAt.current = Date.now();
      focusNext.current = keyOf(next);
      const then = headingOf(next);
      setSaid(`${outcomeOf(key, item)}. Next: ${/[.?!]$/.test(then) ? then : `${then}.`}`);
      go(address({ item: keyOf(next) }), { stay: true, replace: true });
      return;
    }
    setFinished(item);
    setSaid(`${outcomeOf(key, item)}. Nothing else needs you.`);
    go(address({ item: "" }), { stay: true, replace: true });
  }, [view.items]);

  // Leaving the page is moving on, so an item settled while it was open is gone when the person comes back.
  useEffect(() => {
    if (!active) {
      setGone(null);
    }
  }, [active]);

  // An item the server labeled with no project is of the only project there is.
  const ofProject = (item: NeedItem) => !project || !item.project || item.project.name === project;
  const counts = Object.fromEntries(filters.map((row) => [row.id, view.items.filter((item) => ofProject(item) && (row.id === "all" || filterOf(item) === row.id)).length]));
  const kept = (item: NeedItem) => ofProject(item) && (filter === "all" || filterOf(item) === filter);
  const visible = view.items.filter(kept);
  // The item that left stays in the list where its tier and its wait put it, until the person moves on.
  const sections = arrange(gone && kept(gone) && !visible.some((item) => keyOf(item) === keyOf(gone)) ? [...visible, gone] : visible, order, grouped);
  const listed = flatten(sections);
  // A person who looks at another view and comes back finds the item they had open.
  const remembered = useRef("");
  if (chosenKey) {
    remembered.current = chosenKey;
  }
  const selected =
    listed.find((item) => keyOf(item) === chosenKey) ?? (chosenKey ? undefined : (listed.find((item) => keyOf(item) === remembered.current) ?? (narrow ? undefined : listed[0])));
  const selectedKey = selected ? keyOf(selected) : "";
  const assistantOfSelected = useAssistantCard(selected);
  useEffect(() => {
    lastSelected.current = selectedKey;
    lastListed.current = listed.map(keyOf);
  });
  const isGone = (item: NeedItem) => gone !== null && keyOf(gone) === keyOf(item);

  // The next item's primary button takes focus once its card shows. One that cannot be pressed yet, such as a batch
  // with nothing chosen, leaves focus on the card's first button, or on the item in the list.
  useEffect(() => {
    if (!focusNext.current || focusNext.current !== selectedKey) {
      return;
    }
    focusNext.current = "";
    const card = [...document.querySelectorAll<HTMLElement>("[data-card]")].find((element) => element.dataset.card === selectedKey);
    (card?.querySelector<HTMLElement>(".decide .btn-primary:not(:disabled)") ?? card?.querySelector<HTMLElement>(".decide .btn:not(:disabled)") ?? document.getElementById(`need-${selectedKey}`))?.focus();
  });

  // With nothing left, focus moves to the line that says so, and what happens next. A new item ends it.
  useEffect(() => {
    if (finished && active) {
      doneNote.current?.focus();
    }
  }, [finished, active]);
  const anyListed = listed.length > 0;
  useEffect(() => {
    if (anyListed) {
      setFinished(null);
    }
  }, [anyListed]);

  const select = (item: NeedItem | undefined, focus = true) => {
    if (!item) {
      return;
    }
    if (gone && keyOf(gone) !== keyOf(item)) {
      setGone(null);
    }
    setFinished(null);
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
  const orderLabel = (orders.find((row) => row.id === order)?.label ?? "").toLowerCase();
  const nextOf = (item: NeedItem) => <NextStepLine step={nextStepOf(item, decided[keyOf(item)])} project={item.project?.name ?? current} />;
  const guard = guardPress(movedAt);
  const waitOf = (item: NeedItem) => view.as_of - item.since + elapsed;
  const itemRow = (item: NeedItem) => {
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
            if (gone && keyOf(gone) !== key) {
              setGone(null);
            }
            setFinished(null);
          }}
        >
          <Icon name={kindOf(item).icon} />
          <span className="item-title" title={fullHeadingOf(item) === headingOf(item) ? undefined : fullHeadingOf(item)}>
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
              <span className="truncate">{done ? (answered[key] ? "Answered by you" : decided[key] ? "Decided by you" : resolved[key] ? "Settled by you" : "Resolved elsewhere") : contextOf(item)}</span>
            )}
          </span>
        </Link>
      </li>
    );
  };
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
                {view.items.length === 0 ? "Nothing is waiting." : `${view.items.length} waiting in ${projectsCount === 1 ? "1 project" : `${projectsCount} projects`} · ${orderLabel}`}
              </p>
              <p className="visually-hidden" aria-live="polite" data-said>
                {said}
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
                <div className="needs-controls">
                  <Select small aria-label="Order" value={order} onChange={(event) => go(address({ order: event.target.value === "importance" ? "" : event.target.value }), { stay: true })}>
                    {orders.map((row) => (
                      <option key={row.id} value={row.id}>
                        {row.label}
                      </option>
                    ))}
                  </Select>
                  <label className="check-row">
                    <input type="checkbox" checked={grouped} onChange={(event) => go(address({ group: event.target.checked ? "" : "off" }), { stay: true })} />
                    Group by lane
                  </label>
                  <label className="check-row">
                    <input type="checkbox" checked={advance} onChange={(event) => setAdvance(event.target.checked)} />
                    Advance after each decision
                  </label>
                </div>
              </>
            ) : null}
          </div>
          <TabPanel idBase={tabsId} selected={tab}>
            {tab === "decided" ? (
              <EmptyState icon="history" title="Decided requests are not shown yet">
                This build's API lists only what still waits. Each decision stays in the project's ledger, and the last 30 days of them arrive here in a later build.
              </EmptyState>
            ) : listed.length === 0 && finished ? (
              <div className="empty needs-done" ref={doneNote} tabIndex={-1}>
                <Icon name="check" size="lg" />
                <strong>{view.items.some(ofProject) ? "Nothing else of this kind needs you" : "Nothing else needs you"}</strong>
                {nextOf(finished)}
              </div>
            ) : listed.length === 0 ? (
              <EmptyState icon="inbox" title={view.items.length === 0 ? "Nothing needs you" : "Nothing of this kind waits"}>
                {view.items.length === 0
                  ? "Every lane has what it needs from you. New items appear here as they arrive: plan approvals, questions, approval requests, uncertain effects, and stuck work."
                  : "Choose All to see everything that waits."}
              </EmptyState>
            ) : (
              <ol className="split-list-body" aria-label="Waiting items">
                {sections.map((section) => {
                  const rows = section.entries.map((entry) =>
                    entry.lane ? (
                      <li key={`lane:${entry.lane}`} className="lane-group">
                        <ItemProject.Provider value={entry.items[0]?.project?.name ?? ""}>
                          <LaneGroupHead entry={entry} after={after} onActed={(keys) => keys.forEach((key) => acted.current.add(key))} />
                        </ItemProject.Provider>
                        <ol aria-label={`Lane ${entry.lane}`}>{entry.items.map((item) => itemRow(item))}</ol>
                      </li>
                    ) : (
                      entry.items.map((item) => itemRow(item))
                    ),
                  );
                  if (section.tier === null) {
                    return rows;
                  }
                  const count = section.entries.reduce((sum, entry) => sum + entry.items.length, 0);
                  const heading = `${tabsId}-tier-${section.tier}`;
                  return (
                    <li key={`tier:${section.tier}`} className="tier">
                      <h2 className="tier-head" id={heading}>
                        <span>{tierNames[section.tier] ?? `Tier ${section.tier}`}</span>
                        <span>
                          <span className="visually-hidden">, </span>
                          {count}
                        </span>
                      </h2>
                      <ol aria-labelledby={heading}>{rows}</ol>
                    </li>
                  );
                })}
              </ol>
            )}
          </TabPanel>
        </section>
        <section className="split-detail" aria-label="The selected item" onKeyDownCapture={guard} onClickCapture={guard}>
          {narrow && selected ? (
            <Button small icon="chevron-left" className="back-to-list" onClick={() => go(address({ item: "" }), { stay: true })}>
              All items
            </Button>
          ) : null}
          {selected && tab === "open" && isGone(selected) && selected.kind !== "confirm" ? (
            <div className="decision is-resolved">
              <div className="decision-body">
                <ResolvedNote
                  heading={headingOf(selected)}
                  answered={answered[keyOf(selected)]}
                  decided={decided[keyOf(selected)]}
                  resolved={resolved[keyOf(selected)]}
                  noteRef={resolvedNote}
                  more={listed.some((item) => keyOf(item) !== keyOf(selected))}
                  next={nextOf(selected)}
                />
                {retried[keyOf(selected)] ? <StartRetried order={retried[keyOf(selected)] as WorkOrder} project={selected.project?.name} after={after} /> : null}
                {selected.kind === "plan_approval" && registered[keyOf(selected)] ? <RegisteredRepositories registration={registered[keyOf(selected)] as ManifestRegistration} approval={selected.id} /> : null}
              </div>
            </div>
          ) : null}
          {selected && tab === "open" && isGone(selected) && selected.kind === "confirm" ? (
            <ResolvedCard need={selected.card} project={selected.project?.name ?? ""} after={after} frame={frameOf(selected)} askAgain={assistantOfSelected.askAgain} />
          ) : null}
          {/* Every card stays mounted, and only the selected one shows, so an answer in progress survives a look at another item. */}
          {view.items.map((item) => (
            <div key={keyOf(item)} data-card={keyOf(item)} hidden={tab !== "open" || !selected || keyOf(selected) !== keyOf(item)}>
              <ItemProject.Provider value={item.project?.name ?? ""}>
                <Card
                  item={item}
                  after={async (ok?: boolean) => {
                    // Only a change the server took is a decision made here; a refused one leaves the card to say why.
                    if (ok !== false) {
                      acted.current.add(keyOf(item));
                    }
                    await after();
                  }}
                  frame={frameOf(item)}
                  registered={{ registration: registered[keyOf(item)], onRegistered: (registration) => setRegistered((was) => ({ ...was, [keyOf(item)]: registration })) }}
                  settled={{
                    onAnswered: (done) => setAnswered((was) => ({ ...was, [keyOf(item)]: done })),
                    onDecided: (done) => setDecided((was) => ({ ...was, [keyOf(item)]: done })),
                    onResolved: (words) => setResolved((was) => ({ ...was, [keyOf(item)]: words })),
                    onRetried: (order) => {
                      setResolved((was) => ({ ...was, [keyOf(item)]: "you retried it, and the work is ready, but no lane has started. Start it to choose the worker that runs it" }));
                      setRetried((was) => ({ ...was, [keyOf(item)]: order }));
                    },
                  }}
                />
              </ItemProject.Provider>
            </div>
          ))}
        </section>
      </div>
    </ViewTime.Provider>
  );
}
