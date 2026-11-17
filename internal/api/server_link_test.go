package api_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/servertls"
	"ropeshift.com/ropeshift/internal/api"
)

// A worker reaches its server over a link whose trust is the join string's: the server's certificate authority by
// its fingerprint when the string names one, and the system's roots when it does not. These tests serve TLS with
// authorities of their own and call the server through the link a worker makes.

// authority makes a certificate authority in a directory of the test's own, and the certificate it issues for the
// names. The time is the system's unless a clock is given.
func authority(t *testing.T, at *fake.Clock, names ...string) *servertls.Authority {
	t.Helper()
	options := servertls.Options{Names: names, Clock: clock.System{}}
	if at != nil {
		options.Clock = at
	}
	made, err := servertls.OpenAuthority(filepath.Join(t.TempDir(), "tls"), options)
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func roots(t *testing.T, made *servertls.Authority) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(made.CertificatePEM()) {
		t.Fatal("no certificate in the authority's PEM")
	}
	return pool
}

// serveWith serves the API's health on a loopback port in TLS, until the test ends, and returns its port.
func serveWith(t *testing.T, config *tls.Config) string {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: healthy(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = httpServer.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = httpServer.Close()
		<-done
	})
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// without strips the authority from the chain a server sends, as a server that supplies its own certificate does.
func without(config *tls.Config) *tls.Config {
	stripped := config.Clone()
	inner := stripped.GetCertificate
	stripped.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, err := inner(hello)
		if err != nil {
			return nil, err
		}
		leaf := *cert
		leaf.Certificate = leaf.Certificate[:1]
		return &leaf, nil
	}
	return stripped
}

// reaches calls the health of a server through a link and says what came back.
func reaches(t *testing.T, link api.ServerLink) error {
	t.Helper()
	client, err := api.NewServerClient(link)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.Health(context.Background())
	return err
}

// A worker whose join string names a fingerprint trusts the authority that fingerprint is of and no other, so a
// server that holds another authority, whatever name it presents, is refused, and the refusal says which.
func TestALinkThatPinsAnAuthorityTrustsThatAuthorityAndNoOther(t *testing.T) {
	t.Parallel()
	pinned := authority(t, nil, "127.0.0.1")
	port := serveWith(t, pinned.TLSConfig())
	url := "https://127.0.0.1:" + port
	if err := reaches(t, api.ServerLink{URL: url, CASHA256: pinned.Fingerprint()}); err != nil {
		t.Fatalf("the server of the pinned authority is refused: %v", err)
	}
	// The same server, pinned to another authority.
	other := authority(t, nil, "127.0.0.1")
	err := reaches(t, api.ServerLink{URL: url, CASHA256: other.Fingerprint()})
	if !errors.Is(err, api.ErrPinRefused) || !errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("a server that is not the pinned authority's answers %v", err)
	}
	reason, ok := api.TrustFailure(err)
	if !ok || !strings.Contains(reason, "pinned") {
		t.Errorf("the refusal says %q, %v", reason, ok)
	}
	for _, shown := range []string{err.Error(), reason} {
		for _, secret := range []string{pinned.Fingerprint(), other.Fingerprint()} {
			if strings.Contains(shown, secret) {
				t.Errorf("a refusal shows a fingerprint: %q", shown)
			}
		}
	}
}

// A server that sends no authority in its chain cannot show it is the one pinned, and a server that sends the pinned
// authority's public certificate beside a certificate of another's making is no better: the pin is checked by what
// signed the server's certificate, and the authority's certificate is public.
func TestALinkThatPinsAnAuthorityRefusesAChainThatDoesNotEndAtIt(t *testing.T) {
	t.Parallel()
	pinned := authority(t, nil, "127.0.0.1")
	url := "https://127.0.0.1:" + serveWith(t, without(pinned.TLSConfig()))
	err := reaches(t, api.ServerLink{URL: url, CASHA256: pinned.Fingerprint()})
	if !errors.Is(err, api.ErrPinRefused) {
		t.Errorf("a server that sends no authority answers %v", err)
	}

	attacker := authority(t, nil, "127.0.0.1")
	block, _ := pem.Decode(pinned.CertificatePEM())
	forged := attacker.TLSConfig().Clone()
	inner := forged.GetCertificate
	forged.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, err := inner(hello)
		if err != nil {
			return nil, err
		}
		chain := *cert
		chain.Certificate = [][]byte{cert.Certificate[0], block.Bytes}
		return &chain, nil
	}
	url = "https://127.0.0.1:" + serveWith(t, forged)
	if err := reaches(t, api.ServerLink{URL: url, CASHA256: pinned.Fingerprint()}); !errors.Is(err, api.ErrPinRefused) {
		t.Errorf("a certificate of another authority beside the pinned one's answers %v", err)
	}
}

