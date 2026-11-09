package api

import "testing"

// Every surface says money and counts the way the web UI does, so a terminal and a page agree.
func TestDollarsReadAsTheWebUISaysThem(t *testing.T) {
	tests := []struct {
		usd  float64
		want string
	}{
		{0, "$0.00"},
		{1.3, "$1.30"},
		{1.2345, "$1.23"},
		{2.6, "$2.60"},
		{0.005, "$0.01"},
		{0.004, "under $0.01"},
		{0.0001, "under $0.01"},
		{1234.5, "$1234.50"},
	}
	for _, tt := range tests {
		if got := Dollars(tt.usd); got != tt.want {
			t.Errorf("Dollars(%v) = %q, want %q", tt.usd, got, tt.want)
		}
	}
}

func TestCountSaysOneInTheSingular(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 batches of questions"},
		{1, "1 batch of questions"},
		{2, "2 batches of questions"},
	}
	for _, tt := range tests {
		if got := Count(tt.n, "batch of questions", "batches of questions"); got != tt.want {
			t.Errorf("Count(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
