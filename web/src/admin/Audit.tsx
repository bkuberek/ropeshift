import { useEffect, useId, useState } from "react";

import { readAudit } from "../api/admin";
import type { AuditEvent, AuditPage, Scope } from "../api/admin";
import type { Live } from "../api/useView";
import { addressOf, go } from "../app/route";
import { Page } from "../parts/parts";
import { Button, Segmented } from "../ui/controls";
import { EmptyState, Panel, TableRegion } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Reading, timeOf, useRead } from "./parts";

const actors = ["", "member", "worker", "lane", "orchestrator", "system"];

type Filters = { actor: string; kind: string; subject: string };

// AuditPage lists a log's events newest first, at the scope its address names: who did what to which
// record, and when. Selecting a row shows its whole record.
export function AuditView({ live, project, label, scope, actor, kind }: { live: Live; project: string; label: string; scope: Scope; actor: string; kind: string }) {
  const [filters, setFilters] = useState<Filters>({ actor, kind, subject: "" });
  const [typed, setTyped] = useState<Filters>({ actor, kind, subject: "" });
  const [pages, setPages] = useState<number[]>([0]);
  const [open, setOpen] = useState<AuditEvent | null>(null);
  const before = pages[pages.length - 1] ?? 0;
  const read = useRead<AuditPage>(() => readAudit(scope, { ...filters, before, limit: 50 }), `audit:${scope}:${project}:${JSON.stringify(filters)}:${before}`, live);
  const id = useId();
  useEffect(() => setOpen(null), [read.view]);
  const apply = (next: Filters) => {
    setFilters(next);
    setPages([0]);
    const path = addressOf("/audit", { actor: next.actor || undefined, kind: next.kind || undefined });
    go(scope === "project" && project ? `/projects/${encodeURIComponent(project)}${path}` : path, { installation: true, stay: true, replace: true });
  };
  const filtered = filters.actor !== "" || filters.kind !== "" || filters.subject !== "";
  const events = read.view?.events ?? [];
  return (
    <Page
      title="Audit"
      note={scope === "project" ? `Every event in ${label}'s ledger, newest first.` : "The installation's own events: projects, members, workers, and the roster's installation layer."}
      actions={
        <Segmented<Scope>
          label="Log"
          value={scope}
          choices={[
            { value: "installation", label: "Installation" },
            ...(project ? [{ value: "project" as const, label: `Project: ${label}` }] : []),
          ]}
          onChange={(next) => go(next === "installation" ? "/audit" : `/projects/${encodeURIComponent(project)}/audit`, { installation: true })}
        />
      }
    >
      <form
        className="filters-row"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          apply(typed);
        }}
      >
        <Field label="Actor">
          <Select value={typed.actor} onChange={(event) => setTyped({ ...typed, actor: event.target.value })}>
            {actors.map((one) => (
              <option key={one} value={one}>
                {one || "Any actor"}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Act">
          <input className="input mono" autoComplete="off" spellCheck={false} placeholder="project.created…" value={typed.kind} onChange={(event) => setTyped({ ...typed, kind: event.target.value })} />
        </Field>
        <Field label="Subject">
          <input className="input mono" autoComplete="off" spellCheck={false} placeholder="work_order…" value={typed.subject} onChange={(event) => setTyped({ ...typed, subject: event.target.value })} />
        </Field>
        <div className="filters-actions">
          <Button type="submit" variant="primary">
            Filter
          </Button>
          {filtered ? (
            <Button
              onClick={() => {
                const none = { actor: "", kind: "", subject: "" };
                setTyped(none);
                apply(none);
              }}
            >
              Clear filters
            </Button>
          ) : null}
        </div>
      </form>
      <Reading failure={read.failure} ready={read.view !== null}>
        {events.length === 0 ? (
          <div className="panel">
            <EmptyState
              icon="history"
              title={filtered ? "No event matches these filters" : "No event yet"}
              action={
                filtered ? (
                  <Button
                    onClick={() => {
                      const none = { actor: "", kind: "", subject: "" };
                      setTyped(none);
                      apply(none);
                    }}
                  >
                    Clear filters
                  </Button>
                ) : undefined
              }
            >
              {read.view?.scanned ? "The read stopped at its bound before it found one; older events are left to read." : "Events appear here as the ledger records them."}
            </EmptyState>
          </div>
        ) : (
          <div className="stack">
            <Panel flush>
              <TableRegion label="Events">
                <table className="table stack-on-phone">
                  <thead>
                    <tr>
                      <th scope="col" className="r">
                        Revision
                      </th>
                      <th scope="col">Time</th>
                      <th scope="col">Actor</th>
                      <th scope="col">Act</th>
                      <th scope="col">Subject</th>
                    </tr>
                  </thead>
                  <tbody>
                    {events.map((event) => (
                      <tr key={event.rev} aria-selected={open?.rev === event.rev || undefined}>
                        <td data-label="Revision" className="r num">
                          <button type="button" className="link-button mono" aria-expanded={open?.rev === event.rev} aria-controls={id} onClick={() => setOpen(open?.rev === event.rev ? null : event)}>
                            {event.rev}
                          </button>
                        </td>
                        <td data-label="Time" className="num">
                          {timeOf(event.recorded_at)}
                        </td>
                        <td data-label="Actor">
                          {event.actor_kind} <span className="mono small subtle">{event.actor_id}</span>
                        </td>
                        <td data-label="Act" className="mono small">
                          {event.type}
                        </td>
                        <td data-label="Subject">
                          {event.subject_kind} <span className="mono small subtle">{event.subject_id}</span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableRegion>
              <div className="pager">
                <Button small disabled={pages.length < 2} onClick={() => setPages(pages.slice(0, -1))}>
                  Newer
                </Button>
                <span className="small muted">{read.view?.scanned ? "Stopped at the read's bound; older events remain." : ""}</span>
                <Button small disabled={!read.view?.next} onClick={() => read.view && setPages([...pages, read.view.next])}>
                  Older
                </Button>
              </div>
            </Panel>
            <div id={id}>
              {open ? (
                <Panel title={`Event ${open.rev}`} level={3}>
                  <dl className="kv">
                    <dt>Act</dt>
                    <dd className="mono">{open.type}</dd>
                    <dt>Actor</dt>
                    <dd>
                      {open.actor_kind} <span className="mono">{open.actor_id}</span>
                    </dd>
                    <dt>Subject</dt>
                    <dd>
                      {open.subject_kind} <span className="mono">{open.subject_id}</span>
                    </dd>
                    <dt>Recorded</dt>
                    <dd>{timeOf(open.recorded_at)}</dd>
                    <dt>Event ID</dt>
                    <dd className="mono">{open.id}</dd>
                  </dl>
                  <pre className="op event-record">{JSON.stringify(open.data ?? {}, null, 2)}</pre>
                </Panel>
              ) : null}
            </div>
          </div>
        )}
      </Reading>
    </Page>
  );
}
