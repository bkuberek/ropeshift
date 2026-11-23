import { readLaneMessages } from "../api/admin";
import type { MessageList } from "../api/types";
import { useView } from "../api/useView";
import type { Live } from "../api/useView";
import { Banner, EmptyState, Id, Panel, SkeletonRows, StateBadge, TableRegion } from "../ui/display";
import { timeOf } from "../admin/parts";
import { deliveryStates } from "../floor/states";
import { words } from "../parts/parts";

// reasons say why a message ended where it did, in words a sender can act on. A reason this build has no words for
// still shows, as its own name.
const reasons: Record<string, string> = {
  timed_out: "Timed out before it was injected",
  generation_ended: "Its generation ended, and the sender decides whether to send it again",
  ledger_fact: "Never injected, so the next generation is told of it",
  undeliverable: "Not delivered: its session takes no message on this agent CLI version, and the sender decides whether to send it again",
  channel_unavailable:
    "Not delivered: its session had no channel for it just then, because it had no socket yet or its state could not be read, and the sender decides whether to send it again",
};

// LaneMessages lists what was sent to a lane, oldest first, as `ropeshift message list` does: each message with
// its kind, the generation it was written for, how far it got, and what the lane replied. A state claims only what
// its signal proves, so only a reply says the lane read a message. Every word of a body and a reply is text.
export function LaneMessages({ lane, live }: { lane: string; live: Live }) {
  const read = useView<MessageList>(() => readLaneMessages(lane), `lane-messages:${lane}`, live);
  const messages = read.view?.messages ?? [];
  return (
    <>
      {read.failure ? <Banner tone="danger">{read.view ? `The messages are not current. ${read.failure}` : read.failure}</Banner> : null}
      {!read.view && !read.failure ? (
        <div className="panel">
          <SkeletonRows rows={3} />
        </div>
      ) : null}
      {read.view && messages.length === 0 ? (
        <Panel title="Messages">
          <EmptyState icon="send" title="Nothing was sent to this lane">
            A message sent to this lane shows here with how far it got. Only a reply from the lane says it read one.
          </EmptyState>
        </Panel>
      ) : null}
      {messages.length > 0 ? (
        <Panel
          title="Messages"
          flush
          foot="Each state claims only what its signal proves: a write to a session shows that bytes were handed over, and only a reply says the lane read the message."
        >
          <TableRegion label="Messages">
            <table className="table stack-on-phone">
              <thead>
                <tr>
                  <th scope="col">Message</th>
                  <th scope="col">Kind</th>
                  <th scope="col" className="r">
                    Generation
                  </th>
                  <th scope="col">State</th>
                  <th scope="col">Sent</th>
                  <th scope="col">Body</th>
                  <th scope="col">Reply</th>
                </tr>
              </thead>
              <tbody>
                {messages.map((message) => (
                  <tr key={message.id}>
                    <td data-label="Message">
                      <Id value={message.id} />
                    </td>
                    <td data-label="Kind">{words(message.kind)}</td>
                    <td data-label="Generation" className="r num">
                      {message.to.generation}
                    </td>
                    <td data-label="State">
                      <StateBadge table={deliveryStates} state={message.state} />
                      {message.reason ? <span className="cell-note">{reasons[message.reason] ?? words(message.reason)}</span> : null}
                    </td>
                    <td data-label="Sent" className="num">
                      {timeOf(message.created_at)}
                    </td>
                    <td data-label="Body" className="message-text">
                      {message.body}
                    </td>
                    <td data-label="Reply" className={message.reply ? "message-text" : "hide-phone"}>
                      {message.reply}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableRegion>
        </Panel>
      ) : null}
    </>
  );
}
