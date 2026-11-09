package entry

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/notify"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ports"
)

// desktopEnv is what a desktop program needs to find a person's session, and all it gets.
var desktopEnv = []string{"HOME", "PATH", "DISPLAY", "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"}

// notifyDeps is every way a channel reaches past this process, so a test can hold all of them. A nil
// Dial is the webhook's own.
type notifyDeps struct {
	Dial      notify.Dial
	Processes ports.ProcessRunner
	LookPath  func(string) (string, error)
	Lookup    func(string) (string, bool)
	GOOS      string
}

func liveNotifyDeps() notifyDeps {
	return notifyDeps{Processes: osexec.Runner{}, LookPath: exec.LookPath, Lookup: os.LookupEnv, GOOS: runtime.GOOS}
}

func (d notifyDeps) environment() []string {
	var env []string
	for _, name := range desktopEnv {
		if value, ok := d.Lookup(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// channelsFor says what a policy turns on. The web channel sends nothing off the machine, so it is
// always on, and every other channel exists only because a member published one.
func channelsFor(deps notifyDeps) func(authority.NotifyPolicy) []ports.Notifier {
	return func(policy authority.NotifyPolicy) []ports.Notifier {
		channels := []ports.Notifier{notify.Web{}}
		if policy.Desktop {
			channels = append(channels, notify.NewDesktop(notify.DesktopConfig{
				GOOS: deps.GOOS, Processes: deps.Processes, LookPath: deps.LookPath, Env: deps.environment(),
			}))
		}
		if policy.WebhookURL != "" {
			key := ""
			if policy.WebhookSecretEnv != "" {
				key, _ = deps.Lookup(policy.WebhookSecretEnv)
			}
			channels = append(channels, notify.NewWebhook(notify.WebhookConfig{URL: policy.WebhookURL, Key: key, Dial: deps.Dial}))
		}
		return channels
	}
}

// noticeTick tells members what waits. It reads the policy each pass, so a publish takes effect with no
// restart, and what it reports of a failure is what a channel said, which holds no address.
func noticeTick(config server.Config, deps notifyDeps, every time.Duration) control.Tick {
	notices := control.NewNotices(config.Store, channelsFor(deps))
	waiting := server.Waiting(config)
	return control.Tick{
		Name:  "notices",
		Every: every,
		Run: func(ctx context.Context) error {
			items, err := waiting(ctx)
			if err != nil {
				return err
			}
			return notices.Deliver(ctx, config.Scope, items, tickActor("notify-tick"))
		},
	}
}
