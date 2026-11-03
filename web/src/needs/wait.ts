const minute = 60_000;
const hour = 60 * minute;
const day = 24 * hour;

// waitLabel says how long something has waited, in the largest units that still read at a glance.
export function waitLabel(ms: number): string {
  if (ms < minute) {
    return "now";
  }
  if (ms < hour) {
    return `${Math.floor(ms / minute)}m`;
  }
  if (ms < day) {
    const hours = Math.floor(ms / hour);
    const minutes = Math.floor((ms % hour) / minute);
    return minutes === 0 ? `${hours}h` : `${hours}h ${minutes}m`;
  }
  const days = Math.floor(ms / day);
  const hours = Math.floor((ms % day) / hour);
  return hours === 0 ? `${days}d` : `${days}d ${hours}h`;
}

// spanWords says a stretch of time in the words a screen reader reads well.
function spanWords(ms: number): string {
  const parts: string[] = [];
  const add = (amount: number, unit: string) => {
    if (amount > 0) {
      parts.push(`${amount} ${unit}${amount === 1 ? "" : "s"}`);
    }
  };
  add(Math.floor(ms / day), "day");
  add(Math.floor((ms % day) / hour), "hour");
  if (ms < day) {
    add(Math.floor((ms % hour) / minute), "minute");
  }
  return parts.join(" ");
}

// waitWords is the same wait for a screen reader, which reads "3h" badly.
export function waitWords(ms: number): string {
  return ms < minute ? "waiting since just now" : `waiting ${spanWords(ms)}`;
}

// agoLabel says how long ago something was true, and agoWords says it to a screen reader.
export function agoLabel(ms: number): string {
  return ms < minute ? "just now" : `${waitLabel(ms)} ago`;
}

export function agoWords(ms: number): string {
  return ms < minute ? "just now" : `${spanWords(ms)} ago`;
}

// forWords says how long something has lasted, to a screen reader.
export function forWords(ms: number): string {
  return ms < minute ? "for under a minute" : `for ${spanWords(ms)}`;
}
