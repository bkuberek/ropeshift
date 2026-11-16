import { timeOf } from "../admin/parts";
import type { Estimate, WorkEstimate } from "../api/measures";
import { Badge } from "../ui/display";
import { estimateWords, spokenBand } from "./estimateWords";
import { goSpan } from "./format";

// A track draws a band to scale from nothing to a little past its longest record. The words beside it carry the
// meaning, so the track is a picture with a sentence for a label.
type Track = { from: number; to: number; p50: number; p85: number | null; actual: number | null };

function trackOf(e: Estimate, actualMs?: number): Track {
  const longest = Math.max(e.delivery_max_ms, e.delivery_p85_ms, actualMs ?? 0);
  const scale = longest * (e.p85_open ? 1.5 : 1.1) || 1;
  const at = (ms: number) => Math.min(100, (ms / scale) * 100);
  return {
    from: at(e.delivery_min_ms),
    to: e.p85_open ? 100 : at(e.delivery_p85_ms),
    p50: at(e.delivery_p50_ms),
    p85: e.p85_open ? null : at(e.delivery_p85_ms),
    actual: actualMs === undefined ? null : at(actualMs),
  };
}

// EstimateTrack is the band as a picture: a solid fill for a confident estimate and a hatch for a low-confidence
// one, a tick at p50 and one at p85, and for an open band no p85 tick, with the fill running to the end of the
// track under an arrow.
function EstimateTrack({ estimate, label, actualMs }: { estimate: Estimate; label: string; actualMs?: number }) {
  const track = trackOf(estimate, actualMs);
  return (
    <div
      className="estimate-track"
      role="img"
      aria-label={`${label}: ${spokenBand(estimate)}${actualMs === undefined ? "" : `, actual ${goSpan(actualMs)}`}`}
      data-fill={estimate.confidence === "low" ? "hatch" : "solid"}
    >
      <span className="estimate-fill" style={{ left: `${track.from}%`, width: `${Math.max(track.to - track.from, 1)}%` }} />
      <span className="estimate-tick" data-tick="p50" style={{ left: `${track.p50}%` }} />
      {track.p85 === null ? (
        <span className="estimate-open" aria-hidden="true">
          ›
        </span>
      ) : (
        <span className="estimate-tick" data-tick="p85" style={{ left: `${track.p85}%` }} />
      )}
      {track.actual === null ? null : <span className="estimate-tick" data-tick="actual" style={{ left: `${track.actual}%` }} />}
    </div>
  );
}

// EstimateBand says one estimate: the Low confidence badge before the label when the estimate is low, the label,
// its time and cost bands in the words `ropeshift work show` prints, the band drawn to scale, and the explanation
// it was made with. An estimate with no history says why in the absence words, and is never drawn as a band.
// Where space is short the badge stays and the explanation waits under a control. An actual delivery time draws
// its own tick and is said beside the band.
export function EstimateBand({ estimate, label = "Estimate", compact = false, actualMs }: { estimate: Estimate | null | undefined; label?: string; compact?: boolean; actualMs?: number }) {
  if (!estimate) {
    return null;
  }
  const words = estimateWords(estimate);
  const none = estimate.status === "no_history";
  const explanation = compact ? (
    <details className="estimate-more">
      <summary>How it was made</summary>
      <p className="small muted">{words.explanation}</p>
    </details>
  ) : (
    <p className="small muted estimate-explanation">{words.explanation}</p>
  );
  return (
    <div className="estimate" data-confidence={estimate.confidence ?? "none"}>
      <p className="estimate-line">
        {estimate.confidence === "low" ? <Badge tone="warning" glyph="triangle">Low confidence</Badge> : null}{" "}
        <span className="estimate-label">{label}</span>{" "}
        {none ? (
          <span>{words.label}</span>
        ) : (
          <>
            {estimate.confidence === "confident" ? <span>{words.label}</span> : null} <span>{words.delivery}</span> <span>{words.cost}</span>
          </>
        )}
      </p>
      {none ? null : <EstimateTrack estimate={estimate} label={label} actualMs={actualMs} />}
      {actualMs === undefined ? null : <p className="small estimate-actual">Actual {goSpan(actualMs)}</p>}
      {explanation}
    </div>
  );
}

// WorkEstimateView shows a work order's current estimate beside the baseline its finished work is measured
// against, each in its own band. A work order with no estimate, or with no baseline yet, says so in words.
export function WorkEstimateView({ read, compact = false, actualMs }: { read: WorkEstimate; compact?: boolean; actualMs?: number }) {
  return (
    <div className="estimate-pair">
      {read.estimate ? (
        <div data-band="estimate">
          <EstimateBand estimate={read.estimate} compact={compact} />
        </div>
      ) : (
        <p className="muted">No estimate was made for this work order.</p>
      )}
      {read.baseline ? (
        <div data-band="baseline">
          <EstimateBand estimate={read.baseline.estimate} label="Baseline" compact={compact} actualMs={actualMs} />
          <p className="small muted">
            Recorded {timeOf(read.baseline.recorded_at)}
            {read.baseline.approval_id ? ", when the plan version that showed it was approved" : ", when the work order was made"}.
          </p>
        </div>
      ) : (
        <p className="muted">No baseline is recorded for this work order yet.</p>
      )}
    </div>
  );
}
