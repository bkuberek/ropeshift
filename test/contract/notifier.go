package contract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// NotifierRig is one channel under test. Heard reads what reached the far end, as the far end saw it,
// and Refuse makes the far end say no.
type NotifierRig struct {
	Notifier ports.Notifier
	Heard    func() []ledger.Notice
	Refuse   func()
	// Secrets are what must stay out of every error: an address, a key.
	Secrets []string
}

// NotifierNotice is a notice a channel carries in the contract.
func NotifierNotice() ledger.Notice {
	return ledger.NewNotice("workspace-a", ledger.NoticeWork, "lapsed", "wo-1", 1_790_000_000_000)
}

// NotifierContract holds a channel to what the tick relies on.
func NotifierContract(t *testing.T, build func(*testing.T) NotifierRig) {
	t.Helper()
	t.Run("a channel has a name", func(t *testing.T) {
		if build(t).Notifier.Channel() == "" {
			t.Error("a delivery could not say where it went")
		}
	})
	t.Run("a notice reaches the far end whole", func(t *testing.T) {
		rig := build(t)
		notice := NotifierNotice()
		notice.Attempt = 1
		if err := rig.Notifier.Notify(t.Context(), notice); err != nil {
			t.Fatal(err)
		}
		heard := rig.Heard()
		if len(heard) != 1 || heard[0].Text != notice.Text {
			t.Fatalf("heard %+v", heard)
		}
	})
	t.Run("a repeat reaches the far end again, because delivery is at least once", func(t *testing.T) {
		rig := build(t)
		notice := NotifierNotice()
		for attempt := int64(1); attempt <= 2; attempt++ {
			notice.Attempt = attempt
			if err := rig.Notifier.Notify(t.Context(), notice); err != nil {
				t.Fatal(err)
			}
		}
		if heard := rig.Heard(); len(heard) != 2 {
			t.Fatalf("heard %+v", heard)
		}
	})
	t.Run("a context that ended sends nothing", func(t *testing.T) {
		rig := build(t)
		ended, stop := context.WithCancel(t.Context())
		stop()
		if err := rig.Notifier.Notify(ended, NotifierNotice()); err == nil {
			t.Error("a notice went out on a context that had ended")
		}
		if heard := rig.Heard(); len(heard) != 0 {
			t.Errorf("heard %+v", heard)
		}
	})
	t.Run("a refusal is an error that keeps its secrets", func(t *testing.T) {
		rig := build(t)
		if rig.Refuse == nil {
			t.Skip("this channel has no far end to refuse")
		}
		rig.Refuse()
		err := rig.Notifier.Notify(t.Context(), NotifierNotice())
		if err == nil {
			t.Fatal("a refusal read as a delivery")
		}
		for _, secret := range rig.Secrets {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%q holds %q", err, secret)
			}
		}
	})
}

// NoticeFields lists the JSON fields of a notice, as a receiver reads them.
func NoticeFields(t *testing.T) []string {
	t.Helper()
	notice := NotifierNotice()
	notice.Attempt = 1
	raw, err := json.Marshal(notice)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	return names
}
