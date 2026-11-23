package mcpserver_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/mcpserver"
)

// afterWrite runs a hook as the first line of an answer is written, so a test can read what stood at that moment.
type afterWrite struct {
	bytes.Buffer
	at func()
}

func (w *afterWrite) Write(p []byte) (int, error) {
	if w.at != nil && w.Len() == 0 {
		w.at()
	}
	return w.Buffer.Write(p)
}

const (
	initializeLine = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	listLine       = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n"
	pingLine       = `{"jsonrpc":"2.0","id":3,"method":"ping","params":{}}` + "\n"
)

// A session's CLI connects ropeshift's server in the background, and the worker briefs the session only once the
// server has answered the CLI's tools/list, so the CLI holds the tools when the first prompt arrives. The server says
// so with a file at the path its environment names, made once the answer is written, and for no other request.
func TestTheServerMarksTheSessionOnceItHasAnsweredToolsList(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "tools-listed-1")
	w := &worker{owner: "lane"}
	var standing []bool
	out := &afterWrite{}
	out.at = func() {
		_, err := os.Stat(marker)
		standing = append(standing, err == nil)
	}
	if err := mcpserver.ServeMarked(strings.NewReader(initializeLine+pingLine), out, w.call, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a request that lists no tools made the marker: %v", err)
	}

	standing, out = nil, &afterWrite{}
	out.at = func() {
		_, err := os.Stat(marker)
		standing = append(standing, err == nil)
	}
	if err := mcpserver.ServeMarked(strings.NewReader(listLine), out, w.call, marker); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"tools"`) {
		t.Fatalf("the answer lists no tools: %q", out.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the server did not mark a session it listed tools for: %v", err)
	}
	if len(standing) != 1 || standing[0] {
		t.Errorf("the marker stood %v as the answer was written, and it may be made only after", standing)
	}
}

// A marker is only a signal: a path the server cannot write, and a server started with none, change nothing it
// answers.
func TestAMarkerTheServerCannotWriteChangesNoAnswer(t *testing.T) {
	w := &worker{owner: "lane"}
	for name, marker := range map[string]string{
		"no marker":          "",
		"a directory absent": filepath.Join(t.TempDir(), "absent", "tools-listed-1"),
	} {
		var out bytes.Buffer
		if err := mcpserver.ServeMarked(strings.NewReader(listLine), &out, w.call, marker); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), `"tools"`) {
			t.Errorf("%s: the answer lists no tools: %q", name, out.String())
		}
	}
}
