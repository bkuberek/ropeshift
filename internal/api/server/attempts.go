package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

// The network listener limits the guesses a client address makes at a secret (ADR-0031). A request to sign in,
// to change the passphrase with the current one, or to exchange a join token is a guess, and what a guess that
// fails does is counted against the address the request came from. After MaxFailures within Lockout, the
// address is refused for Lockout, whatever it guesses next.

const (
	// DefaultMaxFailures and DefaultLockout are what a limit with neither set allows: 10 failures within 15
	// minutes, and a refusal of 15 minutes.
	DefaultMaxFailures = 10
	DefaultLockout     = 15 * time.Minute
	// DefaultCapacity is how many addresses the limit tracks at once.
	DefaultCapacity = 10_000
	// inFlightWait is what a client is told to wait when it is refused only because the guesses it has in flight
	// could, if all were wrong, reach the limit.
	inFlightWait = time.Second
)

// AttemptLimits say how many wrong guesses a client address may make, how long they count and how long a
// refusal lasts, how many addresses are tracked at once, and what time it is. A zero value takes the default.
type AttemptLimits struct {
	MaxFailures int
	Lockout     time.Duration
	Capacity    int
	Now         func() time.Time
}

// Attempts limits the wrong guesses each client address makes at a passphrase or a join token. Its counts live
// in memory, so a restart clears them. A guess counts as wrong from the moment it is let through until it is
// judged, so guesses sent at once are let through no more than the limit allows. It keeps a table of addresses
// with a failure or a guess in flight, and the table never holds more than its capacity.
type Attempts struct {
	max      int
	lockout  time.Duration
	capacity int
	now      func() time.Time

	mu        sync.Mutex
	clients   map[netip.Addr]*tally
	lastSweep time.Time
}

// tally is what one address has done: its failures within the window, its guesses in flight, and when its
// refusal ends.
type tally struct {
	failures []time.Time
	pending  int
	until    time.Time
	touched  time.Time
}

// NewAttempts returns a limit with the numbers it is given.
func NewAttempts(limits AttemptLimits) *Attempts {
	a := &Attempts{max: limits.MaxFailures, lockout: limits.Lockout, capacity: limits.Capacity, now: limits.Now, clients: map[netip.Addr]*tally{}}
	if a.max <= 0 {
		a.max = DefaultMaxFailures
	}
	if a.lockout <= 0 {
		a.lockout = DefaultLockout
	}
	if a.capacity <= 0 {
		a.capacity = DefaultCapacity
	}
	if a.now == nil {
		a.now = time.Now
	}
	return a
}

// Tracked is how many addresses the table holds.
func (a *Attempts) Tracked() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.clients)
}

// clean is an address as it is: an IPv4 address in its IPv6 form is the IPv4 address, and a zone names a link on
// the server's machine and no client.
func clean(client netip.Addr) netip.Addr { return client.WithZone("").Unmap() }

// grouped is the address a client is counted by. A host holds a whole /64 of IPv6 addresses and takes a new one
// for every request it likes, so IPv6 addresses count by their /64, and an IPv4 address counts as itself.
func grouped(client netip.Addr) netip.Addr {
	client = clean(client)
	if client.Is6() {
		return netip.PrefixFrom(client, 64).Masked().Addr()
	}
	return client
}

// Begin lets a client's next guess through, or refuses it and says how long to wait. A guess is refused while
// the address is locked out, and while the address's failures and the guesses it already has in flight could,
// if all were wrong, reach the limit. The guess it lets through is the caller's to judge.
func (a *Attempts) Begin(client netip.Addr) (*Attempt, time.Duration) {
	client = grouped(client)
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if now.Sub(a.lastSweep) >= a.lockout {
		a.sweep(now)
	}
	attempt := &Attempt{limits: a, client: client}
	record := a.clients[client]
	if record != nil {
		record.forget(now, a.lockout)
		if record.until.After(now) {
			return nil, record.until.Sub(now)
		}
		if len(record.failures)+record.pending >= a.max {
			return nil, inFlightWait
		}
	} else if record = a.room(client, now); record == nil {
		// Every slot holds a guess in flight, so this one goes through uncounted. A client with more addresses
		// than the table has slots was never held by a limit that counts by address.
		return attempt, 0
	}
	record.pending++
	record.touched = now
	attempt.tracked = true
	return attempt, 0
}

// forget drops the failures that are older than the window, and a refusal that ended.
func (t *tally) forget(now time.Time, window time.Duration) {
	kept := t.failures[:0]
	for _, at := range t.failures {
		if now.Sub(at) < window {
			kept = append(kept, at)
		}
	}
	t.failures = kept
	if !t.until.After(now) {
		t.until = time.Time{}
	}
}

// idle says an address has nothing to remember.
func (t *tally) idle(now time.Time, window time.Duration) bool {
	t.forget(now, window)
	return t.pending == 0 && len(t.failures) == 0 && t.until.IsZero()
}

// sweep drops every address with nothing to remember. It runs once a window, and when the table is full.
func (a *Attempts) sweep(now time.Time) {
	a.lastSweep = now
	for client, record := range a.clients {
		if record.idle(now, a.lockout) {
			delete(a.clients, client)
		}
	}
}

