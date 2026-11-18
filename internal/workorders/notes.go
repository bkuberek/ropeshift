package workorders

import (
	"fmt"
	"slices"
	"strings"
)

// NotePoint is the security point a review note is about (FR-PRAC-9).
type NotePoint string

// The security points a review judges in every diff, whatever the criteria say.
const (
	PointSecrets      NotePoint = "secrets"
	PointInjection    NotePoint = "injection"
	PointDependencies NotePoint = "dependencies"
	PointAuthority    NotePoint = "authority"
)

// NotePoints lists the security points, in the order the review brief names them.
func NotePoints() []NotePoint {
	return []NotePoint{PointSecrets, PointInjection, PointDependencies, PointAuthority}
}

const (
	// MaxNotes bounds the notes of one review report.
	MaxNotes = 20
	// MaxNoteReasons bounds the reasons of one note, in bytes.
	MaxNoteReasons = 4096
)

// ReviewNote is one security concern a review lane reports: the point, why, and what it rests on. A note is
// not a verdict. It fails no criterion and changes no outcome, a rework, or a merge decision.
type ReviewNote struct {
	Point     NotePoint `json:"point"`
	Reasons   string    `json:"reasons"`
	Citations []string  `json:"citations,omitempty"`
}

// Validate refuses a note that names no known point, gives no reasons, or gives more than MaxNoteReasons bytes of them.
func (n ReviewNote) Validate() error {
	if !slices.Contains(NotePoints(), n.Point) {
		return fmt.Errorf("%w: note point %q is not secrets, injection, dependencies, or authority", ErrInvalid, n.Point)
	}
	if strings.TrimSpace(n.Reasons) == "" {
		return fmt.Errorf("%w: the %s note gives no reasons", ErrInvalid, n.Point)
	}
	if len(n.Reasons) > MaxNoteReasons {
		return fmt.Errorf("%w: the reasons of the %s note are %d bytes, and the bound is %d", ErrInvalid, n.Point, len(n.Reasons), MaxNoteReasons)
	}
	for _, citation := range n.Citations {
		if strings.TrimSpace(citation) == "" {
			return fmt.Errorf("%w: the %s note has an empty citation", ErrInvalid, n.Point)
		}
	}
	return nil
}

// checkNotes refuses a report with more than MaxNotes notes, or with one that is not whole.
func (r Report) checkNotes() error {
	if len(r.Notes) > MaxNotes {
		return fmt.Errorf("%w: the report has %d notes, and the bound is %d", ErrInvalid, len(r.Notes), MaxNotes)
	}
	for _, n := range r.Notes {
		if err := n.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Note is a review note on record. It is bound to the commit the review read and to the attempt that left it,
// as the review's verdicts are, and Seq is its place in the review's report.
type Note struct {
	ReviewNote
	Review    string `json:"review_id"`
	WorkOrder string `json:"work_order_id"`
	Attempt   int64  `json:"attempt"`
	Commit    string `json:"commit"`
	Seq       int64  `json:"seq"`
	CreatedAt int64  `json:"created_at"`
}

// CountedNotes keeps the notes that were left for the attempt a work order is at and the commit it left. A note
// on any other commit or attempt stays on record and is not shown with the current verdicts.
func CountedNotes(notes []Note, attempt int64, commit string) []Note {
	var out []Note
	if commit == "" {
		return out
	}
	for _, n := range notes {
		if n.Attempt == attempt && n.Commit == commit {
			out = append(out, n)
		}
	}
	return out
}
