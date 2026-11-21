import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import { beforeEach, describe, expect, it } from "vitest";

import routes from "../../testdata/routes.json";
import { paletteActions } from "../app/paletteActions";
import { FakeEvents, serveApi, stubEvents } from "../testing";
import {
  acknowledgeOneWay,
  acknowledgeRisk,
  addCategory,
  addRepositories,
  archiveLane,
  changeRepository,
  changeRoster,
  closeWindow,
  createJoinToken,
  revokeJoinToken,
  createPlanWork,
  createProject,
  downloadBackup,
  downloadInstallationBackup,
  importUpload,
  listTemplates,
  openWindow,
  publishPolicy,
  readAcceptance,
  readAdoptionPlan,
  readApprovals,
  readAudit,
  readAutonomy,
  readCaps,
  readCategories,
  readFleet,
  readHealth,
  readLaneMessages,
  readLaneTelemetry,
  readMembers,
  readNotices,
  readOrchestrator,
  readOutsideEdits,
  readPlans,
  readPolicies,
  readRepositories,
  readRisk,
  readRoster,
  readRosterHistory,
  readSources,
  readSpend,
  readTemplateWrite,
  readTimeline,
  readWorkers,
  releaseClaim,
  removeProject,
  removeRepository,
  renameProject,
  restoreProject,
  archiveProject,
  revertRoster,
  setCaps,
  setFleetCaps,
  setFleetMode,
  setLevel,
  startContractReview,
  withdrawOneWay,
  writeTemplates,
} from "./admin";
import {
  actOnWork,
  answerBatch,
  cancelScheduledStart,
  answerQuestion,
  changePassphrase,
  confirmRevision,
  decideApproval,
  decideEffect,
  dismissQuestion,
  mergeQuestion,
  decideQuestionNow,
  followEvents,
  followPane,
  overturnDecision,
  passReview,
  sendPaneKeys,
  readPaneScrollback,
  readApproval,
  scheduleOrchestrator,
  readDocument,
  readDocumentVersions,
  readNeedsYou,
  readOutline,
  readPlan,
  readProjects,
  readRecorded,
  readView,
  recordDecision,
  recordQuestion,
  rerunCI,
  retryRepository,
  reviseSession,
  reworkForDecisions,
  searchCandidates,
  sendBack,
  signIn,
  signOut,
  skipRepository,
  startOrchestrator,
  waitLongerForCI,
} from "./client";
import { acknowledgeFinding, checkPractices, listPractices, readPracticesCheck, withdrawAcknowledgement } from "./practices";
import { readForgeAccounts } from "./projectRemote";
import { createDelegate, readDelegates, readOperations, removeDelegate, revokeDelegate, signOutAll } from "./delegates";
import { readCalibration, readCIDurations, readDelivery, readDeliveryHistory, readEstimate, readPlanForecast } from "./measures";
import { blockSpend, raiseSpendLimit, readWorkOrderSpend, replanSpend, resumeSpend } from "./spend";
import { claimAndStart, startClaimed } from "./runWork";
import {
  clearConversation,
  compactConversation,
  createConversation,
  deleteConversation,
  openConversation,
  readConversationEntries,
  readConversations,
  readConversationTargets,
  readLaneStream,
  readOrchestratorStream,
  renameConversation,
  sendMessage,
  sendToAssistant,
  stopConversation,
} from "./talk";
import type { Route } from "./types";

// FR-UI-7 holds the web UI to the command line, and the Go tests hold the web listener to it: every row of
// the list below names a command, and every command and operation has a row or a named exception. This
// holds the UI to the list. Every route the listener serves has a client function that makes its call,
// which a component, a hook, or a flow imports, or it is a gap with the slice of U2 that closes it. An
// entry that stays on the gap list after its gap closes fails here, so the list only shrinks.

// A caller is a call of one exported client function, with the name a component imports it by.
type Caller = { fn: string; call: () => unknown };

