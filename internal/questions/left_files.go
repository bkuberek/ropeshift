package questions

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// The two answers to a path a lane left out (FR-LANE-20).
const (
	LeftFileCommit = "Commit it: it is part of the project, and a later lane commits it"
	LeftFileIgnore = "Ignore it: add it to the repository's .gitignore"
)

// leftFileIDDigest is how much of a digest a left-out path's question ID keeps.
const leftFileIDDigest = 16

// LeftFile is the question a path raises when a lane left it out of its commits because its session did not create
// or edit it. The orchestrator decides it, and no member is asked. Its text names only the repository and the path,
// so the same path in the same repository is the same question: one still open takes no second, and one answered
// is answered from the record, so a path is decided once.
func LeftFile(id, repository, path string) (Question, error) {
	text := fmt.Sprintf("Is %q part of the project in repository %s? A lane left it out of its commits, because its "+
		"session did not create or edit it. If it belongs to the project, a later lane commits it. If not, the "+
		"repository's .gitignore gets an entry for it. Decide it with the scope repo:%s, so this path is not asked "+
		"about again.", path, repository, repository)
	return New(Question{ID: id, Text: text, Categories: []string{CategoryFiles}, Options: []string{LeftFileCommit, LeftFileIgnore}})
}

// LeftFileID names the question one report raises for one path, so the same report read twice names the same one.
func LeftFileID(report, path string) string {
	sum := sha256.Sum256([]byte(report + "\x00" + path))
	return "files-" + hex.EncodeToString(sum[:])[:leftFileIDDigest]
}
