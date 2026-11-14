package lanes

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// errNotNumstat refuses a line that is not one of `git diff --numstat`.
var errNotNumstat = errors.New("not a line of git diff --numstat")

// ChangeSizeOf reads what `git diff --numstat` printed (FR-OBS-13): one line per changed file, its lines
// added and removed and then its path, which git quotes when it holds a tab or a newline, or `-` for both
// counts on a binary file, which counts as a changed file with no lines. A line it cannot read is refused,
// never counted as zero.
func ChangeSizeOf(numstat string) (ledger.ChangeSize, error) {
	var out ledger.ChangeSize
	for line := range strings.Lines(numstat) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			return ledger.ChangeSize{}, fmt.Errorf("%w: %q", errNotNumstat, line)
		}
		out.Files++
		if fields[0] == "-" && fields[1] == "-" {
			continue
		}
		added, addedErr := strconv.ParseUint(fields[0], 10, 63)
		removed, removedErr := strconv.ParseUint(fields[1], 10, 63)
		if addedErr != nil || removedErr != nil {
			return ledger.ChangeSize{}, fmt.Errorf("%w: %q", errNotNumstat, line)
		}
		out.Added += int64(added)
		out.Removed += int64(removed)
	}
	return out, nil
}
