import type { PracticesView } from "../api/practices";
import { plainText } from "../admin/untrustedText";
import { Badge, LookBadge } from "../ui/display";
import type { Check } from "./useCheck";
import { checking, countsOf, expiredWords, severities, severityLook } from "./logic";

// OpenCounts are a repository's open findings, one badge for each severity that has any: a word and a number, so a
// severity never reads by its color alone.
export function OpenCounts({ view }: { view: PracticesView }) {
  const counts = countsOf(view);
  if (counts.open === 0) {
    return (
      <Badge tone="success" glyph="check">
        No open findings
      </Badge>
    );
  }
  return (
    <>
      {severities
        .filter((severity) => counts[severity] > 0)
        .map((severity) => (
          <LookBadge key={severity} look={{ ...severityLook[severity], label: `${severity} ${counts[severity]}` }} />
        ))}
    </>
  );
}

// CheckLine says what is happening to a repository's check: a request a worker is carrying out, a request that ran out
// before its worker reported, or why none was made.
export function CheckLine({ view, check }: { view: PracticesView; check: Check }) {
  const request = view.request;
  const reason = check.reasons[view.repository];
  if (checking(request, check.ended)) {
    return (
      <span className="small muted" role="status">
        Checking on {plainText(request.worker_name || request.worker)}
      </span>
    );
  }
  if (check.expired[view.repository]) {
    return (
      <span className="small muted" role="status">
        {expiredWords}
      </span>
    );
  }
  return reason ? <span className="small muted">{plainText(reason)}</span> : null;
}
