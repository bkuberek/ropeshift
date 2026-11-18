import { useState } from "react";

import { rerunCI, waitLongerForCI } from "../api/client";
import type { CINeed } from "../api/types";
import { Link } from "../app/route";
import { span } from "../measures/format";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { Fact, Facts, Id, Refusal } from "../ui/display";
import { Field } from "../ui/forms";
import { Icon } from "../ui/Icon";
import { ciKinds } from "./ciWords";
import type { Frame } from "./frame";
import { useAct } from "./useAct";

// forgeLink keeps the address of a run only when a page may follow it. It is the forge's word, so an address is a link
// only when it is https, has a host, and holds no user or password, as the pages the stated risk names are.
function forgeLink(address: string | undefined): string | null {
  if (!address) {
    return null;
  }
  try {
    const url = new URL(address);
    return url.protocol === "https:" && url.host !== "" && url.username === "" && url.password === "" ? url.href : null;
  } catch {
    return null;
  }
}

// ClockFacts says where the head stands against the limit its kind is raised at: how long the checks have run or
// waited for a runner, the limit with what Wait longer added, and the time CI is expected to take.
function ClockFacts({ need }: { need: CINeed }) {
  const watch = need.watch;
  const queue = need.kind === "ci_queue_stalled";
  const expected = watch.history ? `p50 ${span(watch.expected_p50_ms ?? 0)} · p85 ${span(watch.expected_ms ?? 0)}` : "no history";
  return (
    <>
      <Fact name={queue ? "Queued for" : "Running for"}>{span(queue ? watch.queue_ms : watch.elapsed_ms)}</Fact>
      {need.kind === "ci_not_finished" || queue ? (
        <Fact name={queue ? "Queue limit" : "Time limit"}>
          {span(queue ? watch.queue_timeout_ms : watch.timeout_ms)}
          {!queue && watch.extended_ms ? ` (${span(watch.extended_ms)} added by Wait longer)` : ""}
        </Fact>
      ) : null}
      <Fact name="Expected CI time">{expected}</Fact>
      {watch.reruns ? <Fact name="Reruns of this head">{watch.reruns}</Fact> : null}
    </>
  );
}

// CICard is CI that asks a person (ADR-0037): CI that has not finished, a run no runner picked up, and what the
// classification found that no agent can act on. It offers what its kind has: Wait longer, which adds one more p85
// or the time named, Rerun, which is recorded and starts the head's CI clock again, and the run's page on the forge.
// None of them starts an agent.
export function CICard({ need, after, frame }: { need: CINeed; after: () => Promise<void>; frame: Frame }) {
  const { act, busy, failure } = useAct(after);
  const [by, setBy] = useState("");
  const words = ciKinds[need.kind];
  const id = need.work_order.id;
  const run = forgeLink(need.run_url);
  // The link is the one place an address from the forge is followed: the run of CI that asks a person.
  const waits = need.actions.includes("ci_wait");
  const reruns = need.actions.includes("ci_rerun");
  return (
    <DecisionCard
      kind={{ icon: words.icon, name: words.name }}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      binding={need.watch.head ? `Bound to head ${need.watch.head.slice(0, 12)}. A new push ends the wait.` : undefined}
      actions={
        <>
          {run ? (
            <a className="btn" href={run} target="_blank" rel="noreferrer noopener">
              Open the run
              <span className="visually-hidden"> (opens in a new tab)</span>
              <Icon name="external" size="sm" />
            </a>
          ) : null}
          {reruns ? (
            <Button disabled={busy} onClick={() => void act(() => rerunCI(id))}>
              Rerun
            </Button>
          ) : null}
          {waits ? (
            <Button variant="primary" loading={busy} disabled={busy} onClick={() => void act(() => waitLongerForCI(id, by.trim()))}>
              Wait longer
            </Button>
          ) : null}
        </>
      }
    >
      <p>{words.lead}</p>
      <Facts>
        <Fact name="Work order">
          <Link to={`/work-order?id=${encodeURIComponent(id)}`}>{need.work_order.title || id}</Link>
        </Fact>
        <Fact name="Pull request">{`#${need.number}`}</Fact>
        <ClockFacts need={need} />
        {need.finding?.check ? <Fact name="Check">{need.finding.check}</Fact> : null}
        {need.finding?.conclusion ? (
          <Fact name="Conclusion" mono>
            {need.finding.conclusion}
          </Fact>
        ) : null}
        {need.finding?.base_commit ? (
          <Fact name="Base commit">
            <Id value={need.finding.base_commit} />
          </Fact>
        ) : null}
      </Facts>
      {waits ? (
        <Field label="Wait for (optional)" help="Such as 45m or 2h. Left empty, it adds one more p85 of this repository's CI, or the no-history p85 with none.">
          <input className="input mono field-narrow" autoComplete="off" spellCheck={false} value={by} disabled={busy} onChange={(event) => setBy(event.target.value)} />
        </Field>
      ) : null}
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
