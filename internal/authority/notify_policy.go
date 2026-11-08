package authority

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// PolicyNotify says which channels a workspace tells a member through, beyond the web UI, which sends
// nothing anywhere. With no version of it published, nothing leaves the machine (FR-API-4).
const PolicyNotify PolicyKind = "notify"

// ErrBadNotifyPolicy marks a notify policy a workspace cannot run on.
var ErrBadNotifyPolicy = errors.New("invalid notify policy")

// secretEnvName is what the name of a variable looks like, so a key pasted in its place is refused and
// never reaches the ledger.
var secretEnvName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// NotifyPolicy is the body of a notify policy version. It holds where a notice goes and never a secret:
// the key that signs a delivery stays in the daemon's environment, and this names the variable.
type NotifyPolicy struct {
	Version          int64  `toml:"version" json:"version"`
	Desktop          bool   `toml:"desktop" json:"desktop"`
	WebhookURL       string `toml:"webhook_url" json:"webhook_url,omitempty"`
	WebhookSecretEnv string `toml:"webhook_secret_env" json:"webhook_secret_env,omitempty"`
}

// NotifyPolicyFile is the table a member publishes.
type NotifyPolicyFile struct {
	NotifyPolicy NotifyPolicy `toml:"notify_policy"`
}

// NotifyPolicyDraft is a notify policy as written, and what it says.
type NotifyPolicyDraft struct {
	Body   string
	Policy NotifyPolicy
}

// BuiltInNotifyPolicyBody is what a workspace runs on until a member publishes one, which configures
// nothing, so nothing goes out.
const BuiltInNotifyPolicyBody = `[notify_policy]
version = 1
`

// DefaultNotifyPolicy is what BuiltInNotifyPolicyBody says.
func DefaultNotifyPolicy() NotifyPolicy { return NotifyPolicy{Version: 1} }

// checkWebhookURL refuses an address that would carry a secret, or that would carry a notice in the
// clear past this machine. A userinfo part and a query are where a URL holds a credential, and a URL
// ends up in logs and proxies, so neither is ever allowed. A path cannot be told from a token, which
// is why a key goes in the signature and docs/notifications.md says not to put one here.
func checkWebhookURL(url string) error {
	rest, secure := strings.CutPrefix(url, "https://")
	if !secure {
		var plain bool
		if rest, plain = strings.CutPrefix(url, "http://"); !plain {
			return fmt.Errorf("%w: a webhook is https", ErrBadNotifyPolicy)
		}
	}
	host, _, _ := strings.Cut(rest, "/")
	if host == "" || strings.ContainsAny(url, "@?# \t\r\n") {
		return fmt.Errorf("%w: a webhook address holds a host and a path and nothing else", ErrBadNotifyPolicy)
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(host, "[::1]"), ":")
	if !secure && name != "127.0.0.1" && name != "localhost" && name != "" {
		return fmt.Errorf("%w: only a loopback webhook may go without https", ErrBadNotifyPolicy)
	}
	return nil
}

// Check accepts a policy a workspace can run on.
func (p NotifyPolicy) Check() error {
	if p.Version < 1 {
		return fmt.Errorf("%w: version %d", ErrBadNotifyPolicy, p.Version)
	}
	if p.WebhookSecretEnv != "" {
		if p.WebhookURL == "" {
			return fmt.Errorf("%w: a signing key with no webhook", ErrBadNotifyPolicy)
		}
		if !secretEnvName.MatchString(p.WebhookSecretEnv) {
			return fmt.Errorf("%w: webhook_secret_env names a variable, and it never holds the key", ErrBadNotifyPolicy)
		}
	}
	if p.WebhookURL != "" {
		return checkWebhookURL(p.WebhookURL)
	}
	return nil
}

// NewNotifyPolicyVersion stores a draft under a generated ID.
func NewNotifyPolicyVersion(id, authoredBy string, draft NotifyPolicyDraft, createdAt int64) (PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicyNotify, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadNotifyPolicy reads a version's settings, once its body is what its digest covers.
func ReadNotifyPolicy(version PolicyVersion) (NotifyPolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return NotifyPolicy{}, err
	}
	var policy NotifyPolicy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return NotifyPolicy{}, err
	}
	return policy, nil
}
