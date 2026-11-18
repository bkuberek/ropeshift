package ledger

import (
	"slices"
	"strings"
	"testing"
)

// Each rule of the set finds its shape and names itself, leaves the words around a value, and keeps what the forge
// already masked.
func TestEachCredentialRuleFindsItsShape(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		rule, text string
		secret     string
	}{
		{"github_token", "push with ghp_abcdefghijklmnopqrstuvwxyz0123456789 done", "abcdefghijklmnopqrstuvwxyz0123"},
		{"github_pat", "github_pat_11ABCDEFG0abcdefghijklmnopqrstuvwxyz_0123456789", "11ABCDEFG0abcdefghij"},
		{"gitlab_token", "token glpat-abcdefghij0123456789 here", "abcdefghij0123456789"},
		{"npm_token", "//registry.npmjs.org/:_authToken npm_abcdefghijklmnopqrstuvwxyz0123456789", "abcdefghijklmnopqrstuvwxyz0123456789"},
		{"aws_access_key", "AKIAIOSFODNN7EXAMPLE", "IOSFODNN7EXAMPLE"},
		{"aws_secret_key", "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "wJalrXUtnFEMI/K7MDENG"},
		{"aws_secret_key", `AWS_SECRET_ACCESS_KEY="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`, "wJalrXUtnFEMI/K7MDENG"},
		{"stripe_key", "sk_live_51HabcdefGHIJKLmnopqr", "51HabcdefGHIJKL"},
		{"stripe_key", "rk_live_51HabcdefGHIJKLmnopqr", "51HabcdefGHIJKL"},
		{"google_api_key", "key=AIzaSyA-abcdefghijklmnopqrstuvwxyz01234", "SyA-abcdefghijkl"},
		{"slack_token", "xoxb-1234567890-abcdefghijkl", "1234567890-abcd"},
		{"api_key", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz", "api03-abcdefghij"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijk", "eyJzdWIi"},
		{"bearer_token", "Authorization: Bearer abcdef0123456789.xyz", "abcdef0123456789"},
		{"url_credentials", "git clone https://user:hunter2@example.com/x.git", "hunter2"},
		{"secret_assignment", "DEPLOY_TOKEN=s3cr3tvalue123", "s3cr3tvalue123"},
		{"secret_assignment", "export MY_SECRET: s3cr3tvalue123", "s3cr3tvalue123"},
		{"secret_assignment", `password="correct horse"`, "correct"},
		{"secret_assignment", "api_key=abcd1234efgh", "abcd1234efgh"},
		{"secret_assignment", "PASSWD:hunter22", "hunter22"},
		{"private_key", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJB\nabc\n-----END RSA PRIVATE KEY-----", "MIIBOgIBAAJB"},
	} {
		got := RedactCredentials(tt.text)
		if !strings.Contains(got, "[redacted: "+tt.rule+"]") {
			t.Errorf("%s: %q reads %q, want the rule named", tt.rule, tt.text, got)
		}
		if strings.Contains(got, tt.secret) {
			t.Errorf("%s: %q still holds %q: %q", tt.rule, tt.text, tt.secret, got)
		}
	}
	// A pair keeps its name, so a line still says which variable held what was removed.
	if got := RedactCredentials("DEPLOY_TOKEN=s3cr3tvalue123 next"); got != "DEPLOY_TOKEN=[redacted: secret_assignment] next" {
		t.Errorf("an assignment reads %q", got)
	}
}

func TestAMaskedOrShortValueIsNoCredential(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"password: ***", "API_TOKEN=****", "SECRET=***\nBearer ***\nToken ***", "token=abc", "secret: no", "the password is rotated daily",
		"TestWidget/retries_a_payment passed in 0.4s", "AKIA is a prefix", "skip=1 sk_live_ is a prefix",
	} {
		if got := RedactCredentials(text); got != text {
			t.Errorf("%q reads %q, want it kept", text, got)
		}
	}
}

// A value a rule already replaced is not replaced again by the assignment rule.
func TestAValueAlreadyRedactedIsLeftAlone(t *testing.T) {
	t.Parallel()
	got := RedactCredentials("GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789")
	if got != "GITHUB_TOKEN=[redacted: github_token]" {
		t.Errorf("a token in an assignment reads %q", got)
	}
}

// F1: a key whose start was cut off by the window the log was read through is redacted from the start of the text to
// its END line, and a whole key, and a key cut off at the end, as before.
func TestAPrivateKeyCutByAWindowIsRedactedFromTheStart(t *testing.T) {
	t.Parallel()
	tail := "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7\nZm9vYmFyYmF6\n-----END PRIVATE KEY-----\nTestWidget FAIL\n"
	got := RedactCredentials(tail)
	if strings.Contains(got, "MIIEvQ") || strings.Contains(got, "Zm9vYmFy") || !strings.Contains(got, "[redacted: private_key]") || !strings.HasSuffix(got, "\nTestWidget FAIL\n") {
		t.Errorf("a key cut at its start reads %q", got)
	}
	whole := RedactCredentials("before\n-----BEGIN PRIVATE KEY-----\nZm9vYmFy\n-----END PRIVATE KEY-----\nafter")
	if whole != "before\n[redacted: private_key]\nafter" {
		t.Errorf("a whole key reads %q", whole)
	}
	cutAtEnd := RedactCredentials("before\n-----BEGIN OPENSSH PRIVATE KEY-----\nZm9vYmFy")
	if cutAtEnd != "before\n[redacted: private_key]" {
		t.Errorf("a key cut at its end reads %q", cutAtEnd)
	}
	// Through the excerpt a worker and the control plane both make.
	excerpt, _ := CIExcerpt(tail, 200, 8192)
	if strings.Contains(excerpt, "MIIEvQ") || strings.Contains(excerpt, "END PRIVATE KEY") {
		t.Errorf("the excerpt of a key cut at its start reads %q", excerpt)
	}
}

func TestTheRuleSetIsVersionedAndNamed(t *testing.T) {
	t.Parallel()
	if CIRedactionRulesVersion < 1 {
		t.Errorf("the rule set has version %d", CIRedactionRulesVersion)
	}
	names := RedactionRuleNames()
	for _, want := range []string{"private_key", "aws_secret_key", "stripe_key", "google_api_key", "gitlab_token", "npm_token", "secret_assignment"} {
		if !slices.Contains(names, want) {
			t.Errorf("the rule set lacks %s: %v", want, names)
		}
	}
}
