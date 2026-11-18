package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
)

// ServerLink is how a worker reaches the server it joined and decides whether to trust it (ADR-0031).
type ServerLink struct {
	// URL is the worker URL of the join string: an https:// URL, or an http:// URL of a loopback address.
	URL string
	// CASHA256 is the SHA-256 of the server's certificate authority in hexadecimal. With one, the worker trusts the
	// server's certificate when it chains to that authority and covers the host name of the URL, and the system's
	// roots count for nothing. Without one, it verifies through Roots.
	CASHA256 string
	// Roots is what a link with no fingerprint verifies through. Nil is the system's roots, and a test gives its own.
	Roots *x509.CertPool
}

// NewServerClient returns the client of a worker's link to its server, which speaks the API as every client does,
// follows no redirect, and reads no proxy from the environment. It refuses a URL a worker may not reach and a
// fingerprint that does not fit it, with a sentence that repeats neither.
func NewServerClient(link ServerLink) (*Client, error) {
	switch {
	case strings.HasPrefix(strings.ToLower(link.URL), "http://"):
		if link.CASHA256 != "" {
			return nil, errors.New("a fingerprint pins a certificate authority, and an http:// URL has none")
		}
		return NewTCPClient(link.URL)
	case strings.HasPrefix(strings.ToLower(link.URL), "https://"):
		base, err := SecureServerAddress(link.URL)
		if err != nil {
			return nil, err
		}
		if link.CASHA256 == "" {
			return NewTLSClient(base, &tls.Config{RootCAs: link.Roots, MinVersion: tls.VersionTLS12})
		}
		raw, err := hex.DecodeString(link.CASHA256)
		if err != nil || len(raw) != sha256.Size {
			return nil, errors.New("the fingerprint is not the SHA-256 of a certificate authority in hexadecimal")
		}
		parsed, err := url.Parse(base)
		if err != nil {
			return nil, errors.New("the server address is not a URL")
		}
		return NewTLSClient(base, PinnedTLS(hex.EncodeToString(raw), parsed.Hostname()))
	}
	return nil, errors.New("the server address is an https:// URL, or an http:// URL of a loopback address")
}

// ErrPinRefused says the server's certificate does not chain to the certificate authority a worker pinned: the
// server sent no authority, sent one whose fingerprint is not the pin, or sent a certificate that authority did not
// sign. It names neither the pin nor what the server sent.
var ErrPinRefused = errors.New("the server's certificate does not chain to the certificate authority this worker pinned")

// PinnedTLS is the TLS config of a client that trusts one certificate authority, named by the SHA-256 fingerprint
// of its certificate, and no root of the system's. The server sends its certificate and the authority's, and the
// client finds the certificate in the chain whose fingerprint is the pin, and verifies the server's certificate
// against that one alone, for the host name and the time. The authority's certificate is public, so what proves
// the server is that the pinned authority signed its certificate, and a chain that holds the pinned certificate
// beside another's leaf proves nothing.
func PinnedTLS(fingerprint, host string) *tls.Config {
	want := []byte(strings.ToLower(fingerprint))
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// The chain is verified in VerifyConnection, against the pinned certificate alone.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) < 2 {
				return ErrPinRefused
			}
			var pinned *x509.Certificate
			for _, cert := range state.PeerCertificates[1:] {
				sum := sha256.Sum256(cert.Raw)
				if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), want) == 1 {
					pinned = cert
					break
				}
			}
			if pinned == nil {
				return ErrPinRefused
			}
			authority, others := x509.NewCertPool(), x509.NewCertPool()
			authority.AddCert(pinned)
			for _, cert := range state.PeerCertificates[1:] {
				if cert != pinned {
					others.AddCert(cert)
				}
			}
			_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
				Roots: authority, Intermediates: others, DNSName: host, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			})
			var name x509.HostnameError
			var invalid x509.CertificateInvalidError
			switch {
			case err == nil:
				return nil
			case errors.As(err, &name), errors.As(err, &invalid):
				return err
			}
			return ErrPinRefused
		},
	}
}

// TrustFailure says in a sentence why a server was not trusted, when that is why a call to it failed: the pin did
// not hold, the system's roots do not vouch for the certificate, the certificate does not cover the host name the
// worker was given, or it has ended. The sentence names no fingerprint.
func TrustFailure(err error) (string, bool) {
	var name x509.HostnameError
	var invalid x509.CertificateInvalidError
	var unknown x509.UnknownAuthorityError
	var refused *tls.CertificateVerificationError
	switch {
	case errors.Is(err, ErrPinRefused):
		return ErrPinRefused.Error(), true
	case errors.As(err, &name):
		return "the server's certificate does not cover the host name " + name.Host, true
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return "the server's certificate has expired, or is not yet valid", true
		}
		return "the server's certificate is not valid for a server", true
	case errors.As(err, &unknown), errors.As(err, &refused):
		// The verifier of macOS refuses a chain it does not trust with an error of its own text rather than an
		// x509.UnknownAuthorityError, so a verification that failed with no class above is the roots' refusal.
		return "the server's certificate is not trusted by this machine's system roots", true
	}
	return "", false
}
