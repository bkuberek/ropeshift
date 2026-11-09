package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
)

// alone runs one command line with no control plane to reach.
func alone(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, cli.Options{})
	return code, stdout.String(), stderr.String()
}

// listed reports a line of text that starts with the command's words.
func listed(text, command string) bool {
	words := strings.Fields(command)
	for line := range strings.Lines(text) {
		fields := strings.Fields(line)
		if len(fields) >= len(words) && strings.Join(fields[:len(words)], " ") == command {
			return true
		}
	}
	return false
}

// TestEveryCommandAnswersHelp walks every command the package routes. Each answers --help and -h
// with exit 0 and its usage on stdout: the usage line, one line on what it does, and its flags. A
// command routed without a summary, or one that acts before it reads its flags, fails here.
func TestEveryCommandAnswersHelp(t *testing.T) {
	commands := cli.Commands()
	if len(commands) < 80 {
		t.Fatalf("the registry lists %d commands", len(commands))
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			for _, form := range []string{"--help", "-h"} {
				code, stdout, stderr := alone(append(strings.Fields(command), form)...)
				if code != cli.ExitOK || stderr != "" {
					t.Fatalf("%s: exit %d, stderr %q", form, code, stderr)
				}
				lines := strings.Split(stdout, "\n")
				if len(lines) < 5 || !strings.HasPrefix(lines[0]+" ", "usage: ropeshift "+command+" ") || lines[1] != "" || lines[2] == "" {
					t.Fatalf("%s: the help holds no usage line and summary:\n%s", form, stdout)
				}
				if !strings.Contains(stdout, "\nFlags:\n") || !strings.Contains(stdout, "-json") {
					t.Errorf("%s: the help lists no flags:\n%s", form, stdout)
				}
			}
		})
	}
}

// The registry that routes commands is the one help reads: each command is in one group, once, with
// a summary.
func TestTheRegistryDescribesEachCommandOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, group := range cli.Groups() {
		if group.Title == "" || len(group.Commands) == 0 {
			t.Errorf("group %+v", group)
		}
		for _, command := range group.Commands {
			if seen[command.Name] {
				t.Errorf("%s is registered twice", command.Name)
			}
			seen[command.Name] = true
			if command.Summary == "" {
				t.Errorf("%s has no summary", command.Name)
			}
		}
	}
	if commands := cli.Commands(); len(commands) != len(seen) {
		t.Errorf("Commands lists %d, and the groups hold %d", len(commands), len(seen))
	}
}

// A group's first word lists the commands under it: on stdout when help is asked for, and on
// stderr with exit 2 when the command is missing.
func TestAGroupListsItsCommands(t *testing.T) {
	var work []string
	for _, command := range cli.Commands() {
		if strings.HasPrefix(command, "work ") {
			work = append(work, command)
		}
	}
	code, stdout, stderr := alone("work", "--help")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("work --help: exit %d, stderr %q", code, stderr)
	}
	code, missing, stderrMissing := alone("work")
	if code != cli.ExitUsage || missing != "" {
		t.Fatalf("work: exit %d, stdout %q", code, missing)
	}
	for _, command := range work {
		if !listed(stdout, command) {
			t.Errorf("work --help does not list %s:\n%s", command, stdout)
		}
		if !listed(stderrMissing, command) {
			t.Errorf("work does not list %s:\n%s", command, stderrMissing)
		}
	}
}

func TestAnUnknownCommandPointsToHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		hint string
	}{
		{"an unknown first word", []string{"nonsense"}, "ropeshift help"},
		{"an unknown command in a group", []string{"work", "nonsense"}, "ropeshift help work"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := alone(tt.args...)
			if code != cli.ExitUsage || stdout != "" {
				t.Fatalf("exit %d, stdout %q", code, stdout)
			}
			if !strings.Contains(stderr, "unknown command") || !strings.Contains(stderr, tt.hint) {
				t.Errorf("stderr %q names no unknown command and no %q", stderr, tt.hint)
			}
		})
	}
}
