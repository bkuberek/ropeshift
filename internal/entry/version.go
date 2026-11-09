package entry

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	buildversion "ropeshift.com/ropeshift/internal/version"
)

// versionInfo is what `version` prints; internal/version owns it and the release stamp.
type versionInfo = buildversion.Info

// version is this binary's version, which telemetry reports as service.version. It is read once, at
// start: a release build matches no pattern to read it, and a development build compiles two small ones.
var version = buildversion.Read(commandCLI).Version

// versionFlags is the version command's flag set, which its help prints too.
func versionFlags(command string) (*flag.FlagSet, *bool) {
	flags := flag.NewFlagSet(command+" version", flag.ContinueOnError)
	return flags, flags.Bool("json", false, "print as JSON")
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
	info := buildversion.Read(command)
	if !*asJSON {
		fmt.Fprintln(stdout, info.String())
		return exitOK
	}
	encoder := json.NewEncoder(stdout)
	if err := encoder.Encode(info); err != nil {
		fmt.Fprintf(stderr, "%s version: %v\n", command, err)
		return exitError
	}
	return exitOK
}
