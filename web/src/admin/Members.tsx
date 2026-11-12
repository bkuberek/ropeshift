import { readMembers } from "../api/admin";
import type { MemberList } from "../api/admin";
import { signOut } from "../api/client";
import type { Live } from "../api/useView";
import { Ago, Page, Until } from "../parts/parts";
import { Button } from "../ui/controls";
import { EmptyState, Panel, Refusal, TableRegion } from "../ui/display";
import { Reading, useChange, useRead } from "./parts";

// MembersPage lists who can act here: each member with a live session, when each began and ends. The
// first release has no roles, so every member can do everything (FR-AUTH-9).
export function MembersPage({ live, onSignedOut }: { live: Live; onSignedOut: () => void }) {
  const read = useRead<MemberList>(() => readMembers(), "members", live);
  const change = useChange();
  const list = read.view;
  return (
    <Page
      title="Members"
      note="Everyone who can sign in to this installation. The first release has no roles: every member can answer, decide, and change anything."
      actions={
        <Button icon="lock" loading={change.busy} onClick={() => void change.run(() => signOut()).then(() => onSignedOut())}>
          Sign out this session
        </Button>
      }
    >
      {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      <Reading failure={read.failure} ready={list !== null}>
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
            foot={`${list.enrolled === 1 ? "One member can" : `${list.enrolled} members can`} sign in. Adding and removing members, and signing out every session of one, are not served to this page yet; a session ends on its own when it expires.`}
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
    </Page>
  );
}
