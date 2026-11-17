package contract

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/servertls"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/test/apitest"
)

// A group server's listener serves TLS when it is off loopback, in its own authority's certificate (mode a) or
// in one a person supplies (mode b). These rigs serve the network listener on a loopback port with each, and
// the worker's side is the test's own client until a worker pins an authority in production: the pin below
// checks the chain the server sends, and the supplied certificate's authority stands for the system roots.

// serving makes what a listener serves, and the config of the client that trusts it.
type serving func(t *testing.T) (listener, client *tls.Config)

// secureLink is the rig of the network listener over TLS, with the client a serving function trusts.
func secureLink(serve serving) func(t *testing.T, protocol int) *LinkRig {
	return func(t *testing.T, protocol int) *LinkRig {
		t.Helper()
		listener, trusting := serve(t)
		return networkLink(t, protocol, listener, func(network *apitest.Web) (*api.Client, error) {
			return api.NewTLSClient("https://"+network.Host, trusting)
		})
	}
}

// ownAuthority is mode (a): the server's certificate authority and the certificate it issued, with a client that
// pins the authority's fingerprint.
func ownAuthority(t *testing.T) (listener, client *tls.Config) {
	t.Helper()
	authority, err := servertls.OpenAuthority(filepath.Join(t.TempDir(), "tls"), servertls.Options{Names: []string{"127.0.0.1"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	return authority.TLSConfig(), apitest.PinnedTLS(authority.Fingerprint(), "127.0.0.1")
}

// suppliedCertificate is mode (b): a certificate and key in files, with a client that verifies through the roots
// that hold the authority that made them.
func suppliedCertificate(t *testing.T) (listener, client *tls.Config) {
	t.Helper()
	dir := t.TempDir()
	authority, err := servertls.OpenAuthority(filepath.Join(dir, "authority"), servertls.Options{Names: []string{"127.0.0.1"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{servertls.CertFile: "tls.crt", servertls.KeyFile: "tls.key"} {
		raw, err := os.ReadFile(filepath.Join(dir, "authority", from))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, to), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pair, err := servertls.OpenPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), nil)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(authority.CertificatePEM()) {
		t.Fatal("the authority's certificate is not a certificate")
	}
	return pair.TLSConfig(), &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
}

// A client pinned to one authority reaches no server that holds another, so the rigs above prove the chain a
// server sends and not only that a handshake completes.
func TestAClientPinnedToAnotherAuthorityReachesNothing(t *testing.T) {
	t.Parallel()
	listener, pinned := ownAuthority(t)
	_, other := ownAuthority(t)
	if err := callOver(t, listener, other); !errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("a client pinned to another authority answers %v", err)
	}
	if err := callOver(t, listener, pinned); errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("a client pinned to the server's authority answers %v", err)
	}
}

// A certificate someone supplies is trusted through the roots, so a client that knows no authority of it
// reaches nothing.
func TestAClientThatKnowsNoAuthorityOfTheSuppliedCertificateReachesNothing(t *testing.T) {
	t.Parallel()
	listener, trusting := suppliedCertificate(t)
	if err := callOver(t, listener, nil); !errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("a client on the system roots answers %v", err)
	}
	if err := callOver(t, listener, trusting); errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("a client that trusts the authority answers %v", err)
	}
}

// callOver makes one call to a listener through a client with the config. A listener that answers it, even to
// refuse a call that carries no credential, was reached.
func callOver(t *testing.T, listener, trusting *tls.Config) error {
	t.Helper()
	var err error
	networkLink(t, 0, listener, func(network *apitest.Web) (*api.Client, error) {
		client, dialed := api.NewTLSClient("https://"+network.Host, trusting)
		if dialed != nil {
			return nil, dialed
		}
		_, err = client.Health(context.Background())
		return client, nil
	})
	return err
}
