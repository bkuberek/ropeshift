package servertls_test

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/servertls"
)

// start is a round second, because a certificate's times have no finer.
var start = time.Date(2030, time.March, 1, 12, 0, 0, 0, time.UTC)

func open(t *testing.T, dir string, clock *fake.Clock, names ...string) *servertls.Authority {
	t.Helper()
	authority, err := servertls.OpenAuthority(dir, servertls.Options{Names: names, Clock: clock, Warn: func(err error) { t.Errorf("warned: %v", err) }})
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

// served is the certificate the listener would present now.
func served(t *testing.T, config *tls.Config) *x509.Certificate {
	t.Helper()
	cert, err := config.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func poolOf(t *testing.T, pem []byte) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("no certificate in the PEM")
	}
	return pool
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestTheAuthorityIsMadeAtFirstStartAndItsKeysAreReadableOnlyByItsUser(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	authority := open(t, dir, fake.NewClock(start), "ropeshift.lan")
	if got := mode(t, dir); got != 0o700 {
		t.Errorf("the directory is %o", got)
	}
	for _, name := range []string{servertls.CAKeyFile, servertls.KeyFile} {
		if got := mode(t, filepath.Join(dir, name)); got != 0o600 {
			t.Errorf("%s is %o", name, got)
		}
	}
	for _, name := range []string{servertls.CAFile, servertls.CertFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The authority is valid for ten years, and says it signs certificates and no authority below it.
	raw, err := os.ReadFile(filepath.Join(dir, servertls.CAFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(authority.CertificatePEM()) {
		t.Error("the file and CertificatePEM differ")
	}
	ca := certificateOf(t, raw)
	if !ca.IsCA || !ca.MaxPathLenZero || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Errorf("the authority is not an authority that signs certificates and no authority below it: %+v", ca)
	}
	if got := ca.NotAfter; !got.Equal(start.AddDate(10, 0, 0)) {
		t.Errorf("the authority ends %s", got)
	}
	sum := sha256.Sum256(ca.Raw)
	if authority.Fingerprint() != hex.EncodeToString(sum[:]) {
		t.Errorf("the fingerprint is %s", authority.Fingerprint())
	}
}

func certificateOf(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, rest := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatalf("the PEM is not one certificate: %q", pemBytes)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestAKeyFileWiderThanItsUserIsTightenedAtStart(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	open(t, dir, clock, "ropeshift.lan")
	for _, name := range []string{servertls.CAKeyFile, servertls.KeyFile} {
		if err := os.Chmod(filepath.Join(dir, name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	open(t, dir, clock, "ropeshift.lan")
	for _, name := range []string{servertls.CAKeyFile, servertls.KeyFile} {
		if got := mode(t, filepath.Join(dir, name)); got != 0o600 {
			t.Errorf("%s is %o", name, got)
		}
	}
	if got := mode(t, dir); got != 0o700 {
		t.Errorf("the directory is %o", got)
	}
}

func TestARestartKeepsTheAuthorityAndTheCertificateItHolds(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	first := open(t, dir, clock, "ropeshift.lan")
	issued := served(t, first.TLSConfig())
	clock.Advance(24 * time.Hour)
	second := open(t, dir, clock, "ropeshift.lan")
	if first.Fingerprint() != second.Fingerprint() {
		t.Error("a restart made a new authority")
	}
	if got := served(t, second.TLSConfig()); got.SerialNumber.Cmp(issued.SerialNumber) != 0 {
		t.Error("a restart issued a new certificate with 89 days left on the one it held")
	}
}

func TestTheServerCertificateCoversEveryNameItIsGiven(t *testing.T) {
	t.Parallel()
	names := []string{"ropeshift.lan", "Mixed.Case.Lan", "192.168.1.5", "::1", "ropeshift.tailnet.example"}
	authority := open(t, filepath.Join(t.TempDir(), "tls"), fake.NewClock(start), names...)
	leaf := served(t, authority.TLSConfig())
	roots := poolOf(t, authority.CertificatePEM())
	for _, name := range append(names[:len(names):len(names)], "mixed.case.lan") {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, CurrentTime: start}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, other := range []string{"other.lan", "192.168.1.6", "ropeshift.lan.evil.example"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: other, CurrentTime: start}); err == nil {
			t.Errorf("the certificate covers %s", other)
		}
	}
	if leaf.IsCA || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("the certificate is not a server's alone: %+v", leaf.ExtKeyUsage)
	}
}

// A certificate is issued for ninety days, and the server issues the next with thirty left, with no restart.
func TestTheServerCertificateLastsNinetyDaysAndIsRenewedWithThirtyLeft(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	authority := open(t, dir, clock, "ropeshift.lan")
	config := authority.TLSConfig()
	first := served(t, config)
	if got := first.NotAfter; !got.Equal(start.Add(servertls.CertValidity)) || servertls.CertValidity != 90*24*time.Hour {
		t.Fatalf("the certificate ends %s", got)
	}

	clock.Advance(60*24*time.Hour - time.Minute)
	if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("the certificate was renewed with more than 30 days left")
	}
	clock.Advance(time.Minute)
	renewed := served(t, config)
	if renewed.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Fatal("the certificate was not renewed with 30 days left")
	}
	if got := renewed.NotAfter; !got.Equal(clock.Now().Add(servertls.CertValidity)) {
		t.Errorf("the renewed certificate ends %s", got)
	}
	roots := poolOf(t, authority.CertificatePEM())
	if _, err := renewed.Verify(x509.VerifyOptions{Roots: roots, DNSName: "ropeshift.lan", CurrentTime: clock.Now()}); err != nil {
		t.Errorf("the renewed certificate does not chain to the same authority: %v", err)
	}

	// What was renewed is what a restart finds, and one served again is not issued again.
	if again := served(t, config); again.SerialNumber.Cmp(renewed.SerialNumber) != 0 {
		t.Error("a certificate that was just renewed was renewed again")
	}
	restarted := open(t, dir, clock, "ropeshift.lan")
	if got := served(t, restarted.TLSConfig()); got.SerialNumber.Cmp(renewed.SerialNumber) != 0 {
		t.Error("the restart did not find the renewed certificate on disk")
	}
	if mode(t, filepath.Join(dir, servertls.KeyFile)) != 0o600 {
		t.Error("the renewed key is readable by more than its user")
	}
}

func TestACertificateWithThirtyDaysLeftIsIssuedAgainAtStart(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	first := served(t, open(t, dir, clock, "ropeshift.lan").TLSConfig())
	clock.Advance(servertls.CertValidity - servertls.RenewBefore)
	second := served(t, open(t, dir, clock, "ropeshift.lan").TLSConfig())
	if second.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Error("the restart kept a certificate with 30 days left")
	}
}

func TestACertificateForOtherNamesIsIssuedWhenTheNamesChange(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	first := open(t, dir, clock, "ropeshift.lan")
	second := open(t, dir, clock, "ropeshift.lan", "10.0.0.7")
	if first.Fingerprint() != second.Fingerprint() {
		t.Error("a change of names made a new authority")
	}
	leaf := served(t, second.TLSConfig())
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: poolOf(t, second.CertificatePEM()), DNSName: "10.0.0.7", CurrentTime: start}); err != nil {
		t.Errorf("the new name is not covered: %v", err)
	}
	third := open(t, dir, clock, "ropeshift.lan")
	if got := served(t, third.TLSConfig()); len(got.IPAddresses) != 0 {
		t.Errorf("a name that was dropped is still covered: %v", got.IPAddresses)
	}
}

