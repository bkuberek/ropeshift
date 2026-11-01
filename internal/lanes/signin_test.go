package lanes_test

import (
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
)

func TestReadSignIn(t *testing.T) {
	tests := []struct {
		method, kind, status string
	}{
		{"none", "", lanes.SignedOut},
		{"claude.ai", lanes.SignInSubscription, lanes.SignedIn},
		{"oauth_token", lanes.SignInSubscription, lanes.SignedIn},
		{"api_key", lanes.SignInAPIKey, lanes.SignedIn},
		{"api_key_helper", lanes.SignInAPIKey, lanes.SignedIn},
		{"third_party", lanes.SignInAPIKey, lanes.SignedIn},
		{"a word nobody documented", "", lanes.SignInUnknown},
		{"", "", lanes.SignInUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			kind, status := lanes.ReadSignIn(tt.method)
			if kind != tt.kind || status != tt.status {
				t.Errorf("kind %q, status %q", kind, status)
			}
		})
	}
}

func TestDefaultRunMode(t *testing.T) {
	tests := []struct {
		name        string
		kind        string
		interactive bool
		want        string
	}{
		{"a subscription runs where a person can attach", lanes.SignInSubscription, true, lanes.ProfileRunTmux},
		{"an API key runs headless", lanes.SignInAPIKey, true, lanes.ProfileRunHeadless},
		{"a sign-in it could not read runs headless", "", true, lanes.ProfileRunHeadless},
		{"no terminal leaves only headless", lanes.SignInSubscription, false, lanes.ProfileRunHeadless},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.DefaultRunMode(tt.kind, tt.interactive); got != tt.want {
				t.Errorf("run mode %q, want %q", got, tt.want)
			}
		})
	}
}
