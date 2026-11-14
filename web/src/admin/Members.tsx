import { useEffect, useState } from "react";

import { readMembers } from "../api/admin";
import type { MemberList } from "../api/admin";
import { signOut } from "../api/client";
import { signOutAll } from "../api/delegates";
import type { Live } from "../api/useView";
import { Ago, Page, Until } from "../parts/parts";
import { Button } from "../ui/controls";
import { EmptyState, Panel, Refusal, TableRegion } from "../ui/display";
import { Dialog } from "../ui/overlays";
import { DelegatesPanel } from "./Delegates";
import { Reading, useChange, useRead } from "./parts";

// MembersPage lists who can act here: each member with a live session, when each began and ends. The
// first release has no roles, so every member can do everything (FR-AUTH-9).
export function MembersPage({ live, onSignedOut, dialog = "" }: { live: Live; onSignedOut: () => void; dialog?: string }) {
  const read = useRead<MemberList>(() => readMembers(), "members", live);
  const change = useChange();
  const [creating, setCreating] = useState(dialog === "delegate");
  const [leaving, setLeaving] = useState(dialog === "sign-out-all");
  // The palette opens a dialog of this page by its address, while the page may already be open.
  useEffect(() => {
    if (dialog === "delegate") {
      setCreating(true);
    }
    if (dialog === "sign-out-all") {
      setLeaving(true);
    }
  }, [dialog]);
  const list = read.view;
  return (
    <Page
      title="Members"
      note="Everyone who can sign in to this installation. The first release has no roles: every member can answer, decide, and change anything."
      actions={
        <>
          <Button icon="lock" onClick={() => setLeaving(true)}>
            Sign out all sessions
          </Button>
          <Button icon="lock" loading={change.busy} onClick={() => void change.run(() => signOut()).then(() => onSignedOut())}>
            Sign out this session
          </Button>
        </>
      }
    >
      {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      <Reading failure={read.failure} ready={list !== null} asOf={list?.as_of} readAt={read.readAt}>
        {list && list.members.length === 0 ? (
          <div className="panel">
            <EmptyState icon="users" title="No member holds a session">
              A member signs in with the installation's passphrase, in this page or with ropeshift login.
            </EmptyState>
          </div>
        ) : null}
        {list && list.members.length > 0 ? (
          <Panel
            flush
            title="Members with a live session"
            foot={`${list.enrolled === 1 ? "One member can" : `${list.enrolled} members can`} sign in. Adding and removing members is not served to this page yet. A session ends on its own when it expires, and Sign out all sessions ends every one of yours.`}
          >
            <TableRegion label="Members">
              <table className="table stack-on-phone">
                <thead>
                  <tr>
                    <th scope="col">Member</th>
                    <th scope="col" className="r">
                      Sessions
                    </th>
                    <th scope="col">Last sign-in</th>
                    <th scope="col">Next to expire</th>
                  </tr>
                </thead>
                <tbody>
                  {list.members.map((member) => {
                    const soonest = Math.min(...member.sessions.map((session) => session.expires_at));
                    return (
                      <tr key={member.id}>
                        <td data-label="Member">
                          <strong className="mono">{member.id}</strong>
                        </td>
                        <td data-label="Sessions" className="r num">
                          {member.sessions.length}
                        </td>
                        <td data-label="Last sign-in">{member.last_sign_in ? <Ago at={member.last_sign_in} /> : null}</td>
                        <td data-label="Next to expire">{Number.isFinite(soonest) ? <Until at={soonest} /> : null}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </TableRegion>
          </Panel>
        ) : null}
      </Reading>
      <DelegatesPanel live={live} creating={creating} onCreating={setCreating} />
      <Dialog
        open={leaving}
        title="Sign out all sessions"
        onClose={() => setLeaving(false)}
        footer={
          <>
            <span className="spacer" />
            <Button onClick={() => setLeaving(false)}>Cancel</Button>
            <Button variant="primary" loading={change.busy} onClick={() => void change.run(() => signOutAll()).then((ended) => ended && onSignedOut())}>
              Sign out everywhere
            </Button>
          </>
        }
      >
        <p className="small">This ends every session of yours, in every browser and shell, this one with them, and revokes every delegate credential of yours. An agent acting with one can do nothing more until you make it a new one.</p>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </Dialog>
    </Page>
  );
}
