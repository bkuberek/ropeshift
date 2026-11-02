package exit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/test/apitest"
)

const toolPayload = `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":"a command the hook must not forward"},"tool_response":{"stdout":"output"}}`

// hookRun is one `ropeshift hook` process.
type hookRun struct {
	exit    int
	stdout  string
	elapsed time.Duration
}

// hookEnv is the environment a lane's process gives its hooks.
type hookEnv struct {
	socket, spool, token, policy string
}

func (e hookEnv) list() []string {
	return []string{
		api.EnvWorkerSocket + "=" + e.socket, api.EnvSpool + "=" + e.spool, api.EnvLaneToken + "=" + e.token,
		api.EnvPolicy + "=" + e.policy, api.EnvLaneID + "=lane-1", api.EnvGeneration + "=3", api.EnvWorkOrder + "=wo",
	}
}

func runHook(t *testing.T, env hookEnv, event, input string) hookRun {
	t.Helper()
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	cmd := exec.Command(binaries.cli, "hook", event)
	cmd.Env = env.list()
	cmd.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	start := time.Now()
	err := cmd.Run()
	run := hookRun{stdout: stdout.String(), elapsed: time.Since(start)}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		run.exit = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return run
}

func newHookEnv(t *testing.T) hookEnv {
	t.Helper()
	dir := apitest.ShortDir(t)
	return hookEnv{socket: filepath.Join(dir, "w.sock"), spool: filepath.Join(dir, "spool.jsonl"), token: "a-token"}
}

func spoolLines(t *testing.T, path string) []lanes.HookEvent {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []lanes.HookEvent
	for line := range bytes.SplitSeq(bytes.TrimSpace(raw), []byte("\n")) {
		var event lanes.HookEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("a corrupt spool line %q: %v", line, err)
		}
		out = append(out, event)
	}
	return out
}

func TestHookExitsZeroWithTheWorkerDown(t *testing.T) {
	env := newHookEnv(t)
	run := runHook(t, env, "PostToolUse", toolPayload)
	if run.exit != 0 || run.stdout != "" {
		t.Fatalf("exit %d, stdout %q", run.exit, run.stdout)
	}
	lines := spoolLines(t, env.spool)
	if len(lines) != 1 {
		t.Fatalf("spool %+v", lines)
	}
	got := lines[0]
	if got.Event != "PostToolUse" || got.Subject != "toolu_1" || got.Tool != "Bash" || got.Lane != "lane-1" || got.Generation != 3 {
		t.Errorf("spooled %+v", got)
	}
	raw, err := os.ReadFile(env.spool)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), env.token) || strings.Contains(string(raw), "must not forward") {
		t.Errorf("the spool holds a secret or tool input: %s", raw)
	}
}

func TestHookExitsZeroWithAStaleSocket(t *testing.T) {
	env := newHookEnv(t)
	listener, err := net.Listen("unix", env.socket)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if run := runHook(t, env, "Stop", `{}`); run.exit != 0 {
		t.Fatalf("exit %d", run.exit)
	}
	if lines := spoolLines(t, env.spool); len(lines) != 1 {
		t.Errorf("spool %+v", lines)
	}
}

func TestHookExitsZeroWithTheWorkerHung(t *testing.T) {
	env := newHookEnv(t)
	listener, err := net.Listen("unix", env.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range held {
			_ = conn.Close()
		}
	}()
	run := runHook(t, env, "PostToolUse", toolPayload)
	if run.exit != 0 {
		t.Fatalf("exit %d", run.exit)
	}
	if lines := spoolLines(t, env.spool); len(lines) != 1 {
		t.Errorf("a hook that missed its ack spools: %+v", lines)
	}
}

func TestHookExitsZeroOnMalformedInput(t *testing.T) {
	env := newHookEnv(t)
	run := runHook(t, env, "Stop", "{not json")
	if run.exit != 0 || run.stdout != "" {
		t.Fatalf("exit %d, stdout %q", run.exit, run.stdout)
	}
	lines := spoolLines(t, env.spool)
	if len(lines) != 1 || !lines[0].Malformed {
		t.Errorf("spool %+v", lines)
	}
}

