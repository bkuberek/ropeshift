package server_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api/server"
)

// trusted_proxies lists addresses and CIDR ranges, and nothing else. A list that means more than its writer meant
// would trust a client to say who it is, so every entry is read strictly.
func TestTrustedProxiesTakeAddressesAndRangesAndNothingElse(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{
		"10.0.0.1", "10.0.0.0/8", "172.16.0.0/12", "192.168.1.0/24", "127.0.0.1", "127.0.0.1/32", "::1", "fd00::/8", "2001:db8::1",
		"2001:db8:1:2::/64", "10.0.0.1/32", "::ffff:10.0.0.1", "::ffff:10.0.0.0/104", "0.0.0.0/1", "::/1",
	} {
		if _, err := server.ParseProxies([]string{entry}); err != nil {
			t.Errorf("%q is refused: %v", entry, err)
		}
	}
	for _, entry := range []string{
		"", " ", "0.0.0.0/0", "::/0", "::ffff:0.0.0.0/96", "::ffff:0:0/96", "0.0.0.0", "::", "10.0.0.5/24", "2001:db8::1/64",
		"10.0.0.1/33", "10.0.0.0/-1", "10.0.0.0/abc", "10.0.0.1/24/8", "example.com", "localhost", "10.0.0.1:80", "[::1]:80", "http://10.0.0.1",
		"fe80::1%eth0", "10.0.0.1 10.0.0.2", "10.0.0.1,10.0.0.2", "*", "10.0.0", "10.0.0.256", "any", "all", "::ffff:10.0.0.0/64",
	} {
		if proxies, err := server.ParseProxies([]string{entry}); err == nil {
			t.Errorf("%q is taken: %+v", entry, proxies)
		} else if !strings.Contains(err.Error(), "trusted proxy") {
			t.Errorf("the refusal of %q does not say which setting it is of: %v", entry, err)
		}
	}
	if _, err := server.ParseProxies([]string{"10.0.0.1", "10.0.0.2/31", "10.0.0.1"}); err != nil {
		t.Errorf("a list of valid entries is refused: %v", err)
	}
	if _, err := server.ParseProxies([]string{"10.0.0.1", "everyone"}); err == nil {
		t.Error("one bad entry among good ones is taken")
	}
	if proxies, err := server.ParseProxies(nil); err != nil || proxies.Len() != 0 {
		t.Errorf("no entries is %v, %v", proxies, err)
	}
}

func TestAProxyIsContainedByItsAddressOrItsRange(t *testing.T) {
	t.Parallel()
	proxies, err := server.ParseProxies([]string{"10.0.0.0/24", "192.168.1.9", "fd00::/8", "::ffff:172.16.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if proxies.Len() != 4 {
		t.Errorf("the list holds %d entries", proxies.Len())
	}
	for in, want := range map[string]bool{
		"10.0.0.1": true, "10.0.0.255": true, "10.0.1.0": false, "192.168.1.9": true, "192.168.1.10": false, "fd12::1": true, "fe80::1": false,
		"::ffff:10.0.0.7": true, "::ffff:192.168.1.9": true, "172.16.0.5": true, "172.16.0.6": false, "fe80::1%eth0": false, "fd12::1%eth0": true,
		"203.0.113.7": false, "127.0.0.1": false,
	} {
		if got := proxies.Contains(netip.MustParseAddr(in)); got != want {
			t.Errorf("Contains(%s) is %v", in, got)
		}
	}
	var none *server.Proxies
	if none.Contains(netip.MustParseAddr("10.0.0.1")) || none.Len() != 0 {
		t.Error("no proxies contain an address")
	}
}

func forwardedFrom(t *testing.T, proxies []string, remote string, headers map[string][]string) server.Forwarded {
	t.Helper()
	list, err := server.ParseProxies(proxies)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	request.RemoteAddr = remote
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	got := list.Resolve(request)
	if client := list.Client(request); client != got.Client {
		t.Errorf("Client is %s and Resolve says %s", client, got.Client)
	}
	return got
}

// The client is the rightmost address in X-Forwarded-For that is not listed, which no client can choose: an
// address the client wrote is left of what the first proxy added, and a proxy adds the address it was reached from.
func TestTheClientIsTheRightmostAddressThatIsNotListed(t *testing.T) {
	t.Parallel()
	listed := []string{"10.0.0.0/8", "fd00::/8"}
	tests := []struct {
		name    string
		remote  string
		headers map[string][]string
		client  string
		via     bool
	}{
		{"one proxy and one client", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"203.0.113.7"}}, "203.0.113.7", true},
		{"two listed hops", "10.0.0.3:4000", map[string][]string{"X-Forwarded-For": {"203.0.113.7, 10.0.0.2"}}, "203.0.113.7", true},
		{"three listed hops", "10.0.0.4:4000", map[string][]string{"X-Forwarded-For": {"203.0.113.7, 10.0.0.2, 10.0.0.3"}}, "203.0.113.7", true},
		{"an address the client wrote", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"198.51.100.9, 203.0.113.7"}}, "203.0.113.7", true},
		{"an address the client wrote before two hops", "10.0.0.3:4000", map[string][]string{"X-Forwarded-For": {"198.51.100.9, 203.0.113.7, 10.0.0.2"}}, "203.0.113.7", true},
		{"a client that wrote a listed address", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"10.0.0.99, 203.0.113.7"}}, "203.0.113.7", true},
		{"several header lines", "10.0.0.3:4000", map[string][]string{"X-Forwarded-For": {"198.51.100.9", "203.0.113.7", "10.0.0.2"}}, "203.0.113.7", true},
		{"spaces and tabs", "10.0.0.3:4000", map[string][]string{"X-Forwarded-For": {" 203.0.113.7 ,\t10.0.0.2 \t"}}, "203.0.113.7", true},
		{"a port after an IPv4 address", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"203.0.113.7:51234"}}, "203.0.113.7", true},
		{"an IPv6 address", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"2001:db8::7"}}, "2001:db8::7", true},
		{"an IPv6 address in brackets", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"[2001:db8::7]"}}, "2001:db8::7", true},
		{"an IPv6 address in brackets with a port", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"[2001:db8::7]:443"}}, "2001:db8::7", true},
		{"an IPv6 address with a zone", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"fe80::7%eth0"}}, "fe80::7", true},
		{"an IPv4 address in its IPv6 form", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"::ffff:203.0.113.7"}}, "203.0.113.7", true},
		{"a listed IPv6 hop", "[fd00::2]:4000", map[string][]string{"X-Forwarded-For": {"203.0.113.7, fd00::1"}}, "203.0.113.7", true},
		{"every address listed", "10.0.0.3:4000", map[string][]string{"X-Forwarded-For": {"10.0.0.9, 10.0.0.2"}}, "10.0.0.9", true},
		{"no header at all", "10.0.0.2:4000", nil, "10.0.0.2", true},
		{"an empty header", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {""}}, "10.0.0.2", true},
		{"a name for an address", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"unknown"}}, "10.0.0.2", true},
		{"an obfuscated identifier", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"_hidden"}}, "10.0.0.2", true},
		{"garbage beside a hop", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"203.0.113.7, evil, 10.0.0.5"}}, "10.0.0.5", true},
		{"an address with trailing text", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"203.0.113.7;for=1"}}, "10.0.0.2", true},
		{"an address out of range", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {"203.0.113.256"}}, "10.0.0.2", true},
		{"an empty entry beside a hop", "10.0.0.3:4000", map[string][]string{"X-Forwarded-For": {"203.0.113.7,,10.0.0.2"}}, "10.0.0.2", true},
		{"a quoted address", "10.0.0.2:4000", map[string][]string{"X-Forwarded-For": {`"203.0.113.7"`}}, "10.0.0.2", true},
		{"Forwarded is not read", "10.0.0.2:4000", map[string][]string{"Forwarded": {"for=203.0.113.7"}}, "10.0.0.2", true},
		{"Forwarded is not read beside the header", "10.0.0.2:4000", map[string][]string{"Forwarded": {"for=198.51.100.1"}, "X-Forwarded-For": {"203.0.113.7"}}, "203.0.113.7", true},
		{"X-Real-IP is not read", "10.0.0.2:4000", map[string][]string{"X-Real-Ip": {"203.0.113.7"}}, "10.0.0.2", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forwardedFrom(t, listed, tt.remote, tt.headers)
			if got.Client.String() != tt.client || got.Via != tt.via {
				t.Errorf("the client is %s, via a proxy %v, and wants %s", got.Client, got.Via, tt.client)
			}
		})
	}
}

