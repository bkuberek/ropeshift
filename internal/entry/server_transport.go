package entry

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/servertls"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ports"
)

// A group server's listener is secured one of four ways (ADR-0031). NFR-SEC-2 lets plaintext cross loopback and
// the private hop from a reverse proxy that terminates TLS, and nothing else. So a listener on any other address
// serves TLS, in a certificate the server makes from its own authority or in one a person supplies, or serves
// plaintext to the proxies it lists. A listener on loopback serves plaintext, unless a certificate is supplied or a
// name asks for the server's own.

const (
	// envTLS, envTLSCert, envTLSKey, envServerNames, envTrustedProxies, and envPublicURL are a container's way to
	// give the server what its flags give.
	envTLS            = "ROPESHIFT_TLS"
	envTLSCert        = "ROPESHIFT_TLS_CERT"
	envTLSKey         = "ROPESHIFT_TLS_KEY"
	envServerNames    = "ROPESHIFT_SERVER_NAMES"
	envTrustedProxies = "ROPESHIFT_TRUSTED_PROXIES"
	envPublicURL      = "ROPESHIFT_PUBLIC_URL"
	// envWorkerURL gives the URL the server's workers reach it at, which its join strings carry.
	envWorkerURL = "ROPESHIFT_WORKER_URL"
	// tlsDirectory holds the server's authority and its certificate, inside its state directory.
	tlsDirectory = "tls"
)

// transportMode is how a group server's listener is secured.
type transportMode string

const (
	// transportLoopback serves plaintext on this machine alone.
	transportLoopback transportMode = "loopback"
	// transportOwnAuthority serves a certificate the server's own authority issued (mode a).
	transportOwnAuthority transportMode = "own-authority"
	// transportCertificate serves a certificate and key a person supplies (mode b).
	transportCertificate transportMode = "certificate"
	// transportProxy serves plaintext to the reverse proxies it lists, which terminate TLS (mode c).
	transportProxy transportMode = "proxy"
)

// serverTransport is how the listener is secured, and what that needs.
type serverTransport struct {
	mode transportMode
	// proxies are the peers a plaintext listener takes connections from beside loopback, and reads forwarded
	// headers from, and publicURL is the origin their clients' browsers use, in the form the web UI compares with.
	proxies   *server.Proxies
	publicURL string
	// names are what the server's own certificate covers.
	names []string
	// certFile and keyFile are the supplied certificate and its key.
	certFile, keyFile string
	// workerURL is the URL --worker-url gave, checked and in the one form a worker keeps. It is empty when none was
	// given, and the listener then says where workers reach it by what it knows.
	workerURL string
	// warnings are what the plan allows and a person should read, which the server prints once it is listening.
	warnings []string
}

// planTransport decides how the listener is secured from its address and the certificate settings, and
// refuses a combination that leaves the transport unclear or plaintext off loopback. It touches nothing, so a
// refusal costs the machine nothing.
func planTransport(opts serverOptions) (serverTransport, error) {
	plan, err := planListener(opts)
	if err != nil {
		return serverTransport{}, err
	}
	if plan.workerURL, err = checkWorkerURL(opts.workerURL, plan); err != nil {
		return serverTransport{}, err
	}
	plan.warnings = loopbackWarnings(opts.listen, plan)
	return plan, nil
}

