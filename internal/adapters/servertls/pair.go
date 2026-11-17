package servertls

import (
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"
)

// Pair is a certificate and key a person supplies, such as one from a public authority or a tailnet. The files
// are read again when the modification time or the size of either changes, checked as each connection arrives,
// so a renewal needs no restart.
type Pair struct {
	certFile, keyFile string
	report            reporter

	mu      sync.Mutex
	current *tls.Certificate
	seen    stamp
}

// stamp is what a pair's files looked like when they were last read.
type stamp struct {
	cert, key fileStamp
}

type fileStamp struct {
	modified time.Time
	size     int64
}

// OpenPair reads a certificate and key, and refuses a pair that does not load. warn hears a replacement that
// does not load, which leaves the pair in use served.
func OpenPair(certFile, keyFile string, warn func(error)) (*Pair, error) {
	p := &Pair{certFile: certFile, keyFile: keyFile, report: reporter{warn: warn}}
	seen, err := p.stamp()
	if err != nil {
		return nil, err
	}
	current, err := p.load()
	if err != nil {
		return nil, err
	}
	p.seen, p.current = seen, current
	return p, nil
}

// TLSConfig is what a listener serves: the pair, read again when either file changes.
func (p *Pair) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}, GetCertificate: p.certificate}
}

// certificate answers a connection with the pair, read again first when a file changed. A replacement that
// does not load, or files that are gone for a moment, leave the pair in use, and the next change is read.
func (p *Pair) certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen, err := p.stamp()
	if err != nil {
		p.report.say(err)
		return p.current, nil
	}
	p.report.clear()
	if seen == p.seen {
		return p.current, nil
	}
	// A pair that does not load is not tried again until a file changes again, which is how a certificate
	// written before its key comes right.
	p.seen = seen
	loaded, err := p.load()
	if err != nil {
		p.report.say(err)
		return p.current, nil
	}
	p.current = loaded
	return p.current, nil
}

func (p *Pair) stamp() (stamp, error) {
	cert, err := stampOf(p.certFile)
	if err != nil {
		return stamp{}, err
	}
	key, err := stampOf(p.keyFile)
	if err != nil {
		return stamp{}, err
	}
	return stamp{cert, key}, nil
}

// stampOf follows a link, because a certificate that a tool renews is often a link to the file it wrote last.
func stampOf(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{info.ModTime(), info.Size()}, nil
}

func (p *Pair) load() (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(p.certFile, p.keyFile)
	if err != nil {
		return nil, fmt.Errorf("the certificate %s and its key %s do not load as a pair: %w", p.certFile, p.keyFile, err)
	}
	return &cert, nil
}
