import { plainText } from "../admin/untrustedText";
import type { CreationOutcome, CreationStep, RepositoryOutcome } from "../api/types";
import type { StateLook } from "../floor/states";
import { Button } from "../ui/controls";
import { Badge } from "../ui/display";
import { useCopy } from "../ui/useCopy";

// What a worker or the forge printed, a path, a name, or a URL reaches the page as text: the escape sequences, control
// characters, and bidirectional marks are dropped, and a line break is a space, so a command a member copies is one line.
export function plainLine(text: string | undefined): string {
  return plainText(text).replace(/\s*\n\s*/g, " ").replace(/\t/g, " ").trim();
}

// registrationStates are what became of a repository a manifest lists, and in a preview what would. A creation that
// approving would make reads Will create before the decision and Created after it, and one that stopped at a step is
// Not created.
export const registrationStates: Record<string, StateLook> = {
  registered: { label: "Registered", tone: "success", glyph: "check" },
  would: { label: "Will register", tone: "active", glyph: "half" },
  known: { label: "Already registered", tone: "neutral", glyph: "dot" },
  refused: { label: "Refused", tone: "danger", glyph: "cross" },
  not_attempted: { label: "Not attempted", tone: "neutral", glyph: "dot" },
  planned: { label: "Planned", tone: "neutral", glyph: "ring" },
  will_create: { label: "Will create", tone: "active", glyph: "half" },
  created: { label: "Created", tone: "success", glyph: "check" },
  create_failed: { label: "Not created", tone: "danger", glyph: "cross" },
  skipped: { label: "Skipped", tone: "neutral", glyph: "slash" },
};

// stateOf is the row of registrationStates an entry reads as, before or after approval.
export function stateOf(one: RepositoryOutcome, preview: boolean | undefined): string {
  if (preview && one.outcome === "registered") {
    return "would";
  }
  if (preview && one.outcome === "created") {
    return "will_create";
  }
  return one.outcome;
}

// forgeWords names a forge as a member reads it.
const forgeWords = (forge: string) => (forge === "github" ? "GitHub" : plainLine(forge));

// repositoryOn is the forge repository a creation makes: the owner and the name.
export const repositoryOn = (creation: CreationOutcome) => `${plainLine(creation.owner)}/${plainLine(creation.forge_name)}`;

// creationLine says where a creation makes its repository: "GitHub acme/api, private, branch main, as octo".
export function creationLine(creation: CreationOutcome): string {
  return [
    `${forgeWords(creation.forge)} ${repositoryOn(creation)}`,
    creation.visibility === "public" ? "public" : "private",
    creation.default_branch ? `branch ${plainLine(creation.default_branch)}` : "",
    creation.login ? `as ${plainLine(creation.login)}` : "",
  ]
    .filter(Boolean)
    .join(", ");
}

// PublicWarning is the badge a public repository carries, so a member reads that anyone can read it.
export function PublicWarning({ creation }: { creation: CreationOutcome }) {
  return creation.visibility === "public" ? (
    <Badge tone="warning" glyph="triangle">
      Public: anyone can read it
    </Badge>
  ) : null;
}

// createsLine is what approving does about the repositories it creates, as the decision card says it: "creates 1
// repository on GitHub, acme/api, private, and registers it". It is empty when approving creates none.
export function createsLine(entries: RepositoryOutcome[] | undefined): string {
  const made = (entries ?? []).filter((one) => one.outcome === "created" && one.creation);
  const [only] = made;
  if (!only?.creation) {
    return "";
  }
  if (made.length === 1) {
    return `creates 1 repository on ${forgeWords(only.creation.forge)}, ${repositoryOn(only.creation)}, ${only.creation.visibility === "public" ? "public" : "private"}, and registers it`;
  }
  const named = made.map((one) => (one.creation ? `${repositoryOn(one.creation)} (${one.creation.visibility === "public" ? "public" : "private"})` : ""));
  const last = named.pop();
  return `creates ${made.length} repositories on ${forgeWords(only.creation.forge)}, ${named.join(", ")} and ${last}, and registers them`;
}

// A step's name as a member reads it, and its outcome as a word.
const stepNames: Record<string, string> = { create: "Create", origin: "Origin", push: "Push" };
const outcomeWords: Record<string, string> = { done: "Done", failed: "Failed", skipped: "Skipped" };

export type StepRow = { name: string; outcome: string; detail: string };

// stepRows lists what a creation did, in the order it does it: the files written and the first commit, which the
// creation's record holds as a commit, then the create, origin, and push steps. A step the creation never reached is not
// reached. A creation with no record of anything lists none.
export function stepRows(creation: CreationOutcome | undefined): StepRow[] {
  if (!creation) {
    return [];
  }
  const steps: CreationStep[] = creation.steps ?? [];
  const rows: StepRow[] = [];
  if (creation.commit || creation.commit_error || steps.length > 0) {
    rows.push({ name: "Files written", outcome: "Done", detail: "" });
  }
  if (creation.commit) {
    rows.push({ name: "First commit", outcome: "Done", detail: plainLine(creation.commit).slice(0, 7) });
  } else if (creation.commit_error) {
    rows.push({ name: "First commit", outcome: "Failed", detail: plainLine(creation.commit_error) });
  }
  if (steps.length > 0) {
    for (const wanted of ["create", "origin", "push"]) {
      const found = steps.find((one) => one.step === wanted);
      rows.push({ name: stepNames[wanted] ?? wanted, outcome: found ? (outcomeWords[found.outcome] ?? plainLine(found.outcome)) : "Not reached", detail: plainLine(found?.detail) });
    }
    for (const other of steps.filter((one) => !(one.step in stepNames))) {
      rows.push({ name: plainLine(other.step), outcome: outcomeWords[other.outcome] ?? plainLine(other.outcome), detail: plainLine(other.detail) });
    }
  }
  return rows;
}

// StepList lists each step of a creation with its outcome as a word and its detail, so no outcome reads by color alone.
// On a phone each step stacks, its outcome under its name.
export function StepList({ creation, label }: { creation: CreationOutcome | undefined; label: string }) {
  const rows = stepRows(creation);
  if (rows.length === 0) {
    return null;
  }
  return (
    <ol className="creation-steps" aria-label={label}>
      {rows.map((row) => (
        <li key={row.name}>
          <span className="creation-step-name">{row.name}</span>
          <span className="creation-step-outcome">
            <span className="visually-hidden">: </span>
            {row.outcome}
          </span>
          {row.detail ? <span className="creation-step-detail muted small">{row.detail}</span> : null}
        </li>
      ))}
    </ol>
  );
}

// CommandLine is a command in mono with Copy, which copies the same line the page shows.
export function CommandLine({ command }: { command: string }) {
  const copy = useCopy();
  const line = plainLine(command);
  return (
    <span className="row">
      <code className="mono small">{line}</code>
      <span className="spacer" />
      <Button small icon="copy" aria-label={`Copy ${line}`} onClick={() => copy(line, line)}>
        Copy
      </Button>
    </span>
  );
}
