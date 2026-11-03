//go:build chaos

package chaos

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
)

// The two processes this file adds to the test binary.
const (
	envNumbered = "ROPESHIFT_CHAOS_NUMBERED"
	envReader   = "ROPESHIFT_CHAOS_SHIM_READER"
)

const (
	shimLines = 20_000
	shimRate  = 10_000
	shimBatch = 100
	shimLimit = 60 * time.Second
	// cursorEvery does not divide shimBatch, because a reader idles between batches, and
	// a kill there must fall between its log write and its cursor write.
	cursorEvery  = 70
	skippedLines = 500
)

// numberedChild writes numbered lines at a fixed rate, which gives each run a ground
// truth no worker can talk its way past.
func numberedChild(text string) int {
	lines, rate, _ := strings.Cut(text, ":")
	total, err := strconv.Atoi(lines)
	if err != nil {
		return exitBroken
	}
	perSecond, err := strconv.Atoi(rate)
	if err != nil {
		return exitBroken
	}
	out := bufio.NewWriter(os.Stdout)
	pause := time.Second * shimBatch / time.Duration(perSecond)
	for i := 1; i <= total; i++ {
		fmt.Fprintf(out, "%d\n", i)
		if i%shimBatch == 0 {
			if out.Flush() != nil {
				return exitBroken
			}
			time.Sleep(pause)
		}
	}
	if out.Flush() != nil {
		return exitBroken
	}
	return exitDone
}

// readerSpec is one worker stand-in. Log is its own record of what it took, and Cursor
// its durable position, written after the log, so a kill between them redelivers.
type readerSpec struct {
	Binary string   `json:"binary,omitempty"`
	Dir    string   `json:"dir"`
	Socket string   `json:"socket"`
	Log    string   `json:"log"`
	Cursor string   `json:"cursor"`
	Env    []string `json:"env,omitempty"`
	// Skip is the broken variant: a reader that loses what it missed.
	Skip int64 `json:"skip,omitempty"`
}

func shimConfig(spec readerSpec) shimlink.Config {
	return shimlink.Config{Dir: spec.Dir, Socket: spec.Socket, Command: []string{os.Args[0]}, Linger: 30 * time.Second}
}

// startShim spawns the production binary the way the driver does.
func startShim(spec readerSpec) (ports.Process, error) {
	return osexec.Runner{}.Start(context.Background(), ports.ProcessSpec{
		Path: spec.Binary, Args: shimlink.Args(shimConfig(spec)), Env: spec.Env, Detached: true,
	})
}

func readCursor(path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}

func writeCursor(path string, cursor int64) error {
	next := path + ".next"
	if err := os.WriteFile(next, []byte(strconv.FormatInt(cursor, 10)), 0o600); err != nil {
		return err
	}
	return os.Rename(next, path)
}

func dialShim(ctx context.Context, socket string) (*shimlink.Client, error) {
	var err error
	for deadline := time.Now().Add(shimLimit); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		var client *shimlink.Client
		if client, err = shimlink.Dial(ctx, socket); err == nil {
			return client, nil
		}
	}
	return nil, err
}

// shimReaderMain is one worker: it starts the shim if asked, reads from its cursor to the
// child's exit, and prints where it is, so a parent can kill it mid-stream.
func shimReaderMain(raw string) int {
	var spec readerSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return exitBroken
	}
	if err := readShim(spec); err != nil {
		fmt.Fprintln(os.Stderr, "reader:", err)
		return exitBroken
	}
	return exitDone
}