// checkWorkerURL checks the URL workers are to reach the server at against how the server is secured, so that no
// join string is made that fails at the worker's end: an address that is not the one the server's own certificate
// covers, or a scheme the listener does not speak. Empty is no URL given.
func checkWorkerURL(given string, plan serverTransport) (string, error) {
	if given == "" {
		return "", nil
	}
	checked, err := api.CheckWorkerURL(given)
	if err != nil {
		return "", fmt.Errorf("--worker-url (%s): %w", envWorkerURL, err)
	}
	parsed, err := url.Parse(checked)
	if err != nil {
		return "", fmt.Errorf("--worker-url (%s) is no URL", envWorkerURL)
	}
	secure := parsed.Scheme == "https"
	switch plan.mode {
	case transportOwnAuthority, transportCertificate:
		if !secure {
			return "", fmt.Errorf("--worker-url (%s) is an https:// URL, because this server serves TLS", envWorkerURL)
		}
	case transportLoopback:
		if secure {
			return "", fmt.Errorf("--worker-url (%s) is an http:// URL, because this server serves plaintext on its own machine: workers reach a server over https:// behind a reverse proxy that terminates TLS, which is --tls off with --trusted-proxies and --public-url", envWorkerURL)
		}
	}
	if plan.mode == transportOwnAuthority {
		host := parsed.Hostname()
		if ip, err := netip.ParseAddr(host); err == nil {
			host = ip.String()
		}
		if !slices.Contains(plan.names, host) {
			return "", fmt.Errorf("--worker-url (%s) reaches the server at the host %q, which the certificate this server makes does not cover, so no worker could verify it: use one of the names --name gives (%s), or add the host to --name", envWorkerURL, host, strings.Join(plan.names, ", "))
		}
	}
	return checked, nil
}

// loopbackWarnings says what a proxy on this machine would meet. A listener that loopback reaches and that does not
// list the loopback address is listing no proxy that connects from this machine, and the server then believes none
// of what a proxy on this machine forwards: every client is one address, which the limit on wrong guesses counts as
// one. A proxy on another machine is right to leave loopback out, so this is a warning.
func loopbackWarnings(listen string, plan serverTransport) []string {
	if plan.mode != transportProxy {
		return nil
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return nil
	}
	reaches := netip.MustParseAddr("127.0.0.1")
	switch ip, err := netip.ParseAddr(host); {
	case host == "" || host == "localhost":
	case err != nil:
		// A host name of the network is no address loopback reaches.
		return nil
	case ip.IsUnspecified() || ip.IsLoopback():
		if ip.Is6() && !ip.IsUnspecified() {
			reaches = ip
		}
	default:
		return nil
	}
	if plan.proxies.Contains(reaches) {
		return nil
	}
	return []string{fmt.Sprintf("this listener is reachable from this machine, and --trusted-proxies (%s) does not list %s: a reverse proxy on this machine connects from %s, so the server would read nothing it forwards, and every member would sign in from one address and share one limit on wrong guesses; list %s if the proxy is on this machine, and leave it out if it is on another", envTrustedProxies, reaches, reaches, reaches)}
}

// planListener is the plan of the listener's own security.
func planListener(opts serverOptions) (serverTransport, error) {
	if _, _, err := net.SplitHostPort(opts.listen); err != nil {
		return serverTransport{}, fmt.Errorf("--listen %q: %w", opts.listen, err)
	}
	switch opts.tls {
	case "", "on":
	case "off":
		return planProxy(opts)
	default:
		return serverTransport{}, fmt.Errorf("--tls (%s) is on or off, and is %q", envTLS, opts.tls)
	}
	switch {
	case len(opts.trustedProxies) > 0:
		return serverTransport{}, fmt.Errorf("--trusted-proxies (%s) names the proxies of a server that serves plaintext, which is --tls off: a server that serves TLS takes no proxy's word for where a request came from", envTrustedProxies)
	case opts.publicURL != "":
		return serverTransport{}, fmt.Errorf("--public-url (%s) is the origin members reach a server behind a proxy at, which is --tls off with --trusted-proxies", envPublicURL)
	}
	switch {
	case (opts.tlsCert == "") != (opts.tlsKey == ""):
		missing := "--tls-key"
		if opts.tlsCert == "" {
			missing = "--tls-cert"
		}
		return serverTransport{}, fmt.Errorf("a supplied certificate comes with its key: give %s too (%s and %s)", missing, envTLSCert, envTLSKey)
	case opts.tlsCert != "":
		if len(opts.names) > 0 {
			return serverTransport{}, errors.New("--name names the certificate the server makes, and --tls-cert supplies one: give one or the other")
		}
		return serverTransport{mode: transportCertificate, certFile: opts.tlsCert, keyFile: opts.tlsKey}, nil
	case len(opts.names) > 0:
		names, err := servertls.CleanNames(opts.names)
		if err != nil {
			return serverTransport{}, fmt.Errorf("--name: %w", err)
		}
		return serverTransport{mode: transportOwnAuthority, names: names}, nil
	case server.Loopback(opts.listen):
		return serverTransport{mode: transportLoopback}, nil
	}
	return serverTransport{}, fmt.Errorf("a server off loopback serves TLS, and the certificate it makes needs a name: set --name (%s) to the names members and workers reach it by, or supply a certificate with --tls-cert and --tls-key", envServerNames)
}

