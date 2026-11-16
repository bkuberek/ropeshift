// How the delivery measures and the forecasts say a duration, a date, and a cost. Each reads as the command
// line prints it, so a number and its words are the same on either surface.

// goSpan says a stretch of milliseconds to the second, as `ropeshift plan forecast` does: 45s, 30m0s, 1h30m0s.
export function goSpan(ms: number): string {
  const seconds = Math.round(ms / 1000);
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const rest = seconds % 60;
  if (hours > 0) {
    return `${hours}h${minutes}m${rest}s`;
  }
  return minutes > 0 ? `${minutes}m${rest}s` : `${rest}s`;
}

const two = (n: number) => String(n).padStart(2, "0");

// utcDate says a time in epoch milliseconds to the minute in UTC, with "or later" when it is a lower bound.
export function utcDate(ms: number, lowerBound: boolean): string {
  const at = new Date(ms);
  const text = `${at.getUTCFullYear()}-${two(at.getUTCMonth() + 1)}-${two(at.getUTCDate())} ${two(at.getUTCHours())}:${two(at.getUTCMinutes())} UTC`;
  return lowerBound ? `${text} or later` : text;
}

// utcDay says a time in epoch milliseconds as its UTC day, as the command prints the days of a window and of a week.
export const utcDay = (ms: number): string => new Date(ms).toISOString().slice(0, 10);

// fixed writes a number to some decimal places. One that falls exactly between two goes to the even one, as the
// command line's own rounding does, where a browser's goes up; such a number is an odd multiple of one over two
// to the power of one more than the places, and no other number falls exactly between.
export function fixed(value: number, places: number): string {
  const steps = value * 2 ** (places + 1);
  if (Number.isInteger(steps) && Math.abs(steps) % 2 === 1) {
    const scaled = value * 10 ** places;
    const down = Math.floor(scaled);
    return ((down % 2 === 0 ? down : down + 1) / 10 ** places).toFixed(places);
  }
  return value.toFixed(places);
}

// usd says an estimated cost, with "at least" when it is a lower bound.
export function usd(value: number, lowerBound: boolean): string {
  return `${lowerBound ? "at least " : ""}$${fixed(value, 2)}`;
}

// span says a stretch in its two largest units, as a person reads a duration: 45s, 45m, 1h 30m, 1d 2h.
export function span(ms: number): string {
  if (ms < 60_000) {
    return `${Math.round(ms / 1000)}s`;
  }
  const minutes = Math.round(ms / 60_000);
  const days = Math.floor(minutes / 1440);
  const hours = Math.floor((minutes % 1440) / 60);
  const rest = minutes % 60;
  if (days > 0) {
    return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
  }
  if (hours > 0) {
    return rest > 0 ? `${hours}h ${rest}m` : `${hours}h`;
  }
  return `${rest}m`;
}