func readShim(spec readerSpec) error {
	ctx := context.Background()
	if spec.Binary != "" {
		if _, err := startShim(spec); err != nil {
			return err
		}
	}
	client, err := dialShim(ctx, spec.Socket)
	if err != nil {
		return err
	}
	cursor, err := readCursor(spec.Cursor)
	if err != nil {
		return err
	}
	stream, err := client.Attach(ctx, cursor+spec.Skip)
	if err != nil {
		return err
	}
	defer stream.Close()
	log, err := os.OpenFile(spec.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	fmt.Println("AT attached")
	for taken := 1; ; taken++ {
		record, err := stream.Next()
		if err != nil {
			return err
		}
		if record.Exit != nil {
			return writeCursor(spec.Cursor, record.Seq)
		}
		if record.Line == "" {
			continue
		}
		if _, err := fmt.Fprintf(log, "%d %s\n", record.Seq, record.Line); err != nil {
			return err
		}
		if taken%cursorEvery == 0 {
			if err := writeCursor(spec.Cursor, record.Seq); err != nil {
				return err
			}
		}
	}
}

// shimRig is one run: a production shim, a ground-truth child, and readers.
type shimRig struct {
	t    *testing.T
	spec readerSpec
	dir  string
}

var shimBinary = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp(doubles.ShortTemp(), "rsshim")
	if err != nil {
		return "", err
	}
	binary, err := doubles.Binary(dir, doubles.PrebuiltRopeshift, "ropeshift.dev/ropeshift/cmd/ropeshift")
	if err != nil {
		return "", err
	}
	previous := afterTests
	afterTests = func() error {
		_ = os.RemoveAll(dir)
		return previous()
	}
	return binary, nil
})

func newShimRig(t *testing.T) *shimRig {
	t.Helper()
	binary, err := shimBinary()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(doubles.ShortTemp(), "rs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	r := &shimRig{t: t, dir: dir, spec: readerSpec{
		Binary: binary, Dir: filepath.Join(dir, "shim"), Socket: filepath.Join(dir, "s.sock"),
		Log: filepath.Join(dir, "taken.log"), Cursor: filepath.Join(dir, "cursor"),
		Env: []string{envNumbered + "=" + strconv.Itoa(shimLines) + ":" + strconv.Itoa(shimRate), "PATH=/usr/bin:/bin"},
	}}
	t.Cleanup(r.leaveNothing)
	return r
}

// leaveNothing stops by exact ID what this run started, and only that.
func (r *shimRig) leaveNothing() {
	state, err := shimlink.ReadState(r.spec.Dir)
	if err != nil {
		return
	}
	if state.ChildPID != 0 {
		_ = syscall.Kill(state.ChildPID, syscall.SIGKILL)
	}
	if state.ShimPID != 0 {
		_ = syscall.Kill(state.ShimPID, syscall.SIGKILL)
	}
}

// reader starts a worker stand-in in a process group of its own.
func (r *shimRig) reader(spec readerSpec) (*exec.Cmd, <-chan struct{}, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), envReader+"="+string(raw))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	attached := make(chan struct{})
	go func() {
		_, _ = bufio.NewReader(stdout).ReadString('\n')
		close(attached)
		_, _ = io.Copy(io.Discard, stdout)
	}()
	return cmd, attached, nil
}

// await waits for a reader to reach the child's exit.
func (r *shimRig) await(cmd *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(shimLimit):
		_ = cmd.Process.Kill()
		return errors.New("a reader never reached the exit")
	}
}

// settle reads with a fresh worker, from the cursor, to the end.
func (r *shimRig) settle(spec readerSpec) error {
	spec.Binary = ""
	cmd, _, err := r.reader(spec)
	if err != nil {
		return err
	}
	return r.await(cmd)
}

