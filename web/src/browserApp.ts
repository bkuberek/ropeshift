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
  const recovery = {
    act: "work_retry",
    work_order_id: "wo-failed",
    reason: "The lane failed on a flaky check, and a retry may pass.",
    check: "go test ./...",
    limit: "max_retries is 1 and the work was retried once",
    policy_version: 3,
  };
  return [
    as(parked, "wo-long-parked", "long_parked"),
    as(lapsed, "wo-behind", "worker_behind", "The worker runs 0.1.0 and the lane needs 0.2.0."),
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

// needsYouItems is what the checks' Needs you holds: the ledger's own items, the confirmation cards a delegate
// prepared, two merge requests, and a card of each kind the ledger's items do not reach.
export function needsYouItems(): NeedItem[] {
  const needs = JSON.parse(goldens["needs-you"] ?? "{}") as { items: NeedItem[] };
  const cards = JSON.parse(goldens["needs-you-cards"] ?? "{}") as { items: NeedItem[] };
  return [...needs.items, ...cards.items, mergeRequest("approve", "wo-running"), mergeRequest("held", "wo-held"), ...otherCards()] as NeedItem[];
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
  // What the projects, repositories, settings, agents, members, and audit pages read.
  const admin: Record<string, string | undefined> = {
    "/v1/repositories": goldens.repositories,
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
