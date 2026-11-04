package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// desktopTimeout bounds one notice, because a desktop session that went away must not hold the tick.
const desktopTimeout = 5 * time.Second

const appName = "ropeshift"

// desktopScript takes the text as an argument and never as source, so no text can become a command.
var desktopScript = []string{"-e", "on run argv", "-e", `display notification (item 1 of argv) with title "` + appName + `"`, "-e", "end run"}

// DesktopConfig is what a desktop channel runs on.
type DesktopConfig struct {
	GOOS      string
	Processes ports.ProcessRunner
	LookPath  func(string) (string, error)
	// Env is the whole environment of the program, which the desktop session's variables reach.
	Env []string
}

// Desktop shows a notice on this machine, through the program the platform has for that. It starts
// nothing until a notice goes out, and a workspace turns it on.
type Desktop struct {
	config DesktopConfig
}

// NewDesktop returns a channel.
func NewDesktop(config DesktopConfig) *Desktop { return &Desktop{config: config} }

// Channel names this channel.
func (*Desktop) Channel() string { return ChannelDesktop }

func (d *Desktop) command(text string) (string, []string, error) {
	switch d.config.GOOS {
	case "darwin":
		path, err := d.config.LookPath("osascript")
		return path, append(append([]string{}, desktopScript...), text), err
	case "linux":
		path, err := d.config.LookPath("notify-send")
		return path, []string{"--app-name=" + appName, appName, text}, err
	}
	return "", nil, fmt.Errorf("%s has no desktop channel", d.config.GOOS)
}

// Notify shows one notice. Only its fixed text reaches the desktop.
func (d *Desktop) Notify(ctx context.Context, notice ledger.Notice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, args, err := d.command(notice.Text)
	if err != nil {
		return errors.New("no program here shows a desktop notice")
	}
	process, err := d.config.Processes.Start(ctx, ports.ProcessSpec{Path: path, Args: args, Env: d.config.Env})
	if err != nil {
		return errors.New("the desktop program did not start")
	}
	type ended struct {
		status ports.ExitStatus
		err    error
	}
	done := make(chan ended, 1)
	go func() {
		status, err := process.Wait()
		done <- ended{status, err}
	}()
	select {
	case result := <-done:
		if result.err != nil {
			return errors.New("the desktop program did not finish")
		}
		if result.status.Code != 0 {
			return fmt.Errorf("the desktop program exited %d", result.status.Code)
		}
		return nil
	case <-time.After(desktopTimeout):
		_ = process.Signal(ports.SignalKill)
		return errors.New("the desktop program did not answer in time")
	case <-ctx.Done():
		_ = process.Signal(ports.SignalKill)
		return ctx.Err()
	}
}

var _ ports.Notifier = (*Desktop)(nil)
