package entry

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/notify"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// With `[notify_policy]` choosing the desktop and a webhook for `spend_hard_limit` and nothing for `spend_soft_limit`, a
// hard stop reaches the web UI, the desktop, and the webhook, and a soft notice reaches only the web UI (FR-SPEND-19). It
// runs on the real tick and the real channels, with a desktop program and a webhook server of the test's own, so a channel
// beyond the choice would be heard here.
func TestASpendAlertReachesOnlyTheChannelsItsKindChose(t *testing.T) {
	d := apitest.Start(t)
	var mu sync.Mutex
	var hooked []map[string]any
	far := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var fields map[string]any
		mu.Lock()
		defer mu.Unlock()
		if json.Unmarshal(raw, &fields) == nil {
			hooked = append(hooked, fields)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer far.Close()
	body := "[notify_policy]\nversion = 2\ndesktop = true\nwebhook_url = \"" + far.URL + "/hook\"\n\n" +
		"[notify_policy.spend_channels]\nspend_hard_limit = [\"desktop\", \"webhook\"]\nspend_soft_limit = []\n"
	if _, err := d.Client.PublishPolicy(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	deps := watching(t, (&net.Dialer{}).DialContext)
	runner := deps.Processes.(*fake.ProcessRunner)
	runner.Install("/usr/bin/notify-send", func(ports.ProcessSpec, <-chan struct{}) int { return 0 })
	spend := control.NewSpend(d.Config.Store)
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "spend"}
	for _, kind := range []string{ledger.NoticeSpendSoftLimit, ledger.NoticeSpendHardLimit} {
		err := spend.Note(t.Context(), d.Config.Scope, ledger.SpendNotice{Kind: kind, Scope: ledger.NoticeScopeProjectDay, LimitUSD: 10, ChargedUSD: 11}, system)
		if err != nil {
			t.Fatal(err)
		}
	}
	tick := noticeTick(d.Config, registryOf(d.Config.Store, d.Config.Scope), deps.notifyDeps, time.Minute)
	for range 2 {
		if err := tick.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	spawned := runner.Spawned()
	if len(spawned) != 1 {
		t.Fatalf("the desktop heard %d notices, want the hard one alone: %+v", len(spawned), spawned)
	}
	want := ledger.DefaultProjectName + ": The project reached its daily spend limit, and no work starts until the day ends or a member raises it."
	if last := spawned[0].Args[len(spawned[0].Args)-1]; last != want {
		t.Errorf("the desktop heard %q, want %q", last, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hooked) != 1 || hooked[0]["kind"] != ledger.NoticeSpendHardLimit || hooked[0]["reason"] != ledger.NoticeScopeProjectDay {
		t.Fatalf("the webhook heard %+v, want the hard notice once", hooked)
	}
	// A webhook's payload holds the fields the document states, and nothing of what was charged or which limit.
	for name := range hooked[0] {
		if !slices.Contains(documentedFields(t), name) {
			t.Errorf("%s left the machine, and the document does not say so", name)
		}
	}
	lines, err := d.Client.Notices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	heard := map[string][]string{}
	for _, line := range lines {
		if !line.Delivered {
			t.Errorf("a notice stands undelivered: %+v", line)
		}
		heard[line.Channel] = append(heard[line.Channel], line.Kind)
	}
	if !slices.Equal(heard[notify.ChannelWeb], []string{ledger.NoticeSpendSoftLimit, ledger.NoticeSpendHardLimit}) ||
		!slices.Equal(heard[notify.ChannelDesktop], []string{ledger.NoticeSpendHardLimit}) ||
		!slices.Equal(heard[notify.ChannelWebhook], []string{ledger.NoticeSpendHardLimit}) {
		t.Errorf("the notices stand %+v", heard)
	}
}

// A webhook that refuses a spend alert leaves it in the web UI, delivered there, with the webhook's failure on its own
// line, so a member who looks is told.
func TestAFailedWebhookLeavesASpendAlertInTheWebUI(t *testing.T) {
	d := apitest.Start(t)
	far := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer far.Close()
	body := "[notify_policy]\nversion = 2\nwebhook_url = \"" + far.URL + "/hook\"\n\n[notify_policy.spend_channels]\nspend_hard_limit = [\"webhook\"]\n"
	if _, err := d.Client.PublishPolicy(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	err := control.NewSpend(d.Config.Store).Note(t.Context(), d.Config.Scope,
		ledger.SpendNotice{Kind: ledger.NoticeSpendHardLimit, Scope: ledger.NoticeScopeProjectWeek}, ledger.Actor{Kind: ledger.ActorSystem, ID: "spend"})
	if err != nil {
		t.Fatal(err)
	}
	deps := watching(t, (&net.Dialer{}).DialContext)
	if err := noticeTick(d.Config, registryOf(d.Config.Store, d.Config.Scope), deps.notifyDeps, time.Minute).Run(t.Context()); err == nil {
		t.Fatal("a webhook that answered 500 reported nothing")
	}
	lines, err := d.Client.Notices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byChannel := map[string]ledger.NoticeLine{}
	for _, line := range lines {
		byChannel[line.Channel] = line
	}
	if len(lines) != 2 || !byChannel[notify.ChannelWeb].Delivered || byChannel[notify.ChannelWeb].Kind != ledger.NoticeSpendHardLimit ||
		byChannel[notify.ChannelWebhook].Delivered || byChannel[notify.ChannelWebhook].Failure == "" {
		t.Errorf("the notice stands %+v, want it in the web UI and refused by the webhook with its reason", lines)
	}
}
