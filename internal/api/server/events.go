package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const (
	// streamEvery is how often the stream reads for new events. Each read is short, and a reader
	// asked for live updates, so this bounds a latency and costs a little.
	streamEvery = 100 * time.Millisecond
	// streamPage bounds one read, so a reader far behind makes no long transaction.
	streamPage = 100
	// streamComment keeps a quiet stream's connection honest, so a reader that left is found.
	streamComment = 15 * time.Second
)

func (h *handler) eventRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+api.EventsPath, h.streamEvents)
}

// resumePoint reads where a reader stopped. A browser's stream names that in Last-Event-ID on its own
// when it reconnects, and has no way to on its first connection, which `after` is for.
func resumePoint(r *http.Request) (int64, bool) {
	text := r.Header.Get("Last-Event-ID")
	if text == "" {
		text = r.URL.Query().Get("after")
	}
	if text == "" {
		return 0, true
	}
	after, err := strconv.ParseInt(text, 10, 64)
	return after, err == nil && after >= 0
}

// streamEvents follows the ledger. Every event has its revision, and revisions have no gaps, so the
// one cursor a reader holds is enough: it reads from where it stopped and misses nothing, however long
// it was away.
func (h *handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	after, ok := resumePoint(r)
	if !ok {
		invalid(w, "a stream resumes from a revision")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		refuse(w, http.StatusInternalServerError, api.ErrorDetail{Code: api.CodeUnavailable, Message: "streaming is not available"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	// A browser may hold headers back until the body starts, so a quiet ledger still gets a first line.
	if _, err := fmt.Fprint(w, ": open\n\n"); err != nil {
		return
	}
	flusher.Flush()
	reading := time.NewTicker(streamEvery)
	defer reading.Stop()
	quiet := time.Now()
	for {
		var page []ledger.Event
		err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
			var err error
			page, err = tx.Events(after, streamPage)
			return err
		})
		if err != nil {
			return
		}
		for _, event := range page {
			raw, err := json.Marshal(api.StreamEventOf(event))
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: ledger\ndata: %s\n\n", event.Rev, raw); err != nil {
				return
			}
			after = event.Rev
		}
		if len(page) != 0 {
			flusher.Flush()
			quiet = time.Now()
		} else if time.Since(quiet) >= streamComment {
			if _, err := fmt.Fprint(w, ": still here\n\n"); err != nil {
				return
			}
			flusher.Flush()
			quiet = time.Now()
		}
		if len(page) == streamPage {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-reading.C:
		}
	}
}