const view = (name: string): Caller => ({ fn: "readView", call: () => readView(name) });
const act = (action: Parameters<typeof actOnWork>[1]): Caller => ({ fn: "actOnWork", call: () => actOnWork("wo-1", action, "because") });
const step = (fn: string, call: () => unknown): Caller => ({ fn, call });

const callers: Record<string, Caller> = {
  "POST /v1/sessions": step("signIn", () => signIn("a passphrase")),
  "POST /v1/sessions/end": step("signOut", () => signOut()),
  "PUT /v1/passphrase": step("changePassphrase", () => changePassphrase("the old passphrase", "the new passphrase")),
  "GET /v1/projects": step("readProjects", () => readProjects()),
  "GET /v1/needs-you": step("readNeedsYou", () => readNeedsYou()),
  "PUT /v1/workers/{id}/orchestrator": step("startOrchestrator", () => startOrchestrator("worker-1")),
  "POST /v1/workers/{id}/orchestrator/schedule": step("scheduleOrchestrator", () => scheduleOrchestrator("worker-1")),
  "DELETE /v1/workers/{id}/orchestrator/schedule": step("cancelScheduledStart", () => cancelScheduledStart("worker-1")),
  "GET /v1/workers/{id}/orchestrator": step("readOrchestrator", () => readOrchestrator("worker-1")),
  "POST /v1/projects": step("createProject", () => createProject("tidewater", "")),
  "POST /v1/projects/{project}/rename": step("renameProject", () => renameProject("tidewater", "harbor")),
  "POST /v1/projects/{project}/archive": step("archiveProject", () => archiveProject("tidewater")),
  "POST /v1/projects/{project}/restore": step("restoreProject", () => restoreProject("tidewater")),
  "POST /v1/projects/{project}/remove": step("removeProject", () => removeProject("tidewater", "tidewater")),
  "POST /v1/projects/import/upload": step("importUpload", () => importUpload("AAAA", "tidewater")),
  "POST /v1/installation/backup/download": step("downloadInstallationBackup", () => downloadInstallationBackup()),
  "GET /v1/templates": step("listTemplates", () => listTemplates()),
  "POST /v1/workers/{id}/template-writes": step("writeTemplates", () => writeTemplates("worker-1", { shape: "monorepo", path: "/srv/code/harbor" })),
  "GET /v1/workers/{id}/template-writes/{request}": step("readTemplateWrite", () => readTemplateWrite("worker-1", "request-1")),
  "POST /v1/workers/{id}/forge-account-reads": step("readForgeAccounts", () => readForgeAccounts("worker-1")),
  "GET /v1/workers/{id}/forge-account-reads/{request}": step("readForgeAccounts", () => readForgeAccounts("worker-1")),
  "GET /v1/workers": step("readWorkers", () => readWorkers()),
  "POST /v1/workers/tokens": step("createJoinToken", () => createJoinToken()),
  "POST /v1/workers/tokens/{id}/revoke": step("revokeJoinToken", () => revokeJoinToken("token-1")),
  "GET /v1/workers/{id}/autonomy": step("readAutonomy", () => readAutonomy("worker-1")),
  "PUT /v1/workers/{id}/autonomy": step("setLevel", () => setLevel("worker-1", "attended")),
  "POST /v1/workers/{id}/autonomy/acknowledgement": step("acknowledgeRisk", () => acknowledgeRisk("worker-1", "digest")),
  "POST /v1/workers/{id}/autonomy/one-way-acknowledgement": step("acknowledgeOneWay", () => acknowledgeOneWay("worker-1", "digest")),
  "DELETE /v1/workers/{id}/autonomy/one-way-acknowledgement": step("withdrawOneWay", () => withdrawOneWay("worker-1")),
  "POST /v1/workers/{id}/windows": step("openWindow", () => openWindow("worker-1", "window-1", 3_600_000)),
  "DELETE /v1/workers/{id}/windows": step("closeWindow", () => closeWindow("worker-1")),
  "GET /v1/workers/{id}/caps": step("readCaps", () => readCaps("worker-1")),
  "PUT /v1/workers/{id}/caps": step("setCaps", () => setCaps("worker-1", { max_lanes: null, max_lanes_per_sign_in: 0 })),
  "GET /v1/autonomy/risk": step("readRisk", () => readRisk()),
  "GET /v1/health": step("readHealth", () => readHealth()),
  "GET /v1/members": step("readMembers", () => readMembers()),
  "GET /v1/audit": step("readAudit", () => readAudit("installation", {})),
  "POST /v1/sessions/end-all": step("signOutAll", () => signOutAll()),
  "GET /v1/delegates": step("readDelegates", () => readDelegates()),
  "POST /v1/delegates": step("createDelegate", () => createDelegate("tidewater", "ci", 60_000)),
  "POST /v1/delegates/{id}/revoke": step("revokeDelegate", () => revokeDelegate("delegate-1")),
  "POST /v1/delegates/{id}/remove": step("removeDelegate", () => removeDelegate("delegate-1")),
  "GET /v1/operations": step("readOperations", () => readOperations()),
  "POST /v1/adoption/plan": step("readAdoptionPlan", () => readAdoptionPlan("installation", { root: "/work/api" })),
  "GET /v1/roster": step("readRoster", () => readRoster("installation")),
  "GET /v1/roster/history": step("readRosterHistory", () => readRosterHistory("installation")),
  "POST /v1/roster/changes": step("changeRoster", () => changeRoster("installation", { op: "disable", agent: "reviewer" })),
  "POST /v1/roster/revert": step("revertRoster", () => revertRoster("installation", 1)),

  "GET /v1/projects/{project}/events": step("followEvents", () => followEvents(0)),
  "GET /v1/projects/{project}/documents/read": step("readDocument", () => readDocument({ path: "docs/a.md" })),
  "GET /v1/projects/{project}/documents/versions": step("readDocumentVersions", () => readDocumentVersions({ path: "docs/a.md" })),
  "GET /v1/projects/{project}/documents/recorded": step("readRecorded", () => readRecorded("spec:a")),
  "POST /v1/projects/{project}/questions": step("recordQuestion", () => recordQuestion({ id: "q-1", text: "Which currency?" })),
  "POST /v1/projects/{project}/questions/{id}/dismiss": step("dismissQuestion", () => dismissQuestion("q-1")),
  "POST /v1/projects/{project}/questions/{id}/merge": step("mergeQuestion", () => mergeQuestion("q-1", "q-2")),
  "POST /v1/projects/{project}/questions/{id}/decide-now": step("decideQuestionNow", () => decideQuestionNow("q-1")),
  "POST /v1/projects/{project}/questions/{id}/answer": step("answerQuestion", () => answerQuestion("q-1", "Two retries.")),
  "POST /v1/projects/{project}/batches/{id}/answers": step("answerBatch", () => answerBatch("batch-1", [{ question: "q-1", accepted: true }])),
  "POST /v1/projects/{project}/decisions": step("recordDecision", () => recordDecision({ id: "d-1", status: "decided", statement: "Retry twice." })),
  "POST /v1/projects/{project}/decisions/{id}/overturn": step("overturnDecision", () => overturnDecision("d-1", { id: "d-2", statement: "Retry three times." })),
  "POST /v1/projects/{project}/approvals/{id}/decide": step("decideApproval", () => decideApproval("a-1", "digest", true, "")),
  "POST /v1/projects/{project}/approvals/{id}/repositories/{name}/retry": step("retryRepository", () => retryRepository("a-1", "api")),
  "POST /v1/projects/{project}/approvals/{id}/repositories/{name}/skip": step("skipRepository", () => skipRepository("a-1", "api", "built by hand")),
  "POST /v1/projects/{project}/effects/{id}/decide": step("decideEffect", () => decideEffect("effect-1", "retry", "")),
  "POST /v1/projects/{project}/work-orders/{id}/review": step("passReview", () => passReview("wo-1")),
  "POST /v1/projects/{project}/work-orders/{id}/send-back": step("sendBack", () => sendBack("wo-1", "because")),
  "POST /v1/projects/{project}/work-orders/{id}/accept": act("accept"),
  "POST /v1/projects/{project}/work-orders/{id}/rework": act("rework"),
  "POST /v1/projects/{project}/work-orders/{id}/reassign": act("reassign"),
  "POST /v1/projects/{project}/work-orders/{id}/resolve": act("resolve"),
  "POST /v1/projects/{project}/work-orders/{id}/replan": act("replan"),
  "POST /v1/projects/{project}/work-orders/{id}/cancel": act("cancel"),
  "POST /v1/projects/{project}/work-orders/{id}/retry": act("retry"),
  "POST /v1/projects/{project}/work-orders/{id}/claims/{claim}/release": step("releaseClaim", () => releaseClaim("wo-1", "claim-1", 3)),
  "POST /v1/projects/{project}/work-orders/{id}/lane-archive": step("archiveLane", () => archiveLane("wo-1", "lane-1", false)),
  "GET /v1/projects/{project}/work-orders/{id}/acceptance": step("readAcceptance", () => readAcceptance("wo-1")),
  "GET /v1/projects/{project}/work-orders/{id}/timeline": step("readTimeline", () => readTimeline("wo-1")),
  "GET /v1/projects/{project}/work-orders/{id}/estimate": step("readEstimate", () => readEstimate("wo-1")),
  "POST /v1/projects/{project}/work-orders/{id}/lane-reset": act("reset"),
  "POST /v1/projects/{project}/work-orders/{id}/lane-respawn": act("respawn"),
  "POST /v1/projects/{project}/work-orders/{id}/lane-resume": act("resume"),
  "GET /v1/projects/{project}/panes/{id}": step("followPane", () => followPane("lane-1")),
  "POST /v1/projects/{project}/panes/{id}/input": step("sendPaneKeys", () => sendPaneKeys("lane-1", "Shift-Tab")),
  "GET /v1/projects/{project}/panes/{id}/scrollback": step("readPaneScrollback", () => readPaneScrollback("lane-1", 200)),
  "POST /v1/projects/{project}/backup/download": step("downloadBackup", () => downloadBackup("tidewater")),
  "GET /v1/projects/{project}/audit": step("readAudit", () => readAudit("project", {})),
  "GET /v1/projects/{project}/outside-edits": step("readOutsideEdits", () => readOutsideEdits()),
  "GET /v1/projects/{project}/repositories": step("readRepositories", () => readRepositories()),
  "PATCH /v1/projects/{project}/repositories/{id}": step("changeRepository", () => changeRepository("repo-1", { planning: true })),
  "POST /v1/projects/{project}/repositories": step("addRepositories", () => addRepositories(["/work/api"], 1, true)),
  "POST /v1/projects/{project}/repositories/{id}/remove": step("removeRepository", () => removeRepository("repo-1")),
  "POST /v1/projects/{project}/adoption/plan": step("readAdoptionPlan", () => readAdoptionPlan("project", { root: "/work/api" })),
  "GET /v1/projects/{project}/fleet": step("readFleet", () => readFleet()),
  "PUT /v1/projects/{project}/fleet/mode": step("setFleetMode", () => setFleetMode("pause")),
  "PUT /v1/projects/{project}/fleet/caps": step("setFleetCaps", () => setFleetCaps({ wip_limit: 4, lanes_per_repository: 2, lanes_per_worker: 2 })),
  "GET /v1/projects/{project}/policies": step("readPolicies", () => readPolicies("recovery")),
  "POST /v1/projects/{project}/policies": step("publishPolicy", () => publishPolicy("[recovery_policy]")),
  "GET /v1/projects/{project}/spend": step("readSpend", () => readSpend()),
  "GET /v1/projects/{project}/categories": step("readCategories", () => readCategories()),
  "POST /v1/projects/{project}/categories": step("addCategory", () => addCategory("release-planning")),
  "GET /v1/projects/{project}/candidates": step("searchCandidates", () => searchCandidates("retry budget")),
  "GET /v1/projects/{project}/approvals": step("readApprovals", () => readApprovals("approved")),
  "GET /v1/projects/{project}/plans": step("readPlans", () => readPlans()),
  "GET /v1/projects/{project}/plans/{id}": step("readPlan", () => readPlan("plan-1")),
  "POST /v1/projects/{project}/plans/{id}/reviews": step("startContractReview", () => startContractReview("plan-1", "review-1")),
  "POST /v1/projects/{project}/plans/{id}/revision/confirm": step("confirmRevision", () => confirmRevision("plan-1")),
  "POST /v1/projects/{project}/plans/{id}/revision/sessions": step("reviseSession", () => reviseSession("plan-1", { id: "revise-1", repository: "repo-1" })),
  "POST /v1/projects/{project}/work-orders/{id}/decision-rework": step("reworkForDecisions", () => reworkForDecisions("wo-1", ["d-1"])),
  "POST /v1/projects/{project}/work-orders/{id}/ci-wait": step("waitLongerForCI", () => waitLongerForCI("wo-1", "45m")),
  "POST /v1/projects/{project}/work-orders/{id}/ci-rerun": step("rerunCI", () => rerunCI("wo-1")),
  "GET /v1/projects/{project}/work-orders/{id}/spend": step("readWorkOrderSpend", () => readWorkOrderSpend("wo-1")),
  "POST /v1/projects/{project}/work-orders/{id}/spend/raise": step("raiseSpendLimit", () => raiseSpendLimit("wo-1", { by_percent: 50 })),
  "POST /v1/projects/{project}/work-orders/{id}/spend/resume": step("resumeSpend", () => resumeSpend("wo-1")),
  "POST /v1/projects/{project}/work-orders/{id}/spend/block": step("blockSpend", () => blockSpend("wo-1")),
  "POST /v1/projects/{project}/work-orders/{id}/spend/replan": step("replanSpend", () => replanSpend("wo-1")),
  "GET /v1/projects/{project}/plans/{id}/forecast": step("readPlanForecast", () => readPlanForecast("plan-1")),
  "GET /v1/projects/{project}/measures/delivery": step("readDelivery", () => readDelivery("30d", "kind")),
  "GET /v1/projects/{project}/measures/estimates": step("readCalibration", () => readCalibration("30d")),
  "GET /v1/projects/{project}/measures/history": step("readDeliveryHistory", () => readDeliveryHistory()),
  "GET /v1/projects/{project}/measures/ci": step("readCIDurations", () => readCIDurations()),
  "GET /v1/projects/{project}/approvals/{id}": step("readApproval", () => readApproval("a-1")),
  "GET /v1/projects/{project}/lanes/{id}/outline": step("readOutline", () => readOutline("lane-1")),
  "GET /v1/projects/{project}/lanes/{id}/messages": step("readLaneMessages", () => readLaneMessages("lane-1")),
  "GET /v1/projects/{project}/notices": step("readNotices", () => readNotices()),
  "GET /v1/telemetry/lanes": step("readLaneTelemetry", () => readLaneTelemetry()),
  "GET /v1/projects/{project}/sources": step("readSources", () => readSources()),
  "POST /v1/projects/{project}/work-orders": step("createPlanWork", () => createPlanWork({ id: "wo-1", goal: "Saved searches", repository: "" })),
  "POST /v1/projects/{project}/work-orders/{id}/claims": step("claimAndStart", () => claimAndStart("wo-1", "worker-1")),
  "POST /v1/projects/{project}/work-orders/{id}/lanes": step("startClaimed", () => startClaimed("wo-1", "lane-1")),
  "GET /v1/projects/{project}/practices": step("listPractices", () => listPractices()),
  "POST /v1/projects/{project}/practices/checks": step("checkPractices", () => checkPractices([])),
  "GET /v1/projects/{project}/practices/checks/{request}": step("readPracticesCheck", () => readPracticesCheck("request-1")),
  "POST /v1/projects/{project}/practices/acknowledgements": step("acknowledgeFinding", () => acknowledgeFinding("repo-1", "codeowners", "the owners are the whole team")),
  "POST /v1/projects/{project}/practices/acknowledgements/withdraw": step("withdrawAcknowledgement", () => withdrawAcknowledgement("repo-1", "codeowners")),
  "GET /v1/projects/{project}/roster": step("readRoster", () => readRoster("project")),
  "GET /v1/projects/{project}/roster/history": step("readRosterHistory", () => readRosterHistory("project")),
  "POST /v1/projects/{project}/roster/changes": step("changeRoster", () => changeRoster("project", { op: "disable", agent: "reviewer" })),
  "POST /v1/projects/{project}/roster/revert": step("revertRoster", () => revertRoster("project", 1)),
  "POST /v1/projects/{project}/messages": step("sendMessage", () => sendMessage({ lane: "lane-1" }, "Go on.")),
  "GET /v1/projects/{project}/conversation-targets": step("readConversationTargets", () => readConversationTargets()),
  "GET /v1/projects/{project}/conversations": step("readConversations", () => readConversations()),
  "POST /v1/projects/{project}/conversations": step("createConversation", () => createConversation("chat-1")),
  "GET /v1/projects/{project}/conversations/{id}/entries": step("readConversationEntries", () => readConversationEntries("chat-1")),
  "POST /v1/projects/{project}/conversations/{id}/open": step("openConversation", () => openConversation("chat-1")),
  "POST /v1/projects/{project}/conversations/{id}/stop": step("stopConversation", () => stopConversation("chat-1")),
  "POST /v1/projects/{project}/conversations/{id}/rename": step("renameConversation", () => renameConversation("chat-1", "Plan")),
  "POST /v1/projects/{project}/conversations/{id}/delete": step("deleteConversation", () => deleteConversation("chat-1")),
  "POST /v1/projects/{project}/conversations/{id}/compact": step("compactConversation", () => compactConversation("chat-1")),
  "POST /v1/projects/{project}/conversations/{id}/clear": step("clearConversation", () => clearConversation("chat-1")),
  "POST /v1/projects/{project}/conversations/{id}/messages": step("sendToAssistant", () => sendToAssistant("chat-1", "Hello.")),
  "GET /v1/projects/{project}/lanes/{id}/stream": step("readLaneStream", () => readLaneStream("lane-1")),
  "GET /v1/projects/{project}/orchestrators/{worker}/stream": step("readOrchestratorStream", () => readOrchestratorStream("worker-1")),
  "GET /v1/projects/{project}/views/needs-you": view("needs-you"),
  "GET /v1/projects/{project}/views/roadmap": view("roadmap"),
  "GET /v1/projects/{project}/views/plans": view("plans"),
  "GET /v1/projects/{project}/views/documents": view("documents"),
  "GET /v1/projects/{project}/views/decisions": view("decisions"),
  "GET /v1/projects/{project}/views/questions": view("questions"),
  "GET /v1/projects/{project}/views/in-progress": view("in-progress"),
  "GET /v1/projects/{project}/views/on-hold": view("on-hold"),
  "GET /v1/projects/{project}/views/stuck": view("stuck"),
  "GET /v1/projects/{project}/views/completed": view("completed"),
  "GET /v1/projects/{project}/views/shop-floor": view("shop-floor"),
  "GET /v1/projects/{project}/views/operations": view("operations"),
  "GET /v1/projects/{project}/views/search": view("search"),
};