func TestHookExitsZeroWithNothingSet(t *testing.T) {
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	for _, args := range [][]string{{"hook"}, {"hook", "Stop"}} {
		cmd := exec.Command(binaries.cli, args...)
		cmd.Env = []string{}
		cmd.Stdin = strings.NewReader("")
		if err := cmd.Run(); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestParallelHooksDoNotCorruptTheSpool(t *testing.T) {
	env := newHookEnv(t)
	const writers, each = 8, 25
	var group sync.WaitGroup
	for writer := range writers {
		group.Go(func() {
			for i := range each {
				input := fmt.Sprintf(`{"session_id":"s1","tool_name":"Bash","tool_use_id":"toolu_%d_%d"}`, writer, i)
				if run := runHook(t, env, "PostToolUse", input); run.exit != 0 {
					t.Errorf("exit %d", run.exit)
				}
			}
		})
	}
	group.Wait()
	if lines := spoolLines(t, env.spool); len(lines) != writers*each {
		t.Errorf("%d lines", len(lines))
	}
}

func TestSessionStartRegroundsAfterALossOfContext(t *testing.T) {
	for source, want := range map[string]bool{"startup": false, "compact": true, "clear": true, "resume": true} {
		t.Run(source, func(t *testing.T) {
			run := runHook(t, newHookEnv(t), "SessionStart", `{"session_id":"s1","source":"`+source+`"}`)
			if run.exit != 0 {
				t.Fatalf("exit %d", run.exit)
			}
			if !want {
				if run.stdout != "" {
					t.Errorf("stdout %q", run.stdout)
				}
				return
			}
			var output struct {
				Specific struct {
					Event   string `json:"hookEventName"`
					Context string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(run.stdout), &output); err != nil {
				t.Fatalf("%q: %v", run.stdout, err)
			}
			parts := []string{"lane-1", "wo"}
			if source != lanes.SourceClear {
				parts = append(parts, "generation 3")
			}
			for _, part := range parts {
				if !strings.Contains(output.Specific.Context, part) {
					t.Errorf("context %q", output.Specific.Context)
				}
			}
			if output.Specific.Event != "SessionStart" {
				t.Errorf("event %q", output.Specific.Event)
			}
		})
	}
}

func deniedBy(t *testing.T, stdout string) string {
	t.Helper()
	if stdout == "" {
		return ""
	}
	var output struct {
		Specific struct {
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("%q: %v", stdout, err)
	}
	if output.Specific.Decision != "deny" {
		t.Fatalf("decision %q", output.Specific.Decision)
	}
	return output.Specific.Reason
}

func TestPreToolUseDeniesWithTheWorkerDown(t *testing.T) {
	env := newHookEnv(t)
	env.policy = filepath.Join(t.TempDir(), "policy.json")
	rules := `{"deny":[{"tool":"Bash","command_prefix":"git push --force","reason":"a force push is denied"}]}`
	if err := os.WriteFile(env.policy, []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	denied := `{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"git push --force origin main"}}`
	if run := runHook(t, env, "PreToolUse", denied); run.exit != 0 || deniedBy(t, run.stdout) != "a force push is denied" {
		t.Errorf("exit %d, stdout %q", run.exit, run.stdout)
	}
	allowed := `{"tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"git status"}}`
	if run := runHook(t, env, "PreToolUse", allowed); run.exit != 0 || run.stdout != "" {
		t.Errorf("exit %d, stdout %q", run.exit, run.stdout)
	}
}

func TestAPolicyThatCannotBeReadDenies(t *testing.T) {
	env := newHookEnv(t)
	env.policy = filepath.Join(t.TempDir(), "absent.json")
	run := runHook(t, env, "PreToolUse", `{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	if run.exit != 0 || deniedBy(t, run.stdout) == "" {
		t.Errorf("exit %d, stdout %q", run.exit, run.stdout)
	}
}
