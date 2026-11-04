package entry

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

// version is set at release time with -ldflags.
var version = "dev"

type versionInfo struct {
	Command string `json:"command"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func readVersion(command string) versionInfo {
	info := versionInfo{
		Command: command,
		Version: version,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if info.Version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
		info.Version = build.Main.Version
	}
	for _, setting := range build.Settings {
		if setting.Key == "vcs.revision" {
			info.Commit = setting.Value
		}
	}
	return info
}

func runVersion(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command+" version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print as JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "%s version: unexpected argument %q\n", command, flags.Arg(0))
		return exitUsage
	}
	info := readVersion(command)
	if !*asJSON {
		fmt.Fprintf(stdout, "%s %s (%s, %s/%s)\n", info.Command, info.Version, info.Go, info.OS, info.Arch)
		return exitOK
	}
	encoder := json.NewEncoder(stdout)
	if err := encoder.Encode(info); err != nil {
		fmt.Fprintf(stderr, "%s version: %v\n", command, err)
		return exitError
	}
	return exitOK
}
