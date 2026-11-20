import { readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";

import type { Page, Route } from "playwright-core";
import type { ViteDevServer } from "vite";
import { createServer } from "vite";

import type { NeedItem } from "./api/types";

// What the browser suites share: the application served by Vite, the daemon's answers from what the Go tests wrote
// from a real ledger, the wait for a view to finish reading, and axe-core's run over the page.

const require = createRequire(import.meta.url);
const axeSource = readFileSync(require.resolve("axe-core/axe.min.js"), "utf8");
export const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22a", "wcag22aa"];

export const goldens: Record<string, string | undefined> = Object.fromEntries(
  readdirSync("testdata")
    .filter((name) => name.endsWith(".json"))
    .map((name) => [name.replace(/\.json$/, ""), readFileSync(join("testdata", name), "utf8")]),
);

// What the conversation surface reads: what the member can talk to, with a planning session on a pane, a
// headless lane, and an interactive lane beside the stopped orchestrator the ledger wrote.
const talkTargets = JSON.stringify({
  ...JSON.parse(goldens["conversation-targets"] ?? "{}"),
  planning: [
    { lane: "lane-wo-running", work_order: "wo-plan", title: "Plan alerts", kind: "plan", worker: "worker-a", worker_name: "solo", generation: 1, run_mode: "tmux", state: "working" },
  ],
  lanes: [
    { lane: "lane-wo-1", work_order: "wo-1", title: "Add the retry budget", kind: "change", worker: "worker-a", worker_name: "solo", generation: 1, run_mode: "headless", state: "working" },
    { lane: "lane-wo-pane", work_order: "wo-2", title: "Highlight spans", kind: "change", worker: "worker-a", worker_name: "solo", generation: 1, run_mode: "tmux", state: "working" },
  ],
});

// Two merge requests, as the ledger holds them: the merge of a reviewed commit to approve at person_approves, whose
// work order has a page, and a merge the forge refused, held until a member decides it.
function mergeRequest(subject: "approve" | "held", workOrder: string) {
  const commit = "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432";
  const detail = {
    work_order_id: workOrder,
    repository: "harbor-web",
    commit,
    branch: "ropeshift/" + workOrder,
    base: "main",
    pull_request: 12,
    url: "https://github.com/acme/harbor-web/pull/12",
    method: "squash",
    merge_authority: "person_approves",
    ...(subject === "held" ? { reason: "the base branch requires 2 approving reviews", request: "merge:" + workOrder + ":1" } : {}),
  };
  const id = `merge-${subject}`;
  return {
    kind: "approval",
    id,
    since: 1790000500000,
    approval: {
      id,
      lane_id: "",
      work_order_id: workOrder,
      generation: 0,
      kind: "merge",
      subject,
      subject_digest: commit,
      summary:
        subject === "held"
          ? `The merge of pull request #12, commit 9f8e7d6 of work order ${workOrder} (Add the retry budget), cannot go on: ${detail.reason}. Approve to try it again, or deny to send the work back for rework.`
          : `Approve the merge of pull request #12, commit 9f8e7d6 of work order ${workOrder} (Add the retry budget), at person_approves. The review's verdicts are on the work order.`,
      requested_by: "system:integration",
      state: "pending",
      raised_at: 1790000500000,
      detail: JSON.stringify(detail),
    },
  };
}

// The kinds of card the ledger's own Needs you does not hold in this fixture: an orchestrator that stopped, work whose
// lane stayed parked a long time, work whose worker must be upgraded, and a request of an orchestrator to run an act
// its recovery policy does not allow.
function otherCards(): NeedItem[] {
  const base = (JSON.parse(goldens["needs-you"] ?? "{}") as { items: NeedItem[] }).items;
  const work = (id: string) => base.find((item) => item.id === id) as Extract<NeedItem, { kind: "work" }>;
  const parked = work("wo-parked");
  const lapsed = work("wo-lapsed");
  const as = (item: Extract<NeedItem, { kind: "work" }>, id: string, reason: string, detail?: string): NeedItem => ({
    ...item,
    id,
    work: { ...item.work, reason, detail, work_order: item.work.work_order && { ...item.work.work_order, id, title: `Work order ${id}` } },
  });
  // A merged adoption whose base lacks the design history, or could not be checked for it: done work with no lane and
  // nothing to press, as the API serves it.
  const history = (id: string, reason: string, detail: string): NeedItem => ({
    kind: "work",
    id,
    since: lapsed.since,
    work: { reason, detail, actions: [], work_order: lapsed.work.work_order && { ...lapsed.work.work_order, id, kind: "plan", title: `Work order ${id}`, state: "done" } },
  });
  const recovery = {
    act: "work_retry",
    work_order_id: "wo-failed",
    reason: "The lane failed on a flaky check, and a retry may pass.",
    check: "go test ./...",
    limit: "max_retries is 1 and the work was retried once",
    policy_version: 3,
  };
  return [
    {
      id: "plan-revise",
      kind: "plan_revision",
      since: 1790000650000,
      plan_revision: {
        plan: "plan-revise",
        title: "Retry budget",
        revision: {
          id: "revision-1",
          plan_id: "plan-revise",
          approved_version: 2,
          decision_ids: ["d-logo", "d-ids"],
          finished: [{ work_order: "wo-built", decisions: ["d-logo"] }],
          opened_at: 1790000650000,
        },
        decisions: [
          { id: "d-logo", statement: "Use the wordmark.", scope: "plan:plan-revise", reversibility: "reversible", status: "decided", decided_by: "member-1" },
          { id: "d-ids", statement: "Number work orders with ULIDs.", scope: "plan:plan-revise", reversibility: "one_way", status: "decided", decided_by: "member-1" },
        ],
        finished: [{ work_order: "wo-built", title: "Add the budget", state: "done", decisions: ["d-logo"] }],
        waiting: [{ work_order: "wo-waiting", title: "Document the budget" }],
      },
    },
    // A one-way decision the orchestrator of an unattended worker made itself, which waits for a member's review.
    {
      id: "d-repositories",
      kind: "one_way_review",
      since: 1790000660000,
      one_way_review: {
        decision: {
          id: "d-repositories",
          status: "decided",
          statement: "Keep one repository per service.",
          rationale: "The plan fixes one repository per service, and the lane recommends it.",
          citations: ["wo:wo-running", "requirement:FAC-2"],
          scope: "wo:wo-running",
          reversibility: "one_way",
          categories: ["architecture"],
          decided_by: "worker-a",
          decided_by_kind: "orchestrator",
          question_id: "q-repositories",
        },
        decided_by_name: "worker-a",
        question: "How many repositories does the product have?",
        actions: ["decision_keep", "decision_overturn"],
      },
    },
    as(parked, "wo-long-parked", "long_parked"),
    as(lapsed, "wo-behind", "worker_behind", "The worker runs 0.1.0 and the lane needs 0.2.0."),
    history(
      "wo-history-missing",
      "history_missing",
      "Branch lane/adopt merged into main, and main at 4f2a9c1 does not contain 9d11ff0, the design repository's tip that the adoption imported. Merge 9d11ff0 into main with a merge commit to bring the history back.",
    ),
    history("wo-history-unread", "history_unread", "Branch lane/adopt merged into main, and ropeshift could not check that main contains the design history the adoption imported: the plan approval names no design tip."),
    {
      id: "orchestrator-worker-a",
      kind: "work",
      since: 1790000700000,
      work: {
        reason: "tripped",
        actions: ["start_orchestrator"],
        orchestrator: { id: "orchestrator-worker-a", worker_id: "worker-a", desired_state: "running", run: 4, resets: 2, actual_state: "tripped", current_generation: 3, conditions: ["day_capped"], reported_at: 1790000000000 },
      },
    },
    {
      id: "recovery-1",
      kind: "approval",
      since: 1790000600000,
      approval: {
        id: "recovery-1",
        lane_id: "lane-wo-failed",
        work_order_id: "wo-failed",
        generation: 1,
        kind: "recovery",
        subject: "work_retry",
        subject_digest: "sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0",
        summary: "Retry wo-failed",
        requested_by: "orchestrator:worker-a",
        state: "pending",
        raised_at: 1790000600000,
        detail: JSON.stringify(recovery),
      },
    },
  ];
}

// The CI that asks a person, as a Go test wrote it from a real ledger: a head past its CI timeout, a run no runner
// picked up, and each finding of the classification. Each is titled by its kind, so a palette that lists them by title
// finds one.
function ciCards(): NeedItem[] {
  const written = JSON.parse(goldens["ci-needs-you"] ?? "{}") as { items: NeedItem[] };
  return written.items.map((item) => (item.kind === "ci" ? { ...item, ci: { ...item.ci, work_order: { ...item.ci.work_order, title: `${item.ci.work_order.title} (${item.ci.kind})` } } } : item));
}

// The work order a hard spend limit parked, as a Go test wrote it from a real ledger. It is titled by its kind, so a
// palette that lists items by title finds it.
function spendCards(): NeedItem[] {
  const written = JSON.parse(goldens["spend-needs-you"] ?? "{}") as { items: NeedItem[] };
  return written.items.map((item) => (item.kind === "spend" ? { ...item, spend: { ...item.spend, work_order: { ...item.spend.work_order, title: `${item.spend.work_order.title} (spend stop)` } } } : item));
}

// The repository a plan approval did not create, as a Go test wrote it from a real ledger, in each state a creation waits in: failed at
// its push, refused with the command that fixes it, refused because a forge repository exists, interrupted, and run by another
// request. Each is named for its state, so a palette that lists items by title finds it.
function creationCards(): NeedItem[] {
  const written = JSON.parse(goldens["repository-creation-needs-you"] ?? "{}") as { items: NeedItem[] };
  const [failed] = written.items;
  if (!failed || failed.kind !== "repository_creation") {
    return [];
  }
  const state = (name: string, change: Partial<(typeof failed)["repository_creation"]>): NeedItem => ({
    ...failed,
    id: `${failed.repository_creation.approval}/${name}`,
    repository_creation: { ...failed.repository_creation, name, ...change },
  });
  return [
    failed,
    state("signedout", {
      state: "refused",
      reason: "signedout is not registered: gh_signed_out",
      remedy: "run `gh auth login --hostname github.com`",
      creation: { forge: "github", owner: "acme", forge_name: "signedout", visibility: "public", default_branch: "main", remedy: "gh auth login --hostname github.com" },
    }),
    state("existing", {
      state: "refused",
      reason: "existing is not registered: remote_exists",
      remedy: "clone acme/existing at /work/existing and write `remote` in place of `create` in the manifest entry for existing, or skip it",
      creation: { forge: "github", owner: "acme", forge_name: "existing", visibility: "private", default_branch: "main" },
    }),
    state("halted", { state: "interrupted", reason: "the daemon stopped while it ran" }),
    state("running", { state: "running", reason: "", remedy: "", actions: [] }),
  ];
}

// plansOf is the plan pages the checks open for a version that creates repositories: what approving would create, a version whose
// entry no work orders name, and what an approval created and where it stopped.
function planOf(name: string): string | undefined {
  const created = JSON.parse(goldens["plan-created"] ?? "null") as { approval?: { approval: { state: string }; registration?: { preview?: boolean; repositories: Record<string, unknown>[] } } } | null;
  const planned = goldens["plan-planned"];
  if (!created?.approval?.registration) {
    return undefined;
  }
  const entry = (extra: Record<string, unknown>) => ({ path: "", manifest_path: "", ...extra });
  const creation = (forge_name: string, visibility: string, more: Record<string, unknown> = {}) => ({ forge: "github", owner: "acme", forge_name, visibility, default_branch: "main", login: "octo", ...more });
  if (name === "plan-created") {
    created.approval.registration.repositories = [
      entry({ name: "api", manifest_path: "repos/api", path: "/work/repos/api", outcome: "created", creation: creation("api", "private", { url: "git@github.com:acme/api.git" }) }),
      entry({ name: "site", manifest_path: "repos/site", path: "/work/repos/site", outcome: "created", creation: creation("site", "public", { url: "git@github.com:acme/site.git" }) }),
      entry({
        name: "docs",
        manifest_path: "repos/docs",
        path: "/work/repos/docs",
        outcome: "refused",
        reason: "docs is not registered: gh_signed_out: Run `gh auth login --hostname github.com` on this machine",
        refusal_code: "gh_signed_out",
        creation: creation("docs", "public", { remedy: "gh auth login --hostname github.com" }),
      }),
      entry({
        name: "legacy",
        manifest_path: "repos/legacy",
        path: "/work/repos/legacy",
        outcome: "refused",
        reason: "legacy is not registered: remote_exists",
        refusal_code: "remote_exists",
        creation: creation("legacy", "private", { remedy: "clone acme/legacy to repos/legacy, and write `remote` in place of `create` in the manifest's entry for legacy" }),
      }),
      entry({
        name: "busy",
        manifest_path: "repos/busy",
        path: "/work/repos/busy",
        outcome: "refused",
        reason: "busy is not registered: path_busy",
        refusal_code: "path_busy",
        creation: creation("busy", "private", { remedy: "another creation at this path is running; preview again when it finishes" }),
      }),
    ];
    return JSON.stringify(created);
  }
  if (name === "plan-planned") {
    return planned;
  }
  if (name === "plan-made") {
    const decided = JSON.parse(goldens["approval-decided-created"] ?? "{}") as { repositories?: Record<string, unknown>[] };
    created.approval.approval.state = "approved";
    created.approval.registration = { repositories: decided.repositories ?? [] };
    return JSON.stringify(created);
  }
  return undefined;
}

// conflictCards is a lane whose branch changed files a person changed too, as a Go test wrote it from a real ledger.
function conflictCards(): NeedItem[] {
  return (JSON.parse(goldens["conflict-needs-you"] ?? "{}") as { items?: NeedItem[] }).items ?? [];
}

// needsYouItems is what the checks' Needs you holds: the ledger's own items, the confirmation cards a delegate
// prepared, two merge requests, a card of each kind the ledger's items do not reach, the CI that asks a person, and a
// work order a spend limit parked, the repositories a plan approval did not create, and a conflict between a lane and a
// person's edits.
export function needsYouItems(): NeedItem[] {
  const needs = JSON.parse(goldens["needs-you"] ?? "{}") as { items: NeedItem[] };
  const cards = JSON.parse(goldens["needs-you-cards"] ?? "{}") as { items: NeedItem[] };
  return [
    ...needs.items,
    ...cards.items,
    mergeRequest("approve", "wo-running"),
    mergeRequest("held", "wo-held"),
    ...otherCards(),
    ...ciCards(),
    ...spendCards(),
    ...creationCards(),
    ...conflictCards(),
  ] as NeedItem[];
}

const projects = JSON.stringify({ projects: [{ id: "default", name: "default", display_name: "Default", state: "active", created_at: 1, repositories: [] }] });

// Two projects the ledger holds nothing in, for the views that have an empty state: "quiet" has no repository
// either, and "fresh" has one. Every read model of either lists nothing and counts nothing.
function emptyViews(): Record<string, unknown> {
  const stamp = { as_of: 1790000964002, revision: 1 };
  const measured = JSON.parse(goldens.operations ?? "{}") as Record<string, any>;
  const nothing = { numerator: 0, denominator: 0, value: null };
  const agreement = { decisions: 0, agreed: 0, disagreed: 0, unknown: 0, agreement_rate: nothing, approvals_overturned: 0, later_defects: 0, failures_overridden: 0 };
  Object.assign(measured, { work_orders: {}, lanes: {}, workers: 0, workers_fresh: 0, waiting_for_a_person: 0 });
  Object.assign(measured.measures, {
    interruptions: 0,
    completed: 0,
    interruptions_per_completed: nothing,
    overturn_rate: nothing,
    rework: nothing,
    cost_per_outcome: {},
    review_agreement: { workspace: { last_30_days: agreement, all_time: agreement }, repositories: [] },
  });
  return {
    "needs-you": { ...stamp, items: [] },
    completed: { ...stamp, items: [] },
    "in-progress": { ...stamp, items: [] },
    "on-hold": { ...stamp, items: [] },
    stuck: { ...stamp, items: [] },
    plans: { ...stamp, plans: [], unplanned: [], reviews: [], work_actions: {} },
    roadmap: { ...stamp, goals: [] },
    "shop-floor": { ...stamp, workers: [], lanes: [] },
    operations: measured,
  };
}
const empty = emptyViews();

// spendOf is what a project's Spend section reads. The default project is past its daily hard limit and under its weekly
// limit, and three projects stand elsewhere against the limits: with none set, under every soft limit, and past a soft limit.
function spendOf(project: string): string {
  const view = JSON.parse(goldens.spend ?? "{}") as { project_spend: Record<"day" | "week", Record<string, number>>; work_orders: Record<string, any>[] };
  const stand = (day: [number, number, number], week: [number, number, number], order: [number, number, number]) => {
    for (const [name, [spent, soft, hard]] of [["day", day], ["week", week]] as const) {
      Object.assign(view.project_spend[name], { spent_usd: spent, soft_limit: soft, hard_limit: hard, remaining: hard > 0 ? Math.max(hard - spent, 0) : 0 });
    }
    const [spent, soft, hard] = order;
    Object.assign(view.work_orders[0] ?? {}, { charged: { recorded_usd: spent, estimated_usd: 0, estimated_turns: 0, charged_usd: spent }, soft_limit: soft, hard_limit: hard, remaining: hard > 0 ? Math.max(hard - spent, 0) : 0 });
  };
  if (project === "spend-unset") {
    stand([0, 0, 0], [0, 0, 0], [0, 0, 0]);
  } else if (project === "spend-under") {
    stand([3, 8, 10], [3, 40, 50], [5, 12.5, 25]);
  } else if (project === "spend-soft") {
    stand([9, 8, 10], [45, 40, 50], [13, 12.5, 25]);
  }
  return JSON.stringify(view);
}

// answer stands where the daemon stands, as `serveViews` does for the other tests.
export async function answer(route: Route): Promise<void> {
  const url = new URL(route.request().url());
  const path = url.pathname.replace(/^\/v1\/projects\/[^/]+/, "/v1");
  const json = (body: string, status = 200) => route.fulfill({ status, contentType: "application/json", body });
  if (url.pathname === "/v1/projects") {
    return json(projects);
  }
  if (path === "/v1/needs-you") {
    return json(JSON.stringify({ ...JSON.parse(goldens["needs-you"] ?? "{}"), items: needsYouItems() }));
  }
  const view = /^\/v1\/views\/([a-z-]+)$/.exec(path)?.[1];
  const project = /^\/v1\/projects\/([^/]+)\//.exec(url.pathname)?.[1];
  // The projects with nothing in them answer every read model, the repositories, and the audit with nothing.
  if (project === "quiet" || project === "fresh") {
    if (view && view in empty) {
      return json(JSON.stringify(empty[view]));
    }
    if (path === "/v1/repositories") {
      return json(project === "quiet" ? JSON.stringify({ repositories: [] }) : (goldens.repositories ?? "{}"));
    }
    if (path === "/v1/audit") {
      return json(JSON.stringify({ scope: "project", as_of: 1790000964002, head: 0, next: 0, events: [] }));
    }
    if (path === "/v1/practices") {
      return json(JSON.stringify({ repositories: [] }));
    }
  }
  // The project that has work in review: a change the review could not judge, one whose merge waits for the
  // member's approval with a check running, and one whose merge the member approved.
  if (project === "reviewing" && (view === "completed" || view === "needs-you") && goldens[`reviews-${view}`]) {
    return json(goldens[`reviews-${view}`] ?? "{}");
  }
  // Its member has decided twelve times after a review, so its agreement shows a rate.
  if (project === "reviewing" && view === "operations") {
    const measured = JSON.parse(goldens.operations ?? "{}") as Record<string, any>;
    const { workspace } = measured.measures.review_agreement;
    workspace.last_30_days = { decisions: 12, agreed: 9, disagreed: 3, unknown: 0, agreement_rate: { numerator: 9, denominator: 12, value: 0.75 }, approvals_overturned: 2, later_defects: 1, failures_overridden: 1 };
    return json(JSON.stringify(measured));
  }
  if (view && goldens[view]) {
    return json(goldens[view]);
  }
  // What a work order's Changes and Activity tabs read: work that is running, work whose review ruled and waits
  // for a member, work that ended with a delivery record, and work that never reached ready.
  const evidence = /^\/v1\/work-orders\/([^/]+)\/(acceptance|timeline)$/.exec(path);
  if (evidence) {
    const read = ({ "wo-running": "running", "wo-accept": "reviewed", "wo-done": "ended", "wo-loose": "empty" } as Record<string, string | undefined>)[evidence[1] ?? ""];
    const body = read ? goldens[`${evidence[2]}-${read}`] : undefined;
    return body ? json(body) : json(JSON.stringify({ error: { code: "not_found", message: "no such work order" } }), 404);
  }
  // A work order's estimate beside its baseline.
  if (/^\/v1\/work-orders\/[^/]+\/estimate$/.test(path) && goldens["work-estimate"]) {
    return json(goldens["work-estimate"]);
  }
  if (path === "/v1/documents/read") {
    return json(goldens.document ?? "{}");
  }
  if (path === "/v1/documents/versions") {
    return json(goldens["document-versions"] ?? "{}");
  }
  if (path === "/v1/documents/recorded") {
    return json(goldens["recorded-document"] ?? "{}");
  }
  // The practices check: the golden's checked repository stands as the project's one repository, api, with a request open
  // beside its result, which the page reads until it is reported.
  if (path === "/v1/practices") {
    return json((goldens.practices ?? "{}").replaceAll("repo-a", "id-api").replaceAll("widgets", "api"));
  }
  if (/^\/v1\/practices\/checks\/[^/]+$/.test(path)) {
    const open = (JSON.parse(goldens.practices ?? "{}") as { repositories: { request?: unknown }[] }).repositories.find((row) => row.request)?.request;
    return json(JSON.stringify({ ...(open as object), repository: "id-api" }));
  }
  // What the projects, repositories, settings, agents, members, and audit pages read.
  const admin: Record<string, string | undefined> = {
    "/v1/repositories": goldens.repositories,
    "/v1/outside-edits": goldens["outside-edits"],
    "/v1/fleet": goldens.fleet,
    "/v1/policies": goldens["policies-recovery"],
    "/v1/roster": goldens.roster,
    "/v1/roster/history": goldens["roster-history"],
    "/v1/members": goldens.members,
    "/v1/audit": goldens.audit,
    "/v1/health": JSON.stringify({
      status: "ok",
      revision: 1,
      mode: "solo",
      version: "v0.1.0-alpha.16",
      commit: "0123456789abcdef0123456789abcdef01234567",
      date: "2026-11-08T10:00:00Z",
      installed: "v0.1.0-alpha.16",
    }),
    "/v1/workers": JSON.stringify({ workers: [{ id: "worker-1", name: "atlas", manifest: { capacity: { max_lanes: 6 } } }] }),
    "/v1/workers/worker-1/caps": goldens["worker-caps"],
    "/v1/categories": JSON.stringify({ categories: ["security", "data"] }),
  };
  // What the delegates panel, the delegate policy, and the audit's delegate rows read.
  if (path === "/v1/policies" && url.searchParams.get("kind") === "delegate") {
    return json(goldens["policies-delegate"] ?? "{}");
  }
  if (path === "/v1/policies" && url.searchParams.get("kind") === "estimate") {
    return json(goldens["policies-estimate"] ?? "{}");
  }
  // What the Spend sections and the channel choice of Notifications read.
  const spendPolicies: Record<string, string> = { spend: "policies-spend", installation_spend: "policies-installation-spend", notify: "policies-notify" };
  const spendKind = spendPolicies[url.searchParams.get("kind") ?? ""];
  if (path === "/v1/policies" && spendKind) {
    return json(goldens[spendKind] ?? "{}");
  }
  if (path === "/v1/spend") {
    return json(spendOf(project ?? ""));
  }
  if (path === "/v1/audit" && url.searchParams.get("actor") === "delegate") {
    return json(goldens["audit-delegates"] ?? "{}");
  }
  const delegates: Record<string, string | undefined> = { "/v1/delegates": goldens.delegates, "/v1/operations": goldens["operation-classes"] };
  if (delegates[path]) {
    return json(delegates[path] ?? "{}");
  }
  if (admin[path]) {
    return json(admin[path] ?? "{}");
  }
  // A plan, a request to approve one that has no plan yet, the outline of a planning lane, and the sources the
  // daemon reads.
  if (/^\/v1\/plans\/[^/]+\/forecast$/.test(path) && goldens.forecast) {
    return json(goldens.forecast);
  }
  if (path === "/v1/measures/delivery") {
    return json((url.searchParams.get("by") === "size" ? goldens["delivery-size"] : goldens.delivery) ?? "{}");
  }
  if (path === "/v1/measures/estimates") {
    return json(goldens.calibration ?? "{}");
  }
  if (path === "/v1/measures/history") {
    return json(goldens.history ?? "{}");
  }
  if (path === "/v1/measures/ci") {
    return json(goldens["ci-durations"] ?? JSON.stringify({ repositories: [] }));
  }
  if (/^\/v1\/work-orders\/[^/]+\/spend$/.test(path) && goldens["work-order-spend"]) {
    return json(goldens["work-order-spend"]);
  }
  const creating = /^\/v1\/plans\/(plan-created|plan-planned|plan-made)$/.exec(path)?.[1];
  const creatingPlan = creating ? planOf(creating) : undefined;
  if (creatingPlan) {
    return json(creatingPlan);
  }
  if (path.startsWith("/v1/plans/adopt-approval")) {
    return json(JSON.stringify({ error: { code: "not_found", message: "no such plan" } }), 404);
  }
  if (path.startsWith("/v1/plans/") && goldens.plan) {
    return json(goldens.plan);
  }
  if (path === "/v1/approvals/adopt-approval" && goldens["approval-adopted"]) {
    return json(goldens["approval-adopted"]);
  }
  if (/^\/v1\/lanes\/[^/]+\/outline$/.test(path) && goldens.outline) {
    return json(goldens.outline);
  }
  if (path === "/v1/sources") {
    return json(JSON.stringify({ sources: [] }));
  }
  // What each channel was told, what was sent to a lane, and what each lane reported of its tokens and cost.
  if (path === "/v1/notices" && goldens.notices) {
    return json(goldens.notices);
  }
  if (/^\/v1\/lanes\/[^/]+\/messages$/.test(path) && goldens["lane-messages"]) {
    return json(goldens["lane-messages"]);
  }
  if (path === "/v1/templates" && goldens.templates) {
    return json(goldens.templates);
  }
  if (path === "/v1/telemetry/lanes" && goldens["telemetry-lanes"]) {
    return json(goldens["telemetry-lanes"]);
  }
  if (/^\/v1\/workers\/[^/]+\/orchestrator$/.test(path)) {
    return json(
      JSON.stringify({ id: "orchestrator-worker-a", worker_id: "worker-a", desired_state: "running", run: 4, resets: 2, actual_state: "tripped", current_generation: 3, conditions: ["day_capped"], reported_at: 1790000000000 }),
    );
  }
  if (path === "/v1/conversation-targets") {
    return json(talkTargets);
  }
  if (path === "/v1/conversations" && goldens.conversations) {
    return json(goldens.conversations);
  }
  if (/^\/v1\/conversations\/[^/]+\/entries$/.test(path) && goldens["conversation-entries"]) {
    return json(goldens["conversation-entries"]);
  }
  if (/^\/v1\/lanes\/[^/]+\/stream$/.test(path) && goldens["lane-stream"]) {
    return json(goldens["lane-stream"]);
  }
  if (/^\/v1\/orchestrators\/[^/]+\/stream$/.test(path) && goldens["orchestrator-stream"]) {
    return json(goldens["orchestrator-stream"]);
  }
  if (path === "/v1/events" || path.startsWith("/v1/panes/")) {
    return route.fulfill({ status: 200, contentType: "text/event-stream", body: ": open\n\n" });
  }
  return json(JSON.stringify({ error: { code: "not_found", message: "not served in this test" } }), 404);
}

type Violation = { id: string; help: string; nodes: { target: string[] }[] };

// dialogsSeen holds the name of every dialog an audit found open, so a suite can check that each dialog the states
// list was audited.
export const dialogsSeen = new Set<string>();

// audit runs axe-core over the page with the WCAG 2.0, 2.1, and 2.2 A and AA rules, and names each violation.
export async function audit(page: Page): Promise<string[]> {
  const open = await page.evaluate(() =>
    [...document.querySelectorAll("[role=dialog][aria-labelledby]")].map((dialog) => document.getElementById(dialog.getAttribute("aria-labelledby") ?? "")?.textContent ?? ""),
  );
  for (const name of open) {
    dialogsSeen.add(name);
  }
  await page.evaluate(axeSource);
  const violations = (await page.evaluate(
    (runOnly) =>
      (window as unknown as { axe: { run: (context: Document, options: object) => Promise<{ violations: unknown[] }> } }).axe
        .run(document, { runOnly: { type: "tag", values: runOnly }, resultTypes: ["violations"] })
        .then((result) => result.violations),
    tags,
  )) as Violation[];
  return violations.map((violation) => `${violation.id}: ${violation.help} at ${violation.nodes.map((node) => node.target.join(" ")).join(", ")}`);
}

// ready waits until the view's title stands and no skeleton holds the place of its body.
export async function ready(page: Page): Promise<void> {
  await page.locator("main h1:visible").first().waitFor({ timeout: 20_000 });
  await page.waitForFunction(() => !document.querySelector("main .skeleton-rows"), null, { timeout: 20_000 });
  await page.waitForTimeout(150);
}

// serveApp starts the application on a loopback port, and says where.
export async function serveApp(): Promise<{ server: ViteDevServer; base: string }> {
  const server = await createServer({ configFile: "vite.config.ts", logLevel: "error", server: { host: "127.0.0.1", port: 0, strictPort: false, hmr: false } });
  await server.listen();
  return { server, base: (server.resolvedUrls?.local[0] ?? "").replace(/\/$/, "") };
}
