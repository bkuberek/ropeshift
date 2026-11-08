package cli

import (
	"errors"
	"fmt"
	"net/http"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
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
	positional, ok := parse(set, args)
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
// the current passphrase through the path every change takes.
func (c *invocation) passphraseSet(args []string) int {
	set := c.flags()
	positional, ok := parse(set, args)
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
	err = client.SetPassphrase(c.ctx, api.SetPassphrase{Passphrase: passphrase})
	return c.finish(err, struct {
		Set bool `json:"set"`
	}{true}, func() string { return "passphrase set\n" })
}
