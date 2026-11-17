package contract

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/servertls"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/test/apitest"
)

// A group server's listener serves TLS when it is off loopback, in its own authority's certificate (mode a) or
// in one a person supplies (mode b). These rigs serve the network listener on a loopback port with each, and the
// worker's side is the production link a join string makes: the pin of the authority's fingerprint for mode (a),
// and the roots for mode (b), where the supplied certificate's authority stands for the system's roots, which a
// test cannot add to.

// serving makes what a listener serves, and the link of the worker that trusts it, which holds no URL until the
// listener has a port.
type serving func(t *testing.T) (listener *tls.Config, link api.ServerLink)

// secureLink is the rig of the network listener over TLS, with the link a serving function makes.
func secureLink(serve serving) func(t *testing.T, protocol int) *LinkRig {
	return func(t *testing.T, protocol int) *LinkRig {
		t.Helper()
		listener, link := serve(t)
		return networkLink(t, protocol, listener, func(network *apitest.Web) (*api.Client, error) {
			link.URL = "https://" + network.Host
			return api.NewServerClient(link)
		})
	}
}

// ownAuthority is mode (a): the server's certificate authority and the certificate it issued, with a link that
// pins the authority's fingerprint.
func ownAuthority(t *testing.T) (listener *tls.Config, link api.ServerLink) {
	t.Helper()
	authority, err := servertls.OpenAuthority(filepath.Join(t.TempDir(), "tls"), servertls.Options{Names: []string{"127.0.0.1"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	return authority.TLSConfig(), api.ServerLink{CASHA256: authority.Fingerprint()}
}

// suppliedCertificate is mode (b): a certificate and key in files, with a link that verifies through the roots
// that hold the authority that made them.
func suppliedCertificate(t *testing.T) (listener *tls.Config, link api.ServerLink) {
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
	return pair.TLSConfig(), api.ServerLink{Roots: roots}
}

// proxiedLink is mode (c): the listener serves plaintext to the test proxy, which is the only proxy it lists, and
// the worker reaches the proxy over TLS, pinned to the proxy's authority, which stands for the roots a real
// proxy's certificate chains to. The proxy sets the headers a real one does.
func proxiedLink(t *testing.T, protocol int) *LinkRig {
	t.Helper()
	authority, err := servertls.OpenAuthority(filepath.Join(t.TempDir(), "tls"), servertls.Options{Names: []string{"127.0.0.1"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	proxies, err := server.ParseProxies([]string{"127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	return networkLink(t, protocol, nil, func(network *apitest.Web) (*api.Client, error) {
		proxy := apitest.ReverseProxy(t, network.Host, authority.TLSConfig())
		return api.NewServerClient(api.ServerLink{URL: "https://" + proxy, CASHA256: authority.Fingerprint()})
	}, func(options *apitest.NetworkOptions) {
		options.Proxies, options.PublicURL, options.Client = proxies, "https://ropeshift.example", proxies.Client
	})
}

// A worker pinned to one authority reaches no server that holds another, and is told that the pin did not hold, so
// the rigs above prove the chain a server sends and not only that a handshake completes.
func TestAWorkerPinnedToAnotherAuthorityReachesNothing(t *testing.T) {
	t.Parallel()
	listener, pinned := ownAuthority(t)
	_, other := ownAuthority(t)
	err := callOver(t, listener, other)
	if !errors.Is(err, api.ErrUnreachable) || !errors.Is(err, api.ErrPinRefused) {
		t.Fatalf("a worker pinned to another authority answers %v", err)
	}
	if reason, ok := api.TrustFailure(err); !ok || !strings.Contains(reason, "pinned") || strings.Contains(err.Error(), other.CASHA256) || strings.Contains(err.Error(), pinned.CASHA256) {
		t.Errorf("the refusal says %q, %v, and reads: %v", reason, ok, err)
	}
	if err := callOver(t, listener, pinned); errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("a worker pinned to the server's authority answers %v", err)
	}
}

// A certificate someone supplies is trusted through the roots, so a worker whose join string pins nothing and that
// knows no authority of it reaches nothing, and is told that the system roots do not vouch for the server.
func TestAWorkerOnTheSystemRootsReachesNothingOfAnAuthorityThatIsNotInThem(t *testing.T) {
	t.Parallel()
	listener, trusting := suppliedCertificate(t)
	err := callOver(t, listener, api.ServerLink{})
	if !errors.Is(err, api.ErrUnreachable) || errors.Is(err, api.ErrPinRefused) {
		t.Fatalf("a worker on the system roots answers %v", err)
	}
	if reason, ok := api.TrustFailure(err); !ok || !strings.Contains(reason, "system roots") {
		t.Errorf("the refusal says %q, %v", reason, ok)
	}
	if err := callOver(t, listener, trusting); errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("a worker whose roots hold the authority answers %v", err)
	}
}

// callOver makes one call to a listener through the link of a worker. A listener that answers it, even to refuse a
// call that carries no credential, was reached.
func callOver(t *testing.T, listener *tls.Config, link api.ServerLink) error {
	t.Helper()
	var err error
	networkLink(t, 0, listener, func(network *apitest.Web) (*api.Client, error) {
		link.URL = "https://" + network.Host
		client, dialed := api.NewServerClient(link)
		if dialed != nil {
			return nil, dialed
		}
		_, err = client.Health(context.Background())
		return client, nil
	})
	return err
}
