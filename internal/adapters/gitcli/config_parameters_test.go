package gitcli

import (
	"slices"
	"strings"
	"testing"
)

func TestParseParametersReadsWhatGitWrites(t *testing.T) {
	tests := map[string]struct {
		text string
		want [][2]string
	}{
		"one":              {`'a.b'='c'`, [][2]string{{"a.b", "c"}}},
		"two":              {`'a.b'='c' 'd.e'='f'`, [][2]string{{"a.b", "c"}, {"d.e", "f"}}},
		"an empty value":   {`'g.h'=''`, [][2]string{{"g.h", ""}}},
		"no value":         {`'e.f'= 'g.h'='x'`, [][2]string{{"e.f", "true"}, {"g.h", "x"}}},
		"a quote":          {`'c.d'='it'\''s'`, [][2]string{{"c.d", "it's"}}},
		"a bang":           {`'i.j'=''\!'x'`, [][2]string{{"i.j", "!x"}}},
		"spaces":           {`'k.l'='a b\c'\!''`, [][2]string{{"k.l", `a b\c!`}}},
		"an equals sign":   {`'a.b'='x=y'`, [][2]string{{"a.b", "x=y"}}},
		"the older format": {`'a.b=c' 'd.e'`, [][2]string{{"a.b", "c"}, {"d.e", "true"}}},
		"nothing":          {``, nil},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := parseParameters(tt.text)
			if !ok || !slices.Equal(got, tt.want) {
				t.Errorf("parseParameters(%q) = %q, %v; want %q", tt.text, got, ok, tt.want)
			}
		})
	}
}

func TestParseParametersRefusesAValueGitDoesNotWrite(t *testing.T) {
	for _, text := range []string{`a.b=c`, `'a.b'='c`, `'a.b'='c'x`, `'a.b'x`, `'a.b'='c'\`} {
		if got, ok := parseParameters(text); ok {
			t.Errorf("parseParameters(%q) = %q, want a refusal", text, got)
		}
	}
}

// A setting git's -c left in the environment stays, after the ones the environment's count holds, and the variable
// itself goes, so a setting a command adds beats both.
func TestFoldParametersKeepsTheSettingsInTheCountAndInTheParameters(t *testing.T) {
	env := Git{Env: []string{
		"PATH=/usr/bin", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=a.first", "GIT_CONFIG_VALUE_0=one",
		`GIT_CONFIG_PARAMETERS='a.second'='two' 'core.hooksPath'='/hooks'`,
	}, Config: [][2]string{{"core.hooksPath", "/dev/null"}}}.environment()
	if slices.ContainsFunc(env, func(entry string) bool { return strings.HasPrefix(entry, "GIT_CONFIG_PARAMETERS=") }) {
		t.Errorf("the environment still holds GIT_CONFIG_PARAMETERS: %q", env)
	}
	got := configured(t, env)
	for key, want := range map[string]string{"a.first": "one", "a.second": "two", "core.hooksPath": "/dev/null", "maintenance.auto": "false"} {
		if got[key] != want {
			t.Errorf("%s is %q, want %q, in %v", key, got[key], want, got)
		}
	}
}
