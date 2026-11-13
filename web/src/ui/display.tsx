import { useId } from "react";
import type { ReactNode } from "react";

import { Link } from "../app/route";
import type { StateLook, Tone } from "../floor/states";
import { conditions, laneStates, lookOf, workStates } from "../floor/states";
import { Ago } from "../parts/parts";
import type { GlyphName, IconName } from "./Icon";
import { Glyph, Icon } from "./Icon";

// Badge is a state: a tinted pill with a glyph shape and a word, so it never reads by color alone.
export function Badge({ tone = "neutral", glyph, plain = false, children }: { tone?: Tone; glyph: GlyphName; plain?: boolean; children: ReactNode }) {
  return (
    <span className={`badge tone-${tone}${plain ? " plain" : ""}`} data-tone={tone}>
      <Glyph name={glyph} />
      {children}
    </span>
  );
}

export function LookBadge({ look, plain = false }: { look: StateLook; plain?: boolean }) {
  return (
    <Badge tone={look.tone} glyph={look.glyph} plain={plain}>
      {look.label}
    </Badge>
  );
}

// StateBadge says a record's state from one of the tables, or its own name when the table has no row.
export function StateBadge({ table, state, plain = false }: { table: Record<string, StateLook>; state: string; plain?: boolean }) {
  return <LookBadge look={lookOf(table, state)} plain={plain} />;
}

export function WorkBadge({ state }: { state: string }) {
  return <StateBadge table={workStates} state={state} />;
}

export function LaneBadge({ state }: { state: string }) {
  return <StateBadge table={laneStates} state={state} />;
}

// ConditionChip is a flag beside a lane's state, outlined so it never reads as a second state.
export function ConditionChip({ name }: { name: string }) {
  const look = conditions[name] ?? { label: name, tone: "neutral" as const };
  const tone = look.tone === "warning" ? " warn" : look.tone === "danger" ? " danger" : "";
  return <span className={`cond${tone}`}>{look.label}</span>;
}

// Id shows an identifier in mono on one line. One too long for its column ends in an ellipsis, and the
// whole of it stays in the page for a screen reader and a copy, and in the title a pointer brings up.
export function Id({ value }: { value: string }) {
  return (
    <span className="id" title={value}>
      {value}
    </span>
  );
}

// RecordChip names a record: its kind's icon and its identifier, in mono, and a link to it when it has one.
export function RecordChip({ id, icon, to, label }: { id: string; icon?: IconName; to?: string; label?: string }) {
  const body = (
    <>
      {icon ? <Icon name={icon} size="sm" /> : null}
      <span className="truncate">{id}</span>
    </>
  );
  return to ? (
    <Link to={to} className="chip" aria-label={label}>
      {body}
    </Link>
  ) : (
    <span className="chip" title={id}>
      {body}
    </span>
  );
}

export type TargetKind = "assistant" | "orchestrator" | "planning" | "lane";

const targetLooks: Record<TargetKind, { label: string; icon: IconName }> = {
  assistant: { label: "Assistant", icon: "delegate" },
  orchestrator: { label: "Orchestrator", icon: "hub" },
  planning: { label: "Planning session", icon: "flag" },
  lane: { label: "Lane", icon: "terminal" },
};

// TargetChip names a conversation target by its kind's label, color, and icon, which no two kinds share.
// Its form, a tag with an edge, is used for nothing else.
export function TargetChip({ kind, small = false, children }: { kind: TargetKind; small?: boolean; children?: ReactNode }) {
  const look = targetLooks[kind];
  return (
    <span className={`target-chip t-${kind}${small ? " sm" : ""}`}>
      <Icon name={look.icon} size="sm" />
      {children ?? look.label}
    </span>
  );
}

// ProjectTag labels a record with the project it belongs to: the project's mark, then its name.
export function ProjectTag({ name, display }: { name: string; display?: string }) {
  const shown = display || name;
  return (
    <span className="proj">
      <span className="proj-mark" aria-hidden="true">
        {shown.charAt(0).toUpperCase()}
      </span>
      {shown}
    </span>
  );
}

// Panel is a flat region with a hairline: a head that names it, a body, and a foot for its source.
export function Panel({ title, level = 2, actions, foot, flush = false, className = "", children }: {
  title?: ReactNode;
  level?: 2 | 3;
  actions?: ReactNode;
  foot?: ReactNode;
  flush?: boolean;
  className?: string;
  children: ReactNode;
}) {
  const heading = useId();
  const Title = level === 2 ? "h2" : "h3";
  return (
    <section className={`panel${className ? ` ${className}` : ""}`} aria-labelledby={title ? heading : undefined}>
      {title ? (
        <div className="panel-head">
          <Title id={heading}>{title}</Title>
          {actions}
        </div>
      ) : null}
      {flush ? children : <div className="panel-body">{children}</div>}
      {foot ? <div className="panel-foot">{foot}</div> : null}
    </section>
  );
}

