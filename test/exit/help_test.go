package exit

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// runAs runs the built binary under one of its names, in the install's environment and with no
// stdin, as a person's shell would with nothing piped in.
func (in *install) runAs(binary string, args ...string) (int, string, string) {
	in.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = in.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.String(), stderr.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), stdout.String(), stderr.String()
	}
	in.t.Fatalf("%v: %v", args, err)
	return 0, "", ""
}

// TestTheBinaryExplainsItself runs the built binary under both names, as an installed one starts.
// A bare command lists every command on stderr and exits 2, help prints the same list on stdout,
// one command answers --help, the MCP server answers it without waiting on stdin, and --version is
// the version command.
func TestTheBinaryExplainsItself(t *testing.T) {
	in := newInstall(t)
	for binary, name := range map[string]string{binaries.cli: "ropeshift", binaries.daemon: "ropeshiftd"} {
		code, stdout, bare := in.runAs(binary)
		if code != 2 || stdout != "" || !strings.HasPrefix(bare, "usage: "+name+" <command>") {
			t.Errorf("a bare %s: exit %d, stdout %q, stderr %q", name, code, stdout, bare)
		}
		for _, form := range []string{"help", "--help", "-h"} {
			if code, help, stderr := in.runAs(binary, form); code != 0 || stderr != "" || help != bare {
				t.Errorf("%s %s: exit %d, stderr %q, stdout\n%s", name, form, code, stderr, help)
			}
		}
		_, version, _ := in.runAs(binary, "version")
		if code, stdout, _ := in.runAs(binary, "--version"); code != 0 || stdout != version || version == "" {
			t.Errorf("%s --version: exit %d, %q, want %q", name, code, stdout, version)
		}
	}
	code, mcp, stderr := in.runAs(binaries.cli, "mcp", "--help")
	if code != 0 || stderr != "" || !strings.HasPrefix(mcp, "usage: ropeshift mcp") || !strings.Contains(mcp, "stdio") {
		t.Errorf("ropeshift mcp --help: exit %d, stderr %q, stdout\n%s", code, stderr, mcp)
	}
	code, solo, stderr := in.runAs(binaries.daemon, "solo", "--help")
	if code != 0 || stderr != "" || !strings.Contains(solo, "-source") {
		t.Errorf("ropeshiftd solo --help: exit %d, stderr %q, stdout\n%s", code, stderr, solo)
	}
	if code, _, stderr := in.runAs(binaries.cli, "launch"); code != 2 || !strings.Contains(stderr, "ropeshift help") {
		t.Errorf("an unknown command: exit %d, stderr %q", code, stderr)
	}
}
