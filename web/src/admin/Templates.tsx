import { useEffect, useId, useMemo, useRef } from "react";

import type { Template, TemplateList } from "../api/admin";
import { listTemplates } from "../api/admin";
import type { Live } from "../api/useView";
import { addressOf, go, Link, useArrivalFocus } from "../app/route";
import { Page } from "../parts/parts";
import { Button } from "../ui/controls";
import { Badge, Banner, EmptyState, SkeletonRows } from "../ui/display";
import { useNarrow } from "../ui/narrow";
import { useToast } from "../ui/overlays";
import { useRead } from "./parts";

// The folders of templates in the order the list shows them, and the name of the group the top-level files fall under.
const folders = ["workspace", "monorepo", "repository", "user", "policies"];
const shared = "Shared";

type Group = { name: string; templates: Template[] };

// groupsOf puts each template under the first segment of its name, in the order of the folders, with the
// top-level files last under Shared. A group with no template is left out. A folder the list does not name
// follows the named ones, so a template added to a new folder is listed.
export function groupsOf(templates: Template[]): Group[] {
  const named = new Map<string, Template[]>();
  for (const template of templates) {
    const [first, ...rest] = template.name.split("/");
    const group = rest.length === 0 ? shared : (first ?? shared);
    named.set(group, [...(named.get(group) ?? []), template]);
  }
  const order = [...folders, ...[...named.keys()].filter((name) => name !== shared && !folders.includes(name)), shared];
  return order.flatMap((name) => (named.has(name) ? [{ name, templates: named.get(name) ?? [] }] : []));
}

const textOf = (name: string) => `ropeshift template show ${name}`;

type BodyProps = { view: TemplateList; stale: string; selected: string };

function TemplatesBody({ view, stale, selected }: BodyProps) {
  const narrow = useNarrow();
  const toast = useToast();
  const title = useRef<HTMLHeadingElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const titleId = useId();
  useArrivalFocus(title);
  const groups = useMemo(() => groupsOf(view.templates), [view.templates]);
  const ordered = groups.flatMap((group) => group.templates);
  // With no name in the address, the first template shown is selected, except on a phone, where the list stands
  // alone until a template is chosen.
  const current = ordered.find((template) => template.name === selected) ?? (selected ? undefined : narrow ? undefined : ordered[0]);

  // Choosing a template on a phone pushes its page, and focus goes to its heading.
  const was = useRef(selected);
  useEffect(() => {
    if (narrow && selected && was.current !== selected) {
      heading.current?.focus();
    }
    was.current = selected;
  }, [selected, narrow]);

  const back = () => {
    const left = selected;
    go(addressOf("/templates"), { stay: true });
    requestAnimationFrame(() => document.getElementById(`template-${left}`)?.focus());
  };

  const copy = (template: Template) =>
    void navigator.clipboard?.writeText(template.content).then(
      () => toast("success", `Copied ${template.name}.`),
      () => toast("warning", "The browser did not let this page copy. Select the text and copy it."),
    );

  return (
    <div className={`split split-templates${selected ? " show-detail" : ""}`}>
      <section className="split-list" aria-labelledby={titleId}>
        <div className="split-list-head">
          <div className="split-title">
            <h1 id={titleId} ref={title} tabIndex={-1}>
              Templates
            </h1>
            <p className="subtle small">{view.templates.length} templates</p>
          </div>
        </div>
        <div className="split-list-body">
          {stale ? (
            <div className="convo-banner">
              <Banner tone="danger">{`What this page shows is not current. ${stale}`}</Banner>
            </div>
          ) : null}
          {groups.map((group) => (
            <div key={group.name}>
              <p className="overline convo-list-group" id={`${titleId}-${group.name}`}>
                {group.name}
              </p>
              <ul aria-labelledby={`${titleId}-${group.name}`}>
                {group.templates.map((template) => (
                  <li key={template.name}>
                    <Link
                      id={`template-${template.name}`}
                      to={addressOf("/templates", { name: template.name })}
                      stay
                      className="item template-item"
                      aria-current={current?.name === template.name ? "true" : undefined}
                    >
                      <span className="item-title template-name">{template.name}</span>
                      <span className="item-meta template-meta">
                        {template.shapes.map((shape) => (
                          <Badge key={shape} glyph="ring">
                            {shape}
                          </Badge>
                        ))}
                        {template.writes_to ? (
                          <>
                            <span className="visually-hidden">Written to </span>
                            <code className="template-path">{template.writes_to}</code>
                          </>
                        ) : (
                          <span className="subtle">Not written by a command</span>
                        )}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </section>
      <section className="split-detail" aria-label="The selected template">
        <Button small icon="chevron-left" className="back-to-list" onClick={back}>
          All templates
        </Button>
        {current ? (
          <div className="stack">
            <div className="row template-head">
              <h2 ref={heading} tabIndex={-1} className="template-heading">
                {current.name}
              </h2>
              <span className="spacer" />
              <Button small icon="copy" onClick={() => copy(current)}>
                Copy
              </Button>
            </div>
            <p className="small muted">
              Print this text with <code>{textOf(current.name)}</code>
            </p>
            <div className="template-text" role="region" aria-label={`Text of ${current.name}`} tabIndex={0}>
              <pre>{current.content}</pre>
            </div>
          </div>
        ) : (
          <EmptyState icon="doc" title="No template with that name">
            The list names every template this build holds. Choose one from it.
          </EmptyState>
        )}
      </section>
    </div>
  );
}

// TemplatesPage is the Templates view (FR-TPL-5, FR-UI-7): every template the daemon's binary embeds, beside the
// selected template's text, which is what `ropeshift template show` prints and `ropeshift new` writes. It is read-only.
export function TemplatesPage({ live, selected }: { live: Live; selected: string }) {
  const read = useRead<TemplateList>(() => listTemplates(), "templates", live);
  if (read.view) {
    return <TemplatesBody view={read.view} stale={read.failure} selected={selected} />;
  }
  return (
    <Page title="Templates">
      {read.failure ? (
        <Banner
          tone="danger"
          action={
            <Button small onClick={read.again}>
              Try again
            </Button>
          }
        >
          {read.failure}
        </Banner>
      ) : (
        <div className="panel">
          <SkeletonRows rows={6} />
        </div>
      )}
    </Page>
  );
}
