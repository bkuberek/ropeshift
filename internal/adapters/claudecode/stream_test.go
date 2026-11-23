package claudecode

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// A replay names the input it confirms. A peer's replay on a version that keeps the sender's `uuid` names that, and on a
// version that gives it a `uuid` of its own the sender's ID is `origin.msg_id`, which the message carries only when it
// names a reply address.
func TestAReplayNamesTheInputItConfirms(t *testing.T) {
	for _, tt := range []struct {
		name string
		line string
		want string
	}{
		{
			name: "a peer's replay that keeps the sender's uuid",
			line: `{"type":"user","isReplay":true,"uuid":"sent-1","origin":{"kind":"peer"},"message":{"role":"user","content":"hello"}}`,
			want: "sent-1",
		},
		{
			name: "a peer's replay with a uuid of the CLI's own names the sender's ID in its origin",
			line: `{"type":"user","isReplay":true,"isSynthetic":true,"uuid":"cli-1","origin":{"kind":"peer","from":"uds:/r1.sock","msg_id":"sent-1"},"message":{"role":"user","content":"hello"}}`,
			want: "sent-1",
		},
		{
			name: "a peer's replay whose origin names another reply address is not the sender's",
			line: `{"type":"user","isReplay":true,"isSynthetic":true,"uuid":"cli-1","origin":{"kind":"peer","from":"uds:/other.sock","msg_id":"sent-1"},"message":{"role":"user","content":"hello"}}`,
			want: "cli-1",
		},
		{
			name: "a peer's replay of a message with no reply address names no sender's ID",
			line: `{"type":"user","isReplay":true,"isSynthetic":true,"uuid":"cli-1","origin":{"kind":"peer","from":"unknown"},"message":{"role":"user","content":"hello"}}`,
			want: "cli-1",
		},
		{
			name: "a replay of the worker's own stdin line",
			line: `{"type":"user","isReplay":true,"uuid":"stdin-1","message":{"role":"user","content":"hello"}}`,
			want: "stdin-1",
		},
		{
			name: "an origin of another shape costs the line nothing",
			line: `{"type":"user","isReplay":true,"uuid":"cli-1","origin":"peer","message":{"role":"user","content":"hello"}}`,
			want: "cli-1",
		},
		{
			name: "an origin that is not a peer's names no sender's ID",
			line: `{"type":"user","isReplay":true,"uuid":"cli-1","origin":{"kind":"human","msg_id":"sent-1"},"message":{"role":"user","content":"hello"}}`,
			want: "cli-1",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := parseLineOf([]byte(tt.line), false, false, "uds:/r1.sock")
			if err != nil {
				t.Fatal(err)
			}
			if p.event.Kind != ports.EventInputAdmitted || p.event.InputID != tt.want {
				t.Errorf("the line confirms %+v, want input %q", p.event, tt.want)
			}
		})
	}
}

// The sender's ID appears on lines that are not the replay too: the turn's `result` carries the same origin. Only a
// user line the CLI marks as a replay confirms an input, so a turn's end never does.
func TestOnlyAReplayConfirmsAnInput(t *testing.T) {
	for _, tt := range []struct{ name, line string }{
		{"a result that carries the sender's origin", `{"type":"result","subtype":"success","uuid":"cli-2","origin":{"kind":"peer","from":"uds:/r1.sock","msg_id":"sent-1"}}`},
		{"a user line that is no replay", `{"type":"user","uuid":"cli-1","origin":{"kind":"peer","from":"uds:/r1.sock","msg_id":"sent-1"}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := parseLine([]byte(tt.line), false)
			if err != nil {
				t.Fatal(err)
			}
			if p.event.Kind == ports.EventInputAdmitted {
				t.Errorf("the line confirms %+v", p.event)
			}
		})
	}
}
