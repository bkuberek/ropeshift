package cli_test

import (
	"bytes"
	"net/http"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/version"
	"ropeshift.com/ropeshift/test/apitest"
)

// templatesRead is the call `template show` makes of a daemon.
const templatesRead = "GET /v1/templates"

// templateDaemon is a control plane that answers the templates route with the list of its own binary at a
// version, or with a refusal.
func templateDaemon(t *testing.T, build string, list []api.Template) *plane {
	t.Helper()
	p := newPlane(t)
	p.on(templatesRead, func(*http.Request, []byte) (any, *api.Error) {
		return api.TemplateList{Version: build, Templates: list}, nil
	})
	return p
}

// theCommands are every form `template show` takes, with the arguments of each.
var theCommands = map[string][]string{
	"the list":      {"template", "show"},
	"the JSON list": {"template", "show", "--json"},
	"a template":    {"template", "show", "workspace/agents.md"},
	"another":       {"template", "show", "user/profile.toml"},
	"an unknown":    {"template", "show", "workspace/agent.md"},
	"a group":       {"template", "show", "workspace"},
}

// showOn runs a command against a socket and returns what it printed.
func showOn(socket string, args []string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, cli.Options{Socket: socket})
	return code, stdout.String(), stderr.String()
}

// FR-TPL-5: the command reads the daemon's copy when the daemon answers on the socket and runs this build, and
// its embedded copy otherwise. Either way it prints the same text and lists the same templates, and it says
// nothing of which it read.
func TestTemplateShowPrintsTheSameWhicheverCopyItReads(t *testing.T) {
	list, err := apitest.ExampleTemplates()
	if err != nil {
		t.Fatal(err)
	}
	own := version.Read("").Version
	same := templateDaemon(t, own, list)
	other := templateDaemon(t, own+"-another-build", []api.Template{{Name: "claude.md", Shapes: []string{}, WritesTo: "OTHER", Content: "from another build\n"}})
	// A daemon that serves no templates, as one of an earlier build, which answers the route as unknown.
	older := newPlane(t)
	missing := filepath.Join(t.TempDir(), "no-daemon.sock")

	for name, args := range theCommands {
		wantCode, wantOut, wantErr := bare(args...)
		for source, socket := range map[string]string{"a daemon of this version": same.Socket, "a daemon of another version": other.Socket, "a daemon with no templates": older.Socket, "no daemon": missing} {
			code, stdout, stderr := showOn(socket, args)
			if code != wantCode || stdout != wantOut || stderr != wantErr {
				t.Errorf("%s with %s: exit %d, stdout %d bytes, stderr %q; the embedded copy gives exit %d, %d bytes, stderr %q",
					name, source, code, len(stdout), stderr, wantCode, len(wantOut), wantErr)
			}
		}
	}
	if wantCode, _, _ := bare("template", "show", "claude.md"); wantCode != cli.ExitOK {
		t.Fatalf("the embedded copy of claude.md exits %d", wantCode)
	}
}

// The daemon of this version is the one read, once for each command, and the daemon of another version is asked
// and not believed: its list and its text never reach the output.
func TestTemplateShowReadsTheDaemonOfThisVersionAndNoOther(t *testing.T) {
	list, err := apitest.ExampleTemplates()
	if err != nil {
		t.Fatal(err)
	}
	own := version.Read("").Version
	same := templateDaemon(t, own, list)
	other := templateDaemon(t, "v0.0.1-other", []api.Template{{Name: "claude.md", Shapes: []string{}, WritesTo: "OTHER", Content: "from another build\n"}})
	for _, args := range [][]string{{"template", "show"}, {"template", "show", "--json"}, {"template", "show", "claude.md"}, {"template", "show", "no/such"}} {
		if code, _, _ := showOn(same.Socket, args); code != cli.ExitOK && code != cli.ExitUsage {
			t.Errorf("%v exits %d", args, code)
		}
		showOn(other.Socket, args)
	}
	for name, daemon := range map[string]*plane{"the same version": same, "another version": other} {
		reads := daemon.reads()
		if len(reads) != 4 {
			t.Errorf("%s was called %d times, want once for each of 4 commands: %v", name, len(reads), reads)
		}
		for _, read := range reads {
			if read != templatesRead {
				t.Errorf("%s was called with %q, and the command reads nothing else", name, read)
			}
		}
	}
	_, out, _ := showOn(other.Socket, []string{"template", "show", "claude.md"})
	if out == "from another build\n" {
		t.Errorf("the text of another build's daemon was printed")
	}
}

// What the daemon holds is what is printed: a daemon of this version whose copy differs shows that the copy,
// which is the read this test proves was made, and which holds only while the versions are the same.
func TestTemplateShowPrintsTheDaemonsCopyWhenTheVersionsAreTheSame(t *testing.T) {
	own := version.Read("").Version
	same := templateDaemon(t, own, []api.Template{
		{Name: "a/one.md", Shapes: []string{"workspace", "monorepo"}, WritesTo: "ONE.md", Content: "the daemon's first\n"},
		{Name: "b/two.md", Shapes: []string{}, WritesTo: "", Content: "the daemon's second\n"},
	})
	if _, out, _ := showOn(same.Socket, []string{"template", "show", "a/one.md"}); out != "the daemon's first\n" {
		t.Errorf("printed %q", out)
	}
	if _, out, _ := showOn(same.Socket, []string{"template", "show"}); out != "a/one.md\nb/two.md\n" {
		t.Errorf("listed %q", out)
	}
	_, out, _ := showOn(same.Socket, []string{"template", "show", "--json"})
	if want := `[{"name":"a/one.md","writes_to":"ONE.md"},{"name":"b/two.md","writes_to":""}]` + "\n"; out != want {
		t.Errorf("listed %q, want %q", out, want)
	}
}

// A daemon that refuses the read, or answers what is no list, leaves the embedded copy, with nothing said.
func TestTemplateShowFallsBackWhenTheDaemonRefusesTheRead(t *testing.T) {
	refusing := newPlane(t)
	refusing.on(templatesRead, func(*http.Request, []byte) (any, *api.Error) {
		return nil, &api.Error{Status: http.StatusNotImplemented, Detail: api.ErrorDetail{Code: api.CodeUnavailable, Message: "this daemon serves no templates"}}
	})
	for name, args := range theCommands {
		wantCode, wantOut, wantErr := bare(args...)
		if code, stdout, stderr := showOn(refusing.Socket, args); code != wantCode || stdout != wantOut || stderr != wantErr {
			t.Errorf("%s: exit %d, stderr %q, stdout %d bytes; want exit %d, stderr %q, %d bytes", name, code, stderr, len(stdout), wantCode, wantErr, len(wantOut))
		}
	}
}