// A server of the pinned authority whose certificate does not cover the name the worker was given, or has ended,
// is refused, and the refusal says that and not that the authority is wrong.
func TestALinkThatPinsAnAuthorityStillChecksTheNameAndTheTime(t *testing.T) {
	t.Parallel()
	pinned := authority(t, nil, "127.0.0.1")
	port := serveWith(t, pinned.TLSConfig())
	err := reaches(t, api.ServerLink{URL: "https://localhost:" + port, CASHA256: pinned.Fingerprint()})
	reason, ok := api.TrustFailure(err)
	if !ok || !strings.Contains(reason, "host name") || errors.Is(err, api.ErrPinRefused) {
		t.Errorf("a name the certificate does not cover answers %q, %v: %v", reason, ok, err)
	}

	past := authority(t, fake.NewClock(time.Now().AddDate(-2, 0, 0)), "127.0.0.1")
	port = serveWith(t, past.TLSConfig())
	err = reaches(t, api.ServerLink{URL: "https://127.0.0.1:" + port, CASHA256: past.Fingerprint()})
	if reason, ok := api.TrustFailure(err); !ok || !strings.Contains(reason, "expired") {
		t.Errorf("a certificate that ended answers %q, %v: %v", reason, ok, err)
	}
}

// With no fingerprint the worker verifies through the system's roots, which a server's own authority is not in, so
// it is refused and told so, and a certificate a root vouches for is trusted. The roots stand for the system's, as
// a test cannot add to them.
func TestALinkWithNoFingerprintTrustsTheRootsAndRefusesAnAuthorityThatIsNotInThem(t *testing.T) {
	t.Parallel()
	made := authority(t, nil, "127.0.0.1")
	port := serveWith(t, made.TLSConfig())
	url := "https://127.0.0.1:" + port
	if err := reaches(t, api.ServerLink{URL: url, Roots: roots(t, made)}); err != nil {
		t.Fatalf("a certificate a trusted root issued is refused: %v", err)
	}
	for name, link := range map[string]api.ServerLink{
		"the system's roots":      {URL: url},
		"roots of another's":      {URL: url, Roots: roots(t, authority(t, nil, "127.0.0.1"))},
		"roots that hold nothing": {URL: url, Roots: x509.NewCertPool()},
	} {
		err := reaches(t, link)
		reason, ok := api.TrustFailure(err)
		if !ok || !strings.Contains(reason, "system roots") || errors.Is(err, api.ErrPinRefused) {
			t.Errorf("%s: a certificate no root vouches for answers %q, %v: %v", name, reason, ok, err)
		}
		if strings.Contains(err.Error(), made.Fingerprint()) || strings.Contains(reason, made.Fingerprint()) {
			t.Errorf("%s: a refusal shows a fingerprint", name)
		}
	}
	// A server that sends the authority in its chain is trusted no more for it.
	supplied := serveWith(t, without(made.TLSConfig()))
	if err := reaches(t, api.ServerLink{URL: "https://127.0.0.1:" + supplied, Roots: roots(t, made)}); err != nil {
		t.Errorf("a server of a trusted root that sends its leaf alone is refused: %v", err)
	}
}

// A link is made only for a URL a worker may reach and a pin that fits it, and the refusal of one never repeats it.
func TestALinkIsMadeOnlyForAURLAndAPinThatFit(t *testing.T) {
	t.Parallel()
	pin := strings.Repeat("ab", 32)
	for name, link := range map[string]api.ServerLink{
		"a loopback address over http": {URL: "http://127.0.0.1:7400"},
		"localhost over http":          {URL: "http://localhost:7400"},
		"https with a pin":             {URL: "https://ropeshift.lan:7400", CASHA256: pin},
		"https with no pin":            {URL: "https://ropeshift.lan:7400"},
		"https with no port":           {URL: "https://ropeshift.lan"},
	} {
		client, err := api.NewServerClient(link)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		client.Close()
	}
	for name, link := range map[string]api.ServerLink{
		"http off loopback":       {URL: "http://ropeshift.lan:7400"},
		"a pin on http":           {URL: "http://127.0.0.1:7400", CASHA256: pin},
		"a pin that is too short": {URL: "https://ropeshift.lan:7400", CASHA256: "abcd"},
		"a pin that is no hex":    {URL: "https://ropeshift.lan:7400", CASHA256: strings.Repeat("zz", 32)},
		"credentials":             {URL: "https://me:hunter2@ropeshift.lan:7400"},
		"a path":                  {URL: "https://ropeshift.lan:7400/hunter2"},
		"no scheme":               {URL: "ropeshift.lan:7400"},
		"an empty URL":            {},
	} {
		client, err := api.NewServerClient(link)
		if err == nil {
			client.Close()
			t.Errorf("%s: a link is made", name)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), pin) {
			t.Errorf("%s: the refusal repeats what was given: %v", name, err)
		}
	}
}

// The pin's own config reports what it refuses in the classes TrustFailure names, and a config of no fingerprint
// verifies nothing it was not asked to.
func TestThePinnedConfigNeverTakesAServerThatSendsNothing(t *testing.T) {
	t.Parallel()
	config := api.PinnedTLS(strings.Repeat("ab", 32), "ropeshift.lan")
	if config.VerifyConnection == nil || !config.InsecureSkipVerify || config.MinVersion < tls.VersionTLS12 {
		t.Fatalf("the pinned config is %+v", config)
	}
	if err := config.VerifyConnection(tls.ConnectionState{}); !errors.Is(err, api.ErrPinRefused) {
		t.Errorf("a server that sent no certificate answers %v", err)
	}
	for _, shown := range []error{api.ErrPinRefused} {
		if strings.Contains(shown.Error(), strings.Repeat("ab", 32)) {
			t.Error("the pin's refusal shows a fingerprint")
		}
	}
}
