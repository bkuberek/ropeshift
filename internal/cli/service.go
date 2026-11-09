package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ropeshift.dev/ropeshift/internal/api"
)

// Service is a service definition on this machine: the name its manager knows it by, and its file.
type Service struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ErrServiceUsage means a service's name, or a flag meant for the daemon, is invalid.
var ErrServiceUsage = errors.New("invalid service")

// errNoServiceManager means this platform has no service manager ropeshift writes for.
var errNoServiceManager = errors.New("this platform has no service manager ropeshift writes for")

// serviceInstall writes the daemon's service and starts it (NFR-INST-2). Everything after -- goes to
// `ropeshiftd solo` as it is.
func (c *invocation) serviceInstall(args []string) int {
	ours, daemonArgs := args, []string(nil)
	if at := slices.Index(args, "--"); at >= 0 {
		ours, daemonArgs = args[:at], slices.Clone(args[at+1:])
	}
	set := c.flags()
	name := set.String("name", "", "the service's name, by default this platform's")
	positional, ok := c.parse(set, ours)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q: the daemon's flags go after --", positional[0])
	}
	if c.install == nil {
		return c.serviceFailure(errNoServiceManager)
	}
	installed, err := c.install(c.ctx, *name, daemonArgs)
	if err != nil {
		return c.serviceFailure(err)
	}
	return c.finish(nil, installed, func() string {
		return fmt.Sprintf("installed %s at %s, and started it\n", installed.Name, installed.Path)
	})
}

// serviceUninstall stops the daemon's service and removes its definition. The lanes it ran keep
// running, and the daemon adopts them when it starts again.
func (c *invocation) serviceUninstall(args []string) int {
	set := c.flags()
	name := set.String("name", "", "the service's name, by default this platform's")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if c.uninstall == nil {
		return c.serviceFailure(errNoServiceManager)
	}
	removed, err := c.uninstall(c.ctx, *name)
	if err != nil {
		return c.serviceFailure(err)
	}
	return c.finish(nil, removed, func() string {
		return fmt.Sprintf("stopped %s and removed %s, which stops no lane\n", removed.Name, removed.Path)
	})
}

// serviceFailure explains a failure that never reached the control plane.
func (c *invocation) serviceFailure(err error) int {
	detail := api.ErrorDetail{Code: api.CodeUnavailable, Message: err.Error()}
	exit := ExitError
	switch {
	case errors.Is(err, ErrServiceUsage):
		detail.Code, exit = api.CodeInvalid, ExitUsage
	case errors.Is(err, os.ErrNotExist):
		detail.Code, exit = api.CodeNotFound, ExitNotFound
	}
	c.printError(detail)
	return exit
}
