import type { Estimate } from "../api/measures";
import { fixed, goSpan } from "./format";

// What an estimate says, in the words `ropeshift work show` prints. An open band reads "more than" its longest
// record, and a cost band that holds a lower bound reads "at least". The explanation is the text the API gives
// every surface.

export type EstimateWords = {
  // The estimate's label: confident, low, or no history.
  label: string;
  // The time and cost bands, each empty for an estimate with no history.
  delivery: string;
  cost: string;
  explanation: string;
};

const dollars = (value: number) => `$${fixed(value, 2)}`;

export function estimateWords(e: Estimate): EstimateWords {
  if (e.status === "no_history") {
    return { label: "no history", delivery: "", cost: "", explanation: e.explanation };
  }
  const open = e.p85_open ? "more than " : "";
  const bound = e.cost_at_least ? "at least " : "";
  return {
    label: e.confidence ?? "",
    delivery: `delivery p50 ${goSpan(e.delivery_p50_ms)} p85 ${open}${goSpan(e.delivery_p85_ms)}`,
    cost: `cost ${bound}p50 ${dollars(e.cost_p50_usd)} p85 ${open}${dollars(e.cost_p85_usd)}`,
    explanation: e.explanation,
  };
}

// spokenBand reads a band's track aloud: its p50, and its p85 with "more than" when the band is open.
export function spokenBand(e: Estimate): string {
  return `p50 ${goSpan(e.delivery_p50_ms)}, p85 ${e.p85_open ? "more than " : ""}${goSpan(e.delivery_p85_ms)}`;
}
