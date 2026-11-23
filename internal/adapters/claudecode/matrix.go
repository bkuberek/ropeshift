package claudecode

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
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
	// SurfaceMaxBudget is `--max-budget-usd` on a headless session: a client that reaches the cap ends its turn with
	// a result of subtype `error_max_budget_usd`. The driver passes the flag only where the check passed, because the
	// flag was never reached in the spikes, and then only as a backstop for a worker that is slow to stop a lane.
	SurfaceMaxBudget lanes.Surface = "max_budget_usd"
	// SurfaceStreamUsage is `stream_event` lines on a headless session run with `--include-partial-messages`: a
	// `message_start` that carries the tokens of an API call, and a `message_delta` that carries its output. They are what
	// a turn's spend is estimated from while it runs, because the client's own total comes only when the turn ends. On a
	// version where the check did not pass, a turn's spend is seen from its assistant messages when they carry usage, and
	// otherwise not until the turn ends, so a long turn can pass its limit before the worker sees it. The check reports
	// that, and degrades nothing.
	SurfaceStreamUsage lanes.Surface = "stream_usage"
)

// Surfaces lists every surface the matrix holds a result for, in the matrix's order.
var Surfaces = []lanes.Surface{
	SurfaceSocketLine, SurfaceReceipts, SurfacePeerMessageHold, SurfaceSocketDir,
	SurfaceUnifiedWindows, SurfaceCommandLifecycle, SurfacePrintKeepsLogin, SurfaceStartScreens, SurfaceMaxBudget,
	SurfaceStreamUsage,
}

// noted are surfaces a check reports and the driver does not use to run, so their result degrades
// nothing: the design does not need `command_lifecycle`, no state follows from seeing a hold, the budget flag is a
// backstop, so a version without it runs as well, held to its limit by the worker alone, and the stream's usage is read
// where it comes and its absence is what the check reports.
var noted = []lanes.Surface{SurfaceCommandLifecycle, SurfacePeerMessageHold, SurfaceMaxBudget, SurfaceStreamUsage}

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

// judged sets Degraded: a surface this driver relies on did not pass. The login check and the windows count
// wherever a probe ran on anything but an API key, because a sign-in nobody could read is a
// subscription and the figures belong to a subscription, and the screens count only for a driver with a terminal.
func judged(q lanes.Qualification, interactive bool) lanes.Qualification {
	q.Degraded = false
	for _, surface := range Surfaces {
		switch {
		case isNoted(surface):
			continue
		case (surface == SurfacePrintKeepsLogin || surface == SurfaceUnifiedWindows) && q.Source == lanes.QualifiedByProbe && q.SignIn == ports.SignInAPIKey:
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

// ProbeChecks lists the checks a probe runs for a worker on this sign-in: every surface of the matrix, less the login
// and the windows on an API key, which cannot read a subscription's login or its window figures, and the screens when
// the driver has no terminal. A result holds for its version only if it passed every one of them.
func (d *Driver) ProbeChecks(signIn string) []lanes.Surface {
	out := make([]lanes.Surface, 0, len(Surfaces))
	for _, surface := range Surfaces {
		switch {
		case (surface == SurfacePrintKeepsLogin || surface == SurfaceUnifiedWindows) && signIn == ports.SignInAPIKey:
		case surface == SurfaceStartScreens && d.terminal == nil:
		default:
			out = append(out, surface)
		}
	}
	return out
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
	// BudgetFlag is `--max-budget-usd` on a headless generation, as the client's own cap (FR-SPEND-5).
	BudgetFlag bool
}

// UseOf reads a qualification as uses. A surface is used only when its own check passed, so a
// failure and a check that could not run take away the same thing. The start screens have no use
// here: the start check matches them exactly and parks a lane on any other screen, which is what
// the matrix says a failure leads to.
func UseOf(q lanes.Qualification) Use {
	return Use{
		PeerInjection: q.Passed(SurfaceSocketLine), Receipts: q.Passed(SurfaceReceipts),
		PrivateSockets: q.Passed(SurfaceSocketDir), WindowFigures: q.Passed(SurfaceUnifiedWindows),
		HeadlessOnSubscription: q.Passed(SurfacePrintKeepsLogin), BudgetFlag: q.Passed(SurfaceMaxBudget),
	}
}

// Judge sets Degraded for a driver with or without a terminal.
func Judge(q lanes.Qualification, interactive bool) lanes.Qualification {
	return judged(q, interactive)
}

// versionWord is what the first word of `claude --version` holds: a dotted number, which a pre-release
// or build suffix may follow.
var versionWord = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+([-+][0-9A-Za-z.-]+)?$`)

// parseVersion reads the first word of `claude --version`, and is empty when that word is no version.
func parseVersion(output string) string {
	words := strings.Fields(output)
	if len(words) == 0 || !versionWord.MatchString(words[0]) {
		return ""
	}
	return words[0]
}
