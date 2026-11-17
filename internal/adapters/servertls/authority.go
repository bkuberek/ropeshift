// Package servertls makes the certificates a group server's TLS listener presents (ADR-0031): its own
// certificate authority with the server certificate that authority issues, and a certificate and key a
// person supplies, which are read again when their files change.
package servertls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

// The files of an authority's directory, and how long what it makes lasts.
const (
	CAFile    = "ca.crt"
	CAKeyFile = "ca.key"
	CertFile  = "server.crt"
	KeyFile   = "server.key"

	// CAValidityYears is how long the authority lasts. CertValidity is how long a server certificate lasts, and
	// RenewBefore how long before its end the server issues the next one.
	CAValidityYears = 10
	CertValidity    = 90 * 24 * time.Hour
	RenewBefore     = 30 * 24 * time.Hour

	// skew is how long before it is issued a certificate begins, so a machine whose clock runs behind accepts it.
	skew = 5 * time.Minute
	// renewRetry is how long a renewal that failed waits before it is tried again.
	renewRetry = time.Minute

	privateDir  = 0o700
	privateFile = 0o600
	publicFile  = 0o644
)

// Options say what an authority issues for and how it reads the time.
type Options struct {
	// Names are the host names and addresses the server certificate covers.
	Names []string
	Clock ports.Clock
	// Warn hears a problem that leaves the server running, such as a renewal that could not be written.
	Warn func(error)
}

// Authority is a server's own certificate authority and the certificate it issues the server.
type Authority struct {
	dir     string
	names   []string
	clock   ports.Clock
	report  reporter
	ca      *x509.Certificate
	caDER   []byte
	caKey   *ecdsa.PrivateKey
	finger  string
	mu      sync.RWMutex
	leaf    *tls.Certificate
	renewAt time.Time
	// failure is why the last renewal failed, which a connection gets once the certificate has ended.
	failure error
}

// OpenAuthority returns the authority kept in a directory, and makes it, with a server certificate, when the
// directory holds none. A certificate the directory holds is kept while it covers the names and has more than
// RenewBefore left.
func OpenAuthority(dir string, options Options) (*Authority, error) {
	names, err := CleanNames(options.Names)
	if err != nil {
		return nil, err
	}
	if options.Clock == nil {
		return nil, errors.New("a certificate authority needs a clock")
	}
	if err := os.MkdirAll(dir, privateDir); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, privateDir); err != nil {
		return nil, err
	}
	a := &Authority{dir: dir, names: names, clock: options.Clock, report: reporter{warn: options.Warn}}
	now := a.clock.Now()
	if err := a.openCA(now); err != nil {
		return nil, err
	}
	leaf := a.keptLeaf(now)
	if leaf == nil {
		if leaf, err = a.issue(now); err != nil {
			return nil, err
		}
	}
	a.setLeaf(leaf, now)
	return a, nil
}

// TLSConfig is what a listener serves: the server certificate, issued again when it nears its end.
func (a *Authority) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}, GetCertificate: a.certificate}
}

// Fingerprint is the SHA-256 of the authority's certificate in lowercase hex, which a client pins.
func (a *Authority) Fingerprint() string { return a.finger }

// CertificatePEM is the authority's certificate, which a person trusts on a device to stop its warning.
func (a *Authority) CertificatePEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.caDER})
}

func (a *Authority) path(name string) string { return filepath.Join(a.dir, name) }