// planProxy plans a server that serves plaintext (--tls off). On loopback with nothing else that is the plaintext
// of a server on its own machine. Otherwise it is mode (c): the server takes plaintext from the proxies it lists,
// which terminate TLS, and members' browsers reach the proxy at the public URL. It refuses to start without both,
// because a server that takes plaintext off loopback from proxies it does not know, or serves pages to a host it
// was never told, is the mistake this setting exists to stop.
func planProxy(opts serverOptions) (serverTransport, error) {
	switch {
	case opts.tlsCert != "" || opts.tlsKey != "":
		return serverTransport{}, errors.New("--tls off serves no certificate, and --tls-cert and --tls-key supply one: give one or the other")
	case len(opts.names) > 0:
		return serverTransport{}, errors.New("--tls off serves no certificate, and --name names the one the server makes: give one or the other")
	}
	proxies, err := server.ParseProxies(opts.trustedProxies)
	if err != nil {
		return serverTransport{}, fmt.Errorf("--trusted-proxies (%s): %w", envTrustedProxies, err)
	}
	switch {
	case proxies.Len() == 0 && opts.publicURL == "" && server.Loopback(opts.listen):
		return serverTransport{mode: transportLoopback}, nil
	case proxies.Len() == 0 && opts.publicURL == "":
		return serverTransport{}, fmt.Errorf("a server with TLS off takes plaintext off loopback only from the reverse proxies it lists, and says where members reach them: set --trusted-proxies (%s) and --public-url (%s)", envTrustedProxies, envPublicURL)
	case proxies.Len() == 0:
		return serverTransport{}, fmt.Errorf("--public-url is where members reach the reverse proxy in front of this server, and the server takes plaintext only from a proxy it lists: set --trusted-proxies (%s)", envTrustedProxies)
	case opts.publicURL == "":
		return serverTransport{}, fmt.Errorf("a server behind a reverse proxy says where members reach it, for the pages it serves and the cookies it sets: set --public-url (%s)", envPublicURL)
	}
	origin, err := server.CheckPublicURL(opts.publicURL)
	if err != nil {
		return serverTransport{}, fmt.Errorf("--public-url (%s): %w", envPublicURL, err)
	}
	if !strings.HasPrefix(origin, "https://") {
		return serverTransport{}, fmt.Errorf("--public-url (%s) is an https:// origin, because members reach a proxy that terminates TLS over https", envPublicURL)
	}
	return serverTransport{mode: transportProxy, proxies: proxies, publicURL: origin}, nil
}

// serverListener is the one listener of a group server and what it serves.
type serverListener struct {
	net.Listener
	mode transportMode
	// fingerprint is the SHA-256 of the server's own authority's certificate in lowercase hex, which a client
	// pins. It is empty outside mode (a).
	fingerprint string
	// authorityFile is where that certificate is, for a person to trust on a device.
	authorityFile string
	// publicURL is the origin members reach a server behind a proxy at. It is empty otherwise.
	publicURL string
	// names are what the server's own certificate covers, and given is the worker URL --worker-url gave.
	names []string
	given string
}