func TestTheChainTheServerSendsEndsAtTheAuthorityAClientPins(t *testing.T) {
	t.Parallel()
	authority := open(t, filepath.Join(t.TempDir(), "tls"), fake.NewClock(start), "127.0.0.1", "localhost")
	config := authority.TLSConfig()
	if config.MinVersion < tls.VersionTLS12 {
		t.Errorf("the lowest TLS version is %x", config.MinVersion)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.(*tls.Conn).Handshake()
	}()
	// The test's authority is made at a fixed time, which the client's clock has left behind, so the client reads
	// the chain and verifies nothing.
	client, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	chain := client.ConnectionState().PeerCertificates
	if len(chain) != 2 {
		t.Fatalf("the server sent %d certificates", len(chain))
	}
	sum := sha256.Sum256(chain[1].Raw)
	if hex.EncodeToString(sum[:]) != authority.Fingerprint() {
		t.Error("the second certificate is not the authority a client pins")
	}
	if chain[0].CheckSignatureFrom(chain[1]) != nil {
		t.Error("the authority did not sign the server's certificate")
	}
	if got := client.ConnectionState().NegotiatedProtocol; got != "http/1.1" {
		t.Errorf("the protocol is %q", got)
	}
}

func TestAnAuthorityPastItsEndIsRefusedAndNeverReplacedAlone(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	first := open(t, dir, clock, "ropeshift.lan")
	clock.Advance(11 * 365 * 24 * time.Hour)
	_, err := servertls.OpenAuthority(dir, servertls.Options{Names: []string{"ropeshift.lan"}, Clock: clock})
	if err == nil || !strings.Contains(err.Error(), "expired") || !strings.Contains(err.Error(), servertls.CAFile) {
		t.Fatalf("an expired authority answers %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, servertls.CAFile))
	if err != nil || string(raw) != string(first.CertificatePEM()) {
		t.Error("the expired authority was replaced")
	}
}

func TestAnAuthorityWhoseKeyIsGoneIsRefused(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	open(t, dir, clock, "ropeshift.lan")
	if err := os.Remove(filepath.Join(dir, servertls.CAKeyFile)); err != nil {
		t.Fatal(err)
	}
	_, err := servertls.OpenAuthority(dir, servertls.Options{Names: []string{"ropeshift.lan"}, Clock: clock})
	if err == nil || !strings.Contains(err.Error(), servertls.CAKeyFile) {
		t.Fatalf("an authority with no key answers %v", err)
	}
}

func TestAnAuthorityWhoseKeyDoesNotMatchItsCertificateIsRefused(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(start)
	dir := filepath.Join(t.TempDir(), "tls")
	other := filepath.Join(t.TempDir(), "tls")
	open(t, dir, clock, "ropeshift.lan")
	open(t, other, clock, "ropeshift.lan")
	raw, err := os.ReadFile(filepath.Join(other, servertls.CAKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, servertls.CAKeyFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := servertls.OpenAuthority(dir, servertls.Options{Names: []string{"ropeshift.lan"}, Clock: clock}); err == nil {
		t.Fatal("a key of another authority is taken")
	}
}

// The certificate is the last file of a new authority, so a start that ended before it leaves a key and
// no certificate, which nothing was ever issued from.
func TestAnAuthorityThatWasNeverFinishedIsMadeAgain(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	first := open(t, dir, clock, "ropeshift.lan")
	for _, name := range []string{servertls.CAFile, servertls.CertFile, servertls.KeyFile} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	second := open(t, dir, clock, "ropeshift.lan")
	if first.Fingerprint() == second.Fingerprint() {
		t.Error("the authority was not made again")
	}
}

func TestAnAuthorityNeedsANameAndAClock(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	for name, options := range map[string]servertls.Options{
		"no name":          {Clock: fake.NewClock(start)},
		"an empty name":    {Names: []string{" "}, Clock: fake.NewClock(start)},
		"no clock":         {Names: []string{"ropeshift.lan"}},
		"a name with port": {Names: []string{"ropeshift.lan:7400"}, Clock: fake.NewClock(start)},
	} {
		if _, err := servertls.OpenAuthority(dir, options); err == nil {
			t.Errorf("%s is taken", name)
		}
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a refusal left a directory behind")
	}
}

func TestADamagedCertificateFileIsIssuedAgainAtStart(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	first := served(t, open(t, dir, clock, "ropeshift.lan").TLSConfig())
	if err := os.WriteFile(filepath.Join(dir, servertls.CertFile), []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := open(t, dir, clock, "ropeshift.lan")
	if got := served(t, second.TLSConfig()); got.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Error("a damaged certificate file was served")
	}
}