// setLeaf keeps a certificate as the one in use. The next renewal is RenewBefore ahead of its end, and no sooner
// than renewRetry from now, so a certificate that is the last its authority can sign is not issued again by
// every handshake.
func (a *Authority) setLeaf(leaf *tls.Certificate, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.leaf, a.failure = leaf, nil
	a.renewAt = later(leaf.Leaf.NotAfter.Add(-RenewBefore), now.Add(renewRetry))
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// certificate answers a connection with the current server certificate, issuing the next first when the
// current one has RenewBefore left or less. A renewal that cannot be kept leaves the certificate in use, which
// serves until its end, and is tried again no sooner than renewRetry later, because each try makes a key. Once the
// certificate has ended, the connection gets the error of the renewal that failed.
func (a *Authority) certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	now := a.clock.Now()
	a.mu.RLock()
	leaf, renewAt := a.leaf, a.renewAt
	a.mu.RUnlock()
	if now.Before(renewAt) && now.Before(leaf.Leaf.NotAfter) {
		return leaf, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	live := now.Before(a.leaf.Leaf.NotAfter)
	switch {
	case now.Before(a.renewAt) && live:
		return a.leaf, nil
	case now.Before(a.renewAt) && a.failure != nil:
		// The certificate ended, and the renewal that was tried since failed: its error is the answer until the
		// next try. A certificate that ended with no renewal tried is renewed now, and answers with that error if
		// it cannot be.
		return nil, a.failure
	}
	renewed, err := a.renew(now)
	if err != nil {
		a.failure = fmt.Errorf("the server certificate could not be renewed: %w", err)
		a.renewAt = now.Add(renewRetry)
		if live {
			a.report.say(a.failure)
			return a.leaf, nil
		}
		return nil, a.failure
	}
	a.report.clear()
	a.leaf, a.failure = renewed, nil
	a.renewAt = later(renewed.Leaf.NotAfter.Add(-RenewBefore), now.Add(renewRetry))
	return a.leaf, nil
}

// renew issues the next server certificate, from an authority that has not ended.
func (a *Authority) renew(now time.Time) (*tls.Certificate, error) {
	if !now.Before(a.ca.NotAfter) {
		return nil, a.expired()
	}
	return a.issue(now)
}

func (a *Authority) expired() error {
	return fmt.Errorf("the certificate authority %s expired on %s: remove %s and %s to make a new authority, and every client pins the new one", a.path(CAFile), a.ca.NotAfter.UTC().Format(time.DateOnly), CAFile, CAKeyFile)
}

// tighten makes a key readable by its user alone, and says so when it was readable by more, because whoever
// could read it may have.
func (a *Authority) tighten(name string) error {
	info, err := os.Stat(a.path(name))
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		a.report.say(fmt.Errorf("the key %s was readable by more than its user (mode %o), and is now readable by its user alone: whoever could read it may have", a.path(name), info.Mode().Perm()))
	}
	return os.Chmod(a.path(name), privateFile)
}

// openCA reads the authority, and makes it when no certificate is kept. The certificate is the last file of a
// new authority, so a key with no certificate is an authority that was never finished, and nothing was issued
// from it. A certificate with no key is an authority that lost its key, which only a person replaces, because
// every worker pins it.
func (a *Authority) openCA(now time.Time) error {
	raw, err := os.ReadFile(a.path(CAFile))
	if errors.Is(err, fs.ErrNotExist) {
		return a.makeCA(now)
	}
	if err != nil {
		return err
	}
	ca, err := parseCertificate(raw)
	if err != nil {
		return fmt.Errorf("the certificate authority %s: %w", a.path(CAFile), err)
	}
	key, err := readKey(a.path(CAKeyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the certificate authority %s has no key at %s: remove both to make a new authority, and every worker joins again", a.path(CAFile), a.path(CAKeyFile))
	}
	if err != nil {
		return fmt.Errorf("the certificate authority's key %s: %w", a.path(CAKeyFile), err)
	}
	a.ca, a.caDER, a.caKey = ca, ca.Raw, key
	a.finger = fingerprint(ca.Raw)
	switch {
	case !ca.IsCA:
		return fmt.Errorf("the certificate authority %s is not an authority", a.path(CAFile))
	case !key.PublicKey.Equal(ca.PublicKey):
		return fmt.Errorf("the key %s does not belong to the certificate authority %s", a.path(CAKeyFile), a.path(CAFile))
	case !now.Before(ca.NotAfter):
		return a.expired()
	}
	if err := a.tighten(CAKeyFile); err != nil {
		return err
	}
	if ca.NotAfter.Sub(now) < RenewBefore {
		a.report.say(fmt.Errorf("the certificate authority %s ends on %s: before then, remove %s and %s to make a new authority, and every client pins the new one", a.path(CAFile), ca.NotAfter.UTC().Format(time.DateOnly), CAFile, CAKeyFile))
	}
	return nil
}

func (a *Authority) makeCA(now time.Time) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := newSerial()
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "ropeshift server certificate authority"},
		NotBefore: now.Add(-skew), NotAfter: now.AddDate(CAValidityYears, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	if err := writeKey(a.path(CAKeyFile), key); err != nil {
		return err
	}
	if err := writeFile(a.path(CAFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), publicFile); err != nil {
		return err
	}
	a.ca, a.caDER, a.caKey = ca, der, key
	a.finger = fingerprint(der)
	return nil
}