// workerURL is where the server's workers reach it, which its join strings carry. A URL that was given is that URL.
// Otherwise it is what the listener implies: a server of its own authority is reached by its first name at the port
// it bound, a server behind a proxy by its public URL, and a server on loopback by the address it bound. A server
// serving a supplied certificate knows none of its certificate's names, so it has no URL until it is told one, and
// the empty string says so.
func (l *serverListener) workerURL() string {
	if l.given != "" {
		return l.given
	}
	switch l.mode {
	case transportOwnAuthority:
		if len(l.names) == 0 || l.Listener == nil {
			return ""
		}
		_, port, err := net.SplitHostPort(l.Addr().String())
		if err != nil {
			return ""
		}
		return "https://" + net.JoinHostPort(l.names[0], port)
	case transportProxy:
		reached, err := api.CheckWorkerURL(l.publicURL)
		if err != nil {
			return ""
		}
		return reached
	case transportLoopback:
		if l.Listener == nil {
			return ""
		}
		return "http://" + l.Addr().String()
	}
	return ""
}

// announce says what the server serves and where its workers reach it. It never says the fingerprint of the
// server's authority: that is the trust anchor of a join string, which a member's `worker token` makes, and a
// server's output is a log. The authority's file is named, which is what a person trusts on a device. A server that
// knows no URL for its workers warns on its error stream, and says what to set.
func announce(stdout, stderr io.Writer, socket string, l *serverListener) {
	fmt.Fprintf(stdout, "ropeshiftd: serving %s\n", socket)
	if l.Listener != nil {
		fmt.Fprintf(stdout, "ropeshiftd: server %s://%s\n", l.scheme(), l.Addr())
	}
	if l.publicURL != "" {
		fmt.Fprintf(stdout, "ropeshiftd: public url %s\n", l.publicURL)
	}
	if l.authorityFile != "" {
		fmt.Fprintf(stdout, "ropeshiftd: certificate authority %s\n", l.authorityFile)
	}
	if workers := l.workerURL(); workers != "" {
		fmt.Fprintf(stdout, "ropeshiftd: worker url %s\n", workers)
		return
	}
	fmt.Fprintf(stderr, "ropeshiftd: warning: this server does not know the URL its workers reach it at, so it makes no join strings: set --worker-url (%s)\n", envWorkerURL)
}

// listen opens the listener the plan describes. A plan for plaintext binds loopback and nothing else, whatever
// the address says. The authority of mode (a) lives in the state directory, and its time comes from the clock.
// warn hears what leaves the server running, such as a renewal that could not be kept.
func (t serverTransport) listen(address, stateDir string, clock ports.Clock, warn func(error)) (*serverListener, error) {
	opened := &serverListener{mode: t.mode}
	var config *tls.Config
	switch t.mode {
	case transportOwnAuthority:
		dir := filepath.Join(stateDir, tlsDirectory)
		authority, err := servertls.OpenAuthority(dir, servertls.Options{Names: t.names, Clock: clock, Warn: warn})
		if err != nil {
			return nil, err
		}
		config, opened.fingerprint, opened.authorityFile = authority.TLSConfig(), authority.Fingerprint(), filepath.Join(dir, servertls.CAFile)
	case transportCertificate:
		pair, err := servertls.OpenPair(t.certFile, t.keyFile, warn)
		if err != nil {
			return nil, err
		}
		config = pair.TLSConfig()
	}
	listener, err := server.ListenNetwork(address, server.Transport{TLS: config, Proxies: t.proxies})
	if err != nil {
		return nil, err
	}
	opened.Listener, opened.publicURL, opened.names, opened.given = listener, t.publicURL, t.names, t.workerURL
	return opened, nil
}

// scheme is the scheme of the address the listener serves: plaintext on loopback and to a proxy, TLS otherwise.
func (l *serverListener) scheme() string {
	if l.mode == transportLoopback || l.mode == transportProxy {
		return "http"
	}
	return "https"
}

// webAddress is the address the server's health reports for its web UI, which a command on the server's
// machine builds a link from with an http:// scheme. A server that serves TLS reports none.
func (l *serverListener) webAddress() string {
	if l.mode != transportLoopback {
		return ""
	}
	return l.Addr().String()
}
