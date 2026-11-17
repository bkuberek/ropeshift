package apitest

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// PinnedTLS is the TLS config of a client that trusts one certificate authority, named by the SHA-256
// fingerprint of its certificate. The chain a server sends must hold that certificate, and the server's own
// certificate must chain to it and cover host. It stands in for a worker's pin in tests, which verify nothing
// through the system roots.
func PinnedTLS(fingerprint, host string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// The chain is verified in VerifyConnection, against the pinned certificate alone.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("the server sent no certificate")
			}
			var pinned *x509.Certificate
			for _, cert := range state.PeerCertificates[1:] {
				sum := sha256.Sum256(cert.Raw)
				if hex.EncodeToString(sum[:]) == fingerprint {
					pinned = cert
				}
			}
			if pinned == nil {
				return errors.New("no certificate the server sent matches the pinned fingerprint")
			}
			roots := x509.NewCertPool()
			roots.AddCert(pinned)
			_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, DNSName: host, CurrentTime: time.Now()})
			if err != nil {
				return fmt.Errorf("the server's certificate does not chain to the pinned authority for %s: %w", host, err)
			}
			return nil
		},
	}
}
