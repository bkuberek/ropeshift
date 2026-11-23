package claudecode

import "testing"

// A `peer_message_hold` line counts only when it says the session holds the message: a line that says the message was
// dropped, with the message's own ID or with the reply address, is no evidence of a hold.
func TestOnlyAHeldLineShowsAHold(t *testing.T) {
	const id, from = "sent-1", "uds:/r1.sock"
	for _, tt := range []struct {
		name      string
		line      probeLine
		receipted bool
		want      bool
	}{
		{"a held line that names the message's ID", probeLine{MessageUUID: id, State: holdHeld}, false, true},
		{"a dropped line that names the message's ID", probeLine{MessageUUID: id, State: "dropped"}, false, false},
		{"a held line for the reply address, with the session's receipt", probeLine{MessageUUID: "cli-1", State: holdHeld, From: from}, true, true},
		{"a dropped line for the reply address, with the session's receipt", probeLine{MessageUUID: "cli-1", State: "dropped", From: from}, true, false},
		{"a held line for the reply address, with no receipt", probeLine{MessageUUID: "cli-1", State: holdHeld, From: from}, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.line.Type, tt.line.Subtype = "system", "peer_message_hold"
			s := &probeSession{lines: []probeLine{tt.line}}
			if tt.receipted {
				s.receipted = []string{id}
			}
			if got := s.held(id, from); got != tt.want {
				t.Errorf("held = %v, want %v", got, tt.want)
			}
		})
	}
}
