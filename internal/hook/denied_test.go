package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
)

// forwarded runs one hook and returns what reached the worker's socket.
func forwarded(t *testing.T, cfg config, input string) (lanes.HookEvent, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "w.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	received := make(chan lanes.Request, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		var request lanes.Request
		if json.Unmarshal(line, &request) == nil {
			received <- request
		}
		raw, err := json.Marshal(lanes.Reply{OK: true})
		if err == nil {
			_, _ = conn.Write(append(raw, '\n'))
		}
	}()
	cfg.socket, cfg.token = socket, "a-token"
	var out bytes.Buffer
	event, raw := take(cfg, strings.NewReader(input))
	run(cfg, event, raw, &out)
	select {
	case request := <-received:
		if request.Hook == nil {
			t.Fatalf("the worker got %+v", request)
		}
		return *request.Hook, out.String()
	case <-time.After(5 * time.Second):
		t.Fatal("nothing reached the worker")
		return lanes.HookEvent{}, ""
	}
}

func policyFile(t *testing.T, policy lanes.DenyPolicy) string {
	t.Helper()
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A refused call never reaches `PostToolUse`, so the worker has to hear that it was refused, in the
// event that would otherwise read as a tool at work.
func TestARefusedCallReachesTheWorkerAsRefused(t *testing.T) {
	bash := `{"tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":"go test"}}`
	question := `{"tool_name":"AskUserQuestion","tool_use_id":"toolu_1","tool_input":{"questions":[]}}`
	tests := []struct {
		name   string
		policy string
		input  string
		want   string
	}{
		{"a denial the policy names", policyFile(t, lanes.DenyPolicy{Deny: []lanes.DenyRule{{Tool: "Bash", Reason: "no"}}}), bash, lanes.DetailDenied},
		{"a hold", policyFile(t, lanes.DenyPolicy{Hold: "hand off"}), bash, lanes.DetailDenied},
		{"the question tool in a pane", policyFile(t, lanes.DenyPolicy{QuestionTool: lanes.QuestionToolDeny}), question, lanes.DetailDenied},
		{"a policy file that cannot be read", filepath.Join(t.TempDir(), "gone.json"), bash, lanes.DetailDenied},
		{"a policy that does not parse", func() string {
			path := filepath.Join(t.TempDir(), "broken.json")
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}(), bash, lanes.DetailDenied},
		{"a call that passes", policyFile(t, lanes.DenyPolicy{}), bash, ""},
		{"a deferred question", policyFile(t, lanes.DenyPolicy{QuestionTool: lanes.QuestionToolDefer}), question, ""},
		{"a session with no policy", "", bash, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, _ := forwarded(t, config{event: eventPreToolUse, policy: tt.policy}, tt.input)
			if event.Detail != tt.want {
				t.Errorf("the worker heard %q", event.Detail)
			}
			if event.Subject != "toolu_1" {
				t.Errorf("the call reads %q", event.Subject)
			}
		})
	}
}

// A `PermissionRequest` names no call, so a digest is the only thing that ties one to its start.
func TestACallThatStartsNamesItsDigest(t *testing.T) {
	input := `{"tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":"git tag v1"}}`
	want, err := authority.ReadToolCall("Bash", []byte(`{"command":"git tag v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	event, _ := forwarded(t, config{event: eventPreToolUse}, input)
	if event.Digest != want.Digest {
		t.Errorf("the digest reads %q, want %q", event.Digest, want.Digest)
	}
}

// The error of a failed call is the output of the tool, which does not leave the session. The event
// has to say only which call ended.
func TestAFailedCallTellsTheWorkerOnlyWhichCallEnded(t *testing.T) {
	input := `{"tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":"go test"},"error":"Exit code 1\nthe secret text"}`
	want, err := authority.ReadToolCall("Bash", []byte(`{"command":"go test"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{eventPostToolUseFailure, eventPermissionDenied} {
		t.Run(event, func(t *testing.T) {
			got, _ := forwarded(t, config{event: event}, strings.Replace(input, `"error"`, `"reason"`, 1))
			if event == eventPostToolUseFailure {
				got, _ = forwarded(t, config{event: event}, input)
			}
			if got.Detail != "" || got.Subject != "toolu_1" || got.Digest != want.Digest {
				t.Errorf("the worker heard %+v", got)
			}
		})
	}
}

// FR-LANE-19: a file program runs with no prompt, so the hook reads the disk for a link on its way, which
// the text of a path cannot show, and denies the call before the CLI's allow rule runs it.
func TestTheHookDeniesAFileProgramThroughALink(t *testing.T) {
	root := t.TempDir()
	worktree, outside := filepath.Join(root, "lane-1"), filepath.Join(root, "kept")
	for _, dir := range []string{worktree, outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "out")); err != nil {
		t.Fatal(err)
	}
	policy := policyFile(t, lanes.CompilePolicy(lanes.PolicySpec{Worktree: worktree}))
	for command, want := range map[string]string{"rm -r out/": lanes.ReasonLink, "rm -f notes.md": ""} {
		input, err := json.Marshal(map[string]any{"tool_name": "Bash", "cwd": worktree, "tool_input": map[string]string{"command": command}})
		if err != nil {
			t.Fatal(err)
		}
		_, answer := decide(config{event: eventPreToolUse, policy: policy}, input)
		if !strings.Contains(string(answer), want) || (want == "") != (len(answer) == 0) {
			t.Errorf("%q: the hook answered %s", command, answer)
		}
	}
}

// The hook reads the path a search names, so a search outside a session's read roots is denied too.
func TestTheHookDeniesASearchOutsideTheReadRoots(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "policy.json")
	raw, err := json.Marshal(lanes.CompilePolicy(lanes.PolicySpec{Worktree: "/run/a/session", ReadRoots: []string{"/run/a/session"}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config{event: eventPreToolUse, policy: policy}
	for _, input := range []string{
		`{"tool_name":"Grep","cwd":"/run/a/session","tool_input":{"pattern":"token","path":"/home/x/.config"}}`,
		`{"tool_name":"Glob","cwd":"/run/a/session","tool_input":{"pattern":"*","path":"/state"}}`,
		`{"tool_name":"Read","cwd":"/run/a/session","tool_input":{"file_path":"/proc/1/environ"}}`,
	} {
		if verdict, answer := decide(cfg, []byte(input)); verdict != callDenied || !strings.Contains(string(answer), "deny") {
			t.Errorf("%s: %v %s", input, verdict, answer)
		}
	}
	if verdict, _ := decide(cfg, []byte(`{"tool_name":"Grep","cwd":"/run/a/session","tool_input":{"pattern":"x"}}`)); verdict == callDenied {
		t.Error("a search where the session stands was denied")
	}
}
