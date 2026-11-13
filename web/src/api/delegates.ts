import { call } from "./client";

// The installation's routes for delegate credentials, signing out everywhere, and the class table.
export const delegatePaths = {
  delegates: () => "/v1/delegates",
  revoke: (id: string) => `/v1/delegates/${encodeURIComponent(id)}/revoke`,
  remove: (id: string) => `/v1/delegates/${encodeURIComponent(id)}/remove`,
  signOutAll: () => "/v1/sessions/end-all",
  operations: () => "/v1/operations",
};

export type DelegateState = "active" | "expired" | "revoked";

// Delegate is one of the member's delegate credentials. The ledger keeps only its token's digest.
export type Delegate = {
  id: string;
  label: string;
  project: string;
  kind: string;
  created_at: number;
  expires_at: number;
  last_used_at: number | null;
  revoked_at: number | null;
  state: DelegateState;
};

export type DelegateList = { as_of: number; delegates: Delegate[] };

// DelegateCreated is a new credential with its token, which this is the one time anyone sees.
export type DelegateCreated = Delegate & { token: string };

export type SignedOutAll = { sessions: number; delegates: string[] };

export type OperationClass = "operational" | "consent" | "member_only";

// Operation is one row of the class table: what a delegate's call to it does.
export type Operation = { id: string; label: string; class: OperationClass; exemptable: boolean; never_reason?: string };

export type OperationList = { operations: Operation[] };

// The lives a member chooses for a new credential, from an hour to the thirty days the API allows.
export const delegateLives: { label: string; ms: number }[] = [
  { label: "1 hour", ms: 3_600_000 },
  { label: "8 hours", ms: 8 * 3_600_000 },
  { label: "1 day", ms: 24 * 3_600_000 },
  { label: "7 days", ms: 7 * 24 * 3_600_000 },
  { label: "30 days", ms: 30 * 24 * 3_600_000 },
];

export const readDelegates = () => call<DelegateList>("GET", delegatePaths.delegates());
export const createDelegate = (project: string, label: string, expiresInMs: number) =>
  call<DelegateCreated>("POST", delegatePaths.delegates(), { project, label, expires_in_ms: expiresInMs });
export const revokeDelegate = (id: string) => call<Delegate>("POST", delegatePaths.revoke(id), {});
export const removeDelegate = (id: string) => call<void>("POST", delegatePaths.remove(id), {});
export const signOutAll = () => call<SignedOutAll>("POST", delegatePaths.signOutAll(), {});
export const readOperations = () => call<OperationList>("GET", delegatePaths.operations());
