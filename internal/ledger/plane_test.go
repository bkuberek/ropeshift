package ledger

import (
	"errors"
	"strings"
	"testing"
)

func TestNegotiateProtocol(t *testing.T) {
	tests := []struct {
		name    string
		server  int
		worker  int
		want    int
		refused string
	}{
		{"the same version", 3, 3, 3, ""},
		{"one version behind", 3, 2, 2, ""},
		{"two versions behind", 3, 1, 0, "no longer served"},
		{"ahead of the control plane", 3, 4, 0, "upgrade the control plane"},
		{"the oldest version still served is one behind", 3, 2, 2, ""},
		{"a version no longer served", 2, 1, 0, "no longer served"},
		{"a version before any", 2, 0, 0, "no longer served"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NegotiateProtocol(tt.server, tt.worker)
			if tt.refused == "" {
				if err != nil || got != tt.want {
					t.Fatalf("got %d, %v", got, err)
				}
				return
			}
			var refused *ProtocolError
			if !errors.As(err, &refused) {
				t.Fatalf("got %d, %v", got, err)
			}
			if !strings.Contains(err.Error(), tt.refused) {
				t.Errorf("message %q does not say %q", err, tt.refused)
			}
		})
	}
}

func TestUploadedEventIDIsTheTriple(t *testing.T) {
	first := UploadedEventID("w", 2, 7)
	if first != UploadedEventID("w", 2, 7) {
		t.Error("the same triple gave two IDs")
	}
	for _, other := range []string{UploadedEventID("x", 2, 7), UploadedEventID("w", 3, 7), UploadedEventID("w", 2, 8)} {
		if other == first {
			t.Errorf("%s repeats", other)
		}
	}
}
