package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

var attemptsStart = time.Date(2030, time.March, 1, 12, 0, 0, 0, time.UTC)

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func limiter(clock *fake.Clock, max, capacity int) *server.Attempts {
	return server.NewAttempts(server.AttemptLimits{MaxFailures: max, Lockout: 15 * time.Minute, Capacity: capacity, Now: clock.Now})
}

// fails makes n wrong guesses from a client, each let through and found wrong.
func fails(t *testing.T, a *server.Attempts, client netip.Addr, n int) {
	t.Helper()
	for i := range n {
		attempt, wait := a.Begin(client)
		if attempt == nil {
			t.Fatalf("guess %d from %s was refused for %s", i+1, client, wait)
		}
		attempt.Fail()
	}
}

// After the failures the limit names, from one address, that address is refused for the lockout, and a refusal
// is not extended by the guesses it turns away.
func TestAnAddressIsRefusedForTheLockoutAfterItsLastAllowedFailure(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(attemptsStart)
	a := limiter(clock, 10, 100)
	client := addr("203.0.113.7")
	fails(t, a, client, 9)
	if attempt, _ := a.Begin(client); attempt == nil {
		t.Fatal("the tenth guess was refused")
	} else {
		attempt.Fail()
	}
	attempt, wait := a.Begin(client)
	if attempt != nil || wait != 15*time.Minute {
		t.Fatalf("after ten failures the address is refused for %s, with attempt %v", wait, attempt)
	}
	clock.Advance(14*time.Minute + 59*time.Second)
	attempt, wait = a.Begin(client)
	if attempt != nil || wait != time.Second {
		t.Fatalf("a second before the lockout ends the address is refused for %s, with attempt %v", wait, attempt)
	}
	clock.Advance(time.Second)
	attempt, _ = a.Begin(client)
	if attempt == nil {
		t.Fatal("the address is refused after its lockout ended")
	}
	attempt.Fail()
	// The count began again: nine more failures are allowed before the next refusal.
	fails(t, a, client, 8)
	if refused, _ := a.Begin(client); refused == nil {
		t.Error("the count did not begin again once the lockout ended")
	} else {
		refused.Release()
	}
}

// The window slides: failures count while they are within the lockout of now, and an old one counts no more.
func TestFailuresOlderThanTheWindowCountForNothing(t *testing.T) {
	t.Parallel()
	client := addr("203.0.113.7")
	// Failures at 0 and 10 minutes, and a third at 14: all three are within 15 minutes, and the address is refused.
	clock := fake.NewClock(attemptsStart)
	a := limiter(clock, 3, 100)
	fails(t, a, client, 1)
	clock.Advance(10 * time.Minute)
	fails(t, a, client, 1)
	clock.Advance(4 * time.Minute)
	fails(t, a, client, 1)
	if attempt, wait := a.Begin(client); attempt != nil || wait <= 0 {
		t.Errorf("three failures within 15 minutes were let through: %v, %s", attempt, wait)
	}
	// The third at 16 minutes finds the first 16 minutes old, so two failures stand and the address is not refused.
	clock = fake.NewClock(attemptsStart)
	a = limiter(clock, 3, 100)
	fails(t, a, client, 1)
	clock.Advance(10 * time.Minute)
	fails(t, a, client, 1)
	clock.Advance(6 * time.Minute)
	fails(t, a, client, 1)
	attempt, wait := a.Begin(client)
	if attempt == nil {
		t.Fatalf("a failure older than the window was counted: refused for %s", wait)
	}
	attempt.Release()
}

func TestAnotherAddressIsUnaffectedByAnAddressThatIsRefused(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(attemptsStart)
	a := limiter(clock, 3, 100)
	fails(t, a, addr("203.0.113.7"), 3)
	if attempt, _ := a.Begin(addr("203.0.113.7")); attempt != nil {
		t.Fatal("the address was not refused")
	}
	for _, other := range []string{"203.0.113.8", "198.51.100.7", "2001:db8::7", "203.0.114.7"} {
		attempt, wait := a.Begin(addr(other))
		if attempt == nil {
			t.Errorf("%s was refused for %s because of another address", other, wait)
			continue
		}
		attempt.Release()
	}
	// An IPv4 address in its IPv6 form is the same address, and a zone is no address of the client's.
	for _, same := range []string{"::ffff:203.0.113.7"} {
		if attempt, _ := a.Begin(addr(same)); attempt != nil {
			t.Errorf("%s escaped the refusal of %s", same, "203.0.113.7")
		}
	}
}

