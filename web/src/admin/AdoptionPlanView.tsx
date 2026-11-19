import type { AdoptionPlan, LayoutClone, PlannedFile, PlannedRepository, PracticeFinding, PracticeGuide, PracticeSeverity } from "../api/admin";
import type { StateLook } from "../floor/states";
import { Digest, LookBadge, TableRegion } from "../ui/display";
import { offersRestructure, shapeSummary } from "./adoption";

// How a clone was found, and what it holds, as `ropeshift adopt --dry-run` says them.
const foundWhere = { here: "where adopt runs", ignored: "in a folder the clone ignores", path: "named by a path" } as const;

function holds(clone: LayoutClone): string {
  return [clone.plan ? "plan content" : "", `${clone.documents} Markdown ${clone.documents === 1 ? "document" : "documents"}`, clone.instructions ? "instruction file" : ""].filter(Boolean).join(", ");
}

function outcomeOf(row: PlannedRepository, plan: AdoptionPlan): StateLook {
  switch (row.outcome) {
    case "registered":
      return { label: "Will register", tone: "success", glyph: "dot" };
    case "known":
      return { label: "Already in this project", tone: "neutral", glyph: "ring" };
    case "not_registered":
      return { label: `Not registered: its history is imported into ${plan.planning.name} by the restructuring`, tone: "neutral", glyph: "ring" };
    default:
      return { label: `Refused: ${row.reason ?? "no reason given"}`, tone: "danger", glyph: "cross" };
  }
}

// The look of each severity, in the order a list of findings reads: the most severe first.
const severities: { severity: PracticeSeverity; look: StateLook }[] = [
  { severity: "high", look: { label: "high", tone: "danger", glyph: "cross" } },
  { severity: "medium", look: { label: "medium", tone: "warning", glyph: "triangle" } },
  { severity: "low", look: { label: "low", tone: "neutral", glyph: "ring" } },
];

function FindingRow({ finding, guide }: { finding: PracticeFinding; guide?: PracticeGuide }) {
  const look = severities.find((one) => one.severity === finding.severity)?.look ?? severities[2]!.look;
  return (
    <li>
      <LookBadge look={look} /> <code>{finding.id}</code>
      <div>{finding.detail}</div>
      {finding.path ? (
        <div className="small muted">
          At <code>{finding.path}</code>
          {finding.line ? `:${finding.line}` : ""} ({finding.rule}, commit <code>{finding.commit}</code>)
        </div>
      ) : null}
      {guide ? <div className="small muted">Fix: {guide.fix}</div> : null}
      {guide?.command ? (
        <div className="small muted">
          Template: <code>{guide.command}</code>
        </div>
      ) : null}
    </li>
  );
}

// RepositoryPractices is one repository's open findings in a row that opens to list each with its fix, or what says why the
// check could not read the clone. A dry run acknowledges nothing, so every finding is open.
function RepositoryPractices({ row, guides }: { row: PlannedRepository; guides: Record<string, PracticeGuide> }) {
  if (!row.practices) {
    return <span className="small muted">{row.practices_note}</span>;
  }
  const findings = row.practices.findings ?? [];
  if (findings.length === 0) {
    return <span className="small muted">No findings</span>;
  }
  return (
    <details className="disclosure">
      <summary>
        {findings.length} {findings.length === 1 ? "finding" : "findings"}
        {severities.map(({ severity, look }) => {
          const count = findings.filter((finding) => finding.severity === severity).length;
          return count > 0 ? (
            <LookBadge key={severity} look={{ ...look, label: `${count} ${look.label}` }} />
          ) : null;
        })}
      </summary>
      <ul className="plan-notes" aria-label={`Findings of ${row.name ?? row.path}`}>
        {severities.flatMap(({ severity }) =>
          findings
            .filter((finding) => finding.severity === severity)
            .map((finding) => <FindingRow key={finding.id} finding={finding} guide={guides[finding.practice]} />),
        )}
      </ul>
    </details>
  );
}

