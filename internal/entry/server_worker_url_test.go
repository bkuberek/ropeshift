package entry

import (
	"bytes"
	"net"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/clock"
)

// The URL a server's workers reach it at, which its join strings carry, comes from --worker-url or the environment
// a container gives it, and a flag wins.
func TestAServerTakesItsWorkerURLFromAFlagOrTheEnvironment(t *testing.T) {
	t.Parallel()
	given := map[string]string{"ROPESHIFT_WORKER_URL": "https://env.example:7400"}
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"the default", nil, nil, ""},
		{"the environment", nil, given, "https://env.example:7400"},
		{"a flag over the environment", []string{"--worker-url", "https://flag.example:7400"}, given, "https://flag.example:7400"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, ok := parseServer(append([]string{"--database", "postgres://db/x"}, tt.args...), env(tt.env), &stderr)
			if !ok || opts.workerURL != tt.want {
				t.Fatalf("worker URL %q, refused %v: %s", opts.workerURL, !ok, &stderr)
			}
		})
	}
}

// A worker URL is checked before the server touches anything, against how the listener is secured: a worker that
// pins the server's own authority must find its host among the names the certificate covers, and a worker URL says
// https only where the server serves TLS or a proxy does, and http only where the server serves plaintext.
func TestAWorkerURLIsCheckedAgainstHowTheServerIsSecured(t *testing.T) {
	t.Parallel()
	own := func(url string) serverOptions {
		return serverOptions{listen: "0.0.0.0:7400", names: []string{"Ropeshift.LAN", "192.168.1.5", "fd00::5"}, workerURL: url}
	}
	supplied := func(url string) serverOptions {
		return serverOptions{listen: "0.0.0.0:7400", tlsCert: "/c.pem", tlsKey: "/k.pem", workerURL: url}
	}
	proxied := func(url string) serverOptions {
		return serverOptions{listen: "0.0.0.0:7400", tls: "off", trustedProxies: []string{"10.0.0.0/8"}, publicURL: "https://ropeshift.example", workerURL: url}
	}
	loopback := func(url string) serverOptions { return serverOptions{listen: "127.0.0.1:7400", workerURL: url} }
	tests := []struct {
		name    string
		opts    serverOptions
		want    string
		refuses []string
	}{
		{name: "its own authority, by a name", opts: own("https://ropeshift.lan:7400"), want: "https://ropeshift.lan:7400"},
		{name: "its own authority, in capitals, with the default port", opts: own("HTTPS://Ropeshift.Lan/"), want: "https://ropeshift.lan:443"},
		{name: "its own authority, by an address", opts: own("https://192.168.1.5:7400"), want: "https://192.168.1.5:7400"},
		{name: "its own authority, by an IPv6 address", opts: own("https://[fd00::5]:7400"), want: "https://[fd00::5]:7400"},
		{name: "its own authority, by a host its certificate does not cover", opts: own("https://other.lan:7400"), refuses: []string{"--worker-url", "other.lan", "--name"}},
		{name: "its own authority, by an address its certificate does not cover", opts: own("https://192.168.1.6:7400"), refuses: []string{"--worker-url", "192.168.1.6", "--name"}},
		{name: "its own authority, over http", opts: own("http://127.0.0.1:7400"), refuses: []string{"--worker-url", "TLS"}},
		{name: "a supplied certificate, by any host", opts: supplied("https://ropeshift.example:7400"), want: "https://ropeshift.example:7400"},
		{name: "a supplied certificate, over http", opts: supplied("http://127.0.0.1:7400"), refuses: []string{"--worker-url", "TLS"}},
		{name: "a proxy, by its public host", opts: proxied("https://ropeshift.example"), want: "https://ropeshift.example:443"},
		{name: "a proxy, by another host the proxy serves", opts: proxied("https://workers.example:8443"), want: "https://workers.example:8443"},
		{name: "a proxy, over http on this machine", opts: proxied("http://localhost:7400"), want: "http://127.0.0.1:7400"},
		{name: "a proxy, over http off this machine", opts: proxied("http://ropeshift.lan:7400"), refuses: []string{"--worker-url", "loopback"}},
		{name: "loopback, over http", opts: loopback("http://127.0.0.1:7400"), want: "http://127.0.0.1:7400"},
		{name: "loopback, by localhost", opts: loopback("http://localhost:7400"), want: "http://127.0.0.1:7400"},
		{name: "loopback, over https", opts: loopback("https://ropeshift.example"), refuses: []string{"--worker-url", "--tls off"}},
		{name: "no scheme", opts: own("ropeshift.lan:7400"), refuses: []string{"--worker-url"}},
		{name: "a path", opts: own("https://ropeshift.lan:7400/ropeshift"), refuses: []string{"--worker-url"}},
		{name: "credentials", opts: own("https://me:hunter2@ropeshift.lan:7400"), refuses: []string{"--worker-url"}},
		{name: "a port past the last", opts: own("https://ropeshift.lan:99999"), refuses: []string{"--worker-url", "port"}},
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
			if plan.workerURL != tt.want {
				t.Errorf("the plan keeps the worker URL %q, and wants %q", plan.workerURL, tt.want)
			}
		})
	}
}

