//go:build chaos

package chaos

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	"ropeshift.com/ropeshift/internal/adapters/notify"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/contract"
)

// The broken variants of a notice, each of which the far end's own count must catch.
const (
	sabotageForgetsAttempts = "a repeat leaves no trace"
	sabotageRecordsFirst    = "a delivery recorded before it goes"
)

// noticeHook is the far end of a webhook, in the parent, so nothing a killed child wrote can vouch for
// what reached it.
type noticeHook struct {
	mu    sync.Mutex
	heard []ledger.Notice
}

func (h *noticeHook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var notice ledger.Notice
	if err := json.Unmarshal(body, &notice); err == nil {
		h.mu.Lock()
		h.heard = append(h.heard, notice)
		h.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *noticeHook) notices() []ledger.Notice {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ledger.Notice{}, h.heard...)
}

func roleNotify(c *child) error {
	switch c.spec.Variant {
	case sabotageForgetsAttempts:
		control.Sabotaged(control.Sabotage{ForgetsAttempts: true})
	case sabotageRecordsFirst:
		control.Sabotaged(control.Sabotage{RecordsBeforeSending: true})
	}
	channel := notify.NewWebhook(notify.WebhookConfig{URL: c.spec.Path})
	notices := control.NewNotices(c.store, func(authority.NotifyPolicy) []ports.Notifier { return []ports.Notifier{channel} }).RetryingAfter(0)
	item := ledger.NewNotice(c.scope, ledger.NoticeWork, "blocked", c.spec.WorkOrder, 1)
	return notices.Deliver(c.ctx, c.scope, []ledger.Notice{item}, c.actor)
}

// noticeCase kills a tick around one delivery. A notice must reach the far end at least once, however a
// kill falls, and every copy that reached it must show in the ledger.
func noticeCase(op string, steps ...string) crashCase {
	var hook *noticeHook
	return crashCase{
		name: "a notice at " + op, op: op, steps: steps,
		prepare: func(w *world) (childSpec, error) {
			hook = &noticeHook{}
			far := httptest.NewServer(hook)
			w.t.Cleanup(far.Close)
			return childSpec{Role: "notify", WorkOrder: w.rig.ID("wo"), Path: far.URL + "/hook"}, nil
		},
		settled: func(w *world, _ childSpec) error { return noticeSettled(w, hook) },
	}
}

func noticeSettled(w *world, hook *noticeHook) error {
	heard := hook.notices()
	if len(heard) == 0 {
		return contract.Violatedf("a notice the ledger calls delivered never reached anyone")
	}
	lines, err := control.NewNotices(w.store, nil).List(w.rig.Ctx, w.rig.Scope)
	if err != nil {
		return err
	}
	if len(lines) != 1 || !lines[0].Delivered {
		return contract.Violatedf("the ledger reads %+v", lines)
	}
	if int64(len(heard)) > lines[0].Attempts {
		return contract.Violatedf("the far end heard %d copies, and the ledger shows %d attempts", len(heard), lines[0].Attempts)
	}
	for i, notice := range heard {
		if notice.Delivery != heard[0].Delivery {
			return contract.Violatedf("a repeat changed its delivery: %+v", heard)
		}
		if notice.Attempt <= int64(i) {
			return contract.Violatedf("a repeat did not say so: %+v", heard)
		}
	}
	return nil
}

func noticeCases() []crashCase {
	return []crashCase{
		noticeCase("notify", "attempted", "sent", "recorded"),
		noticeCase("notice.attempt", transactionSteps...),
		noticeCase("notice.record", transactionSteps...),
	}
}

func noticeVariants() []variant {
	return []variant{
		{
			name: sabotageForgetsAttempts, check: "crash",
			crash: func() (crashCase, string) { return noticeCase("notify", "sent"), "sent" },
		},
		{
			name: sabotageRecordsFirst, check: "crash",
			crash: func() (crashCase, string) { return noticeCase("notify", "recorded-first"), "recorded-first" },
		},
	}
}
