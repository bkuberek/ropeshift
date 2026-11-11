package exit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// webURL reads the address the daemon says it serves, which a test asked it to choose.
func (d *daemon) webURL(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for line := range strings.Lines(d.stdout.String()) {
			if address, found := strings.CutPrefix(strings.TrimSpace(line), "ropeshiftd: web "); found {
				return address
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the daemon never named its web address: %s", d.stdout.String())
	return ""
}

func webCall(t *testing.T, method, url string, body any, cookies []*http.Cookie, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	for name, value := range headers {
		if name == "Host" {
			request.Host = value
			continue
		}
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, raw
}

// TestTheBinaryServesItsOwnWebUI is the real binary with no Node.js on its PATH: the page, a sign-in, a
// read, and the checks that stand in front of them. Which page depends on whether this tree was built.
func TestTheBinaryServesItsOwnWebUI(t *testing.T) {
	in := newInstall(t)
	d := in.start("--web", "127.0.0.1:0")
	in.waitReady(d)
	base := d.webURL(t)
	if _, err := exec.LookPath("node"); err == nil {
		t.Log("this test's own PATH holds node, and the daemon's does not")
	}
	page, raw := webCall(t, http.MethodGet, base+"/", nil, nil, nil)
	if page.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte("ropeshift")) {
		t.Fatalf("page %d: %s", page.StatusCode, raw)
	}
	if !strings.HasPrefix(page.Header.Get("Content-Type"), "text/html") || page.Header.Get("Content-Security-Policy") == "" {
		t.Errorf("headers %v", page.Header)
	}
	view := base + api.InProject(ledger.DefaultProjectName, api.ViewPath(api.ViewNeedsYou))
	anonymous, _ := webCall(t, http.MethodGet, view, nil, nil, nil)
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Errorf("a read with no session: %d", anonymous.StatusCode)
	}
	foreign, _ := webCall(t, http.MethodGet, base+"/", nil, nil, map[string]string{"Host": "attacker.example"})
	if foreign.StatusCode != http.StatusForbidden {
		t.Errorf("a foreign host: %d", foreign.StatusCode)
	}
	signedIn, raw := webCall(t, http.MethodPost, base+api.SignInPath, api.SignIn{Passphrase: apitest.Passphrase}, nil, nil)
	if signedIn.StatusCode != http.StatusCreated {
		t.Fatalf("sign-in %d: %s", signedIn.StatusCode, raw)
	}
	read, raw := webCall(t, http.MethodGet, view, nil, signedIn.Cookies(), nil)
	if read.StatusCode != http.StatusOK {
		t.Fatalf("read %d: %s", read.StatusCode, raw)
	}
	var needs api.NeedsYou
	if err := json.Unmarshal(raw, &needs); err != nil {
		t.Fatal(err)
	}
	if needs.Revision == 0 {
		t.Errorf("view %+v", needs)
	}
}

func TestAWebAddressOffLoopbackStopsTheDaemon(t *testing.T) {
	in := newInstall(t)
	d := in.start("--web", "0.0.0.0:0")
	select {
	case err := <-d.exited:
		d.exited <- err
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.ExitCode() == 0 {
			t.Fatalf("exit %v", err)
		}
		if !strings.Contains(d.stderr.String(), "loopback") {
			t.Errorf("stderr %q", d.stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon served off loopback")
	}
}

// A port something else holds costs the UI and nothing else, because lanes do not wait on a page.
func TestAWebPortInUseLeavesTheDaemonRunning(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	in := newInstall(t)
	d := in.start("--web", "127.0.0.1:"+strconv.Itoa(busy.Addr().(*net.TCPAddr).Port))
	in.waitReady(d)
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(d.stderr.String(), "web:") {
		if time.Now().After(deadline) {
			t.Fatalf("nothing said the UI is off: %q", d.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebOffServesNoPort(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	if strings.Contains(d.stdout.String(), "ropeshiftd: web") {
		t.Errorf("stdout %q", d.stdout.String())
	}
}
