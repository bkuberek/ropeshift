import { useId } from "react";

import { Field } from "../ui/forms";
import { retriedAfterFailure } from "./recoverSpecs";

// RetryNote is a retry's note: what the last lane's end recorded, quoted above the field and marked as the lane's,
// and the member's own note, which starts empty and may stay so. The lane's words render as text whatever they
// hold, and only the member's typing reaches the field.
export function RetryNote({
  cause,
  label,
  note,
  disabled,
  autoFocus,
  onChange,
}: {
  cause?: string;
  label: string;
  note: string;
  disabled?: boolean;
  autoFocus?: boolean;
  onChange: (note: string) => void;
}) {
  const caption = useId();
  const recorded = cause?.trim();
  return (
    <>
      {recorded ? (
        <figure className="recorded-cause" aria-labelledby={caption}>
          <figcaption id={caption} className="small muted">
            The lane recorded
          </figcaption>
          <blockquote className="small">{recorded}</blockquote>
        </figure>
      ) : null}
      <Field label={`${label} (optional)`} help={`Leave it empty and the retry says: ${retriedAfterFailure}`}>
        <textarea className="textarea" rows={2} autoComplete="off" value={note} disabled={disabled} autoFocus={autoFocus} onChange={(event) => onChange(event.target.value)} />
      </Field>
    </>
  );
}
