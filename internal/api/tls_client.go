package api

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NewTLSClient returns a client on a server's https address, which speaks the same API as the unix client. The
// config decides whom the client trusts: the roots a caller gives it, or a check of its own, such as a pinned
// authority. A nil config verifies through the system roots, and no config lowers the version below TLS 1.2.
// The address is checked as SecureServerAddress checks it, before any connection opens, and a refusal never
// repeats what the address held.
func NewTLSClient(server string, config *tls.Config) (*Client, error) {
	base, err := SecureServerAddress(server)
	if err != nil {
		return nil, err
	}
	config = config.Clone()
	if config == nil {
		config = &tls.Config{}
	}
	config.MinVersion = max(config.MinVersion, tls.VersionTLS12)
	// No proxy from the environment, and no redirect: the credential goes to the address and nowhere else.
	transport := &http.Transport{Proxy: nil, TLSClientConfig: config, MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second}
	return &Client{http: &http.Client{Transport: transport, CheckRedirect: refuseRedirect}, base: base}, nil
}

// SecureServerAddress checks an https server address and returns the base URL a client calls: the host in
// lowercase, with its port, which is 443 when the address names none, and no trailing slash. An address that
// carries credentials, a path, a query, or a fragment is refused.
func SecureServerAddress(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil {
		return "", errors.New("the server address is not a URL")
	}
	switch {
	case u.User != nil:
		return "", errors.New("the server address carries credentials, which an address never does")
	case u.Scheme != "https":
		return "", errors.New("the server address is an https:// URL")
	case u.Hostname() == "":
		return "", errors.New("the server address names no host")
	case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "":
		return "", errors.New("the server address is a host and a port, and nothing after them")
	}
	port := "443"
	if given := u.Port(); given != "" {
		number, err := strconv.Atoi(given)
		if err != nil || number < 1 || number > 65535 {
			return "", errors.New("the server address has no usable port")
		}
		port = given
	}
	return "https://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), nil
}