// With no --worker-url a server says where its workers reach it by what it knows: a server of its own authority by
// its first name at the port it bound, a server behind a proxy by its public URL, and a server on loopback by the
// address it bound. A supplied certificate says nothing of its names, so a server with one has none until it is told,
// and says so as nothing rather than guessing, because a join string with a host the certificate does not cover
// would fail at the worker's end.
func TestAServerSaysWhereItsWorkersReachItWhenNoneWasGiven(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		opts   serverOptions
		listen string
		want   func(port string) string
	}{
		{"its own authority", serverOptions{listen: "127.0.0.1:0", names: []string{"Localhost", "127.0.0.1"}}, "127.0.0.1:0", func(port string) string { return "https://localhost:" + port }},
		{"its own authority, by an IPv6 address", serverOptions{listen: "127.0.0.1:0", names: []string{"::1"}}, "127.0.0.1:0", func(port string) string { return "https://[::1]:" + port }},
		{"a proxy", serverOptions{listen: "127.0.0.1:0", tls: "off", trustedProxies: []string{"127.0.0.1"}, publicURL: "https://ropeshift.example"}, "127.0.0.1:0", func(string) string { return "https://ropeshift.example:443" }},
		{"loopback", serverOptions{listen: "127.0.0.1:0"}, "127.0.0.1:0", func(port string) string { return "http://127.0.0.1:" + port }},
		{"a flag over all of it", serverOptions{listen: "127.0.0.1:0", names: []string{"localhost"}, workerURL: "https://localhost:9"}, "127.0.0.1:0", func(string) string { return "https://localhost:9" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planTransport(tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := plan.listen(tt.listen, t.TempDir(), clock.System{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			_, port, _ := net.SplitHostPort(listener.Addr().String())
			if got := listener.workerURL(); got != tt.want(port) {
				t.Errorf("the server says its workers reach it at %q, and wants %q", got, tt.want(port))
			}
		})
	}
	plan, err := planTransport(serverOptions{listen: "127.0.0.1:0", tlsCert: "/c.pem", tlsKey: "/k.pem"})
	if err != nil {
		t.Fatal(err)
	}
	if got := (&serverListener{mode: plan.mode}).workerURL(); got != "" {
		t.Errorf("a server with a supplied certificate and no worker URL says %q", got)
	}
}

// The server tells its operator where its workers reach it, and never what a worker pins: the fingerprint of an
// authority is the trust anchor of a join string, so it reaches a member in one and the server's own output does not
// repeat it. The line that names the authority's file stays, which is what a person trusts on a device.
func TestTheServerSaysWhereItsWorkersReachItAndPrintsNoFingerprint(t *testing.T) {
	t.Parallel()
	plan, err := planTransport(serverOptions{listen: "127.0.0.1:0", names: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := plan.listen("127.0.0.1:0", t.TempDir(), clock.System{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if len(listener.fingerprint) != 64 {
		t.Fatalf("the listener holds the fingerprint %q", listener.fingerprint)
	}
	var stdout, stderr bytes.Buffer
	announce(&stdout, &stderr, "/run/control.sock", listener)
	said := stdout.String() + stderr.String()
	if strings.Contains(said, listener.fingerprint) || strings.Contains(strings.ToLower(said), "sha256") {
		t.Errorf("the server printed the fingerprint of its authority: %s", said)
	}
	for _, want := range []string{"worker url " + listener.workerURL(), "certificate authority " + listener.authorityFile, "server https://" + listener.Addr().String()} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the server did not say %q: %s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("a server that knows its worker URL warned: %s", &stderr)
	}

	// A server with no way to know says what to set, on stderr, and says no worker url.
	unknown := &serverListener{Listener: listener.Listener, mode: transportCertificate}
	stdout.Reset()
	stderr.Reset()
	announce(&stdout, &stderr, "/run/control.sock", unknown)
	if strings.Contains(stdout.String(), "worker url") || !strings.Contains(stderr.String(), "--worker-url") || !strings.Contains(stderr.String(), "ROPESHIFT_WORKER_URL") {
		t.Errorf("a server with no worker URL said %q and warned %q", stdout.String(), stderr.String())
	}
}

// A reverse proxy on the server's own machine reaches it from 127.0.0.1 or ::1. A server that does not list that
// address reads nothing the proxy forwards, so every member signs in from one address and a few wrong guesses
// refuse them all. The server starts anyway, because a proxy on another machine is right to leave it out, and warns,
// naming the setting that fixes it. A listener no proxy of this machine can reach through loopback has no warning.
func TestAProxyListenerThatLoopbackCanReachWarnsWhenLoopbackIsNotListed(t *testing.T) {
	t.Parallel()
	proxied := func(listen string, proxies ...string) serverOptions {
		return serverOptions{listen: listen, tls: "off", trustedProxies: proxies, publicURL: "https://ropeshift.example"}
	}
	tests := []struct {
		name    string
		opts    serverOptions
		warning string
	}{
		{"every address, loopback not listed", proxied("0.0.0.0:7400", "10.0.0.0/8"), "127.0.0.1"},
		{"no host, loopback not listed", proxied(":7400", "10.0.0.5"), "127.0.0.1"},
		{"every IPv6 address, loopback not listed", proxied("[::]:7400", "10.0.0.0/8"), "127.0.0.1"},
		{"the IPv4 loopback address, not listed", proxied("127.0.0.1:7400", "10.0.0.5"), "127.0.0.1"},
		{"localhost, not listed", proxied("localhost:7400", "10.0.0.5"), "127.0.0.1"},
		{"the IPv6 loopback address, not listed", proxied("[::1]:7400", "10.0.0.5"), "::1"},
		{"the IPv6 loopback address, with only the IPv4 one listed", proxied("[::1]:7400", "127.0.0.1"), "::1"},
		{"every address, with loopback listed", proxied("0.0.0.0:7400", "10.0.0.0/8", "127.0.0.1"), ""},
		{"every address, with a range of loopback listed", proxied("0.0.0.0:7400", "127.0.0.0/8"), ""},
		{"the IPv6 loopback address, listed", proxied("[::1]:7400", "::1"), ""},
		{"an address of this machine's network", proxied("10.0.0.5:7400", "10.0.0.0/8"), ""},
		{"a host name of the network", proxied("proxy-side.lan:7400", "10.0.0.0/8"), ""},
		{"a server that serves TLS", serverOptions{listen: "0.0.0.0:7400", names: []string{"ropeshift.lan"}}, ""},
		{"a server on loopback with no proxy", serverOptions{listen: "127.0.0.1:7400"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planTransport(tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if tt.warning == "" {
				if len(plan.warnings) != 0 {
					t.Errorf("the plan warns: %q", plan.warnings)
				}
				return
			}
			if len(plan.warnings) != 1 {
				t.Fatalf("the plan warns %q", plan.warnings)
			}
			for _, want := range []string{tt.warning, "--trusted-proxies", "ROPESHIFT_TRUSTED_PROXIES"} {
				if !strings.Contains(plan.warnings[0], want) {
					t.Errorf("the warning does not say %q: %s", want, plan.warnings[0])
				}
			}
		})
	}
}
