import { words } from "../parts/parts";

// CategoryChoices is the categories of a project as checkboxes, in the order the API lists them. It is empty
// while they are read, and says so to a screen reader.
export function CategoryChoices({ names, chosen, onChange, disabled = false }: { names: string[] | null; chosen: string[]; onChange: (chosen: string[]) => void; disabled?: boolean }) {
  return (
    <fieldset className="field" aria-busy={names === null || undefined}>
      <legend className="label">Categories</legend>
      {names === null ? <p className="small muted">Reading the categories.</p> : null}
      <div className="check-grid">
        {(names ?? []).map((name) => (
          <label key={name} className="check-row">
            <input
              type="checkbox"
              disabled={disabled}
              checked={chosen.includes(name)}
              onChange={(event) => onChange((names ?? []).filter((one) => (one === name ? event.target.checked : chosen.includes(one))))}
            />
            <span>{words(name)}</span>
          </label>
        ))}
      </div>
      <p className="help">What the question or decision is about, so a member filters to what they can answer. Leave them all clear for none.</p>
    </fieldset>
  );
}
