package ledger

import (
	"errors"
	"testing"
)

func TestBindingFromRemote(t *testing.T) {
	for _, tt := range []struct{ remote, want string }{
		{"https://github.com/acme/widgets.git", "github.com/acme/widgets"},
		{"https://github.com/acme/widgets", "github.com/acme/widgets"},
		{"https://token@github.com/acme/widgets.git", "github.com/acme/widgets"},
		{"git@github.com:acme/widgets.git", "github.com/acme/widgets"},
		{"ssh://git@ghe.example.com:2222/acme/widgets.git", "ghe.example.com/acme/widgets"},
		{"/srv/git/widgets.git", ""},
		{"file:///srv/git/widgets.git", ""},
		{"https://gitlab.example.com/group/sub/widgets.git", ""},
		{"", ""},
	} {
		if got := BindingFromRemote(tt.remote); got != tt.want {
			t.Errorf("BindingFromRemote(%q) = %q, want %q", tt.remote, got, tt.want)
		}
	}
}

func TestParseForgeBinding(t *testing.T) {
	binding, err := ParseForgeBinding("github.com/acme/widgets")
	if err != nil || binding != (ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}) || binding.String() != "github.com/acme/widgets" {
		t.Errorf("parsed %+v: %v", binding, err)
	}
	for _, bad := range []string{"acme/widgets", "github.com/acme/widgets/more", "github.com//widgets", "github.com/-acme/widgets", "github.com/acme/wid gets", "github.com/../widgets"} {
		if _, err := ParseForgeBinding(bad); !errors.Is(err, ErrInvalidRepository) {
			t.Errorf("ParseForgeBinding(%q): %v", bad, err)
		}
	}
}
