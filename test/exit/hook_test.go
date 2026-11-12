package exit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/test/apitest"
)

const toolPayload = `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":"a command the hook must not forward"},"tool_response":{"stdout":"output"}}`

// hookRun is one `ropeshift hook` process.
type hookRun struct {
	exit    int
	stdout  string
	elapsed time.Duration
}

// hookToken is the token a hook here runs with.
const hookToken = "a-token"

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
	return hookEnv{socket: filepath.Join(dir, "w.sock"), spool: filepath.Join(dir, "spool.jsonl"), token: hookToken}
}

func spoolLines(t *testing.T, path string) []lanes.HookEvent {
	t.Helper()
	return spoolLinesSealedWith(t, path, hookToken)
}

// spoolLinesSealedWith reads a spool whose lines a hook sealed with token.
func spoolLinesSealedWith(t *testing.T, path, token string) []lanes.HookEvent {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []lanes.HookEvent
	digestOf := func(string) (string, error) { return lanes.TokenDigest(token), nil }
	for line := range bytes.SplitSeq(bytes.TrimSpace(raw), []byte("\n")) {
		event, err := lanes.OpenHook(line, digestOf)
		if err != nil {
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
	if strings.Contains(string(raw), env.token) || strings.Contains(string(raw), lanes.TokenDigest(env.token)) || strings.Contains(string(raw), "must not forward") {
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

// A loaded machine can hold a hook past its own deadline before its spool line is down, and the
// deadline must not end the hook first, because delivery is at least once. A spool that is a FIFO no
// one reads holds the write open past the deadline, the way a stall does, until this test reads it.
func TestAHookPastItsDeadlineStillSpools(t *testing.T) {
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	env := newHookEnv(t)
	if err := syscall.Mkfifo(env.spool, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaries.cli, "hook", "PostToolUse")
	cmd.Env = env.list()
	cmd.Stdin = strings.NewReader(toolPayload)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// Far past the hook's deadline, a hook the deadline ended has exited, and one that waits for its
	// line is still opening the FIFO.
	select {
	case err := <-exited:
		t.Fatalf("the hook ended before its spool line was down: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	opened := make(chan *os.File, 1)
	go func() {
		if reader, err := os.Open(env.spool); err == nil {
			opened <- reader
		}
	}()
	// An open that met no writer returns once one comes.
	t.Cleanup(func() {
		if writer, err := os.OpenFile(env.spool, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = writer.Close()
		}
	})
	var reader *os.File
	select {
	case reader = <-opened:
	case err := <-exited:
		t.Fatalf("the hook ended before its spool line was down: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the hook never opened its spool")
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-exited; err != nil {
		t.Errorf("the hook: %v", err)
	}
	digestOf := func(string) (string, error) { return lanes.TokenDigest(hookToken), nil }
	if event, err := lanes.OpenHook(bytes.TrimSpace(raw), digestOf); err != nil || event.Subject != "toolu_1" {
		t.Errorf("spooled %q: %+v, %v", raw, event, err)
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

// The calls a policy hook decides past its deadline: one it passes, one it denies, and the question
// tool, which it defers.
const (
	passedCall   = `{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"git status"}}`
	deniedCall   = `{"tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"git push --force origin main"}}`
	deferredCall = `{"tool_name":"AskUserQuestion","tool_use_id":"t3","tool_input":{"questions":[{"question":"Which budget?","options":[{"label":"Three"},{"label":"Five"}]}]}}`
)

// heldPastTheDeadline runs one PreToolUse hook on a policy that denies a force push and defers the
// question tool, and returns what it printed. Its spool is a FIFO no one reads, so handing on its
// event holds it past its deadline, the way a stall on a loaded machine does, until this reads it.
func heldPastTheDeadline(t *testing.T, input string) string {
	t.Helper()
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	env := newHookEnv(t)
	env.policy = filepath.Join(t.TempDir(), "policy.json")
	rules := `{"deny":[{"tool":"Bash","command_prefix":"git push --force","reason":"a force push is denied"}],"question_tool":"defer"}`
	if err := os.WriteFile(env.policy, []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(env.spool, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaries.cli, "hook", "PreToolUse")
	cmd.Env = env.list()
	cmd.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		t.Fatalf("the hook ended before its event was down: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	opened := make(chan *os.File, 1)
	go func() {
		if reader, err := os.Open(env.spool); err == nil {
			opened <- reader
		}
	}()
	t.Cleanup(func() {
		if writer, err := os.OpenFile(env.spool, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = writer.Close()
		}
	})
	select {
	case reader := <-opened:
		defer reader.Close()
		if _, err := io.ReadAll(reader); err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the hook never opened its spool")
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the hook: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the hook never exited")
	}
	return stdout.String()
}

// A policy hook fails closed only until it reaches its verdict. A loaded machine can hold a hook past
// its deadline while it hands on the event of a call the policy passed, and the deadline must not turn
// that pass into a denial.
func TestAPassedCallStaysPassedPastTheDeadline(t *testing.T) {
	if out := heldPastTheDeadline(t, passedCall); out != "" {
		t.Errorf("a call the policy passed was answered %q", out)
	}
}

// The agent CLI reads a hook's answer as one JSON object. A hook held past its deadline after it
// answered gives that answer and no other, so what it printed is at most one object, and it is the
// verdict the policy reached.
func TestAHookPastItsDeadlineAnswersAtMostOnce(t *testing.T) {
	for name, call := range map[string]struct{ input, decision, reason string }{
		"pass":  {input: passedCall},
		"deny":  {input: deniedCall, decision: "deny", reason: "a force push is denied"},
		"defer": {input: deferredCall, decision: "defer"},
	} {
		t.Run(name, func(t *testing.T) {
			out := heldPastTheDeadline(t, call.input)
			if out == "" {
				if call.decision != "" {
					t.Errorf("no answer, and the policy reached %s", call.decision)
				}
				return
			}
			var answer struct {
				Specific struct {
					Decision string `json:"permissionDecision"`
					Reason   string `json:"permissionDecisionReason"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(out), &answer); err != nil {
				t.Fatalf("the hook printed %q, which is not one JSON object: %v", out, err)
			}
			if call.decision != "" && (answer.Specific.Decision != call.decision || answer.Specific.Reason != call.reason) {
				t.Errorf("the hook answered %q, and the policy reached %s", out, call.decision)
			}
		})
	}
}

// A hook still deciding at its deadline has given no verdict, so it denies the call, and the event it
// took reaches the spool once as that of a refused call, because delivery is at least once. A policy
// file that is a FIFO no one writes holds the hook in its decision past the deadline.
func TestAPolicyHookStillDecidingAtItsDeadlineDenies(t *testing.T) {
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	env := newHookEnv(t)
	env.policy = filepath.Join(t.TempDir(), "policy.json")
	if err := syscall.Mkfifo(env.policy, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaries.cli, "hook", "PreToolUse")
	cmd.Env = env.list()
	cmd.Stdin = strings.NewReader(passedCall)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		if writer, err := os.OpenFile(env.policy, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = writer.Close()
		}
	})
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the hook: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the deadline never ended the hook")
	}
	if reason := deniedBy(t, stdout.String()); reason != "the policy check ran out of time" {
		t.Errorf("a hook with no verdict at its deadline answered %q", stdout.String())
	}
	lines := spoolLines(t, env.spool)
	if len(lines) != 1 || lines[0].Subject != "t1" || lines[0].Detail != lanes.DetailDenied {
		t.Errorf("the spool holds %+v", lines)
	}
}

// A session that lost its place hears who it is however early its hook's deadline comes, even before
// the hook has read what the policy file adds, and its event reaches the spool once. A policy file
// that is a FIFO no one writes holds the hook in that read past the deadline.
func TestARegroundingHookStillReadingItsPolicyAtItsDeadlineGroundsTheSession(t *testing.T) {
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	env := newHookEnv(t)
	env.policy = filepath.Join(t.TempDir(), "policy.json")
	if err := syscall.Mkfifo(env.policy, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaries.cli, "hook", "SessionStart")
	cmd.Env = env.list()
	cmd.Stdin = strings.NewReader(`{"session_id":"s1","source":"compact"}`)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		if writer, err := os.OpenFile(env.policy, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = writer.Close()
		}
	})
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the hook: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the deadline never ended the hook")
	}
	var answer struct {
		Specific struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("the session heard %q: %v", stdout.String(), err)
	}
	for _, part := range []string{"lane-1", "wo", "generation 3"} {
		if answer.Specific.Event != "SessionStart" || !strings.Contains(answer.Specific.Context, part) {
			t.Errorf("the session heard %q", stdout.String())
		}
	}
	if lines := spoolLines(t, env.spool); len(lines) != 1 || lines[0].Detail != "compact" {
		t.Errorf("the spool holds %+v", lines)
	}
}

// A status line hook follows the hook contract, and never spools. A loaded machine can hold it past
// its deadline while it passes its input on, and it still exits 0, spools nothing, and prints nothing
// but its input. A pipe no one reads holds the pass-through write past the deadline, the way a stall
// does.
func TestAStatusLinePastItsDeadlineExitsZero(t *testing.T) {
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	env := newHookEnv(t)
	input := `{"session_id":"s1","padding":"` + strings.Repeat("x", 1<<20) + `"}`
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	cmd := exec.Command(binaries.cli, "statusline", "--pass")
	cmd.Env = env.list()
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = writer
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the status line: %v\n%s", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the deadline never ended the status line")
	}
	passed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(passed) == len(input) {
		t.Fatal("the whole input went through, so nothing held the status line past its deadline")
	}
	if !strings.HasPrefix(input, string(passed)) {
		t.Errorf("the status line printed %d bytes that are not its input", len(passed))
	}
	if _, err := os.Stat(env.spool); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a status line spooled: %v", err)
	}
}
