package questions

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The choices a decision on a path a lane left out makes (FR-LANE-20): the project keeps the path, so a later lane
// commits it, or it ignores it, so a later lane adds it to the repository's .gitignore.
const (
	LeftFileKeep   = "keep"
	LeftFileIgnore = "ignore"
)

// The words around a left-out path in its question, which ReadLeftFile reads back. The path is quoted, so no name
// can hold them.
const (
	leftFileOpening    = "Is "
	leftFileRepository = " part of the project in repository "
	leftFileClosing    = "? "
)

// leftFileIDDigest is how much of a digest a left-out path's question ID keeps.
const leftFileIDDigest = 16

// LeftFileCommitOption is the answer that keeps a path in the project.
func LeftFileCommitOption(path string) string {
	return "Commit " + strconv.Quote(path) + ": it is part of the project, and a later lane commits it"
}

// LeftFileIgnoreOption is the answer that adds a path to the repository's .gitignore.
func LeftFileIgnoreOption(path string) string {
	return "Ignore " + strconv.Quote(path) + ": a later lane adds it to the repository's .gitignore"
}

// LeftFile is the question a path raises when a lane left it out of its commits because its session did not create
// or edit it. The orchestrator decides it, and no member is asked. Its text names only the repository and the path,
// so the same path in the same repository is the same question: one still open takes no second, and one answered
// is answered from the record, so a path is decided once.
func LeftFile(id, repository, path string) (Question, error) {
	text := leftFileOpening + strconv.Quote(path) + leftFileRepository + repository + leftFileClosing +
		"A lane left it out of its commits, because its session did not create or edit it. Decide it with one of the " +
		"options as the statement: the next lane in the repository acts on it."
	return New(Question{
		ID: id, Text: text, Categories: []string{CategoryFiles},
		Options: []string{LeftFileCommitOption(path), LeftFileIgnoreOption(path)},
	})
}

// LeftFileID names the question one report raises for one path, so the same report read twice names the same one.
func LeftFileID(report, path string) string {
	sum := sha256.Sum256([]byte(report + "\x00" + path))
	return "files-" + hex.EncodeToString(sum[:])[:leftFileIDDigest]
}

// ReadLeftFile reads the repository and the path a left-out path's question names, and says whether it is one.
func ReadLeftFile(q Question) (repository, path string, ok bool) {
	if !slices.Contains(q.Categories, CategoryFiles) {
		return "", "", false
	}
	rest, opened := strings.CutPrefix(q.Text, leftFileOpening)
	quoted, err := strconv.QuotedPrefix(rest)
	if !opened || err != nil {
		return "", "", false
	}
	path, err = strconv.Unquote(quoted)
	rest, named := strings.CutPrefix(rest[len(quoted):], leftFileRepository)
	repository, _, closed := strings.Cut(rest, leftFileClosing)
	if err != nil || !named || !closed || path == "" || repository == "" {
		return "", "", false
	}
	return repository, path, true
}

// LeftFileChoice reads what a statement decides of a left-out path's question: LeftFileKeep, LeftFileIgnore, or
// nothing for a statement that is neither option.
func LeftFileChoice(q Question, statement string) (string, bool) {
	_, path, ok := ReadLeftFile(q)
	switch {
	case !ok:
		return "", false
	case strings.TrimSpace(statement) == LeftFileCommitOption(path):
		return LeftFileKeep, true
	case strings.TrimSpace(statement) == LeftFileIgnoreOption(path):
		return LeftFileIgnore, true
	}
	return "", false
}

// CheckLeftFileRuling holds a ruling on a left-out path's question to one a worker acts on: its statement is one of
// the question's options, and a ruling that names no scope takes the repository's. Any other question passes.
func CheckLeftFileRuling(q Question, record *Decision) error {
	repository, path, ok := ReadLeftFile(q)
	if !ok {
		return nil
	}
	if _, chosen := LeftFileChoice(q, record.Statement); !chosen {
		return fmt.Errorf("%w: a decision on %s states one of its options: %q or %q", ErrInvalid, path,
			LeftFileCommitOption(path), LeftFileIgnoreOption(path))
	}
	if record.Scope == "" {
		record.Scope = Scope{ScopeRepository, repository}.String()
	}
	return nil
}
