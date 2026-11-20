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
	// Before is every file the worktree held changed or untracked when the lane's first session began its first
	// turn, and Began says it was read. A lane with no reading owns every change, as a lane that ran before a
	// worker read it did.
	Before []string
	Began  bool
	// Written is every path the lane's edit and write tools named, relative to the worktree, and AllWritten says
	// no write is missing from it. While one may be, no change is left out, because leaving out a file the
	// lane wrote would lose its work.
	Written    []string
	AllWritten bool
	// Kept are the paths the orchestrator decided are part of the project in this lane's repository, each a file or
	// a folder that ends in `/`. A change at or under one is the lane's own, whoever made it.
	Kept []string
}

// Split divides a survey's changes, one file each, into the lane's own and the rest, each sorted. A file is
// compared whole with what the reading holds, so a new file in a folder that was there before is the lane's own.
func (f LaneFiles) Split(changes []string) (own, left []string) {
	for _, change := range changes {
		if !f.Began || !f.AllWritten || !slices.Contains(f.Before, change) || slices.Contains(f.Written, change) ||
			atOrUnder(f.Kept, change) {
			own = append(own, change)
			continue
		}
		left = append(left, change)
	}
	return sortedSet(own), sortedSet(left)
}

// atOrUnder says a path is one of the paths, or lies in one of them that is a folder.
func atOrUnder(paths []string, change string) bool {
	for _, held := range paths {
		if held == change || strings.HasSuffix(held, "/") && strings.HasPrefix(change, held) {
			return true
		}
	}
	return false
}

// IgnoreCommitSubject is the subject of the commit a worker makes on a lane's branch, before its session starts,
// that adds the paths the orchestrator decided to ignore to the repository's .gitignore.
const IgnoreCommitSubject = "chore: ignore files that are not part of the project"

// IgnoreLine is the .gitignore line that ignores one path a lane left out and nothing else: anchored at the
// repository's top, with each character git would read as a pattern taken literally, and a trailing space kept.
func IgnoreLine(path string) string {
	var line strings.Builder
	line.WriteByte('/')
	for _, c := range path {
		if strings.ContainsRune(`\*?[`, c) {
			line.WriteByte('\\')
		}
		line.WriteRune(c)
	}
	text := line.String()
	if trimmed := strings.TrimRight(text, " "); trimmed != text {
		text = trimmed + strings.Repeat("\\ ", len(text)-len(trimmed))
	}
	return text
}

// ForeignCommits is what a lane, and a member, are told of a push the worker refused (FR-LANE-20): the paths the
// branch's commits beyond its base hold that the lane's session did not make, and how the lane takes them out.
func ForeignCommits(base string, paths []string) string {
	return "ropeshift policy: this lane's commits hold files its session did not create or edit: " + strings.Join(paths, ", ") +
		". Nothing was pushed. Take them out of the commits, such as with git reset --soft " + base + " and new commits " +
		"that name only your own files, then push again. The orchestrator decides whether each belongs to the project."
}

// CollapseLeft is a report's view of the files left out: each file in a folder git tracks nothing in, and that holds
// none of the lane's own files, reads as that folder, the outermost such one, which is what a .gitignore entry
// names. Every other file reads as itself.
func CollapseLeft(left, own, folders []string) []string {
	var out []string
	for _, file := range left {
		named := file
		for _, folder := range folders {
			if strings.HasPrefix(file, folder) && len(folder) < len(named) && !slices.ContainsFunc(own, func(mine string) bool {
				return strings.HasPrefix(mine, folder)
			}) {
				named = folder
			}
		}
		out = append(out, named)
	}
	return sortedSet(out)
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
