import { useId } from "react";
import type { ReactNode } from "react";

import type { ProjectLabel } from "../api/types";
import type { IconName } from "./Icon";
import { Icon } from "./Icon";
import { ProjectTag } from "./display";

type DecisionCardProps = {
  // kind is the overline: the kind's icon and name.
  kind: { icon: IconName; name: string };
  project?: ProjectLabel;
  // title says what will happen, as a sentence.
  title: ReactNode;
  // state is the badge beside the title, such as Open.
  state?: ReactNode;
  // provenance says who asked, where, and how long it has waited.
  provenance?: ReactNode;
  // binding names what the decision binds to, and what voids it.
  binding?: ReactNode;
  // actions are the decision's buttons: the safe choice first, the primary last.
  actions?: ReactNode;
  // resolution replaces the actions once the record is decided.
  resolution?: ReactNode;
  className?: string;
  // compact is the card inline in a conversation: the same parts in a narrower frame, under a smaller heading.
  compact?: boolean;
  children?: ReactNode;
};

// DecisionCard is one decision a member makes, in one form everywhere it appears: a kind line, a title
// that says what will happen, who asked, what it changes and why, what it binds to, and its buttons.
export function DecisionCard({ kind, project, title, state, provenance, binding, actions, resolution, className = "", compact = false, children }: DecisionCardProps) {
  const heading = useId();
  const Title = compact ? "h3" : "h2";
  return (
    <article className={`decision${compact ? " decision-compact" : ""}${className ? ` ${className}` : ""}`} aria-labelledby={heading}>
      <header className="decision-head">
        <div className="decision-heading">
          <p className="decision-kind">
            <Icon name={kind.icon} size="sm" />
            <span>{kind.name}</span>
            {project ? (
              <>
                <span aria-hidden="true">·</span>
                <ProjectTag name={project.name} display={project.display_name} />
              </>
            ) : null}
          </p>
          <Title id={heading} className="decision-title">
            {title}
          </Title>
          {provenance ? <p className="decision-provenance">{provenance}</p> : null}
        </div>
        {state ? <div className="decision-state">{state}</div> : null}
      </header>
      {children ? <div className="decision-body">{children}</div> : null}
      {binding || actions || resolution ? (
        <footer className="decision-foot">
          {binding ? <p className="binding">{binding}</p> : <span className="spacer" />}
          {resolution ?? <div className="decide">{actions}</div>}
        </footer>
      ) : null}
    </article>
  );
}

// DecisionSection is one part of a card's body, such as what it changes or why, under a small heading.
export function DecisionSection({ title, children }: { title: string; children: ReactNode }) {
  const heading = useId();
  return (
    <section className="decision-section" aria-labelledby={heading}>
      <h3 id={heading} className="h4">
        {title}
      </h3>
      {children}
    </section>
  );
}
