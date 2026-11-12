package server

import (
	"cmp"
	"net/http"
	"slices"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ports"
)

// listMembers answers each member that holds a live session, with those sessions, and how many members
// can sign in. A session's token stays in the hands of whoever signed in, so the list names none.
func (h *handler) listMembers(w http.ResponseWriter, r *http.Request) {
	out := api.MemberList{Members: []api.MemberView{}}
	err := h.Installation.Read(r.Context(), func(tx ports.SharedReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		out.AsOf = now
		if out.Enrolled, err = tx.Members().CountMembers(); err != nil {
			return err
		}
		sessions, err := tx.Members().Sessions()
		if err != nil {
			return err
		}
		byMember := map[string]*api.MemberView{}
		for _, session := range sessions {
			if !session.Live(now) {
				continue
			}
			view := byMember[session.Member]
			if view == nil {
				view = &api.MemberView{ID: session.Member}
				byMember[session.Member] = view
			}
			view.Sessions = append(view.Sessions, api.MemberSession{CreatedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt})
			view.LastSignIn = max(view.LastSignIn, session.CreatedAt)
		}
		for _, view := range byMember {
			slices.SortFunc(view.Sessions, func(a, b api.MemberSession) int { return cmp.Compare(b.CreatedAt, a.CreatedAt) })
			out.Members = append(out.Members, *view)
		}
		slices.SortFunc(out.Members, func(a, b api.MemberView) int { return cmp.Compare(a.ID, b.ID) })
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}
