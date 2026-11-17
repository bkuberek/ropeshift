package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// A client on a loopback address speaks the same API as a client on the socket: the same paths, with the
// credential the caller gave it.
func TestATCPClientCallsTheAPIOnALoopbackAddress(t *testing.T) {
	t.Parallel()
	var seen *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(server.Close)
	client, err := api.NewTCPClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if _, err := client.WithCredential("a-worker-credential").Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen.URL.Path != "/v1/health" {
		t.Fatalf("the server saw %v", seen)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer a-worker-credential" {
		t.Errorf("Authorization is %q", got)
	}
}

// A refusal the server explains comes back as the API's own error, as it does on the socket.
func TestATCPClientReadsTheServersRefusal(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"bad_credential","message":"no"}}`))
	}))
	t.Cleanup(server.Close)
	client, err := api.NewTCPClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	_, err = client.Health(context.Background())
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Detail.Code != api.CodeBadCredential || refused.Status != http.StatusUnauthorized {
		t.Fatalf("the refusal reads %v", err)
	}
}

// A client sends a credential to the address it was given and to no other. A server that answers a call with a
// redirect would have the client send the call, session and credential in its headers, to wherever the redirect
// points, so no redirect is followed.
func TestATCPClientFollowsNoRedirect(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var reached []http.Header
			elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = append(reached, r.Header.Clone())
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))
			t.Cleanup(elsewhere.Close)
			redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, elsewhere.URL+r.URL.Path, status)
			}))
			t.Cleanup(redirecting.Close)
			client, err := api.NewTCPClient(redirecting.URL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			client = client.WithCredential("a-worker-credential")
			client.UseSession("a-member-session")
			if _, err := client.Health(context.Background()); err == nil {
				t.Error("a redirected call succeeded")
			}
			if len(reached) != 0 {
				t.Errorf("a redirect was followed, with headers %v", reached[0])
			}
		})
	}
}

// Two spellings of one server are one address.
func TestAServerAddressHasOneSpelling(t *testing.T) {
	t.Parallel()
	for spelling, want := range map[string]string{
		"http://localhost:7400":  "http://127.0.0.1:7400",
		"http://127.0.0.1:7400/": "http://127.0.0.1:7400",
		"http://127.0.0.1":       "http://127.0.0.1:80",
		"http://[::1]:7400":      "http://[::1]:7400",
	} {
		if got, err := api.ServerAddress(spelling); err != nil || got != want {
			t.Errorf("%s is %q, %v, want %q", spelling, got, err, want)
		}
	}
}

// Nothing answering is a server that cannot be reached, which a caller tells from a refusal.
func TestATCPClientToNothingIsUnreachable(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.NotFoundHandler())
	address := server.URL
	server.Close()
	client, err := api.NewTCPClient(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if _, err := client.Health(context.Background()); !errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("the call to a closed port reads %v", err)
	}
}

// Until a server can be reached over TLS, a client reaches only a server on its own machine over plain
// HTTP, and a value that could send a credential anywhere else is refused before it connects.
func TestATCPClientRefusesWhatItCannotKeepOnTheMachine(t *testing.T) {
	t.Parallel()
	loopback := []string{"http://127.0.0.1:7400", "http://localhost:7400", "http://[::1]:7400", "http://127.0.0.1:7400/", "http://127.0.0.1"}
	for _, address := range loopback {
		t.Run(address, func(t *testing.T) {
			client, err := api.NewTCPClient(address)
			if err != nil {
				t.Fatalf("%s is refused: %v", address, err)
			}
			client.Close()
		})
	}
	refused := map[string]string{
		"":                                  "empty",
		"127.0.0.1:7400":                    "no scheme",
		"https://127.0.0.1:7400":            "tls",
		"ftp://127.0.0.1:7400":              "scheme",
		"http://192.168.1.2:7400":           "off loopback",
		"http://10.0.0.1:7400":              "off loopback",
		"http://0.0.0.0:7400":               "unspecified",
		"http://attacker.example:7400":      "name",
		"http://127.0.0.1.attacker.example": "name that starts like an address",
		"http://user:pass@127.0.0.1:7400":   "credentials in the URL",
		"http://127.0.0.1:7400/v1":          "path",
		"http://127.0.0.1:7400?x=1":         "query",
		"http://127.0.0.1:7400#frag":        "fragment",
		"http://":                           "no host",
		"http://127.0.0.1:99999":            "port",
		"http://127.0.0.1:0":                "port zero",
	}
	for address, why := range refused {
		t.Run(why+" "+address, func(t *testing.T) {
			client, err := api.NewTCPClient(address)
			if err == nil {
				client.Close()
				t.Fatalf("%q is accepted", address)
			}
			if strings.Contains(err.Error(), "pass") {
				t.Errorf("the refusal repeats the secret: %v", err)
			}
		})
	}
}