// PracticesTable lists each repository the dry run read the practices of, one row each, closed until a member opens it
// (FR-PRAC-6). A plan that asked for none has no table.
function PracticesTable({ plan }: { plan: AdoptionPlan }) {
  const rows = plan.repositories.filter((row) => row.practices || row.practices_note);
  if (rows.length === 0) {
    return null;
  }
  return (
    <TableRegion label="Practices found">
      <table className="table stack-on-phone dry-run">
        <thead>
          <tr>
            <th scope="col">Clone</th>
            <th scope="col">Findings</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.path}>
              <td data-label="Clone" className="wrap">
                <strong>{row.name ?? row.path}</strong>
              </td>
              <td data-label="Findings" className="wrap">
                <RepositoryPractices row={row} guides={plan.guides ?? {}} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

// PlanSummary is the plan as the CLI's dry run prints it: the layout, the shape, where the plan lives, every repository
// with what registering would do with it, the findings of each repository's practices, and the notes of what the
// detection could not settle.
export function PlanSummary({ plan }: { plan: AdoptionPlan }) {
  const clones = new Map(plan.layout.clones.map((clone) => [clone.path, clone]));
  return (
    <>
      <dl className="kv">
        <dt>Layout</dt>
        <dd>
          <code>{plan.layout.root}</code>
        </dd>
        <dt>Shape</dt>
        <dd>
          <strong>{plan.shape}</strong> <span className="muted">{shapeSummary(plan.shape)}</span>
        </dd>
        <dt>Plan lives in</dt>
        <dd>{plan.planning.undecided ? <>Undecided, {plan.planning.why}</> : <><strong>{plan.planning.name}</strong>, {plan.planning.why}</>}</dd>
        {plan.design ? (
          <>
            <dt>Design repository</dt>
            <dd>
              <strong>{plan.design.name}</strong>, {plan.design.why}
            </dd>
          </>
        ) : null}
      </dl>
      <TableRegion label="Clones found">
        <table className="table stack-on-phone dry-run">
          <thead>
            <tr>
              <th scope="col">Clone</th>
              <th scope="col">Holds</th>
              <th scope="col">Result</th>
            </tr>
          </thead>
          <tbody>
            {plan.repositories.map((row) => {
              const clone = clones.get(row.path);
              return (
                <tr key={row.path}>
                  <td data-label="Clone" className="wrap">
                    {row.name ? <strong>{row.name}</strong> : null}
                    {row.name ? <br /> : null}
                    <code>{row.path}</code>
                    {clone ? <span className="small muted">{` ${foundWhere[clone.where]}`}</span> : null}
                  </td>
                  <td data-label="Holds" className="wrap">
                    {clone ? holds(clone) : <span className="subtle">none</span>}
                  </td>
                  <td data-label="Result" className="wrap">
                    <LookBadge look={outcomeOf(row, plan)} />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </TableRegion>
      <PracticesTable plan={plan} />
      {(plan.notes ?? []).length > 0 ? (
        <ul className="small muted plan-notes" aria-label="Notes">
          {(plan.notes ?? []).map((note) => (
            <li key={note}>{note}</li>
          ))}
        </ul>
      ) : null}
      <p className="small muted">
        Digest <Digest value={plan.digest} length={16} />. A dry run records nothing.
      </p>
    </>
  );
}

// FileTable lists files a restructuring adds, each with what it does to the file and what it holds.
function FileTable({ label, files }: { label: string; files: PlannedFile[] }) {
  return (
    <TableRegion label={label}>
      <table className="table stack-on-phone dry-run">
        <thead>
          <tr>
            <th scope="col">Change</th>
            <th scope="col">Path</th>
            <th scope="col">What it holds</th>
          </tr>
        </thead>
        <tbody>
          {files.map((file) => (
            <tr key={`${file.action}:${file.path}`}>
              <td data-label="Change">{file.action === "add" ? "Add" : (file.lines?.length ?? 1) > 1 ? "Append lines to" : "Append a line to"}</td>
              <td data-label="Path" className="wrap">
                <code>{file.path}</code>
              </td>
              <td data-label="What it holds" className="wrap">
                {file.detail}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

type ChoiceProps = {
  plan: AdoptionPlan;
  chosen: boolean;
  busy: boolean;
  // refusal says why the choice cannot be made: the repository the plan lives in was refused, or none is settled.
  refusal: string;
  onChoose: (chosen: boolean) => void;
  onLeaveOut: (tree: string, leave: boolean) => void;
};

// RestructureChoice lists everything restructuring would do in the plan's shape, and takes the member's choice to do
// it. A restructuring happens only when it is chosen (FR-ADOPT-12), and a code tree left out recomputes the plan.
export function RestructureChoice({ plan, chosen, busy, refusal, onChoose, onLeaveOut }: ChoiceProps) {
  if (!offersRestructure(plan)) {
    return null;
  }
  const added = (plan.files ?? []).filter((file) => !file.template);
  const lacking = (plan.files ?? []).filter((file) => file.template);
  const takes = plan.takes ?? [];
  const collisions = plan.collisions ?? [];
  const trees = plan.code_trees ?? [];
  const moves = plan.moves ?? [];
  const steps = plan.steps ?? [];
  const makefileLeftOut = plan.makefile_left_out ?? [];
  const nothing = added.length + lacking.length + takes.length + collisions.length + moves.length + steps.length === 0;
  return (
    <fieldset className="field plan-choice" aria-describedby="restructure-help">
      <legend className="label">Restructure</legend>
      <p className="help" id="restructure-help">
        Adopting does not restructure unless you choose it here. Everything below is committed to the adoption branch, which merges only when you approve the plan.
      </p>
      <label className="check-row">
        <input type="checkbox" checked={chosen} disabled={Boolean(refusal) || busy} onChange={(event) => onChoose(event.target.checked)} />
        <span>Restructure the planning repository as listed</span>
      </label>
      {refusal ? <p className="small muted">{refusal}</p> : null}
      {nothing ? <p className="small muted">The planning repository already holds everything a restructuring adds, so there is nothing to list.</p> : null}
      {added.length > 0 ? <FileTable label="Files added" files={added} /> : null}
      {lacking.length > 0 ? <FileTable label="Files the layout lacks" files={lacking} /> : null}
      {makefileLeftOut.length > 0 ? (
        <>
          <p className="help">The manifest still lists each repository the Makefile leaves out.</p>
          <ul className="small plan-notes" aria-label="Left out of the Makefile">
            {makefileLeftOut.map((one) => (
              <li key={one.path}>
                <strong>{one.name}</strong> (<code>{one.path}</code>): {one.reason}
              </li>
            ))}
          </ul>
        </>
      ) : null}
      {takes.length > 0 ? (
        <TableRegion label={`Paths taken from ${plan.design?.name ?? "the design repository"}`} className="plan-scroll">
          <table className="table dry-run">
            <thead>
              <tr>
                <th scope="col">Path taken from {plan.design?.name ?? "the design repository"}</th>
              </tr>
            </thead>
            <tbody>
              {takes.map((path) => (
                <tr key={path}>
                  <td className="wrap">
                    <code>{path}</code>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableRegion>
      ) : null}
      {collisions.length > 0 ? (
        <TableRegion label="Collisions">
          <table className="table stack-on-phone dry-run">
            <thead>
              <tr>
                <th scope="col">Path both repositories hold</th>
                <th scope="col">Version kept</th>
              </tr>
            </thead>
            <tbody>
              {collisions.map((one) => (
                <tr key={one.path}>
                  <td data-label="Path both repositories hold" className="wrap">
                    <code>{one.path}</code>
                  </td>
                  <td data-label="Version kept">{one.kept === "union" ? "Both, joined" : "The workspace's"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableRegion>
      ) : null}
      {trees.length > 0 ? (
        <fieldset className="field">
          <legend className="label">Code trees of {plan.design?.name ?? "the design repository"}</legend>
          <p className="help">A tree left out is removed in the merge commit. Checking one reads the plan again.</p>
          {trees.map((tree) => (
            <label key={tree.path} className="check-row">
              <input type="checkbox" checked={Boolean(tree.left_out)} disabled={Boolean(tree.whole) || busy} onChange={(event) => onLeaveOut(tree.path, event.target.checked)} />
              <span>
                Leave out <code>{tree.path}</code> <span className="muted">({tree.build_file})</span>
                {tree.whole ? <span className="muted">, the whole repository, which cannot be left out</span> : null}
              </span>
            </label>
          ))}
        </fieldset>
      ) : null}
      {moves.length > 0 ? (
        <div className="stack plan-moves">
          <p className="help">Ropeshift never moves a clone. Run these yourself, in this order, when you want the layout.</p>
          {moves.map((move) => (
            <div key={move.name} className="field">
              <span className="label">
                Move {move.name}: <code>{move.from}</code> to <code>{move.to}</code>
              </span>
              <pre className="code-block" role="region" aria-label={`Commands that move ${move.name}`} tabIndex={0}>
                <code>{move.commands.join("\n")}</code>
              </pre>
            </div>
          ))}
        </div>
      ) : null}
      {steps.length > 0 ? (
        <div className="stack plan-moves">
          <p className="help">Ropeshift writes to no clone but the planning repository's. Run these yourself in each code repository that holds no instruction file.</p>
          {steps.map((step) => (
            <div key={step.path} className="field">
              <span className="label">
                Instructions for {step.name}: <code>{step.path}</code>
              </span>
              <pre className="code-block" role="region" aria-label={`Commands that write the instructions of ${step.name}`} tabIndex={0}>
                <code>{step.commands.join("\n")}</code>
              </pre>
            </div>
          ))}
        </div>
      ) : null}
    </fieldset>
  );
}
