package gitcli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Stream runs one command and hands its stdout to read as git writes it, for output too large to hold, such as the
// patches of a whole history. When read returns an error the command is stopped and that error is returned. When it
// returns nil, what it left unread is discarded and the command's own failure is returned.
func (g Git) Stream(ctx context.Context, read func(io.Reader) error, args ...string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	cmd.Env = g.environment()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	readErr := read(stdout)
	if readErr != nil {
		cancel()
	} else {
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}
