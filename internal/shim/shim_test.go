package shim_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/shim"
)

const envChild = "ROPESHIFT_SHIM_TEST_CHILD"

// TestMain turns the test binary into the child, so a test needs no shell and no build.
func TestMain(m *testing.M) {
	switch mode, count, _ := strings.Cut(os.Getenv(envChild), ":"); mode {
	case "":
		os.Exit(m.Run())
	case "numbered":
		lines, _ := strconv.Atoi(count)
		out := bufio.NewWriterSize(os.Stdout, 1<<16)
		for i := 1; i <= lines; i++ {
			fmt.Fprintf(out, "%d\n", i)
		}
		_ = out.Flush()
	case "echo":
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	case "exit":
		code, _ := strconv.Atoi(count)
		os.Exit(code)
	case "hold":
		fmt.Println("holding")
		time.Sleep(time.Hour)
	}
	os.Exit(0)
}

// running is one shim in this process, on a real child and a real socket.
type running struct {
	t      *testing.T
	config shimlink.Config
	client *shimlink.Client
	done   chan error
	stop   context.CancelFunc
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func start(t *testing.T, mode string, change func(*shimlink.Config)) *running {
	t.Helper()
	t.Setenv(envChild, mode)
	dir := shortDir(t)
	config := shimlink.Config{Dir: filepath.Join(dir, "g"), Socket: filepath.Join(dir, "s.sock"), Command: []string{os.Args[0]}, Linger: time.Minute}
	if change != nil {
		change(&config)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{t: t, config: config, done: make(chan error, 1), stop: cancel}
	go func() { r.done <- shim.Run(ctx, config) }()
	t.Cleanup(func() {
		if state, err := shimlink.ReadState(config.Dir); err == nil && state.ChildPID != 0 {
			_ = syscall.Kill(state.ChildPID, syscall.SIGKILL)
		}
		cancel()
		select {
		case err := <-r.done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("the shim did not stop")
		}
	})
	r.client = r.dial()
	return r
}

func (r *running) dial() *shimlink.Client {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if client, err := shimlink.Dial(r.t.Context(), r.config.Socket); err == nil {
			return client
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatal("no shim answered")
	return nil
}

// collect reads an attach through the child's exit, and fails on a gap in the numbers.
func (r *running) collect(since int64) []shimlink.Record {
	r.t.Helper()
	stream, err := r.client.Attach(r.t.Context(), since)
	if err != nil {
		r.t.Fatal(err)
	}
	defer stream.Close()
	var out []shimlink.Record
	for {
		record, err := stream.Next()
		if err != nil {
			r.t.Fatalf("after %d records: %v", len(out), err)
		}
		if record.Seq != since+int64(len(out))+1 {
			r.t.Fatalf("record %d after %d records from %d", record.Seq, len(out), since)
		}
		out = append(out, record)
		if record.Exit != nil {
			return out
		}
	}
}

func lines(records []shimlink.Record) []string {
	var out []string
	for _, record := range records {
		if record.Line != "" {
			out = append(out, record.Line)
		}
	}
	return out
}

func TestEveryLineArrivesInOrder(t *testing.T) {
	r := start(t, "numbered:5000", nil)
	records := r.collect(0)
	got := lines(records)
	if len(got) != 5000 {
		t.Fatalf("%d lines", len(got))
	}
	for i, line := range got {
		if line != strconv.Itoa(i+1) {
			t.Fatalf("line %d is %q", i+1, line)
		}
	}
	if exit := records[len(records)-1].Exit; exit == nil || exit.Code != 0 {
		t.Errorf("exit %+v", exit)
	}
}

func TestCatchUpAndLiveAreOnePath(t *testing.T) {
	r := start(t, "numbered:100", nil)
	first := r.collect(0)
	later := r.collect(50)
	if len(later) != len(first)-50 || later[0].Line != "51" {
		t.Errorf("%d records from 50, first %+v", len(later), later[0])
	}
}

func TestAnExitIsRecorded(t *testing.T) {
	r := start(t, "exit:3", nil)
	records := r.collect(0)
	if exit := records[len(records)-1].Exit; exit == nil || exit.Code != 3 {
		t.Errorf("exit %+v", exit)
	}
}

func TestASignalReachesTheChildAndIsRecorded(t *testing.T) {
	r := start(t, "hold", nil)
	if err := r.client.Signal(t.Context(), shimlink.SignalKill); err != nil {
		t.Fatal(err)
	}
	records := r.collect(0)
	if exit := records[len(records)-1].Exit; exit == nil || exit.Signal == "" {
		t.Errorf("exit %+v", exit)
	}
}

// A shim signals only its child, and only with the three names a stop uses.
func TestAnyOtherSignalIsRefused(t *testing.T) {
	r := start(t, "hold", nil)
	err := r.client.Signal(t.Context(), "HUP")
	var refused *shimlink.RefusedError
	if !errors.As(err, &refused) || refused.Code != shimlink.CodeBadSignal {
		t.Errorf("signal: %v", err)
	}
}

// A line that does not parse would end the session with no result, so none reaches it.
func TestAMalformedLineNeverReachesTheChild(t *testing.T) {
	r := start(t, "echo", nil)
	tests := map[string]string{
		"not JSON":        `not json`,
		"truncated":       `{"type":"user"`,
		"no type":         `{"message":"x"}`,
		"empty type":      `{"type":""}`,
		"not an object":   `["type"]`,
		"a number type":   `{"type":1}`,
		"two lines":       `{"type":"user"}` + "\n" + `{"type":"user"}`,
		"a carriage line": `{"type":"user"}` + "\r",
		"nothing":         ``,
	}
	for name, line := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := r.client.Input(t.Context(), name, line)
			var refused *shimlink.RefusedError
			if !errors.As(err, &refused) || refused.Code != shimlink.CodeBadLine {
				t.Errorf("input: %v", err)
			}
		})
	}
	const good = `{"type":"user","message":{"role":"user"}}`
	if _, err := r.client.Input(t.Context(), "not JSON", good); err != nil {
		t.Fatalf("a refused line spent its ID: %v", err)
	}
	if _, err := r.client.Input(t.Context(), "unknown", `{"type":"a type the CLI ignores"}`); err != nil {
		t.Fatalf("an unknown type was refused: %v", err)
	}
	if err := r.client.CloseStdin(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := lines(r.collect(0))
	if !slices.Equal(got, []string{good, `{"type":"a type the CLI ignores"}`}) {
		t.Errorf("the child saw %q", got)
	}
}

func TestARepeatedInputArrivesOnce(t *testing.T) {
	r := start(t, "echo", nil)
	const line = `{"type":"user"}`
	first, err := r.client.Input(t.Context(), "the-id", line)
	if err != nil || first {
		t.Fatalf("first: %v, %v", first, err)
	}
	for range 3 {
		again, err := r.client.Input(t.Context(), "the-id", line)
		if err != nil || !again {
			t.Fatalf("repeat: %v, %v", again, err)
		}
	}
	if err := r.client.CloseStdin(t.Context()); err != nil {
		t.Fatal(err)
	}
	records := r.collect(0)
	if got := lines(records); len(got) != 1 {
		t.Errorf("the child saw %q", got)
	}
	inputs := 0
	for _, record := range records {
		if record.Input == "the-id" {
			inputs++
		}
	}
	if inputs != 1 {
		t.Errorf("%d input records", inputs)
	}
}

func TestInputAfterStdinClosedIsRefused(t *testing.T) {
	r := start(t, "echo", nil)
	if err := r.client.CloseStdin(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := r.client.Input(t.Context(), "late", `{"type":"user"}`)
	var refused *shimlink.RefusedError
	if !errors.As(err, &refused) || refused.Code != shimlink.CodeStdinClosed {
		t.Errorf("input: %v", err)
	}
}

func TestPingProvesTheLoop(t *testing.T) {
	r := start(t, "hold", nil)
	if err := r.client.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestASecondShimIsRefused(t *testing.T) {
	r := start(t, "hold", nil)
	err := shim.Run(t.Context(), r.config)
	if !errors.Is(err, shim.ErrLocked) {
		t.Errorf("second shim: %v", err)
	}
}

func TestANewAttachTakesOver(t *testing.T) {
	r := start(t, "hold", nil)
	first, err := r.client.Attach(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.Next(); err != nil {
		t.Fatal(err)
	}
	second, err := r.client.Attach(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := first.Next(); !errors.Is(err, shimlink.ErrUnreachable) {
		t.Errorf("the first attach: %v", err)
	}
	if record, err := second.Next(); err != nil || record.Line != "holding" {
		t.Errorf("the second attach: %+v, %v", record, err)
	}
}

func TestALingeringShimEndsAndLeavesTheExitOnDisk(t *testing.T) {
	r := start(t, "exit:4", func(c *shimlink.Config) { c.Linger = 20 * time.Millisecond })
	select {
	case err := <-r.done:
		r.done <- err
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the shim did not end")
	}
	records, err := shimlink.ReadJournal(r.config.Dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if exit := records[len(records)-1].Exit; exit == nil || exit.Code != 4 {
		t.Errorf("records %+v", records)
	}
}

func TestShutdownWaitsForTheChild(t *testing.T) {
	r := start(t, "hold", nil)
	var refused *shimlink.RefusedError
	if err := r.client.Shutdown(t.Context()); !errors.As(err, &refused) {
		t.Errorf("shutdown of a running child: %v", err)
	}
	if err := r.client.Signal(t.Context(), shimlink.SignalKill); err != nil {
		t.Fatal(err)
	}
	r.collect(0)
	if err := r.client.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-r.done:
		r.done <- err
		if err != nil {
			t.Error(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the shim did not end")
	}
}

func TestStateNamesBothProcesses(t *testing.T) {
	r := start(t, "hold", nil)
	state, err := shimlink.ReadState(r.config.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.ShimPID != os.Getpid() || state.ChildPID == 0 || state.Version != shimlink.Version {
		t.Errorf("state %+v", state)
	}
}

func TestSocketIsPrivate(t *testing.T) {
	r := start(t, "hold", nil)
	info, err := os.Stat(r.config.Socket)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode %o", mode)
	}
}

func TestNegotiate(t *testing.T) {
	tests := []struct {
		name         string
		ours, theirs int
		want         int
		refused      bool
	}{
		{name: "the same", ours: 1, theirs: 1, want: 1},
		{name: "a worker reaches back to an older shim", ours: 3, theirs: 1, want: 1},
		{name: "an older worker speaks its own to a newer shim", ours: 1, theirs: 3, want: 1},
		{name: "a shim too old to speak to", ours: 2, theirs: 0, refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := shimlink.Negotiate(tt.ours, tt.theirs)
			if (err != nil) != tt.refused || got != tt.want {
				t.Errorf("got %d, %v", got, err)
			}
		})
	}
}

func openJournal(t *testing.T, capBytes int64) (*shimlink.Journal, string) {
	t.Helper()
	dir := t.TempDir()
	journal, err := shimlink.OpenJournal(dir, capBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	return journal, dir
}

func TestTailFollowsAndEndsWithTheJournal(t *testing.T) {
	journal, _ := openJournal(t, 0)
	for i := range 3 {
		if _, err := journal.Append(shimlink.Record{Line: strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	tail := journal.Tail(1)
	defer tail.Close()
	for _, want := range []int64{2, 3} {
		record, err := tail.Next(t.Context())
		if err != nil || record.Seq != want {
			t.Fatalf("record %+v: %v", record, err)
		}
	}
	got := make(chan int64, 1)
	go func() {
		record, err := tail.Next(context.Background())
		if err != nil {
			t.Error(err)
		}
		got <- record.Seq
	}()
	if _, err := journal.Append(shimlink.Record{Line: "live"}); err != nil {
		t.Fatal(err)
	}
	if seq := <-got; seq != 4 {
		t.Errorf("seq %d", seq)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tail.Next(t.Context()); !errors.Is(err, io.EOF) {
		t.Errorf("after the close: %v", err)
	}
}

func dirSize(t *testing.T, dir string) int64 {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
	}
	return total
}

// TestRotationHoldsTheCapWithASlowReader is D21. A reader that stops costs no more than
// the cap, and when it returns it is told what it lost, and reads on in order.
func TestRotationHoldsTheCapWithASlowReader(t *testing.T) {
	const capBytes = 8 << 10
	const total = 2000
	journal, dir := openJournal(t, capBytes)
	var line string
	for i := range total {
		line = fmt.Sprintf("line-%08d-%s", i, strings.Repeat("x", 40))
		if _, err := journal.Append(shimlink.Record{Line: line}); err != nil {
			t.Fatal(err)
		}
		if size := dirSize(t, dir); size > capBytes+int64(len(line))*2 {
			t.Fatalf("the journal holds %d bytes after %d records", size, i)
		}
	}
	tail := journal.Tail(0)
	defer tail.Close()
	gap, err := tail.Next(t.Context())
	if err != nil || gap.Gap == 0 {
		t.Fatalf("no gap reported: %+v, %v", gap, err)
	}
	next := gap.Gap + 1
	for next <= total {
		record, err := tail.Next(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if record.Seq != next {
			t.Fatalf("record %d, want %d", record.Seq, next)
		}
		next++
	}
}

// A reader inside a segment when rotation removes it finishes that file, then learns
// what went past it.
func TestAReaderBehindRotationGoesOn(t *testing.T) {
	const capBytes = 8 << 10
	journal, _ := openJournal(t, capBytes)
	for i := range 20 {
		if _, err := journal.Append(shimlink.Record{Line: fmt.Sprintf("line-%d-%s", i, strings.Repeat("x", 40))}); err != nil {
			t.Fatal(err)
		}
	}
	tail := journal.Tail(0)
	defer tail.Close()
	if record, err := tail.Next(t.Context()); err != nil || record.Seq != 1 {
		t.Fatalf("record %+v: %v", record, err)
	}
	const total = 2000
	for i := 20; i < total; i++ {
		if _, err := journal.Append(shimlink.Record{Line: fmt.Sprintf("line-%d-%s", i, strings.Repeat("x", 40))}); err != nil {
			t.Fatal(err)
		}
	}
	var last int64
	sawGap := false
	for last < total {
		record, err := tail.Next(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if record.Gap != 0 {
			sawGap, last = true, record.Gap
			continue
		}
		last = record.Seq
	}
	if !sawGap {
		t.Error("a reader far behind rotation was told nothing")
	}
}

func TestATornLastLineIsNotARecord(t *testing.T) {
	journal, dir := openJournal(t, 0)
	if _, err := journal.Append(shimlink.Record{Line: "whole"}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "journal-*"))
	if err != nil || len(files) != 1 {
		t.Fatalf("files %v: %v", files, err)
	}
	file, err := os.OpenFile(files[0], os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"seq":2,"line":"torn`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	records, err := shimlink.ReadJournal(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Line != "whole" {
		t.Errorf("records %+v", records)
	}
}

func TestReadJournalOfNothing(t *testing.T) {
	records, err := shimlink.ReadJournal(t.TempDir(), 0)
	if err != nil || len(records) != 0 {
		t.Errorf("records %+v: %v", records, err)
	}
}
