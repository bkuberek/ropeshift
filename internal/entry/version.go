package entry

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	buildversion "ropeshift.com/ropeshift/internal/version"
)

// versionInfo is what `version` prints; internal/version owns it and the release stamp.
type versionInfo = buildversion.Info

// version is this binary's version, which telemetry reports as service.version. It is read once, at
// start: a release build matches no pattern to read it, and a development build compiles two small ones.
var version = buildversion.Read(commandCLI).Version

// readBuild describes this binary, started as a command.
var readBuild = buildversion.Read

// daemonAsked bounds how long `version` waits for the daemon to say which build it runs.
const daemonAsked = 2 * time.Second

// daemonHealth asks the daemon on this machine's control socket what it runs. It reports false when none
// answers, which is no fault of `version`.
var daemonHealth = func(ctx context.Context) (api.Health, bool) {
	paths, err := installationPaths()
	if err != nil {
		return api.Health{}, false
	}
	client := api.NewUnixClient(paths.ControlSocket())
	defer client.Close()
	health, err := client.Health(ctx)
	return health, err == nil
}

// versionFlags is the version command's flag set, which its help prints too.
func versionFlags(command string) (*flag.FlagSet, *bool) {
	flags := flag.NewFlagSet(command+" version", flag.ContinueOnError)
	return flags, flags.Bool("json", false, "print as JSON")
}

// daemonVersion is what a running daemon says of its own build, as `version --json` prints it.
type daemonVersion struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	Installed string `json:"installed,omitempty"`
}

// versionReport is `version --json`: this binary's build, and beside it the daemon's when one answers.
type versionReport struct {
	buildversion.Info
	Daemon  *daemonVersion `json:"daemon,omitempty"`
	Warning string         `json:"warning,omitempty"`
}

func runVersion(command string, args []string, stdout, stderr io.Writer) int {
	flags, asJSON := versionFlags(command)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "%s version: unexpected argument %q\n", command, flags.Arg(0))
		return exitUsage
	}
	report := versionReport{Info: readBuild(command)}
	if command == commandCLI {
		ctx, cancel := context.WithTimeout(context.Background(), daemonAsked)
		defer cancel()
		if health, ok := daemonHealth(ctx); ok {
			report.Daemon = &daemonVersion{Version: health.Version, Commit: health.Commit, Date: health.Date, Installed: health.Installed}
			report.Warning = daemonWarning(health, report.Info)
		}
	}
	if !*asJSON {
		fmt.Fprintln(stdout, report.Info.String())
		if report.Daemon != nil {
			fmt.Fprintln(stdout, "daemon "+cmp.Or(report.Daemon.Version, "(version not reported)"))
		}
		if report.Warning != "" {
			fmt.Fprintln(stdout, "warning: "+report.Warning)
		}
		return exitOK
	}
	encoder := json.NewEncoder(stdout)
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(stderr, "%s version: %v\n", command, err)
		return exitError
	}
	return exitOK
}

// daemonWarning is the daemon's own warning, which compares the build installed at its path, or else the
// same comparison with this binary as the installed build.
func daemonWarning(health api.Health, own buildversion.Info) string {
	if health.Warning != "" {
		return health.Warning
	}
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return buildversion.Warning(health.Version, buildversion.Installed{Path: path, Version: own.Version})
}
