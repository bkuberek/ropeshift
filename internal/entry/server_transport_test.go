package entry

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/servertls"
	"ropeshift.com/ropeshift/test/apitest"
)

// A server's certificate settings come from its flags and from the environment a container gives it, and a flag
// wins over the environment.
func TestAServerTakesItsCertificateSettingsFromFlagsAndTheEnvironment(t *testing.T) {
	t.Parallel()
	given := map[string]string{"ROPESHIFT_TLS_CERT": "/env/cert.pem", "ROPESHIFT_TLS_KEY": "/env/key.pem", "ROPESHIFT_SERVER_NAMES": "a.lan, b.lan"}
	tests := []struct {
		name  string
		args  []string
		env   map[string]string
		cert  string
		key   string
		names []string
	}{
		{"the defaults", nil, nil, "", "", nil},
		{"the environment", nil, given, "/env/cert.pem", "/env/key.pem", []string{"a.lan", "b.lan"}},
		{"flags over the environment", []string{"--tls-cert", "/flag/cert.pem", "--tls-key", "/flag/key.pem", "--name", "c.lan", "--name", "d.lan,e.lan"}, given, "/flag/cert.pem", "/flag/key.pem", []string{"c.lan", "d.lan", "e.lan"}},
		{"a flag for one setting only", []string{"--name", "c.lan"}, given, "/env/cert.pem", "/env/key.pem", []string{"c.lan"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, ok := parseServer(append([]string{"--database", "postgres://db/x"}, tt.args...), env(tt.env), &stderr)
			if !ok {
				t.Fatalf("refused: %s", &stderr)
			}
			if opts.tlsCert != tt.cert || opts.tlsKey != tt.key || !slices.Equal(opts.names, tt.names) {
				t.Errorf("cert %q, key %q, names %q", opts.tlsCert, opts.tlsKey, opts.names)
			}
		})
	}
}

// A server behind a reverse proxy takes the same three settings from its flags and from its environment, and a flag
// wins over the variable.
func TestAServerTakesItsProxySettingsFromFlagsAndTheEnvironment(t *testing.T) {
	t.Parallel()
	given := map[string]string{"ROPESHIFT_TLS": "off", "ROPESHIFT_TRUSTED_PROXIES": "10.0.0.0/8, 172.16.0.0/12", "ROPESHIFT_PUBLIC_URL": "https://env.example"}
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		tls     string
		proxies []string
		public  string
	}{
		{"the defaults", nil, nil, "", nil, ""},
		{"the environment", nil, given, "off", []string{"10.0.0.0/8", "172.16.0.0/12"}, "https://env.example"},
		{"flags over the environment", []string{"--tls", "on", "--trusted-proxies", "192.168.0.0/16", "--trusted-proxies", "fd00::/8,::1", "--public-url", "https://flag.example"}, given, "on", []string{"192.168.0.0/16", "fd00::/8", "::1"}, "https://flag.example"},
		{"one flag", []string{"--public-url", "https://flag.example"}, given, "off", []string{"10.0.0.0/8", "172.16.0.0/12"}, "https://flag.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, ok := parseServer(append([]string{"--database", "postgres://db/x"}, tt.args...), env(tt.env), &stderr)
			if !ok {
				t.Fatalf("refused: %s", &stderr)
			}
			if opts.tls != tt.tls || !slices.Equal(opts.trustedProxies, tt.proxies) || opts.publicURL != tt.public {
				t.Errorf("tls %q, proxies %q, public URL %q", opts.tls, opts.trustedProxies, opts.publicURL)
			}
		})
	}
}