// room makes a slot for an address, and is nil when every slot holds a guess in flight. It lets go of
// addresses that were quiet for the window first, then of the one that has been idle longest and is not
// refused, then of the one whose refusal ends first. An address with a guess in flight is never let go.
func (a *Attempts) room(client netip.Addr, now time.Time) *tally {
	if len(a.clients) >= a.capacity {
		a.sweep(now)
	}
	if len(a.clients) >= a.capacity {
		var victim netip.Addr
		var oldest *tally
		for candidate, record := range a.clients {
			if record.pending > 0 {
				continue
			}
			if oldest == nil || letGoBefore(record, oldest, now) {
				victim, oldest = candidate, record
			}
		}
		if oldest != nil {
			delete(a.clients, victim)
		}
	}
	if len(a.clients) >= a.capacity {
		return nil
	}
	record := &tally{touched: now}
	a.clients[client] = record
	return record
}

// letGoBefore says an address is let go of before another: one that is not refused before one that is, the one
// idle longest among those that are not, and the one whose refusal ends first among those that are.
func letGoBefore(candidate, than *tally, now time.Time) bool {
	refused, other := candidate.until.After(now), than.until.After(now)
	switch {
	case refused != other:
		return !refused
	case refused:
		return candidate.until.Before(than.until)
	}
	return candidate.touched.Before(than.touched)
}

// Attempt is one guess that was let through. It is judged once, by Fail, Pass, or Release, and the first of them
// to be called stands. Every method does nothing on a nil Attempt, which is what a request that no limit applies
// to holds.
type Attempt struct {
	limits  *Attempts
	client  netip.Addr
	tracked bool
	settled atomic.Bool
}

// Fail says the guess was wrong, which counts against the address.
func (t *Attempt) Fail() { t.settle(failed) }

// Pass says the guess was right, which clears the address's failures.
func (t *Attempt) Pass() { t.settle(passed) }

// Release says the guess proved nothing, as a request that failed for another reason does, and counts for
// nothing.
func (t *Attempt) Release() { t.settle(released) }

// Judge settles a guess by what became of it, and fails closed. A request that succeeded is a right guess when
// clears says it proves the passphrase. A request that ended in an error proved nothing only when the error says
// the check never ran or the guess was right; every other error counts as a wrong guess, a wrong passphrase and a
// wrong join token first, and a store that broke or a client that left after the check with them. Otherwise a
// client that can make a request fail after its check could guess for free.
func (t *Attempt) Judge(err error, clears bool) {
	switch {
	case err == nil && clears:
		t.Pass()
	case err == nil, provesNothing(err):
		t.Release()
	default:
		t.Fail()
	}
}

// provesNothing says an error is no wrong guess: no passphrase was set to guess at, the session that a change
// needs was gone before anything was checked, or the passphrase was right and what followed was the new one's
// bounds or a change that another won.
func provesNothing(err error) bool {
	if errors.Is(err, authority.ErrBadProof) || errors.Is(err, ledger.ErrBadJoinToken) {
		return false
	}
	return errors.Is(err, authority.ErrNotEnrolled) || errors.Is(err, authority.ErrNoSession) ||
		errors.Is(err, authority.ErrInvalid) || errors.Is(err, authority.ErrPassphraseChanged)
}

type outcome int

const (
	failed outcome = iota
	passed
	released
)

func (t *Attempt) settle(what outcome) {
	if t == nil || t.limits == nil || !t.settled.CompareAndSwap(false, true) {
		return
	}
	a := t.limits
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	record := a.clients[t.client]
	if record == nil && what == failed {
		// A guess that went through for want of room still counts, if room has come.
		record = a.room(t.client, now)
	}
	if record == nil {
		return
	}
	if t.tracked && record.pending > 0 {
		record.pending--
	}
	record.forget(now, a.lockout)
	record.touched = now
	switch what {
	case failed:
		record.failures = append(record.failures, now)
		if len(record.failures) >= a.max {
			record.until, record.failures = now.Add(a.lockout), nil
		}
	case passed:
		record.failures = nil
	}
	if record.idle(now, a.lockout) {
		delete(a.clients, t.client)
	}
}

// PeerAddress is the address a request's TCP connection came from: an IPv4 address in its IPv6 form is read as
// IPv4, a zone is dropped, and no header says otherwise. An address that does not parse is the zero Address.
func PeerAddress(r *http.Request) netip.Addr {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	return clean(peer.Addr())
}

type clientKey struct{}

// withClient records which address a request came from, for the limits that count by it. A request that
// reaches a handler with none came over the control socket, which no limit applies to: its file permissions
// are its credential.
func withClient(ctx context.Context, client netip.Addr) context.Context {
	return context.WithValue(ctx, clientKey{}, client)
}

func clientOf(ctx context.Context) (netip.Addr, bool) {
	client, ok := ctx.Value(clientKey{}).(netip.Addr)
	return client, ok
}

// beginAttempt lets a request that makes a guess through, or answers it with 429 and says when to try again. It
// lets every request through, holding no Attempt, when no limit is set or the request came over the socket.
func beginAttempt(attempts *Attempts, w http.ResponseWriter, r *http.Request) (*Attempt, bool) {
	if attempts == nil {
		return nil, true
	}
	client, known := clientOf(r.Context())
	if !known {
		return nil, true
	}
	attempt, wait := attempts.Begin(client)
	if attempt != nil {
		return attempt, true
	}
	seconds := max(1, int((wait+time.Second-1)/time.Second))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	refuse(w, http.StatusTooManyRequests, api.ErrorDetail{
		Code:    api.CodeTooManyAttempts,
		Message: fmt.Sprintf("too many failed attempts from this address: try again in %d seconds", seconds),
	})
	return nil, false
}
