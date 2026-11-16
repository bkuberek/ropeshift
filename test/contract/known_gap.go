package contract

import (
	"errors"
)

// knownGap marks a check, or the part of one, that asserts behavior the product does not have yet.
// A gap's ID names the gap in the design that decides its fix, such as GAP-MP-G3.
//
//   - A Violation skips the check and names the gap, so the run stays green while the gap stands and
//     every skip line says what is missing.
//   - A nil error fails the check, because the gap is fixed and the marker has to go. A fix cannot
//     leave its marker behind, and a later regression of the fix then fails the check as any does.
//   - Any other error is returned as it is, so a harness fault still fails the run.
//
// A marker is removed in the change that fixes its gap, and that change adds a broken variant to
// the chaos suite for the check. A variant runs a check with the marker gone: while the marker
// stands, the check skips instead of failing, which the chaos suite reports as an error rather than
// as a catch.
func knownGap(r *Rig, id string, err error) error {
	r.T.Helper()
	var violation *Violation
	switch {
	case err == nil:
		r.T.Fatalf("gap %s is fixed: remove the marker", id)
	case errors.As(err, &violation):
		r.T.Skipf("known gap %s: %s", id, violation.Message)
	}
	return err
}
