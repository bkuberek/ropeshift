package entry

import (
	"bytes"
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/cli"
)

// isolate points the CLI at a state and run directory of the test's own, so no command a test
// routes can reach this machine's installation, and gives the process an empty stdin, so a command
// that reads one ends instead of waiting.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv(api.EnvStateDir, t.TempDir())
	t.Setenv(api.EnvRunDir, t.TempDir())
	empty, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = empty
	t.Cleanup(func() {
		os.Stdin = stdin
		_ = empty.Close()
	})
}

func runMain(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Main(args, &stdout, &stderr)
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

// TestABareCommandListsEveryCommand is a command with no arguments: a usage error, so it exits 2, and
// it prints on stderr what help prints, which names every command, how to ask about one, and the
// reference.
func TestABareCommandListsEveryCommand(t *testing.T) {
	isolate(t)
	lists := map[string][]string{
		commandCLI:    {"help", "version", "hook", "mcp", "statusline", "lane-pane", "lane-shim", "view <name>"},
		commandDaemon: {"solo", "help", "version"},
	}
	for _, command := range cli.Commands() {
		if !strings.HasPrefix(command, "view ") {
			lists[commandCLI] = append(lists[commandCLI], command)
		}
	}
	for binary, commands := range lists {
		t.Run(binary, func(t *testing.T) {
			code, stdout, stderr := runMain(binary)
			if code != exitUsage || stdout != "" {
				t.Fatalf("exit %d, stdout %q", code, stdout)
			}
			if _, help, _ := runMain(binary, "help"); stderr != help {
				t.Errorf("a bare %s prints\n%s\nand help prints\n%s", binary, stderr, help)
			}
			for _, command := range commands {
				if !listed(stderr, command) {
					t.Errorf("%s is not listed:\n%s", command, stderr)
				}
			}
			for _, pointer := range []string{binary + " help <command>", binary + " <command> --help", "docs/cli.md"} {
				if !strings.Contains(stderr, pointer) {
					t.Errorf("the list does not say %q:\n%s", pointer, stderr)
				}
			}
		})
	}
}

// Every way of asking for help prints the same list on stdout and exits 0.
func TestEveryHelpFormPrintsTheList(t *testing.T) {
	isolate(t)
	for _, binary := range []string{commandCLI, commandDaemon} {
		_, want, _ := runMain(binary, "help")
		if !strings.HasPrefix(want, "usage: "+binary+" <command>") {
			t.Fatalf("%s help prints %q", binary, want)
		}
		for _, form := range []string{"help", "-h", "--help", "-help"} {
			code, stdout, stderr := runMain(binary, form)
			if code != exitOK || stderr != "" {
				t.Errorf("%s %s: exit %d, stderr %q", binary, form, code, stderr)
			}
			if stdout != want {
				t.Errorf("%s %s prints\n%s", binary, form, stdout)
			}
		}
	}
}

// --version is the version command, so its --json stays the command's.
func TestTheVersionFlagIsTheVersionCommand(t *testing.T) {
	isolate(t)
	for _, binary := range []string{commandCLI, commandDaemon} {
		_, want, _ := runMain(binary, "version")
		for _, form := range []string{"--version", "-version"} {
			code, stdout, stderr := runMain(binary, form)
			if code != exitOK || stderr != "" || stdout != want {
				t.Errorf("%s %s: exit %d, stdout %q, stderr %q, want %q", binary, form, code, stdout, stderr, want)
			}
		}
		code, stdout, stderr := runMain(binary, "--version", "--json")
		var got versionInfo
		if err := json.Unmarshal([]byte(stdout), &got); code != exitOK || err != nil || got.Command != binary {
			t.Errorf("%s --version --json: exit %d, %+v, %v, stderr %q", binary, code, got, err, stderr)
		}
	}
}

// The MCP server answers help without reading stdin, and says who starts it.
func TestTheMCPServerAnswersHelp(t *testing.T) {
	isolate(t)
	code, stdout, stderr := runMain(commandCLI, "mcp", "--help")
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, says := range []string{"usage: ropeshift mcp", "stdio", "agent CLI", "by hand"} {
		if !strings.Contains(stdout, says) {
			t.Errorf("the help does not say %q:\n%s", says, stdout)
		}
	}
}

// The daemon's command answers help with its flags.
func TestTheDaemonAnswersHelp(t *testing.T) {
	isolate(t)
	code, stdout, stderr := runMain(commandDaemon, "solo", "--help")
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "usage: ropeshiftd solo") || !strings.Contains(stdout, "-source") {
		t.Errorf("the help does not hold the usage and the flags:\n%s", stdout)
	}
	if _, help, _ := runMain(commandDaemon, "help", "solo"); help != stdout {
		t.Errorf("help solo prints\n%s", help)
	}
}

