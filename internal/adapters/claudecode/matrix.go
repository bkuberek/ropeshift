package claudecode

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// The surfaces this driver uses only through a check, one for each row of the qualification
// matrix's checks for undocumented surfaces.
const (
	// SurfaceSocketLine is the socket's message line and its `uuid`: injection of anyone's message,
	// and the worker's own input to a pane.
	SurfaceSocketLine lanes.Surface = "socket_line"
	// SurfaceReceipts is a receipt on a reply address, which is how `refused` is read.
	SurfaceReceipts lanes.Surface = "receipts"
	// SurfacePeerMessageHold is `system/peer_message_hold`, which shows a hold.
	SurfacePeerMessageHold lanes.Surface = "peer_message_hold"
	// SurfaceSocketDir is `CLAUDE_CODE_TMPDIR` moving the socket into a lane's private directory.
	SurfaceSocketDir lanes.Surface = "socket_dir"
	// SurfaceUnifiedWindows is `unifiedWindows.<window>.utilization` on a `rate_limit_event`.
	SurfaceUnifiedWindows lanes.Surface = "unified_windows"
	// SurfaceCommandLifecycle is `command_lifecycle`, which the design notes and does not use.
	SurfaceCommandLifecycle lanes.Surface = "command_lifecycle"
	// SurfacePrintKeepsLogin says `-p` still runs on a subscription's login: bare mode is not its
	// default, as `apiKeySource`, `messaging_socket_path`, and `rate_limit_event` show.
	SurfacePrintKeepsLogin lanes.Surface = "print_keeps_login"
	// SurfaceStartScreens is the known start screens the interactive start check matches.
	SurfaceStartScreens lanes.Surface = "start_screens"
)

// Surfaces lists every surface the matrix holds a result for, in the matrix's order.
var Surfaces = []lanes.Surface{
	SurfaceSocketLine, SurfaceReceipts, SurfacePeerMessageHold, SurfaceSocketDir,
	SurfaceUnifiedWindows, SurfaceCommandLifecycle, SurfacePrintKeepsLogin, SurfaceStartScreens,
}

// noted are surfaces a check reports and the driver does not use, so their result degrades
// nothing: the design does not need `command_lifecycle`, and no state follows from seeing a hold.
var noted = []lanes.Surface{SurfaceCommandLifecycle, SurfacePeerMessageHold}

//go:embed matrix.json
var shippedMatrix []byte

// matrixFile is the matrix as the binary ships it: versions, surfaces, and results only.
type matrixFile struct {
	Driver   string      `json:"driver"`
	Versions []matrixRow `json:"versions"`
}

type matrixRow struct {
	Version string                              `json:"version"`
	Checks  map[lanes.Surface]lanes.CheckResult `json:"checks"`
}

// shipped is the parsed matrix. A matrix that does not parse is a build defect, which its test
// catches, and the driver then treats every version as unqualified.
var shipped = readMatrix(shippedMatrix)

func readMatrix(raw []byte) matrixFile {
	var out matrixFile
	if err := json.Unmarshal(raw, &out); err != nil {
		return matrixFile{}
	}
	return out
}

// CheckMatrix reports what is wrong with a matrix document: a version listed twice, a surface it
// does not know, a result that is not one of the three, or a surface with no result.
func CheckMatrix(raw []byte) error {
	var file matrixFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return err
	}
	if file.Driver != DriverName {
		return fmt.Errorf("the matrix names driver %q", file.Driver)
	}
	seen := map[string]bool{}
	for _, row := range file.Versions {
		if row.Version == "" || seen[row.Version] {
			return fmt.Errorf("version %q is empty or listed twice", row.Version)
		}
		seen[row.Version] = true
		for surface, result := range row.Checks {
			if !known(surface) {
				return fmt.Errorf("%s: unknown surface %q", row.Version, surface)
			}
			switch result {
			case lanes.CheckPassed, lanes.CheckFailed, lanes.CheckUnobserved:
			default:
				return fmt.Errorf("%s: %s reads %q", row.Version, surface, result)
			}
		}
		for _, surface := range Surfaces {
			if _, ok := row.Checks[surface]; !ok {
				return fmt.Errorf("%s: no result for %s", row.Version, surface)
			}
		}
	}
	return nil
}

