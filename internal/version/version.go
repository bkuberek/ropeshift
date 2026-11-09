// Package version is what a binary says about its own build: the release it is, the commit it came
// from, when it was built, and whether the tree held changes. A release build stamps these with
// -ldflags -X on the variables below. Any other build reads what the Go toolchain recorded, and is a
// development build unless the toolchain recorded an exact release of the module, as go install
// module@vX.Y.Z does. docs/versioning.md states the policy.
package version

import (
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// Development is the version of every build that is not a release.
const Development = "dev"

// The release stamp. A release build sets each with -X ropeshift.com/ropeshift/internal/version.<name>:
// version as vMAJOR.MINOR.PATCH with an optional pre-release, commit as the full revision, date in
// RFC 3339, and modified as true or false.
var (
	version  string
	commit   string
	date     string
	modified string
)

type stamp struct{ version, commit, date, modified string }

// Info describes one build. Every key is present in its JSON on every build, so a reader can rely on
// the shape: commit and date are empty when the build does not know them.
type Info struct {
	Command  string `json:"command"`
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Modified bool   `json:"modified"`
	Go       string `json:"go"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// String is the line `version` prints: the command and version, then whatever else is known.
func (i Info) String() string {
	var details []string
	if i.Commit != "" {
		details = append(details, "commit "+short(i.Commit))
	}
	if i.Date != "" {
		details = append(details, "built "+i.Date)
	}
	if i.Modified {
		details = append(details, "modified")
	}
	details = append(details, i.Go, i.OS+"/"+i.Arch)
	return i.Command + " " + i.Version + " (" + strings.Join(details, ", ") + ")"
}

// Read describes this binary, started as command.
func Read(command string) Info {
	build, ok := debug.ReadBuildInfo()
	return resolve(command, stamp{version, commit, date, modified}, build, ok)
}

func resolve(command string, stamped stamp, build *debug.BuildInfo, ok bool) Info {
	info := Info{
		Command: command, Version: Development, Commit: stamped.commit, Date: stamped.date,
		Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
	}
	stampedModified := stamped.modified == "true" || stamped.modified == "false"
	info.Modified = stamped.modified == "true"
	var recorded string
	if ok {
		recorded = build.Main.Version
		for _, setting := range build.Settings {
			switch {
			case setting.Key == "vcs.revision" && info.Commit == "":
				info.Commit = setting.Value
			case setting.Key == "vcs.modified" && !stampedModified:
				info.Modified = setting.Value == "true"
			}
		}
	}
	switch {
	case stamped.version != "":
		info.Version = stamped.version
	case isRelease(recorded):
		info.Version = recorded
	case info.Commit == "" && isPseudo(recorded):
		info.Commit = recorded[strings.LastIndexByte(recorded, '-')+1:]
	}
	return info
}

// release is vMAJOR.MINOR.PATCH with an optional pre-release and no build metadata, which is where
// the toolchain marks a build from a tree with changes as +dirty.
var release = `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`

// pseudo is a Go pseudo-version, as golang.org/x/mod/module defines it: what the toolchain records for
// a commit no tag names.
var pseudo = `^v[0-9]+\.(0\.0-|\d+\.\d+-([^+]*\.)?0\.)\d{14}-[A-Za-z0-9]+(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`

// The patterns compile when a build is described, not when the package loads, because every process,
// hooks included, imports this package and only the version command and the MCP server describe one.
func isRelease(v string) bool { return regexp.MustCompile(release).MatchString(v) && !isPseudo(v) }

func isPseudo(v string) bool { return regexp.MustCompile(pseudo).MatchString(v) }

func short(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}
