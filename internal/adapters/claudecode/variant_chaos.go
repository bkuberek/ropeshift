//go:build chaos

package claudecode

import "os"

// EnvSabotage names a broken variant of the driver, to prove that a check can fail. The
// chaos build holds this seam, and a normal build has none.
const EnvSabotage = "ROPESHIFT_CHAOS_DRIVER"

// The variants.
const (
	// SabotageBlindConfirm confirms the trust dialog from wherever its focus stands.
	SabotageBlindConfirm = "blind-confirm"
	// SabotageRepeatBrief sends a brief that may already have arrived.
	SabotageRepeatBrief = "repeat-brief"
)

func sabotagedFocus(focus focusLine) focusLine {
	if os.Getenv(EnvSabotage) == SabotageBlindConfirm {
		return focusOnAccept
	}
	return focus
}

func repeatsBrief() bool { return os.Getenv(EnvSabotage) == SabotageRepeatBrief }
