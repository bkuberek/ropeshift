package servertls_test

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/servertls"
)

// obstruct puts a directory where a file belongs, so the file a renewal renames onto that name cannot take its
// place, whoever runs the test. It returns what clears the obstruction.
func obstruct(t *testing.T, path string) func() {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "kept"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
}

func fileOf(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func handshake(t *testing.T, config *tls.Config) (*tls.Certificate, error) {
	t.Helper()
	return config.GetCertificate(&tls.ClientHelloInfo{})
}

// A renewal that cannot be kept leaves the certificate in use serving until its end, says so, and is not tried
// again by every connection: each try makes a key, and a server under load would make one per handshake.
func TestARenewalThatFailsLeavesTheCertificateInUseServedAndWaitsBeforeItTriesAgain(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	var said heard
	config := openHearing(t, dir, clock, &said, "ropeshift.lan").TLSConfig()
	first := served(t, config)
	unobstruct := obstruct(t, filepath.Join(dir, servertls.CertFile))
	keyPath := filepath.Join(dir, servertls.KeyFile)

	clock.Advance(servertls.CertValidity - servertls.RenewBefore)
	if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("a renewal that failed replaced the certificate in use")
	}
	if got := said.all(); len(got) != 1 || !strings.Contains(got[0], "renew") {
		t.Fatalf("the failed renewal said %v", got)
	}
	afterFirstTry := fileOf(t, keyPath)
	for range 20 {
		if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) != 0 {
			t.Fatal("the certificate in use was replaced by a handshake that followed a failed renewal")
		}
	}
	if !bytes.Equal(fileOf(t, keyPath), afterFirstTry) {
		t.Error("handshakes made a key again, with the renewal that failed a moment before")
	}

	// A minute on, the next handshake tries again, and the certificate in use still serves.
	clock.Advance(time.Minute)
	if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("a second failed renewal replaced the certificate in use")
	}
	if bytes.Equal(fileOf(t, keyPath), afterFirstTry) {
		t.Error("the renewal was never tried again")
	}

	// Once its end has come, a certificate that could not be renewed serves nobody.
	clock.Advance(servertls.RenewBefore)
	if _, err := handshake(t, config); err == nil {
		t.Error("a certificate past its end was served")
	}

	unobstruct()
	clock.Advance(time.Minute)
	renewed := served(t, config)
	if renewed.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Error("the renewal was not made once the files could be written")
	}
}

// The authority's end is checked when a certificate is renewed, not only at start: a server that outlives its
// authority issues no certificate from it.
func TestNoCertificateIsIssuedFromAnAuthorityThatExpiredWhileTheServerRan(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	authority := open(t, dir, clock, "ropeshift.lan")
	config := authority.TLSConfig()
	served(t, config)
	written := fileOf(t, filepath.Join(dir, servertls.CertFile))

	clock.Advance(start.AddDate(10, 0, 1).Sub(start))
	_, err := handshake(t, config)
	if err == nil || !strings.Contains(err.Error(), "expired") || !strings.Contains(err.Error(), servertls.CAFile) {
		t.Fatalf("a server past its authority's end answers %v", err)
	}
	if !bytes.Equal(fileOf(t, filepath.Join(dir, servertls.CertFile)), written) {
		t.Error("a certificate was issued from an authority that expired")
	}
}

// A certificate that ended before its renewal was due, because its authority was about to end, is answered with
// the error of that, and never with nothing: a connection that gets no certificate and no error is left to guess.
func TestACertificateThatEndedBeforeItsRenewalWasDueAnswersWithAnError(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	var said heard
	ca := certificateOf(t, openHearing(t, dir, clock, &said, "ropeshift.lan").CertificatePEM())

	clock.Advance(ca.NotAfter.Add(-30 * time.Second).Sub(start))
	restarted := openHearing(t, dir, clock, &said, "ropeshift.lan")
	served(t, restarted.TLSConfig())
	clock.Advance(45 * time.Second)
	cert, err := handshake(t, restarted.TLSConfig())
	if cert != nil || err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("a certificate that ended with its authority is answered with %v, %v", cert, err)
	}
}

// A certificate never outlives the authority that issued it, and a server whose authority ends within the month
// says so, with what to do.
func TestAServerCertificateNeverOutlivesItsAuthorityAndTheServerWarnsOfItsEnd(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tls")
	clock := fake.NewClock(start)
	var said heard
	authority := openHearing(t, dir, clock, &said, "ropeshift.lan")
	if got := said.all(); len(got) != 0 {
		t.Fatalf("a new authority warned: %v", got)
	}
	ca := certificateOf(t, authority.CertificatePEM())

	clock.Advance(ca.NotAfter.Add(-10 * 24 * time.Hour).Sub(start))
	restarted := openHearing(t, dir, clock, &said, "ropeshift.lan")
	leaf := served(t, restarted.TLSConfig())
	if leaf.NotAfter.After(ca.NotAfter) {
		t.Errorf("the certificate ends %s, past its authority's end %s", leaf.NotAfter, ca.NotAfter)
	}
	if !slices.ContainsFunc(said.all(), func(text string) bool {
		return strings.Contains(text, servertls.CAFile) && strings.Contains(text, servertls.CAKeyFile)
	}) {
		t.Errorf("nothing says the authority ends within the month: %v", said.all())
	}
	// A certificate that is already the last the authority can sign is not made again by every handshake.
	if again := served(t, restarted.TLSConfig()); again.SerialNumber.Cmp(leaf.SerialNumber) != 0 {
		t.Error("the last certificate the authority can sign was issued again at once")
	}
}
