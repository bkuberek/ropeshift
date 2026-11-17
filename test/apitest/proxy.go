package apitest

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ReverseProxy serves TLS on a loopback port with the certificate it is given and forwards every request to a
// plaintext address, as a reverse proxy that terminates TLS does: it keeps the Host header the client sent, appends
// the client's address to X-Forwarded-For after whatever the client wrote there, says in X-Forwarded-Proto that its
// own client came over TLS, and passes a stream on as it is written. It returns the address it serves, which lives
// until the test ends.
func ReverseProxy(t testing.TB, target string, serving *tls.Config) string {
	t.Helper()
	to := &url.URL{Scheme: "http", Host: target}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(to)
			r.Out.Host = r.In.Host
			forwarded := r.In.Header.Values("X-Forwarded-For")
			if client, _, err := net.SplitHostPort(r.In.RemoteAddr); err == nil {
				forwarded = append(forwarded, client)
			}
			r.Out.Header.Set("X-Forwarded-For", strings.Join(forwarded, ", "))
			r.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		FlushInterval: -1,
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serving)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = httpServer.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = httpServer.Close()
		<-done
	})
	return listener.Addr().String()
}