// Behind a proxy that terminates TLS the listener takes plaintext, which only loopback and the proxies it lists may
// send (NFR-SEC-2), so TLS off with no proxy listed, or with no public URL for its members to use, is no server to
// start, and says what is missing.
func TestAServerBehindAProxyNeedsItsProxiesAndItsPublicURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    serverOptions
		mode    transportMode
		proxies int
		public  string
		refuses []string
	}{
		{name: "the proxy's network", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8"}, publicURL: "https://ropeshift.example"}, mode: transportProxy, proxies: 1, public: "https://ropeshift.example"},
		{name: "a public URL in capitals, with its default port", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8", "fd00::/8"}, publicURL: "HTTPS://Ropeshift.Example:443/"}, mode: transportProxy, proxies: 2, public: "https://ropeshift.example"},
		{name: "a public URL with a port", opts: serverOptions{listen: ":7400", tls: "off", trustedProxies: []string{"10.0.0.2"}, publicURL: "https://ropeshift.example:8443"}, mode: transportProxy, proxies: 1, public: "https://ropeshift.example:8443"},
		{name: "a proxy on this machine", opts: serverOptions{listen: "127.0.0.1:7400", tls: "off", trustedProxies: []string{"127.0.0.1"}, publicURL: "https://ropeshift.example"}, mode: transportProxy, proxies: 1, public: "https://ropeshift.example"},
		{name: "TLS off on loopback with nothing else", opts: serverOptions{listen: "127.0.0.1:7400", tls: "off"}, mode: transportLoopback},
		{name: "TLS on is what a server has", opts: serverOptions{listen: "127.0.0.1:7400", tls: "on"}, mode: transportLoopback},
		{name: "off loopback with nothing", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off"}, refuses: []string{"--trusted-proxies", "--public-url"}},
		{name: "no public URL", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8"}}, refuses: []string{"--public-url"}},
		{name: "no public URL beside a proxy on loopback", opts: serverOptions{listen: "127.0.0.1:7400", tls: "off", trustedProxies: []string{"127.0.0.1"}}, refuses: []string{"--public-url"}},
		{name: "no proxy", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", publicURL: "https://ropeshift.example"}, refuses: []string{"--trusted-proxies"}},
		{name: "no proxy on loopback", opts: serverOptions{listen: "127.0.0.1:7400", tls: "off", publicURL: "https://ropeshift.example"}, refuses: []string{"--trusted-proxies"}},
		{name: "a public URL that is http", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8"}, publicURL: "http://ropeshift.example"}, refuses: []string{"https"}},
		{name: "a public URL with a path", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8"}, publicURL: "https://ropeshift.example/ropeshift"}, refuses: []string{"--public-url"}},
		{name: "a public URL with credentials", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8"}, publicURL: "https://me:hunter2@ropeshift.example"}, refuses: []string{"--public-url"}},
		{name: "a public URL that is a name", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8"}, publicURL: "ropeshift.example"}, refuses: []string{"--public-url"}},
		{name: "every address as a proxy", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"0.0.0.0/0"}, publicURL: "https://ropeshift.example"}, refuses: []string{"--trusted-proxies", "0.0.0.0/0"}},
		{name: "every IPv6 address as a proxy", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8", "::/0"}, publicURL: "https://ropeshift.example"}, refuses: []string{"--trusted-proxies", "::/0"}},
		{name: "a proxy that is a name", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"proxy.internal"}, publicURL: "https://ropeshift.example"}, refuses: []string{"--trusted-proxies", "proxy.internal"}},
		{name: "a certificate beside TLS off", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8"}, publicURL: "https://ropeshift.example", tlsCert: "/c.pem", tlsKey: "/k.pem"}, refuses: []string{"--tls off", "--tls-cert"}},
		{name: "names beside TLS off", opts: serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8"}, publicURL: "https://ropeshift.example", names: []string{"ropeshift.example"}}, refuses: []string{"--tls off", "--name"}},
		{name: "proxies without TLS off", opts: serverOptions{listen: "0.0.0.0:7400", trustedProxies: []string{"10.0.0.0/8"}, names: []string{"ropeshift.lan"}}, refuses: []string{"--trusted-proxies", "--tls off"}},
		{name: "proxies with a certificate", opts: serverOptions{listen: "0.0.0.0:7400", trustedProxies: []string{"10.0.0.0/8"}, tlsCert: "/c.pem", tlsKey: "/k.pem"}, refuses: []string{"--trusted-proxies", "--tls off"}},
		{name: "a public URL without TLS off", opts: serverOptions{listen: "0.0.0.0:7400", publicURL: "https://ropeshift.example", names: []string{"ropeshift.lan"}}, refuses: []string{"--public-url", "--tls off"}},
		{name: "a public URL on loopback", opts: serverOptions{listen: "127.0.0.1:7400", publicURL: "https://ropeshift.example"}, refuses: []string{"--public-url", "--tls off"}},
		{name: "a value that is not on or off", opts: serverOptions{listen: "127.0.0.1:7400", tls: "maybe"}, refuses: []string{"--tls", "maybe"}},
		{name: "a value in capitals", opts: serverOptions{listen: "127.0.0.1:7400", tls: "OFF"}, refuses: []string{"--tls", "OFF"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planTransport(tt.opts)
			if len(tt.refuses) > 0 {
				if err == nil {
					t.Fatalf("the plan is %+v", plan)
				}
				for _, want := range tt.refuses {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not say %q: %v", want, err)
					}
				}
				if strings.Contains(err.Error(), "hunter2") {
					t.Errorf("the refusal repeats a password: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.mode != tt.mode || plan.proxies.Len() != tt.proxies || plan.publicURL != tt.public {
				t.Errorf("the plan is %+v", plan)
			}
		})
	}
}

// With TLS off and a proxy listed, the server refuses before it opens a database, and says what to set.
func TestAServerWithTLSOffAndNoProxyIsRefusedBeforeItConnects(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	code, stdout, stderr := runMain(commandDaemon, "server", "--database", "host=/nowhere dbname=none", "--listen", "0.0.0.0:7400", "--tls", "off")
	if code != exitUsage || stdout != "" {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	for _, want := range []string{"--trusted-proxies", "--public-url", "ROPESHIFT_TRUSTED_PROXIES", "ROPESHIFT_PUBLIC_URL"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q does not name %s", stderr, want)
		}
	}
}

// Behind a proxy the listener serves plaintext to the proxy and to loopback, reports no web address of its own,
// and says where members reach it. A peer that is not listed is closed, which the listener's own tests hold.
func TestTheServerBehindAProxyServesPlaintextToItsProxy(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	plan, err := planTransport(serverOptions{listen: "127.0.0.1:0", tls: "off", trustedProxies: []string{"127.0.0.1"}, publicURL: "https://ropeshift.example"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := plan.listen("127.0.0.1:0", state, clock.System{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serve(t, listener)
	response, err := http.Get("http://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || listener.fingerprint != "" || listener.scheme() != "http" || listener.webAddress() != "" || listener.publicURL != "https://ropeshift.example" {
		t.Errorf("status %d, fingerprint %q, scheme %q, web address %q, public URL %q", response.StatusCode, listener.fingerprint, listener.scheme(), listener.webAddress(), listener.publicURL)
	}
	if _, err := os.Stat(filepath.Join(state, tlsDirectory)); err == nil {
		t.Error("a server behind a proxy made a certificate authority")
	}
}

// The limit on wrong guesses is 10 within 15 minutes unless a setting says otherwise, from a flag or from the
// environment a container gives, and a flag wins.
func TestAServerTakesItsSignInLimitFromFlagsAndTheEnvironment(t *testing.T) {
	t.Parallel()
	given := map[string]string{"ROPESHIFT_SIGNIN_MAX_FAILURES": "5", "ROPESHIFT_SIGNIN_LOCKOUT": "30m"}
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		max     int
		lockout time.Duration
	}{
		{"the defaults", nil, nil, 10, 15 * time.Minute},
		{"the environment", nil, given, 5, 30 * time.Minute},
		{"flags over the environment", []string{"--signin-max-failures", "3", "--signin-lockout", "1h"}, given, 3, time.Hour},
		{"one flag", []string{"--signin-lockout", "90s"}, given, 5, 90 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, ok := parseServer(append([]string{"--database", "postgres://db/x"}, tt.args...), env(tt.env), &stderr)
			if !ok {
				t.Fatalf("refused: %s", &stderr)
			}
			if opts.signinMax != tt.max || opts.signinLockout != tt.lockout {
				t.Errorf("max %d, lockout %s", opts.signinMax, opts.signinLockout)
			}
		})
	}
}

func TestAServerRefusesALimitThatLimitsNothing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		env  map[string]string
		says string
	}{
		{"no failures allowed at all", []string{"--signin-max-failures", "0"}, nil, "--signin-max-failures"},
		{"a negative count", []string{"--signin-max-failures", "-3"}, nil, "--signin-max-failures"},
		{"no lockout", []string{"--signin-lockout", "0s"}, nil, "--signin-lockout"},
		{"a negative lockout", []string{"--signin-lockout", "-5m"}, nil, "--signin-lockout"},
		{"a count in the environment that is no number", nil, map[string]string{"ROPESHIFT_SIGNIN_MAX_FAILURES": "ten"}, "ROPESHIFT_SIGNIN_MAX_FAILURES"},
		{"a lockout in the environment that is no duration", nil, map[string]string{"ROPESHIFT_SIGNIN_LOCKOUT": "soon"}, "ROPESHIFT_SIGNIN_LOCKOUT"},
		{"a count of zero in the environment", nil, map[string]string{"ROPESHIFT_SIGNIN_MAX_FAILURES": "0"}, "--signin-max-failures"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if _, ok := parseServer(append([]string{"--database", "postgres://db/x"}, tt.args...), env(tt.env), &stderr); ok {
				t.Fatal("the limit is taken")
			}
			if !strings.Contains(stderr.String(), tt.says) {
				t.Errorf("stderr does not name %s: %s", tt.says, &stderr)
			}
		})
	}
}

// The listener's address decides what it must serve: plaintext reaches only the machine it is on, so any
// other address serves TLS, in the certificate the server makes or in one a person supplies.
func TestAServerPlansItsTransportFromItsAddressAndItsCertificateSettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    serverOptions
		mode    transportMode
		names   []string
		refuses string
	}{
		{name: "loopback with nothing set", opts: serverOptions{listen: "127.0.0.1:7400"}, mode: transportLoopback},
		{name: "localhost", opts: serverOptions{listen: "localhost:7400"}, mode: transportLoopback},
		{name: "the IPv6 loopback", opts: serverOptions{listen: "[::1]:7400"}, mode: transportLoopback},
		{name: "any address with names", opts: serverOptions{listen: "0.0.0.0:7400", names: []string{"ropeshift.lan", "192.168.1.5"}}, mode: transportOwnAuthority, names: []string{"ropeshift.lan", "192.168.1.5"}},
		{name: "every address, IPv6", opts: serverOptions{listen: "[::]:7400", names: []string{"ropeshift.lan"}}, mode: transportOwnAuthority, names: []string{"ropeshift.lan"}},
		{name: "no host", opts: serverOptions{listen: ":7400", names: []string{"ropeshift.lan"}}, mode: transportOwnAuthority, names: []string{"ropeshift.lan"}},
		{name: "a LAN address", opts: serverOptions{listen: "192.168.1.5:7400", names: []string{"Ropeshift.LAN", "ropeshift.lan"}}, mode: transportOwnAuthority, names: []string{"ropeshift.lan"}},
		{name: "a host name", opts: serverOptions{listen: "server.example:7400", names: []string{"server.example"}}, mode: transportOwnAuthority, names: []string{"server.example"}},
		{name: "a supplied certificate off loopback", opts: serverOptions{listen: "0.0.0.0:7400", tlsCert: "/c.pem", tlsKey: "/k.pem"}, mode: transportCertificate},
		{name: "a supplied certificate on loopback", opts: serverOptions{listen: "127.0.0.1:7400", tlsCert: "/c.pem", tlsKey: "/k.pem"}, mode: transportCertificate},
		{name: "off loopback with no name", opts: serverOptions{listen: "0.0.0.0:7400"}, refuses: "--name"},
		{name: "off loopback with names that are empty", opts: serverOptions{listen: "0.0.0.0:7400", names: []string{" ", ""}}, refuses: "name"},
		{name: "a name with a port", opts: serverOptions{listen: "0.0.0.0:7400", names: []string{"ropeshift.lan:7400"}}, refuses: "ropeshift.lan:7400"},
		{name: "a name that is a URL", opts: serverOptions{listen: "0.0.0.0:7400", names: []string{"https://ropeshift.lan"}}, refuses: "https://ropeshift.lan"},
		{name: "a certificate with no key", opts: serverOptions{listen: "0.0.0.0:7400", tlsCert: "/c.pem"}, refuses: "--tls-key"},
		{name: "a key with no certificate", opts: serverOptions{listen: "0.0.0.0:7400", tlsKey: "/k.pem"}, refuses: "--tls-cert"},
		{name: "names beside a supplied certificate", opts: serverOptions{listen: "0.0.0.0:7400", tlsCert: "/c.pem", tlsKey: "/k.pem", names: []string{"ropeshift.lan"}}, refuses: "--name"},
		{name: "names on loopback", opts: serverOptions{listen: "127.0.0.1:7400", names: []string{"localhost", "127.0.0.1"}}, mode: transportOwnAuthority, names: []string{"localhost", "127.0.0.1"}},
		{name: "no port", opts: serverOptions{listen: "no port", names: []string{"ropeshift.lan"}}, refuses: "--listen"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planTransport(tt.opts)
			if tt.refuses != "" {
				if err == nil || !strings.Contains(err.Error(), tt.refuses) {
					t.Fatalf("the plan answers %v, plan %+v", err, plan)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.mode != tt.mode || !slices.Equal(plan.names, tt.names) {
				t.Errorf("the plan is %+v", plan)
			}
			if tt.mode == transportCertificate && (plan.certFile != tt.opts.tlsCert || plan.keyFile != tt.opts.tlsKey) {
				t.Errorf("the plan names the files %q and %q", plan.certFile, plan.keyFile)
			}
		})
	}
}

// The refusal comes before the server touches a database, a lock, or a socket, so a mistaken address costs
// nothing, and it tells a person what to set.
func TestAServerOffLoopbackWithNoCertificateNameIsRefusedBeforeItConnects(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	code, stdout, stderr := runMain(commandDaemon, "server", "--database", "host=/nowhere dbname=none", "--listen", "0.0.0.0:7400")
	if code != exitUsage || stdout != "" {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	for _, want := range []string{"--name", "ROPESHIFT_SERVER_NAMES", "--tls-cert"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q does not name %s", stderr, want)
		}
	}
}

func get(t *testing.T, client *tls.Config, address string) *http.Response {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: client, DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	response, err := (&http.Client{Transport: transport}).Get(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	return response
}

func serve(t *testing.T, listener *serverListener) {
	t.Helper()
	httpServer := networkHTTPServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = httpServer.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = httpServer.Close()
		<-done
	})
}

// Mode (a): the server makes its authority in its state directory at first start, serves the certificate it
// issued, sends the authority with it so a client can pin the fingerprint, and keeps the keys to its user.
func TestTheServerServesItsOwnAuthorityAndKeepsItsKeysPrivate(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	plan := serverTransport{mode: transportOwnAuthority, names: []string{"127.0.0.1"}}
	listener, err := plan.listen("127.0.0.1:0", state, clock.System{}, func(err error) { t.Errorf("warned: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serve(t, listener)

	if len(listener.fingerprint) != 64 || !strings.HasSuffix(listener.authorityFile, filepath.Join("tls", "ca.crt")) {
		t.Fatalf("the listener names the fingerprint %q and the authority %q", listener.fingerprint, listener.authorityFile)
	}
	response := get(t, apitest.PinnedTLS(listener.fingerprint, "127.0.0.1"), "https://"+listener.Addr().String()+"/")
	if response.StatusCode != http.StatusOK || response.TLS == nil {
		t.Fatalf("status %d, TLS %v", response.StatusCode, response.TLS)
	}
	for _, name := range []string{"ca.key", "server.key"} {
		info, err := os.Stat(filepath.Join(state, tlsDirectory, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v, %v", name, info, err)
		}
	}
	// A client that pins a fingerprint that is not the authority's reaches nothing.
	transport := &http.Transport{TLSClientConfig: apitest.PinnedTLS(strings.Repeat("0", 64), "127.0.0.1"), DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	if _, err := (&http.Client{Transport: transport}).Get("https://" + listener.Addr().String() + "/"); err == nil {
		t.Error("a client with the wrong pin was served")
	}
}

// The bounds of the network server cover a handshake too: a client that opens a connection and never finishes
// its hello holds nothing for long, and a plaintext request to the TLS port reaches no route.
func TestAClientThatNeverFinishesATLSHandshakeIsCutOff(t *testing.T) {
	was := networkLimits
	networkLimits = serverLimits{readHeader: 300 * time.Millisecond, read: 600 * time.Millisecond, idle: time.Second}
	t.Cleanup(func() { networkLimits = was })
	plan := serverTransport{mode: transportOwnAuthority, names: []string{"127.0.0.1"}}
	listener, err := plan.listen("127.0.0.1:0", t.TempDir(), clock.System{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serve(t, listener)
	for name, sent := range map[string][]byte{
		"nothing":          nil,
		"half a hello":     {0x16, 0x03, 0x01, 0x00, 0xff, 0x01},
		"a plaintext call": []byte("GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"),
	} {
		t.Run(name, func(t *testing.T) {
			conn, err := net.Dial("tcp", listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if sent != nil {
				if _, err := conn.Write(sent); err != nil {
					t.Fatal(err)
				}
			}
			started := time.Now()
			if err := conn.SetReadDeadline(started.Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			answer, _ := io.ReadAll(conn)
			if waited := time.Since(started); waited > 5*time.Second {
				t.Errorf("the server held the connection for %s", waited)
			}
			if strings.HasSuffix(string(answer), "ok") {
				t.Errorf("a connection that never spoke TLS was answered by a route: %q", answer)
			}
		})
	}
}

// A restart finds the authority where it was left, so the fingerprint a join string carries survives it.
func TestTheServerKeepsItsAuthorityAcrossRestarts(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	plan := serverTransport{mode: transportOwnAuthority, names: []string{"127.0.0.1"}}
	var fingerprints []string
	for range 2 {
		listener, err := plan.listen("127.0.0.1:0", state, clock.System{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		fingerprints = append(fingerprints, listener.fingerprint)
		_ = listener.Close()
	}
	if fingerprints[0] != fingerprints[1] {
		t.Errorf("the fingerprint changed across a restart: %v", fingerprints)
	}
}

// Mode (b): the certificate and key are files a person supplies, and the server reads them again when they
// change, so the next connection after a renewal is served the renewed certificate with no restart.
func TestACertificateFileReplacedWhileTheServerRunsIsServedToTheNextConnection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	roots := x509.NewCertPool()
	// supply puts a certificate and key a certificate authority of the person's made into the two files, and
	// adds that authority to the roots a client verifies through.
	supply := func(modified time.Time) {
		t.Helper()
		from := filepath.Join(t.TempDir(), "made")
		made, err := servertls.OpenAuthority(from, servertls.Options{Names: []string{"127.0.0.1"}, Clock: clock.System{}})
		if err != nil {
			t.Fatal(err)
		}
		roots.AppendCertsFromPEM(made.CertificatePEM())
		for name, to := range map[string]string{servertls.CertFile: certFile, servertls.KeyFile: keyFile} {
			raw, err := os.ReadFile(filepath.Join(from, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(to, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(to, modified, modified); err != nil {
				t.Fatal(err)
			}
		}
	}
	supply(time.Now().Add(-2 * time.Hour))
	plan := serverTransport{mode: transportCertificate, certFile: certFile, keyFile: keyFile}
	listener, err := plan.listen("127.0.0.1:0", t.TempDir(), clock.System{}, func(err error) { t.Errorf("warned: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serve(t, listener)
	if listener.fingerprint != "" {
		t.Errorf("a supplied certificate has a fingerprint %q for a join string to pin", listener.fingerprint)
	}

	first := get(t, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, "https://"+listener.Addr().String()+"/")
	supply(time.Now().Add(-time.Hour))
	second := get(t, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, "https://"+listener.Addr().String()+"/")
	if first.TLS.PeerCertificates[0].SerialNumber.Cmp(second.TLS.PeerCertificates[0].SerialNumber) == 0 {
		t.Error("the replaced certificate was not served to the next connection")
	}
}

// On loopback with nothing set the listener serves plaintext, as it did before it could serve TLS, makes no
// certificate, and binds nothing else.
func TestTheServerOnLoopbackServesPlaintextAndMakesNoCertificate(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	plan := serverTransport{mode: transportLoopback}
	listener, err := plan.listen("127.0.0.1:0", state, clock.System{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serve(t, listener)
	response, err := http.Get("http://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || listener.fingerprint != "" {
		t.Errorf("status %d, fingerprint %q", response.StatusCode, listener.fingerprint)
	}
	if _, err := os.Stat(filepath.Join(state, tlsDirectory)); err == nil {
		t.Error("a server on loopback made a certificate authority")
	}
	if _, err := plan.listen("0.0.0.0:0", state, clock.System{}, nil); err == nil {
		t.Error("a plaintext listener bound an address off loopback")
	}
}
