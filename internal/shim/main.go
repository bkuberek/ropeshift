package shim

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
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
// takeEnvFile reads a file of environment entries, each ended by a NUL byte, and removes it first, so what
// it holds reaches the child and is left nowhere else.
func takeEnvFile(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	var env []string
	for _, entry := range strings.Split(string(raw), "\x00") {
		if entry != "" {
			env = append(env, entry)
		}
	}
	return env, nil
}

func Main(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("lane-shim", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config shimlink.Config
	flags.StringVar(&config.Dir, "dir", "", "the directory of this generation's journal")
	flags.StringVar(&config.Socket, "socket", "", "the socket clients reach this shim on")
	flags.DurationVar(&config.Linger, "linger", 0, "how long to stay after the child ends")
	flags.Int64Var(&config.JournalCap, "journal-cap", 0, "the journal's limit in bytes")
	flags.StringVar(&config.EnvFile, "env-file", "", "a private file of the child's own environment, which this removes")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if config.EnvFile != "" {
		env, err := takeEnvFile(config.EnvFile)
		if err != nil {
			fmt.Fprintf(stderr, "lane-shim: %v\n", err)
			return exitError
		}
		config.Env = env
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