// Facts is a list of names and values, the name on the left.
export function Facts({ children, label }: { children: ReactNode; label?: string }) {
  return (
    <dl className="kv" aria-label={label}>
      {children}
    </dl>
  );
}

export function Fact({ name, mono = false, children }: { name: string; mono?: boolean; children: ReactNode }) {
  return (
    <div className="kv-row">
      <dt>{name}</dt>
      <dd className={mono ? "mono" : undefined}>{children}</dd>
    </div>
  );
}

export type BannerTone = "neutral" | "active" | "warning" | "danger";

const bannerIcons: Record<BannerTone, IconName> = { neutral: "info", active: "info", warning: "warning", danger: "warning" };

// Banner states a fact about the view until the fact changes. A danger banner is an alert.
export function Banner({ tone = "neutral", icon, action, children }: { tone?: BannerTone; icon?: IconName; action?: ReactNode; children: ReactNode }) {
  return (
    <div className={`banner tone-${tone}`} role={tone === "danger" ? "alert" : undefined}>
      <Icon name={icon ?? bannerIcons[tone]} />
      <div className="banner-body">{children}</div>
      {action}
    </div>
  );
}



export type FreshnessKind = "observed" | "last_reported" | "stale";

const freshClass: Record<FreshnessKind, string> = { observed: "fresh", last_reported: "fresh reported", stale: "fresh stale" };
const freshWords: Record<FreshnessKind, string> = { observed: "Observed", last_reported: "Last reported", stale: "Stale" };

// freshnessOf reads the API's word for how a value was learned. Anything but observed is last reported.
export function freshnessOf(word: string): FreshnessKind {
  return word === "observed" ? "observed" : word === "stale" ? "stale" : "last_reported";
}

// Freshness says how a value was learned and how old it is (FR-UI-5): a solid dot for observed, a hollow
// one for last reported, a warning ring for stale, then the age, which counts on between reads. The
// tooltip names the source.
export function Freshness({ kind, at, source, children }: { kind: FreshnessKind; at?: number; source?: string; children?: ReactNode }) {
  return (
    <span className={freshClass[kind]} title={source ? `${freshWords[kind]} from ${source}` : undefined}>
      {freshWords[kind]}
      {at ? (
        <>
          {" "}
          <Ago at={at} />
        </>
      ) : null}
      {children}
    </span>
  );
}

// Absent says a value is missing, in words, and never draws it as zero.
export function Absent({ children = "unavailable" }: { children?: ReactNode }) {
  return <span className="unavailable">{children}</span>;
}

// EmptyState says what would appear, and offers the one action that fills it.
export function EmptyState({ icon = "info", title, action, children }: { icon?: IconName; title: string; action?: ReactNode; children?: ReactNode }) {
  return (
    <div className="empty">
      <Icon name={icon} size="lg" />
      <strong>{title}</strong>
      {children ? <p>{children}</p> : null}
      {action}
    </div>
  );
}

// SkeletonRows stand where a list will be while it reads, shaped like its rows.
export function SkeletonRows({ rows = 4, label = "Reading…" }: { rows?: number; label?: string }) {
  return (
    <div className="skeleton-rows" aria-busy="true">
      <span className="visually-hidden">{label}</span>
      {Array.from({ length: rows }, (_, at) => (
        <span key={at} className="skeleton-row" aria-hidden="true">
          <span className="skeleton" />
          <span className="skeleton skeleton-short" />
        </span>
      ))}
    </div>
  );
}

// TableRegion holds a table that may scroll sideways. It is named and focusable, so a keyboard can scroll it.
export function TableRegion({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="table-wrap" role="region" aria-label={label} tabIndex={0}>
      {children}
    </div>
  );
}

// Refusal is what the server refused, in its words, at the control that asked. The control stays usable.
export function Refusal({ children }: { children: ReactNode }) {
  return (
    <p className="msg msg-error refusal" role="alert">
      <Icon name="warning" size="sm" />
      <span>{children}</span>
    </p>
  );
}

// Digest shows the start of a digest, in mono, with the whole of it in its title.
export function Digest({ value, length = 12 }: { value: string; length?: number }) {
  return (
    <span className="digest" title={value}>
      {value.replace(/^sha256:/, "").slice(0, length)}
    </span>
  );
}