func known(surface lanes.Surface) bool {
	for _, s := range Surfaces {
		if s == surface {
			return true
		}
	}
	return false
}

// MatrixRow is what the shipped matrix holds of a version, and an unqualified version when it
// holds nothing. Nothing in a row extends to another version.
func MatrixRow(version string) lanes.Qualification {
	for _, row := range shipped.Versions {
		if row.Version == version {
			q := lanes.Qualification{Driver: DriverName, Version: version, Source: lanes.QualifiedByMatrix, Checks: map[lanes.Surface]lanes.CheckResult{}}
			for _, surface := range Surfaces {
				q.Checks[surface] = lanes.CheckUnobserved
				if result, ok := row.Checks[surface]; ok {
					q.Checks[surface] = result
				}
			}
			return judged(q, true)
		}
	}
	return unqualified(version)
}

// MatrixVersions lists the versions the shipped matrix holds, in its order.
func MatrixVersions() []string {
	out := make([]string, 0, len(shipped.Versions))
	for _, row := range shipped.Versions {
		out = append(out, row.Version)
	}
	return out
}

// unqualified is a version nothing has checked, on which no undocumented surface is used.
func unqualified(version string) lanes.Qualification {
	q := lanes.Qualification{Driver: DriverName, Version: version, Checks: map[lanes.Surface]lanes.CheckResult{}}
	for _, surface := range Surfaces {
		q.Checks[surface] = lanes.CheckUnobserved
	}
	q.Degraded = true
	return q
}

// judged sets Degraded: a surface this driver relies on did not pass. The login check counts only
// where a subscription's login was there to read, and the screens only for a driver with a
// terminal, because neither surface is used anywhere else.
func judged(q lanes.Qualification, interactive bool) lanes.Qualification {
	q.Degraded = false
	for _, surface := range Surfaces {
		switch {
		case isNoted(surface):
			continue
		case surface == SurfacePrintKeepsLogin && q.Source == lanes.QualifiedByProbe && q.SignIn != ports.SignInSubscription:
			continue
		case surface == SurfaceStartScreens && !interactive:
			continue
		}
		if !q.Passed(surface) {
			q.Degraded = true
		}
	}
	return q
}

func isNoted(surface lanes.Surface) bool {
	for _, s := range noted {
		if s == surface {
			return true
		}
	}
	return false
}

// Qualified reports a version the shipped matrix holds with every surface the driver relies on
// passed. A worker's probe can say otherwise for this machine.
func Qualified(version string) bool { return !MatrixRow(version).Degraded }

// Use is what a qualification lets this driver do. Each field is the one use of one surface, and
// the driver decides that use from this alone.
type Use struct {
	// PeerInjection is anyone's message and the worker's word mid-turn on the socket, and the
	// worker's own input to a pane, which has no stdin.
	PeerInjection bool
	// Receipts is a reply address, which is the only way `refused` is read.
	Receipts bool
	// PrivateSockets is `CLAUDE_CODE_TMPDIR` on a session.
	PrivateSockets bool
	// WindowFigures is reading `unifiedWindows` for the near-limit watch.
	WindowFigures bool
	// HeadlessOnSubscription is a headless lane on a subscription's login.
	HeadlessOnSubscription bool
}

// UseOf reads a qualification as uses. A surface is used only when its own check passed, so a
// failure and a check that could not run take away the same thing. The start screens have no use
// here: the start check matches them exactly and parks a lane on any other screen, which is what
// the matrix says a failure leads to.
func UseOf(q lanes.Qualification) Use {
	return Use{
		PeerInjection: q.Passed(SurfaceSocketLine), Receipts: q.Passed(SurfaceReceipts),
		PrivateSockets: q.Passed(SurfaceSocketDir), WindowFigures: q.Passed(SurfaceUnifiedWindows),
		HeadlessOnSubscription: q.Passed(SurfacePrintKeepsLogin),
	}
}

// Judge sets Degraded for a driver with or without a terminal.
func Judge(q lanes.Qualification, interactive bool) lanes.Qualification {
	return judged(q, interactive)
}

// parseVersion reads the first word of `claude --version`.
func parseVersion(output string) string {
	version, _, _ := strings.Cut(strings.TrimSpace(output), " ")
	return version
}
