package server

import (
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// A server behind a reverse proxy that terminates TLS (ADR-0031, mode c) takes plaintext from the proxies it
// lists and from loopback, and reads from a listed peer, and from no other, what the peer says of where a request
// came from. Two headers are read, X-Forwarded-For for the client's address and X-Forwarded-Proto for whether the
// proxy's own client came over TLS. Forwarded and X-Real-IP are not read, because two headers that can disagree
// need a rule to choose, and common proxies set X-Forwarded-For.

// Proxies is the addresses and ranges of the proxies a server trusts to say where a request came from. A nil
// Proxies lists none.
type Proxies struct {
	prefixes []netip.Prefix
}

// ParseProxies reads a list of proxies, each an address or a CIDR range and nothing else, so a list never means
// more than its writer wrote. It refuses an entry that matches every address, one wider than a /8 of IPv4
// addresses or a /16 of IPv6 addresses, one that is not a range's first address, a name, a port, a zone, and an
// empty entry. An IPv4 address in its IPv6 form is the IPv4 address.
func ParseProxies(entries []string) (*Proxies, error) {
	list := &Proxies{}
	for _, entry := range entries {
		prefix, err := parseProxy(entry)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(list.prefixes, prefix) {
			list.prefixes = append(list.prefixes, prefix)
		}
	}
	return list, nil
}

// The widest range a list takes: a /8 of IPv4 addresses holds the whole of a private network, and a /16 of IPv6
// addresses holds more than the block a site is given, which is a /48. A range wider than that names hosts that are
// no proxy of this server, and each of them could say who a client is, so a wider range is refused rather than
// warned of.
const (
	minIPv4ProxyBits = 8
	minIPv6ProxyBits = 16
)

func minProxyBits(addr netip.Addr) int {
	if addr.Is4() {
		return minIPv4ProxyBits
	}
	return minIPv6ProxyBits
}

func parseProxy(entry string) (netip.Prefix, error) {
	refuse := func(why string, args ...any) error {
		return fmt.Errorf("trusted proxy %q %s", entry, fmt.Sprintf(why, args...))
	}
	text := strings.TrimSpace(entry)
	if text == "" {
		return netip.Prefix{}, refuse("is empty")
	}
	var prefix netip.Prefix
	if strings.Contains(text, "/") {
		parsed, err := netip.ParsePrefix(text)
		if err != nil {
			return netip.Prefix{}, refuse("is no address or CIDR range")
		}
		prefix = parsed
	} else {
		addr, err := netip.ParseAddr(text)
		if err != nil {
			return netip.Prefix{}, refuse("is no address or CIDR range")
		}
		if addr.Zone() != "" {
			return netip.Prefix{}, refuse("names a zone, which no client address carries")
		}
		prefix = netip.PrefixFrom(addr, addr.BitLen())
	}
	if prefix.Addr().Is4In6() {
		if prefix.Bits() < 96 {
			return netip.Prefix{}, refuse("is a range of IPv6 addresses that holds IPv4 addresses in part: list an IPv4 range or an IPv6 range")
		}
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}
	switch {
	case prefix.Bits() == 0:
		return netip.Prefix{}, refuse("matches every address, which trusts every client to say who it is")
	case prefix.Bits() == prefix.Addr().BitLen() && prefix.Addr().IsUnspecified():
		return netip.Prefix{}, refuse("is the unspecified address, which no proxy has")
	case prefix.Bits() < minProxyBits(prefix.Addr()):
		return netip.Prefix{}, refuse("is wider than a range of trusted proxies may be, a /%d of IPv4 addresses or a /%d of IPv6 addresses: list the proxies, or the narrower range that holds them, because every address in a listed range may say who a client is", minIPv4ProxyBits, minIPv6ProxyBits)
	case prefix != prefix.Masked():
		return netip.Prefix{}, refuse("has bits set past its prefix: write %s", prefix.Masked())
	}
	return prefix, nil
}

// Len is how many entries the list holds.
func (p *Proxies) Len() int {
	if p == nil {
		return 0
	}
	return len(p.prefixes)
}

// Contains says an address is one of the listed proxies, or in a listed range.
func (p *Proxies) Contains(addr netip.Addr) bool {
	if p == nil {
		return false
	}
	addr = clean(addr)
	return slices.ContainsFunc(p.prefixes, func(prefix netip.Prefix) bool { return prefix.Contains(addr) })
}

// Forwarded is what a request says of where it came from, as far as its peer may be believed.
type Forwarded struct {
	// Client is the address the request came from: the rightmost address of X-Forwarded-For that is not listed,
	// when the peer is a listed proxy, and the peer itself otherwise.
	Client netip.Addr
	// Proto is the rightmost value of X-Forwarded-Proto, "https" or "http", when the peer is a listed proxy and
	// said one of them. It is empty otherwise.
	Proto string
	// Via says the peer is a listed proxy.
	Via bool
}

// Resolve reads where a request came from. A peer that is not listed says nothing: what its headers hold is the
// client's own word, so the client is the peer. From a listed peer it walks X-Forwarded-For from the right, which
// is the end each proxy appends the address it was reached from to. A listed address is another hop of ours and
// the walk goes on past it, and the first address that is not listed is the client. The walk stops at an entry
// that is not an address, and the client is then the last hop it saw, because whatever stands left of an entry no
// proxy would write is not the word of a proxy.
func (p *Proxies) Resolve(r *http.Request) Forwarded {
	peer := PeerAddress(r)
	if !p.Contains(peer) {
		return Forwarded{Client: peer}
	}
	got := Forwarded{Client: peer, Via: true, Proto: forwardedProto(r.Header.Values("X-Forwarded-Proto"))}
	var entries []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		entries = append(entries, strings.Split(line, ",")...)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		addr, ok := forwardedAddress(entries[i])
		if !ok {
			break
		}
		got.Client = addr
		if !p.Contains(addr) {
			break
		}
	}
	return got
}

// Client is the address a request came from, as Resolve reads it, which is what WebOptions.Client takes.
func (p *Proxies) Client(r *http.Request) netip.Addr { return p.Resolve(r).Client }

// forwardedAddress reads one entry of X-Forwarded-For: an address, with a port or in brackets or both, which is
// every form a proxy writes one in. Anything else is no address, a name and a quoted address included.
func forwardedAddress(entry string) (netip.Addr, bool) {
	text := strings.Trim(entry, " \t")
	if addr, err := netip.ParseAddr(text); err == nil {
		return clean(addr), true
	}
	if pair, err := netip.ParseAddrPort(text); err == nil {
		return clean(pair.Addr()), true
	}
	if inner, ok := strings.CutPrefix(text, "["); ok {
		if inner, ok = strings.CutSuffix(inner, "]"); ok {
			if addr, err := netip.ParseAddr(inner); err == nil {
				return clean(addr), true
			}
		}
	}
	return netip.Addr{}, false
}

// forwardedProto reads the rightmost value of X-Forwarded-Proto, the nearest proxy's, and says what it says only
// when it is http or https.
func forwardedProto(lines []string) string {
	last := ""
	for _, line := range lines {
		for _, value := range strings.Split(line, ",") {
			if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
				last = value
			}
		}
	}
	if last == "http" || last == "https" {
		return last
	}
	return ""
}