// A guess that was right clears its address's count, and a guess that proves nothing leaves it as it was.
func TestARightGuessClearsTheCountAndAGuessThatProvedNothingLeavesIt(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(attemptsStart)
	a := limiter(clock, 3, 100)
	client := addr("203.0.113.7")
	fails(t, a, client, 2)
	attempt, _ := a.Begin(client)
	attempt.Release()
	attempt, _ = a.Begin(client)
	attempt.Fail()
	if refused, _ := a.Begin(client); refused != nil {
		t.Fatal("two failures and a third, with a guess that proved nothing between, were let through")
	}

	clock.Advance(15 * time.Minute)
	fails(t, a, client, 2)
	attempt, _ = a.Begin(client)
	attempt.Pass()
	fails(t, a, client, 2)
	if next, _ := a.Begin(client); next == nil {
		t.Error("a right guess did not clear the failures before it")
	} else {
		next.Release()
	}
}

// A guess settles once: the first word on it stands, so a handler that fails after it judged cannot count it twice.
func TestAGuessSettlesOnce(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(attemptsStart)
	a := limiter(clock, 3, 100)
	client := addr("203.0.113.7")
	attempt, _ := a.Begin(client)
	attempt.Fail()
	attempt.Fail()
	attempt.Release()
	attempt.Pass()
	fails(t, a, client, 1)
	if next, _ := a.Begin(client); next == nil {
		t.Error("one failure settled three times")
	} else {
		next.Release()
	}
	var none *server.Attempt
	none.Fail()
	none.Pass()
	none.Release()
	none.Judge(nil, true)
}

// The guesses in flight count as if they were wrong, so a client that sends a hundred at once is let through no
// more than the limit allows, and a guess that finishes makes room for the next.
func TestGuessesInFlightCountAgainstTheLimit(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(attemptsStart)
	a := limiter(clock, 3, 100)
	client := addr("203.0.113.7")
	var admitted []*server.Attempt
	for range 3 {
		attempt, _ := a.Begin(client)
		if attempt == nil {
			t.Fatal("a guess within the limit was refused")
		}
		admitted = append(admitted, attempt)
	}
	if attempt, wait := a.Begin(client); attempt != nil || wait != time.Second {
		t.Fatalf("a fourth guess in flight is refused for %s, with attempt %v", wait, attempt)
	}
	admitted[0].Release()
	next, _ := a.Begin(client)
	if next == nil {
		t.Fatal("a guess that finished made no room")
	}
	next.Release()
	for _, attempt := range admitted[1:] {
		attempt.Fail()
	}
	third, _ := a.Begin(client)
	if third == nil {
		t.Fatal("two failures were not followed by a third guess")
	}
	third.Fail()
	if attempt, wait := a.Begin(client); attempt != nil || wait != 15*time.Minute {
		t.Errorf("three failures were let through a limit of three: %v, %s", attempt, wait)
	}
}

func TestJudgeCountsAWrongPassphraseAndAWrongJoinTokenAndNothingElse(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(attemptsStart)
	for name, test := range map[string]struct {
		err    error
		clears bool
		counts bool
	}{
		"a wrong passphrase":    {authority.ErrBadProof, true, true},
		"a wrong join token":    {ledger.ErrBadJoinToken, false, true},
		"a wrapped wrong proof": {fmt.Errorf("sign in: %w", authority.ErrBadProof), true, true},
		"no passphrase set":     {authority.ErrNotEnrolled, true, false},
		"a store that is down":  {fmt.Errorf("the store: %w", http.ErrServerClosed), true, false},
		"an expired session":    {authority.ErrNoSession, true, false},
		"a right guess":         {nil, true, false},
		"a right join token":    {nil, false, false},
	} {
		a := limiter(clock, 1, 100)
		client := addr("203.0.113.9")
		attempt, _ := a.Begin(client)
		attempt.Judge(test.err, test.clears)
		next, _ := a.Begin(client)
		if refused := next == nil; refused != test.counts {
			t.Errorf("%s: refused is %v", name, refused)
		}
	}
}

