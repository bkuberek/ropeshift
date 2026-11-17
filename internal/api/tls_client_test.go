package api_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

func healthy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
}

// trusting is the TLS config of a client that trusts the one certificate a test server holds.
func trusting(server *httptest.Server) *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
}

// A client on an https address speaks the same API as a client on the socket, and decides whom to trust by the
// config it is given, which is how a worker comes to pin an authority.
func TestATLSClientCallsTheAPIOverHTTPSAsItsConfigTrusts(t *testing.T) {
	t.Parallel()
	var seen *http.Request
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		healthy().ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := api.NewTLSClient(server.URL, trusting(server))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if _, err := client.WithCredential("a-worker-credential").Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen.URL.Path != "/v1/health" || seen.Header.Get("Authorization") != "Bearer a-worker-credential" || seen.TLS == nil {
		t.Fatalf("the server saw %v", seen)
	}
}

// A config that does not trust the server's certificate reaches nothing, and a nil config trusts the system's
// roots, which a test server's certificate is not among.
func TestATLSClientReachesNoServerItsConfigDoesNotTrust(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(healthy())
	t.Cleanup(server.Close)
	for name, config := range map[string]*tls.Config{"no config": nil, "an empty config": {}} {
		client, err := api.NewTLSClient(server.URL, config)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Health(context.Background())
		client.Close()
		if !errors.Is(err, api.ErrUnreachable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A client takes no version below TLS 1.2, whatever its config allows, so a worker never sends its credential
// over a protocol that is broken.
func TestATLSClientTakesNoVersionBelowTLS12(t *testing.T) {
	t.Parallel()
	server := httptest.NewUnstartedServer(healthy())
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}
	server.StartTLS()
	t.Cleanup(server.Close)
	config := trusting(server)
	config.MinVersion = tls.VersionTLS10
	client, err := api.NewTLSClient(server.URL, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Health(context.Background()); !errors.Is(err, api.ErrUnreachable) {
		t.Errorf("a server that speaks TLS 1.1 answers %v", err)
	}
}

func TestATLSClientTakesAnHTTPSHostAndAPortAndNothingElse(t *testing.T) {
	t.Parallel()
	taken := map[string]string{
		"https://ropeshift.lan":        "https://ropeshift.lan:443",
		"https://ropeshift.lan:7400":   "https://ropeshift.lan:7400",
		"https://ropeshift.lan:7400/":  "https://ropeshift.lan:7400",
		"https://192.168.1.5:7400":     "https://192.168.1.5:7400",
		"https://[::1]:7400":           "https://[::1]:7400",
		"https://Ropeshift.LAN:7400":   "https://ropeshift.lan:7400",
		"https://localhost:7400":       "https://localhost:7400",
		"https://ropeshift.lan:443":    "https://ropeshift.lan:443",
		"https://ropeshift.lan:65535/": "https://ropeshift.lan:65535",
	}
	for address, want := range taken {
		got, err := api.SecureServerAddress(address)
		if err != nil || got != want {
			t.Errorf("%s gives %q, %v, and wants %q", address, got, err, want)
		}
		client, err := api.NewTLSClient(address, nil)
		if err != nil {
			t.Errorf("%s: %v", address, err)
			continue
		}
		client.Close()
	}
	for _, address := range []string{
		"http://ropeshift.lan:7400", "http://127.0.0.1:7400", "ropeshift.lan:7400", "//ropeshift.lan:7400", "ftp://ropeshift.lan", "wss://ropeshift.lan",
		"https://", "https:///path", "https://:7400", "https://user@ropeshift.lan", "https://user:hunter2@ropeshift.lan", "https://ropeshift.lan/path",
		"https://ropeshift.lan/?x=1", "https://ropeshift.lan?", "https://ropeshift.lan#frag", "https://ropeshift.lan:0", "https://ropeshift.lan:65536",
		"https://ropeshift.lan:abc", "https://ropeshift.lan:-1", "https:ropeshift.lan", "", " https://ropeshift.lan", "https://ropeshift lan",
	} {
		got, err := api.SecureServerAddress(address)
		if err == nil {
			t.Errorf("%q is taken as %q", address, got)
			continue
		}
		// A refusal never repeats what the address held, which may have been a password.
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("the refusal of %q repeats the password: %v", address, err)
		}
		if client, err := api.NewTLSClient(address, nil); err == nil {
			client.Close()
			t.Errorf("a client is made on %q", address)
		}
	}
}

// A call goes to the address and nowhere else: a redirect would carry the session and the credential in its
// headers to wherever it points.
func TestATLSClientFollowsNoRedirect(t *testing.T) {
	t.Parallel()
	var reached bool
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		healthy().ServeHTTP(w, r)
	}))
	t.Cleanup(elsewhere.Close)
	redirecting := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirecting.Close)
	config := trusting(redirecting)
	config.RootCAs.AddCert(elsewhere.Certificate())
	client, err := api.NewTLSClient(redirecting.URL, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.WithCredential("a-worker-credential").Health(context.Background()); err == nil {
		t.Error("a redirect was followed")
	}
	if reached {
		t.Error("the redirect's target was called")
	}
}
