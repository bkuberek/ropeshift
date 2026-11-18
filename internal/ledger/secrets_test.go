package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// secretShapes holds one text for each rule of the secret scan, built from parts so no credential sits in this file.
var secretShapes = []struct{ rule, text, secret string }{
	{"private_key", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJB\nabc\n-----END RSA PRIVATE KEY-----", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJB\nabc\n-----END RSA PRIVATE KEY-----"},
	{"github_pat", "x = " + "github_pat_" + "11ABCDEFG0abcdefghijklmnopqrstuvwxyz_0123456789", "github_pat_" + "11ABCDEFG0abcdefghijklmnopqrstuvwxyz_0123456789"},
	{"github_token", "push with " + "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789 done", "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"},
	{"gitlab_token", "token " + "glpat-" + "abcdefghij0123456789 here", "glpat-" + "abcdefghij0123456789"},
	{"npm_token", "_authToken " + "npm_" + "abcdefghijklmnopqrstuvwxyz0123456789", "npm_" + "abcdefghijklmnopqrstuvwxyz0123456789"},
	{"aws_access_key", "id " + "AKIA" + "IOSFODNN7EXAMPLE", "AKIA" + "IOSFODNN7EXAMPLE"},
	{"aws_secret_key", "aws_secret_access_key = " + "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
	{"stripe_key", "key " + "sk_live_" + "51HabcdefGHIJKLmnopqr", "sk_live_" + "51HabcdefGHIJKLmnopqr"},
	{"google_api_key", "key=" + "AIza" + "SyA-abcdefghijklmnopqrstuvwxyz01234", "AIza" + "SyA-abcdefghijklmnopqrstuvwxyz01234"},
	{"slack_token", "hook " + "xoxb-" + "1234567890-abcdefghijkl", "xoxb-" + "1234567890-abcdefghijkl"},
	{"api_key", "key " + "sk-ant-" + "api03-abcdefghijklmnopqrstuvwxyz", "sk-ant-" + "api03-abcdefghijklmnopqrstuvwxyz"},
	{"jwt", "jwt " + "eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0" + "." + "abcdefghijk", "eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0" + "." + "abcdefghijk"},
}

func TestTheSecretRulesAreTheTwelveFixedFormatRules(t *testing.T) {
	t.Parallel()
	want := []string{
		"private_key", "github_pat", "github_token", "gitlab_token", "npm_token", "aws_access_key", "aws_secret_key",
		"stripe_key", "google_api_key", "slack_token", "api_key", "jwt",
	}
	if got := SecretRuleNames(); !slices.Equal(got, want) {
		t.Errorf("the secret rules are %v, want %v", got, want)
	}
	for _, generic := range []string{"bearer_token", "url_credentials", "secret_assignment"} {
		if slices.Contains(SecretRuleNames(), generic) {
			t.Errorf("%s is a secret rule", generic)
		}
	}
}

func TestFindSecretsFindsEachRuleWithItsDigest(t *testing.T) {
	t.Parallel()
	for _, tt := range secretShapes {
		got := FindSecrets("before\n" + tt.text + "\nafter")
		if len(got) != 1 {
			t.Errorf("%s: %d matches, want 1: %+v", tt.rule, len(got), got)
			continue
		}
		sum := sha256.Sum256([]byte(tt.secret))
		start := strings.Index("before\n"+tt.text+"\nafter", tt.secret)
		want := SecretMatch{Rule: tt.rule, Start: start, End: start + len(tt.secret), Digest: hex.EncodeToString(sum[:])}
		if got[0] != want {
			t.Errorf("%s: found %+v, want %+v", tt.rule, got[0], want)
		}
	}
}

func TestGenericShapesAreNoSecrets(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"password=hunter2", "DEPLOY_TOKEN=s3cr3tvalue123", "Authorization: Bearer abcdef0123456789.xyz",
		"git clone https://user:hunter2@example.com/x.git", "api_key=abcd1234efgh", "the key was cut\nMIIEvQ\n-----END PRIVATE KEY-----\n",
	} {
		if got := FindSecrets(text); len(got) != 0 {
			t.Errorf("%q has secret matches %+v", text, got)
		}
	}
}

// A secret inside a key's block is part of the block and is not found a second time.
func TestASecretInsideAPrivateKeyIsOneMatch(t *testing.T) {
	t.Parallel()
	text := "-----BEGIN PRIVATE KEY-----\n" + "AKIA" + "IOSFODNN7EXAMPLE\n-----END PRIVATE KEY-----\n" + "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"
	got := FindSecrets(text)
	if len(got) != 2 || got[0].Rule != "private_key" || got[1].Rule != "github_token" {
		t.Errorf("found %+v", got)
	}
}

func TestSecretMatchHoldsNoText(t *testing.T) {
	t.Parallel()
	for _, field := range reflect.VisibleFields(reflect.TypeOf(SecretMatch{})) {
		if field.Type.Kind() == reflect.String && field.Name != "Rule" && field.Name != "Digest" {
			t.Errorf("SecretMatch.%s is a string that could hold text", field.Name)
		}
	}
	token := "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"
	out, err := json.Marshal(FindSecrets("x " + token))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), token) || strings.Contains(string(out), token[4:]) {
		t.Errorf("a match marshals to %s", out)
	}
}

// Marking the secret rules leaves what CI log redaction applies, and in what order, as it was.
func TestRedactionStillAppliesEveryRule(t *testing.T) {
	t.Parallel()
	want := []string{
		"private_key", "private_key", "github_pat", "github_token", "gitlab_token", "npm_token", "aws_access_key", "aws_secret_key",
		"stripe_key", "google_api_key", "slack_token", "api_key", "jwt", "bearer_token", "url_credentials", "secret_assignment",
	}
	if got := RedactionRuleNames(); !slices.Equal(got, want) {
		t.Errorf("the redaction rules are %v, want %v", got, want)
	}
	for _, tt := range secretShapes {
		if got := RedactCredentials(tt.text); strings.Contains(got, tt.secret) || !strings.Contains(got, "[redacted: "+tt.rule+"]") {
			t.Errorf("%s: %q reads %q", tt.rule, tt.text, got)
		}
	}
	for text, want := range map[string]string{
		"password=hunter2 ok":                        "password=[redacted: secret_assignment] ok",
		"Authorization: Bearer abcdef0123456789.xyz": "Authorization: [redacted: bearer_token]",
		"https://user:hunter2@example.com/x":         "https://[redacted: url_credentials]@example.com/x",
	} {
		if got := RedactCredentials(text); got != want {
			t.Errorf("%q reads %q, want %q", text, got, want)
		}
	}
}