// groundTruth holds what a worker took against what the child wrote. A repeat is a kill
// between the log and the cursor, which a worker drops by its sequence number.
func (r *shimRig) groundTruth() (missing, repeated int, err error) {
	file, err := os.Open(r.spec.Log)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	seen := map[string]int{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		_, line, _ := strings.Cut(scanner.Text(), " ")
		seen[line]++
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	for i := 1; i <= shimLines; i++ {
		switch times := seen[strconv.Itoa(i)]; {
		case times == 0:
			missing++
		case times > 1:
			repeated += times - 1
		}
	}
	return missing, repeated, nil
}

func (r *shimRig) noneMissing() error {
	missing, repeated, err := r.groundTruth()
	if err != nil {
		return err
	}
	r.t.Logf("lines=%d missing=%d redelivered=%d", shimLines, missing, repeated)
	if missing != 0 {
		return contract.Violatedf("%d of %d lines missing at the worker", missing, shimLines)
	}
	return nil
}

// killMidStream is the first two T10 cases: a worker dies with lines still coming, and a
// new one reads on from its cursor.
func (r *shimRig) killMidStream(kill func(*exec.Cmd) error, skip int64) error {
	cmd, attached, err := r.reader(r.spec)
	if err != nil {
		return err
	}
	select {
	case <-attached:
	case <-time.After(shimLimit):
		_ = cmd.Process.Kill()
		return errors.New("the first reader never attached")
	}
	time.Sleep(300 * time.Millisecond)
	if err := kill(cmd); err != nil {
		return err
	}
	_ = cmd.Wait()
	taken, _, err := r.groundTruth()
	if err != nil {
		return err
	}
	if taken = shimLines - taken; taken == 0 || taken == shimLines {
		return fmt.Errorf("the kill was not mid-stream: the worker held %d of %d lines", taken, shimLines)
	}
	r.t.Logf("the worker held %d of %d lines when it died", taken, shimLines)
	time.Sleep(300 * time.Millisecond)
	next := r.spec
	next.Skip = skip
	if err := r.settle(next); err != nil {
		return err
	}
	return r.noneMissing()
}

func killReader(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGKILL) }

func killReaderGroup(cmd *exec.Cmd) error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

// checkShimGroundTruth is run on the broken reader, so the count is proven able to fail.
func checkShimGroundTruth(t *testing.T) error {
	return newShimRig(t).killMidStream(killReader, skippedLines)
}

func TestShimWorkerKilledMidStream(t *testing.T) {
	if err := newShimRig(t).killMidStream(killReader, 0); err != nil {
		t.Fatal(err)
	}
}

// A signal to the worker's whole process group stops at its session.
func TestShimWorkerGroupKilled(t *testing.T) {
	if err := newShimRig(t).killMidStream(killReaderGroup, 0); err != nil {
		t.Fatal(err)
	}
}

// An absent worker costs disk and not progress: the child runs to its end at full rate.
func TestShimNoWorkerAttached(t *testing.T) {
	r := newShimRig(t)
	started := time.Now()
	if _, err := startShim(r.spec); err != nil {
		t.Fatal(err)
	}
	err := until("the child's end in the journal", func() (bool, error) {
		records, err := shimlink.ReadJournal(r.spec.Dir, 0)
		if err != nil {
			return false, err
		}
		return len(records) != 0 && records[len(records)-1].Exit != nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("the child ran %v with no worker attached", time.Since(started).Round(time.Millisecond))
	if err := r.settle(r.spec); err != nil {
		t.Fatal(err)
	}
	if err := r.noneMissing(); err != nil {
		t.Fatal(err)
	}
}

// TestPlainPipeBlocks is the control: the same child on a pipe, with a reader that stops.
// It shows what the shim is for, and that the child can block at all.
func TestPlainPipeBlocks(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), envNumbered+"="+strconv.Itoa(shimLines*10)+":"+strconv.Itoa(shimRate*10))
	child.Stdout = write
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	_ = write.Close()
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	first := make([]byte, 64)
	if _, err := read.Read(first); err != nil {
		t.Fatal(err)
	}
	planned := time.Duration(shimLines*10) * time.Second / (shimRate * 10)
	select {
	case err := <-exited:
		t.Fatalf("the child finished with no reader: %v", err)
	case <-time.After(planned * 3):
	}
	_ = read.Close()
	select {
	case err := <-exited:
		var failed *exec.ExitError
		if !errors.As(err, &failed) {
			t.Errorf("a broken pipe ended the child with %v", err)
		}
	case <-time.After(shimLimit):
		_ = child.Process.Kill()
		t.Fatal("the child outlived its reader")
	}
}
