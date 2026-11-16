package ledger

import "strings"

// Vendor is what a person needs to know to install and sign in to one agent CLI, and the terms a
// subscription to it is held to.
type Vendor struct{ Product, Company, Command, Terms string }

var vendors = map[string]Vendor{
	"claude-code": {Product: "Claude Code", Company: "Anthropic", Command: "claude", Terms: "Anthropic's Consumer Terms"},
}

// VendorOf names the vendor of a driver. A driver the table does not hold is called by its own name.
func VendorOf(driver string) Vendor {
	if v, ok := vendors[driver]; ok {
		return v
	}
	return Vendor{Product: driver, Company: "its vendor", Command: driver, Terms: "Its vendor's terms"}
}

// The doctor's words for a driver that is not usable.
const (
	WordAbsent    = "absent"
	WordUnusable  = "unusable"
	WordSignedOut = "signed out"
)

// FailedCheck says the driver's CLI is on PATH and failed its version check, so nothing runs on it. The
// doctor's detail then names the vendor's command as it ran it, and how it failed.
func (d DriverManifest) FailedCheck() bool {
	return !d.Present && strings.HasPrefix(d.Detail, VendorOf(d.Driver).Command+" --version ")
}

// DriverResult is what the doctor says of one driver, in the words `ropeshift worker doctor` prints and the
// Workers view shows. Usable is the rule an offer applies, and Word, Reason, and Fix say why a driver is not:
// absent with the doctor's detail, unusable with the detail and the fix, or signed out with the kind of its
// sign-in. A driver that is usable has none of the three.
type DriverResult struct {
	Driver string `json:"driver"`
	Usable bool   `json:"usable"`
	Word   string `json:"word,omitempty"`
	Reason string `json:"reason,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// Result states one driver the way a person reads it: usable or not, and why. It is the only place these
// words are written, so the command and the web UI cannot differ.
func (d DriverManifest) Result() DriverResult {
	result := DriverResult{Driver: d.Driver, Usable: d.usable()}
	switch v := VendorOf(d.Driver); {
	case d.FailedCheck():
		result.Word, result.Reason = WordUnusable, d.Detail
		result.Fix = "Run " + v.Command + " --version to see why, and repair or reinstall " + v.Product + "."
	case !d.Present:
		result.Word, result.Reason = WordAbsent, d.Detail
	case !result.Usable:
		result.Word, result.Reason = WordSignedOut, "no sign-in"
		if d.SignIn.Kind != "" {
			result.Reason = "sign-in " + d.SignIn.Kind
		}
	}
	return result
}

// Results states each driver of a manifest, in the order the worker named them. A manifest that names none
// gives an empty list.
func (m Manifest) Results() []DriverResult {
	results := make([]DriverResult, 0, len(m.Drivers))
	for _, driver := range m.Drivers {
		results = append(results, driver.Result())
	}
	return results
}
