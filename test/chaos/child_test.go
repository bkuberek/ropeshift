//go:build chaos

package chaos

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

const childEnv = "ROPESHIFT_CHAOS_CHILD"

// Exit codes of a child. A SIGKILL shows as a signal instead.
const (
	exitDone    = 0
	exitBroken  = 3
	exitRefused = 5
)

// childSpec is everything a child knows. It holds nothing from an earlier process,
// because a restart must work from the database alone.
type childSpec struct {
	Role    string `json:"role"`
	Target  Target `json:"target"`
	Scope   string `json:"scope"`
	LogPath string `json:"log"`
	Variant string `json:"variant,omitempty"`

	// KillOp and KillStep name the step where the child ends itself with SIGKILL.
	KillOp   string `json:"kill_op,omitempty"`
	KillStep string `json:"kill_step,omitempty"`

	WorkOrder  string `json:"work_order,omitempty"`
	ClaimID    string `json:"claim_id,omitempty"`
	Worker     string `json:"worker,omitempty"`
	Lane       string `json:"lane,omitempty"`
	Fence      int64  `json:"fence,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	TTLMs      int64  `json:"ttl_ms,omitempty"`
	Key        string `json:"key,omitempty"`

	Slot  int    `json:"slot,omitempty"`
	Spawn int    `json:"spawn,omitempty"`
	Seed  uint64 `json:"seed,omitempty"`
	// Current is the file a worker keeps its open claim in, standing for the
	// journal of a later unit. Gate is the file a stopped writer waits for.
	Current string `json:"current,omitempty"`
	Gate    string `json:"gate,omitempty"`
	// Socket and Journal are a worker child's control plane and journal.
	Socket  string `json:"socket,omitempty"`
	Journal string `json:"journal,omitempty"`
	// Repository, Path, Branch, and GitEnv are a worktree child's request.
	Repository string   `json:"repository,omitempty"`
	Path       string   `json:"path,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	GitEnv     []string `json:"git_env,omitempty"`
}

type child struct {
	spec  childSpec
	ctx   context.Context
	store ports.Store
	ops   contract.Ops
	log   *EffectLog
	rng   *rand.Rand
	scope ledger.WorkspaceID
	actor ledger.Actor
}

// afterTests stops what a tagged suite started.
var afterTests = func() error { return nil }

func TestMain(m *testing.M) {
	if raw := os.Getenv(childEnv); raw != "" {
		os.Exit(childMain(raw))
	}
	code := m.Run()
	if err := afterTests(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}

func childMain(raw string) int {
	var spec childSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		fmt.Fprintln(os.Stderr, "bad child spec:", err)
		return exitBroken
	}
	err := runChild(spec)
	switch {
	case err == nil:
		return exitDone
	case errors.Is(err, ErrRefused):
		fmt.Fprintln(os.Stderr, err)
		return exitRefused
	default:
		fmt.Fprintf(os.Stderr, "child %s/%d: %v\n", spec.Role, spec.Slot, err)
		return exitBroken
	}
}

func runChild(spec childSpec) error {
	if err := Verify(spec.Target, LiveInstallation()); err != nil {
		return err
	}
	store, err := sqlite.Open(spec.Target.Path, sqlstore.Options{})
	if err != nil {
		return err
	}
	defer store.Close()
	log, err := OpenEffectLog(spec.LogPath)
	if err != nil {
		return err
	}
	defer log.Close()
	c := &child{
		spec:  spec,
		ctx:   context.Background(),
		log:   log,
		rng:   rand.New(rand.NewPCG(spec.Seed, uint64(spec.Slot)<<32|uint64(spec.Spawn))),
		scope: ledger.WorkspaceID(spec.Scope),
		actor: ledger.Actor{Kind: ledger.ActorWorker, ID: fmt.Sprintf("worker-%d", spec.Slot)},
	}
	c.store, c.ops, err = variantOps(spec.Variant, store)
	if err != nil {
		return err
	}
	crashpoint.Arm(func(op, step string) {
		if op == spec.KillOp && step == spec.KillStep {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGKILL)
			// The signal can land after this returns. A bare select would trip the
			// runtime's deadlock check first, and a timer does not.
			time.Sleep(time.Hour)
		}
	})
	role, ok := childRoles[spec.Role]
	if !ok {
		return fmt.Errorf("unknown role %q", spec.Role)
	}
	return role(c)
}

func (c *child) attempted(op, workOrder, key string, fence int64) error {
	return c.log.Append(Record{Kind: Attempted, Op: op, WorkOrder: workOrder, Key: key, Fence: fence, Actor: c.actor.ID})
}

func (c *child) completed(op, workOrder, key string, fence, rev int64) error {
	return c.log.Append(Record{Kind: Completed, Op: op, WorkOrder: workOrder, Key: key, Fence: fence, Rev: rev, Actor: c.actor.ID})
}

func (c *child) member() ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
}

func (c *child) lease() workorders.Lease {
	return workorders.Lease{WorkOrder: c.spec.WorkOrder, ClaimID: c.spec.ClaimID, Fence: c.spec.Fence}
}

func (c *child) holder() workorders.Holder {
	return workorders.Holder{Lease: c.lease(), Lane: c.spec.Lane, Generation: c.spec.Generation}
}

func (c *child) rejected(op, workOrder, key string, fence int64, note string) error {
	return c.log.Append(Record{Kind: Rejected, Op: op, WorkOrder: workOrder, Key: key, Fence: fence, Note: note, Actor: c.actor.ID})
}

// proc is one running child.
type proc struct {
	cmd    *exec.Cmd
	at     chan string
	exited chan struct{}
	stderr *lockedBuffer
	status error
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startChild(spec childSpec) (*proc, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), childEnv+"="+string(raw))
	p := &proc{cmd: cmd, at: make(chan string, 1), exited: make(chan struct{}), stderr: &lockedBuffer{}}
	cmd.Stderr = p.stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if name, ok := strings.CutPrefix(scanner.Text(), "AT "); ok {
				p.at <- name
			}
		}
		p.status = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

func (p *proc) wait() { <-p.exited }

func (p *proc) sigkill() {
	_ = p.cmd.Process.Signal(syscall.SIGKILL)
	<-p.exited
}

func (p *proc) exitCode() (code int, signaled bool) {
	var exit *exec.ExitError
	if errors.As(p.status, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 0, true
		}
		return exit.ExitCode(), false
	}
	return 0, false
}

// runToEnd runs one child with a deadline and returns how it ended.
func runToEnd(spec childSpec) (code int, signaled bool, stderr string, err error) {
	p, err := startChild(spec)
	if err != nil {
		return 0, false, "", err
	}
	select {
	case <-p.exited:
	case <-time.After(45 * time.Second):
		p.sigkill()
		return 0, false, p.stderr.String(), fmt.Errorf("child %s did not end", spec.Role)
	}
	code, signaled = p.exitCode()
	return code, signaled, p.stderr.String(), nil
}
