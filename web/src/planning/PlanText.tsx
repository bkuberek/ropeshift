import type { Criterion } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Panel } from "../ui/display";
import { DocumentText } from "./DocumentText";

// What a member reads of a plan to know what it says: the goal it pursues, the text of its file, and the contracts
// it holds. Every text here comes from a repository, so it is read as DocumentText reads a document: as text.

export type GoalText = { title: string; statement?: string; success_measures?: string[] };
export type ContractText = { id: string; title: string; statement?: string; criteria?: Criterion[] };

export const outside = "This text comes from a repository, as anyone with a commit wrote it. It is shown as text: nothing in it runs, loads, or links.";

export function GoalPanel({ goal }: { goal?: GoalText }) {
  return (
    <Panel title="Goal">
      {goal ? (
        <div className="stack">
          <p>
            <strong>{goal.title}</strong>
          </p>
          {goal.statement?.trim() ? <DocumentText body={goal.statement} title={goal.title} /> : null}
          {goal.success_measures?.length ? (
            <ul aria-label="Success measures">
              {goal.success_measures.map((measure, index) => (
                <li key={index}>{measure}</li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : (
        <p className="muted">This plan names no goal.</p>
      )}
    </Panel>
  );
}

export function PlanTextPanel({ title, body, cut = false }: { title: string; body?: string; cut?: boolean }) {
  return (
    <Panel title="Plan text">
      {body?.trim() ? (
        <div className="stack">
          <p className="outside" role="note">
            {outside}
          </p>
          <DocumentText body={body} title={title} />
          {cut ? <p className="small muted">Only the start of the text is kept. The plan's file holds the rest.</p> : null}
        </div>
      ) : (
        <p className="muted">This version recorded no plan text.</p>
      )}
    </Panel>
  );
}

export function ContractsPanel({ contracts, plan }: { contracts: ContractText[]; plan?: string }) {
  return (
    <Panel title="Contracts in this version" actions={plan ? <Link to={addressOf("/plans", { plan })}>Open the plan</Link> : undefined}>
      {contracts.length ? (
        <ul className="stack list-plain">
          {contracts.map((contract) => (
            <li key={contract.id} className="stack">
              <strong>{contract.title}</strong>
              {contract.statement?.trim() ? <DocumentText body={contract.statement} title={contract.title} /> : null}
              {contract.criteria?.length ? (
                <ul aria-label={`Criteria of ${contract.title}`}>
                  {contract.criteria.map((criterion) => (
                    <li key={criterion.id}>{criterion.text}</li>
                  ))}
                </ul>
              ) : (
                <p className="small muted">No criteria.</p>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">This version holds no contract.</p>
      )}
    </Panel>
  );
}

// WhatThePlanSays is the three panels in the order a member reads them.
export function WhatThePlanSays({ title, goal, body, cut, contracts, plan }: { title: string; goal?: GoalText; body?: string; cut?: boolean; contracts: ContractText[]; plan?: string }) {
  return (
    <>
      <GoalPanel goal={goal} />
      <PlanTextPanel title={title} body={body} cut={cut} />
      <ContractsPanel contracts={contracts} plan={plan} />
    </>
  );
}