// uiGaps are the routes the listener serves that no client function calls yet, each with the slice of U2
// that adds the call. The first run of this test wrote the list.
const uiGaps: Record<string, string> = {
  "GET /v1/projects/{project}/conversations/{id}": "U2h2",
};

const gapSlice = /^(U2[a-k]|P[34][a-j])[12]?$/;

const key = (route: Route) => `${route.method} ${route.pattern}`;

// matcher is a route's pattern as an expression a call's path may match, with an ID or a project in any
// segment and a query after it.
const matcher = (route: Route) => new RegExp(`^${route.pattern.replaceAll(/\{[^}]+\}/g, "[^/?]+")}(\\?.*)?$`);

const served = routes as Route[];

describe("the routes the web listener serves", () => {
  it("each has a client function that calls it, or is a gap with the slice that closes it", () => {
    const missing = served.filter((route) => !(key(route) in callers) && !(key(route) in uiGaps)).map(key);
    expect(missing, "a route with no client function and no gap").toEqual([]);
    const unknown = [...Object.keys(callers), ...Object.keys(uiGaps)].filter((name) => !served.some((route) => key(route) === name));
    expect(unknown, "an entry that names no route of the list").toEqual([]);
  });

  it("have no gap that is also called, so a closed gap leaves the list", () => {
    expect(Object.keys(uiGaps).filter((name) => name in callers), "a gap with a caller: remove it from uiGaps").toEqual([]);
  });

  it("name the slice of U2 that closes each gap", () => {
    for (const [name, slice] of Object.entries(uiGaps)) {
      expect(slice, name).toMatch(gapSlice);
    }
  });
});

