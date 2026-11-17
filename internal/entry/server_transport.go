package entry

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/adapters/servertls"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ports"
)

// A group server's listener is secured one of three ways (ADR-0031). NFR-SEC-2 lets plaintext cross loopback
// and nothing else, so a listener on any other address serves TLS, in a certificate the server makes from its
// own authority or in one a person supplies. A listener on loopback serves plaintext, unless a certificate is
// supplied or a name asks for the server's own.

const (
	// envTLSCert, envTLSKey, and envServerNames are a container's way to give the server what its flags give.
	envTLSCert     = "ROPESHIFT_TLS_CERT"
	envTLSKey      = "ROPESHIFT_TLS_KEY"
	envServerNames = "ROPESHIFT_SERVER_NAMES"
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
)

// serverTransport is how the listener is secured, and what that needs.
type serverTransport struct {
	mode transportMode
	// names are what the server's own certificate covers.
	names []string
	// certFile and keyFile are the supplied certificate and its key.
	certFile, keyFile string
}

// planTransport decides how the listener is secured from its address and the certificate settings, and
// refuses a combination that leaves the transport unclear or plaintext off loopback. It touches nothing, so a
// refusal costs the machine nothing.
func planTransport(opts serverOptions) (serverTransport, error) {
	if _, _, err := net.SplitHostPort(opts.listen); err != nil {
		return serverTransport{}, fmt.Errorf("--listen %q: %w", opts.listen, err)
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

// serverListener is the one listener of a group server and what it serves.
type serverListener struct {
	net.Listener
	mode transportMode
	// fingerprint is the SHA-256 of the server's own authority's certificate in lowercase hex, which a client
	// pins. It is empty outside mode (a).
	fingerprint string
	// authorityFile is where that certificate is, for a person to trust on a device.
	authorityFile string
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
	listener, err := server.ListenNetwork(address, config)
	if err != nil {
		return nil, err
	}
	opened.Listener = listener
	return opened, nil
}

// scheme is the scheme of the address the listener serves.
func (l *serverListener) scheme() string {
	if l.mode == transportLoopback {
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
