import type { DeliveryHistory, HistoryGroup } from "../api/measures";
import { Badge } from "../ui/display";
import { historyWords } from "./historyWords";

function Section({ title, groups, repositories }: { title: string; groups: HistoryGroup[]; repositories: Record<string, string> }) {
  return (
    <section data-section={title} aria-label={title}>
      <h4 className="overline outline-title">{title}</h4>
      <ul className="history-groups">
        {groups.map((group) => {
          const words = historyWords(group, repositories);
          return (
            <li key={words.label} data-group={words.label}>
              <p className="history-label">
                {group.confidence === "low" ? <Badge tone="warning" glyph="triangle">Low confidence</Badge> : null} <strong>{words.label}</strong> <span>{words.finished}</span>
              </p>
              <p className="small history-figures">
                <span>{words.delivery}</span> <span>{words.lane}</span> <span>{words.cost}</span> <span>{words.rounds}</span> <span>{words.change}</span>
              </p>
              <p className="small muted">{words.explanation}</p>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

// DeliveryHistoryView is the delivery history the planning session's pack holds, labeled as what the session sees:
// the project's finished work by kind and size, then by kind, size, capability class, and repository, each group with
// its count, delivery time, lane time, and cost at p50 and p85, its mean rounds, its median change size, and, under
// the minimum, the Low confidence badge and the explanation the session receives. It shows what the API returns.
export function DeliveryHistoryView({ view }: { view: DeliveryHistory }) {
  const empty = view.by_kind_size.length === 0 && view.by_kind_size_class_repository.length === 0;
  return (
    <div className="stack-8 history">
      <h3 className="overline outline-title">What the planning session sees</h3>
      <p className="small muted">
        The work this project has finished, from its own delivery records. Each group reads its {view.recent_samples} most recent work orders that finished done, and a group under {view.min_samples} is labeled low confidence.
        These figures are history and no promise.
      </p>
      {empty ? (
        <p className="muted">No finished work yet. Work orders will have no estimate until comparable work finishes.</p>
      ) : (
        <>
          <Section title="By kind and size" groups={view.by_kind_size} repositories={view.repositories} />
          <Section title="By kind, size, capability class, and repository" groups={view.by_kind_size_class_repository} repositories={view.repositories} />
        </>
      )}
    </div>
  );
}
