package server

import (
	"crypto/tls"
	"net"
	"testing"
)

// A plaintext listener that lists proxies is the peer filter, and no other: the filter is what closes a peer that
// is neither loopback nor listed, so a listener bound off loopback without it would serve every machine that can
// reach it. The filter's own tests hold what it does, and this holds that ListenNetwork installs it.
func TestAPlaintextListenerThatListsProxiesIsWrappedInThePeerFilter(t *testing.T) {
	t.Parallel()
	proxies, err := ParseProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"127.0.0.1:0", "0.0.0.0:0"} {
		listener, err := ListenNetwork(address, Transport{Proxies: proxies})
		if err != nil {
			t.Fatal(err)
		}
		filter, ok := listener.(*peerFilter)
		if !ok {
			_ = listener.Close()
			t.Fatalf("the plaintext listener on %s is %T, and wants the peer filter", address, listener)
		}
		if filter.proxies != proxies {
			t.Errorf("the filter on %s lists %v, and not the proxies it was given", address, filter.proxies)
		}
		_ = listener.Close()
	}
}

// A listener that serves TLS is not the filter, because every connection it takes is encrypted, and a plaintext
// listener on loopback with no proxy listed is not either, because only this machine reaches it.
func TestOnlyAPlaintextListenerThatListsProxiesIsFiltered(t *testing.T) {
	t.Parallel()
	secured, err := ListenNetwork("127.0.0.1:0", Transport{TLS: &tls.Config{}})
	if err != nil {
		t.Fatal(err)
	}
	defer secured.Close()
	if _, ok := secured.(*peerFilter); ok {
		t.Error("a listener that serves TLS is the peer filter")
	}
	local, err := ListenNetwork("127.0.0.1:0", Transport{})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if _, ok := local.(*net.TCPListener); !ok {
		t.Errorf("a plaintext listener on loopback with no proxies is %T", local)
	}
}
