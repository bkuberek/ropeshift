package servertls_test

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/servertls"
)

// supply puts a certificate and key into a directory as tls.crt and tls.key, the way a person's own certificate
// authority would, with the modification time given. It returns that authority's certificate as a pool, which
// stands for the system roots a client verifies through.
func supply(t *testing.T, to string, modified time.Time, names ...string) *x509.CertPool {
	t.Helper()
	from := filepath.Join(t.TempDir(), "tls")
	authority := open(t, from, fake.NewClock(start), names...)
	place(t, filepath.Join(from, servertls.CertFile), filepath.Join(to, "tls.crt"), modified)
	place(t, filepath.Join(from, servertls.KeyFile), filepath.Join(to, "tls.key"), modified)
	return poolOf(t, authority.CertificatePEM())
}

func place(t *testing.T, from, to string, modified time.Time) {
	t.Helper()
	raw, err := os.ReadFile(from)
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

// warnings collects what a pair says of files that will not load.
type warnings struct {
	mu    sync.Mutex
	heard []error
}

func (w *warnings) hear(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.heard = append(w.heard, err)
}

func (w *warnings) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.heard)
}

func openPair(t *testing.T, dir string, heard *warnings) *servertls.Pair {
	t.Helper()
	pair, err := servertls.OpenPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), heard.hear)
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func TestASuppliedPairIsServedAsItIs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	roots := supply(t, dir, start, "ropeshift.lan")
	var heard warnings
	pair := openPair(t, dir, &heard)
	leaf := served(t, pair.TLSConfig())
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "ropeshift.lan", CurrentTime: start}); err != nil {
		t.Error(err)
	}
	if got := pair.TLSConfig().MinVersion; got < 0x0303 {
		t.Errorf("the lowest TLS version is %x", got)
	}
	if heard.count() != 0 {
		t.Errorf("a pair that loads warned: %v", heard.heard)
	}
}

// A renewal replaces the files while the server runs, and the next connection is served the new certificate.
func TestAPairReplacedWhileTheServerRunsIsServedAtTheNextConnection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	supply(t, dir, start, "ropeshift.lan")
	var heard warnings
	pair := openPair(t, dir, &heard)
	config := pair.TLSConfig()
	first := served(t, config)
	if again := served(t, config); again.SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("the same files gave two certificates")
	}

	roots := supply(t, dir, start.Add(time.Hour), "ropeshift.lan")
	renewed := served(t, config)
	if renewed.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Fatal("the replaced files were not read again")
	}
	if _, err := renewed.Verify(x509.VerifyOptions{Roots: roots, DNSName: "ropeshift.lan", CurrentTime: start}); err != nil {
		t.Errorf("what is served is not the new pair: %v", err)
	}
	if heard.count() != 0 {
		t.Errorf("a replacement that loads warned: %v", heard.heard)
	}
}

// A replacement that does not load is no reason to stop serving, so the old pair stays, and the next
// replacement that loads is served.
func TestAReplacementThatDoesNotLoadLeavesTheOldPairServedAndSaysSoOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	supply(t, dir, start, "ropeshift.lan")
	var heard warnings
	pair := openPair(t, dir, &heard)
	config := pair.TLSConfig()
	first := served(t, config)

	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), []byte("half a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "tls.crt"), start.Add(time.Hour), start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) != 0 {
			t.Fatal("a certificate that does not load was served")
		}
	}
	if heard.count() != 1 {
		t.Errorf("the pair warned %d times of one bad replacement", heard.count())
	}

	supply(t, dir, start.Add(2*time.Hour), "ropeshift.lan")
	if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Error("the replacement that loads was not served")
	}
}

// A renewal that writes the certificate before the key leaves a pair that does not match for a moment. The
// old pair serves until the key arrives, and then the new one does.
func TestACertificateWrittenBeforeItsKeyIsServedOnceTheKeyArrives(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	supply(t, dir, start, "ropeshift.lan")
	var heard warnings
	pair := openPair(t, dir, &heard)
	config := pair.TLSConfig()
	first := served(t, config)

	next := filepath.Join(t.TempDir(), "next")
	if err := os.Mkdir(next, 0o700); err != nil {
		t.Fatal(err)
	}
	supply(t, next, start.Add(time.Hour), "ropeshift.lan")
	place(t, filepath.Join(next, "tls.crt"), filepath.Join(dir, "tls.crt"), start.Add(time.Hour))
	if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("a certificate with another pair's key was served")
	}
	place(t, filepath.Join(next, "tls.key"), filepath.Join(dir, "tls.key"), start.Add(time.Hour))
	if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Error("the new pair was not served once its key arrived")
	}
}

func TestFilesThatAreGoneForAWhileLeaveTheOldPairServed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	supply(t, dir, start, "ropeshift.lan")
	var heard warnings
	pair := openPair(t, dir, &heard)
	config := pair.TLSConfig()
	first := served(t, config)

	for _, name := range []string{"tls.crt", "tls.key"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) != 0 {
			t.Fatal("the pair changed with its files gone")
		}
	}
	if heard.count() != 1 {
		t.Errorf("the pair warned %d times of files that are gone", heard.count())
	}
	supply(t, dir, start.Add(time.Hour), "ropeshift.lan")
	if got := served(t, config); got.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Error("the files that came back were not served")
	}
}

func TestAPairThatDoesNotLoadIsRefusedAtStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := servertls.OpenPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), nil); err == nil {
		t.Error("files that do not exist are taken")
	}
	supply(t, dir, start, "ropeshift.lan")
	other := t.TempDir()
	supply(t, other, start, "ropeshift.lan")
	// A certificate with another pair's key.
	if _, err := servertls.OpenPair(filepath.Join(dir, "tls.crt"), filepath.Join(other, "tls.key"), nil); err == nil {
		t.Error("a certificate with another key is taken")
	}
}
