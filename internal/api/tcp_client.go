package api

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrServerOffLoopback refuses an address a worker or a client would send a credential to over a network no
// one encrypts: this version reaches a server over plain HTTP on its own machine only.
var ErrServerOffLoopback = errors.New("a server off loopback needs TLS, which this version does not have")

// NewTCPClient returns a client on a server's address, which speaks the same API as the unix client. In
// this version the address is a plain http:// URL of a loopback address and a port, with no credentials,
// path, query, or fragment, because a credential sent over plain HTTP stays on the machine only on
// loopback. A name that is not `localhost`, a private address, and an https:// URL are refused before any
// connection opens, and the refusal never repeats what the URL held.
func NewTCPClient(server string) (*Client, error) {
	base, err := ServerAddress(server)
	if err != nil {
		return nil, err
	}
	// No proxy from the environment: the credential goes to the address and nowhere else.
	transport := &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second}
	return &Client{http: &http.Client{Transport: transport}, base: base}, nil
}

// ServerAddress checks a server address and returns the base URL a client calls: the address with the
// loopback name `localhost` spelled as the address it stands for, and a trailing slash taken off. A worker
// keeps this form, so two spellings of one server are one.
func ServerAddress(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil {
		return "", errors.New("the server address is not a URL")
	}
	switch {
	case u.User != nil:
		return "", errors.New("the server address carries credentials, which an address never does")
	case u.Scheme != "http":
		return "", errors.New("this version reaches a server only over http:// on a loopback address")
	case u.Hostname() == "":
		return "", errors.New("the server address names no host")
	case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "":
		return "", errors.New("the server address is a host and a port, and nothing after them")
	}
	host := u.Hostname()
	if host == "localhost" {
		host = "127.0.0.1"
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "", ErrServerOffLoopback
	}
	port := "80"
	if given := u.Port(); given != "" {
		number, err := strconv.Atoi(given)
		if err != nil || number < 1 || number > 65535 {
			return "", errors.New("the server address has no usable port")
		}
		port = given
	}
	return "http://" + net.JoinHostPort(host, port), nil
}
