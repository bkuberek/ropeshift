package server

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// publicOrigin is where members' browsers reach a server that sits behind a reverse proxy that terminates TLS: the
// origin the web UI is served under. The Host header of such a request is host, and the Origin header of one that
// has one is origin.
type publicOrigin struct {
	host   string
	origin string
	secure bool
}

// CheckPublicURL reads a public URL, the origin members' browsers use: a scheme, a host, and a port when it is not
// the scheme's own, and nothing after them. It returns the URL in the form the server compares with, in lowercase
// and without a default port. The scheme is http or https.
func CheckPublicURL(raw string) (string, error) {
	parsed, err := parsePublicOrigin(raw)
	if err != nil {
		return "", err
	}
	return parsed.origin, nil
}

func parsePublicOrigin(raw string) (*publicOrigin, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("the public URL is not a URL")
	}
	switch {
	case u.Scheme != "https" && u.Scheme != "http":
		return nil, errors.New("the public URL is an http:// or https:// origin")
	case u.User != nil:
		return nil, errors.New("the public URL carries credentials, which an origin never does")
	case u.Hostname() == "":
		return nil, errors.New("the public URL names no host")
	case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "":
		return nil, errors.New("the public URL is a scheme, a host, and a port, and nothing after them")
	}
	port := u.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New("the public URL has no usable port")
		}
	}
	hostname := strings.ToLower(u.Hostname())
	if strings.Contains(hostname, ":") {
		hostname = "[" + hostname + "]"
	}
	host := hostname
	if port != "" && port != defaultPort(u.Scheme) {
		host = net.JoinHostPort(strings.Trim(hostname, "[]"), port)
	}
	return &publicOrigin{host: host, origin: u.Scheme + "://" + host, secure: u.Scheme == "https"}, nil
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}