// described is every command a binary answers, from its route table and, for the CLI, the CLI's
// registry, as help describes each.
func described(binary string) []cli.Command {
	var all []cli.Command
	for _, group := range groups(binary) {
		all = append(all, group.Commands...)
	}
	return all
}

// TestEveryCommandAnswersHelp walks every command each binary routes, its own and the CLI's. Each has
// a summary, is in the list of every command, and answers --help, -h, and help <command> alike, with
// exit 0 and its usage on stdout. A route added without a summary, or one that runs before it can
// answer help, as the MCP server once did, fails here.
func TestEveryCommandAnswersHelp(t *testing.T) {
	isolate(t)
	for _, binary := range []string{commandCLI, commandDaemon} {
		_, list, _ := runMain(binary, "help")
		for _, command := range described(binary) {
			t.Run(binary+" "+command.Name, func(t *testing.T) {
				if command.Summary == "" {
					t.Error("no summary")
				}
				if line := cmp.Or(command.Family, command.Name); !listed(list, line) {
					t.Errorf("%q is not in the list of every command", line)
				}
				words := strings.Fields(command.Name)
				code, want, stderr := runMain(append(append([]string{binary}, words...), "--help")...)
				if first, _, _ := strings.Cut(want, "\n"); code != exitOK || stderr != "" || !strings.HasPrefix(first+" ", "usage: "+binary+" "+command.Name+" ") {
					t.Fatalf("--help: exit %d, stderr %q, stdout\n%s", code, stderr, want)
				}
				for _, args := range [][]string{append(slices.Clone(words), "-h"), append([]string{"help"}, words...)} {
					code, stdout, stderr := runMain(append([]string{binary}, args...)...)
					if code != exitOK || stderr != "" || stdout != want {
						t.Errorf("%v: exit %d, stderr %q, stdout\n%s", args, code, stderr, stdout)
					}
				}
			})
		}
	}
}

// TestTheReferenceListsEveryCommand holds docs/cli.md to the router both ways: every command a
// binary answers has a line there, and every command a table there names is one a binary answers.
func TestTheReferenceListsEveryCommand(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	docs := string(raw)
	for _, binary := range []string{commandCLI, commandDaemon} {
		for _, group := range routes(binary) {
			for _, r := range group.routes {
				if !regexp.MustCompile("`" + binary + " " + regexp.QuoteMeta(r.Name) + "[` ]").MatchString(docs) {
					t.Errorf("%s %s is not in docs/cli.md", binary, r.Name)
				}
			}
		}
	}
	row := regexp.MustCompile("(?m)^\\| `(ropeshiftd?)((?: [a-z][a-z-]*)+)")
	rows := row.FindAllStringSubmatch(docs, -1)
	if len(rows) < 80 {
		t.Fatalf("read %d command rows, so the scan found too few", len(rows))
	}
	for _, found := range rows {
		binary, words := found[1], strings.Fields(found[2])
		if _, ok := findRoute(binary, words[0]); ok {
			continue
		}
		if binary == commandCLI && (cli.HasCommand(strings.Join(words, " ")) || cli.HasCommand(words[0]) ||
			len(words) == 1 && cli.IsCommand(words[0])) {
			continue
		}
		t.Errorf("docs/cli.md lists %s %s, which no binary answers", binary, strings.Join(words, " "))
	}
}

func TestAnUnknownCommandPointsToHelp(t *testing.T) {
	isolate(t)
	tests := []struct {
		name string
		args []string
		says []string
	}{
		{"an unknown CLI command", []string{commandCLI, "launch"}, []string{`unknown command "launch"`, "ropeshift help"}},
		{"an unknown daemon command", []string{commandDaemon, "launch"}, []string{`unknown command "launch"`, "ropeshiftd help"}},
		{"a flag that is no command", []string{commandCLI, "--launch"}, []string{`unknown flag "--launch"`, "ropeshift help"}},
		{"an unknown command in a group", []string{commandCLI, "work", "launch"}, []string{`unknown command "work launch"`, "ropeshift help work"}},
		{"help on an unknown command", []string{commandCLI, "help", "launch"}, []string{`unknown command "launch"`, "ropeshift help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runMain(tt.args...)
			if code != exitUsage || stdout != "" {
				t.Fatalf("exit %d, stdout %q", code, stdout)
			}
			for _, says := range tt.says {
				if !strings.Contains(stderr, says) {
					t.Errorf("stderr %q does not say %q", stderr, says)
				}
			}
		})
	}
}