// FR-UI-9: the command palette has an action for every operation that changes something and has a control in the UI,
// sign-in and sign-out excepted. paletteActions names each one's action, and paletteActions.test.tsx opens the
// palette and looks for it.
const signing = new Set(["POST /v1/sessions", "POST /v1/sessions/end"]);

describe("the command palette", () => {
  const changing = served.filter((route) => route.method !== "GET" && key(route) in callers && !signing.has(key(route))).map(key);

  it("has an action for each route that changes something and has a UI caller", () => {
    expect(
      changing.filter((name) => !(name in paletteActions)),
      "a changing route with a caller and no palette action",
    ).toEqual([]);
  });

  it("names no action for a route that does not change anything, has no caller, or is the sign-in", () => {
    expect(
      Object.keys(paletteActions).filter((name) => !changing.includes(name)),
      "a palette action for a route that is not one of them",
    ).toEqual([]);
  });
});

describe("each client function the table names", () => {
  beforeEach(() => {
    stubEvents();
  });

  for (const route of served.filter((row) => key(row) in callers)) {
    it(`${key(route)} is reached by ${callers[key(route)]?.fn}`, async () => {
      const sent = serveApi(() => ({ status: 200, body: {} }));
      await Promise.resolve((callers[key(route)] as Caller).call()).catch(() => undefined);
      const calls = [...sent.map((call) => ({ method: call.method, url: call.url })), ...FakeEvents.opened.map((stream) => ({ method: "GET", url: stream.url }))];
      expect(
        calls.some((call) => call.method === route.method && matcher(route).test(call.url)),
        `${callers[key(route)]?.fn} sent ${calls.map((call) => `${call.method} ${call.url}`).join(", ") || "nothing"}`,
      ).toBe(true);
    });
  }
});

