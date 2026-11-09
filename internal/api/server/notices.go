package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/BurntSushi/toml"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// notifyTable names the table of a notify policy.
const notifyTable = "notify_policy"

func (h *handler) noticeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/notices", h.listNotices)
}

func (h *handler) listNotices(w http.ResponseWriter, r *http.Request) {
	lines, err := h.notices.List(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	if lines == nil {
		lines = []ledger.NoticeLine{}
	}
	reply(w, http.StatusOK, api.NoticeList{Notices: lines})
}

// readNotify reads a notify policy as a member wrote it. A setting this build does not know stops it,
// because a channel nobody reads would look turned on.
func readNotify(body string) (authority.NotifyPolicyDraft, error) {
	var file authority.NotifyPolicyFile
	meta, err := toml.Decode(body, &file)
	if err != nil {
		return authority.NotifyPolicyDraft{}, fmt.Errorf("%w: %v", authority.ErrBadNotifyPolicy, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return authority.NotifyPolicyDraft{}, fmt.Errorf("%w: unknown setting %s", authority.ErrBadNotifyPolicy, unknown[0])
	}
	if !meta.IsDefined(notifyTable, "version") {
		return authority.NotifyPolicyDraft{}, fmt.Errorf("%w: a notify policy states its version", authority.ErrBadNotifyPolicy)
	}
	return authority.NotifyPolicyDraft{Body: body, Policy: file.NotifyPolicy}, nil
}

// Waiting reads what waits for a person, as notices, so the tick and the Needs you view cannot differ
// about it. Only the kind of each thing and why it waits go in, which is what keeps a title, a
// question, or anything else an agent wrote from leaving the machine.
func Waiting(config Config) func(context.Context) ([]ledger.Notice, error) {
	h := newHandler(config)
	return func(ctx context.Context) ([]ledger.Notice, error) {
		var notices []ledger.Notice
		err := h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
			now, err := tx.Now()
			if err != nil {
				return err
			}
			items, err := h.needs(tx, api.Stamp{AsOf: now})
			if err != nil {
				return err
			}
			for _, item := range items {
				reason := ""
				if item.Work != nil {
					reason = item.Work.Reason
				}
				notices = append(notices, ledger.NewNotice(h.Scope, item.Kind, reason, item.ID, item.Since))
			}
			return nil
		})
		return notices, err
	}
}
