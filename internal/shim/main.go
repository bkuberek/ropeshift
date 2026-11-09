package shim

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"ropeshift.com/ropeshift/internal/api/shimlink"
)

// The shim's exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// Main is `ropeshift lane-shim`. A signal to this process ends it, and the child finishes
// its turn and exits because its stdin closed. The shim ignores a hangup, because its
// session has no terminal for one to mean anything.
func Main(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("lane-shim", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config shimlink.Config
	flags.StringVar(&config.Dir, "dir", "", "the directory of this generation's journal")
	flags.StringVar(&config.Socket, "socket", "", "the socket clients reach this shim on")
	flags.DurationVar(&config.Linger, "linger", 0, "how long to stay after the child ends")
	flags.Int64Var(&config.JournalCap, "journal-cap", 0, "the journal's limit in bytes")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	config.Command = flags.Args()
	if config.Dir == "" || config.Socket == "" || len(config.Command) == 0 {
		fmt.Fprintln(stderr, "usage: ropeshift lane-shim --dir D --socket S -- command [args]")
		return exitUsage
	}
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
	err := Run(context.Background(), config)
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, ErrLocked):
		fmt.Fprintf(stderr, "lane-shim: %v\n", err)
		return shimlink.ExitLocked
	}
	fmt.Fprintf(stderr, "lane-shim: %v\n", err)
	return exitError
}
