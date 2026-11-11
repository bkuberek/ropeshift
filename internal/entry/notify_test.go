package entry

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/notify"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
)

// watched is every way off this machine, with a count of each use. Nothing it hands out goes anywhere
// unless a test gives it a real dial.
type watched struct {
	notifyDeps
	dials atomic.Int64
}

func watching(t *testing.T, onward notify.Dial) *watched {
	t.Helper()
	w := &watched{}
	w.notifyDeps = notifyDeps{
		Processes: fake.NewProcessRunner(), GOOS: "linux",
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Lookup:   func(string) (string, bool) { return "", false },
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			w.dials.Add(1)
			if onward == nil {
				return nil, errors.New("a connection opened")
			}
			return onward(ctx, network, address)
		},
	}
	return w
}

func somethingWaits(t *testing.T) *apitest.Daemon {
	t.Helper()
	d := apitest.Start(t)
	apitest.NewWorld(t, d).Running("wo", "Rotate the keys").End(workorders.OutcomeBlocked, "no_outcome")
	return d
}

// With nothing configured, nothing goes out: this is the promise FR-API-4 is built on. Every way a
// channel has of leaving this machine runs through what this test holds, and a rule test keeps
// anything else from opening a connection. The second half proves the watch can see one.
func TestNoConfigurationOpensNoConnection(t *testing.T) {
	d := somethingWaits(t)
	deps := watching(t, nil)
	tick := noticeTick(d.Config, registryOf(d.Config.Store, d.Config.Scope), deps.notifyDeps, time.Minute)
	if err := tick.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := tick.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := deps.dials.Load(); got != 0 {
		t.Fatalf("%d connections opened with nothing configured", got)
	}
	if spawned := deps.Processes.(*fake.ProcessRunner).Spawned(); len(spawned) != 0 {
		t.Fatalf("a program ran with nothing configured: %+v", spawned)
	}
	lines, err := d.Client.Notices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Channel != notify.ChannelWeb || !lines[0].Delivered {
		t.Fatalf("lines %+v", lines)
	}

	body := "[notify_policy]\nversion = 2\nwebhook_url = \"https://hooks.example.com/ropeshift\"\n"
	if _, err := d.Client.PublishPolicy(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if err := tick.Run(t.Context()); err == nil {
		t.Fatal("a webhook nobody could reach reported nothing")
	}
	if got := deps.dials.Load(); got != 1 {
		t.Errorf("a published webhook opened %d connections", got)
	}
}

func TestTheDesktopChannelNeedsAPolicyToo(t *testing.T) {
	d := somethingWaits(t)
	deps := watching(t, nil)
	runner := deps.Processes.(*fake.ProcessRunner)
	runner.Install("/usr/bin/notify-send", func(ports.ProcessSpec, <-chan struct{}) int { return 0 })
	if _, err := d.Client.PublishPolicy(t.Context(), "[notify_policy]\nversion = 2\ndesktop = true\n"); err != nil {
		t.Fatal(err)
	}
	if err := noticeTick(d.Config, registryOf(d.Config.Store, d.Config.Scope), deps.notifyDeps, time.Minute).Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	spawned := runner.Spawned()
	if len(spawned) != 1 {
		t.Fatalf("spawned %+v", spawned)
	}
	if last := spawned[0].Args[len(spawned[0].Args)-1]; last != "A lane stopped, and it needs you." {
		t.Errorf("the desktop heard %q", last)
	}
}

// hostileWaiting lays out one of every kind of thing that waits, each full of what must not leave.
func hostileWaiting(t *testing.T) (*apitest.Daemon, []string) {
	t.Helper()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	apitest.Populate(apitest.NewWorld(t, d), clock)
	return d, []string{
		apitest.WorldHostileBit, "Rotate the keys", "Move the archive", "Trim the log", "wo-lapsed", "wo-blocked", "approval-1",
		"lane-wo-", "no_outcome", "git tag", "Should the budget", "worker-a",
	}
}

// A notice leaves the machine, so this reads a real one off the wire, from a ledger full of text that
// must stay here, and holds it to the fields the document states.
func TestANoticeCarriesNothingFromTheLedger(t *testing.T) {
	d, private := hostileWaiting(t)
	var mu sync.Mutex
	var bodies []string
	far := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body strings.Builder
		buffer := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buffer)
			body.Write(buffer[:n])
			if err != nil {
				break
			}
		}
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, r.URL.String()+" "+strings.Join(r.Header.Values("User-Agent"), ",")+" "+body.String())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer far.Close()
	if _, err := d.Client.PublishPolicy(t.Context(), "[notify_policy]\nversion = 2\nwebhook_url = \""+far.URL+"/hook\"\n"); err != nil {
		t.Fatal(err)
	}
	deps := watching(t, (&net.Dialer{}).DialContext)
	if err := noticeTick(d.Config, registryOf(d.Config.Store, d.Config.Scope), deps.notifyDeps, time.Minute).Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 5 {
		t.Fatalf("heard %d notices", len(bodies))
	}
	kinds := map[string]bool{}
	for _, sent := range bodies {
		for _, secret := range private {
			if strings.Contains(sent, secret) {
				t.Errorf("%q left the machine in %s", secret, sent)
			}
		}
		_, raw, _ := strings.Cut(sent, "ropeshift ")
		var fields map[string]any
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			t.Fatal(err)
		}
		kinds[fields["kind"].(string)] = true
		for name := range fields {
			if !slices.Contains(documentedFields(t), name) {
				t.Errorf("%s left the machine, and the document does not say so", name)
			}
		}
	}
	for _, kind := range []string{ledger.NoticeQuestions, ledger.NoticeApproval, ledger.NoticeEffect, ledger.NoticeWork} {
		if !kinds[kind] {
			t.Errorf("no %s was sent, so this proved nothing of it", kind)
		}
	}
}

