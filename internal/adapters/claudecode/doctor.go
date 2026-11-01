package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// statusTimeout bounds the vendor's status command.
const statusTimeout = 30 * time.Second

// maxStatusOutput bounds what the status command can print.
const maxStatusOutput = 1 << 20

// signIn runs `claude auth status`, the one sign-in command this code runs, which the vendor
// documents as a status read. It decodes `authMethod` and nothing else, so an email or an
// organization in the same answer never reaches a variable. It exits 1 when signed out, so
// the answer, not the exit code, decides. A real-session build runs no such command.
func (d *Driver) signIn(ctx context.Context) (kind, status string) {
	if !lanes.ReadsVendorStatus {
		return "", lanes.SignInUnknown
	}
	env := d.versionEnv()
	binary, err := findBinary(env)
	if err != nil {
		return "", lanes.SignInUnknown
	}
	var out bytes.Buffer
	process, err := d.runner.Start(ctx, ports.ProcessSpec{
		Path: binary, Args: []string{"auth", "status"}, Env: env, Stdout: &limitedWriter{target: &out, room: maxStatusOutput},
	})
	if err != nil {
		return "", lanes.SignInUnknown
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = process.Wait()
	}()
	select {
	case <-finished:
	case <-time.After(statusTimeout):
		_ = process.Signal(ports.SignalKill)
		return "", lanes.SignInUnknown
	}
	var answer struct {
		Method string `json:"authMethod"`
	}
	if json.Unmarshal(out.Bytes(), &answer) != nil {
		return "", lanes.SignInUnknown
	}
	return lanes.ReadSignIn(answer.Method)
}

type limitedWriter struct {
	target *bytes.Buffer
	room   int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	kept := min(len(p), w.room)
	w.target.Write(p[:kept])
	w.room -= kept
	return len(p), nil
}

// projectSettings are the files that can tighten the inbound policy past `--settings`. A
// worktree shares its checkout's local file, so both places count.
func projectSettings(spec ports.SessionSpec) []string {
	paths := []string{filepath.Join(spec.Dir, ".claude", "settings.json"), filepath.Join(spec.Dir, ".claude", "settings.local.json")}
	if spec.Repository != "" {
		paths = append(paths, filepath.Join(spec.Repository, ".claude", "settings.local.json"))
	}
	return paths
}

// inboundProblems reads the inbound message policy (FR-DRV-11). A lane's generated settings
// say `accept`, and the vendor documents that a stricter project value wins over them, so a
// repository can silently stop every message and every brief to a pane. A file that does not
// parse is one the CLI skips too, so it decides nothing here. This reads no other key.
func inboundProblems(spec ports.SessionSpec) []string {
	var problems []string
	for _, path := range projectSettings(spec) {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("cannot read %s: %v", path, err))
			continue
		}
		var settings struct {
			Inbound string `json:"crossSessionInbound"`
		}
		if json.Unmarshal(raw, &settings) != nil || settings.Inbound == "" || settings.Inbound == inboundAccept {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s sets crossSessionInbound to %s, so a session would not take its input", path, settings.Inbound))
	}
	return problems
}
