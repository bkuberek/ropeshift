import { ApiError } from "../api/client";

// lockedOut says a refusal is a group server's limit on wrong guesses at the passphrase, which a sign-in and a change
// of the passphrase both meet. Its wait is the refusal's retryAfter.
export function lockedOut(error: unknown): error is ApiError {
  return error instanceof ApiError && (error.code === "too_many_attempts" || error.status === 429);
}

// waitOf says how long a limited address waits: the seconds a server gave, in whole minutes from a minute up and in
// whole hours from an hour up, and a minute when it gave none.
export function waitOf(seconds: number | undefined): string {
  if (seconds === undefined) {
    return "a minute";
  }
  if (seconds < 60) {
    return seconds === 1 ? "1 second" : `${seconds} seconds`;
  }
  if (seconds < 60 * 60) {
    const minutes = Math.ceil(seconds / 60);
    return minutes === 1 ? "a minute" : `${minutes} minutes`;
  }
  const hours = Math.ceil(seconds / (60 * 60));
  return hours === 1 ? "an hour" : `${hours} hours`;
}

// tooManyAttempts is what a page says while a group server limits its address.
export function tooManyAttempts(seconds: number | undefined): string {
  return `Too many sign-in attempts. Wait ${waitOf(seconds)}, then try again.`;
}
