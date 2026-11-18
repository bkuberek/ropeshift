package authority_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

func notifyPolicyOf(t *testing.T, body string) authority.NotifyPolicy {
	t.Helper()
	var file authority.NotifyPolicyFile
	meta, err := toml.Decode(body, &file)
	if err != nil {
		t.Fatal(err)
	}
	if unknown := meta.Undecoded(); len(unknown) != 0 {
		t.Fatalf("a setting the policy does not read: %v", unknown)
	}
	return file.NotifyPolicy
}

const spendChannelsBody = `[notify_policy]
version = 2
desktop = true
webhook_url = "https://hooks.example.com/ropeshift"

[notify_policy.spend_channels]
spend_hard_limit = ["desktop", "webhook"]
spend_soft_limit = []
`

// A member chooses the channels of each spend notice kind, beyond the web UI that always has it. With nothing chosen a
// spend notice reaches the web UI only, whatever else the policy turns on; a kind of another sort goes to every channel
// the policy turns on, as it always did (FR-SPEND-19).
func TestAPolicyChoosesTheChannelsOfEachSpendNoticeKind(t *testing.T) {
	chosen := notifyPolicyOf(t, spendChannelsBody)
	if err := chosen.Check(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		kind, channel string
		want          bool
	}{
		{ledger.NoticeSpendHardLimit, authority.NoticeChannelWeb, true},
		{ledger.NoticeSpendHardLimit, authority.NoticeChannelDesktop, true},
		{ledger.NoticeSpendHardLimit, authority.NoticeChannelWebhook, true},
		{ledger.NoticeSpendSoftLimit, authority.NoticeChannelWeb, true},
		{ledger.NoticeSpendSoftLimit, authority.NoticeChannelDesktop, false},
		{ledger.NoticeSpendSoftLimit, authority.NoticeChannelWebhook, false},
		{ledger.NoticeSpendNoProgress, authority.NoticeChannelDesktop, false},
		{ledger.NoticeSpendWaiting, authority.NoticeChannelWebhook, false},
		{ledger.NoticeSpendPolicyChanged, authority.NoticeChannelWebhook, false},
		{ledger.NoticeWork, authority.NoticeChannelDesktop, true},
		{ledger.NoticeWork, authority.NoticeChannelWebhook, true},
		{ledger.NoticeQuestions, "another", true},
	} {
		if got := chosen.Sends(tt.kind, tt.channel); got != tt.want {
			t.Errorf("%s on %s: %v, want %v", tt.kind, tt.channel, got, tt.want)
		}
	}
	// A policy that chooses nothing sends a spend notice to the web UI alone.
	plain := authority.DefaultNotifyPolicy()
	for _, kind := range ledger.SpendNoticeKinds {
		if !plain.Sends(kind, authority.NoticeChannelWeb) || plain.Sends(kind, authority.NoticeChannelDesktop) || plain.Sends(kind, authority.NoticeChannelWebhook) {
			t.Errorf("%s with no choice reaches more than the web UI", kind)
		}
	}
	// A policy that turns a channel on and chooses no spend kind for it keeps spend notices off it.
	turnedOn := notifyPolicyOf(t, "[notify_policy]\nversion = 2\ndesktop = true\n")
	if turnedOn.Sends(ledger.NoticeSpendHardLimit, authority.NoticeChannelDesktop) || !turnedOn.Sends(ledger.NoticeWork, authority.NoticeChannelDesktop) {
		t.Error("a channel a member turned on carries a spend notice nobody chose for it, or fewer of the others")
	}
}

