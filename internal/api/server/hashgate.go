package server

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"ropeshift.com/ropeshift/internal/api"
)

// DefaultHashDepth is how many passphrase checks may be in the gate at once, the one running and those waiting. A
// check is a password hash, which costs on purpose, so the line is short: a member signing in finds room, and a
// crowd of addresses that each guess within their own limit finds the line full and is turned away.
const DefaultHashDepth = 16

// ErrHashBusy means the gate has no room for another check.
var ErrHashBusy = errors.New("the gate is full")

// HashGate lets one passphrase check run at a time, and lets no more than its depth wait for their turn, so the
// time the server spends on checks is bounded however many addresses are guessing, and a client that finds the
// line full is refused at once and holds nothing.
type HashGate struct {
	// places is the line, the running check among them, and running is the turn.
	places  chan struct{}
	running chan struct{}
}

// NewHashGate returns a gate of the depth, or of DefaultHashDepth when the depth is below one.
func NewHashGate(depth int) *HashGate {
	if depth < 1 {
		depth = DefaultHashDepth
	}
	return &HashGate{places: make(chan struct{}, depth), running: make(chan struct{}, 1)}
}

// Enter takes a place in the line and waits for its turn, and returns what leaves the gate, which does so once
// however often it is called. It refuses with ErrHashBusy when the line is full, and with the context's error when
// the caller gives up while it waits, which frees its place. A nil gate lets every check through.
func (g *HashGate) Enter(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case g.places <- struct{}{}:
	default:
		return nil, ErrHashBusy
	}
	select {
	case g.running <- struct{}{}:
	case <-ctx.Done():
		<-g.places
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-g.running
			<-g.places
		})
	}, nil
}

// Held is how many places of the line are taken.
func (g *HashGate) Held() int {
	if g == nil {
		return 0
	}
	return len(g.places)
}

// enterGate gives a request that checks a passphrase its turn. A request that finds the line full is answered with
// 503 and a Retry-After, and one whose client has gone is answered with nothing, because nobody is there to read it.
func enterGate(gate *HashGate, w http.ResponseWriter, r *http.Request) (leave func(), ok bool) {
	leave, err := gate.Enter(r.Context())
	if err == nil {
		return leave, true
	}
	if errors.Is(err, ErrHashBusy) {
		w.Header().Set("Retry-After", "1")
		refuse(w, http.StatusServiceUnavailable, api.ErrorDetail{Code: api.CodeUnavailable, Message: "the server is checking as many passphrases as it can: try again shortly"})
	}
	return nil, false
}
