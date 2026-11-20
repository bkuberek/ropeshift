package lanes

import (
	"slices"
	"strings"
)

// LaneFiles is what a worker knows of the files a lane's own session made (FR-LANE-20). A lane commits,
// checkpoints, and salvages only these, and reports every other change in its worktree by path.
//
// A session's edit and write tools name what they write, and a file its shell made, through a generator, a
// formatter, or `git mv`, names nothing. So a change counts as the lane's when it was not there yet when the
// lane's session began its first turn: by then the session's own start has run, and a file an agent CLI's setup
// writes there, such as a tool's cache, is already in the worktree. A change that was there counts as the lane's
// only when its tools wrote it. Git's status leaves out every ignored file, so an ignored path, an input root's
// among them, is never a change of either kind.
type LaneFiles struct {
	// Before is what the worktree held changed or untracked when the lane's first session began its first turn,
	// as a survey names it, and Began says it was read. A lane with no reading owns every change, as a lane
	// that ran before a worker read it did.
	Before []string
	Began  bool
	// Written is every path the lane's edit and write tools named, relative to the worktree, and AllWritten says
	// no write is missing from it. While one may be, no change is left out, because leaving out a file the
	// lane wrote would lose its work.
	Written    []string
	AllWritten bool
}

// Split divides a survey's changes into the lane's own and the rest, each sorted. A change that ends in `/` is
// a folder git shows whole, because it holds no tracked file. A write inside a folder that was there before is
// the lane's own, and the folder itself is still left out, so a snapshot takes the one file and nothing else.
func (f LaneFiles) Split(changes []string) (own, left []string) {
	for _, change := range changes {
		if !f.Began || !f.AllWritten || !f.wasThere(change) {
			own = append(own, change)
			continue
		}
		wrote := false
		for _, written := range f.Written {
			if written == change || strings.HasSuffix(change, "/") && strings.HasPrefix(written, change) {
				own = append(own, written)
				wrote = wrote || written == change
			}
		}
		if !wrote {
			left = append(left, change)
		}
	}
	return sortedSet(own), sortedSet(left)
}

// wasThere says a change lies at or under something the worktree held before the first turn.
func (f LaneFiles) wasThere(change string) bool {
	for _, before := range f.Before {
		if before == change || strings.HasSuffix(before, "/") && strings.HasPrefix(change, before) {
			return true
		}
	}
	return false
}

func sortedSet(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// LaneFilesOf reads a lane's files from the digests its worker kept, in any order: every generation's writes, and
// the earliest reading of what the worktree held before a first turn, which a worker takes only in the lane's
// first generation. A digest that missed events, or wrote past the paths it keeps, leaves the writes not all
// known, and so does one that cannot be read.
func LaneFilesOf(records []DigestRecord) LaneFiles {
	files := LaneFiles{AllWritten: true}
	first := int64(0)
	for _, record := range records {
		builder, err := RestoreDigest(record.State)
		if err != nil {
			files.AllWritten = false
			continue
		}
		digest := builder.Digest()
		files.Written = append(files.Written, digest.FilesWritten...)
		if !digest.Complete || digest.FilesWrittenMore > 0 {
			files.AllWritten = false
		}
		if before, began := builder.Before(); began && (first == 0 || record.Generation < first) {
			files.Before, files.Began, first = before, true, record.Generation
		}
	}
	files.Written = sortedSet(files.Written)
	return files
}