// A webhook is signed with the value of a variable of the daemon's environment that a member names, and the webhook goes to a
// host the member chooses, so any variable a member could name would be a way to send its value off the machine. A version
// that is published names a variable that starts with the prefix of the variables that may hold a webhook key, and a version
// stored before that is still read.
func TestAPublishedPolicyNamesOnlyAVariableThatMayHoldAWebhookKey(t *testing.T) {
	t.Parallel()
	body := func(name string) string {
		return "[notify_policy]\nversion = 2\nwebhook_url = \"https://hooks.example.com/r\"\nwebhook_secret_env = \"" + name + "\"\n"
	}
	for _, name := range []string{"DATABASE_URL", "AWS_SECRET_ACCESS_KEY", "ROPESHIFT_WEBHOOK_KEY", "HOOK_ROPESHIFT_", "ROPESHIFT_HOOK_"} {
		err := notifyPolicyOf(t, body(name)).Check()
		if !errors.Is(err, authority.ErrBadNotifyPolicy) || !strings.Contains(err.Error(), ledger.WebhookSecretEnvPrefix) {
			t.Errorf("a signing variable named %s was answered %v, want a refusal that names %s", name, err, ledger.WebhookSecretEnvPrefix)
		}
	}
	for _, name := range []string{"ROPESHIFT_HOOK_NOTIFY", "ROPESHIFT_HOOK_X1_2"} {
		if err := notifyPolicyOf(t, body(name)).Check(); err != nil {
			t.Errorf("a signing variable named %s was refused: %v", name, err)
		}
	}
	// A version stored before the prefix was required reads as it did, so a daemon that holds one still delivers.
	old := authority.NotifyPolicyDraft{Body: body("DATABASE_URL"), Policy: notifyPolicyOf(t, body("DATABASE_URL"))}
	version, err := authority.NewNotifyPolicyVersion("old", "member-1", old, 1)
	if err != nil {
		t.Fatal(err)
	}
	read, err := authority.ReadNotifyPolicy(version)
	if err != nil || read.WebhookSecretEnv != "DATABASE_URL" {
		t.Errorf("a stored version reads %+v: %v", read, err)
	}
}

// A choice that names no spend kind, no channel this build has, a channel the policy leaves off, or the email the mailer
// would carry is refused with what it got wrong, because a choice that did nothing would read as turned on.
func TestAPolicyRefusesAChoiceItCannotKeep(t *testing.T) {
	for name, tt := range map[string]struct{ body, want string }{
		"a kind that is not a spend kind": {
			"[notify_policy]\nversion = 2\ndesktop = true\n[notify_policy.spend_channels]\nci_not_finished = [\"desktop\"]\n", "ci_not_finished",
		},
		"a kind nobody has": {
			"[notify_policy]\nversion = 2\ndesktop = true\n[notify_policy.spend_channels]\nspend_everything = [\"desktop\"]\n", "spend_everything",
		},
		"a channel nobody has": {
			"[notify_policy]\nversion = 2\ndesktop = true\n[notify_policy.spend_channels]\nspend_hard_limit = [\"pager\"]\n", "pager",
		},
		"the email no mailer sends": {
			"[notify_policy]\nversion = 2\n[notify_policy.spend_channels]\nspend_hard_limit = [\"email\"]\n", "email",
		},
		"the desktop turned off": {
			"[notify_policy]\nversion = 2\n[notify_policy.spend_channels]\nspend_hard_limit = [\"desktop\"]\n", "desktop",
		},
		"a webhook with no address": {
			"[notify_policy]\nversion = 2\n[notify_policy.spend_channels]\nspend_hard_limit = [\"webhook\"]\n", "webhook",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := notifyPolicyOf(t, tt.body).Check()
			if !errors.Is(err, authority.ErrBadNotifyPolicy) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("the policy was answered %v, want a refusal that names %q", err, tt.want)
			}
		})
	}
	// The web UI is named for no reason and is accepted, and a channel named twice is kept once.
	policy := notifyPolicyOf(t, "[notify_policy]\nversion = 2\ndesktop = true\n[notify_policy.spend_channels]\nspend_hard_limit = [\"web\", \"desktop\", \"desktop\"]\n")
	if err := policy.Check(); err != nil || !policy.Sends(ledger.NoticeSpendHardLimit, authority.NoticeChannelDesktop) {
		t.Errorf("a choice of the web UI and the desktop twice was answered %v", err)
	}
}