// The modules that hold the client functions. A function a component, a hook, or a flow imports is one the
// UI uses, and one only these import is a wrapper with no control.
const modules = new Set(["client.ts", "admin.ts", "delegates.ts", "measures.ts", "talk.ts", "runWork.ts", "projectRemote.ts", "practices.ts"]);
const skipped = new Set(["testing.ts", "test-setup.ts", "serve.ts"]);

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      return sources(path);
    }
    const name = entry.name;
    const code = (name.endsWith(".ts") || name.endsWith(".tsx")) && !name.includes(".test.") && !skipped.has(name);
    return code && !(modules.has(name) && relative(join(import.meta.dirname, ".."), dir) === "api") ? [path] : [];
  });
}

describe("the client functions", () => {
  it("are each imported by a component, a hook, or a flow, so none is a wrapper with no control", () => {
    const text = sources(join(import.meta.dirname, "..")).map((path) => readFileSync(path, "utf8"));
    const unused = [...new Set(Object.values(callers).map((caller) => caller.fn))].filter(
      (fn) => !text.some((source) => new RegExp(`import\\s+(?:type\\s+)?\\{[^}]*\\b${fn}\\b[^}]*\\}\\s+from`).test(source)),
    );
    expect(unused, "a client function no source outside the client modules imports").toEqual([]);
  });
});