// The table of addresses is bounded: a flood of addresses that each fail once never holds more than the
// capacity, the oldest idle ones go first, and an address that is refused outlasts them.
func TestTheTableOfAddressesStaysBoundedAndKeepsWhoIsRefused(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(attemptsStart)
	a := limiter(clock, 3, 8)
	refused := addr("203.0.113.1")
	fails(t, a, refused, 3)
	for i := range 200 {
		fails(t, a, netip.AddrFrom4([4]byte{198, 51, byte(i / 250), byte(i%250 + 1)}), 1)
		clock.Advance(time.Second)
		if got := a.Tracked(); got > 8 {
			t.Fatalf("after %d addresses the table holds %d", i+1, got)
		}
	}
	if attempt, _ := a.Begin(refused); attempt != nil {
		t.Error("a flood of addresses lifted the refusal of one")
	}
	// The newest addresses are still counted, and the oldest were let go.
	newest := netip.AddrFrom4([4]byte{198, 51, 0, 200})
	fails(t, a, newest, 2)
	if attempt, _ := a.Begin(newest); attempt != nil {
		t.Error("the newest address lost its failures")
	}
	oldest := netip.AddrFrom4([4]byte{198, 51, 0, 1})
	fails(t, a, oldest, 2)
	if attempt, _ := a.Begin(oldest); attempt == nil {
		t.Error("the oldest address kept the failure it made 200 addresses ago")
	} else {
		attempt.Release()
	}
}

// Addresses that have been quiet for the window are let go before one that is current is, and a table of them
// empties.
func TestAddressesThatWentQuietAreForgotten(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(attemptsStart)
	a := limiter(clock, 3, 1000)
	for i := range 50 {
		fails(t, a, netip.AddrFrom4([4]byte{198, 51, 100, byte(i + 1)}), 1)
	}
	if a.Tracked() != 50 {
		t.Fatalf("the table holds %d", a.Tracked())
	}
	clock.Advance(16 * time.Minute)
	attempt, _ := a.Begin(addr("203.0.113.9"))
	attempt.Release()
	if got := a.Tracked(); got > 1 {
		t.Errorf("the table holds %d addresses that have been quiet for 16 minutes", got)
	}
}

// With every slot held by a guess in flight there is room for no more, and the guess that finds none goes
// through uncounted, which a client with more addresses than the capacity does not need the limit to allow.
func TestATableFullOfGuessesInFlightLetsTheNextThrough(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(attemptsStart)
	a := limiter(clock, 3, 2)
	var held []*server.Attempt
	for _, client := range []string{"203.0.113.1", "203.0.113.2"} {
		attempt, _ := a.Begin(addr(client))
		held = append(held, attempt)
	}
	attempt, _ := a.Begin(addr("203.0.113.3"))
	if attempt == nil {
		t.Fatal("a guess was refused for want of room")
	}
	attempt.Fail()
	if got := a.Tracked(); got > 2 {
		t.Errorf("the table holds %d", got)
	}
	for _, attempt := range held {
		attempt.Release()
	}
}

func TestManyGuessesAtOnceCountEachOnce(t *testing.T) {
	t.Parallel()
	a := server.NewAttempts(server.AttemptLimits{MaxFailures: 5, Lockout: time.Hour, Capacity: 100})
	client := addr("203.0.113.7")
	var wrong, refused int
	var mu sync.Mutex
	var wait sync.WaitGroup
	for range 200 {
		wait.Go(func() {
			attempt, _ := a.Begin(client)
			if attempt == nil {
				mu.Lock()
				refused++
				mu.Unlock()
				return
			}
			time.Sleep(time.Millisecond)
			attempt.Fail()
			mu.Lock()
			wrong++
			mu.Unlock()
		})
	}
	wait.Wait()
	if wrong != 5 || refused != 195 {
		t.Errorf("%d guesses were let through and %d refused of 200, against a limit of 5", wrong, refused)
	}
}

// The address a request comes from is the one its TCP connection came from, with an IPv4 address in its IPv6 form
// read as IPv4 and a zone dropped, and no header says otherwise.
func TestThePeerIsTheAddressTheConnectionCameFrom(t *testing.T) {
	t.Parallel()
	for remote, want := range map[string]string{
		"203.0.113.9:5555":        "203.0.113.9",
		"[2001:db8::9]:5555":      "2001:db8::9",
		"[::ffff:203.0.113.9]:80": "203.0.113.9",
		"[fe80::1%eth0]:80":       "fe80::1",
		"127.0.0.1:7400":          "127.0.0.1",
		"not an address":          "invalid IP",
		"":                        "invalid IP",
	} {
		request := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
		request.RemoteAddr = remote
		request.Header.Set("X-Forwarded-For", "198.51.100.200")
		request.Header.Set("Forwarded", "for=198.51.100.201")
		request.Header.Set("X-Real-IP", "198.51.100.202")
		if got := server.PeerAddress(request).String(); got != want {
			t.Errorf("%q is read as %q, and wants %q", remote, got, want)
		}
	}
}
