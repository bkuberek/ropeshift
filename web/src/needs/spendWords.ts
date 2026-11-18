import type { SpendBand, SpendResume, WorkOrderSpend } from "../api/types";
import { usd } from "../measures/format";
import type { IconName } from "../ui/Icon";

// What a spend stop says, in a person's words (ADR-0037). Every amount is US dollars at list price.

// Where a work order's limits came from.
export const limitBasisWords: Record<NonNullable<WorkOrderSpend["basis"]>, string> = {
  baselined_band: "the cost band recorded when its plan was approved",
  current_band: "its current cost band, because it has no recorded one",
  no_history: "the limit for its size, because it has no trusted cost band",
  review: "a share of the limit of the work it judges",
};

// What one more generation was sized by.
export const allowanceBasisWords: Record<SpendResume["basis"], string> = {
  mean_generation: "the mean charge of the generations that recorded spend",
  estimate: "the work order's estimate, because no generation recorded spend",
};

export const money = (amount: number) => usd(amount, false);

// The overline of a spend stop's card.
export const spendKind: { icon: IconName; name: string } = { icon: "gauge", name: "Spend limit reached" };

// bandWords says the cost band and how far it can be trusted. A work order with no estimate has no band, and its
// limits are the size's.
export function bandWords(band: SpendBand | undefined): string {
  if (!band) {
    return "no estimate";
  }
  const trust = band.confidence === "confident" ? "confident" : band.confidence === "low" ? "low confidence" : "no confidence recorded";
  const from = `${band.outcomes} finished ${band.outcomes === 1 ? "work order" : "work orders"}`;
  return `p50 ${money(band.p50)} · p85 ${money(band.p85)}${band.open ? " or more" : ""} · ${trust}, from ${from}`;
}

// shareWords says how much of a work order's spend was waiting. It is absent while no model call has been attributed.
export function shareWords(share: number | undefined): string {
  return share === undefined ? "not attributed yet" : `${Math.round(share * 100)} percent of its spend`;
}

// A raise to an amount is a number of dollars with at most two places.
const amountPattern = /^\d{1,7}(\.\d{1,2})?$/;

// The most a limit is raised to, which the server holds too.
export const largestLimit = 1_000_000;

// amountError says why an amount cannot be a hard limit, or nothing when it can. A limit that stays at or below what the
// work order is charged lifts nothing, so it is refused before it is sent.
export function amountError(text: string, spend: WorkOrderSpend): string {
  const amount = text.trim();
  if (amount === "") {
    return "";
  }
  if (!amountPattern.test(amount)) {
    return "Write an amount in dollars, such as 80 or 80.50.";
  }
  const value = Number(amount);
  if (value > largestLimit) {
    return `A limit is at most ${money(largestLimit)}.`;
  }
  if (value <= spend.charged.charged_usd) {
    return `A limit of ${money(value)} would stay below the ${money(spend.charged.charged_usd)} this work order is charged.`;
  }
  if (value <= spend.hard_limit) {
    return `A raise must be above the ${money(spend.hard_limit)} limit in force.`;
  }
  return "";
}