// A peer that is not listed says nothing of where a request came from: its headers are the client's own words.
func TestForwardedHeadersFromAPeerThatIsNotListedAreIgnored(t *testing.T) {
	t.Parallel()
	headers := map[string][]string{
		"X-Forwarded-For": {"198.51.100.1"}, "X-Forwarded-Proto": {"https"}, "Forwarded": {"for=198.51.100.2;proto=https"}, "X-Real-Ip": {"198.51.100.3"},
	}
	for _, remote := range []string{"203.0.113.9:4000", "[2001:db8::9]:4000", "127.0.0.1:4000", "11.0.0.1:4000"} {
		got := forwardedFrom(t, []string{"10.0.0.0/8"}, remote, headers)
		peer := netip.MustParseAddrPort(remote).Addr()
		if got.Client != peer || got.Proto != "" || got.Via {
			t.Errorf("a request from %s is read as %+v", remote, got)
		}
	}
	// With no proxies listed, no peer is one.
	got := forwardedFrom(t, nil, "10.0.0.2:4000", headers)
	if got.Client.String() != "10.0.0.2" || got.Proto != "" || got.Via {
		t.Errorf("with no proxies a request is read as %+v", got)
	}
	var none *server.Proxies
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:4000"
	request.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := none.Resolve(request); got.Client.String() != "10.0.0.2" || got.Via {
		t.Errorf("a server with no list of proxies reads %+v", got)
	}
}

// X-Forwarded-Proto says whether the proxy's own client came over TLS. The rightmost value is the nearest proxy's,
// and a value that is neither http nor https says nothing.
func TestTheProtocolIsReadFromAListedPeerOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"https", []string{"https"}, "https"},
		{"http", []string{"http"}, "http"},
		{"in capitals with spaces", []string{"  HTTPS "}, "https"},
		{"the rightmost of a list", []string{"https, http"}, "http"},
		{"the rightmost of several lines", []string{"http", "https"}, "https"},
		{"an empty rightmost is skipped", []string{"http,"}, "http"},
		{"another protocol", []string{"gopher"}, ""},
		{"a protocol that is neither", []string{"https, gopher"}, ""},
		{"nothing", nil, ""},
		{"an empty value", []string{""}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forwardedFrom(t, []string{"10.0.0.0/8"}, "10.0.0.2:4000", map[string][]string{"X-Forwarded-Proto": tt.values})
			if got.Proto != tt.want {
				t.Errorf("the protocol is %q, and wants %q", got.Proto, tt.want)
			}
		})
	}
}
