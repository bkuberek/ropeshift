package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
)

// askPassphrase asks on the terminal, and only there. With no terminal, nothing can ask, so
// the refusal explains what a person should do.
func (c *invocation) askPassphrase(prompt string) (string, error) {
	if c.readPassphrase == nil {
		return "", noTerminal(errors.New("this process has none"))
	}
	passphrase, err := c.readPassphrase(prompt)
	if err != nil {
		return "", noTerminal(err)
	}
	return passphrase, nil
}

func noTerminal(cause error) error {
	return &api.Error{Status: http.StatusUnauthorized, Detail: api.ErrorDetail{
		Code: api.CodeSessionRequired,
		Message: fmt.Sprintf("a passphrase comes only from a terminal (%v): run this in one, or run eval \"$(ropeshift login)\" there first",
			cause),
	}}
}

// login prints a session for one shell to eval. It goes to stdout and nowhere else: no file
// holds it, and a lane cannot inherit it (D26).
func (c *invocation) login(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	passphrase, err := c.askPassphrase("Passphrase: ")
	if err != nil {
		return c.failure(err)
	}
	client := c.client()
	defer client.Close()
	session, err := client.SignIn(c.ctx, api.SignIn{Passphrase: passphrase, TTLMs: authority.LoginTTLMs})
	return c.finish(err, session, func() string {
		return fmt.Sprintf("export %s=%s\n", authority.EnvMemberToken, session.Token)
	})
}

// passphraseSet sets the passphrase. The first needs no session, and a later one asks for
// the current passphrase through the path every change takes. A later one ends every session
// of the member, the one it came from included, and says so.
func (c *invocation) passphraseSet(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	passphrase, err := c.askPassphrase("New passphrase: ")
	if err != nil {
		return c.failure(err)
	}
	again, err := c.askPassphrase("Again: ")
	if err != nil {
		return c.failure(err)
	}
	if passphrase != again {
		return c.usageError("the two passphrases differ")
	}
	client := c.client()
	defer client.Close()
	// A passphrase set before this one makes it a later set. The first needs no session and ends none, so an exported
	// token that opens nothing, which the first set passes over, does not make it one.
	before, err := client.Passphrase(c.ctx)
	if err != nil {
		return c.failure(err)
	}
	changed, err := client.SetPassphrase(c.ctx, api.SetPassphrase{Passphrase: passphrase})
	// A set the daemon refused for want of a session, which the command then opened, is a later one whatever was
	// read before it.
	later := c.asked || (before.Set && c.session != "")
	return c.finish(err, struct {
		Set           bool  `json:"set"`
		SessionsEnded int64 `json:"sessions_ended"`
		Delegates     int64 `json:"delegates"`
	}{true, changed.SessionsEnded, changed.Delegates}, func() string {
		return passphraseSetText(changed, later, c.session != "")
	})
}

// passphraseSetText says what a set did. later says it came from a session, and exported that the session was the
// one this shell exported, which no longer opens anything.
func passphraseSetText(changed api.PassphraseChanged, later, exported bool) string {
	if !later {
		return "passphrase set\n"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "passphrase set\nEnded %s.\n", plural(int(changed.SessionsEnded), "other session", "other sessions"))
	switch {
	case changed.Delegates == 1:
		text.WriteString("1 delegate credential still acts: ropeshift delegate revoke ends it.\n")
	case changed.Delegates > 1:
		fmt.Fprintf(&text, "%d delegate credentials still act: ropeshift delegate revoke ends each.\n", changed.Delegates)
	}
	if exported {
		text.WriteString("This shell's session ended too: run eval \"$(ropeshift login)\" to sign in again.\n")
	}
	return text.String()
}