// keptLeaf returns the server certificate the directory holds when this authority issued it, it covers exactly
// the names, and it has more than RenewBefore left. It is nil otherwise, and the caller issues one, because
// nothing here is worth keeping that cannot be made again. Its key is tightened either way.
func (a *Authority) keptLeaf(now time.Time) *tls.Certificate {
	if err := a.tighten(KeyFile); err != nil {
		return nil
	}
	cert, err := tls.LoadX509KeyPair(a.path(CertFile), a.path(KeyFile))
	if err != nil || cert.Leaf == nil {
		return nil
	}
	leaf := cert.Leaf
	if leaf.CheckSignatureFrom(a.ca) != nil || !sameNames(leaf, a.names) || !now.Before(leaf.NotAfter.Add(-RenewBefore)) {
		return nil
	}
	cert.Certificate = append(cert.Certificate[:1], a.caDER)
	return &cert
}

// issue makes a server certificate for the names, and keeps it and its key in the directory.
func (a *Authority) issue(now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: a.names[0]},
		// A certificate never outlives the authority that signed it.
		NotBefore: now.Add(-skew), NotAfter: earlier(now.Add(CertValidity), a.ca.NotAfter),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, name := range a.names {
		if ip := net.ParseIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.ca, &key.PublicKey, a.caKey)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	if err := writeKey(a.path(KeyFile), key); err != nil {
		return nil, err
	}
	if err := writeFile(a.path(CertFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), publicFile); err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, a.caDER}, PrivateKey: key, Leaf: leaf}, nil
}

var hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// CleanNames lowercases and de-duplicates the names a certificate covers, and refuses one that is neither a
// host name nor an address, or a list with no name in it.
func CleanNames(given []string) ([]string, error) {
	var names []string
	for _, name := range given {
		name = strings.ToLower(strings.TrimSpace(name))
		if ip := net.ParseIP(name); ip != nil {
			name = ip.String()
		} else if len(name) > 253 || !hostName.MatchString(name) {
			return nil, fmt.Errorf("%q is neither a host name nor an address, which a certificate name is, with no port and no scheme", name)
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("a server certificate needs at least one name")
	}
	return names, nil
}

// sameNames says a certificate covers exactly the names.
func sameNames(leaf *x509.Certificate, names []string) bool {
	var covered []string
	covered = append(covered, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		covered = append(covered, ip.String())
	}
	if len(covered) != len(names) {
		return false
	}
	slices.Sort(covered)
	return slices.Equal(covered, slices.Sorted(slices.Values(names)))
}

func newSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func parseCertificate(raw []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("holds no certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func readKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("holds no private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("holds a key that is not an ECDSA key")
	}
	return key, nil
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), privateFile)
}

// writeFile replaces a file whole. The new one is made with the mode a key needs, so no moment leaves it wider,
// and renamed over the old, so a start that stops in the middle leaves the old file or the new one.
func writeFile(path string, data []byte, perm os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := temp.Name()
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Chmod(perm)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closed := temp.Close(); err == nil {
		err = closed
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// reporter tells a problem once for as long as it stays the same, so a certificate that cannot be renewed or
// read does not fill a log with a line for every connection.
type reporter struct {
	warn func(error)
	mu   sync.Mutex
	last string
}

func (r *reporter) say(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.warn == nil || err.Error() == r.last {
		return
	}
	r.last = err.Error()
	r.warn(err)
}

func (r *reporter) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = ""
}
