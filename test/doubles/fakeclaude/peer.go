package main

import "fmt"

// peerShapeOrigin is the shape a peer's lines take on Claude Code 2.1.330 and later. The CLI gives the
// replay of a socket message a `uuid` of its own and names the sender's ID only as `origin.msg_id`, on a
// message that names a reply address, which is also the replay's `origin.from`. A message with no reply
// address replays with `origin.from` `unknown` and no `origin.msg_id`. A hold line carries a
// `message_uuid` of the CLI's own and the reply address as `from`. The fixture's `peer_shape` selects
// it, and the empty value is the shape of 2.1.316 to 2.1.319, whose replay carries the sender's `uuid`.
const peerShapeOrigin = "origin"

// unknownSender is what the CLI names a replay's `origin.from` when the message gave no reply address.
const unknownSender = "unknown"

// cliID makes an ID the CLI gives a line of its own, which no sender chose.
func (s *session) cliID() string {
	s.cliIDs++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.cliIDs)
}

// peerReplayLine is the headless acknowledgement of a socket message, with a peer's origin.
func (s *session) peerReplayLine(line string) map[string]any {
	origin := map[string]any{"kind": "peer"}
	replay := map[string]any{
		"type": "user", "isReplay": true, "uuid": messageID(line), "origin": origin,
		"message": map[string]any{"role": "user", "content": messageText(line)},
	}
	if s.script.PeerShape != peerShapeOrigin {
		// A build that changed the line answers with an ID of its own.
		if dropped("uuid") {
			replay["uuid"] = "00000000-0000-4000-8000-00000000beef"
		}
		return replay
	}
	from, msgID := messageFrom(line)
	replay["uuid"], replay["isSynthetic"] = s.cliID(), true
	origin["from"] = from
	if from == "" || dropped("origin_from") {
		origin["from"] = unknownSender
	}
	if msgID != "" && !dropped("msg_id") {
		origin["msg_id"] = msgID
	}
	return replay
}

// peerHoldLine is what a session that holds a message writes to its stream.
func (s *session) peerHoldLine(line string) map[string]any {
	hold := map[string]any{"type": "system", "subtype": "peer_message_hold", "state": "held", "message_uuid": messageID(line)}
	if s.script.PeerShape == peerShapeOrigin {
		from, _ := messageFrom(line)
		if dropped("hold_from") {
			from = ""
		}
		hold["message_uuid"], hold["from"], hold["cause"] = s.cliID(), from, "explicit-setting"
	}
	return hold
}
