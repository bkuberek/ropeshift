package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// talking is a daemon whose API knows a worker, atlas, that can run the assistant on the project's one
// repository, served on a socket of its own.
type talking struct {
	t       *testing.T
	daemon  *apitest.Daemon
	socket  string
	workers *control.Workers
	auth    ledger.WorkerAuth
	hosts   map[string]control.AssistantHost
}

func newTalking(t *testing.T) *talking {
	t.Helper()
	s := &talking{t: t, daemon: apitest.Start(t), hosts: map[string]control.AssistantHost{}}
	config := s.daemon.Config
	config.AssistantHosts = func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
		return s.hosts, nil
	}
	s.socket = filepath.Join(apitest.ShortDir(t), api.ControlSocketName)
	listener, err := net.Listen("unix", s.socket)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = httpServer.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = httpServer.Close()
		<-done
	})
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member"}
	s.workers = control.NewWorkers(s.daemon.Store, apitest.Scope, control.WorkerOptions{HeartbeatMs: apitest.HeartbeatMs, LeaseTTLMs: apitest.LeaseTTL})
	token, err := s.workers.CreateJoinToken(t.Context(), member)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.workers.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: "worker-atlas", Name: "atlas"})
	if err != nil {
		t.Fatal(err)
	}
	s.auth = ledger.WorkerAuth{WorkerID: "worker-atlas", Credential: credential.Credential}
	if _, err := s.workers.OpenSession(t.Context(), ledger.SessionRequest{Auth: s.auth, Protocol: ledger.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewRepositories(s.daemon.Store).Detect(t.Context(), apitest.Scope, control.DetectedRepository{ID: "repo-harbor", Directory: "harbor-api"}, member); err != nil {
		t.Fatal(err)
	}
	s.hosts[s.auth.WorkerID] = control.AssistantHost{Headless: true, Checkouts: map[string]bool{"repo-harbor": true}}
	return s
}

// run runs a command as the daemon's member, typing input on a terminal when input is not nil.
func (s *talking) run(input *string, args ...string) (int, string, string) {
	s.t.Helper()
	var stdout, stderr bytes.Buffer
	opts := cli.Options{Socket: s.socket, Session: s.daemon.Session}
	if input != nil {
		opts.Terminal = strings.NewReader(*input)
	}
	code := cli.Run(args, &stdout, &stderr, opts)
	return code, stdout.String(), stderr.String()
}

func (s *talking) must(args ...string) string {
	s.t.Helper()
	code, stdout, stderr := s.run(nil, args...)
	if code != cli.ExitOK {
		s.t.Fatalf("%v: exit %d: %s", args, code, stderr)
	}
	return stdout
}

func typed(text string) *string { return &text }

func TestTheChatCommandsChangeAConversation(t *testing.T) {
	s := newTalking(t)
	var opened api.ConversationOpened
	if err := json.Unmarshal([]byte(s.must("chat", "new", "--name", "Harbor merges", "--json")), &opened); err != nil {
		t.Fatal(err)
	}
	id := opened.Conversation.ID
	if opened.Conversation.Name != "Harbor merges" || opened.Conversation.WorkerName != "atlas" {
		t.Fatalf("new %+v", opened)
	}
	if out := s.must("chat", "list"); !strings.Contains(out, id) || !strings.Contains(out, "worker atlas can run the assistant") {
		t.Errorf("list %q", out)
	}
	if out := s.must("chat", "send", id, "--body", "Which merges wait?"); !strings.Contains(out, "Which merges wait?") || !strings.Contains(out, "[queued]") {
		t.Errorf("send %q", out)
	}
	if out := s.must("chat", "show", id); !strings.Contains(out, "session started") || !strings.Contains(out, "Which merges wait?") {
		t.Errorf("show %q", out)
	}
	if out := s.must("chat", "rename", id, "Merge queue"); !strings.Contains(out, `"Merge queue"`) {
		t.Errorf("rename %q", out)
	}
	if out := s.must("chat", "stop", id); !strings.Contains(out, "stopped") {
		t.Errorf("stop %q", out)
	}
	if code, _, stderr := s.run(nil, "chat", "compact", id); code != cli.ExitConflict || !strings.Contains(stderr, "not running") {
		t.Errorf("compact a stopped conversation: exit %d %q", code, stderr)
	}
	if code, _, stderr := s.run(nil, "chat", "clear", id); code != cli.ExitConflict {
		t.Errorf("clear a stopped conversation: exit %d %q", code, stderr)
	}
	s.must("chat", "open", id)
	var targets api.ConversationTargets
	if err := json.Unmarshal([]byte(s.must("chat", "targets", "--json")), &targets); err != nil {
		t.Fatal(err)
	}
	if targets.Assistant.Conversation == nil || targets.Assistant.Conversation.ID != id {
		t.Errorf("targets %+v", targets)
	}
	if out := s.must("chat", "delete", id); !strings.Contains(out, "stay in the audit") {
		t.Errorf("delete %q", out)
	}
	if code, _, _ := s.run(nil, "chat", "show", id); code != cli.ExitNotFound {
		t.Errorf("show a deleted conversation: exit %d", code)
	}
	if code, _, _ := s.run(nil, "chat", "rename", id); code != cli.ExitUsage {
		t.Errorf("rename with no name: exit %d", code)
	}
}

func TestChatWithNoWorkerThatCanRunTheAssistantIsAConflict(t *testing.T) {
	s := newTalking(t)
	s.hosts[s.auth.WorkerID] = control.AssistantHost{}
	code, _, stderr := s.run(nil, "chat", "new")
	if code != cli.ExitConflict || !strings.Contains(stderr, "atlas runs no headless session") {
		t.Fatalf("exit %d %q", code, stderr)
	}
}

// chat sends each line typed as a message, prints the conversation as it grows, and stops the session
// when input ends. It needs a terminal, and prints no JSON.
func TestChatTalksUntilInputEndsAndThenStops(t *testing.T) {
	s := newTalking(t)
	code, stdout, stderr := s.run(typed("Which merges wait for me?\n\n"), "chat", "--new")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Which merges wait for me?") || !strings.Contains(stdout, "Stopped the session") {
		t.Errorf("stdout %q", stdout)
	}
	var list api.ConversationList
	if err := json.Unmarshal([]byte(s.must("chat", "list", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Conversations) != 1 || list.Conversations[0].Running() || list.Conversations[0].Name != "Which merges wait for me?" {
		t.Fatalf("after chat: %+v", list.Conversations)
	}
	code, _, stderr = s.run(typed("again\n"), "chat")
	if code != cli.ExitOK || !strings.Contains(stderr, "") {
		t.Fatalf("chat on the newest conversation: exit %d %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(s.must("chat", "list", "--json")), &list); err != nil || len(list.Conversations) != 1 {
		t.Fatalf("chat opened another conversation: %+v %v", list, err)
	}
	if code, _, _ := s.run(nil, "chat"); code != cli.ExitUsage {
		t.Errorf("chat with no terminal: exit %d", code)
	}
	if code, _, _ := s.run(typed(""), "chat", "--json"); code != cli.ExitUsage {
		t.Errorf("chat --json: exit %d", code)
	}
	if code, _, _ := s.run(typed(""), "chat", "--new", "--conversation", "c"); code != cli.ExitUsage {
		t.Errorf("chat --new --conversation: exit %d", code)
	}
}

func TestAMessageAndAnAttachReachTheOrchestrator(t *testing.T) {
	s := newTalking(t)
	code, _, stderr := s.run(nil, "message", "send", "--orchestrator", s.auth.WorkerID, "--body", "Look at WO-1")
	if code != cli.ExitConflict || !strings.Contains(stderr, "The orchestrator on atlas is not running") {
		t.Fatalf("a stopped orchestrator: exit %d %q", code, stderr)
	}
	if _, err := s.workers.SetOrchestrator(t.Context(), s.auth.WorkerID, ledger.OrchestratorRunning, ledger.Actor{Kind: ledger.ActorMember, ID: "member"}); err != nil {
		t.Fatal(err)
	}
	if out := s.must("message", "send", "--orchestrator", s.auth.WorkerID, "--body", "Look at WO-1"); !strings.Contains(out, "to orchestrator "+s.auth.WorkerID) {
		t.Errorf("send %q", out)
	}
	if code, _, _ := s.run(nil, "message", "send", "lane-1", "--orchestrator", s.auth.WorkerID, "--body", "x"); code != cli.ExitUsage {
		t.Errorf("a lane and an orchestrator: exit %d", code)
	}
	code, stdout, stderr := s.run(typed("And WO-2\n"), "orchestrator", "attach")
	if code != cli.ExitOK || !strings.Contains(stdout, "Look at WO-1") || !strings.Contains(stdout, "And WO-2") {
		t.Fatalf("attach: exit %d %q %q", code, stdout, stderr)
	}
	if code, _, _ := s.run(typed(""), "orchestrator", "attach", "--worker", "nobody"); code != cli.ExitNotFound {
		t.Errorf("attach to a worker that serves nothing here: exit %d", code)
	}
}

// A headless lane has no pane, so attaching follows its stream, and a line typed there is a message.
func TestLaneAttachFollowsAHeadlessLane(t *testing.T) {
	s := newTalking(t)
	w := apitest.NewWorld(t, s.daemon)
	lane := w.Running("wo-1", "Add the retry budget")
	err := s.daemon.Store.Write(t.Context(), apitest.Scope, "test.experience", func(tx ports.WriteTx) error {
		if err := tx.Measures().InsertExperience(measures.Experience{
			ID: "exp-1", SubjectKind: measures.SubjectLane, SubjectID: lane.ID, Settings: measures.Settings{RunMode: "headless"}, Chosen: "run",
		}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.experience", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: "test", ID: "1"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := s.run(typed("Use two retries\n"), "lane", "attach", lane.ID)
	if code != cli.ExitOK || !strings.Contains(stdout, "Use two retries") || !strings.Contains(stdout, "runs headless") {
		t.Fatalf("exit %d %q %q", code, stdout, stderr)
	}
	sent := s.must("message", "list", lane.ID)
	if !strings.Contains(sent, "instruction") {
		t.Errorf("the line was not sent as a message: %q", sent)
	}
}

// A delegate acts only through the API, so a command that acts on this machine itself is refused before
// it touches anything, whatever the command line asks.
func TestADelegateRunsNoMachineLocalCommand(t *testing.T) {
	ran := false
	opts := cli.Options{
		Socket: "/nonexistent.sock", Delegate: "a-delegate-token", Terminal: strings.NewReader(""),
		InstallService:   func(context.Context, string, []string) (cli.Service, error) { ran = true; return cli.Service{}, nil },
		UninstallService: func(context.Context, string) (cli.Service, error) { ran = true; return cli.Service{}, nil },
		StartDaemon:      func(context.Context) (func() error, error) { ran = true; return func() error { return nil }, nil },
		Doctor:           func(context.Context) (ledger.Manifest, error) { ran = true; return ledger.Manifest{}, nil },
		StateDir:         t.TempDir(),
	}
	for _, args := range [][]string{
		{"service", "install"}, {"service", "uninstall"}, {"init"}, {"worker", "doctor"}, {"login"}, {"passphrase", "set"},
		{"lane", "attach", "l1"}, {"plan", "start"}, {"adopt"}, {"backup", "/tmp/copy.db"}, {"repository", "add", "."},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.Run(args, &stdout, &stderr, opts)
		if code != cli.ExitForbidden || !strings.Contains(stderr.String(), "delegate") {
			t.Errorf("%v: exit %d %q", args, code, stderr.String())
		}
	}
	if ran {
		t.Error("a machine-local command ran for a delegate")
	}
}

// A delegate's command reads no file on this machine, because a delegate acts only through the API and its
// own reads stop at its trees: each flag that names a file is refused before anything is read or sent.
func TestADelegateReadsNoFileOnThisMachine(t *testing.T) {
	file := filepath.Join(t.TempDir(), "layer.toml")
	if err := os.WriteFile(file, []byte("[agent]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := cli.Options{Socket: "/nonexistent.sock", Delegate: "a-delegate-token", StateDir: t.TempDir()}
	for _, args := range [][]string{
		{"lane", "start", "WO-1", "--profile", file},
		{"agent", "add", "x", "--file", file},
		{"agent", "set", "x", "purpose=@" + file, "--project", "p"},
		{"agent", "guide", "set", "@" + file, "--project", "p"},
		{"policy", "publish", "--file", file},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.Run(args, &stdout, &stderr, opts)
		if code == cli.ExitOK || code == cli.ExitUnreachable || !strings.Contains(stderr.String(), "reads no file") {
			t.Errorf("%v: exit %d %q", args, code, stderr.String())
		}
	}
}

// A delegate's command dials only the control plane this machine's installation names: a socket flag, in
// any of its forms and wherever it stands, is refused before anything is dialed, so no listener a flag
// names receives the delegate's credential.
func TestADelegateNamesNoOtherSocket(t *testing.T) {
	evil := filepath.Join(apitest.ShortDir(t), "evil.sock")
	listener, err := net.Listen("unix", evil)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dialed := make(chan struct{}, 8)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			dialed <- struct{}{}
			_ = conn.Close()
		}
	}()
	opts := cli.Options{Socket: filepath.Join(apitest.ShortDir(t), "control.sock"), Delegate: "a-delegate-token"}
	for _, args := range [][]string{
		{"health", "--socket", evil}, {"health", "--socket=" + evil}, {"health", "-socket", evil}, {"health", "-socket=" + evil},
		{"work", "show", "WO-1", "--socket", evil}, {"lane", "list", "--json", "-socket=" + evil},
	} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(args, &stdout, &stderr, opts); code != cli.ExitForbidden || !strings.Contains(stderr.String(), "socket") {
			t.Errorf("%v: exit %d %q", args, code, stderr.String())
		}
	}
	select {
	case <-dialed:
		t.Error("a delegate's command dialed the socket a flag named")
	case <-time.After(100 * time.Millisecond):
	}
}