// documentedFields reads the table of docs/notifications.md, so a field added to a notice fails until
// the document says so.
func documentedFields(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../docs/notifications.md")
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, match := range regexp.MustCompile("(?m)^\\| `([a-z]+)` \\|").FindAllStringSubmatch(string(raw), -1) {
		fields = append(fields, match[1])
	}
	return fields
}

func TestTheDocumentNamesEveryFieldOfANotice(t *testing.T) {
	documented := documentedFields(t)
	for _, field := range contract.NoticeFields(t) {
		if !slices.Contains(documented, field) {
			t.Errorf("%s is not in the document", field)
		}
	}
}

// A failure reaches the daemon's log through this tick, so what it says is held here, on the real
// path, and not only by the adapter's contract.
func TestAFailureReportsNoAddressAndNoKey(t *testing.T) {
	d := somethingWaits(t)
	const key = "a-signing-key-value"
	deps := watching(t, nil)
	deps.Lookup = func(name string) (string, bool) { return key, name == "ROPESHIFT_WEBHOOK_KEY" }
	url := "https://hooks.example.com/path-only-this-daemon-knows"
	body := "[notify_policy]\nversion = 2\nwebhook_url = \"" + url + "\"\nwebhook_secret_env = \"ROPESHIFT_WEBHOOK_KEY\"\n"
	if _, err := d.Client.PublishPolicy(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	err := noticeTick(d.Config, registryOf(d.Config.Store, d.Config.Scope), deps.notifyDeps, time.Minute).Run(t.Context())
	if err == nil {
		t.Fatal("no failure")
	}
	lines, readErr := d.Client.Notices(t.Context())
	if readErr != nil {
		t.Fatal(readErr)
	}
	raw, marshalErr := json.Marshal(api.NoticeList{Notices: lines})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, text := range []string{err.Error(), string(raw)} {
		for _, secret := range []string{url, "hooks.example.com", key} {
			if strings.Contains(text, secret) {
				t.Errorf("%q holds %q", text, secret)
			}
		}
	}
}

func TestWaitingIsWhatNeedsYouHolds(t *testing.T) {
	d, _ := hostileWaiting(t)
	notices, err := server.Waiting(d.Config)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var needs api.NeedsYou
	if err := d.Client.ReadView(t.Context(), api.ViewNeedsYou, "", &needs); err != nil {
		t.Fatal(err)
	}
	if len(notices) != len(needs.Items) || len(notices) == 0 {
		t.Errorf("%d notices for %d items", len(notices), len(needs.Items))
	}
}
