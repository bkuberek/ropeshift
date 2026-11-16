// The command palette's action for every route that changes something and has a control in the web UI, sign-in and
// sign-out excepted (FR-UI-9). Each names the title the action starts with, and the address of a page whose palette
// lists it, so a test can open the palette there and look. parity.test.ts holds this table to the routes the
// listener serves and the client functions that call them, and paletteActions.test.tsx holds it to the palette.
// keyboard.test.ts runs each action in a browser, from the default view, and reaches the control by keys alone.

export type PaletteAction = {
  title: string;
  at: string;
  // query is what a record's action needs typed before the palette finds it.
  query?: string;
  // elsewhere names the test, beside the source, that opens the palette where the action stands, when that needs a
  // stream or a terminal this table's test does not serve.
  elsewhere?: string;
};

const home = "/needs-you";
const project = "/projects/default";

const decide: PaletteAction = { title: "Decide: ", at: home };
const autonomy: PaletteAction = { title: "Settings: Autonomy and run windows", at: home };

export const paletteActions: Record<string, PaletteAction> = {
  "PUT /v1/passphrase": { title: "Settings: Change passphrase", at: home },
  "PUT /v1/workers/{id}/orchestrator": { title: "Start orchestrator", at: home },
  "POST /v1/projects": { title: "Create project", at: home },
  "POST /v1/projects/{project}/rename": { title: "Rename project: ", at: home },
  "POST /v1/projects/{project}/archive": { title: "Archive project: ", at: home },
  "POST /v1/projects/{project}/restore": { title: "Restore project: ", at: home },
  "POST /v1/projects/{project}/remove": { title: "Remove project: ", at: home },
  "POST /v1/projects/import/upload": { title: "Import a project from a backup", at: home },
  "POST /v1/installation/backup/download": { title: "Back up the installation", at: home },
  "POST /v1/projects/{project}/backup/download": { title: "Back up project: ", at: home },
  "POST /v1/workers/tokens": { title: "Create join token", at: home },
  "PUT /v1/workers/{id}/autonomy": autonomy,
  "POST /v1/workers/{id}/autonomy/acknowledgement": autonomy,
  "POST /v1/workers/{id}/windows": autonomy,
  "DELETE /v1/workers/{id}/windows": autonomy,
  "PUT /v1/workers/{id}/caps": { title: "Settings: Admission and lane caps", at: home },
  "POST /v1/sessions/end-all": { title: "Sign out all sessions", at: home },
  "POST /v1/delegates": { title: "Create delegate", at: home },
  "POST /v1/delegates/{id}/revoke": { title: "Revoke a delegate", at: home },
  "POST /v1/delegates/{id}/remove": { title: "Remove a delegate", at: home },
  "POST /v1/roster/changes": { title: "Installation agents: add an agent", at: home },
  "POST /v1/roster/revert": { title: "Installation agents: restore a version", at: home },
  "POST /v1/projects/{project}/roster/changes": { title: "Add agent", at: home },
  "POST /v1/projects/{project}/roster/revert": { title: "Agents: restore a version", at: home },
  "POST /v1/projects/{project}/questions": { title: "Record question", at: home },
  "POST /v1/projects/{project}/questions/{id}/dismiss": { title: "Dismiss question: ", at: `${project}/questions` },
  "POST /v1/projects/{project}/questions/{id}/answer": { title: "Answer question: ", at: `${project}/questions` },
  "POST /v1/projects/{project}/decisions": { title: "Record decision", at: home },
  "POST /v1/projects/{project}/decisions/{id}/overturn": { title: "Overturn decision: ", at: `${project}/decisions` },
  "POST /v1/projects/{project}/approvals/{id}/decide": decide,
  "POST /v1/projects/{project}/effects/{id}/decide": decide,
  "POST /v1/projects/{project}/work-orders/{id}/review": decide,
  "POST /v1/projects/{project}/work-orders/{id}/send-back": { title: "Send back to planning: ", at: home, query: "send back" },
  "POST /v1/projects/{project}/work-orders/{id}/accept": decide,
  "POST /v1/projects/{project}/work-orders/{id}/rework": decide,
  "POST /v1/projects/{project}/work-orders/{id}/reassign": { title: "End this claim: ", at: home, query: "end this claim" },
  "POST /v1/projects/{project}/work-orders/{id}/resolve": { title: "Back to ready: ", at: home, query: "back to ready" },
  "POST /v1/projects/{project}/work-orders/{id}/replan": { title: "Back to planning: ", at: home, query: "back to planning" },
  "POST /v1/projects/{project}/work-orders/{id}/cancel": { title: "Cancel this work: ", at: home, query: "cancel this work" },
  "POST /v1/projects/{project}/work-orders/{id}/lane-reset": decide,
  "POST /v1/projects/{project}/work-orders/{id}/lane-respawn": decide,
  "POST /v1/projects/{project}/work-orders/{id}/lane-resume": decide,
  "POST /v1/projects/{project}/work-orders/{id}/lane-archive": { title: "Archive the worktree of lane ", at: `${project}/work-order?id=wo-done` },
  "POST /v1/projects/{project}/work-orders/{id}/retry": { title: "Retry: ", at: home, query: "retry" },
  "POST /v1/projects/{project}/work-orders/{id}/claims/{claim}/release": { title: "Release the claim: ", at: home, query: "release the claim" },
  "POST /v1/projects/{project}/work-orders/{id}/claims": { title: "Start work: ", at: home, query: "start work" },
  "POST /v1/projects/{project}/work-orders/{id}/lanes": { title: "Start lane: ", at: home, query: "start lane" },
  "POST /v1/projects/{project}/work-orders": { title: "New work order", at: home },
  "POST /v1/projects/{project}/panes/{id}/input": { title: "Send keys to the pane of lane ", at: `${project}/lane?id=lane-wo-running`, elsewhere: "lanes/LanePane.test.tsx" },
  "PATCH /v1/projects/{project}/repositories/{id}": { title: "Change repository: ", at: `${project}/repositories` },
  "POST /v1/projects/{project}/repositories": { title: "Register repositories", at: home },
  "POST /v1/projects/{project}/repositories/{id}/remove": { title: "Remove repository: ", at: `${project}/repositories` },
  "PUT /v1/projects/{project}/fleet/mode": { title: "Pause or drain the fleet", at: home },
  "PUT /v1/projects/{project}/fleet/caps": { title: "Set the fleet's caps", at: home },
  "POST /v1/projects/{project}/policies": { title: "Settings: Estimate policy", at: home },
  "POST /v1/projects/{project}/categories": { title: "Add category", at: home },
  "POST /v1/projects/{project}/plans/{id}/reviews": { title: "Start contract review", at: home },
  "POST /v1/projects/{project}/messages": { title: "Switch conversation target", at: home },
  "POST /v1/projects/{project}/conversations": { title: "New assistant conversation", at: home },
  "POST /v1/projects/{project}/conversations/{id}/open": { title: "Resume the assistant session", at: `${project}/conversations` },
  "POST /v1/projects/{project}/conversations/{id}/stop": { title: "Stop the assistant session", at: `${project}/conversations` },
  "POST /v1/projects/{project}/conversations/{id}/rename": { title: "Rename the conversation", at: `${project}/conversations` },
  "POST /v1/projects/{project}/conversations/{id}/delete": { title: "Delete the conversation", at: `${project}/conversations` },
  "POST /v1/projects/{project}/conversations/{id}/compact": { title: "Compact the conversation's context", at: `${project}/conversations` },
  "POST /v1/projects/{project}/conversations/{id}/clear": { title: "Clear the conversation's context", at: `${project}/conversations` },
  "POST /v1/projects/{project}/conversations/{id}/messages": { title: "Message the assistant", at: home },
};
